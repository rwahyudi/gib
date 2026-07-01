package ibcli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNetViewList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || trimWAPIPath(r.URL.Path) != networkViewObject {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": []map[string]any{
				{"name": "prod", "comment": "Production"},
				{"name": "default", "comment": "Default view"},
			},
		})
	}))
	defer server.Close()

	app, stdout := dnsWorkflowApp(t, server.URL, server.URL)
	if err := app.Execute([]string{"-o", "json", "net", "view", "list"}); err != nil {
		t.Fatalf("net view list: %v\nstdout:\n%s", err, stdout.String())
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &rows); err != nil {
		t.Fatalf("decode network views: %v\n%s", err, stdout.String())
	}
	if len(rows) != 2 || cleanString(rows[0]["name"]) != "default" || cleanString(rows[1]["name"]) != "prod" {
		t.Fatalf("network view rows = %#v", rows)
	}

	stdout.Reset()
	app.Output = tableOutput
	if err := app.Execute([]string{"net", "view", "list"}); err != nil {
		t.Fatalf("net view table list: %v\nstdout:\n%s", err, stdout.String())
	}
	output := stdout.String()
	for _, want := range []string{"IPAM Network Views", "Current Context:", "Profile:", "default", "Rows:", "2"} {
		if !strings.Contains(output, want) {
			t.Fatalf("network view table missing %q:\n%s", want, output)
		}
	}
	for _, unwanted := range []string{"View:", "Zone:"} {
		if strings.Contains(output, unwanted) {
			t.Fatalf("network view table should not include DNS context %q:\n%s", unwanted, output)
		}
	}
}

func TestNetContextLineMarksGlobalProfile(t *testing.T) {
	app := testApp(t)
	writePlainTestConfig(t, app.GlobalConfigFile, "shared", map[string]Profile{
		"shared": plainTestProfile("shared", "https://shared.example"),
	}, "ibusers")

	line := app.netContextLine(3)
	for _, want := range []string{"Current Context:", "Profile: shared (global)", "Rows: 3"} {
		if !strings.Contains(line, want) {
			t.Fatalf("net context line missing %q:\n%s", want, line)
		}
	}
}

func TestNetListSearchesSortsAndSelectsColumns(t *testing.T) {
	var networkView string
	var containerView string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		switch trimWAPIPath(r.URL.Path) {
		case networkObject:
			networkView = r.URL.Query().Get("network_view")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{
					{"network": "10.0.0.0/24", "network_view": "default", "comment": "Lab"},
					{"network": "192.0.2.0/24", "network_view": "default", "comment": "Production hosts"},
				},
			})
		case networkContainerObject:
			containerView = r.URL.Query().Get("network_view")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{
					{"network": "10.0.0.0/8", "network_view": "default", "comment": "Lab container"},
				},
			})
		default:
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	app, stdout := dnsWorkflowApp(t, server.URL, server.URL)
	if err := app.Execute([]string{"-o", "json", "net", "list", "prod", "--network-view", "default", "--columns", "network,comment"}); err != nil {
		t.Fatalf("net list: %v\nstdout:\n%s", err, stdout.String())
	}
	if networkView != "default" {
		t.Fatalf("network_view query = %q, want default", networkView)
	}
	if containerView != "default" {
		t.Fatalf("container network_view query = %q, want default", containerView)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &rows); err != nil {
		t.Fatalf("decode networks: %v\n%s", err, stdout.String())
	}
	if len(rows) != 1 || cleanString(rows[0]["network"]) != "192.0.2.0/24" || cleanString(rows[0]["comment"]) != "Production hosts" {
		t.Fatalf("network rows = %#v", rows)
	}
	if _, ok := rows[0]["network_view"]; ok {
		t.Fatalf("network_view column should not be selected: %#v", rows[0])
	}
}

func TestNetListIncludesAssignedVLANColumnsByDefault(t *testing.T) {
	var networkReturnFields string
	var containerReturnFields string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		switch trimWAPIPath(r.URL.Path) {
		case networkObject:
			networkReturnFields = r.URL.Query().Get("_return_fields")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{
					{
						"network":            "192.0.2.0/24",
						"network_view":       "default",
						"assigned_vlan":      "123",
						"assigned_vlan_name": "Users",
						"comment":            "Production hosts",
					},
				},
			})
		case networkContainerObject:
			containerReturnFields = r.URL.Query().Get("_return_fields")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{
					{
						"network":            "192.0.0.0/16",
						"network_view":       "default",
						"assigned_vlan":      "100",
						"assigned_vlan_name": "Server-Core",
						"comment":            "Production container",
					},
				},
			})
		default:
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	app, stdout := dnsWorkflowApp(t, server.URL, server.URL)
	if err := app.Execute([]string{"-o", "json", "net", "list", "--network-view", "default"}); err != nil {
		t.Fatalf("net list: %v\nstdout:\n%s", err, stdout.String())
	}
	for _, fields := range []string{networkReturnFields, containerReturnFields} {
		if !strings.Contains(fields, "assigned_vlan") || !strings.Contains(fields, "assigned_vlan_name") {
			t.Fatalf("_return_fields = %q, want assigned VLAN fields", fields)
		}
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &rows); err != nil {
		t.Fatalf("decode networks: %v\n%s", err, stdout.String())
	}
	if len(rows) != 2 {
		t.Fatalf("network rows = %#v", rows)
	}
	if got, want := cleanString(rows[0]["assigned_vlan"]), "100"; got != want {
		t.Fatalf("container assigned_vlan = %q, want %q; row=%#v", got, want, rows[0])
	}
	if got, want := cleanString(rows[0]["assigned_vlan_name"]), "Server-Core"; got != want {
		t.Fatalf("container assigned_vlan_name = %q, want %q; row=%#v", got, want, rows[0])
	}
	if got, want := strings.Join(sortedKeys(rows[0]), ","), "assigned_vlan,assigned_vlan_name,comment,network,type"; got != want {
		t.Fatalf("default columns = %q, want %q; row=%#v", got, want, rows[0])
	}
}

