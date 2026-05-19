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
}

func TestNetListSearchesSortsAndSelectsColumns(t *testing.T) {
	var networkView string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || trimWAPIPath(r.URL.Path) != networkObject {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		networkView = r.URL.Query().Get("network_view")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": []map[string]any{
				{"network": "10.0.0.0/24", "network_view": "default", "comment": "Lab"},
				{"network": "192.0.2.0/24", "network_view": "default", "comment": "Production hosts"},
			},
		})
	}))
	defer server.Close()

	app, stdout := dnsWorkflowApp(t, server.URL, server.URL)
	if err := app.Execute([]string{"-o", "json", "net", "list", "prod", "--network-view", "default", "--columns", "network,comment"}); err != nil {
		t.Fatalf("net list: %v\nstdout:\n%s", err, stdout.String())
	}
	if networkView != "default" {
		t.Fatalf("network_view query = %q, want default", networkView)
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

	if err := app.Execute([]string{"-o", "json", "net", "list", "prod", "--network-view", "default"}); err != nil {
		t.Fatalf("net list cached: %v\nstdout:\n%s", err, stdout.String())
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &rows); err != nil {
		t.Fatalf("decode networks: %v\n%s", err, stdout.String())
	}
	if len(rows) != 1 || cleanString(rows[0]["network"]) != "192.0.2.0/24" || cleanString(rows[0]["comment"]) != "Cached production" {
		t.Fatalf("cached network rows = %#v", rows)
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
	var refreshKind, refreshView string
	app.backgroundNetRefresher = func(profile Profile, kind string, networkView string, ip string) error {
		refreshKind = kind
		refreshView = networkView
		return nil
	}

	if err := app.Execute([]string{"-o", "json", "net", "list", "--network-view", "default"}); err != nil {
		t.Fatalf("net list swr: %v\nstdout:\n%s", err, stdout.String())
	}
	if refreshKind != netCacheKindNetworks || refreshView != "default" {
		t.Fatalf("background refresh kind=%q view=%q", refreshKind, refreshView)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &rows); err != nil {
		t.Fatalf("decode networks: %v\n%s", err, stdout.String())
	}
	if len(rows) != 1 || cleanString(rows[0]["comment"]) != "Stale production" {
		t.Fatalf("SWR network rows = %#v", rows)
	}
}

func TestNetListRefreshesExpiredCacheWithoutSerialCheck(t *testing.T) {
	var networkRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || trimWAPIPath(r.URL.Path) != networkObject {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		networkRequests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": []map[string]any{{
				"_ref":         "network/live",
				"network":      "198.51.100.0/24",
				"network_view": "default",
				"comment":      "Live production",
			}},
		})
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

	if err := app.Execute([]string{"-o", "json", "net", "list", "--network-view", "default"}); err != nil {
		t.Fatalf("net list expired: %v\nstdout:\n%s", err, stdout.String())
	}
	if networkRequests != 1 {
		t.Fatalf("network requests = %d, want 1", networkRequests)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &rows); err != nil {
		t.Fatalf("decode networks: %v\n%s", err, stdout.String())
	}
	if len(rows) != 1 || cleanString(rows[0]["network"]) != "198.51.100.0/24" {
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
	if !strings.Contains(err.Error(), "multiple networks found for 192.0.2.0/24; use --network-view to choose one") {
		t.Fatalf("error = %v\nstdout:\n%s", err, stdout.String())
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

	if err := app.Execute([]string{"-o", "json", "net", "address", "192.0.2.10", "--network-view", "default"}); err != nil {
		t.Fatalf("net address cached: %v\nstdout:\n%s", err, stdout.String())
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &rows); err != nil {
		t.Fatalf("decode addresses: %v\n%s", err, stdout.String())
	}
	if len(rows) != 1 || cleanString(rows[0]["comment"]) != "Cached address" {
		t.Fatalf("cached address rows = %#v", rows)
	}
}

func TestNetAddressShowsIPv4AddressDetails(t *testing.T) {
	var gotIP string
	var gotNetworkView string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || trimWAPIPath(r.URL.Path) != ipv4AddressObject {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
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

	if err := app.Execute([]string{"-o", "json", "net", "next-ip", "192.0.2.0/24", "--network-view", "default"}); err != nil {
		t.Fatalf("net next-ip cached lookup: %v\nstdout:\n%s", err, stdout.String())
	}
	if strings.Join(primaryRequests, ",") != "POST network/ref" {
		t.Fatalf("primary requests = %#v", primaryRequests)
	}
	assertJSONNextIPRows(t, stdout.String(), []string{"192.0.2.20"}, "192.0.2.0/24", "default")
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
		if r.Method != http.MethodGet || trimWAPIPath(r.URL.Path) != networkObject {
			t.Fatalf("read request = %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": []map[string]any{{
				"_ref":         "network/ref",
				"network":      "192.0.2.0/24",
				"network_view": "default",
			}},
		})
	}))
	defer read.Close()

	app, stdout := dnsWorkflowApp(t, primary.URL, read.URL)
	if err := app.Execute([]string{"-o", "json", "net", "next-ip", "192.0.2.0/24", "--network-view", "default"}); err != nil {
		t.Fatalf("net next-ip: %v\nstdout:\n%s", err, stdout.String())
	}
	if strings.Join(readRequests, ",") != "GET network" {
		t.Fatalf("read requests = %#v", readRequests)
	}
	if strings.Join(primaryRequests, ",") != "POST network/ref" {
		t.Fatalf("primary requests = %#v", primaryRequests)
	}
	assertJSONNextIPRows(t, stdout.String(), []string{"192.0.2.20"}, "192.0.2.0/24", "default")
}
