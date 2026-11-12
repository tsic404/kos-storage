package storage

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/tsic404/kos-sdk/manifest"
	"github.com/tsic404/kos-sdk/packaging"
	"github.com/tsic404/kos-sdk/validate"

	"github.com/tsic404/kosd/pkg/extension"
)

// TestPackageCarriesTheContractFieldSet requires the packaged manifest to carry
// exactly the fields KOSD-C-18 and KOSD-C-73 define: no SDK-only field, no
// missing required field, and only the page contribution class (SDK-C-3,
// SDK-C-4).
func TestPackageCarriesTheContractFieldSet(t *testing.T) {
	pkg, _, err := Package()
	if err != nil {
		t.Fatalf("pack the extension: %v", err)
	}
	document := manifestDocument(t, pkg.Manifest)
	if got, want := sortedKeys(document), sortedCopy(manifest.ExtensionManifestFields); !reflect.DeepEqual(got, want) {
		t.Fatalf("manifest fields = %v, want the contract set %v", got, want)
	}

	contributions, ok := document["contributions"].(map[string]any)
	if !ok {
		t.Fatalf("manifest carries no contributions object: %v", document["contributions"])
	}
	if got, want := sortedKeys(contributions), []string{"pages"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("declared contribution classes = %v, want only %v", got, want)
	}

	if id, _ := document["id"].(string); id != Identifier {
		t.Errorf("manifest id = %q, want %q", id, Identifier)
	}
	if version, _ := document["version"].(string); version != Version {
		t.Errorf("manifest version = %q, want %q", version, Version)
	}
	if framework, _ := document["frameworkVersion"].(string); framework != FrameworkRange {
		t.Errorf("manifest frameworkVersion = %q, want %q", framework, FrameworkRange)
	}
}

// TestPackageDeclaresReadOnlyPermissionsAndFrameworkDependency requires the
// permission and dependency blocks to state what the page needs: read scopes
// and control-plane roles, no write scope, and the framework version as its
// only dependency (KOSD-C-18).
func TestPackageDeclaresReadOnlyPermissionsAndFrameworkDependency(t *testing.T) {
	pkg, _, err := Package()
	if err != nil {
		t.Fatalf("pack the extension: %v", err)
	}
	permissions := pkg.Manifest.Permissions
	if len(permissions.Write) != 0 {
		t.Errorf("declared write scopes = %v, want none", permissions.Write)
	}
	if len(permissions.Read) == 0 {
		t.Fatal("declared no read scope, want the scopes the page reads")
	}
	if len(permissions.ObjectRoles) != 0 {
		t.Errorf("declared extension object roles = %v, want none", permissions.ObjectRoles)
	}
	if got, want := permissions.Roles, Roles(); !reflect.DeepEqual(got, want) {
		t.Errorf("declared roles = %v, want %v", got, want)
	}
	for _, role := range permissions.Roles {
		if !isKosRole(role) {
			t.Errorf("declared role %q is outside the fixed role set %v", role, manifest.KosRoles)
		}
	}

	dependencies := pkg.Manifest.Dependencies
	if dependencies.Framework != FrameworkRange {
		t.Errorf("declared framework dependency = %q, want %q", dependencies.Framework, FrameworkRange)
	}
	if len(dependencies.Capabilities) != 0 || len(dependencies.Components) != 0 {
		t.Errorf("declared dependencies %+v, want the framework version only", dependencies)
	}
}

// TestPackageDeclaresTheUninstallRetention requires the declaration to state
// that uninstall removes the page and keeps no data and no external resource
// (KOSD-C-18, KOSD-C-29).
func TestPackageDeclaresTheUninstallRetention(t *testing.T) {
	pkg, _, err := Package()
	if err != nil {
		t.Fatalf("pack the extension: %v", err)
	}
	retention := pkg.Manifest.UninstallRetention
	if got, want := retention.Contributions, (map[string]manifest.RetentionAction{"pages": manifest.Delete}); !reflect.DeepEqual(got, want) {
		t.Errorf("retention = %v, want %v", got, want)
	}
	if len(retention.ExternalResources) != 0 {
		t.Errorf("declared external resources = %v, want none", retention.ExternalResources)
	}
}

// TestManifestPageIsTheDeclaredStoragePage requires the packaged page payload to
// be the page the kernel installs, item by item and action by action
// (STORAGE-C-1, STORAGE-C-81).
func TestManifestPageIsTheDeclaredStoragePage(t *testing.T) {
	pkg, _, err := Package()
	if err != nil {
		t.Fatalf("pack the extension: %v", err)
	}
	payload, err := PagePayload()
	if err != nil {
		t.Fatalf("render the page payload: %v", err)
	}
	if got := pkg.Manifest.Contributions.Pages; len(got) != 1 || !reflect.DeepEqual(got[0], payload) {
		t.Fatalf("packaged pages = %v, want the one declared storage page %v", got, payload)
	}
	if id, _ := payload["id"].(string); id != PageStorage {
		t.Fatalf("packaged page id = %q, want %q", id, PageStorage)
	}
	items, ok := payload["items"].([]any)
	if !ok || len(items) != len(storagePage().Items) {
		t.Fatalf("packaged items = %v, want the %d declared items", payload["items"], len(storagePage().Items))
	}
	actions, ok := payload["actions"].([]any)
	if !ok || len(actions) != len(storagePage().Actions) {
		t.Fatalf("packaged actions = %v, want the %d declared actions", payload["actions"], len(storagePage().Actions))
	}
}

