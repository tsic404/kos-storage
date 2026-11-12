// Package page holds the page-reading helpers the storage acceptance cases
// share, so every case asserts the projection the control plane answers
// (KOSD-C-53).
package page

import (
	"net/http"
	"testing"

	"github.com/tsic404/kosd/pkg/extension"
	"github.com/tsic404/kosd/pkg/pages"
	"github.com/tsic404/kosd/tests/acceptance/harness"

	"github.com/tsic404/kos-storage/pkg/storage"
)

// Start wires the storage readings into a fresh shell and installs the storage
// package, so the page is served exactly as an installation serves it.
func Start(t *testing.T, cluster *harness.Cluster) (*harness.Shell, *storage.Readings) {
	t.Helper()
	readings := &storage.Readings{Dynamic: cluster.Dynamic}
	shell := cluster.StartShell(t, func(config *pages.Config) {
		config.Readings = pages.Router{storage.Identifier: readings}
	})
	readings.Now = shell.Accounts.Clock.Now
	installation := cluster.InstallPackageWith(t, "storage", storage.Declaration(), extension.MemoryArtifacts{}, shell.Retention())
	shell.Use(installation)
	return shell, readings
}

// Read reads one page through the control-plane API.
func Read(t *testing.T, shell *harness.Shell, token, id string) map[string]any {
	t.Helper()
	status, body := shell.Call(t, http.MethodGet, "/api/v1/pages/"+id, nil, token)
	if status != http.StatusOK {
		t.Fatalf("read page %s = %d %v", id, status, body)
	}
	return body
}

// Items indexes one rendered page's items by identity.
func Items(t *testing.T, rendered map[string]any) map[string]map[string]any {
	t.Helper()
	raw, ok := rendered["items"].([]any)
	if !ok {
		t.Fatalf("page has no items: %v", rendered)
	}
	items := make(map[string]map[string]any, len(raw))
	for _, entry := range raw {
		item, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("item is not an object: %v", entry)
		}
		items[harness.String(item, "id")] = item
	}
	return items
}

// Item reads one item off a rendered page.
func Item(t *testing.T, rendered map[string]any, id string) map[string]any {
	t.Helper()
	item, known := Items(t, rendered)[id]
	if !known {
		t.Fatalf("page %q has no item %q", harness.String(rendered, "id"), id)
	}
	return item
}

// Object reads one item's value as an object.
func Object(t *testing.T, rendered map[string]any, id string) map[string]any {
	t.Helper()
	value, ok := Item(t, rendered, id)["value"].(map[string]any)
	if !ok {
		t.Fatalf("item %q value = %v, want an object", id, Item(t, rendered, id)["value"])
	}
	return value
}

// List reads one item's value as a list of records.
func List(t *testing.T, rendered map[string]any, id string) []map[string]any {
	t.Helper()
	raw, ok := Item(t, rendered, id)["value"].([]any)
	if !ok {
		t.Fatalf("item %q value = %v, want a list", id, Item(t, rendered, id)["value"])
	}
	records := make([]map[string]any, 0, len(raw))
	for _, entry := range raw {
		record, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("item %q record is not an object: %v", id, entry)
		}
		records = append(records, record)
	}
	return records
}

// Find returns the record naming one object.
func Find(t *testing.T, records []map[string]any, name string) map[string]any {
	t.Helper()
	for _, record := range records {
		if harness.String(record, "name") == name {
			return record
		}
	}
	t.Fatalf("no record names %q: %v", name, records)
	return nil
}

// FindIn returns the record naming one namespaced object.
func FindIn(t *testing.T, records []map[string]any, namespace, name string) map[string]any {
	t.Helper()
	for _, record := range records {
		if harness.String(record, "namespace") == namespace && harness.String(record, "name") == name {
			return record
		}
	}
	t.Fatalf("no record names %s/%s: %v", namespace, name, records)
	return nil
}

// Records reads one nested list field of an object value as records.
func Records(t *testing.T, object map[string]any, field string) []map[string]any {
	t.Helper()
	raw, ok := object[field].([]any)
	if !ok {
		t.Fatalf("field %q = %v, want a list", field, object[field])
	}
	records := make([]map[string]any, 0, len(raw))
	for _, entry := range raw {
		record, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("field %q record is not an object: %v", field, entry)
		}
		records = append(records, record)
	}
	return records
}

// Actions indexes one rendered page's actions by identity.
func Actions(t *testing.T, rendered map[string]any) map[string]map[string]any {
	t.Helper()
	raw, ok := rendered["actions"].([]any)
	if !ok {
		return map[string]map[string]any{}
	}
	actions := make(map[string]map[string]any, len(raw))
	for _, entry := range raw {
		action, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("action is not an object: %v", entry)
		}
		actions[harness.String(action, "id")] = action
	}
	return actions
}

// RequireSources requires every item's source type and observation time and
// asserts the item list matches the declared set (KOSD-C-53).
func RequireSources(t *testing.T, rendered map[string]any, want map[string]string) {
	t.Helper()
	items := Items(t, rendered)
	if len(items) != len(want) {
		t.Fatalf("page %q has %d items, want %d", harness.String(rendered, "id"), len(items), len(want))
	}
	for id, source := range want {
		item, known := items[id]
		if !known {
			t.Errorf("page %q is missing item %q", harness.String(rendered, "id"), id)
			continue
		}
		if harness.String(item, "source") != source {
			t.Errorf("item %q source = %q, want %q", id, harness.String(item, "source"), source)
		}
		if harness.String(item, "observedAt") == "" {
			t.Errorf("item %q carries no observation time", id)
		}
	}
}
