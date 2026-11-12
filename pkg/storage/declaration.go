// Package storage is the kos-storage system application: the storage page of
// the panel and the readings behind it. It shows device facts, providers and
// storage classes, volumes with their phases, health and snapshot facts,
// sampled usage, backup state, and the volume deletion and migration entries.
// It never reads or writes the data inside a volume (KOSD-NG-10).
package storage

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"

	"github.com/tsic404/kos-sdk/manifest"
	"github.com/tsic404/kos-sdk/packaging"

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

// InstallArtifact returns the artifact a system component names for one
// verified package: the declaration document of KOSD-C-17, derived from the
// package manifest. The kernel's install path reads a declaration document as
// the artifact, so the package manifest is translated, not unpacked.
func InstallArtifact(pkg packaging.Package) ([]byte, error) {
	declaration, err := InstallDeclaration(pkg)
	if err != nil {
		return nil, err
	}
	return extension.Marshal(declaration)
}

// InstallDeclaration is the kernel install form of one verified package. Every
// value comes from the package manifest, so the installed contributions are the
// declared ones. A manifest carrying a contribution class the kernel form has
// no field for is refused instead of being narrowed silently (KOSD-C-18).
//
// The manifest's read scopes and roles stay where the kernel judges roles: the
// page carries its own required role, and a kernel permission entry only ever
// widens an extension object's writers (KOSD-C-59), of which this product
// declares none.
func InstallDeclaration(pkg packaging.Package) (extension.Declaration, error) {
	declaration := pkg.Manifest
	if err := checkCarried(declaration); err != nil {
		return extension.Declaration{}, err
	}
	if framework := declaration.Dependencies.Framework; framework != "" && framework != declaration.FrameworkVersion {
		return extension.Declaration{}, fmt.Errorf(
			"framework dependency %q disagrees with the target range %q",
			framework, declaration.FrameworkVersion,
		)
	}
	pages, err := kernelPages(declaration.Contributions.Pages)
	if err != nil {
		return extension.Declaration{}, err
	}
	retention, err := kernelRetention(declaration.UninstallRetention)
	if err != nil {
		return extension.Declaration{}, err
	}
	return extension.Declaration{
		Identifier:       declaration.ID,
		Version:          declaration.Version,
		FrameworkVersion: declaration.FrameworkVersion,
		Contributions:    extension.Contributions{Pages: pages},
		Dependencies:     kernelDependencies(declaration.Dependencies),
		Configuration:    extension.Configuration{Schema: declaration.Config},
		Retention:        retention,
	}, nil
}

// checkCarried refuses a manifest whose contribution classes the kernel install
// form cannot carry. Only the page class has an install form here; a class that
// appears without one would otherwise install as nothing.
func checkCarried(declaration manifest.ExtensionManifest) error {
	contributions := declaration.Contributions
	classes := []struct {
		name     string
		declared bool
	}{
		{name: "objectClasses", declared: len(contributions.ObjectClasses) > 0},
		{name: "controllers", declared: len(contributions.Controllers) > 0},
		{name: "capabilities", declared: len(contributions.Capabilities) > 0},
		{name: "settings", declared: len(contributions.Settings) > 0},
		{name: "catalogSources", declared: len(contributions.CatalogSources) > 0},
		{name: "discoveryAdapters", declared: len(contributions.DiscoveryAdapters) > 0},
		{name: "diagnosticAdapters", declared: len(contributions.DiagnosticAdapters) > 0},
		{name: "userPreferences", declared: len(contributions.UserPreferences) > 0},
		{name: "apps", declared: len(contributions.Apps) > 0},
	}
	for _, class := range classes {
		if class.declared {
			return fmt.Errorf("contribution class %q has no kernel install form in %s", class.name, Identifier)
		}
	}
	return nil
}

// kernelPages reads the declared page contributions into the kernel's page
// shape (KOSD-C-53). The manifest carries the same page the kernel loads, so a
// failed decode is a declaration mistake, not a runtime condition.
func kernelPages(declared []map[string]any) ([]extension.Page, error) {
	encoded, err := json.Marshal(declared)
	if err != nil {
		return nil, fmt.Errorf("encode the declared pages: %w", err)
	}
	var pages []extension.Page
	if err := json.Unmarshal(encoded, &pages); err != nil {
		return nil, fmt.Errorf("decode the declared pages: %w", err)
	}
	return pages, nil
}

// kernelDependencies maps the manifest's capability and component edges. The
// framework edge is the declaration's target range, which the kernel reads from
// the top-level field.
func kernelDependencies(dependencies manifest.Dependencies) extension.Dependencies {
	kernel := extension.Dependencies{}
	for _, capability := range dependencies.Capabilities {
		kernel.Capabilities = append(kernel.Capabilities, extension.CapabilityRequirement{
			Name:     capability.Name,
			Versions: capability.Version,
		})
	}
	for _, component := range dependencies.Components {
		kernel.Components = append(kernel.Components, extension.ComponentRequirement{
			Identifier: component.ID,
			Versions:   component.Version,
		})
	}
	return kernel
}

// kernelRetention maps the declared fate of each contribution's data (KOSD-C-29).
// An external resource reference has no kernel form: the kernel's entry names
// the resource it covers, and an external reference is never deleted by KOS.
func kernelRetention(retention manifest.UninstallRetention) ([]extension.Retention, error) {
	if len(retention.ExternalResources) > 0 {
		return nil, fmt.Errorf("external resource references have no kernel install form in %s", Identifier)
	}
	classes := make([]string, 0, len(retention.Contributions))
	for class := range retention.Contributions {
		classes = append(classes, class)
	}
	sort.Strings(classes)
	entries := make([]extension.Retention, 0, len(classes))
	for _, class := range classes {
		entries = append(entries, extension.Retention{
			Resource: class,
			Keep:     retention.Contributions[class] == manifest.Retain,
		})
	}
	return entries, nil
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
