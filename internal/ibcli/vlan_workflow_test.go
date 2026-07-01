package ibcli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func vlanWorkflowApp(t *testing.T, server, readServer string) (*App, *bytes.Buffer) {
	return dnsWorkflowApp(t, server, readServer)
}

func vlanListServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		switch trimWAPIPath(r.URL.Path) {
		case networkObject:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{
					{
						"network":      "192.0.2.0/24",
						"network_view": "default",
						"vlans":        []map[string]any{{"vlan": "vlan/ZG5zLnZsYW4kLmNvbS5pbmZvYmxveC5kbnMudmxhbl92aWV3JEF1Y3RsYW5kLjEuNDA5NC4xMjM:Actland/VLAN_123/123", "name": "Users"}},
						"comment":      "Production hosts",
					},
					{
						"network":      "192.0.3.0/24",
						"network_view": "default",
						"vlans":        []map[string]any{{"vlan": "vlan/ZG5zLnZsYW4kLmNvbS5pbmZvYmxveC5kbnMudmxhbl92aWV3JEF1Y3RsYW5kLjEuNDA5NC4xMjI:Actland/VLAN_122/122", "name": "Voice"}},
						"comment":      "Phones",
					},
				},
			})
		case networkContainerObject:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{
					{
						"network":      "192.0.0.0/16",
						"network_view": "default",
						"vlans":        []map[string]any{{"vlan": "vlan/ZG5zLnZsYW4kLmNvbS5pbmZvYmxveC5kbnMudmxhbl92aWV3JEJ1bmRvb3JhLjEuNDA5NC40:Bundoora/VLAN_4/4", "name": "Core"}},
						"comment":      "Container",
					},
				},
			})
		default:
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
	}))
}

func TestVLANListIncludesDefaultColumns(t *testing.T) {
	server := vlanListServer(t)
	defer server.Close()

	app, stdout := vlanWorkflowApp(t, server.URL, server.URL)
	if err := app.Execute([]string{"-o", "json", "vlan", "list", "--network-view", "default"}); err != nil {
		t.Fatalf("vlan list: %v\nstdout:\n%s", err, stdout.String())
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &rows); err != nil {
		t.Fatalf("decode vlans: %v\n%s", err, stdout.String())
	}
	if len(rows) != 3 {
		t.Fatalf("vlan rows = %#v", rows)
	}
	// Sorted by vlan_id: 4 (Core), 122 (Voice), 123 (Users)
	if got, want := cleanString(rows[0]["vlan_id"]), "4"; got != want {
		t.Fatalf("rows[0] vlan_id = %q, want %q; row=%#v", got, want, rows[0])
	}
	if got, want := cleanString(rows[0]["name"]), "Core"; got != want {
		t.Fatalf("rows[0] name = %q, want %q; row=%#v", got, want, rows[0])
	}
	if got, want := cleanString(rows[0]["networks"]), "192.0.0.0/16"; got != want {
		t.Fatalf("rows[0] networks = %q, want %q; row=%#v", got, want, rows[0])
	}
	if got, want := cleanString(rows[0]["parent"]), "Bundoora"; got != want {
		t.Fatalf("rows[0] parent = %q, want %q; row=%#v", got, want, rows[0])
	}
	if got, want := strings.Join(sortedKeys(rows[0]), ","), "comment,name,networks,parent,vlan_id"; got != want {
		t.Fatalf("default columns = %q, want %q; row=%#v", got, want, rows[0])
	}
}

func TestVLANListSelectsColumns(t *testing.T) {
	server := vlanListServer(t)
	defer server.Close()

	app, stdout := vlanWorkflowApp(t, server.URL, server.URL)
	if err := app.Execute([]string{"-o", "json", "vlan", "list", "--network-view", "default", "--columns", "vlan_id,name,network_view"}); err != nil {
		t.Fatalf("vlan list: %v\nstdout:\n%s", err, stdout.String())
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &rows); err != nil {
		t.Fatalf("decode vlans: %v\n%s", err, stdout.String())
	}
	if got, want := strings.Join(sortedKeys(rows[0]), ","), "name,network_view,vlan_id"; got != want {
		t.Fatalf("selected columns = %q, want %q; row=%#v", got, want, rows[0])
	}
}