func TestNetSearchMatchesSortsAndSelectsAssignedVLANColumns(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		switch trimWAPIPath(r.URL.Path) {
		case networkObject:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{
					{"network": "192.0.2.0/24", "network_view": "default", "assigned_vlan": "123", "assigned_vlan_name": "Users", "comment": "Production hosts"},
					{"network": "192.0.3.0/24", "network_view": "default", "assigned_vlan": "122", "assigned_vlan_name": "Voice", "comment": "Phones"},
				},
			})
		case networkContainerObject:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{
					{"network": "192.0.0.0/16", "network_view": "default", "assigned_vlan": "100", "assigned_vlan_name": "Core", "comment": "Container"},
				},
			})
		default:
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	app, stdout := dnsWorkflowApp(t, server.URL, server.URL)
	if err := app.Execute([]string{
		"-o", "json", "net", "search", "user",
		"--network-view", "default",
		"--sort=-assigned_vlan_name",
		"--columns", "network,assigned_vlan,assigned_vlan_name",
	}); err != nil {
		t.Fatalf("net search: %v\nstdout:\n%s", err, stdout.String())
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &rows); err != nil {
		t.Fatalf("decode networks: %v\n%s", err, stdout.String())
	}
	if len(rows) != 1 {
		t.Fatalf("network rows = %#v", rows)
	}
	if got, want := cleanString(rows[0]["network"]), "192.0.2.0/24"; got != want {
		t.Fatalf("network = %q, want %q; row=%#v", got, want, rows[0])
	}
	if got, want := cleanString(rows[0]["assigned_vlan"]), "123"; got != want {
		t.Fatalf("assigned_vlan = %q, want %q; row=%#v", got, want, rows[0])
	}
	if got, want := cleanString(rows[0]["assigned_vlan_name"]), "Users"; got != want {
		t.Fatalf("assigned_vlan_name = %q, want %q; row=%#v", got, want, rows[0])
	}
	if _, ok := rows[0]["comment"]; ok {
		t.Fatalf("comment column should not be selected: %#v", rows[0])
	}
}

func TestNetListIncludesNetworksAndContainers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		switch trimWAPIPath(r.URL.Path) {
		case networkViewObject:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{{"name": "default"}},
			})
		case networkObject:
			if got := r.URL.Query().Get("network_view"); got != "" && got != "default" {
				t.Fatalf("network_view query = %q, want blank or default", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{
					{"network": "192.0.2.0/24", "network_view": "default", "comment": "Production network"},
				},
			})
		case networkContainerObject:
			if got := r.URL.Query().Get("network_view"); got != "" && got != "default" {
				t.Fatalf("container network_view query = %q, want blank or default", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{
					{"network": "192.0.0.0/16", "network_view": "default", "comment": "Production container"},
				},
			})
		default:
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	app, stdout := dnsWorkflowApp(t, server.URL, server.URL)
	if err := app.Execute([]string{"-o", "json", "net", "list", "prod", "--sort", "type"}); err != nil {
		t.Fatalf("net list: %v\nstdout:\n%s", err, stdout.String())
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &rows); err != nil {
		t.Fatalf("decode networks: %v\n%s", err, stdout.String())
	}
	if len(rows) != 2 {
		t.Fatalf("network rows = %#v", rows)
	}
	if cleanString(rows[0]["type"]) != ipamTypeContainer || cleanString(rows[0]["network"]) != "192.0.0.0/16" {
		t.Fatalf("container row = %#v", rows[0])
	}
	if cleanString(rows[1]["type"]) != ipamTypeNetwork || cleanString(rows[1]["network"]) != "192.0.2.0/24" {
		t.Fatalf("network row = %#v", rows[1])
	}
}

func TestIPAMTypeColorMapsKnownTypes(t *testing.T) {
	tests := map[string]string{
		ipamTypeNetwork:   "#22c55e",
		ipamTypeContainer: "#f59e0b",
		"unknown":         "#94a3b8",
	}
	for itemType, want := range tests {
		if got := string(ipamTypeColor(itemType)); got != want {
			t.Fatalf("ipam type color for %q = %q, want %q", itemType, got, want)
		}
	}
}

func TestNetworkSizeColorMapsPrefixBuckets(t *testing.T) {
	tests := map[string]string{
		"10.0.0.0/8":       "#ef4444",
		"10.128.0.0/16":    "#f97316",
		"10.128.48.0/23":   "#f59e0b",
		"10.128.48.0/24":   "#22c55e",
		"10.128.48.128/25": "#06b6d4",
		"192.0.2.1/32":     "#06b6d4",
	}
	for cidr, want := range tests {
		got, ok := networkSizeColor(cidr)
		if !ok {
			t.Fatalf("network size color for %q returned ok=false", cidr)
		}
		if string(got) != want {
			t.Fatalf("network size color for %q = %q, want %q", cidr, got, want)
		}
	}
	if _, ok := networkSizeColor("not-a-cidr"); ok {
		t.Fatalf("network size color for invalid CIDR returned ok=true")
	}
}

func TestDefaultNetworkColumnsPutNetworkBeforeType(t *testing.T) {
	columns, err := parseNetworkColumns("")
	if err != nil {
		t.Fatalf("parse default network columns: %v", err)
	}
	if got, want := strings.Join(columns, ","), "network,type,assigned_vlan,assigned_vlan_name,comment"; got != want {
		t.Fatalf("default network columns = %q, want %q", got, want)
	}
}

func TestNetworkColumnsStillAllowNetworkViewWhenSelected(t *testing.T) {
	columns, err := parseNetworkColumns("network,type,network_view")
	if err != nil {
		t.Fatalf("parse selected network columns: %v", err)
	}
	if got, want := strings.Join(columns, ","), "network,type,network_view"; got != want {
		t.Fatalf("selected network columns = %q, want %q", got, want)
	}
}

func TestNetworkColumnsAllowAssignedVLANColumns(t *testing.T) {
	columns, err := parseNetworkColumns("network,assigned_vlan,assigned_vlan_name")
	if err != nil {
		t.Fatalf("parse assigned VLAN network columns: %v", err)
	}
	if got, want := strings.Join(columns, ","), "network,assigned_vlan,assigned_vlan_name"; got != want {
		t.Fatalf("selected network columns = %q, want %q", got, want)
	}
}

func TestNetSortAllowsAssignedVLANFields(t *testing.T) {
	for _, raw := range []string{"assigned_vlan", "-assigned_vlan_name"} {
		option, err := parseNetSort(raw, true)
		if err != nil {
			t.Fatalf("parseNetSort(%q): %v", raw, err)
		}
		if !option.Enabled {
			t.Fatalf("parseNetSort(%q) disabled sort", raw)
		}
	}
}

