package storage

import (
	"context"
	"time"

	"k8s.io/client-go/dynamic"

	"github.com/tsic404/kosd/pkg/controlplane"
	"github.com/tsic404/kosd/pkg/extension"
	"github.com/tsic404/kosd/pkg/identity"
	"github.com/tsic404/kosd/pkg/pages"
	"github.com/tsic404/kosd/pkg/pages/cluster"
)

// Readings resolves the items the storage page declares (KOSD-C-53).
type Readings struct {
	// Dynamic reads the cluster.
	Dynamic dynamic.Interface
	// Now supplies the read clock; defaults to time.Now.
	Now func() time.Time
	// Usage reports volume fill samples; nil states that no metric source is
	// configured instead of presenting allocated capacity as usage.
	Usage UsageSource
}

// Read implements pages.Readings.
func (r *Readings) Read(ctx context.Context, session identity.Session, item extension.PageItem) (pages.Reading, *controlplane.Error) {
	value, failure := r.value(ctx, session, item)
	if failure != nil {
		return pages.Reading{}, failure
	}
	return pages.Reading{Value: value, ObservedAt: r.reader().Clock()}, nil
}

// value resolves one item from the source it declares.
func (r *Readings) value(ctx context.Context, session identity.Session, item extension.PageItem) (any, *controlplane.Error) {
	switch item.ID {
	case ItemDevicesAndPolicies:
		return r.devicesAndPolicies(ctx)
	case ItemVolumes:
		return r.volumes(ctx)
	case ItemUsage:
		return r.usage(ctx)
	case ItemBackups:
		return r.backups(ctx)
	case ItemDeletion:
		return r.deletionTargets(ctx, session)
	case ItemMigration:
		return r.migrationTargets(ctx, session)
	default:
		return nil, unknownItem(item.ID)
	}
}

// reader is the cluster read seam for this call.
func (r *Readings) reader() *cluster.Reader {
	return &cluster.Reader{Dynamic: r.Dynamic, Now: r.Now}
}

// unknownItem marks an item this product does not declare.
func unknownItem(id string) *controlplane.Error {
	return &controlplane.Error{
		Code:        controlplane.CodeCapabilityMissing,
		Message:     "kos-storage 没有声明页面条目 " + id,
		Stage:       controlplane.StageCapability,
		SideEffects: controlplane.SideEffectsNone,
	}
}

// reasonView states why one fact on the page cannot be modified here.
type reasonView struct {
	Item   string `json:"item"`
	Reason string `json:"reason"`
}
