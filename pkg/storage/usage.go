package storage

import (
	"context"

	"github.com/tsic404/kosd/pkg/controlplane"
)

// UsageSource reports the sampled fill of volumes (STORAGE-C-1). A
// distribution with no volume metric reader leaves it nil, and the item states
// that instead of presenting allocated capacity as usage.
type UsageSource interface {
	// Usage returns one sample per volume.
	Usage(ctx context.Context) ([]VolumeUsage, error)
}

// VolumeUsage is one volume's sampled fill.
type VolumeUsage struct {
	// Namespace is the volume's namespace.
	Namespace string `json:"namespace"`
	// Name is the volume name.
	Name string `json:"name"`
	// Used is the sampled used size.
	Used string `json:"used"`
	// Capacity is the size the claim reports.
	Capacity string `json:"capacity"`
}

// usage reports the sampled fill of the volumes (STORAGE-C-1). Without a
// metric reader the item says so: allocated capacity is not usage.
func (r *Readings) usage(ctx context.Context) (any, *controlplane.Error) {
	if r.Usage == nil {
		return map[string]any{
			"available": false,
			"reason":    "发行版没有接入卷使用量的指标来源，KOS 不读取卷内数据",
		}, nil
	}
	samples, err := r.Usage.Usage(ctx)
	if err != nil {
		return map[string]any{"available": false, "reason": "卷使用量读取失败：" + err.Error()}, nil
	}
	return map[string]any{"available": true, "volumes": samples}, nil
}
