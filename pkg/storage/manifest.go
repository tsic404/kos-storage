package storage

import (
	"encoding/json"
	"fmt"

	"github.com/tsic404/kos-sdk/manifest"
	"github.com/tsic404/kos-sdk/packaging"

	"github.com/tsic404/kosd/api"
)

// FrameworkRange is the framework API version range this package targets
// (KOSD-C-18). It admits the version the kernel serves (KOSD-C-16).
const FrameworkRange = ">=1.1 <2.0"

// ComponentConditions returns the component status conditions the kernel writes
// on an installed package (KOSD-C-07, KOSD-C-28). The declaration names them so
// the status interface and the stored status agree.
func ComponentConditions() []string {
	return []string{"PackageVerified", "FrameworkCompatible", "DependenciesMet", "ContributionsLoaded"}
}

// ReadScopes are the read-only API scopes the storage page reads (KOSD-C-18).
// The page reads the kernel's storage objects and the native storage classes;
// it declares no write scope, because every modification goes through the
// control-plane entries the page carries (STORAGE-C-1).
func ReadScopes() []manifest.ReadScope {
	return []manifest.ReadScope{
		{
			APIGroup:  api.Group,
			Resources: []string{"volumes", "volumemigrations", "providers", "backuppolicies", "nodedevices"},
		},
		{
			APIGroup:  "storage.k8s.io",
			Resources: []string{"storageclasses"},
		},
	}
}

// Roles are the control-plane roles that may use the page (KOSD-C-18). The page
// declares no required role, so every signed-in role reads it.
func Roles() []string {
	return []string{string(manifest.RoleOwner), string(manifest.RoleMember), string(manifest.RoleGuest)}
}

// Manifest is the extension declaration of KOSD-C-18: identity, version, target
// framework range, the storage page as its only contribution, the framework
// dependency, read-only permissions, the component configuration and status
// interface, and the uninstall retention. It declares no object class,
// controller, capability, setting, catalogue source, adapter, preference or
// window application.
func Manifest() (manifest.ExtensionManifest, error) {
	page, err := PagePayload()
	if err != nil {
		return manifest.ExtensionManifest{}, err
	}
	return manifest.ExtensionManifest{
		ID:               Identifier,
		Version:          Version,
		FrameworkVersion: FrameworkRange,
		Contributions: manifest.Contributions{
			Pages: []map[string]any{page},
		},
		Dependencies: manifest.Dependencies{Framework: FrameworkRange},
		Permissions: manifest.ExtensionPermissions{
			Read:  ReadScopes(),
			Roles: Roles(),
		},
		// No contribution declares a configurable property, so the structure
		// is empty rather than filled with defaults.
		Config: map[string]any{},
		Status: manifest.StatusDeclaration{
			Component: &manifest.ComponentStatus{Conditions: ComponentConditions()},
		},
		UninstallRetention: manifest.UninstallRetention{
			// The page holds no data of its own; uninstall removes it and
			// leaves nothing to keep.
			Contributions: map[string]manifest.RetentionAction{"pages": manifest.Delete},
		},
	}, nil
}

// PagePayload renders the storage page as the contribution value the manifest
// carries (KOSD-C-18). The page is declared once, so the package and the
// kernel's install document cannot drift apart.
func PagePayload() (map[string]any, error) {
	encoded, err := json.Marshal(storagePage())
	if err != nil {
		return nil, fmt.Errorf("encode the storage page: %w", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		return nil, fmt.Errorf("decode the storage page: %w", err)
	}
	return payload, nil
}

// Package packs the manifest through the SDK's single packing path (SDK-C-4,
// KOSD-C-17). The product ships no UI asset, so the digest covers the manifest
// alone; the packer reads no product identity or install source.
func Package() (packaging.Package, []byte, error) {
	declaration, err := Manifest()
	if err != nil {
		return packaging.Package{}, nil, err
	}
	document, err := documentOf(declaration)
	if err != nil {
		return packaging.Package{}, nil, err
	}
	return packaging.Pack(document, nil)
}

// documentOf renders the manifest as the decoded JSON value the packer reads.
// The round trip also proves the manifest is JSON-encodable before packing.
func documentOf(declaration manifest.ExtensionManifest) (map[string]any, error) {
	encoded, err := json.Marshal(declaration)
	if err != nil {
		return nil, fmt.Errorf("encode the extension manifest: %w", err)
	}
	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		return nil, fmt.Errorf("decode the extension manifest: %w", err)
	}
	return document, nil
}
