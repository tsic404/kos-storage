package storage

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/tsic404/kosd/api/v1beta1"
	"github.com/tsic404/kosd/pkg/extension"
	"github.com/tsic404/kosd/pkg/identity"
	"github.com/tsic404/kosd/pkg/migration"
	"github.com/tsic404/kosd/pkg/pages"
	volumeapi "github.com/tsic404/kosd/pkg/volume"
	"github.com/tsic404/kosd/pkg/write"
)

// TestDeclarationLoads requires the storage page to pass the framework's own
// declaration checks, so the page is declarable without a cluster.
func TestDeclarationLoads(t *testing.T) {
	loaded, err := Declaration().Load()
	if err != nil {
		t.Fatalf("load the declaration: %v", err)
	}
	if len(loaded.Problems) > 0 {
		t.Fatalf("the declaration has problems: %+v", loaded.Problems)
	}
	if len(loaded.Contributions.Pages) != 1 {
		t.Fatalf("pages = %d, want the one storage page", len(loaded.Contributions.Pages))
	}
	page := loaded.Contributions.Pages[0]
	if page.ID != PageStorage {
		t.Fatalf("page id = %q, want %q", page.ID, PageStorage)
	}
}

// TestPageDeclaresSourcesAndEntries requires every display item to name its
// source, every read-only item its reason, and the page exactly its deletion
// and migration entries (STORAGE-C-1, STORAGE-C-82).
func TestPageDeclaresSourcesAndEntries(t *testing.T) {
	page := storagePage()
	items := map[string]extension.PageItem{}
	for _, item := range page.Items {
		items[item.ID] = item
		if item.Source == "" || !extension.IsPageSource(item.Source) {
			t.Errorf("item %q source = %q, want a page source type", item.ID, item.Source)
		}
		if _, _, ok := pages.ReferencePrefix(item.Reference); !ok || item.Reference != reference(item.ID) {
			t.Errorf("item %q reference = %q, want the product-owned reference", item.ID, item.Reference)
		}
		if item.Action == "" && item.Reason == "" {
			t.Errorf("item %q carries neither an entry nor a read-only reason", item.ID)
		}
	}
	for _, want := range []struct {
		id          string
		source      string
		action      string
		hasReadOnly bool
	}{
		{id: ItemDevicesAndPolicies, source: extension.PageSourceObject, hasReadOnly: true},
		{id: ItemVolumes, source: extension.PageSourceObject, hasReadOnly: true},
		{id: ItemUsage, source: extension.PageSourceMetric, hasReadOnly: true},
		{id: ItemBackups, source: extension.PageSourceObject, hasReadOnly: true},
		{id: ItemDeletion, source: extension.PageSourceObject, action: ActionDeleteVolume},
		{id: ItemMigration, source: extension.PageSourceObject, action: ActionStartMigration},
	} {
		item, known := items[want.id]
		if !known {
			t.Fatalf("page is missing item %q", want.id)
		}
		if item.Source != want.source {
			t.Errorf("item %q source = %q, want %q", want.id, item.Source, want.source)
		}
		if item.Action != want.action {
			t.Errorf("item %q action = %q, want %q", want.id, item.Action, want.action)
		}
		if want.hasReadOnly && item.Reason == "" {
			t.Errorf("item %q carries no read-only reason", want.id)
		}
	}
	actions := map[string]extension.Action{}
	for _, action := range page.Actions {
		actions[action.ID] = action
	}
	if len(actions) != 2 {
		t.Fatalf("actions = %v, want the deletion and migration entries only", page.Actions)
	}
	for _, id := range []string{ActionDeleteVolume, ActionStartMigration} {
		if _, known := actions[id]; !known {
			t.Errorf("page declares no action %q", id)
		}
	}
}

// TestGuestGetsNoEntryTargets requires the readings to offer no delete or
// migration target to a role that may not write volumes (STORAGE-AC-1,
// KOSD-C-59).
func TestGuestGetsNoEntryTargets(t *testing.T) {
	readings := &Readings{}
	session := identitySession(write.RoleGuest)
	for _, item := range []string{ItemDeletion, ItemMigration} {
		value, failure := readings.value(context.Background(), session, extension.PageItem{ID: item})
		if failure != nil {
			t.Fatalf("read item %q: %v", item, failure)
		}
		switch typed := value.(type) {
		case []deletionTargetView:
			if len(typed) > 0 {
				t.Fatalf("item %q offers %d deletion targets to a Guest", item, len(typed))
			}
		case []migrationTargetView:
			if len(typed) > 0 {
				t.Fatalf("item %q offers %d migration targets to a Guest", item, len(typed))
			}
		}
	}
}