func TestNetNextIPColumnsHideNetworkView(t *testing.T) {
	if got, want := strings.Join(netNextIPOutputColumns, ","), "network,type,ip"; got != want {
		t.Fatalf("net next-ip columns = %q, want %q", got, want)
	}
}

func TestNetTableOutputStylesObjectTypes(t *testing.T) {
	app := testApp(t)
	var stdout strings.Builder
	app.Stdout = &stdout
	app.Output = tableOutput

	rows := []map[string]any{
		{"type": ipamTypeContainer, "network": "192.0.0.0/16", "network_view": "default", "comment": "Parent"},
		{"type": ipamTypeNetwork, "network": "192.0.2.0/24", "network_view": "default", "comment": "Child"},
	}
	if err := app.emitNetworkRows("IPAM Networks and Containers (2)", networkOutputColumns, rows); err != nil {
		t.Fatalf("emit network rows: %v", err)
	}
	output := stdout.String()
	for _, want := range []string{"CONTAINER", "NETWORK", "192.0.0.0/16", "192.0.2.0/24"} {
		if !strings.Contains(output, want) {
			t.Fatalf("network table missing %q:\n%s", want, output)
		}
	}
	for _, want := range []string{"Current Context:", "Profile:", "default", "Rows:", "2"} {
		if !strings.Contains(output, want) {
			t.Fatalf("network table footer missing %q:\n%s", want, output)
		}
	}
	for _, unwanted := range []string{"View:", "Zone:"} {
		if strings.Contains(output, unwanted) {
			t.Fatalf("network table footer should not include DNS context %q:\n%s", unwanted, output)
		}
	}
	if strings.Contains(output, "Network View") {
		t.Fatalf("network table should not include Network View by default:\n%s", output)
	}
}

func TestNetListWithoutNetworkViewQueriesAllViews(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		switch trimWAPIPath(r.URL.Path) {
		case networkViewObject:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{
					{"name": "default"},
					{"name": "lab"},
				},
			})
		case networkObject:
			switch r.URL.Query().Get("network_view") {
			case "":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"result": []map[string]any{{"network": "203.0.113.0/24", "network_view": "legacy", "comment": "Unscoped network"}},
				})
			case "default":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"result": []map[string]any{{"network": "10.0.1.0/24", "network_view": "default", "comment": "Default network"}},
				})
			case "lab":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"result": []map[string]any{{"network": "172.16.1.0/24", "network_view": "lab", "comment": "Lab network"}},
				})
			default:
				t.Fatalf("network query missing network_view: %s", r.URL.RawQuery)
			}
		case networkContainerObject:
			switch r.URL.Query().Get("network_view") {
			case "":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"result": []map[string]any{{"network": "203.0.0.0/16", "network_view": "legacy", "comment": "Unscoped container"}},
				})
			case "default":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"result": []map[string]any{{"network": "10.0.0.0/16", "network_view": "default", "comment": "Default container"}},
				})
			case "lab":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"result": []map[string]any{{"network": "172.16.0.0/16", "network_view": "lab", "comment": "Lab container"}},
				})
			default:
				t.Fatalf("container query missing network_view: %s", r.URL.RawQuery)
			}
		default:
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	app, stdout := dnsWorkflowApp(t, server.URL, server.URL)
	if err := app.Execute([]string{"-o", "json", "net", "list", "--columns", "network,type,network_view,comment"}); err != nil {
		t.Fatalf("net list: %v\nstdout:\n%s", err, stdout.String())
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &rows); err != nil {
		t.Fatalf("decode networks: %v\n%s", err, stdout.String())
	}
	if len(rows) != 6 {
		t.Fatalf("network rows = %#v", rows)
	}
	want := []struct {
		itemType string
		network  string
		view     string
	}{
		{ipamTypeContainer, "10.0.0.0/16", "default"},
		{ipamTypeNetwork, "10.0.1.0/24", "default"},
		{ipamTypeContainer, "172.16.0.0/16", "lab"},
		{ipamTypeNetwork, "172.16.1.0/24", "lab"},
		{ipamTypeContainer, "203.0.0.0/16", "legacy"},
		{ipamTypeNetwork, "203.0.113.0/24", "legacy"},
	}
	for index, expected := range want {
		if cleanString(rows[index]["type"]) != expected.itemType || cleanString(rows[index]["network"]) != expected.network || cleanString(rows[index]["network_view"]) != expected.view {
			t.Fatalf("row %d = %#v, want %s %s %s", index, rows[index], expected.itemType, expected.network, expected.view)
		}
	}
}

func TestNetSearchWithoutNetworkViewQueriesAllViews(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		switch trimWAPIPath(r.URL.Path) {
		case networkViewObject:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{
					{"name": "default"},
					{"name": "lab"},
				},
			})
		case networkObject:
			switch r.URL.Query().Get("network_view") {
			case "":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"result": []map[string]any{{"network": "203.0.113.0/24", "network_view": "legacy", "comment": "Shared legacy application"}},
				})
			case "default":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"result": []map[string]any{{"network": "10.0.1.0/24", "network_view": "default", "comment": "Default network"}},
				})
			case "lab":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"result": []map[string]any{{"network": "172.16.1.0/24", "network_view": "lab", "comment": "Shared application"}},
				})
			default:
				t.Fatalf("network query missing network_view: %s", r.URL.RawQuery)
			}
		case networkContainerObject:
			switch r.URL.Query().Get("network_view") {
			case "":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"result": []map[string]any{{"network": "203.0.0.0/16", "network_view": "legacy", "comment": "Shared legacy container"}},
				})
			case "default":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"result": []map[string]any{{"network": "10.0.0.0/16", "network_view": "default", "comment": "Default container"}},
				})
			case "lab":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"result": []map[string]any{{"network": "172.16.0.0/16", "network_view": "lab", "comment": "Shared container"}},
				})
			default:
				t.Fatalf("container query missing network_view: %s", r.URL.RawQuery)
			}
		default:
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	app, stdout := dnsWorkflowApp(t, server.URL, server.URL)
	if err := app.Execute([]string{"-o", "json", "net", "search", "shared", "--columns", "network,type,network_view,comment"}); err != nil {
		t.Fatalf("net search: %v\nstdout:\n%s", err, stdout.String())
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &rows); err != nil {
		t.Fatalf("decode networks: %v\n%s", err, stdout.String())
	}
	if len(rows) != 4 {
		t.Fatalf("network rows = %#v", rows)
	}
	if cleanString(rows[0]["type"]) != ipamTypeContainer || cleanString(rows[0]["network_view"]) != "lab" ||
		cleanString(rows[1]["type"]) != ipamTypeNetwork || cleanString(rows[1]["network_view"]) != "lab" ||
		cleanString(rows[2]["type"]) != ipamTypeContainer || cleanString(rows[2]["network_view"]) != "legacy" ||
		cleanString(rows[3]["type"]) != ipamTypeNetwork || cleanString(rows[3]["network_view"]) != "legacy" {
		t.Fatalf("search rows = %#v", rows)
	}
}

