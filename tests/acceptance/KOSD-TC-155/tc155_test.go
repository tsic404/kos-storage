// Package kosdtc155 installs the packaged storage extension as a system
// component and removes it again, judging the page and its entries on the way in
// and on the way out (KOSD-AC-72, KOSD-TC-155).
package kosdtc155

import (
	"net/http"
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/tsic404/kosd/api"
	"github.com/tsic404/kosd/pkg/extension"
	"github.com/tsic404/kosd/pkg/write"
	"github.com/tsic404/kosd/tests/acceptance/harness"

	"github.com/tsic404/kos-storage/pkg/storage"
	"github.com/tsic404/kos-storage/tests/acceptance/page"
)

const (
	ownerName     = "owner"
	ownerPassword = "owner-password"
)

// storedItems are the display items STORAGE-C-1 and STORAGE-C-82 make the page
// show, with their source types; the two entries are the deletion and migration
// actions STORAGE-C-81 declares.
var storedItems = []struct {
	id     string
	source string
}{
	{id: storage.ItemDevicesAndPolicies, source: extension.PageSourceObject},
	{id: storage.ItemVolumes, source: extension.PageSourceObject},
	{id: storage.ItemUsage, source: extension.PageSourceMetric},
	{id: storage.ItemBackups, source: extension.PageSourceObject},
	{id: storage.ItemDeletion, source: extension.PageSourceObject},
	{id: storage.ItemMigration, source: extension.PageSourceObject},
}

// storedActions are the two entries the page offers (STORAGE-C-1, STORAGE-C-81).
var storedActions = []string{storage.ActionDeleteVolume, storage.ActionStartMigration}

// TestTC155PackageLoadsAndUnloadsTheStoragePage creates a system component for
// the artifact this repository packs, then deletes it. The page and its entries
// appear with the component and disappear with it, the other installed product
// stays untouched, and the kernel object model keeps its kinds and writers.
func TestTC155PackageLoadsAndUnloadsTheStoragePage(t *testing.T) {
	cluster := harness.Start(t)
	shell, _ := page.StartShell(t, cluster)
	owner := shell.SignInOwner(t, ownerName, ownerPassword)
	kindsBefore := api.Kinds()
	ownedBefore := write.OwnedKinds()

	// The other product installs through the same path and must survive the
	// storage product's own uninstall.
	artifacts := extension.MemoryArtifacts{}
	other := cluster.InstallPackage(t, otherComponent, otherDeclaration(), artifacts)
	if _, visible := other.Registry().PageFor(otherPage, write.RoleMember); !visible {
		t.Fatal("the other product's page is not visible after its install")
	}

	// A component naming a digest the artifact does not match is refused, and
	// no contribution of it becomes visible (KOSD-C-17).
	reference, digest, content := page.Artifact(t)
	artifacts[reference] = content
	cluster.Create(t, &unstructured.Unstructured{Object: harness.Component(page.ComponentName, reference, mismatchedDigest)})
	refused := cluster.ExtensionState(t, artifacts)
	result, found := refused.Component(page.ComponentName)
	if !found || result.Phase != extension.PhaseUnavailable || result.Reason != extension.ReasonDigestMismatch {
		t.Fatalf("component with a mismatched digest = %+v (found %v), want unavailable/%s",
			result, found, extension.ReasonDigestMismatch)
	}
	if _, visible := refused.Registry().PageFor(storage.PageStorage, write.RoleOwner); visible {
		t.Fatal("the refused component served its page")
	}

	// A component naming the artifact's own digest installs it.
	cluster.RepointComponent(t, page.ComponentName, reference, digest)
	installed := cluster.ExtensionState(t, artifacts)
	result, found = installed.Component(page.ComponentName)
	if !found || result.Phase != extension.PhaseReady {
		t.Fatalf("component after the digest matched = %+v (found %v), want ready", result, found)
	}
	if result.Identifier != storage.Identifier || result.Version != storage.Version {
		t.Fatalf("installed component = %s@%s, want %s@%s",
			result.Identifier, result.Version, storage.Identifier, storage.Version)
	}
	assertPage(t, installed, "after the install")
	assertFrameless(t, installed)

	// The served page carries the declared items and entries.
	shell.Use(installed)
	rendered := page.Read(t, shell, owner, storage.PageStorage)
	requireItems(t, rendered)
	requireActions(t, rendered)
	if _, visible := installed.Registry().PageFor(otherPage, write.RoleMember); !visible {
		t.Fatal("the storage install withdrew the other product's page")
	}

	// Uninstall: the page and its entries disappear with the component, and
	// nothing is retained (KOSD-C-29).
	uid := cluster.ComponentUID(t, page.ComponentName)
	cluster.DeleteComponent(t, page.ComponentName)
	removed := cluster.ExtensionState(t, artifacts)
	shell.Use(removed)
	if _, found := removed.Component(page.ComponentName); found {
		t.Fatal("the uninstalled component is still in the install set")
	}
	if _, visible := removed.Registry().PageFor(storage.PageStorage, write.RoleOwner); visible {
		t.Fatal("the uninstalled component still serves the storage page")
	}
	if _, visible := removed.Registry().PageFor(otherPage, write.RoleMember); !visible {
		t.Fatal("the storage uninstall withdrew the other product's page")
	}
	status, body := shell.Call(t, http.MethodGet, "/api/v1/pages/"+storage.PageStorage, nil, owner)
	if status == http.StatusOK {
		t.Fatalf("read the uninstalled page = %d %v, want an absent page", status, body)
	}
	if failure, ok := body["error"].(map[string]any); !ok || harness.String(failure, "code") != "not_found" {
		t.Fatalf("read the uninstalled page = %d %v, want not_found", status, body)
	}
	if residuals := cluster.Residuals(t, uid); len(residuals) != 0 {
		t.Fatalf("the uninstall retained %v, want nothing", residuals)
	}
	assertFrameless(t, removed)
	if !reflect.DeepEqual(api.Kinds(), kindsBefore) {
		t.Fatalf("the kernel kind set changed: %v, want %v", api.Kinds(), kindsBefore)
	}
	if !reflect.DeepEqual(write.OwnedKinds(), ownedBefore) {
		t.Fatalf("the writer table changed: %v, want %v", write.OwnedKinds(), ownedBefore)
	}
}

