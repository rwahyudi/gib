package ibcli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDHCPLeaseListUsesLiveFilteredQueryAndJSON(t *testing.T) {
	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if !strings.HasSuffix(r.URL.Path, "/lease") || r.URL.Query().Get("network_view") != "default" || r.URL.Query().Get("network") != "192.0.2.0/24" || r.URL.Query().Get("hardware") != "00:11:22:33:44:55" || r.URL.Query().Get("client_hostname") != "printer" || r.URL.Query().Get("binding_state") != "ACTIVE" {
			t.Fatalf("lease query = %s", r.URL.RequestURI())
		}
		if r.URL.Query().Get("_return_fields") != dhcpLeaseReturnFields || r.URL.Query().Get("_max_results") != "20000" {
			t.Fatalf("paging query = %s", r.URL.RequestURI())
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{{"address": "192.0.2.25", "hardware": "00:11:22:33:44:55", "client_hostname": "printer", "binding_state": "ACTIVE", "network": "192.0.2.0/24", "network_view": "default", "starts": "2026-01-01", "ends": "2026-01-02"}}})
	}))
	defer server.Close()
	app := dhcpWorkflowApp(t, server.URL, server.URL, jsonOutput)
	if err := app.runDHCPLeaseList("printer", "", "192.0.2.0/24", "00:11:22:33:44:55", "printer", "ACTIVE", dhcpLeaseSort{}, defaultDHCPLeaseColumns()); err != nil {
		t.Fatalf("list: %v", err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(app.Stdout.(*bytes.Buffer).Bytes(), &rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) != 1 || rows[0]["ip"] != "192.0.2.25" || rows[0]["hostname"] != "printer" {
		t.Fatalf("rows = %#v", rows)
	}
	if hits != 1 {
		t.Fatalf("lease requests = %d, want 1", hits)
	}
}

func TestDHCPLeasePaginationAndShowDefaultView(t *testing.T) {
	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits == 1 {
			if r.URL.Query().Get("address") != "" {
				t.Fatalf("list address = %q", r.URL.Query().Get("address"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{{"address": "192.0.2.2"}}, "next_page_id": "page-2"})
			return
		}
		if r.URL.Query().Get("_page_id") != "page-2" || r.URL.Query().Get("address") != "" {
			t.Fatalf("page query = %s", r.URL.RequestURI())
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{{"address": "192.0.2.25", "network_view": "default"}}})
	}))
	defer server.Close()
	app := dhcpWorkflowApp(t, server.URL, server.URL, jsonOutput)
	_, client, err := app.configuredClient()
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	rows, err := queryDHCPLeases(client, "", "", "", "", "", "")
	if err != nil || len(rows) != 2 {
		t.Fatalf("paginated rows = %#v, err=%v", rows, err)
	}
}

func TestDeduplicateDHCPLeasesPreservesFirstPeerAndDistinctKeys(t *testing.T) {
	first := map[string]any{"address": "192.0.2.25", "network_view": "default", "starts": "first"}
	rows := []map[string]any{
		first,
		{"address": "192.0.2.25", "network_view": "default", "starts": "peer"},
		{"address": "192.0.2.25", "network_view": "guest", "starts": "other view"},
		{"address": "192.0.2.26", "network_view": "default", "starts": "other IP"},
	}

	got := deduplicateDHCPLeases(rows)
	if len(got) != 3 || got[0]["starts"] != "first" {
		t.Fatalf("deduplicated rows = %#v", got)
	}
	if got[1]["network_view"] != "guest" || got[2]["address"] != "192.0.2.26" {
		t.Fatalf("distinct rows = %#v", got)
	}
}

