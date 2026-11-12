// Package storagetc68 completes a migration and runs its Cleanup: the source claim is
// marked released and stays exactly where it was, because only the volume's
// purge path may delete it (STORAGE-AC-33, STORAGE-TC-68, F6).
package storagetc68

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/tsic404/kosd/api"
	"github.com/tsic404/kosd/pkg/migration"
	"github.com/tsic404/kosd/tests/acceptance/harness"
)

const (
	namespace   = "data"
	volumeName  = "data"
	sourceClass = "local"
	targetClass = "network"
)

func TestTC68CleanupKeepsTheSourceClaim(t *testing.T) {
	cluster := harness.Start(t)
	world := harness.StartMigrationWorld(t, cluster, namespace, volumeName, sourceClass, targetClass)
	source := cluster.Claim(t, namespace, world.SourceClaim)
	sourceUID := source.GetUID()
	world.RequestMigration(t, cluster, targetClass)

	for range 12 {
		world.Round(t)
		if cluster.ClaimExists(t, namespace, world.TargetClaim) {
			world.BindTarget(t, cluster, targetClass)
		}
		world.CompleteStep(t, cluster, migration.StepCopy)
		world.CompleteStep(t, cluster, migration.StepVerify)
		if world.Phase(t, cluster) == migration.PhaseSucceeded {
			break
		}
	}
	if phase := world.Phase(t, cluster); phase != migration.PhaseSucceeded {
		t.Fatalf("migration phase = %q, want %q before Cleanup is judged", phase, migration.PhaseSucceeded)
	}

	// Cleanup moved the binding: the volume now owns the target claim.
	volume := cluster.GetIn(t, namespace, "v1beta1", "Volume", volumeName)
	bound, _, _ := unstructured.NestedString(volume.Object, "status", "boundClaim")
	if bound != world.TargetClaim {
		t.Fatalf("bound claim = %q, want %q after the migration", bound, world.TargetClaim)
	}

	// The source claim still exists, unchanged, and carries the release marker.
	stored := cluster.Claim(t, namespace, world.SourceClaim)
	if stored.GetUID() != sourceUID {
		t.Fatalf("the source claim was replaced: uid %q, want %q", stored.GetUID(), sourceUID)
	}
	if stored.GetDeletionTimestamp() != nil {
		t.Fatal("the source claim is being deleted; Cleanup may only mark it released")
	}
	if stored.GetLabels()["released"] == "" {
		t.Fatalf("the source claim carries no release marker: %v", stored.GetLabels())
	}

	// Cleanup recorded the release on the source claim's stream, and only once.
	events := releaseEvents(t, cluster, namespace)
	if len(events) != 1 {
		t.Fatalf("Cleanup recorded %d release events, want one", len(events))
	}
	involved := events[0].Object["involvedObject"]
	object, _ := involved.(map[string]any)
	if object["kind"] != "PersistentVolumeClaim" || object["name"] != world.SourceClaim || object["namespace"] != namespace {
		t.Fatalf("the release event names %v, want the source claim %q", involved, world.SourceClaim)
	}

	// A terminal migration deletes nothing further: another round leaves the
	// source claim where it is.
	world.MigrationRound(t)
	stored = cluster.Claim(t, namespace, world.SourceClaim)
	if stored.GetDeletionTimestamp() != nil {
		t.Fatal("a round after Cleanup deleted the source claim")
	}
	if stored.GetLabels()["released"] == "" {
		t.Fatal("a round after Cleanup cleared the release marker")
	}

	// Cleanup removed the flow's own step Jobs and kept the data claim.
	for _, step := range []string{migration.StepCopy, migration.StepVerify} {
		if !cluster.JobGone(t, namespace, world.TargetClaim+"-"+step) {
			t.Fatalf("the %s Job survived Cleanup", step)
		}
	}

	// Re-running Cleanup with the marker already present records no second
	// event: the release is written once (C-40).
	writeMigrationStatus(t, cluster, namespace, world.Volume, map[string]any{"phase": migration.PhaseCleanup})
	world.MigrationRound(t)
	if phase := world.Phase(t, cluster); phase != migration.PhaseSucceeded {
		t.Fatalf("migration phase = %q, want %q after the repeated Cleanup", phase, migration.PhaseSucceeded)
	}
	if again := releaseEvents(t, cluster, namespace); len(again) != 1 {
		t.Fatalf("the repeated Cleanup recorded %d release events, want the original one", len(again))
	}
}

// releaseEvents lists the events the flow recorded for releasing a source
// claim.
func releaseEvents(t *testing.T, cluster *harness.Cluster, namespace string) []unstructured.Unstructured {
	t.Helper()
	list, err := cluster.Direct(t, "", "v1", "Event", namespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list events in %q: %v", namespace, err)
	}
	events := make([]unstructured.Unstructured, 0, len(list.Items))
	for _, item := range list.Items {
		if reason, _, _ := unstructured.NestedString(item.Object, "reason"); reason == migration.EventSourceReleased {
			events = append(events, item)
		}
	}
	return events
}

// writeMigrationStatus rewrites the status of one migration, standing in for a
// write the flow would have made itself. It applies under the field manager the
// migration reconciler uses and carries the whole status, so the field
// ownership stays where a real round would have put it: a foreign manager would
// own these fields and starve the reconciler with a conflict (C-05).
func writeMigrationStatus(t *testing.T, cluster *harness.Cluster, namespace, name string, fields map[string]any) {
	t.Helper()
	ctx := context.Background()
	resource := cluster.Direct(t, api.Group, "v1beta1", "VolumeMigration", namespace)
	stored, err := resource.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("read migration %q: %v", name, err)
	}
	status, _, err := unstructured.NestedMap(stored.Object, "status")
	if err != nil {
		t.Fatalf("read migration %q status: %v", name, err)
	}
	for key, value := range fields {
		status[key] = value
	}
	document := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": api.Group + "/v1beta1",
		"kind":       "VolumeMigration",
		"metadata":   map[string]any{"name": name, "namespace": namespace},
		"status":     status,
	}}
	if _, err := resource.ApplyStatus(ctx, name, document, metav1.ApplyOptions{FieldManager: "kos-migration-reconciler"}); err != nil {
		t.Fatalf("apply migration %q status: %v", name, err)
	}
}