func TestNetSearchIncludesParentContainerForMatchingNetworkCIDR(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		switch trimWAPIPath(r.URL.Path) {
		case networkViewObject:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{{"name": "default"}},
			})
		case networkObject:
			if got := r.URL.Query().Get("network_view"); got != "" && got != "default" {
				t.Fatalf("network_view query = %q, want blank or default", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{
					{"network": "10.129.42.0/23", "network_view": "default", "comment": "SAP HANA DR"},
					{"network": "10.129.46.0/23", "network_view": "default", "comment": "SAP HANA Prod"},
				},
			})
		case networkContainerObject:
			if got := r.URL.Query().Get("network_view"); got != "" && got != "default" {
				t.Fatalf("container network_view query = %q, want blank or default", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{
					{"network": "10.0.0.0/8", "network_view": "default", "comment": "Internal"},
					{"network": "10.129.0.0/16", "network_view": "default", "comment": "Servers - Internal Only"},
				},
			})
		default:
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	app, stdout := dnsWorkflowApp(t, server.URL, server.URL)
	if err := app.Execute([]string{"-o", "json", "net", "search", "129.4"}); err != nil {
		t.Fatalf("net search: %v\nstdout:\n%s", err, stdout.String())
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &rows); err != nil {
		t.Fatalf("decode networks: %v\n%s", err, stdout.String())
	}
	if len(rows) != 4 {
		t.Fatalf("network rows = %#v", rows)
	}
	want := []struct {
		itemType string
		network  string
	}{
		{ipamTypeContainer, "10.0.0.0/8"},
		{ipamTypeContainer, "10.129.0.0/16"},
		{ipamTypeNetwork, "10.129.42.0/23"},
		{ipamTypeNetwork, "10.129.46.0/23"},
	}
	for index, expected := range want {
		if cleanString(rows[index]["type"]) != expected.itemType || cleanString(rows[index]["network"]) != expected.network {
			t.Fatalf("row %d = %#v, want %s %s", index, rows[index], expected.itemType, expected.network)
		}
	}
}

func TestNetSearchIncludesChildObjectsForMatchingParentCIDR(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		switch trimWAPIPath(r.URL.Path) {
		case networkViewObject:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{{"name": "default"}},
			})
		case networkObject:
			if got := r.URL.Query().Get("network_view"); got != "" && got != "default" {
				t.Fatalf("network_view query = %q, want blank or default", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{
					{"network": "10.129.42.0/23", "network_view": "default", "comment": "SAP HANA DR"},
					{"network": "10.129.46.0/23", "network_view": "default", "comment": "SAP HANA Prod"},
					{"network": "10.130.42.0/23", "network_view": "default", "comment": "Other"},
				},
			})
		case networkContainerObject:
			if got := r.URL.Query().Get("network_view"); got != "" && got != "default" {
				t.Fatalf("container network_view query = %q, want blank or default", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{
					{"network": "10.0.0.0/8", "network_view": "default", "comment": "Internal"},
					{"network": "10.129.0.0/16", "network_view": "default", "comment": "Servers - Internal Only"},
				},
			})
		default:
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	app, stdout := dnsWorkflowApp(t, server.URL, server.URL)
	if err := app.Execute([]string{"-o", "json", "net", "search", "10.129.0.0/16"}); err != nil {
		t.Fatalf("net search: %v\nstdout:\n%s", err, stdout.String())
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &rows); err != nil {
		t.Fatalf("decode networks: %v\n%s", err, stdout.String())
	}
	if len(rows) != 4 {
		t.Fatalf("network rows = %#v", rows)
	}
	want := []struct {
		itemType string
		network  string
	}{
		{ipamTypeContainer, "10.0.0.0/8"},
		{ipamTypeContainer, "10.129.0.0/16"},
		{ipamTypeNetwork, "10.129.42.0/23"},
		{ipamTypeNetwork, "10.129.46.0/23"},
	}
	for index, expected := range want {
		if cleanString(rows[index]["type"]) != expected.itemType || cleanString(rows[index]["network"]) != expected.network {
			t.Fatalf("row %d = %#v, want %s %s", index, rows[index], expected.itemType, expected.network)
		}
	}
}

