// Package storage is the kos-storage system application: the storage page of
// the panel and the readings behind it. It shows device facts, providers and
// storage classes, volumes with their phases, health and snapshot facts,
// sampled usage, backup state, and the volume deletion and migration entries.
// It never reads or writes the data inside a volume (KOSD-NG-10).
package storage

import (
	"net/http"

	"github.com/tsic404/kosd/pkg/extension"
)

// Identifier is the product's extension identity across versions (KOSD-C-17).
const Identifier = "kos-storage"

// Version is the package version this declaration belongs to (KOSD-C-18).
const Version = "1.0.0"

// PageStorage is the storage page the panel shows (STORAGE-C-1).
const PageStorage = "storage"

// Item identities of the storage page, one per source STORAGE-C-1 and
// STORAGE-C-82 name.
const (
	ItemDevicesAndPolicies = "devices-and-policies"
	ItemVolumes            = "volumes-and-phases"
	ItemUsage              = "volume-usage"
	ItemBackups            = "backup-status"
	ItemDeletion           = "volume-deletion"
	ItemMigration          = "migrate-volume"
)

// Action identities the page offers (STORAGE-C-1, STORAGE-C-81).
const (
	ActionDeleteVolume   = "delete-volume"
	ActionStartMigration = "start-volume-migration"
)

// Control-plane paths the entries write through (KOSD-C-12). The deletion
// entry names one volume; the migration entry names the volume it moves in the
// path so a browser offers it per candidate.
const (
	pathVolumes    = "/api/v1/resources/Volume"
	pathMigrations = "/api/v1/resources/VolumeMigration"
)

// Display reasons of the items with no entry of their own (KOSD-C-53).
const (
	ReasonDevicesAndPolicies = "设备事实由采集适配器写入，存储类与 Provider 由组件注册；本页只读展示，入口不在本页"
	ReasonVolumes            = "卷的阶段、健康与快照事实由卷调和写入；本页只读展示，修改经卷资源接口发起"
	ReasonUsage              = "使用量按指标采样读取，页面不写入；未接入指标来源时标为不可测量"
	ReasonBackups            = "备份状态与最近演练由备份策略调和写入；手动备份、恢复与演练入口归 kos-backup"
)

// Declaration returns the contribution kos-storage ships (KOSD-C-18): the
// storage page and its deletion and migration entries.
func Declaration() extension.Declaration {
	return extension.Declaration{
		Identifier: Identifier,
		Version:    Version,
		Contributions: extension.Contributions{
			Pages: []extension.Page{storagePage()},
		},
	}
}

// storagePage declares the storage items STORAGE-C-1 and STORAGE-C-82 name.
// The deletion entry is its own item: the entry appears only for a session that
// may write the volume, so it cannot share the read-only volume projection.
func storagePage() extension.Page {
	return extension.Page{
		ID:       PageStorage,
		Title:    "存储",
		Location: "panel",
		Items: []extension.PageItem{
			{ID: ItemDevicesAndPolicies, Label: "设备与策略", Source: extension.PageSourceObject, Reference: reference(ItemDevicesAndPolicies), Reason: ReasonDevicesAndPolicies},
			{ID: ItemVolumes, Label: "卷与阶段", Source: extension.PageSourceObject, Reference: reference(ItemVolumes), Reason: ReasonVolumes},
			{ID: ItemUsage, Label: "使用率", Source: extension.PageSourceMetric, Reference: reference(ItemUsage), Reason: ReasonUsage},
			{ID: ItemBackups, Label: "备份状态与最近演练", Source: extension.PageSourceObject, Reference: reference(ItemBackups), Reason: ReasonBackups},
			{ID: ItemDeletion, Label: "删除卷", Source: extension.PageSourceObject, Reference: reference(ItemDeletion), Action: ActionDeleteVolume},
			{ID: ItemMigration, Label: "迁移卷", Source: extension.PageSourceObject, Reference: reference(ItemMigration), Action: ActionStartMigration},
		},
		Actions: []extension.Action{
			{ID: ActionDeleteVolume, Method: http.MethodDelete, Path: pathVolumes + "/{name}?namespace={namespace}"},
			{ID: ActionStartMigration, Method: http.MethodPost, Path: pathMigrations + "?namespace={namespace}&volume={name}"},
		},
	}
}

// reference is the item reference the product owns; the router dispatches by
// the identifier in front of it (KOSD-C-12).
func reference(item string) string { return Identifier + "/" + item }
