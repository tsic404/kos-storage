// Package storagetc1 reads the storage page over a cluster that holds a local
// volume, a shared volume with a migration in flight and a volume whose
// desired policy differs from the recorded one. Every item is readable with its
// source, usage is unmeasurable without a metric source, and a member without
// write permission sees no deletion or migration target
// (STORAGE-AC-1, STORAGE-C-1, STORAGE-C-82, STORAGE-TC-1).
package storagetc1

import (
	"testing"

	"github.com/tsic404/kosd/api/v1beta1"
	"github.com/tsic404/kosd/pkg/extension"
	"github.com/tsic404/kosd/pkg/write"
	"github.com/tsic404/kosd/tests/acceptance/harness"

	"github.com/tsic404/kos-storage/pkg/storage"
	"github.com/tsic404/kos-storage/tests/acceptance/fixtures"
	"github.com/tsic404/kos-storage/tests/acceptance/page"
)

const (
	namespace      = "data"
	ownerName      = "owner"
	ownerPassword  = "owner-password"
	memberName     = "member"
	memberPassword = "member-password"
	guestName      = "guest"
	guestPassword  = "guest-password"
)

func TestTC1StoragePageReadsEverySource(t *testing.T) {
	cluster := harness.Start(t)
	cluster.EnsureNamespace(t, namespace)
	shell, _ := page.Start(t, cluster)
	owner := shell.SignInOwner(t, ownerName, ownerPassword)

	for _, class := range []struct{ name, provisioner string }{
		{name: "local", provisioner: "local.example"},
		{name: "shared", provisioner: "shared.example"},
		{name: "network", provisioner: "network.example"},
	} {
		cluster.StorageClass(t, class.name, class.provisioner, true, "Retain", map[string]string{"fsType": "ext4"})
	}
	cluster.Create(t, fixtures.Provider("local-path", []string{"local", "shared", "network"}, true))
	cluster.Create(t, fixtures.DeviceFact("node-a"))
	cluster.CreateIn(t, namespace, fixtures.BackupPolicy(namespace, "nightly"))

	createVolume(t, cluster, "local-data", ownerName, map[string]any{
		"mobility": string(v1beta1.VolumeMobilityClusterMovable), "capacity": "1Gi",
		"storageClass": "local",
	}, map[string]any{"purgeToken": "local-data"}, map[string]any{
		"phase": "Bound", "health": map[string]any{"state": "Healthy"},
		"boundClaim": "local-data", "storageClass": "local",
		"provider": map[string]any{"name": "local-path", "version": "1.0.0"}, "reclaimPolicy": "Retain",
	})
	createVolume(t, cluster, "shared-media", ownerName, map[string]any{
		"mobility": string(v1beta1.VolumeMobilitySharedRWX), "capacity": "2Gi",
		"storageClass": "network",
	}, nil, map[string]any{
		"phase": "Bound", "health": map[string]any{"state": "Healthy"},
		"boundClaim": "shared-media", "storageClass": "shared",
		"provider": map[string]any{"name": "shared-path", "version": "1.0.0"}, "reclaimPolicy": "Retain",
		"capabilities": []any{
			map[string]any{"name": "storage.volume", "version": "v1"},
			map[string]any{"name": "storage.shared-rwx", "version": "v1"},
			map[string]any{"name": "storage.snapshot", "version": "v1"},
		},
	})
	cluster.CreateIn(t, namespace, fixtures.Migration(namespace, "shared-media", "network", "Copy", true))
	createVolume(t, cluster, "movable", ownerName, map[string]any{
		"mobility": string(v1beta1.VolumeMobilityClusterMovable), "capacity": "1Gi",
		"storageClass": "network",
	}, nil, map[string]any{
		"phase": "Bound", "health": map[string]any{"state": "Healthy"},
		"boundClaim": "movable", "storageClass": "local", "reclaimPolicy": "Retain",
	})
	createVolume(t, cluster, "member-data", memberName, map[string]any{
		"mobility": string(v1beta1.VolumeMobilityClusterMovable), "capacity": "1Gi",
		"storageClass": "network",
	}, nil, map[string]any{
		"phase": "Bound", "health": map[string]any{"state": "Healthy"},
		"boundClaim": "member-data", "storageClass": "local", "reclaimPolicy": "Retain",
	})

	rendered := page.Read(t, shell, owner, storage.PageStorage)
	page.RequireSources(t, rendered, map[string]string{
		storage.ItemDevicesAndPolicies: extension.PageSourceObject,
		storage.ItemVolumes:            extension.PageSourceObject,
		storage.ItemUsage:              extension.PageSourceMetric,
		storage.ItemBackups:            extension.PageSourceObject,
		storage.ItemDeletion:           extension.PageSourceObject,
		storage.ItemMigration:          extension.PageSourceObject,
	})

	policies := page.Object(t, rendered, storage.ItemDevicesAndPolicies)
	if devices := page.Records(t, policies, "devices"); len(devices) != 1 {
		t.Fatalf("devices = %v, want the one stored fact", devices)
	}
	if classes := page.Records(t, policies, "storageClasses"); len(classes) != 3 {
		t.Fatalf("storage classes = %v, want the three stored classes", classes)
	}
	providers := page.Records(t, policies, "providers")
	if len(providers) < 1 {
		t.Fatalf("providers = %v, want the registered provider", providers)
	}
	registered := page.Find(t, providers, "local-path")
	if registered["registered"] != true || registered["verified"] != true || registered["ready"] != true {
		t.Fatalf("provider registered=%v verified=%v ready=%v, want a registered, verified and ready provider",
			registered["registered"], registered["verified"], registered["ready"])
	}

	volumes := page.Records(t, page.Object(t, rendered, storage.ItemVolumes), "volumes")
	if len(volumes) != 4 {
		t.Fatalf("volumes = %v, want the four stored volumes", volumes)
	}
	local := page.Find(t, volumes, "local-data")
	if harness.String(local, "phase") != "Bound" || harness.String(local, "health") != "Healthy" {
		t.Fatalf("local volume = %v, want its phase and health", local)
	}
	shared := page.Find(t, volumes, "shared-media")
	snapshot, ok := shared["snapshot"].(map[string]any)
	if !ok {
		t.Fatalf("shared volume carries no snapshot facts: %v", shared)
	}
	if harness.String(snapshot, "consistency") != "crash-consistent" {
		t.Fatalf("shared volume consistency = %q, want crash-consistent", harness.String(snapshot, "consistency"))
	}
	if capabilities := page.Records(t, snapshot, "capabilities"); len(capabilities) != 3 {
		t.Fatalf("snapshot capabilities = %v, want the recorded capability set", capabilities)
	}
	migration, ok := shared["migration"].(map[string]any)
	if !ok {
		t.Fatalf("shared volume carries no migration: %v", shared)
	}
	stages, ok := migration["stages"].([]any)
	if !ok || len(stages) != 6 {
		t.Fatalf("migration stages = %v, want the six-stage order", migration["stages"])
	}
	if harness.String(migration, "currentStage") != "Copy" {
		t.Fatalf("current stage = %q, want Copy", harness.String(migration, "currentStage"))
	}
	if harness.String(migration, "rollbackPoint") != "Copy" {
		t.Fatalf("rollback point = %q, want Copy during the attempt", harness.String(migration, "rollbackPoint"))
	}
	if duration := harness.String(migration, "window", "duration"); duration != "30m" {
		t.Fatalf("downtime window = %q, want the allowed 30m readable", duration)
	}

	usage := page.Object(t, rendered, storage.ItemUsage)
	if usage["available"] != false || harness.String(usage, "reason") == "" {
		t.Fatalf("usage = %v, want an unmeasurable answer with a reason", usage)
	}
	backups := page.Records(t, page.Object(t, rendered, storage.ItemBackups), "policies")
	if len(backups) != 1 {
		t.Fatalf("backup policies = %v, want the one stored policy", backups)
	}
	if lastBackup, ok := backups[0]["lastBackup"].(map[string]any); !ok || lastBackup["succeeded"] != true {
		t.Fatalf("backup = %v, want the newest successful run", backups[0])
	}
	if lastDrill, ok := backups[0]["lastDrill"].(map[string]any); !ok || lastDrill["succeeded"] != true {
		t.Fatalf("backup = %v, want the newest successful drill", backups[0])
	}

	deletion := page.List(t, rendered, storage.ItemDeletion)
	if len(deletion) != 4 {
		t.Fatalf("deletion targets = %v, want every volume an Owner may delete", deletion)
	}
	confirmed := page.Find(t, deletion, "local-data")
	if confirmed["requiresConfirmation"] != true {
		t.Fatalf("deletion target = %v, want a confirmation before the write", confirmed)
	}
	if harness.String(confirmed, "resourceVersion") == "" {
		t.Fatalf("deletion target = %v, want the resource version the confirmed delete names", confirmed)
	}
	release, ok := confirmed["release"].(map[string]any)
	if !ok {
		t.Fatalf("deletion target = %v, want the release confirmation state", confirmed)
	}
	if harness.String(release, "requiredToken") != "local-data" || release["isConfirmed"] != true {
		t.Fatalf("release confirmation = %v, want the stored token to confirm the release", release)
	}

	targets := page.List(t, rendered, storage.ItemMigration)
	if len(targets) != 2 {
		t.Fatalf("migration targets = %v, want the two volumes whose policy differs", targets)
	}
	movable := page.Find(t, targets, "movable")
	if harness.String(movable, "targetStorageClass") != "network" {
		t.Fatalf("target policy = %q, want the volume's desired policy", harness.String(movable, "targetStorageClass"))
	}
	if harness.String(movable, "actualStorageClass") != "local" {
		t.Fatalf("actual policy = %q, want the recorded policy", harness.String(movable, "actualStorageClass"))
	}
	if window, ok := movable["window"].(map[string]any); !ok || window["isRequired"] != true {
		t.Fatalf("migration window = %v, want the user-supplied downtime window", movable["window"])
	}

	member := shell.Account(t, owner, memberName, memberPassword, write.RoleMember)
	memberPage := page.Read(t, shell, member, storage.PageStorage)
	memberDeletion := page.List(t, memberPage, storage.ItemDeletion)
	if len(memberDeletion) != 1 || harness.String(memberDeletion[0], "name") != "member-data" {
		t.Fatalf("member deletion targets = %v, want only the volume the member owns", memberDeletion)
	}
	memberTargets := page.List(t, memberPage, storage.ItemMigration)
	if len(memberTargets) != 1 || harness.String(memberTargets[0], "name") != "member-data" {
		t.Fatalf("member migration targets = %v, want only the volume the member owns", memberTargets)
	}

	guest := shell.Account(t, owner, guestName, guestPassword, write.RoleGuest)
	guestPage := page.Read(t, shell, guest, storage.PageStorage)
	if targets := page.List(t, guestPage, storage.ItemDeletion); len(targets) != 0 {
		t.Fatalf("guest deletion targets = %v, want none without write permission", targets)
	}
	if targets := page.List(t, guestPage, storage.ItemMigration); len(targets) != 0 {
		t.Fatalf("guest migration targets = %v, want none without write permission", targets)
	}
	if volumes := page.Records(t, page.Object(t, guestPage, storage.ItemVolumes), "volumes"); len(volumes) != 4 {
		t.Fatalf("guest volumes = %v, want the same read-only projection", volumes)
	}
}

// createVolume stores one volume the page reads; extraSpec carries fields the
// case wants on the spec besides the policy and mobility.
func createVolume(t *testing.T, cluster *harness.Cluster, name, owner string, spec, extraSpec, status map[string]any) {
	t.Helper()
	for key, value := range extraSpec {
		spec[key] = value
	}
	cluster.CreateIn(t, namespace, fixtures.Volume(namespace, name, owner, spec, status))
}