// TestMigrationEntryCondition requires the entry condition of STORAGE-C-81:
// the volume is bound and its desired policy differs from the one its binding
// resolved.
func TestMigrationEntryCondition(t *testing.T) {
	for _, test := range []struct {
		name   string
		volume *v1beta1.Volume
		actual string
		want   bool
	}{
		{
			name:   "bound with another resolved policy",
			volume: volumeWith(volumeapi.PhaseBound, "", "network"),
			actual: "local",
			want:   true,
		},
		{
			name:   "bound with the same resolved policy",
			volume: volumeWith(volumeapi.PhaseBound, "", "local"),
			actual: "local",
			want:   false,
		},
		{
			name:   "not bound",
			volume: volumeWith(volumeapi.PhasePending, "", "network"),
			actual: "local",
			want:   false,
		},
		{
			name:   "no resolved policy",
			volume: volumeWith(volumeapi.PhaseBound, "", "network"),
			actual: "",
			want:   false,
		},
		{
			name:   "no desired policy",
			volume: volumeWith(volumeapi.PhaseBound, "", ""),
			actual: "local",
			want:   false,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := isMigrationCandidate(*test.volume, test.actual); got != test.want {
				t.Fatalf("candidate = %v, want %v", got, test.want)
			}
		})
	}
}

// TestRollbackOutcomes requires the page to report the rollback result
// STORAGE-C-81 makes observable.
func TestRollbackOutcomes(t *testing.T) {
	for _, test := range []struct {
		name   string
		volume *v1beta1.Volume
		stored v1beta1.VolumeMigration
		want   string
	}{
		{
			name:   "rolled back to the source claim",
			volume: volumeWith(volumeapi.PhaseBound, "local", "network"),
			stored: failedMigration(migration.PhaseCutover, "target never bound"),
			want:   RollbackRolledBack,
		},
		{
			name:   "preflight needed no rollback",
			volume: volumeWith(volumeapi.PhaseBound, "local", "local"),
			stored: failedMigration(migration.PhasePreflight, "target class missing"),
			want:   RollbackNotNeeded,
		},
		{
			name: "binding did not return",
			volume: &v1beta1.Volume{
				ObjectMeta: metav1.ObjectMeta{Namespace: "data", Name: "data"},
				Status:     v1beta1.VolumeStatus{ObjectStatus: v1beta1.ObjectStatus{Phase: volumeapi.PhaseBound}, BoundClaim: "data-target"},
			},
			stored: failedMigration(migration.PhaseCutover, "target never bound"),
			want:   RollbackFailed,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			rollback := rollbackOf(*test.volume, test.stored)
			if rollback == nil {
				t.Fatal("a failed attempt reports no rollback")
			}
			if rollback.Outcome != test.want {
				t.Fatalf("rollback outcome = %q, want %q", rollback.Outcome, test.want)
			}
			if rollback.Reason == "" {
				t.Fatal("the rollback carries no reason")
			}
		})
	}
}

// TestSharedRWXCrashesSnapshotConsistency requires the computed consistency of
// KOSD-C-38: a shared read-write volume is only crash-consistent.
func TestSharedRWXCrashesSnapshotConsistency(t *testing.T) {
	volume := v1beta1.Volume{
		Spec: v1beta1.VolumeSpec{Mobility: v1beta1.VolumeMobilitySharedRWX},
		Status: v1beta1.VolumeStatus{
			Capabilities: []v1beta1.VolumeCapability{{Name: "storage.shared-rwx", Version: "v1"}},
		},
	}
	if got := snapshotOf(volume).Consistency; got != volumeapi.SnapshotConsistencyCrash {
		t.Fatalf("consistency = %q, want %q", got, volumeapi.SnapshotConsistencyCrash)
	}
}

// TestUsageWithoutMetricSource requires the unmeasurable answer of
// STORAGE-C-1 instead of allocated capacity presented as usage.
func TestUsageWithoutMetricSource(t *testing.T) {
	value, failure := (&Readings{}).usage(context.Background())
	if failure != nil {
		t.Fatalf("read usage: %v", failure)
	}
	answer, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("usage = %v, want an object", value)
	}
	if answer["available"] != false || answer["reason"] == "" {
		t.Fatalf("usage = %v, want an unmeasurable answer with a reason", answer)
	}
}

// identitySession is one signed-in session at the given role.
func identitySession(role write.Role) identity.Session {
	return identity.Session{Username: "alice", Role: role}
}

// volumeWith builds a volume whose recorded policy is actual and whose desired
// policy is desired.
func volumeWith(phase, actual, desired string) *v1beta1.Volume {
	return &v1beta1.Volume{
		ObjectMeta: metav1.ObjectMeta{Namespace: "data", Name: "data"},
		Spec:       v1beta1.VolumeSpec{StorageClass: desired},
		Status: v1beta1.VolumeStatus{
			ObjectStatus: v1beta1.ObjectStatus{Phase: phase},
			StorageClass: actual, BoundClaim: "data",
		},
	}
}

// failedMigration builds a failed attempt that started from the data claim.
func failedMigration(phase, message string) v1beta1.VolumeMigration {
	return v1beta1.VolumeMigration{
		ObjectMeta: metav1.ObjectMeta{Namespace: "data", Name: "data"},
		Spec:       v1beta1.VolumeMigrationSpec{Volume: "data", TargetStorageClass: "network"},
		Status: v1beta1.VolumeMigrationStatus{
			ObjectStatus: v1beta1.ObjectStatus{Phase: migration.PhaseFailed}, SourceClaim: "data",
			Failure: &v1beta1.MigrationFailure{Phase: phase, Message: message},
		},
	}
}