func TestNetSearchDoesNotSynthesizeChildCIDRsFromContainingParentPrefix(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("net search should use fresh cache, got %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	app, stdout := dnsWorkflowApp(t, server.URL, server.URL)
	profile := Profile{Name: defaultProfileName, DNSView: "default"}
	now := time.Now()
	if err := app.writeCachedNetworks(profile, "default", []map[string]any{{
		"_ref":         "network/parent",
		"network":      "10.179.16.0/20",
		"network_view": "default",
		"comment":      "Servers",
	}}, now); err != nil {
		t.Fatalf("write network cache: %v", err)
	}
	if err := app.writeCachedNetworkContainers(profile, "default", nil, now); err != nil {
		t.Fatalf("write container cache: %v", err)
	}

	if err := app.Execute([]string{"-o", "json", "net", "search", "10.179.22", "--network-view", "default"}); err != nil {
		t.Fatalf("net search: %v\nstdout:\n%s", err, stdout.String())
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &rows); err != nil {
		t.Fatalf("decode networks: %v\n%s", err, stdout.String())
	}
	if len(rows) != 1 {
		t.Fatalf("network rows = %#v", rows)
	}
	want := []struct {
		network string
		comment string
	}{
		{"10.179.16.0/20", "Servers"},
	}
	for index, expected := range want {
		if cleanString(rows[index]["type"]) != ipamTypeNetwork || cleanString(rows[index]["network"]) != expected.network || cleanString(rows[index]["comment"]) != expected.comment {
			t.Fatalf("row %d = %#v, want network %s %s", index, rows[index], expected.network, expected.comment)
		}
	}
	if strings.Contains(stdout.String(), "10.179.22.0/24") {
		t.Fatalf("net search output should not include undefined covered child CIDRs:\n%s", stdout.String())
	}
}

func TestNetListDoesNotSynthesizeChildCIDRsFromParentWithoutSearch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("net list should use fresh cache, got %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	app, stdout := dnsWorkflowApp(t, server.URL, server.URL)
	profile := Profile{Name: defaultProfileName, DNSView: "default"}
	now := time.Now()
	if err := app.writeCachedNetworks(profile, "default", nil, now); err != nil {
		t.Fatalf("write network cache: %v", err)
	}
	if err := app.writeCachedNetworkContainers(profile, "default", []map[string]any{{
		"_ref":         "networkcontainer/parent",
		"network":      "10.179.16.0/20",
		"network_view": "default",
		"comment":      "Servers",
	}}, now); err != nil {
		t.Fatalf("write container cache: %v", err)
	}

	if err := app.Execute([]string{"-o", "json", "net", "list", "--network-view", "default"}); err != nil {
		t.Fatalf("net list: %v\nstdout:\n%s", err, stdout.String())
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &rows); err != nil {
		t.Fatalf("decode networks: %v\n%s", err, stdout.String())
	}
	if len(rows) != 1 {
		t.Fatalf("network rows = %#v", rows)
	}
	want := []struct {
		itemType string
		network  string
		comment  string
	}{
		{ipamTypeContainer, "10.179.16.0/20", "Servers"},
	}
	for index, expected := range want {
		if cleanString(rows[index]["type"]) != expected.itemType || cleanString(rows[index]["network"]) != expected.network || cleanString(rows[index]["comment"]) != expected.comment {
			t.Fatalf("row %d = %#v, want %s %s %s", index, rows[index], expected.itemType, expected.network, expected.comment)
		}
	}
	if strings.Contains(stdout.String(), "10.179.22.0/24") {
		t.Fatalf("net list output should not include undefined covered child CIDRs:\n%s", stdout.String())
	}
}

func TestNetListUsesFreshCache(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("net list should use fresh cache, got %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	app, stdout := dnsWorkflowApp(t, server.URL, server.URL)
	profile := Profile{Name: defaultProfileName, DNSView: "default"}
	if err := app.writeCachedNetworks(profile, "default", []map[string]any{{
		"_ref":         "network/ref",
		"network":      "192.0.2.0/24",
		"network_view": "default",
		"comment":      "Cached production",
	}}, time.Now()); err != nil {
		t.Fatalf("write network cache: %v", err)
	}
	if err := app.writeCachedNetworkContainers(profile, "default", []map[string]any{{
		"_ref":         "networkcontainer/ref",
		"network":      "198.51.0.0/16",
		"network_view": "default",
		"comment":      "Cached production container",
	}}, time.Now()); err != nil {
		t.Fatalf("write container cache: %v", err)
	}

	if err := app.Execute([]string{"-o", "json", "net", "list", "prod", "--network-view", "default"}); err != nil {
		t.Fatalf("net list cached: %v\nstdout:\n%s", err, stdout.String())
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &rows); err != nil {
		t.Fatalf("decode networks: %v\n%s", err, stdout.String())
	}
	if len(rows) != 2 {
		t.Fatalf("cached network rows = %#v", rows)
	}
	if cleanString(rows[0]["type"]) != ipamTypeNetwork || cleanString(rows[0]["network"]) != "192.0.2.0/24" || cleanString(rows[0]["comment"]) != "Cached production" {
		t.Fatalf("cached network row = %#v", rows[0])
	}
	if cleanString(rows[1]["type"]) != ipamTypeContainer || cleanString(rows[1]["network"]) != "198.51.0.0/16" || cleanString(rows[1]["comment"]) != "Cached production container" {
		t.Fatalf("cached container row = %#v", rows[1])
	}
}

func TestNetListReturnsSWRCacheAndStartsRefresh(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("net list should return SWR cache without foreground WAPI, got %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	app, stdout := dnsWorkflowApp(t, server.URL, server.URL)
	profile := Profile{Name: defaultProfileName, DNSView: "default"}
	now := time.Now()
	if err := app.writeCachedNetworksEntry(profile, "default", []map[string]any{{
		"_ref":         "network/ref",
		"network":      "192.0.2.0/24",
		"network_view": "default",
		"comment":      "Stale production",
	}}, now.Add(-time.Hour).Unix(), now.Add(time.Hour).Unix()); err != nil {
		t.Fatalf("write stale network cache: %v", err)
	}
	if err := app.writeCachedNetworkContainersEntry(profile, "default", []map[string]any{{
		"_ref":         "networkcontainer/ref",
		"network":      "198.51.0.0/16",
		"network_view": "default",
		"comment":      "Stale production container",
	}}, now.Add(-time.Hour).Unix(), now.Add(time.Hour).Unix()); err != nil {
		t.Fatalf("write stale container cache: %v", err)
	}
	var refreshes []string
	app.backgroundNetRefresher = func(profile Profile, kind string, networkView string, ip string) error {
		refreshes = append(refreshes, kind+"|"+networkView)
		return nil
	}

	if err := app.Execute([]string{"-o", "json", "net", "list", "--network-view", "default"}); err != nil {
		t.Fatalf("net list swr: %v\nstdout:\n%s", err, stdout.String())
	}
	if strings.Join(refreshes, ",") != netCacheKindNetworks+"|default,"+netCacheKindContainers+"|default" {
		t.Fatalf("background refreshes = %#v", refreshes)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &rows); err != nil {
		t.Fatalf("decode networks: %v\n%s", err, stdout.String())
	}
	if len(rows) != 2 || cleanString(rows[0]["comment"]) != "Stale production" || cleanString(rows[1]["comment"]) != "Stale production container" {
		t.Fatalf("SWR network rows = %#v", rows)
	}
}

func TestNetListReturnsExpiredCacheAndStartsRefresh(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("net list should return expired cache without foreground WAPI, got %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	app, stdout := dnsWorkflowApp(t, server.URL, server.URL)
	profile := Profile{Name: defaultProfileName, DNSView: "default"}
	now := time.Now()
	if err := app.writeCachedNetworksEntry(profile, "default", []map[string]any{{
		"_ref":         "network/old",
		"network":      "192.0.2.0/24",
		"network_view": "default",
		"comment":      "Expired production",
	}}, now.Add(-2*time.Hour).Unix(), now.Add(-time.Hour).Unix()); err != nil {
		t.Fatalf("write expired network cache: %v", err)
	}
	if err := app.writeCachedNetworkContainersEntry(profile, "default", []map[string]any{{
		"_ref":         "networkcontainer/old",
		"network":      "198.51.100.0/24",
		"network_view": "default",
		"comment":      "Expired container",
	}}, now.Add(-2*time.Hour).Unix(), now.Add(-time.Hour).Unix()); err != nil {
		t.Fatalf("write expired container cache: %v", err)
	}
	var refreshes []string
	app.backgroundNetRefresher = func(profile Profile, kind string, networkView string, ip string) error {
		refreshes = append(refreshes, kind+"|"+networkView)
		return nil
	}

	if err := app.Execute([]string{"-o", "json", "net", "list", "--network-view", "default"}); err != nil {
		t.Fatalf("net list expired: %v\nstdout:\n%s", err, stdout.String())
	}
	if strings.Join(refreshes, ",") != netCacheKindNetworks+"|default,"+netCacheKindContainers+"|default" {
		t.Fatalf("background refreshes = %#v", refreshes)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &rows); err != nil {
		t.Fatalf("decode networks: %v\n%s", err, stdout.String())
	}
	if len(rows) != 2 || cleanString(rows[0]["comment"]) != "Expired production" || cleanString(rows[1]["comment"]) != "Expired container" {
		t.Fatalf("expired cached network rows = %#v", rows)
	}
	if strings.Contains(stdout.String(), "INFO:") {
		t.Fatalf("json output should not include stale cache notice:\n%s", stdout.String())
	}
}

func TestNetListTableOutputShowsExpiredCacheNotice(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("net list should return expired cache without foreground WAPI, got %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	app, stdout := dnsWorkflowApp(t, server.URL, server.URL)
	profile := Profile{Name: defaultProfileName, DNSView: "default"}
	now := time.Now()
	if err := app.writeCachedNetworksEntry(profile, "default", []map[string]any{{
		"_ref":         "network/old",
		"network":      "192.0.2.0/24",
		"network_view": "default",
		"comment":      "Expired production",
	}}, now.Add(-2*time.Hour).Unix(), now.Add(-time.Hour).Unix()); err != nil {
		t.Fatalf("write expired network cache: %v", err)
	}
	if err := app.writeCachedNetworkContainersEntry(profile, "default", nil, now.Add(-2*time.Hour).Unix(), now.Add(-time.Hour).Unix()); err != nil {
		t.Fatalf("write expired container cache: %v", err)
	}
	app.backgroundNetRefresher = func(profile Profile, kind string, networkView string, ip string) error {
		return nil
	}

	if err := app.Execute([]string{"net", "list", "--network-view", "default"}); err != nil {
		t.Fatalf("net list expired table: %v\nstdout:\n%s", err, stdout.String())
	}
	output := stdout.String()
	for _, want := range []string{"IPAM Networks and Containers", "192.0.2.0/24", "INFO: showing cached IPAM data; refresh queued in background"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expired cache table output missing %q:\n%s", want, output)
		}
	}
}

func TestNetSearchReturnsExpiredCacheAndStartsRefresh(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("net search should return expired cache without foreground WAPI, got %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	app, stdout := dnsWorkflowApp(t, server.URL, server.URL)
	profile := Profile{Name: defaultProfileName, DNSView: "default"}
	now := time.Now()
	if err := app.writeCachedNetworksEntry(profile, "default", []map[string]any{{
		"_ref":         "network/old",
		"network":      "192.0.2.0/24",
		"network_view": "default",
		"comment":      "Expired production",
	}}, now.Add(-2*time.Hour).Unix(), now.Add(-time.Hour).Unix()); err != nil {
		t.Fatalf("write expired network cache: %v", err)
	}
	if err := app.writeCachedNetworkContainersEntry(profile, "default", []map[string]any{{
		"_ref":         "networkcontainer/old",
		"network":      "198.51.100.0/24",
		"network_view": "default",
		"comment":      "Expired container",
	}}, now.Add(-2*time.Hour).Unix(), now.Add(-time.Hour).Unix()); err != nil {
		t.Fatalf("write expired container cache: %v", err)
	}
	var refreshes []string
	app.backgroundNetRefresher = func(profile Profile, kind string, networkView string, ip string) error {
		refreshes = append(refreshes, kind+"|"+networkView)
		return nil
	}

	if err := app.Execute([]string{"-o", "json", "net", "search", "prod", "--network-view", "default"}); err != nil {
		t.Fatalf("net search expired: %v\nstdout:\n%s", err, stdout.String())
	}
	if strings.Join(refreshes, ",") != netCacheKindNetworks+"|default,"+netCacheKindContainers+"|default" {
		t.Fatalf("background refreshes = %#v", refreshes)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &rows); err != nil {
		t.Fatalf("decode networks: %v\n%s", err, stdout.String())
	}
	if len(rows) != 1 || cleanString(rows[0]["comment"]) != "Expired production" {
		t.Fatalf("expired cached search rows = %#v", rows)
	}
}

func TestNetListRefreshFlagRefreshesExpiredCacheWithoutSerialCheck(t *testing.T) {
	var networkRequests int
	var containerRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		switch trimWAPIPath(r.URL.Path) {
		case networkObject:
			networkRequests++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{{
					"_ref":         "network/live",
					"network":      "198.51.100.0/24",
					"network_view": "default",
					"comment":      "Live production",
				}},
			})
		case networkContainerObject:
			containerRequests++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{{
					"_ref":         "networkcontainer/live",
					"network":      "203.0.113.0/24",
					"network_view": "default",
					"comment":      "Live container",
				}},
			})
		default:
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	app, stdout := dnsWorkflowApp(t, server.URL, server.URL)
	profile := Profile{Name: defaultProfileName, DNSView: "default"}
	now := time.Now()
	if err := app.writeCachedNetworksEntry(profile, "default", []map[string]any{{
		"_ref":         "network/old",
		"network":      "192.0.2.0/24",
		"network_view": "default",
		"comment":      "Expired production",
	}}, now.Add(-2*time.Hour).Unix(), now.Add(-time.Hour).Unix()); err != nil {
		t.Fatalf("write expired network cache: %v", err)
	}
	if err := app.writeCachedNetworkContainersEntry(profile, "default", []map[string]any{{
		"_ref":         "networkcontainer/old",
		"network":      "198.51.100.0/24",
		"network_view": "default",
		"comment":      "Expired container",
	}}, now.Add(-2*time.Hour).Unix(), now.Add(-time.Hour).Unix()); err != nil {
		t.Fatalf("write expired container cache: %v", err)
	}

	if err := app.Execute([]string{"-o", "json", "net", "list", "--network-view", "default", "--refresh"}); err != nil {
		t.Fatalf("net list refresh expired: %v\nstdout:\n%s", err, stdout.String())
	}
	if networkRequests != 1 {
		t.Fatalf("network requests = %d, want 1", networkRequests)
	}
	if containerRequests != 1 {
		t.Fatalf("container requests = %d, want 1", containerRequests)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &rows); err != nil {
		t.Fatalf("decode networks: %v\n%s", err, stdout.String())
	}
	if len(rows) != 2 || cleanString(rows[0]["network"]) != "198.51.100.0/24" || cleanString(rows[1]["network"]) != "203.0.113.0/24" {
		t.Fatalf("refreshed network rows = %#v", rows)
	}
}

func TestNetShowRequiresNetworkViewWhenNetworkIsAmbiguous(t *testing.T) {
	server := dnsNextIPServer(t, []map[string]any{
		{"_ref": "network/ref1", "network": "192.0.2.0/24", "network_view": "default"},
		{"_ref": "network/ref2", "network": "192.0.2.0/24", "network_view": "lab"},
	}, map[string]any{"ips": []string{"192.0.2.20"}})
	defer server.Close()

	app, stdout := dnsWorkflowApp(t, server.URL, server.URL)
	err := app.Execute([]string{"net", "show", "192.0.2.0/24"})
	if err == nil {
		t.Fatal("ambiguous network succeeded, want error")
	}
	if !strings.Contains(err.Error(), "multiple networks or containers found for 192.0.2.0/24; use --network-view to choose one") {
		t.Fatalf("error = %v\nstdout:\n%s", err, stdout.String())
	}
}

func TestNetShowPrefersContainerForDuplicateCIDR(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		switch trimWAPIPath(r.URL.Path) {
		case networkObject:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{{
					"_ref":         "network/ref",
					"network":      "192.0.0.0/16",
					"network_view": "default",
					"comment":      "Network",
				}},
			})
		case networkContainerObject:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{{
					"_ref":         "networkcontainer/ref",
					"network":      "192.0.0.0/16",
					"network_view": "default",
					"comment":      "Container",
				}},
			})
		default:
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	app, stdout := dnsWorkflowApp(t, server.URL, server.URL)
	if err := app.Execute([]string{"-o", "json", "net", "show", "192.0.0.0/16", "--network-view", "default"}); err != nil {
		t.Fatalf("net show: %v\nstdout:\n%s", err, stdout.String())
	}
	var row map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &row); err != nil {
		t.Fatalf("decode show: %v\n%s", err, stdout.String())
	}
	if cleanString(row["type"]) != ipamTypeContainer || cleanString(row["comment"]) != "Container" {
		t.Fatalf("show row = %#v", row)
	}
}

