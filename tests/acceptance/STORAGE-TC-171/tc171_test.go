// Package storagetc171 injects a failure in the Cutover stage of a migration
// and reads the storage page: the six-stage order, the failed stage with its
// reason and the rollback result are readable, and the page offers no entry to
// trigger a rollback (STORAGE-AC-79, STORAGE-C-81, STORAGE-TC-171).
package storagetc171

import (
	"strings"
	"testing"
	"time"

	"github.com/tsic404/kosd/pkg/migration"
	"github.com/tsic404/kosd/tests/acceptance/harness"

	"github.com/tsic404/kos-storage/pkg/storage"
	"github.com/tsic404/kos-storage/tests/acceptance/page"
)

const (
	namespace     = "data"
	volumeName    = "data"
	sourceClass   = "local"
	targetClass   = "network"
	ownerName     = "owner"
	ownerPassword = "owner-password"
)

func TestTC171StageFailureAndRollbackAreReadable(t *testing.T) {
	cluster := harness.Start(t)
	world := harness.StartMigrationWorld(t, cluster, namespace, volumeName, sourceClass, targetClass)
	world.RequestMigration(t, cluster, targetClass)

	// The target claim never binds, so Cutover runs out of its window.
	for range 10 {
		world.Round(t)
		world.CompleteStep(t, cluster, migration.StepCopy)
		world.CompleteStep(t, cluster, migration.StepVerify)
		if world.Phase(t, cluster) == migration.PhaseCutover {
			world.Clock.Advance(31 * time.Minute)
		}
		if world.Phase(t, cluster) == migration.PhaseFailed {
			break
		}
	}
	if phase := world.Phase(t, cluster); phase != migration.PhaseFailed {
		t.Fatalf("migration phase = %q, want %q after the injected failure", phase, migration.PhaseFailed)
	}

	shell, _ := page.Start(t, cluster)
	owner := shell.SignInOwner(t, ownerName, ownerPassword)
	rendered := page.Read(t, shell, owner, storage.PageStorage)

	volumes := page.Records(t, page.Object(t, rendered, storage.ItemVolumes), "volumes")
	record := page.Find(t, volumes, volumeName)
	attempt, ok := record["migration"].(map[string]any)
	if !ok {
		t.Fatalf("volume = %v, want the failed attempt readable", record)
	}
	stages, ok := attempt["stages"].([]any)
	if !ok {
		t.Fatalf("stages = %v, want the six-stage order", attempt["stages"])
	}
	wantStages := []string{"Preflight", "Quiesce", "Copy", "Verify", "Cutover", "Cleanup"}
	if len(stages) != len(wantStages) {
		t.Fatalf("stages = %v, want %v", stages, wantStages)
	}
	for index, want := range wantStages {
		if stages[index] != want {
			t.Fatalf("stage %d = %v, want %q", index, stages[index], want)
		}
	}
	if harness.String(attempt, "phase") != migration.PhaseFailed {
		t.Fatalf("phase = %q, want %q", harness.String(attempt, "phase"), migration.PhaseFailed)
	}
	failure, ok := attempt["failure"].(map[string]any)
	if !ok {
		t.Fatalf("attempt = %v, want the failed stage and its reason", attempt)
	}
	if harness.String(failure, "stage") != migration.PhaseCutover {
		t.Fatalf("failure stage = %q, want %q", harness.String(failure, "stage"), migration.PhaseCutover)
	}
	if harness.String(failure, "reason") == "" {
		t.Fatal("the failed stage carries no reason")
	}
	rollback, ok := attempt["rollback"].(map[string]any)
	if !ok {
		t.Fatalf("attempt = %v, want the rollback result", attempt)
	}
	if harness.String(rollback, "outcome") != storage.RollbackRolledBack {
		t.Fatalf("rollback outcome = %q, want %q", harness.String(rollback, "outcome"), storage.RollbackRolledBack)
	}
	if harness.String(rollback, "restoredClaim") != world.SourceClaim {
		t.Fatalf("restored claim = %q, want the source claim %q", harness.String(rollback, "restoredClaim"), world.SourceClaim)
	}
	if harness.String(attempt, "rollbackPoint") != "" {
		t.Fatalf("rollback point = %q, want it cleared after the rollback", harness.String(attempt, "rollbackPoint"))
	}

	actions := page.Actions(t, rendered)
	if len(actions) != 2 {
		t.Fatalf("actions = %v, want only the deletion and migration entries", actions)
	}
	for id, action := range actions {
		entry, _ := action["entry"].(map[string]any)
		if strings.Contains(strings.ToLower(id+harness.String(entry, "path")), "rollback") {
			t.Fatalf("action %q offers a rollback entry: %v", id, action)
		}
	}
}