func TestDHCPLeaseTableAndMachineColumnsRemainDeterministic(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{
			{"address": "192.0.2.25", "hardware": "00:11:22:33:44:55", "client_hostname": strings.Repeat("host", 20), "binding_state": "ACTIVE", "network": "192.0.2.0/24", "network_view": "default"},
			{"address": "192.0.2.25", "hardware": "peer", "client_hostname": "peer", "binding_state": "EXPIRED", "network": "192.0.2.0/24", "network_view": "default"},
		}})
	}))
	defer server.Close()
	app := dhcpWorkflowApp(t, server.URL, server.URL, tableOutput)
	if err := app.runDHCPLeaseList("", "", "", "", "", "", dhcpLeaseSort{}, defaultDHCPLeaseColumns()); err != nil {
		t.Fatalf("table: %v", err)
	}
	out := app.Stdout.(*bytes.Buffer).String()
	if strings.Contains(out, "network view") || !strings.Contains(out, "hostname") || !strings.Contains(out, "Current Context:") || !strings.Contains(out, "DHCP Leases (1)") || strings.Contains(out, "peer") {
		t.Fatalf("table output = %s", out)
	}

	columns := []string{"ip", "state"}
	app.Output = jsonOutput
	app.Stdout = &bytes.Buffer{}
	if err := app.runDHCPLeaseList("", "", "", "", "", "", dhcpLeaseSort{}, columns); err != nil {
		t.Fatalf("json: %v", err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(app.Stdout.(*bytes.Buffer).Bytes(), &rows); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	if len(rows) != 1 || len(rows[0]) != len(columns) || rows[0]["ip"] != "192.0.2.25" || rows[0]["state"] != "ACTIVE" {
		t.Fatalf("json columns = %#v", rows)
	}
	if _, ok := rows[0]["hostname"]; ok {
		t.Fatalf("json unexpectedly included hostname: %#v", rows[0])
	}

	app.Output = csvOutput
	app.Stdout = &bytes.Buffer{}
	if err := app.runDHCPLeaseList("", "", "", "", "", "", dhcpLeaseSort{}, columns); err != nil {
		t.Fatalf("csv: %v", err)
	}
	if got, want := app.Stdout.(*bytes.Buffer).String(), "ip,state\n192.0.2.25,ACTIVE\n"; got != want {
		t.Fatalf("csv output = %q, want %q", got, want)
	}
}

func TestDHCPLeaseColumnsSortAndCompletions(t *testing.T) {
	columns, err := parseDHCPLeaseColumns("ip,network_view,ends")
	if err != nil || strings.Join(columns, ",") != "ip,network_view,ends" {
		t.Fatalf("columns = %#v, err=%v", columns, err)
	}
	if _, err := parseDHCPLeaseColumns("ip,ip"); err == nil {
		t.Fatal("duplicate column accepted")
	}
	option, err := parseDHCPLeaseSort("-ip", true)
	if err != nil || option.Field != "ip" || !option.Desc {
		t.Fatalf("sort = %#v, err=%v", option, err)
	}
	rows := []map[string]any{{"ip": "192.0.2.100"}, {"ip": "192.0.2.2"}}
	applyDHCPLeaseSort(rows, dhcpLeaseSort{Enabled: true, Field: "ip"})
	if rows[0]["ip"] != "192.0.2.2" {
		t.Fatalf("IP sort = %#v", rows)
	}
	completions, _ := dhcpLeaseSortFlagCompletion(nil, nil, "network")
	if len(completions) != 2 || completions[0] != "network" || completions[1] != "network_view" {
		t.Fatalf("sort completions = %#v", completions)
	}
}

func TestDHCPLeaseShowDeduplicatesPeerRows(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("address") != "192.0.2.25" || r.URL.Query().Get("network_view") != "default" {
			t.Fatalf("show query = %s", r.URL.RequestURI())
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{{"address": "192.0.2.25"}, {"address": "192.0.2.25"}}})
	}))
	defer server.Close()
	app := dhcpWorkflowApp(t, server.URL, server.URL, jsonOutput)
	_, client, err := app.configuredClient()
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	lease, err := findDHCPLease(client, "", "192.0.2.25")
	if err != nil || lease["address"] != "192.0.2.25" {
		t.Fatalf("deduplicated lease = %#v, err=%v", lease, err)
	}
}

func TestDHCPLeaseShowNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{}})
	}))
	defer server.Close()
	app := dhcpWorkflowApp(t, server.URL, server.URL, jsonOutput)
	_, client, err := app.configuredClient()
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	if _, err := findDHCPLease(client, "", "192.0.2.25"); err == nil || !strings.Contains(err.Error(), "no DHCP lease found") {
		t.Fatalf("not-found error = %v", err)
	}
}