func TestNetAddressUsesFreshCache(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("net address should use fresh cache, got %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	app, stdout := dnsWorkflowApp(t, server.URL, server.URL)
	profile := Profile{Name: defaultProfileName, DNSView: "default"}
	if err := app.writeCachedIPv4Addresses(profile, "192.0.2.10", "default", []map[string]any{{
		"ip_address":   "192.0.2.10",
		"network":      "192.0.2.0/24",
		"network_view": "default",
		"status":       "USED",
		"comment":      "Cached address",
	}}, time.Now()); err != nil {
		t.Fatalf("write address cache: %v", err)
	}
	if err := app.writeCachedNetworkContainers(profile, "default", []map[string]any{{
		"network":      "192.0.0.0/16",
		"network_view": "default",
	}}, time.Now()); err != nil {
		t.Fatalf("write container cache: %v", err)
	}

	if err := app.Execute([]string{"-o", "json", "net", "address", "192.0.2.10", "--network-view", "default"}); err != nil {
		t.Fatalf("net address cached: %v\nstdout:\n%s", err, stdout.String())
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &rows); err != nil {
		t.Fatalf("decode addresses: %v\n%s", err, stdout.String())
	}
	if len(rows) != 1 || cleanString(rows[0]["comment"]) != "Cached address" || cleanString(rows[0]["container"]) != "192.0.0.0/16" {
		t.Fatalf("cached address rows = %#v", rows)
	}
}

func TestNetAddressShowsIPv4AddressDetails(t *testing.T) {
	var gotIP string
	var gotNetworkView string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		switch trimWAPIPath(r.URL.Path) {
		case ipv4AddressObject:
			gotIP = r.URL.Query().Get("ip_address")
			gotNetworkView = r.URL.Query().Get("network_view")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{{
					"ip_address":   "192.0.2.10",
					"network":      "192.0.2.0/24",
					"network_view": "default",
					"status":       "USED",
					"types":        []any{"HOST", "DHCP"},
					"names":        []any{"app.example.com"},
					"mac_address":  "00:11:22:33:44:55",
					"lease_state":  "ACTIVE",
					"comment":      "Application host",
				}},
			})
		case networkContainerObject:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{
					{"network": "192.0.0.0/16", "network_view": "default"},
					{"network": "192.0.2.0/25", "network_view": "default"},
				},
			})
		default:
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	app, stdout := dnsWorkflowApp(t, server.URL, server.URL)
	if err := app.Execute([]string{"-o", "json", "net", "address", "192.0.2.10", "--network-view", "default"}); err != nil {
		t.Fatalf("net address: %v\nstdout:\n%s", err, stdout.String())
	}
	if gotIP != "192.0.2.10" || gotNetworkView != "default" {
		t.Fatalf("query ip=%q network_view=%q", gotIP, gotNetworkView)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &rows); err != nil {
		t.Fatalf("decode addresses: %v\n%s", err, stdout.String())
	}
	if len(rows) != 1 {
		t.Fatalf("address rows = %#v", rows)
	}
	row := rows[0]
	for key, want := range map[string]string{
		"ip":           "192.0.2.10",
		"network":      "192.0.2.0/24",
		"container":    "192.0.2.0/25",
		"network_view": "default",
		"status":       "USED",
		"types":        "HOST, DHCP",
		"names":        "app.example.com",
		"mac_address":  "00:11:22:33:44:55",
		"lease_state":  "ACTIVE",
		"comment":      "Application host",
	} {
		if got := cleanString(row[key]); got != want {
			t.Fatalf("%s = %q, want %q: %#v", key, got, want, row)
		}
	}
}

