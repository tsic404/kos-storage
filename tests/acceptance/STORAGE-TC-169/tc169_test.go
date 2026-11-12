// Package storagetc169 reads the read-only items of the storage page over a
// failed provider, an unhealthy shared volume and a backup policy with a
// drill: every item carries its source and a read-only reason, the stale
// marker follows the observation time, and the page offers no backup, restore,
// drill or snapshot entry (STORAGE-AC-1, STORAGE-C-82, STORAGE-TC-169).
package storagetc169

import (
	"strings"
	"testing"
	"time"

	"github.com/tsic404/kosd/pkg/extension"
	"github.com/tsic404/kosd/tests/acceptance/harness"

	"github.com/tsic404/kos-storage/pkg/storage"
	"github.com/tsic404/kos-storage/tests/acceptance/fixtures"
	"github.com/tsic404/kos-storage/tests/acceptance/page"
)

const (
	namespace     = "data"
	ownerName     = "owner"
	ownerPassword = "owner-password"
)

func TestTC169ReadOnlyItemsAndEntries(t *testing.T) {
	cluster := harness.Start(t)
	cluster.EnsureNamespace(t, namespace)
	shell, readings := page.Start(t, cluster)
	owner := shell.SignInOwner(t, ownerName, ownerPassword)

	cluster.Create(t, fixtures.Provider("local-path", []string{"local", "network"}, false))
	cluster.Create(t, fixtures.DeviceFact("node-a"))
	cluster.CreateIn(t, namespace, fixtures.BackupPolicy(namespace, "nightly"))
	cluster.StorageClass(t, "local", "local.example", true, "Retain", nil)
	cluster.CreateIn(t, namespace, fixtures.Volume(namespace, "data", ownerName, map[string]any{
		"mobility": "shared-rwx", "capacity": "1Gi", "storageClass": "network",
	}, map[string]any{
		"phase": "Bound", "health": map[string]any{"state": "Unavailable"},
		"boundClaim": "data", "storageClass": "local", "reclaimPolicy": "Retain",
		"capabilities": []any{
			map[string]any{"name": "storage.volume", "version": "v1"},
			map[string]any{"name": "storage.shared-rwx", "version": "v1"},
			map[string]any{"name": "storage.snapshot", "version": "v1"},
		},
	}))

	rendered := page.Read(t, shell, owner, storage.PageStorage)
	page.RequireSources(t, rendered, map[string]string{
		storage.ItemDevicesAndPolicies: extension.PageSourceObject,
		storage.ItemVolumes:            extension.PageSourceObject,
		storage.ItemUsage:              extension.PageSourceMetric,
		storage.ItemBackups:            extension.PageSourceObject,
		storage.ItemDeletion:           extension.PageSourceObject,
		storage.ItemMigration:          extension.PageSourceObject,
	})
	for _, id := range []string{storage.ItemDevicesAndPolicies, storage.ItemVolumes, storage.ItemUsage, storage.ItemBackups} {
		if reason := harness.String(page.Item(t, rendered, id), "reason"); reason == "" {
			t.Errorf("read-only item %q carries no reason", id)
		}
	}

	policies := page.Object(t, rendered, storage.ItemDevicesAndPolicies)
	providers := page.Records(t, policies, "providers")
	if len(providers) < 1 {
		t.Fatalf("providers = %v, want the stored providers", providers)
	}
	failed := page.Find(t, providers, "local-path")
	if failed["verified"] != false || failed["ready"] != false {
		t.Fatalf("provider = %v, want the failed verification readable", failed)
	}
	if !conditionFailed(t, failed, "Verified") {
		t.Fatalf("provider conditions = %v, want Verified=False with its reason", failed["conditions"])
	}
	if readOnly := page.Records(t, failed, "readOnly"); len(readOnly) == 0 {
		t.Fatalf("provider = %v, want its read-only reasons", failed)
	}

	volumes := page.Records(t, page.Object(t, rendered, storage.ItemVolumes), "volumes")
	volume := page.Find(t, volumes, "data")
	if harness.String(volume, "health") != "Unavailable" {
		t.Fatalf("volume health = %q, want Unavailable", harness.String(volume, "health"))
	}
	snapshot, ok := volume["snapshot"].(map[string]any)
	if !ok {
		t.Fatalf("volume = %v, want its snapshot facts", volume)
	}
	capabilities := page.Records(t, snapshot, "capabilities")
	if len(capabilities) != 3 || !hasCapability(capabilities, "storage.snapshot") {
		t.Fatalf("snapshot capabilities = %v, want the recorded set including snapshots", capabilities)
	}
	if harness.String(snapshot, "consistency") != "crash-consistent" {
		t.Fatalf("consistency = %q, want crash-consistent for a shared read-write volume", harness.String(snapshot, "consistency"))
	}
	if readOnly := page.Records(t, volume, "readOnly"); len(readOnly) == 0 {
		t.Fatalf("volume = %v, want its read-only reasons", volume)
	}

	backups := page.Records(t, page.Object(t, rendered, storage.ItemBackups), "policies")
	if len(backups) != 1 {
		t.Fatalf("backup policies = %v, want the one stored policy", backups)
	}
	if drill, ok := backups[0]["lastDrill"].(map[string]any); !ok || drill["succeeded"] != true {
		t.Fatalf("backup = %v, want the newest drill readable", backups[0])
	}

	actions := page.Actions(t, rendered)
	if len(actions) != 2 {
		t.Fatalf("actions = %v, want only the deletion and migration entries", actions)
	}
	for id, action := range actions {
		entry, _ := action["entry"].(map[string]any)
		path := harness.String(entry, "path")
		for _, forbidden := range []string{"Backup", "Restore", "Snapshot", "Provider"} {
			if strings.Contains(path, forbidden) {
				t.Errorf("action %q writes %q, but %s has no entry on this page", id, path, forbidden)
			}
		}
		if !strings.HasPrefix(path, "/api/v1/resources/Volume") {
			t.Errorf("action %q path = %q, want a volume write", id, path)
		}
	}

	// A source observed beyond the freshness bound makes the item stale
	// (KOSD-C-13): the observation stays put while the read clock moves on.
	observed := shell.Accounts.Clock.Now()
	readings.Now = func() time.Time { return observed }
	shell.Accounts.Clock.Advance(3 * time.Minute)
	stale := page.Read(t, shell, owner, storage.PageStorage)
	if page.Item(t, stale, storage.ItemVolumes)["stale"] != true {
		t.Fatalf("a projection observed three minutes ago is not marked stale: %v", page.Item(t, stale, storage.ItemVolumes))
	}
}

// conditionFailed reports whether one provider condition is present as False
// with a reason.
func conditionFailed(t *testing.T, provider map[string]any, conditionType string) bool {
	t.Helper()
	for _, raw := range page.Records(t, provider, "conditions") {
		if harness.String(raw, "type") == conditionType {
			return harness.String(raw, "status") == "False" && harness.String(raw, "reason") != ""
		}
	}
	return false
}

// hasCapability reports whether one capability list names a capability.
func hasCapability(capabilities []map[string]any, name string) bool {
	for _, capability := range capabilities {
		if harness.String(capability, "name") == name {
			return true
		}
	}
	return false
}