// TestPackageIsDeterministic requires the same declaration to yield the same
// structure and digest, which is what makes the package addressable by digest
// (SDK-C-4).
func TestPackageIsDeterministic(t *testing.T) {
	first, firstArchive, err := Package()
	if err != nil {
		t.Fatalf("pack the extension: %v", err)
	}
	second, secondArchive, err := Package()
	if err != nil {
		t.Fatalf("pack the extension again: %v", err)
	}
	if first.Digest != second.Digest {
		t.Fatalf("digest = %s then %s, want one digest per input", first.Digest, second.Digest)
	}
	if !bytes.Equal(firstArchive, secondArchive) {
		t.Fatal("the same declaration produced a different archive")
	}
}

// TestDigestCoversTheManifest requires the digest to change when the manifest
// changes, so an install can bind to the exact declaration it verified
// (SDK-C-4).
func TestDigestCoversTheManifest(t *testing.T) {
	pkg, _, err := Package()
	if err != nil {
		t.Fatalf("pack the extension: %v", err)
	}
	changed := manifestDocument(t, pkg.Manifest)
	changed["version"] = "1.0.1"
	repacked, _, err := packaging.Pack(changed, nil)
	if err != nil {
		t.Fatalf("pack the changed declaration: %v", err)
	}
	if repacked.Digest == pkg.Digest {
		t.Fatal("the digest did not change with the manifest")
	}
}

// TestPackRejectsMissingFieldAndOutOfRangePermission requires the packing path
// to refuse a manifest that lacks a required field or names a permission
// outside the contract, and to produce no artifact (SDK-C-3).
func TestPackRejectsMissingFieldAndOutOfRangePermission(t *testing.T) {
	pkg, _, err := Package()
	if err != nil {
		t.Fatalf("pack the extension: %v", err)
	}
	base := manifestDocument(t, pkg.Manifest)

	missing := manifestDocument(t, pkg.Manifest)
	delete(missing, "frameworkVersion")
	if issue := packIssue(t, missing); issue.Path != "$.frameworkVersion" || issue.Code != validate.CodeMissingField {
		t.Fatalf("missing frameworkVersion = %s, want a missing-field issue at $.frameworkVersion", issue)
	}

	outOfRange := manifestDocument(t, pkg.Manifest)
	outOfRange["permissions"].(map[string]any)["write"] = []any{map[string]any{
		"apiGroup": apiGroupOf(base), "resources": []any{"volumes"}, "verbs": []any{"destroy"},
	}}
	if issue := packIssue(t, outOfRange); issue.Path != "$.permissions.write[0].verbs[0]" || issue.Code != validate.CodeInvalidValue {
		t.Fatalf("out-of-range verb = %s, want an invalid-value issue at $.permissions.write[0].verbs[0]", issue)
	}
}

// TestVerifyAppliesTheReadBudget requires the artifact to stay readable inside
// the caller's budget and the default budget, and to be refused as unreadable
// — with the exceeded dimension named and no partial content — once either
// budget is exceeded (SDK-C-4).
func TestVerifyAppliesTheReadBudget(t *testing.T) {
	pkg, archive, err := Package()
	if err != nil {
		t.Fatalf("pack the extension: %v", err)
	}
	entries, bytes := archiveSize(t, archive)

	within, err := packaging.VerifyWithLimits(archive, pkg.Digest, packaging.Limits{Entries: entries, UncompressedBytes: bytes})
	if err != nil {
		t.Fatalf("verify inside a caller budget of %d entries and %d bytes: %v", entries, bytes, err)
	}
	if within.Digest != pkg.Digest {
		t.Fatalf("verified digest = %s, want %s", within.Digest, pkg.Digest)
	}
	if _, err := packaging.Verify(archive, pkg.Digest); err != nil {
		t.Fatalf("verify under the default budget: %v", err)
	}

	if _, err := packaging.VerifyWithLimits(archive, pkg.Digest, packaging.Limits{Entries: entries - 1}); !strings.Contains(errText(err), "entries") {
		t.Fatalf("an artifact with %d entries under a %d entry budget = %v, want an entry-count refusal", entries, entries-1, err)
	}
	over, err := packaging.VerifyWithLimits(archive, pkg.Digest, packaging.Limits{UncompressedBytes: bytes - 1})
	if !strings.Contains(errText(err), "byte budget") {
		t.Fatalf("an artifact of %d bytes under a %d byte budget = %v, want a byte-budget refusal", bytes, bytes-1, err)
	}
	if over.Digest != "" || over.Manifest.ID != "" {
		t.Fatalf("a refused package returned partial content: %+v", over)
	}
}

