package storage

import (
	"context"
	"sort"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/tsic404/kosd/api/v1beta1"
	"github.com/tsic404/kosd/pkg/controlplane"
	"github.com/tsic404/kosd/pkg/pages/cluster"
)

// backupsView is the read-only backup item STORAGE-C-1 and STORAGE-C-82 name.
type backupsView struct {
	Policies []backupView `json:"policies"`
	ReadOnly []reasonView `json:"readOnly"`
}

// backupView is one backup policy with its newest run and drill (KOSD-C-41).
type backupView struct {
	Namespace    string         `json:"namespace"`
	Name         string         `json:"name"`
	Phase        string         `json:"phase"`
	Health       string         `json:"health"`
	Schedule     string         `json:"schedule,omitempty"`
	Consistency  string         `json:"consistency,omitempty"`
	TargetPath   string         `json:"targetPath,omitempty"`
	NextRunTime  string         `json:"nextRunTime,omitempty"`
	LastBackup   *backupRunView `json:"lastBackup,omitempty"`
	LastDrill    *drillView     `json:"lastDrill,omitempty"`
	DrillEnabled bool           `json:"drillEnabled"`
}

// backupRunView is the newest backup run.
type backupRunView struct {
	CompletedAt string `json:"completedAt,omitempty"`
	Consistency string `json:"consistency,omitempty"`
	Succeeded   bool   `json:"succeeded"`
	Reason      string `json:"reason,omitempty"`
}

// drillView is the newest recovery drill.
type drillView struct {
	Phase       string `json:"phase,omitempty"`
	CompletedAt string `json:"completedAt,omitempty"`
	Succeeded   bool   `json:"succeeded"`
	Reason      string `json:"reason,omitempty"`
}

// backups reads the backup policies with the backup and drill state the page
// shows (KOSD-C-41, STORAGE-C-82).
func (r *Readings) backups(ctx context.Context) (any, *controlplane.Error) {
	objects, failure := r.reader().List(ctx, "BackupPolicy", "")
	if failure != nil {
		return nil, failure
	}
	views := make([]backupView, 0, len(objects))
	for _, object := range objects {
		var policy v1beta1.BackupPolicy
		if failure := cluster.Decode(object, &policy); failure != nil {
			return nil, failure
		}
		views = append(views, backupOf(policy))
	}
	sort.Slice(views, func(left, right int) bool {
		if views[left].Namespace != views[right].Namespace {
			return views[left].Namespace < views[right].Namespace
		}
		return views[left].Name < views[right].Name
	})
	return backupsView{
		Policies: views,
		ReadOnly: []reasonView{
			{Item: "policies", Reason: "备份状态由备份策略调和写入；本页只读展示"},
			{Item: "lastBackup", Reason: "备份由备份策略的计划触发；手动触发入口不在本页"},
			{Item: "lastDrill", Reason: "恢复与演练入口归 kos-backup（BACKUP-C-1）；本页只读展示"},
		},
	}, nil
}

// backupOf renders one backup policy with its newest run and drill.
func backupOf(policy v1beta1.BackupPolicy) backupView {
	view := backupView{
		Namespace: policy.Namespace, Name: policy.Name,
		Phase: policy.Status.Phase, Health: string(policy.Status.Health.State),
		Schedule: policy.Spec.Schedule, Consistency: string(policy.Spec.Consistency),
		TargetPath: policy.Spec.Target.Path, DrillEnabled: policy.Spec.Drill.Enabled,
	}
	if policy.Status.NextRunTime != nil {
		view.NextRunTime = policy.Status.NextRunTime.UTC().Format(timeLayout)
	}
	if record := policy.Status.LastBackup; record != nil {
		view.LastBackup = &backupRunView{
			CompletedAt: timeText(record.CompletedAt), Consistency: string(record.Consistency),
			Succeeded: record.Succeeded, Reason: record.Reason,
		}
	}
	if drill := policy.Status.LastDrill; drill != nil {
		view.LastDrill = &drillView{
			Phase: string(drill.Phase), CompletedAt: timeText(drill.CompletedAt),
			Succeeded: drill.Succeeded, Reason: drill.Reason,
		}
	}
	return view
}

// timeText renders an optional timestamp.
func timeText(moment *metav1.Time) string {
	if moment == nil {
		return ""
	}
	return moment.UTC().Format(timeLayout)
}