func TestNetNextIPUsesCachedNetworkLookupButLiveFunction(t *testing.T) {
	var primaryRequests []string
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryRequests = append(primaryRequests, r.Method+" "+trimWAPIPath(r.URL.Path))
		if r.Method != http.MethodPost || trimWAPIPath(r.URL.Path) != "network/ref" {
			t.Fatalf("primary request = %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ips": []string{"192.0.2.20"}})
	}))
	defer primary.Close()
	read := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("net next-ip should use cached network lookup, got read request %s %s", r.Method, r.URL.Path)
	}))
	defer read.Close()

	app, stdout := dnsWorkflowApp(t, primary.URL, read.URL)
	profile := Profile{Name: defaultProfileName, DNSView: "default"}
	if err := app.writeCachedNetworks(profile, "default", []map[string]any{{
		"_ref":         "network/ref",
		"network":      "192.0.2.0/24",
		"network_view": "default",
	}}, time.Now()); err != nil {
		t.Fatalf("write network cache: %v", err)
	}
	if err := app.writeCachedNetworkContainers(profile, "default", nil, time.Now()); err != nil {
		t.Fatalf("write container cache: %v", err)
	}

	if err := app.Execute([]string{"-o", "json", "net", "next-ip", "192.0.2.0/24", "--network-view", "default"}); err != nil {
		t.Fatalf("net next-ip cached lookup: %v\nstdout:\n%s", err, stdout.String())
	}
	if strings.Join(primaryRequests, ",") != "POST network/ref" {
		t.Fatalf("primary requests = %#v", primaryRequests)
	}
	assertJSONNextIPRows(t, stdout.String(), []string{"192.0.2.20"}, "192.0.2.0/24", "default")
}

