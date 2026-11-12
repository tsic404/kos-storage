package storage

import (
	"context"
	"sort"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/tsic404/kosd/api/v1beta1"
	"github.com/tsic404/kosd/pkg/controlplane"
	"github.com/tsic404/kosd/pkg/identity"
	"github.com/tsic404/kosd/pkg/migration"
	"github.com/tsic404/kosd/pkg/pages/cluster"
	volumeapi "github.com/tsic404/kosd/pkg/volume"
	"github.com/tsic404/kosd/pkg/write"
)

// timeLayout is the timestamp form every projection uses.
const timeLayout = time.RFC3339

// Native classes the binding facts of a volume are read from (KOSD-C-38,
// KOSD-C-40). The volume module writes neither the binding claim nor the policy
// it resolved, so the page reads them where the binding lives.
var (
	persistentVolumeClaims = schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumeclaims"}
	persistentVolumes      = schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumes"}
)

// Rollback outcomes the page reports for a failed migration attempt
// (STORAGE-C-81).
const (
	RollbackRolledBack = "rolled-back"
	RollbackNotNeeded  = "not-needed"
	RollbackFailed     = "failed"
)

// volumesView is the read-only item STORAGE-C-1 and STORAGE-C-82 name.
type volumesView struct {
	Volumes  []volumeView `json:"volumes"`
	ReadOnly []reasonView `json:"readOnly"`
}

// volumeView is one volume as the page shows it: the phase, the aggregated
// health, the policies it declares and resolved, and the snapshot facts.
type volumeView struct {
	Namespace           string         `json:"namespace"`
	Name                string         `json:"name"`
	Phase               string         `json:"phase"`
	Health              string         `json:"health"`
	Mobility            string         `json:"mobility,omitempty"`
	Capacity            string         `json:"capacity,omitempty"`
	BoundClaim          string         `json:"boundClaim,omitempty"`
	DesiredStorageClass string         `json:"desiredStorageClass,omitempty"`
	ActualStorageClass  string         `json:"actualStorageClass,omitempty"`
	Provider            *backendView   `json:"provider,omitempty"`
	Node                string         `json:"node,omitempty"`
	ReclaimPolicy       string         `json:"reclaimPolicy,omitempty"`
	Snapshot            snapshotView   `json:"snapshot"`
	Migration           *migrationView `json:"migration,omitempty"`
	ReadOnly            []reasonView   `json:"readOnly"`
}

// backendView names the storage backend a claim resolved to.
type backendView struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// snapshotView is the read-only snapshot capability and consistency of a
// volume (STORAGE-C-82).
type snapshotView struct {
	Capabilities []capabilityView `json:"capabilities"`
	Consistency  string           `json:"consistency,omitempty"`
	Reason       string           `json:"reason,omitempty"`
}

// migrationView is one volume's current or last failed migration attempt
// (STORAGE-C-81).
type migrationView struct {
	Volume             string        `json:"volume"`
	TargetStorageClass string        `json:"targetStorageClass"`
	Window             windowView    `json:"window"`
	Phase              string        `json:"phase"`
	Stages             []string      `json:"stages"`
	CurrentStage       string        `json:"currentStage,omitempty"`
	RollbackPoint      string        `json:"rollbackPoint"`
	Failure            *failureView  `json:"failure,omitempty"`
	Rollback           *rollbackView `json:"rollback,omitempty"`
}

// windowView is the downtime window of a migration (KOSD-C-40). A candidate
// before a request states that the window is user input; a stored migration
// reports the window it runs under.
type windowView struct {
	Start      string `json:"start,omitempty"`
	Duration   string `json:"duration,omitempty"`
	StartedAt  string `json:"startedAt,omitempty"`
	IsRequired bool   `json:"isRequired,omitempty"`
}

// failureView names the stage a migration attempt failed in and why.
type failureView struct {
	Stage  string `json:"stage"`
	Reason string `json:"reason"`
}