// TestInstallDeclarationMatchesThePackage requires the kernel install form to
// carry the packaged page and the packaged retention, so installing the
// artifact serves what the package declares (KOSD-C-17, KOSD-C-18).
func TestInstallDeclarationMatchesThePackage(t *testing.T) {
	pkg, _, err := Package()
	if err != nil {
		t.Fatalf("pack the extension: %v", err)
	}
	declaration, err := InstallDeclaration(pkg)
	if err != nil {
		t.Fatalf("build the install declaration: %v", err)
	}
	if declaration.Identifier != pkg.Manifest.ID || declaration.Version != pkg.Manifest.Version {
		t.Fatalf("install form identity = %s@%s, want %s@%s",
			declaration.Identifier, declaration.Version, pkg.Manifest.ID, pkg.Manifest.Version)
	}
	if declaration.FrameworkVersion != pkg.Manifest.FrameworkVersion {
		t.Fatalf("install form framework = %q, want %q", declaration.FrameworkVersion, pkg.Manifest.FrameworkVersion)
	}
	if len(declaration.Contributions.Pages) != 1 {
		t.Fatalf("install form pages = %d, want the one packaged page", len(declaration.Contributions.Pages))
	}
	got := jsonValue(t, declaration.Contributions.Pages[0])
	if want := pkg.Manifest.Contributions.Pages[0]; !reflect.DeepEqual(got, want) {
		t.Fatalf("install form page = %v, want the packaged page %v", got, want)
	}
	if got, want := declaration.Retention, []extension.Retention{{Resource: "pages", Keep: false}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("install form retention = %+v, want %+v", got, want)
	}
	if len(declaration.Contributions.ObjectClasses) != 0 || len(declaration.Contributions.Controllers) != 0 {
		t.Fatalf("install form declares contributions the package does not: %+v", declaration.Contributions)
	}
}

// TestInstallDeclarationRefusesAnUncarriedClass requires the install form to
// refuse a package whose contribution class it cannot carry, instead of
// installing the package as if that class were absent (KOSD-C-18).
func TestInstallDeclarationRefusesAnUncarriedClass(t *testing.T) {
	pkg, _, err := Package()
	if err != nil {
		t.Fatalf("pack the extension: %v", err)
	}
	document := manifestDocument(t, pkg.Manifest)
	document["contributions"].(map[string]any)["settings"] = []any{map[string]any{"id": "storage"}}
	changed, _, err := packaging.Pack(document, nil)
	if err != nil {
		t.Fatalf("pack the changed declaration: %v", err)
	}
	if _, err := InstallDeclaration(changed); err == nil || !strings.Contains(err.Error(), "settings") {
		t.Fatalf("install form of a settings contribution = %v, want a refusal naming the class", err)
	}
}

// manifestDocument renders a packaged manifest as its JSON object.
func manifestDocument(t *testing.T, declaration manifest.ExtensionManifest) map[string]any {
	t.Helper()
	return jsonValue(t, declaration)
}

// jsonValue renders one value as the JSON object or array a reader sees.
func jsonValue(t *testing.T, value any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode %T: %v", value, err)
	}
	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatalf("decode %T: %v", value, err)
	}
	return document
}

// packIssue packs a document that must be refused and returns its first issue.
func packIssue(t *testing.T, document map[string]any) validate.Issue {
	t.Helper()
	pkg, archive, err := packaging.Pack(document, nil)
	var invalid *validate.InvalidError
	if !errors.As(err, &invalid) {
		t.Fatalf("pack = %v (package %+v, archive %d bytes), want a rejection", err, pkg, len(archive))
	}
	if len(invalid.Issues) == 0 {
		t.Fatal("a rejection carried no issue")
	}
	return invalid.Issues[0]
}

// archiveSize returns the entry count and the uncompressed content size of one
// package archive.
func archiveSize(t *testing.T, archive []byte) (int, int64) {
	t.Helper()
	decompressed, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("open the package archive: %v", err)
	}
	defer decompressed.Close()
	reader := tar.NewReader(decompressed)
	entries := 0
	var content int64
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("read the package archive: %v", err)
		}
		entries++
		content += header.Size
		if _, err := io.Copy(io.Discard, reader); err != nil {
			t.Fatalf("drain %q: %v", header.Name, err)
		}
	}
	return entries, content
}

// sortedKeys returns a JSON object's keys in a stable order.
func sortedKeys(document map[string]any) []string {
	keys := make([]string, 0, len(document))
	for key := range document {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// sortedCopy returns a sorted copy of a key list.
func sortedCopy(keys []string) []string {
	ordered := append([]string(nil), keys...)
	sort.Strings(ordered)
	return ordered
}

// apiGroupOf reads the kernel API group the manifest declares a read scope on.
func apiGroupOf(document map[string]any) string {
	read, _ := document["permissions"].(map[string]any)["read"].([]any)
	if len(read) == 0 {
		return ""
	}
	group, _ := read[0].(map[string]any)["apiGroup"].(string)
	return group
}

// isKosRole reports whether one name is a role of the KOS role model.
func isKosRole(name string) bool {
	for _, role := range manifest.KosRoles {
		if role == name {
			return true
		}
	}
	return false
}

// errText renders an error for a message check.
func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