func TestNetNextIPUsesContainerRefWhenCIDRMatchesContainer(t *testing.T) {
	var primaryRequests []string
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryRequests = append(primaryRequests, r.Method+" "+trimWAPIPath(r.URL.Path))
		if r.Method != http.MethodPost || trimWAPIPath(r.URL.Path) != "networkcontainer/ref" {
			t.Fatalf("primary request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("_function"); got != "next_available_ip" {
			t.Fatalf("_function = %q, want next_available_ip", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ips": []string{"192.0.2.20"}})
	}))
	defer primary.Close()
	read := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("net next-ip should use cached object lookup, got read request %s %s", r.Method, r.URL.Path)
	}))
	defer read.Close()

	app, stdout := dnsWorkflowApp(t, primary.URL, read.URL)
	profile := Profile{Name: defaultProfileName, DNSView: "default"}
	now := time.Now()
	if err := app.writeCachedNetworks(profile, "default", []map[string]any{{
		"_ref":         "network/ref",
		"network":      "192.0.2.0/24",
		"network_view": "default",
	}}, now); err != nil {
		t.Fatalf("write network cache: %v", err)
	}
	if err := app.writeCachedNetworkContainers(profile, "default", []map[string]any{{
		"_ref":         "networkcontainer/ref",
		"network":      "192.0.2.0/24",
		"network_view": "default",
	}}, now); err != nil {
		t.Fatalf("write container cache: %v", err)
	}

	if err := app.Execute([]string{"-o", "json", "net", "next-ip", "192.0.2.0/24", "--network-view", "default"}); err != nil {
		t.Fatalf("net next-ip container lookup: %v\nstdout:\n%s", err, stdout.String())
	}
	if strings.Join(primaryRequests, ",") != "POST networkcontainer/ref" {
		t.Fatalf("primary requests = %#v", primaryRequests)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &rows); err != nil {
		t.Fatalf("decode next-ip: %v\n%s", err, stdout.String())
	}
	if len(rows) != 1 || cleanString(rows[0]["type"]) != ipamTypeContainer || cleanString(rows[0]["ip"]) != "192.0.2.20" {
		t.Fatalf("next-ip rows = %#v", rows)
	}
}

func TestNetNextIPRoutesLookupToReadAndFunctionToPrimary(t *testing.T) {
	var primaryRequests []string
	var readRequests []string
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryRequests = append(primaryRequests, r.Method+" "+trimWAPIPath(r.URL.Path))
		if r.Method != http.MethodPost || trimWAPIPath(r.URL.Path) != "network/ref" {
			t.Fatalf("primary request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("_function"); got != "next_available_ip" {
			t.Fatalf("_function = %q, want next_available_ip", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ips": []string{"192.0.2.20"}})
	}))
	defer primary.Close()
	read := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		readRequests = append(readRequests, r.Method+" "+trimWAPIPath(r.URL.Path))
		if r.Method != http.MethodGet {
			t.Fatalf("read request = %s %s", r.Method, r.URL.Path)
		}
		switch trimWAPIPath(r.URL.Path) {
		case networkObject:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{{
					"_ref":         "network/ref",
					"network":      "192.0.2.0/24",
					"network_view": "default",
				}},
			})
		case networkContainerObject:
			_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{}})
		default:
			t.Fatalf("read request = %s %s", r.Method, r.URL.Path)
		}
	}))
	defer read.Close()

	app, stdout := dnsWorkflowApp(t, primary.URL, read.URL)
	if err := app.Execute([]string{"-o", "json", "net", "next-ip", "192.0.2.0/24", "--network-view", "default"}); err != nil {
		t.Fatalf("net next-ip: %v\nstdout:\n%s", err, stdout.String())
	}
	if strings.Join(readRequests, ",") != "GET network,GET networkcontainer" {
		t.Fatalf("read requests = %#v", readRequests)
	}
	if strings.Join(primaryRequests, ",") != "POST network/ref" {
		t.Fatalf("primary requests = %#v", primaryRequests)
	}
	assertJSONNextIPRows(t, stdout.String(), []string{"192.0.2.20"}, "192.0.2.0/24", "default")
}