// rollbackView is what the page reports about a rollback the flow executed
// (STORAGE-C-81): the page shows the result and offers no rollback entry.
type rollbackView struct {
	Outcome       string `json:"outcome"`
	RestoredClaim string `json:"restoredClaim,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

// deletionTargetView is one volume the session may delete (STORAGE-C-1).
type deletionTargetView struct {
	Namespace            string              `json:"namespace"`
	Name                 string              `json:"name"`
	ResourceVersion      string              `json:"resourceVersion"`
	RequiresConfirmation bool                `json:"requiresConfirmation"`
	Release              releaseConfirmation `json:"release"`
}

// releaseConfirmation is the separate confirmation releasing data needs
// (KOSD-C-39): the token must equal the volume name.
type releaseConfirmation struct {
	RequiredToken string `json:"requiredToken"`
	IsConfirmed   bool   `json:"isConfirmed"`
}

// migrationTargetView is one volume the session may move to its desired policy
// (STORAGE-C-81).
type migrationTargetView struct {
	Namespace          string     `json:"namespace"`
	Name               string     `json:"name"`
	TargetStorageClass string     `json:"targetStorageClass"`
	ActualStorageClass string     `json:"actualStorageClass,omitempty"`
	Window             windowView `json:"window"`
}

// volumes reads the volumes with their phases, health, snapshot facts and
// current migration state (STORAGE-C-1, STORAGE-C-82).
func (r *Readings) volumes(ctx context.Context) (any, *controlplane.Error) {
	stored, failure := r.volumeObjects(ctx)
	if failure != nil {
		return nil, failure
	}
	migrations, failure := r.migrations(ctx)
	if failure != nil {
		return nil, failure
	}
	views := make([]volumeView, 0, len(stored))
	for _, volume := range stored {
		actual, failure := r.actualPolicy(ctx, volume)
		if failure != nil {
			return nil, failure
		}
		views = append(views, volumeOf(volume, actual, latestMigration(migrations, volume.Namespace, volume.Name)))
	}
	return volumesView{
		Volumes: views,
		ReadOnly: []reasonView{
			{Item: "phase", Reason: "卷的阶段与健康由卷调和写入；本页只读展示"},
			{Item: "health", Reason: "健康按 KOSD-C-08 聚合，陈旧标记按 KOSD-C-13；本页只读展示"},
			{Item: "snapshot", Reason: "快照能力与一致性由卷状态记录；本页只读展示，创建快照入口不在本页"},
			{Item: "migration", Reason: "迁移阶段、失败原因与回滚结果由迁移流程写入；回滚由流程执行，本页没有触发回滚的入口"},
		},
	}, nil
}

// volumeObjects lists and decodes the volumes in a stable order.
func (r *Readings) volumeObjects(ctx context.Context) ([]v1beta1.Volume, *controlplane.Error) {
	objects, failure := r.reader().List(ctx, "Volume", "")
	if failure != nil {
		return nil, failure
	}
	volumes := make([]v1beta1.Volume, 0, len(objects))
	for _, object := range objects {
		var volume v1beta1.Volume
		if failure := cluster.Decode(object, &volume); failure != nil {
			return nil, failure
		}
		volumes = append(volumes, volume)
	}
	sort.Slice(volumes, func(left, right int) bool {
		if volumes[left].Namespace != volumes[right].Namespace {
			return volumes[left].Namespace < volumes[right].Namespace
		}
		return volumes[left].Name < volumes[right].Name
	})
	return volumes, nil
}

// migrations lists the migration objects in a stable order.
func (r *Readings) migrations(ctx context.Context) ([]v1beta1.VolumeMigration, *controlplane.Error) {
	objects, failure := r.reader().List(ctx, "VolumeMigration", "")
	if failure != nil {
		return nil, failure
	}
	migrations := make([]v1beta1.VolumeMigration, 0, len(objects))
	for _, object := range objects {
		var stored v1beta1.VolumeMigration
		if failure := cluster.Decode(object, &stored); failure != nil {
			return nil, failure
		}
		migrations = append(migrations, stored)
	}
	return migrations, nil
}

// volumeOf renders one volume; the latest migration is nil when none runs or
// the newest attempt already succeeded, and actual is the policy its binding
// resolved.
func volumeOf(volume v1beta1.Volume, actual string, latest *v1beta1.VolumeMigration) volumeView {
	health := string(volume.Status.Health.State)
	if health == "" {
		health = string(v1beta1.HealthStateUnknown)
	}
	view := volumeView{
		Namespace: volume.Namespace, Name: volume.Name,
		Phase: volume.Status.Phase, Health: health,
		Mobility: string(volume.Spec.Mobility), Capacity: volume.Spec.Capacity,
		BoundClaim:          volume.Status.BoundClaim,
		DesiredStorageClass: volume.Spec.StorageClass, ActualStorageClass: actual,
		Node: volume.Status.Node, ReclaimPolicy: volume.Status.ReclaimPolicy,
		Snapshot: snapshotOf(volume),
		ReadOnly: []reasonView{
			{Item: "health", Reason: "健康按 KOSD-C-08 聚合；本页只读展示"},
			{Item: "snapshotCapabilities", Reason: "快照能力由卷状态记录；本页只读展示"},
			{Item: "snapshotConsistency", Reason: "一致性按 KOSD-C-38 计算，共享读写卷降为崩溃一致；本页只读展示"},
		},
	}
	if volume.Status.Provider.Name != "" {
		view.Provider = &backendView{Name: volume.Status.Provider.Name, Version: volume.Status.Provider.Version}
	}
	if latest != nil && latest.Status.Phase != migration.PhaseSucceeded {
		view.Migration = migrationOf(volume, *latest)
	}
	return view
}

// actualPolicy reads the storage policy the volume's binding resolved
// (KOSD-C-40, KOSD-C-38). The volume module leaves status.boundClaim and
// status.storageClass to the migration flow, so before the first migration the
// claim is named after the volume and the resolved policy is readable only from
// the PersistentVolume the claim bound.
func (r *Readings) actualPolicy(ctx context.Context, volume v1beta1.Volume) (string, *controlplane.Error) {
	claimName := volume.Name
	if volume.Status.BoundClaim != "" {
		claimName = volume.Status.BoundClaim
	}
	claim, err := r.Dynamic.Resource(persistentVolumeClaims).Namespace(volume.Namespace).
		Get(ctx, claimName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return volume.Status.StorageClass, nil
	}
	if err != nil {
		return "", controlplaneFailure(err, "persistentvolumeclaims")
	}
	persistentVolumeName := cluster.Text(claim.Object, "spec", "volumeName")
	if persistentVolumeName == "" {
		return volume.Status.StorageClass, nil
	}
	persistentVolume, err := r.Dynamic.Resource(persistentVolumes).
		Get(ctx, persistentVolumeName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return volume.Status.StorageClass, nil
	}
	if err != nil {
		return "", controlplaneFailure(err, "persistentvolumes")
	}
	if resolved := cluster.Text(persistentVolume.Object, "spec", "storageClassName"); resolved != "" {
		return resolved, nil
	}
	return volume.Status.StorageClass, nil
}

// snapshotOf renders the snapshot capability and consistency of one volume.
func snapshotOf(volume v1beta1.Volume) snapshotView {
	view := snapshotView{Capabilities: make([]capabilityView, 0, len(volume.Status.Capabilities))}
	for _, capability := range volume.Status.Capabilities {
		view.Capabilities = append(view.Capabilities, capabilityView{Name: capability.Name, Version: capability.Version})
	}
	if sharedRWX(volume) {
		view.Consistency = volumeapi.SnapshotConsistencyCrash
		return view
	}
	if volume.Status.SnapshotConsistency != "" {
		view.Consistency = volume.Status.SnapshotConsistency
		return view
	}
	view.Reason = "卷状态没有记录快照一致性"
	return view
}

// sharedRWX reports whether a volume carries the shared read-write capability,
// which KOSD-C-38 downgrades to crash consistency.
func sharedRWX(volume v1beta1.Volume) bool {
	if volume.Spec.Mobility == v1beta1.VolumeMobilitySharedRWX {
		return true
	}
	for _, capability := range volume.Status.Capabilities {
		if capability.Name == "storage.shared-rwx" {
			return true
		}
	}
	return false
}

// migrationOf renders one stored migration with the rollback result the flow
// produced (STORAGE-C-81).
func migrationOf(volume v1beta1.Volume, stored v1beta1.VolumeMigration) *migrationView {
	view := &migrationView{
		Volume: stored.Spec.Volume, TargetStorageClass: stored.Spec.TargetStorageClass,
		Window: windowOf(stored), Phase: stored.Status.Phase,
		Stages: stageOrder(), RollbackPoint: stored.Status.RollbackPoint,
	}
	if stageInOrder(view.Phase) {
		view.CurrentStage = view.Phase
	}
	if stored.Status.Failure != nil {
		view.Failure = &failureView{Stage: stored.Status.Failure.Phase, Reason: stored.Status.Failure.Message}
	}
	if stored.Status.Phase == migration.PhaseFailed {
		view.Rollback = rollbackOf(volume, stored)
	}
	return view
}

// windowOf renders the downtime window of a stored migration.
func windowOf(stored v1beta1.VolumeMigration) windowView {
	view := windowView{Duration: stored.Spec.Window.Duration}
	if stored.Spec.Window.Start != nil {
		view.Start = stored.Spec.Window.Start.UTC().Format(timeLayout)
	}
	if stored.Status.WindowStartedAt != nil {
		view.StartedAt = stored.Status.WindowStartedAt.UTC().Format(timeLayout)
	}
	return view
}

// rollbackOf reports whether a failed attempt returned the volume to the claim
// it started from. A Preflight refusal changed nothing, so it needed none.
func rollbackOf(volume v1beta1.Volume, stored v1beta1.VolumeMigration) *rollbackView {
	if stored.Status.Failure == nil {
		return nil
	}
	reason := stored.Status.Failure.Message
	if stored.Status.Failure.Phase == migration.PhasePreflight {
		return &rollbackView{Outcome: RollbackNotNeeded, Reason: reason}
	}
	if stored.Status.SourceClaim != "" && volume.Status.BoundClaim == stored.Status.SourceClaim {
		return &rollbackView{Outcome: RollbackRolledBack, RestoredClaim: stored.Status.SourceClaim, Reason: reason}
	}
	return &rollbackView{Outcome: RollbackFailed, RestoredClaim: volume.Status.BoundClaim, Reason: reason}
}

// stageOrder is the six-stage order KOSD-C-40 fixes.
func stageOrder() []string {
	return []string{
		migration.PhasePreflight, migration.PhaseQuiesce, migration.PhaseCopy,
		migration.PhaseVerify, migration.PhaseCutover, migration.PhaseCleanup,
	}
}

// stageInOrder reports whether phase is one of the six flow stages.
func stageInOrder(phase string) bool {
	for _, stage := range stageOrder() {
		if stage == phase {
			return true
		}
	}
	return false
}

// latestMigration picks the newest attempt of one volume. An attempt runs under
// the volume's own name, so several failed attempts may coexist.
func latestMigration(migrations []v1beta1.VolumeMigration, namespace, name string) *v1beta1.VolumeMigration {
	var latest *v1beta1.VolumeMigration
	for index := range migrations {
		candidate := &migrations[index]
		if candidate.Namespace != namespace || candidate.Spec.Volume != name {
			continue
		}
		if latest == nil || isNewerMigration(*candidate, *latest) {
			latest = candidate
		}
	}
	return latest
}

// isNewerMigration orders two attempts by creation time, then name.
func isNewerMigration(left, right v1beta1.VolumeMigration) bool {
	if !left.CreationTimestamp.Equal(&right.CreationTimestamp) {
		return right.CreationTimestamp.Before(&left.CreationTimestamp)
	}
	return left.Name < right.Name
}

// deletionTargets lists the volumes the session may delete (STORAGE-C-1,
// KOSD-C-39). A role without write permission gets no target, so the entry has
// nothing to offer it.
func (r *Readings) deletionTargets(ctx context.Context, session identity.Session) (any, *controlplane.Error) {
	targets := make([]deletionTargetView, 0)
	if !hasWriteRole(session) {
		return targets, nil
	}
	volumes, failure := r.volumeObjects(ctx)
	if failure != nil {
		return nil, failure
	}
	for _, volume := range volumes {
		if volume.DeletionTimestamp != nil || !ownsVolume(session, volume) {
			continue
		}
		targets = append(targets, deletionTargetView{
			Namespace: volume.Namespace, Name: volume.Name,
			ResourceVersion:      volume.ResourceVersion,
			RequiresConfirmation: true,
			Release: releaseConfirmation{
				RequiredToken: volume.Name,
				IsConfirmed:   volume.Spec.PurgeToken == volume.Name,
			},
		})
	}
	return targets, nil
}

// migrationTargets lists the volumes whose desired policy differs from the
// policy the volume records while it is bound (STORAGE-C-81). A volume with an
// attempt in flight has no target: the entry only starts a new attempt.
func (r *Readings) migrationTargets(ctx context.Context, session identity.Session) (any, *controlplane.Error) {
	targets := make([]migrationTargetView, 0)
	if !hasWriteRole(session) {
		return targets, nil
	}
	volumes, failure := r.volumeObjects(ctx)
	if failure != nil {
		return nil, failure
	}
	migrations, failure := r.migrations(ctx)
	if failure != nil {
		return nil, failure
	}
	for _, volume := range volumes {
		actual, failure := r.actualPolicy(ctx, volume)
		if failure != nil {
			return nil, failure
		}
		if !ownsVolume(session, volume) || !isMigrationCandidate(volume, actual) {
			continue
		}
		if latest := latestMigration(migrations, volume.Namespace, volume.Name); latest != nil && stageInOrder(latest.Status.Phase) {
			continue
		}
		targets = append(targets, migrationTargetView{
			Namespace: volume.Namespace, Name: volume.Name,
			TargetStorageClass: volume.Spec.StorageClass,
			ActualStorageClass: actual,
			Window:             windowView{IsRequired: true},
		})
	}
	return targets, nil
}

// isMigrationCandidate applies the entry condition of STORAGE-C-81: the volume
// is bound and the policy its binding resolved differs from the one it
// declares.
func isMigrationCandidate(volume v1beta1.Volume, actual string) bool {
	if volume.Status.Phase != volumeapi.PhaseBound || volume.DeletionTimestamp != nil {
		return false
	}
	if volume.Spec.StorageClass == "" {
		return false
	}
	return actual != "" && actual != volume.Spec.StorageClass
}

// hasWriteRole reports whether a session role may write its own volumes
// (KOSD-C-59).
func hasWriteRole(session identity.Session) bool {
	return session.Role == write.RoleOwner || session.Role == write.RoleMember
}

// ownsVolume applies KOSD-C-59: the global Owner writes every volume, a Member
// only the volumes it owns.
func ownsVolume(session identity.Session, volume v1beta1.Volume) bool {
	return session.Role == write.RoleOwner || volume.Spec.Owner == session.Username
}

// controlplaneFailure maps a native read failure onto the API envelope.
func controlplaneFailure(err error, resource string) *controlplane.Error {
	return &controlplane.Error{
		Code:        controlplane.CodeClusterUnavailable,
		Message:     "读取 " + resource + " 失败：" + err.Error(),
		Stage:       controlplane.StageRead,
		SideEffects: controlplane.SideEffectsNone,
	}
}
