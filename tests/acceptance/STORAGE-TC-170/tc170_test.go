// Package storagetc170 reads three volumes the real volume controller bound:
// the migration entry appears for the bound volume whose desired policy moved
// away from the one its claim resolved, and for neither of the other two
// (STORAGE-AC-79, STORAGE-C-81, STORAGE-TC-170). The bound policy is read from
// the claim's PersistentVolume, because the volume controller never writes
// status.storageClass before a migration.
package storagetc170

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/tsic404/kosd/api"
	"github.com/tsic404/kosd/pkg/volume"
	"github.com/tsic404/kosd/tests/acceptance/harness"

	"github.com/tsic404/kos-storage/pkg/storage"
	"github.com/tsic404/kos-storage/tests/acceptance/page"
)

const (
	namespace     = "data"
	ownerName     = "owner"
	ownerPassword = "owner-password"
)

func TestTC170MigrationEntryAppearsOnlyWhenThePoliciesDiffer(t *testing.T) {
	cluster := harness.Start(t)
	cluster.EnsureNamespace(t, namespace)
	shell, _ := page.Start(t, cluster)
	owner := shell.SignInOwner(t, ownerName, ownerPassword)

	for _, class := range []struct{ name, provisioner string }{
		{name: "local", provisioner: "local.example"},
		{name: "network", provisioner: "network.example"},
	} {
		cluster.StorageClass(t, class.name, class.provisioner, true, "Retain", nil)
	}
	controller := cluster.VolumeController(t, volume.Options{})

	// The desired policy already equals the one the claim resolved.
	createVolume(t, cluster, "same-policy", "network")
	harness.ReconcileVolume(t, controller, namespace, "same-policy")
	cluster.PersistentVolume(t, "pv-same", "network", "network.example", "Retain")
	cluster.BindClaim(t, namespace, "same-policy", "pv-same")
	harness.ReconcileVolume(t, controller, namespace, "same-policy")

	// The user moved the desired policy after the volume bound to local.
	createVolume(t, cluster, "other-policy", "local")
	harness.ReconcileVolume(t, controller, namespace, "other-policy")
	cluster.PersistentVolume(t, "pv-other", "local", "local.example", "Retain")
	cluster.BindClaim(t, namespace, "other-policy", "pv-other")
	harness.ReconcileVolume(t, controller, namespace, "other-policy")
	declarePolicy(t, cluster, "other-policy", "network")
	harness.ReconcileVolume(t, controller, namespace, "other-policy")

	// The desired policy differs, but the volume is not bound yet.
	createVolume(t, cluster, "unbound", "network")
	harness.ReconcileVolume(t, controller, namespace, "unbound")

	rendered := page.Read(t, shell, owner, storage.PageStorage)
	targets := page.List(t, rendered, storage.ItemMigration)
	if len(targets) != 1 {
		t.Fatalf("migration targets = %v, want only the bound volume behind its desired policy", targets)
	}
	target := targets[0]
	if harness.String(target, "name") != "other-policy" {
		t.Fatalf("migration target = %v, want other-policy", target)
	}
	if harness.String(target, "targetStorageClass") != "network" {
		t.Fatalf("target policy = %q, want the volume's desired policy", harness.String(target, "targetStorageClass"))
	}
	if harness.String(target, "actualStorageClass") != "local" {
		t.Fatalf("actual policy = %q, want the policy the claim resolved", harness.String(target, "actualStorageClass"))
	}
	if window, ok := target["window"].(map[string]any); !ok || window["isRequired"] != true {
		t.Fatalf("downtime window = %v, want the user-supplied window", target["window"])
	}

	volumes := page.Records(t, page.Object(t, rendered, storage.ItemVolumes), "volumes")
	bound := page.Find(t, volumes, "other-policy")
	if harness.String(bound, "actualStorageClass") != "local" {
		t.Fatalf("volume actual policy = %q, want the policy its claim resolved", harness.String(bound, "actualStorageClass"))
	}
	unbound := page.Find(t, volumes, "unbound")
	if harness.String(unbound, "phase") != "Pending" {
		t.Fatalf("unbound volume = %v, want the controller's Pending phase", unbound)
	}
}

// createVolume stores one volume the volume controller binds; the caller
// declares the policy the volume asks for.
func createVolume(t *testing.T, cluster *harness.Cluster, name, desired string) {
	t.Helper()
	cluster.CreateIn(t, namespace, harness.Volume(namespace, name, map[string]any{
		"mobility": "cluster-movable", "capacity": "1Gi", "storageClass": desired,
	}))
}

// declarePolicy writes the policy the user wants after the volume bound.
func declarePolicy(t *testing.T, cluster *harness.Cluster, name, desired string) {
	t.Helper()
	resource := cluster.Direct(t, api.Group, "v1beta1", "Volume", namespace)
	stored, err := resource.Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("read volume %q: %v", name, err)
	}
	if err := unstructured.SetNestedField(stored.Object, desired, "spec", "storageClass"); err != nil {
		t.Fatalf("write the declared policy of volume %q: %v", name, err)
	}
	if _, err := resource.Update(context.Background(), stored, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("update volume %q: %v", name, err)
	}
}