// otherComponent and otherPage name the product the case keeps installed.
const (
	otherComponent = "sample"
	otherPage      = "sample"
)

// mismatchedDigest is a well-formed digest the artifact cannot match.
const mismatchedDigest = "sha256:0000000000000000000000000000000000000000000000000000000000000000"

// otherDeclaration is a second product installed over the same path: one page
// and nothing else, so its survival is attributable to the path, not the kind.
func otherDeclaration() extension.Declaration {
	return extension.Declaration{
		Identifier: "kos.sample",
		Version:    "1.0.0",
		Contributions: extension.Contributions{
			Pages: []extension.Page{{
				ID: otherPage, Title: "样例", Location: "panel",
				Items: []extension.PageItem{{
					ID: "entries", Label: "条目", Source: extension.PageSourceLocal,
					Store: otherPage, Reason: "只读展示",
				}},
			}},
		},
	}
}

// assertPage requires the storage page to be visible to a signed-in role.
func assertPage(t *testing.T, installation *extension.Installation, when string) {
	t.Helper()
	if _, visible := installation.Registry().PageFor(storage.PageStorage, write.RoleOwner); !visible {
		t.Fatalf("the storage page is not visible %s", when)
	}
}

// assertFrameless requires the installation to contribute no object class and
// no controller, so it cannot add a writer or serve a kernel kind.
func assertFrameless(t *testing.T, installation *extension.Installation) {
	t.Helper()
	if classes := installation.Registry().ObjectClasses(); len(classes) != 0 {
		t.Fatalf("the installation serves object classes %v, want none", classes)
	}
	if controllers := installation.Registry().Controllers(); len(controllers) != 0 {
		t.Fatalf("the installation contributes controllers %v, want none", controllers)
	}
}

// requireItems requires the rendered page to show exactly the declared items
// with their source types.
func requireItems(t *testing.T, rendered map[string]any) {
	t.Helper()
	items := page.Items(t, rendered)
	if len(items) != len(storedItems) {
		t.Fatalf("the page shows %d items, want %d", len(items), len(storedItems))
	}
	for _, want := range storedItems {
		item, known := items[want.id]
		if !known {
			t.Fatalf("the page is missing item %q", want.id)
		}
		if got := harness.String(item, "source"); got != want.source {
			t.Errorf("item %q source = %q, want %q", want.id, got, want.source)
		}
	}
	for _, id := range []string{storage.ItemDeletion, storage.ItemMigration} {
		if got := harness.String(items[id], "action"); got == "" {
			t.Errorf("item %q carries no entry", id)
		}
	}
}

// requireActions requires the page to offer exactly the deletion and migration
// entries, each naming a control-plane write.
func requireActions(t *testing.T, rendered map[string]any) {
	t.Helper()
	actions := page.Actions(t, rendered)
	if len(actions) != len(storedActions) {
		t.Fatalf("the page offers %d actions, want %d", len(actions), len(storedActions))
	}
	for _, id := range storedActions {
		action, known := actions[id]
		if !known {
			t.Fatalf("the page is missing action %q", id)
		}
		entry, ok := action["entry"].(map[string]any)
		if !ok || harness.String(entry, "method") == "" || harness.String(entry, "path") == "" {
			t.Errorf("action %q entry = %v, want a method and a path", id, action["entry"])
		}
	}
}