func TestVLANSarchMatchesAndSorts(t *testing.T) {
	server := vlanListServer(t)
	defer server.Close()

	app, stdout := vlanWorkflowApp(t, server.URL, server.URL)
	if err := app.Execute([]string{
		"-o", "json", "vlan", "search", "user",
		"--network-view", "default",
		"--sort=-vlan_id",
		"--columns", "vlan_id,name,networks",
	}); err != nil {
		t.Fatalf("vlan search: %v\nstdout:\n%s", err, stdout.String())
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &rows); err != nil {
		t.Fatalf("decode vlans: %v\n%s", err, stdout.String())
	}
	if len(rows) != 1 {
		t.Fatalf("vlan rows = %#v", rows)
	}
	if got, want := cleanString(rows[0]["vlan_id"]), "123"; got != want {
		t.Fatalf("vlan_id = %q, want %q; row=%#v", got, want, rows[0])
	}
	if got, want := cleanString(rows[0]["name"]), "Users"; got != want {
		t.Fatalf("name = %q, want %q; row=%#v", got, want, rows[0])
	}
}

func TestVLANShowByID(t *testing.T) {
	server := vlanListServer(t)
	defer server.Close()

	app, stdout := vlanWorkflowApp(t, server.URL, server.URL)
	if err := app.Execute([]string{"-o", "json", "vlan", "show", "123", "--network-view", "default"}); err != nil {
		t.Fatalf("vlan show: %v\nstdout:\n%s", err, stdout.String())
	}
	var row map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &row); err != nil {
		t.Fatalf("decode vlan: %v\n%s", err, stdout.String())
	}
	if got, want := cleanString(row["vlan_id"]), "123"; got != want {
		t.Fatalf("vlan_id = %q, want %q; row=%#v", got, want, row)
	}
	if got, want := cleanString(row["name"]), "Users"; got != want {
		t.Fatalf("name = %q, want %q; row=%#v", got, want, row)
	}
	if got, want := cleanString(row["networks"]), "192.0.2.0/24"; got != want {
		t.Fatalf("networks = %q, want %q; row=%#v", got, want, row)
	}
	if got, want := cleanString(row["parent"]), "Actland"; got != want {
		t.Fatalf("parent = %q, want %q; row=%#v", got, want, row)
	}
}

func TestVLANShowByName(t *testing.T) {
	server := vlanListServer(t)
	defer server.Close()

	app, stdout := vlanWorkflowApp(t, server.URL, server.URL)
	if err := app.Execute([]string{"-o", "json", "vlan", "show", "Core", "--network-view", "default"}); err != nil {
		t.Fatalf("vlan show: %v\nstdout:\n%s", err, stdout.String())
	}
	var row map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &row); err != nil {
		t.Fatalf("decode vlan: %v\n%s", err, stdout.String())
	}
	if got, want := cleanString(row["vlan_id"]), "4"; got != want {
		t.Fatalf("vlan_id = %q, want %q; row=%#v", got, want, row)
	}
}

func TestVLANShowNotFound(t *testing.T) {
	server := vlanListServer(t)
	defer server.Close()

	app, _ := vlanWorkflowApp(t, server.URL, server.URL)
	err := app.Execute([]string{"-o", "json", "vlan", "show", "999", "--network-view", "default"})
	if err == nil {
		t.Fatalf("expected error for missing vlan")
	}
}

func TestVLANCreateReturnsUnsupportedError(t *testing.T) {
	server := vlanListServer(t)
	defer server.Close()

	app, _ := vlanWorkflowApp(t, server.URL, server.URL)
	err := app.Execute([]string{"vlan", "create", "200", "NewVLAN"})
	if err == nil {
		t.Fatalf("expected unsupported error for vlan create")
	}
	if !strings.Contains(err.Error(), "does not support VLAN create") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVLANDeleteReturnsUnsupportedError(t *testing.T) {
	server := vlanListServer(t)
	defer server.Close()

	app, _ := vlanWorkflowApp(t, server.URL, server.URL)
	err := app.Execute([]string{"vlan", "delete", "100", "-y"})
	if err == nil {
		t.Fatalf("expected unsupported error for vlan delete")
	}
	if !strings.Contains(err.Error(), "does not support VLAN delete") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVLANListFallsBackWhenVLANFieldsUnsupported(t *testing.T) {
	networkRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		switch trimWAPIPath(r.URL.Path) {
		case networkObject:
			networkRequests++
			if strings.Contains(r.URL.Query().Get("_return_fields"), "vlans") {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{"Error": "AdmConProtoError: Unknown argument/field: 'vlans'"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{{"network": "192.0.2.0/24", "network_view": "default", "comment": "Production"}},
			})
		case networkContainerObject:
			if strings.Contains(r.URL.Query().Get("_return_fields"), "vlans") {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{"text": "Unknown argument/field: 'vlans'"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{{"network": "192.0.0.0/16", "network_view": "default", "comment": "Container"}},
			})
		default:
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	app, stdout := vlanWorkflowApp(t, server.URL, server.URL)
	if err := app.Execute([]string{"-o", "json", "vlan", "list", "--network-view", "default", "--refresh"}); err != nil {
		t.Fatalf("vlan list: %v\nstdout:\n%s", err, stdout.String())
	}
	if networkRequests < 2 {
		t.Fatalf("network requests = %d, want >= 2 (retry after vlans rejected)", networkRequests)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &rows); err != nil {
		t.Fatalf("decode vlans: %v\n%s", err, stdout.String())
	}
	// No vlans field -> no VLAN rows derived; empty result is correct.
	if len(rows) != 0 {
		t.Fatalf("expected no vlan rows when vlans field unsupported, got %#v", rows)
	}
}

func TestVLANSortFields(t *testing.T) {
	for _, raw := range []string{"vlan_id", "-name", "parent", "network_view", "networks", "comment"} {
		if _, err := parseVLANSort(raw, true); err != nil {
			t.Fatalf("parseVLANSort(%q): %v", raw, err)
		}
	}
	if _, err := parseVLANSort("bogus", true); err == nil {
		t.Fatalf("parseVLANSort(bogus) should error")
	}
}

func TestVLANColumnsValidation(t *testing.T) {
	cols, err := parseVLANColumns("vlan_id,name,networks")
	if err != nil {
		t.Fatalf("parseVLANColumns: %v", err)
	}
	if got, want := strings.Join(cols, ","), "vlan_id,name,networks"; got != want {
		t.Fatalf("columns = %q, want %q", got, want)
	}
	if _, err := parseVLANColumns("bogus"); err == nil {
		t.Fatalf("parseVLANColumns(bogus) should error")
	}
}

func TestFlattenVLANFields(t *testing.T) {
	assigned, name, entries := flattenVLANFields([]any{
		map[string]any{"vlan": "vlan/ZG5zLnZsYW4kLmNvbS5pbmZvYmxveC5kbnMudmxhbl92aWV3JEF1Y3RsYW5kLjEuNDA5NC4xMjM:Actland/VLAN_123/123", "name": "Users"},
		map[string]any{"vlan": "vlan/ZG5zLnZsYW4kLmNvbS5pbmZvYmxveC5kbnMudmxhbl92aWV3JEF1Y3RsYW5kLjEuNDA5NC40NTY:Actland/VLAN_456/456"},
	})
	if assigned != "123" {
		t.Fatalf("assigned = %q, want 123", assigned)
	}
	if name != "Users" {
		t.Fatalf("name = %q, want Users", name)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	if got, want := entries[0].ID, "123"; got != want {
		t.Fatalf("entries[0] id = %q, want %q", got, want)
	}
	if got, want := entries[0].Parent, "Actland"; got != want {
		t.Fatalf("entries[0] parent = %q, want %q", got, want)
	}
	if got, want := entries[1].Name, "VLAN_456"; got != want {
		t.Fatalf("entries[1] name = %q, want %q (from ref)", got, want)
	}
	if got, want := entries[1].ID, "456"; got != want {
		t.Fatalf("entries[1] id = %q, want %q", got, want)
	}
	// empty/nil input
	assigned2, name2, entries2 := flattenVLANFields(nil)
	if assigned2 != "" || name2 != "" || len(entries2) != 0 {
		t.Fatalf("empty flatten = %q %q %d, want empty", assigned2, name2, len(entries2))
	}
}

func TestParseVLANRef(t *testing.T) {
	tests := []struct {
		raw    string
		id     string
		name   string
		parent string
		ok     bool
	}{
		{"vlan/ZG5zLnZsYW4kLmNvbS5pbmZvYmxveC5kbnMudmxhbl92aWV3JEJ1bmRvb3JhLjEuNDA5NC40:Bundoora/VLAN_4/4", "4", "VLAN_4", "Bundoora", true},
		{"vlan/ZG5z...:Multi/View/Name/10", "10", "View/Name", "Multi", true},
		{"vlan/ZG5z...:View/Name/4094", "4094", "Name", "View", true},
		{"123", "", "", "", false},
		{"", "", "", "", false},
		{"network/ZG5z...:foo/bar/1", "", "", "", false},
		{"vlan/no-colon-here", "", "", "", false},
		{"vlan/ZG5z...:only-one-segment", "", "", "", false},
	}
	for _, tc := range tests {
		id, name, parent, ok := parseVLANRef(tc.raw)
		if ok != tc.ok {
			t.Fatalf("parseVLANRef(%q) ok = %v, want %v", tc.raw, ok, tc.ok)
		}
		if id != tc.id || name != tc.name || parent != tc.parent {
			t.Fatalf("parseVLANRef(%q) = id=%q name=%q parent=%q, want id=%q name=%q parent=%q", tc.raw, id, name, parent, tc.id, tc.name, tc.parent)
		}
	}
}
