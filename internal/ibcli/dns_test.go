package ibcli

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
)

func TestCreatePayloadBuildsHostRecord(t *testing.T) {
	client := &WapiClient{View: "DNS Zone View"}
	objectType, payload, err := createPayload("host", "192.0.2.10", "app", "example.com", 300, "Application host", client)
	if err != nil {
		t.Fatalf("create payload: %v", err)
	}
	if objectType != "record:host" {
		t.Fatalf("object type = %q", objectType)
	}
	if payload["name"] != "app.example.com" {
		t.Fatalf("name = %q", payload["name"])
	}
	if payload["view"] != "DNS Zone View" {
		t.Fatalf("view = %q", payload["view"])
	}
	if payload["ttl"] != 300 || payload["use_ttl"] != true {
		t.Fatalf("ttl fields = %#v", payload)
	}
	addresses, ok := payload["ipv4addrs"].([]map[string]any)
	if !ok || len(addresses) != 1 || addresses[0]["ipv4addr"] != "192.0.2.10" {
		t.Fatalf("ipv4addrs = %#v", payload["ipv4addrs"])
	}
}

func TestCreatePayloadOmitsTTLWhenNotProvided(t *testing.T) {
	client := &WapiClient{View: "DNS Zone View"}
	_, payload, err := createPayload("host", "192.0.2.10", "app", "example.com", -1, "", client)
	if err != nil {
		t.Fatalf("create payload: %v", err)
	}
	if _, ok := payload["ttl"]; ok {
		t.Fatalf("payload includes optional ttl: %#v", payload)
	}
	if _, ok := payload["use_ttl"]; ok {
		t.Fatalf("payload includes use_ttl without ttl: %#v", payload)
	}
}

func TestCreatePayloadQualifiesShortCNAMETarget(t *testing.T) {
	client := &WapiClient{View: "DNS Zone View"}
	objectType, payload, err := createPayload("cname", "computer1", "hostalias1", "example.com", -1, "", client)
	if err != nil {
		t.Fatalf("create payload: %v", err)
	}
	if objectType != "record:cname" {
		t.Fatalf("object type = %q", objectType)
	}
	if payload["name"] != "hostalias1.example.com" {
		t.Fatalf("name = %q", payload["name"])
	}
	if payload["canonical"] != "computer1.example.com" {
		t.Fatalf("canonical = %q", payload["canonical"])
	}
}

func TestCreatePayloadPreservesDottedCNAMETarget(t *testing.T) {
	client := &WapiClient{View: "DNS Zone View"}
	for _, tc := range []struct {
		value string
		want  string
	}{
		{value: "computer1.other.com", want: "computer1.other.com"},
		{value: "computer1.", want: "computer1"},
	} {
		_, payload, err := createPayload("cname", tc.value, "hostalias1", "example.com", -1, "", client)
		if err != nil {
			t.Fatalf("create payload for %q: %v", tc.value, err)
		}
		if payload["canonical"] != tc.want {
			t.Fatalf("canonical for %q = %q, want %q", tc.value, payload["canonical"], tc.want)
		}
	}
}

func TestUpdatePayloadQualifiesShortCNAMETarget(t *testing.T) {
	value := "computer1"
	payload, err := updatePayload("cname", &value, "example.com", -1, "")
	if err != nil {
		t.Fatalf("update payload: %v", err)
	}
	if payload["canonical"] != "computer1.example.com" {
		t.Fatalf("canonical = %q", payload["canonical"])
	}
}

func TestUpdatePayloadPreservesDottedCNAMETarget(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  string
	}{
		{value: "computer1.other.com", want: "computer1.other.com"},
		{value: "computer1.", want: "computer1"},
	} {
		value := tc.value
		payload, err := updatePayload("cname", &value, "", -1, "")
		if err != nil {
			t.Fatalf("update payload for %q: %v", tc.value, err)
		}
		if payload["canonical"] != tc.want {
			t.Fatalf("canonical for %q = %q, want %q", tc.value, payload["canonical"], tc.want)
		}
	}
}

func TestRunDNSCreatePTRUsesIPFirstAndPrimaryReverseZone(t *testing.T) {
	var postedPTR map[string]any
	var primaryRequests []string
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryRequests = append(primaryRequests, r.Method+" "+trimWAPIPath(r.URL.Path))
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/zone_auth"):
			if r.URL.Query().Get("fqdn") != "2.0.192.in-addr.arpa" {
				_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{
					{"fqdn": "2.0.192.in-addr.arpa", "view": "default", "zone_format": "IPV4"},
				},
			})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/record:ptr"):
			if err := json.NewDecoder(r.Body).Decode(&postedPTR); err != nil {
				t.Fatalf("decode ptr payload: %v", err)
			}
			_ = json.NewEncoder(w).Encode("record:ptr/ref")
		default:
			http.NotFound(w, r)
		}
	}))
	defer primary.Close()
	read := emptyReadServer(t)
	defer read.Close()

	app, _ := dnsWorkflowApp(t, primary.URL, read.URL)
	refreshes := captureRecordRefreshes(app)
	if err := app.runDNSCreate("ptr", "192.0.2.10", "app.example.com", "", 300, false, "Managed"); err != nil {
		t.Fatalf("create ptr: %v", err)
	}
	if postedPTR["ipv4addr"] != "192.0.2.10" || postedPTR["ptrdname"] != "app.example.com" || postedPTR["view"] != "default" {
		t.Fatalf("ptr payload = %#v", postedPTR)
	}
	if postedPTR["ttl"] != float64(300) && postedPTR["ttl"] != 300 || postedPTR["use_ttl"] != true || postedPTR["comment"] != "Managed" {
		t.Fatalf("ptr ttl/comment payload = %#v", postedPTR)
	}
	if strings.Join(primaryRequests, ",") != "GET zone_auth,GET zone_auth,POST record:ptr" {
		t.Fatalf("primary requests = %#v", primaryRequests)
	}
	assertQueuedRecordRefreshes(t, refreshes, "2.0.192.in-addr.arpa")
}

func TestRunDNSCreatePTRSupportsIPv6ReverseZone(t *testing.T) {
	address := mustAddr(t, "2001:db8::10")
	reverseZone := reverseZoneCandidates(address)[0]
	var postedPTR map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/zone_auth"):
			if r.URL.Query().Get("fqdn") != reverseZone {
				_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{
					{"fqdn": reverseZone, "view": "default", "zone_format": "IPV6"},
				},
			})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/record:ptr"):
			if err := json.NewDecoder(r.Body).Decode(&postedPTR); err != nil {
				t.Fatalf("decode ptr payload: %v", err)
			}
			_ = json.NewEncoder(w).Encode("record:ptr/ref")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	app := dnsCommandTestApp(t, server.URL, "")
	refreshes := captureRecordRefreshes(app)
	if err := app.runDNSCreate("ptr", address.String(), "ipv6.example.com", "", -1, false, ""); err != nil {
		t.Fatalf("create ipv6 ptr: %v", err)
	}
	if postedPTR["ipv6addr"] != address.String() || postedPTR["ptrdname"] != "ipv6.example.com" {
		t.Fatalf("ptr payload = %#v", postedPTR)
	}
	if _, ok := postedPTR["ipv4addr"]; ok {
		t.Fatalf("ipv6 ptr payload included ipv4addr: %#v", postedPTR)
	}
	assertQueuedRecordRefreshes(t, refreshes, reverseZone)
}

func TestRunDNSCreatePTRExplicitZoneSkipsDiscovery(t *testing.T) {
	const explicitZone = "manual.in-addr.arpa"
	var postedPTR map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/zone_auth"):
			t.Fatalf("unexpected reverse-zone discovery with explicit zone: %s", r.URL.RawQuery)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/record:ptr"):
			if err := json.NewDecoder(r.Body).Decode(&postedPTR); err != nil {
				t.Fatalf("decode ptr payload: %v", err)
			}
			_ = json.NewEncoder(w).Encode("record:ptr/ref")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	app := dnsCommandTestApp(t, server.URL, "")
	refreshes := captureRecordRefreshes(app)
	if err := app.runDNSCreate("ptr", "192.0.2.10", "app.example.com", explicitZone, -1, false, ""); err != nil {
		t.Fatalf("create ptr with explicit zone: %v", err)
	}
	if postedPTR["ipv4addr"] != "192.0.2.10" || postedPTR["ptrdname"] != "app.example.com" {
		t.Fatalf("ptr payload = %#v", postedPTR)
	}
	assertQueuedRecordRefreshes(t, refreshes, explicitZone)
}

func TestRunDNSCreatePTRFindsCIDRReverseZone(t *testing.T) {
	var postedPTR map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/zone_auth"):
			if r.URL.Query().Get("fqdn") != "" {
				_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{
					{"fqdn": "192.168.0.0/16", "view": "default", "zone_format": "IPV4"},
					{"fqdn": "192.168.100.0/24", "view": "default", "zone_format": "IPV4"},
				},
			})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/record:ptr"):
			if err := json.NewDecoder(r.Body).Decode(&postedPTR); err != nil {
				t.Fatalf("decode ptr payload: %v", err)
			}
			_ = json.NewEncoder(w).Encode("record:ptr/ref")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	app := dnsCommandTestApp(t, server.URL, "")
	refreshes := captureRecordRefreshes(app)
	if err := app.runDNSCreate("ptr", "192.168.100.2", "test.net", "", -1, false, ""); err != nil {
		t.Fatalf("create ptr with CIDR reverse zone: %v", err)
	}
	if postedPTR["ipv4addr"] != "192.168.100.2" || postedPTR["ptrdname"] != "test.net" {
		t.Fatalf("ptr payload = %#v", postedPTR)
	}
	assertQueuedRecordRefreshes(t, refreshes, "192.168.100.0/24")
}

func TestRunDNSDeletePTRFallsBackToReverseOwnerNameInCIDRZone(t *testing.T) {
	var sawAddressLookup, sawNameLookup, sawDelete bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/zone_auth"):
			if r.URL.Query().Get("fqdn") != "" {
				_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{{
				"fqdn":        "10.128.1.0/24",
				"view":        "default",
				"zone_format": "IPV4",
			}}})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/record:ptr"):
			switch {
			case r.URL.Query().Get("ipv4addr") == "10.128.1.44":
				sawAddressLookup = true
				_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{}})
			case r.URL.Query().Get("name") == "44.1.128.10.in-addr.arpa":
				sawNameLookup = true
				_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{{
					"_ref":     "record:ptr/ref",
					"name":     "44.1.128.10.in-addr.arpa",
					"ptrdname": "test.com",
					"zone":     "1.128.10.in-addr.arpa",
				}}})
			default:
				t.Fatalf("unexpected PTR lookup query: %s", r.URL.RawQuery)
			}
		case r.Method == http.MethodDelete && trimWAPIPath(r.URL.Path) == "record:ptr/ref":
			sawDelete = true
			_ = json.NewEncoder(w).Encode("record:ptr/ref")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	app := dnsCommandTestApp(t, server.URL, "")
	refreshes := captureRecordRefreshes(app)
	if err := app.runDNSDeletePTR("10.128.1.44", true); err != nil {
		t.Fatalf("delete ptr: %v", err)
	}
	if !sawAddressLookup || !sawNameLookup || !sawDelete {
		t.Fatalf("sawAddressLookup=%t sawNameLookup=%t sawDelete=%t", sawAddressLookup, sawNameLookup, sawDelete)
	}
	assertQueuedRecordRefreshes(t, refreshes, "10.128.1.0/24")
}

func TestRunDNSCreatePTRRejectsNonIPFirstArgument(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.NotFound(w, r)
	}))
	defer server.Close()

	app := dnsCommandTestApp(t, server.URL, "")
	err := app.runDNSCreate("ptr", "app.example.com", "192.0.2.10", "", -1, false, "")
	if err == nil {
		t.Fatal("create ptr succeeded with non-IP first argument")
	}
	if !strings.Contains(err.Error(), "Use: ib dns create ptr <ip-address> <ptr-target>") {
		t.Fatalf("error = %v", err)
	}
	if requests != 0 {
		t.Fatalf("unexpected network requests = %d", requests)
	}
}

func TestRunDNSCreateAManagesPTRUnlessNoPTR(t *testing.T) {
	var createdA, createdPTR int
	var postedPTR map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/zone_auth"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{
					{"fqdn": "2.0.192.in-addr.arpa", "view": "default", "zone_format": "IPV4"},
				},
			})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/record:ptr"):
			_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{}})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/record:a"):
			createdA++
			_ = json.NewEncoder(w).Encode("record:a/ref")
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/record:ptr"):
			createdPTR++
			if err := json.NewDecoder(r.Body).Decode(&postedPTR); err != nil {
				t.Fatalf("decode ptr payload: %v", err)
			}
			_ = json.NewEncoder(w).Encode("record:ptr/ref")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	app := dnsCommandTestApp(t, server.URL, "example.com")
	refreshes := make(chan string, 4)
	app.backgroundRecordRevalidator = func(profile Profile, zone string) error {
		refreshes <- zone
		_ = app.releaseRecordRefreshLease(profile, zone)
		return nil
	}
	if err := app.runDNSCreate("a", "app", "192.0.2.10", "", -1, false, ""); err != nil {
		t.Fatalf("create a with ptr: %v", err)
	}
	if createdA != 1 || createdPTR != 1 {
		t.Fatalf("createdA=%d createdPTR=%d", createdA, createdPTR)
	}
	if postedPTR["ipv4addr"] != "192.0.2.10" || postedPTR["ptrdname"] != "app.example.com" {
		t.Fatalf("ptr payload = %#v", postedPTR)
	}
	assertQueuedRecordRefreshes(t, refreshes, "example.com", "2.0.192.in-addr.arpa")

	if err := app.runDNSCreate("a", "db", "192.0.2.11", "", -1, true, ""); err != nil {
		t.Fatalf("create a with --noptr: %v", err)
	}
	if createdA != 2 || createdPTR != 1 {
		t.Fatalf("--noptr createdA=%d createdPTR=%d", createdA, createdPTR)
	}
	assertQueuedRecordRefreshes(t, refreshes, "example.com")
}

func TestSyncPTRForAddressUpdatesExistingPTR(t *testing.T) {
	var putPayload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/zone_auth"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{
					{"fqdn": "2.0.192.in-addr.arpa", "view": "default", "zone_format": "IPV4"},
				},
			})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/record:ptr"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{
					{"_ref": "record:ptr/ref", "ipv4addr": "192.0.2.10", "ptrdname": "old.example.com", "zone": "2.0.192.in-addr.arpa"},
				},
			})
		case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/record:ptr/ref"):
			if err := json.NewDecoder(r.Body).Decode(&putPayload); err != nil {
				t.Fatalf("decode ptr update payload: %v", err)
			}
			_ = json.NewEncoder(w).Encode("record:ptr/ref")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	app := testApp(t)
	profile := Profile{Name: defaultProfileName, DNSView: "default"}
	reverseZone, err := app.syncPTRForAddress(profile, testWapiClient(server), mustAddr(t, "192.0.2.10"), "app.example.com", 300, "Managed")
	if err != nil {
		t.Fatalf("sync ptr: %v", err)
	}
	if reverseZone != "2.0.192.in-addr.arpa" {
		t.Fatalf("reverse zone = %q", reverseZone)
	}
	if putPayload["ptrdname"] != "app.example.com" || putPayload["ttl"] != float64(300) && putPayload["ttl"] != 300 || putPayload["comment"] != "Managed" {
		t.Fatalf("put payload = %#v", putPayload)
	}
}

func TestSyncPTRForAddressReadsPrimaryServer(t *testing.T) {
	var primaryGETs, readGETs int
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/zone_auth"):
			primaryGETs++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{
					{"fqdn": "2.0.192.in-addr.arpa", "view": "default", "zone_format": "IPV4"},
				},
			})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/record:ptr"):
			primaryGETs++
			_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{}})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/record:ptr"):
			_ = json.NewEncoder(w).Encode("record:ptr/ref")
		default:
			http.NotFound(w, r)
		}
	}))
	defer primary.Close()
	read := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			readGETs++
		}
		http.NotFound(w, r)
	}))
	defer read.Close()

	app := testApp(t)
	client := &WapiClient{
		Server:      primary.URL,
		ReadServer:  read.URL,
		WAPIVersion: defaultWAPIVersion,
		View:        "default",
		httpClient:  primary.Client(),
	}
	if _, err := app.syncPTRForAddress(Profile{Name: defaultProfileName, DNSView: "default"}, client, mustAddr(t, "192.0.2.10"), "app.example.com", -1, ""); err != nil {
		t.Fatalf("sync ptr: %v", err)
	}
	if primaryGETs != 3 || readGETs != 0 {
		t.Fatalf("primaryGETs=%d readGETs=%d", primaryGETs, readGETs)
	}
}

func TestAllRecordsHostAddressTypeNormalizesToHost(t *testing.T) {
	recordType := recordTypeFromAllRecord(map[string]any{"type": "HOST_IPV4ADDR"})
	if recordType != "host" {
		t.Fatalf("record type = %q", recordType)
	}
}

func TestUnsupportedAllRecordsTypeDecodesNSRef(t *testing.T) {
	encodedRef := base64.RawURLEncoding.EncodeToString([]byte("dns.bind_ns$example.com"))
	recordType := recordTypeFromAllRecord(map[string]any{
		"_ref": "allrecords/" + encodedRef + ":example.com/default",
		"type": "UNSUPPORTED",
	})
	if recordType != "ns" {
		t.Fatalf("record type = %q", recordType)
	}
}

func TestParseRecordTypesAcceptsNS(t *testing.T) {
	types, err := parseRecordTypes("ns")
	if err != nil {
		t.Fatalf("parse ns record type: %v", err)
	}
	if len(types) != 1 || types[0] != "ns" {
		t.Fatalf("types = %#v, want []string{\"ns\"}", types)
	}
}

func TestNormalizeRecordTypeArgAcceptsDelegationNS(t *testing.T) {
	recordType, err := normalizeRecordTypeArg("ns")
	if err != nil {
		t.Fatalf("normalize ns write record type: %v", err)
	}
	if recordType != "ns" {
		t.Fatalf("record type = %q, want ns", recordType)
	}
}

func TestUnsupportedAllRecordsTypeDecodesSOARef(t *testing.T) {
	encodedRef := base64.RawURLEncoding.EncodeToString([]byte("dns.bind_soa$example.com"))
	recordType := recordTypeFromAllRecord(map[string]any{
		"_ref": "allrecords/" + encodedRef + ":example.com/default",
		"type": "UNSUPPORTED",
	})
	if recordType != "soa" {
		t.Fatalf("record type = %q", recordType)
	}
}

func TestRecordDisplaySuppressesRefStrings(t *testing.T) {
	item := map[string]any{
		"record": "record:ns/ZG5zLmJpbmRfbnMkZXhhbXBsZS5jb20:example.com/default",
		"type":   "UNSUPPORTED",
	}
	recordType := recordTypeFromAllRecord(item)
	if recordType != "ns" {
		t.Fatalf("record type = %q", recordType)
	}
	if name := recordName(item, recordType); name != "" {
		t.Fatalf("name printed ref: %q", name)
	}
	if value := recordValue(recordType, item); value != "" {
		t.Fatalf("value printed ref: %q", value)
	}
}

func TestRecordDisplayKeepsNonRefRecordText(t *testing.T) {
	item := map[string]any{
		"name":   "_spf.example.com",
		"record": "v=spf1 include:example.net -all",
		"type":   "record:txt",
	}
	recordType := recordTypeFromAllRecord(item)
	if value := recordValue(recordType, item); value != "v=spf1 include:example.net -all" {
		t.Fatalf("value = %q", value)
	}
}

func TestFilterListedRecordsIncludesNS(t *testing.T) {
	records := []TypedRecord{
		{Type: "ns", Item: map[string]any{"name": "example.com", "nameserver": "ns1.example.com", "zone": "example.com"}},
		{Type: "a", Item: map[string]any{"name": "app.example.com", "ipv4addr": "192.0.2.10", "zone": "example.com"}},
	}

	filtered := filterListedRecords(records, SearchOptions{Types: []string{"ns"}})
	if len(filtered) != 1 {
		t.Fatalf("filtered records = %d, want 1: %#v", len(filtered), filtered)
	}
	if filtered[0].Type != "ns" {
		t.Fatalf("filtered type = %q, want ns", filtered[0].Type)
	}
}

func TestRecordValueUsesNameserver(t *testing.T) {
	value := recordValue("ns", map[string]any{"name": "example.com", "nameserver": "ns1.example.com"})
	if value != "ns1.example.com" {
		t.Fatalf("value = %q, want ns1.example.com", value)
	}
}

func TestReverseHostDisplayUsesIPAddressAndForwardName(t *testing.T) {
	row := recordOutputRow("host", map[string]any{
		"name":    "1.1",
		"address": "192.168.1.1",
		"zone":    "192.168.0.0/16",
		"record": map[string]any{
			"name": "www.example.com",
			"ipv4addrs": []any{
				map[string]any{"host": "www.example.com", "ipv4addr": "192.168.1.1"},
			},
		},
	})

	if row["name"] != "192.168.1.1" {
		t.Fatalf("name = %#v, want 192.168.1.1", row["name"])
	}
	if row["value"] != "www.example.com" {
		t.Fatalf("value = %#v, want www.example.com", row["value"])
	}
}

func TestReversePTRDisplayUsesIPAddressAndPTRName(t *testing.T) {
	row := recordOutputRow("ptr", map[string]any{
		"name":     "10",
		"zone":     "10.128.48.0/24",
		"ptrdname": "www.example.com.",
	})

	if row["name"] != "10.128.48.10" {
		t.Fatalf("name = %#v, want 10.128.48.10", row["name"])
	}
	if row["value"] != "www.example.com" {
		t.Fatalf("value = %#v, want www.example.com", row["value"])
	}
}

func TestReverseSearchMatchesIPAddressName(t *testing.T) {
	record := TypedRecord{
		Type: "ptr",
		Item: map[string]any{
			"name":     "10",
			"zone":     "10.128.48.0/24",
			"ptrdname": "www.example.com",
		},
	}

	if !recordMatches(record, SearchOptions{Keyword: "10.128.48.10"}) {
		t.Fatal("reverse record did not match normalized IP address")
	}
}

func TestRecordTypeColorMapsKnownTypes(t *testing.T) {
	tests := map[string]string{
		"host":           "#06b6d4",
		"NS":             "#14b8a6",
		"ns-auth":        "#14b8a6",
		"ns-delegation":  "#facc15",
		"record:soa":     "#ec4899",
		"sharedrecord:a": "#22c55e",
		"unknown":        "#94a3b8",
	}
	for recordType, want := range tests {
		if got := string(recordTypeColor(recordType)); got != want {
			t.Fatalf("record type color for %q = %q, want %q", recordType, got, want)
		}
	}
}

func TestNSRecordDisplayTypeDifferentiatesAuthoritativeAndDelegation(t *testing.T) {
	authoritativeRef := base64.RawURLEncoding.EncodeToString([]byte("dns.fake_bind_ns$example.com"))
	delegationRef := base64.RawURLEncoding.EncodeToString([]byte("dns.bind_ns$child.example.com"))

	tests := []struct {
		name   string
		record TypedRecord
		want   string
	}{
		{
			name: "authoritative fake bind ns ref",
			record: TypedRecord{Type: "ns", Item: map[string]any{
				"_ref":       "allrecords/" + authoritativeRef + ":example.com/default",
				"name":       "example.com",
				"nameserver": "ns1.example.com",
				"zone":       "example.com",
			}},
			want: "ns-auth",
		},
		{
			name: "delegation bind ns ref",
			record: TypedRecord{Type: "ns", Item: map[string]any{
				"_ref":       "allrecords/" + delegationRef + ":child.example.com/default",
				"name":       "child.example.com",
				"nameserver": "ns1.child.example.com",
				"zone":       "example.com",
			}},
			want: "ns-delegation",
		},
		{
			name: "apex ns without ref",
			record: TypedRecord{Type: "ns", Item: map[string]any{
				"name":       "example.com.",
				"nameserver": "ns1.example.com",
				"zone":       "example.com",
			}},
			want: "ns-auth",
		},
		{
			name: "child ns without ref",
			record: TypedRecord{Type: "ns", Item: map[string]any{
				"name":       "child.example.com",
				"nameserver": "ns1.child.example.com",
				"zone":       "example.com",
			}},
			want: "ns-delegation",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := recordDisplayType(tt.record); got != tt.want {
				t.Fatalf("record display type = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEmitRecordsTableLabelsNSRecords(t *testing.T) {
	var stdout bytes.Buffer
	app := &App{Output: tableOutput, Stdout: &stdout}
	if err := app.emitRecordsWithContext([]TypedRecord{
		{Type: "ns", Item: map[string]any{"name": "example.com", "nameserver": "ns1.example.com", "zone": "example.com"}},
		{Type: "ns", Item: map[string]any{"name": "child.example.com", "nameserver": "ns1.child.example.com", "zone": "example.com"}},
	}, false); err != nil {
		t.Fatalf("emit records: %v", err)
	}
	output := stdout.String()
	for _, want := range []string{"NS-AUTH", "NS-DELEGATION"} {
		if !strings.Contains(output, want) {
			t.Fatalf("table output missing %q:\n%s", want, output)
		}
	}
}

func TestStyledRecordTypeNormalizesLabel(t *testing.T) {
	output := styledRecordType("record:host")
	if !strings.Contains(output, "HOST") {
		t.Fatalf("styled type missing HOST label: %q", output)
	}
	if strings.Contains(output, "RECORD:") {
		t.Fatalf("styled type kept record prefix: %q", output)
	}
}

func TestRecordTTLEmptyWhenDefault(t *testing.T) {
	tests := []struct {
		name string
		item map[string]any
		want string
	}{
		{
			name: "default top-level ttl",
			item: map[string]any{"ttl": 300, "use_ttl": false},
			want: "",
		},
		{
			name: "default nested ttl",
			item: map[string]any{"record": map[string]any{"ttl": 300, "use_ttl": false}},
			want: "",
		},
		{
			name: "explicit ttl",
			item: map[string]any{"ttl": 600, "use_ttl": true},
			want: "600",
		},
		{
			name: "ttl without use_ttl",
			item: map[string]any{"ttl": 300},
			want: "300",
		},
	}

	for _, tt := range tests {
		row := recordOutputRow("a", tt.item)
		if row["ttl"] != tt.want {
			t.Fatalf("%s ttl = %#v, want %#v", tt.name, row["ttl"], tt.want)
		}
	}
}

func TestParseRecordColumns(t *testing.T) {
	columns, err := parseRecordColumns("name,value,ttl")
	if err != nil {
		t.Fatalf("parse columns: %v", err)
	}
	if got := strings.Join(columns, ","); got != "name,value,ttl" {
		t.Fatalf("columns = %q, want name,value,ttl", got)
	}

	defaultColumns, err := parseRecordColumns("")
	if err != nil {
		t.Fatalf("parse default columns: %v", err)
	}
	if got := strings.Join(defaultColumns, ","); got != "type,name,value,zone,ttl,created,comment" {
		t.Fatalf("default columns = %q", got)
	}

	for _, tt := range []struct {
		name string
		raw  string
		want string
	}{
		{name: "duplicate", raw: "name,name", want: `duplicate column "name"`},
		{name: "unsupported", raw: "name,owner", want: `unsupported column "owner"`},
		{name: "empty", raw: "name,", want: "record column cannot be empty"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseRecordColumns(tt.raw)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("parse columns error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestEmitRecordsJSONKeepsPlainType(t *testing.T) {
	var stdout bytes.Buffer
	app := &App{Output: jsonOutput, Stdout: &stdout}
	err := app.emitRecords(testRecords(6))
	if err != nil {
		t.Fatalf("emit records: %v", err)
	}
	output := stdout.String()
	if strings.Contains(output, "\x1b[") {
		t.Fatalf("json output contains ANSI styling: %q", output)
	}
	if !strings.Contains(output, `"type": "HOST"`) {
		t.Fatalf("json output missing plain type: %s", output)
	}
	if strings.Contains(output, "Total records:") {
		t.Fatalf("json output contains table total footer: %s", output)
	}
}

func TestEmitRecordsWithSelectedColumns(t *testing.T) {
	t.Run("json", func(t *testing.T) {
		var stdout bytes.Buffer
		app := &App{Output: jsonOutput, Stdout: &stdout}
		if err := app.emitRecordsWithContext(testRecords(1), false, []string{"name", "value"}); err != nil {
			t.Fatalf("emit records: %v", err)
		}
		var rows []map[string]any
		if err := json.Unmarshal(stdout.Bytes(), &rows); err != nil {
			t.Fatalf("decode json: %v\n%s", err, stdout.String())
		}
		if len(rows) != 1 {
			t.Fatalf("rows = %d, want 1", len(rows))
		}
		if len(rows[0]) != 2 || cleanString(rows[0]["name"]) != "app1.example.com" || cleanString(rows[0]["value"]) != "192.0.2.1" {
			t.Fatalf("row = %#v", rows[0])
		}
		if _, ok := rows[0]["type"]; ok {
			t.Fatalf("json row included unselected type: %#v", rows[0])
		}
	})

	t.Run("csv", func(t *testing.T) {
		var stdout bytes.Buffer
		app := &App{Output: csvOutput, Stdout: &stdout}
		if err := app.emitRecordsWithContext(testRecords(1), false, []string{"type", "name"}); err != nil {
			t.Fatalf("emit records: %v", err)
		}
		if got, want := stdout.String(), "type,name\nHOST,app1.example.com\n"; got != want {
			t.Fatalf("csv output = %q, want %q", got, want)
		}
	})

	t.Run("table", func(t *testing.T) {
		var stdout bytes.Buffer
		app := &App{Output: tableOutput, Stdout: &stdout}
		if err := app.emitRecordsWithContext(testRecords(1), false, []string{"name", "value"}); err != nil {
			t.Fatalf("emit records: %v", err)
		}
		output := stdout.String()
		for _, want := range []string{"name", "value", "app1.example.com", "192.0.2.1"} {
			if !strings.Contains(output, want) {
				t.Fatalf("table output missing %q:\n%s", want, output)
			}
		}
		for _, unwanted := range []string{"type", "zone", "comment"} {
			if strings.Contains(output, unwanted) {
				t.Fatalf("table output included unselected %q:\n%s", unwanted, output)
			}
		}
	})
}

func TestEmitRecordsTablePrintsTotalOnlyAboveFive(t *testing.T) {
	var stdout bytes.Buffer
	app := &App{Output: tableOutput, Stdout: &stdout}
	if err := app.emitRecords(testRecords(6)); err != nil {
		t.Fatalf("emit records: %v", err)
	}
	output := stdout.String()
	for _, want := range []string{"DNS Records", "Total records: 6"} {
		if !strings.Contains(output, want) {
			t.Fatalf("table output missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "DNS Records (6)") {
		t.Fatalf("table title still contains record count:\n%s", output)
	}

	stdout.Reset()
	if err := app.emitRecords(testRecords(5)); err != nil {
		t.Fatalf("emit records: %v", err)
	}
	output = stdout.String()
	if strings.Contains(output, "Total records:") {
		t.Fatalf("table output contains total for five records:\n%s", output)
	}
	if strings.Contains(output, "DNS Records (5)") {
		t.Fatalf("table title still contains record count:\n%s", output)
	}
}

func TestEmitRecordsTableAlwaysPrintsCurrentContext(t *testing.T) {
	app := testApp(t)
	var stdout bytes.Buffer
	app.Stdout = &stdout
	writeCompletionProfile(t, app, "https://infoblox.invalid")

	if err := app.emitRecords(testRecords(1)); err != nil {
		t.Fatalf("emit records: %v", err)
	}
	if output := stdout.String(); !strings.Contains(output, "Current Context:") {
		t.Fatalf("record table output missing current context:\n%s", output)
	}
}

func TestEmitRecordsTablePrintsContextBeforeTotal(t *testing.T) {
	app := testApp(t)
	var stdout bytes.Buffer
	app.Stdout = &stdout
	writeCompletionProfile(t, app, "https://infoblox.invalid")

	if err := app.emitRecordsWithContext(testRecords(6), true); err != nil {
		t.Fatalf("emit records: %v", err)
	}
	output := stdout.String()
	var footerLine string
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "Current Context:") {
			footerLine = line
			break
		}
	}
	tableIndex := strings.Index(output, "DNS Records")
	contextIndex := strings.Index(output, "Current Context:")
	totalIndex := strings.Index(output, "Total records: 6")
	if tableIndex < 0 || contextIndex < 0 || totalIndex < 0 {
		t.Fatalf("output missing table, context, or total:\n%s", output)
	}
	if !(tableIndex < contextIndex && contextIndex < totalIndex) {
		t.Fatalf("want table, then current context, then total records:\n%s", output)
	}
	if !strings.Contains(footerLine, "Current Context:") || !strings.Contains(footerLine, "Total records: 6") {
		t.Fatalf("context and total are not on the same footer line:\n%s", output)
	}
	if wantBadge := recordTotalBadgeStyle.Render("Total records: 6"); !strings.Contains(footerLine, wantBadge) {
		t.Fatalf("footer line missing styled total badge %q:\n%s", wantBadge, footerLine)
	}
}

func TestEmitRecordsTableWrapsLongValueAndComment(t *testing.T) {
	var stdout bytes.Buffer
	app := &App{Output: tableOutput, Stdout: &stdout}
	longValue := "v=spf1 include:mail.example.net include:spf.example.org include:really-long-provider.example.net -all"
	longComment := "This record has a long operator comment that should wrap in the table output"
	records := []TypedRecord{{
		Type: "txt",
		Item: map[string]any{
			"name":    "spf.example.com",
			"text":    longValue,
			"zone":    "example.com",
			"comment": longComment,
		},
	}}

	if err := app.emitRecords(records); err != nil {
		t.Fatalf("emit records: %v", err)
	}
	output := stdout.String()
	for _, want := range []string{
		"include:mail.example.net",
		"include:spf.example.org",
		"long operator comment",
		"should wrap in the table",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("wrapped table output missing %q:\n%s", want, output)
		}
	}
	if !strings.Contains(output, "\n│") {
		t.Fatalf("table output did not render multiline rows:\n%s", output)
	}
}

func TestWrapRecordTableCellKeepsLinesWithinWidth(t *testing.T) {
	wrapped := wrapRecordTableCell("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", 16)
	lines := strings.Split(wrapped, "\n")
	if len(lines) < 3 {
		t.Fatalf("wrapped lines = %#v, want multiple lines", lines)
	}
	for _, line := range lines {
		if width := lipgloss.Width(line); width > 16 {
			t.Fatalf("wrapped line width = %d, want <= 16: %q", width, line)
		}
	}
}

func TestEmitRecordsJSONDoesNotWrapLongValueAndComment(t *testing.T) {
	var stdout bytes.Buffer
	app := &App{Output: jsonOutput, Stdout: &stdout}
	longValue := "v=spf1 include:mail.example.net include:spf.example.org include:really-long-provider.example.net -all"
	longComment := "This record has a long operator comment that should stay plain in json output"
	records := []TypedRecord{{
		Type: "txt",
		Item: map[string]any{
			"name":    "spf.example.com",
			"text":    longValue,
			"zone":    "example.com",
			"comment": longComment,
		},
	}}

	if err := app.emitRecords(records); err != nil {
		t.Fatalf("emit records: %v", err)
	}
	output := stdout.String()
	for _, want := range []string{longValue, longComment} {
		if !strings.Contains(output, want) {
			t.Fatalf("json output did not preserve %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, `\n`) {
		t.Fatalf("json output contains wrapped newline escapes:\n%s", output)
	}
}

func TestEmitZonesTablePrintsTotalOnlyAboveFive(t *testing.T) {
	var stdout bytes.Buffer
	app := &App{Output: tableOutput, Stdout: &stdout}
	if err := app.emitZones(testZones(6)); err != nil {
		t.Fatalf("emit zones: %v", err)
	}
	output := stdout.String()
	for _, want := range []string{"DNS Zones", "Total zones: 6"} {
		if !strings.Contains(output, want) {
			t.Fatalf("table output missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "DNS Zones (6)") {
		t.Fatalf("table title still contains zone count:\n%s", output)
	}

	stdout.Reset()
	if err := app.emitZones(testZones(5)); err != nil {
		t.Fatalf("emit zones: %v", err)
	}
	output = stdout.String()
	if strings.Contains(output, "Total zones:") {
		t.Fatalf("table output contains total for five zones:\n%s", output)
	}
	if strings.Contains(output, "DNS Zones (5)") {
		t.Fatalf("table title still contains zone count:\n%s", output)
	}
}

func TestEmitZonesJSONDoesNotPrintTableTotal(t *testing.T) {
	var stdout bytes.Buffer
	app := &App{Output: jsonOutput, Stdout: &stdout}
	if err := app.emitZones(testZones(6)); err != nil {
		t.Fatalf("emit zones: %v", err)
	}
	output := stdout.String()
	if strings.Contains(output, "Total zones:") {
		t.Fatalf("json output contains table total footer: %s", output)
	}
	if strings.Contains(output, "DNS Zones") {
		t.Fatalf("json output contains table title: %s", output)
	}
}

func TestZoneListOptionHelpers(t *testing.T) {
	formats, err := parseZoneFormats("forward,IPV4")
	if err != nil {
		t.Fatalf("parse formats: %v", err)
	}
	if got := strings.Join(formats, ","); got != "FORWARD,IPV4" {
		t.Fatalf("formats = %q, want FORWARD,IPV4", got)
	}
	if _, err := parseZoneFormats("external"); err == nil || !strings.Contains(err.Error(), `unsupported zone type "EXTERNAL"`) {
		t.Fatalf("parse invalid format error = %v", err)
	}

	zoneSort, err := parseZoneSort("-comment", true)
	if err != nil {
		t.Fatalf("parse zone sort: %v", err)
	}
	if !zoneSort.Enabled || zoneSort.Field != "comment" || !zoneSort.Desc {
		t.Fatalf("zone sort = %#v", zoneSort)
	}
	if _, err := parseZoneSort("serial", true); err == nil || !strings.Contains(err.Error(), `unsupported zone sort field "serial"`) {
		t.Fatalf("parse invalid sort error = %v", err)
	}

	columns, err := parseZoneColumns("zone,comment")
	if err != nil {
		t.Fatalf("parse zone columns: %v", err)
	}
	if got := strings.Join(columns, ","); got != "zone,comment" {
		t.Fatalf("columns = %q, want zone,comment", got)
	}
	if _, err := parseZoneColumns("zone,zone"); err == nil || !strings.Contains(err.Error(), `duplicate zone column "zone"`) {
		t.Fatalf("parse duplicate columns error = %v", err)
	}
}

func TestFilterAndSortListedZones(t *testing.T) {
	zones := []map[string]any{
		{"fqdn": "alpha.example.com", "zone_format": "FORWARD", "comment": "keep"},
		{"fqdn": "beta.example.com", "zone_format": "IPV4", "comment": "keep"},
		{"fqdn": "zeta.example.com", "zone_format": "FORWARD", "comment": "skip me"},
		{"fqdn": "delta.example.com", "zone_format": "FORWARD", "comment": "keep"},
	}
	filtered := filterListedZones(zones, []string{"FORWARD"}, []string{"skip"})
	applyZoneSort(filtered, ZoneSort{Enabled: true, Field: "zone", Desc: true})
	var names []string
	for _, zone := range filtered {
		names = append(names, cleanString(zone["fqdn"]))
	}
	if got := strings.Join(names, ","); got != "delta.example.com,alpha.example.com" {
		t.Fatalf("filtered zones = %q", got)
	}
}

func TestEmitZonesWithSelectedColumns(t *testing.T) {
	zones := []map[string]any{{
		"fqdn":        "example.com",
		"view":        "default",
		"zone_format": "FORWARD",
		"ns_group":    "default",
		"comment":     "primary",
	}}

	t.Run("json", func(t *testing.T) {
		var stdout bytes.Buffer
		app := &App{Output: jsonOutput, Stdout: &stdout}
		if err := app.emitZones(zones, []string{"zone", "comment"}); err != nil {
			t.Fatalf("emit zones: %v", err)
		}
		var rows []map[string]any
		if err := json.Unmarshal(stdout.Bytes(), &rows); err != nil {
			t.Fatalf("decode zones JSON: %v\n%s", err, stdout.String())
		}
		if len(rows) != 1 || len(rows[0]) != 2 || cleanString(rows[0]["zone"]) != "example.com" || cleanString(rows[0]["comment"]) != "primary" {
			t.Fatalf("rows = %#v", rows)
		}
	})

	t.Run("csv", func(t *testing.T) {
		var stdout bytes.Buffer
		app := &App{Output: csvOutput, Stdout: &stdout}
		if err := app.emitZones(zones, []string{"zone", "format"}); err != nil {
			t.Fatalf("emit zones: %v", err)
		}
		if got, want := stdout.String(), "zone,format\nexample.com,FORWARD\n"; got != want {
			t.Fatalf("csv output = %q, want %q", got, want)
		}
	})
}

func TestRunZoneInfoTablePrintsFieldsAsRows(t *testing.T) {
	app, stdout := zoneInfoTestApp(t, tableOutput)
	if err := app.runZoneInfo("example.com"); err != nil {
		t.Fatalf("zone info: %v", err)
	}
	output := stdout.String()
	for _, want := range []string{
		"Current Context:",
		"View: default",
		"DNS Zone: example.com",
		"field",
		"value",
		"Zone",
		"example.com",
		"Serial Number",
		"2026050701",
		"Refresh",
		"10800 ( 3 hours )",
		"Retry",
		"3600 ( 1 hour )",
		"Expiry",
		"2419200 ( 28 days )",
		"Negative Caching Ttl",
		"900 ( 15 minutes )",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("zone info table missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "zone_auth/ref:example.com/default") {
		t.Fatalf("zone info table still includes ref:\n%s", output)
	}
	if strings.Contains(output, "Zone  View  Format") {
		t.Fatalf("zone info table still appears column-oriented:\n%s", output)
	}
}

func TestRunZoneInfoJSONKeepsObjectShape(t *testing.T) {
	app, stdout := zoneInfoTestApp(t, jsonOutput)
	if err := app.runZoneInfo("example.com"); err != nil {
		t.Fatalf("zone info: %v", err)
	}
	output := stdout.String()
	for _, want := range []string{
		`"zone": "example.com"`,
		`"serial_number": "2026050701"`,
		`"expiry": "2419200"`,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("zone info json missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, `"Field"`) || strings.Contains(output, `"Value"`) {
		t.Fatalf("zone info json used table field/value shape:\n%s", output)
	}
	if strings.Contains(output, "Current Context:") || strings.Contains(output, "View: default") {
		t.Fatalf("zone info json contains table-only context line:\n%s", output)
	}
	if strings.Contains(output, `"ref"`) || strings.Contains(output, "zone_auth/ref:example.com/default") {
		t.Fatalf("zone info json still includes ref:\n%s", output)
	}
	if strings.Contains(output, "7 days") {
		t.Fatalf("zone info json contains table-only human duration:\n%s", output)
	}
}

func TestFormatSecondsWithHumanDuration(t *testing.T) {
	tests := map[string]string{
		"300":   "300 ( 5 minutes )",
		"86400": "86400 ( 1 day )",
		"3661":  "3661 ( 1 hour 1 minute 1 second )",
		"bad":   "bad",
		"":      "",
	}
	for value, want := range tests {
		if got := formatSecondsWithHumanDuration(value); got != want {
			t.Fatalf("formatSecondsWithHumanDuration(%q) = %q, want %q", value, got, want)
		}
	}
}

func TestCleanIntegerStringNormalizesScientificNotation(t *testing.T) {
	tests := map[any]string{
		2.4192e+06:   "2419200",
		"2.4192e+06": "2419200",
		86400:        "86400",
		"bad":        "bad",
	}
	for value, want := range tests {
		if got := cleanIntegerString(value); got != want {
			t.Fatalf("cleanIntegerString(%v) = %q, want %q", value, got, want)
		}
	}
}

func TestSearchableRecordZonesSkipsSecondaryPrimaryTypes(t *testing.T) {
	zones := searchableRecordZones([]map[string]any{
		{"fqdn": "primary.example.com", "primary_type": "Grid"},
		{"fqdn": "secondary.example.com", "primary_type": "External"},
		{"fqdn": "unset.example.com", "primary_type": "None"},
		{"fqdn": "legacy.example.com"},
	})
	var names []string
	for _, zone := range zones {
		names = append(names, cleanString(zone["fqdn"]))
	}
	got := strings.Join(names, ",")
	want := "primary.example.com,legacy.example.com"
	if got != want {
		t.Fatalf("searchable zones = %q, want %q", got, want)
	}
}

func TestSearchSortsByType(t *testing.T) {
	app := testApp(t)
	profile := Profile{
		Name:        defaultProfileName,
		Server:      "https://infoblox.invalid",
		WAPIVersion: defaultWAPIVersion,
		DNSView:     "default",
		DefaultZone: "example.com",
	}
	if err := app.writeCachedZones(profile, []map[string]any{
		{"fqdn": "example.com", "view": "default", "zone_format": "FORWARD", "primary_type": "Grid"},
	}, time.Now()); err != nil {
		t.Fatalf("write zone cache: %v", err)
	}
	if err := app.writeCachedRecords(profile, "example.com", "2026050801", []map[string]any{
		{"type": "record:txt", "name": "txt.example.com", "text": "match", "zone": "example.com"},
		{"type": "record:a", "name": "a.example.com", "address": "192.0.2.10", "zone": "example.com"},
	}, time.Now()); err != nil {
		t.Fatalf("write record cache: %v", err)
	}

	records, err := app.collectSearchResults(profile, &WapiClient{View: "default"}, SearchOptions{
		Keyword: "example.com",
		Sort:    RecordSort{Enabled: true, Field: "type"},
	})
	if err != nil {
		t.Fatalf("collect search results: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2: %#v", len(records), records)
	}
	if got := canonicalDisplayRecordType(records[0].Type) + "," + canonicalDisplayRecordType(records[1].Type); got != "a,txt" {
		t.Fatalf("record types = %s, want a,txt", got)
	}
}

func TestRecordSortRejectsUnsupportedField(t *testing.T) {
	app := testApp(t)
	err := app.Execute([]string{"dns", "list", "--sort", "owner"})
	if err == nil {
		t.Fatal("dns list accepted unsupported sort field")
	}
	for _, want := range []string{`unsupported sort field "owner"`, "name, type, value, zone, ttl, created, comment"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error missing %q: %v", want, err)
		}
	}
}

func TestRecordSortValueUsesNumericIPOrder(t *testing.T) {
	records := []TypedRecord{
		{Type: "a", Item: map[string]any{"name": "ten.example.com", "address": "192.0.2.10", "zone": "example.com"}},
		{Type: "cname", Item: map[string]any{"name": "alias.example.com", "canonical": "target.example.com", "zone": "example.com"}},
		{Type: "a", Item: map[string]any{"name": "two.example.com", "address": "192.0.2.2", "zone": "example.com"}},
		{Type: "a", Item: map[string]any{"name": "private.example.com", "address": "10.0.0.1", "zone": "example.com"}},
	}

	applyRecordSort(records, RecordSort{Enabled: true, Field: "value"})
	assertSortedRecordNames(t, records, []string{"private.example.com", "two.example.com", "ten.example.com", "alias.example.com"})

	applyRecordSort(records, RecordSort{Enabled: true, Field: "value", Desc: true})
	assertSortedRecordNames(t, records, []string{"ten.example.com", "two.example.com", "private.example.com", "alias.example.com"})
}

func TestRecordSortNameUsesNumericIPOrder(t *testing.T) {
	records := []TypedRecord{
		{Type: "ptr", Item: map[string]any{"name": "10", "address": "192.0.2.10", "ptrdname": "ten.example.com", "zone": "192.0.2.0/24"}},
		{Type: "a", Item: map[string]any{"name": "app.example.com", "address": "192.0.2.1", "zone": "example.com"}},
		{Type: "ptr", Item: map[string]any{"name": "2", "address": "192.0.2.2", "ptrdname": "two.example.com", "zone": "192.0.2.0/24"}},
	}

	applyRecordSort(records, RecordSort{Enabled: true, Field: "name"})
	assertSortedRecordNames(t, records, []string{"192.0.2.2", "192.0.2.10", "app.example.com"})

	applyRecordSort(records, RecordSort{Enabled: true, Field: "name", Desc: true})
	assertSortedRecordNames(t, records, []string{"192.0.2.10", "192.0.2.2", "app.example.com"})
}

func TestDefaultRecordSortUsesNumericReverseIPOrder(t *testing.T) {
	records := []TypedRecord{
		{Type: "ptr", Item: map[string]any{"name": "10", "address": "192.0.2.10", "ptrdname": "ten.example.com", "zone": "192.0.2.0/24"}},
		{Type: "ptr", Item: map[string]any{"name": "100", "address": "192.0.2.100", "ptrdname": "hundred.example.com", "zone": "192.0.2.0/24"}},
		{Type: "ptr", Item: map[string]any{"name": "2", "address": "192.0.2.2", "ptrdname": "two.example.com", "zone": "192.0.2.0/24"}},
	}

	sortRecords(records)
	assertSortedRecordNames(t, records, []string{"192.0.2.2", "192.0.2.10", "192.0.2.100"})
}

func TestDefaultRecordSortPreservesForwardStringOrder(t *testing.T) {
	records := []TypedRecord{
		{Type: "a", Item: map[string]any{"name": "charlie.example.com", "address": "192.0.2.30", "zone": "example.com"}},
		{Type: "a", Item: map[string]any{"name": "alpha.example.com", "address": "192.0.2.10", "zone": "example.com"}},
		{Type: "a", Item: map[string]any{"name": "bravo.example.com", "address": "192.0.2.20", "zone": "example.com"}},
	}

	sortRecords(records)
	assertSortedRecordNames(t, records, []string{"alpha.example.com", "bravo.example.com", "charlie.example.com"})
}

func TestSearchDefaultsToCurrentZoneOnly(t *testing.T) {
	zones := []map[string]any{
		{"fqdn": "example.com", "view": "default", "zone_format": "FORWARD", "primary_type": "Grid"},
		{"fqdn": "child.example.com", "view": "default", "zone_format": "FORWARD", "primary_type": "Grid"},
		{"fqdn": "other.com", "view": "default", "zone_format": "FORWARD", "primary_type": "Grid"},
	}
	var mu sync.Mutex
	requestedZones := map[string]int{}
	server := searchScopeServer(t, zones, requestedZones, &mu)
	defer server.Close()

	app := testApp(t)
	records, err := app.collectSearchResults(
		Profile{DNSView: "default", DefaultZone: "example.com"},
		testWapiClient(server),
		SearchOptions{Keyword: "test"},
	)
	if err != nil {
		t.Fatalf("collect search results: %v", err)
	}
	if len(records) != 1 || recordName(records[0].Item, records[0].Type) != "test.example.com" {
		t.Fatalf("records = %#v", records)
	}
	mu.Lock()
	defer mu.Unlock()
	if requestedZones["example.com"] != 1 {
		t.Fatalf("example.com requests = %d, all requests = %#v", requestedZones["example.com"], requestedZones)
	}
	if requestedZones["child.example.com"] != 0 || requestedZones["other.com"] != 0 {
		t.Fatalf("default search queried out-of-scope zones: %#v", requestedZones)
	}
}

func TestSearchInfersZoneFromFQDNKeyword(t *testing.T) {
	zones := []map[string]any{
		{"fqdn": "latrobe.edu.au", "view": "default", "zone_format": "FORWARD", "primary_type": "Grid"},
		{"fqdn": "net.latrobe.edu.au", "view": "default", "zone_format": "FORWARD", "primary_type": "Grid"},
		{"fqdn": "other.edu.au", "view": "default", "zone_format": "FORWARD", "primary_type": "Grid"},
	}
	var mu sync.Mutex
	requestedZones := map[string]int{}
	server := searchScopeServerWithRecords(t, zones, requestedZones, &mu, func(zone string) []map[string]any {
		if zone == "net.latrobe.edu.au" {
			return []map[string]any{
				{"type": "HOST_IPV4ADDR", "name": "ben-dr-vss", "address": "192.0.2.10", "zone": zone},
			}
		}
		return []map[string]any{
			{"type": "HOST_IPV4ADDR", "name": "other." + zone, "address": "192.0.2.20", "zone": zone},
		}
	})
	defer server.Close()

	app := testApp(t)
	records, err := app.collectSearchResults(
		Profile{DNSView: "default", DefaultZone: "latrobe.edu.au"},
		testWapiClient(server),
		SearchOptions{Keyword: "ben-dr-vss.net.latrobe.edu.au"},
	)
	if err != nil {
		t.Fatalf("collect search results: %v", err)
	}
	if len(records) != 1 || recordName(records[0].Item, records[0].Type) != "ben-dr-vss" {
		t.Fatalf("records = %#v", records)
	}
	mu.Lock()
	defer mu.Unlock()
	if requestedZones["net.latrobe.edu.au"] != 1 {
		t.Fatalf("net.latrobe.edu.au requests = %d, all requests = %#v", requestedZones["net.latrobe.edu.au"], requestedZones)
	}
	if requestedZones["latrobe.edu.au"] != 0 || requestedZones["other.edu.au"] != 0 {
		t.Fatalf("fqdn search queried wrong zones: %#v", requestedZones)
	}
}

func TestSearchExplicitZoneSkipsFQDNInference(t *testing.T) {
	zones := []map[string]any{
		{"fqdn": "latrobe.edu.au", "view": "default", "zone_format": "FORWARD", "primary_type": "Grid"},
		{"fqdn": "net.latrobe.edu.au", "view": "default", "zone_format": "FORWARD", "primary_type": "Grid"},
	}
	var mu sync.Mutex
	requestedZones := map[string]int{}
	server := searchScopeServer(t, zones, requestedZones, &mu)
	defer server.Close()

	app := testApp(t)
	_, err := app.collectSearchResults(
		Profile{DNSView: "default", DefaultZone: "example.com"},
		testWapiClient(server),
		SearchOptions{Keyword: "test.net.latrobe.edu.au", Zone: "latrobe.edu.au"},
	)
	if err != nil {
		t.Fatalf("collect search results: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if requestedZones["latrobe.edu.au"] != 1 {
		t.Fatalf("latrobe.edu.au requests = %d, all requests = %#v", requestedZones["latrobe.edu.au"], requestedZones)
	}
	if requestedZones["net.latrobe.edu.au"] != 0 {
		t.Fatalf("explicit zone search inferred fqdn zone: %#v", requestedZones)
	}
}

func TestGlobalSearchKeepsGlobalScopeForFQDNKeyword(t *testing.T) {
	zones := []map[string]any{
		{"fqdn": "latrobe.edu.au", "view": "default", "zone_format": "FORWARD", "primary_type": "Grid"},
		{"fqdn": "net.latrobe.edu.au", "view": "default", "zone_format": "FORWARD", "primary_type": "Grid"},
	}
	var mu sync.Mutex
	requestedZones := map[string]int{}
	server := searchScopeServer(t, zones, requestedZones, &mu)
	defer server.Close()

	app := testApp(t)
	_, err := app.collectSearchResults(
		Profile{DNSView: "default", DefaultZone: "latrobe.edu.au"},
		testWapiClient(server),
		SearchOptions{Keyword: "test.net.latrobe.edu.au", Global: true},
	)
	if err != nil {
		t.Fatalf("collect search results: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if requestedZones["latrobe.edu.au"] != 1 || requestedZones["net.latrobe.edu.au"] != 1 {
		t.Fatalf("global search did not query all zones: %#v", requestedZones)
	}
}

func TestGlobalSearchFQDNMatchesRelativeNameInInferredZone(t *testing.T) {
	const target = "wod-0003-sr02-v990.net.latrobe.edu.au"
	zones := []map[string]any{
		{"fqdn": "latrobe.edu.au", "view": "default", "zone_format": "FORWARD", "primary_type": "Grid"},
		{"fqdn": "net.latrobe.edu.au", "view": "default", "zone_format": "FORWARD", "primary_type": "Grid"},
		{"fqdn": "2.0.192.in-addr.arpa", "view": "default", "zone_format": "IPV4", "primary_type": "Grid"},
		{"fqdn": "other.edu.au", "view": "default", "zone_format": "FORWARD", "primary_type": "Grid"},
	}
	var mu sync.Mutex
	requestedZones := map[string]int{}
	server := searchScopeServerWithRecords(t, zones, requestedZones, &mu, func(zone string) []map[string]any {
		switch zone {
		case "net.latrobe.edu.au":
			return []map[string]any{
				{"type": "HOST_IPV4ADDR", "name": "wod-0003-sr02-v990", "address": "192.0.2.10", "zone": zone},
			}
		case "2.0.192.in-addr.arpa":
			return []map[string]any{
				{"type": "PTR", "name": "10", "ipv4addr": "192.0.2.10", "ptrdname": target, "zone": zone},
			}
		case "other.edu.au":
			return []map[string]any{
				{"type": "HOST_IPV4ADDR", "name": "wod-0003-sr02-v990", "address": "192.0.2.20", "zone": zone},
			}
		default:
			return nil
		}
	})
	defer server.Close()

	app := testApp(t)
	records, err := app.collectSearchResults(
		Profile{DNSView: "default", DefaultZone: "latrobe.edu.au"},
		testWapiClient(server),
		SearchOptions{Keyword: target, Global: true},
	)
	if err != nil {
		t.Fatalf("collect search results: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("records = %d, want forward and PTR matches: %#v", len(records), records)
	}
	foundForward := false
	foundPTR := false
	for _, record := range records {
		if record.Type == "host" && cleanString(record.Item["zone"]) == "net.latrobe.edu.au" && recordName(record.Item, record.Type) == "wod-0003-sr02-v990" {
			foundForward = true
		}
		if record.Type == "ptr" && cleanString(record.Item["zone"]) == "2.0.192.in-addr.arpa" && recordValue(record.Type, record.Item) == target {
			foundPTR = true
		}
		if cleanString(record.Item["zone"]) == "other.edu.au" {
			t.Fatalf("global fqdn alias matched unrelated zone: %#v", records)
		}
	}
	if !foundForward || !foundPTR {
		t.Fatalf("records missing forward=%t ptr=%t: %#v", foundForward, foundPTR, records)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, zone := range []string{"latrobe.edu.au", "net.latrobe.edu.au", "2.0.192.in-addr.arpa", "other.edu.au"} {
		if requestedZones[zone] != 1 {
			t.Fatalf("%s requests = %d, all requests = %#v", zone, requestedZones[zone], requestedZones)
		}
	}
}

func TestSearchTypeFilterIncludesNS(t *testing.T) {
	encodedNSRef := base64.RawURLEncoding.EncodeToString([]byte("dns.bind_ns$example.com"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/zone_auth"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{
					{"fqdn": "example.com", "view": "default", "zone_format": "FORWARD", "primary_type": "Grid"},
				},
			})
		case strings.HasSuffix(r.URL.Path, "/allrecords"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{
					{
						"_ref":       "allrecords/" + encodedNSRef + ":example.com/default",
						"type":       "UNSUPPORTED",
						"name":       "example.com",
						"nameserver": "ns1.example.com",
						"zone":       "example.com",
					},
					{
						"type":    "HOST_IPV4ADDR",
						"name":    "app.example.com",
						"address": "192.0.2.10",
						"zone":    "example.com",
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	app := testApp(t)
	records, err := app.collectSearchResults(
		Profile{DNSView: "default", DefaultZone: "example.com"},
		testWapiClient(server),
		SearchOptions{Keyword: "example.com", Types: []string{"ns"}},
	)
	if err != nil {
		t.Fatalf("collect search results: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1: %#v", len(records), records)
	}
	if records[0].Type != "ns" {
		t.Fatalf("record type = %q, want ns", records[0].Type)
	}
}

func TestSearchRecursiveIncludesChildZones(t *testing.T) {
	zones := []map[string]any{
		{"fqdn": "example.com", "view": "default", "zone_format": "FORWARD", "primary_type": "Grid"},
		{"fqdn": "child.example.com", "view": "default", "zone_format": "FORWARD", "primary_type": "Grid"},
		{"fqdn": "other.com", "view": "default", "zone_format": "FORWARD", "primary_type": "Grid"},
	}
	var mu sync.Mutex
	requestedZones := map[string]int{}
	server := searchScopeServer(t, zones, requestedZones, &mu)
	defer server.Close()

	app := testApp(t)
	records, err := app.collectSearchResults(
		Profile{DNSView: "default", DefaultZone: "example.com"},
		testWapiClient(server),
		SearchOptions{Keyword: "test", Recursive: true},
	)
	if err != nil {
		t.Fatalf("collect search results: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2: %#v", len(records), records)
	}
	mu.Lock()
	defer mu.Unlock()
	if requestedZones["example.com"] != 1 || requestedZones["child.example.com"] != 1 {
		t.Fatalf("recursive search requests = %#v", requestedZones)
	}
	if requestedZones["other.com"] != 0 {
		t.Fatalf("recursive search queried unrelated zone: %#v", requestedZones)
	}
}

func TestSearchRecursiveCannotBeGlobal(t *testing.T) {
	app := testApp(t)
	_, err := app.collectSearchResults(Profile{DNSView: "default"}, &WapiClient{View: "default"}, SearchOptions{
		Keyword:   "test",
		Global:    true,
		Recursive: true,
	})
	if err == nil {
		t.Fatal("collect search results succeeded, want error")
	}
	if !strings.Contains(err.Error(), "--recursive cannot be used with -g/--global search") {
		t.Fatalf("error = %v", err)
	}
}

func TestGlobalSearchSkipsSecondaryZones(t *testing.T) {
	var mu sync.Mutex
	requestedZones := map[string]int{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/zone_auth"):
			if fqdn := r.URL.Query().Get("fqdn"); fqdn != "" {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"result": []map[string]any{
						{"fqdn": fqdn, "view": "default", "zone_format": "FORWARD", "primary_type": "Grid", "soa_serial_number": "2026050801"},
					},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{
					{
						"fqdn":         "primary.example.com",
						"view":         "default",
						"zone_format":  "FORWARD",
						"primary_type": "Grid",
					},
					{
						"fqdn":         "secondary.example.com",
						"view":         "default",
						"zone_format":  "FORWARD",
						"primary_type": "External",
					},
				},
			})
		case strings.HasSuffix(r.URL.Path, "/allrecords"):
			zone := r.URL.Query().Get("zone")
			mu.Lock()
			requestedZones[zone]++
			mu.Unlock()
			if zone == "secondary.example.com" {
				http.Error(w, `{"text":"Secondary zone data unavailable."}`, http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{
					{
						"type":    "HOST_IPV4ADDR",
						"name":    "test.primary.example.com",
						"address": "192.0.2.10",
						"zone":    zone,
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	app := testApp(t)

	client := &WapiClient{
		Server:      server.URL,
		ReadServer:  server.URL,
		WAPIVersion: defaultWAPIVersion,
		View:        "default",
		httpClient:  server.Client(),
	}
	records, err := app.collectSearchResults(Profile{DNSView: "default"}, client, SearchOptions{Keyword: "test", Global: true})
	if err != nil {
		t.Fatalf("collect search results: %v", err)
	}
	if len(records) != 1 || recordName(records[0].Item, records[0].Type) != "test.primary.example.com" {
		t.Fatalf("records = %#v", records)
	}
	mu.Lock()
	defer mu.Unlock()
	if requestedZones["secondary.example.com"] != 0 {
		t.Fatalf("secondary zone was queried: %#v", requestedZones)
	}
	if requestedZones["primary.example.com"] != 1 {
		t.Fatalf("primary zone requests = %#v", requestedZones)
	}
}

func TestSearchAcrossZonesFetchesRecordsInParallel(t *testing.T) {
	workerLimit := 3
	zoneCount := workerLimit + 4
	zones := make([]map[string]any, 0, zoneCount)
	for i := 0; i < zoneCount; i++ {
		zones = append(zones, map[string]any{
			"fqdn":         fmt.Sprintf("zone%d.example.com", i),
			"view":         "default",
			"zone_format":  "FORWARD",
			"primary_type": "Grid",
		})
	}

	var mu sync.Mutex
	currentAllRecords := 0
	maxAllRecords := 0
	allRecordRequests := 0
	requestedZones := map[string]int{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/zone_auth"):
			if fqdn := r.URL.Query().Get("fqdn"); fqdn != "" {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"result": []map[string]any{
						{"fqdn": fqdn, "view": "default", "zone_format": "FORWARD", "primary_type": "Grid", "soa_serial_number": "2026050801"},
					},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"result": zones})
		case strings.HasSuffix(r.URL.Path, "/allrecords"):
			zone := r.URL.Query().Get("zone")
			mu.Lock()
			allRecordRequests++
			requestedZones[zone]++
			currentAllRecords++
			if currentAllRecords > maxAllRecords {
				maxAllRecords = currentAllRecords
			}
			mu.Unlock()
			defer func() {
				mu.Lock()
				currentAllRecords--
				mu.Unlock()
			}()

			time.Sleep(50 * time.Millisecond)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{
					{
						"type":    "HOST_IPV4ADDR",
						"name":    "test." + zone,
						"address": "192.0.2.10",
						"zone":    zone,
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	app := testApp(t)
	writeConfigForSettings(t, app, ConfigSettings{
		CacheTTLSeconds:      defaultCacheTTLSeconds,
		DNSSearchWorkerLimit: workerLimit,
	})
	client := &WapiClient{
		Server:      server.URL,
		ReadServer:  server.URL,
		WAPIVersion: defaultWAPIVersion,
		View:        "default",
		httpClient:  server.Client(),
	}
	records, err := app.collectSearchResults(Profile{DNSView: "default"}, client, SearchOptions{Keyword: "test", Global: true})
	if err != nil {
		t.Fatalf("collect search results: %v", err)
	}
	if len(records) != zoneCount {
		t.Fatalf("records = %d, want %d: %#v", len(records), zoneCount, records)
	}

	mu.Lock()
	defer mu.Unlock()
	if allRecordRequests != zoneCount {
		t.Fatalf("allrecords requests = %d, want %d", allRecordRequests, zoneCount)
	}
	if maxAllRecords <= 1 {
		t.Fatalf("allrecords requests were not parallel; max concurrency = %d", maxAllRecords)
	}
	if maxAllRecords > workerLimit {
		t.Fatalf("max concurrency = %d, want <= %d", maxAllRecords, workerLimit)
	}
	for _, zone := range zones {
		name := cleanString(zone["fqdn"])
		if requestedZones[name] != 1 {
			t.Fatalf("zone %q requests = %d, all requests = %#v", name, requestedZones[name], requestedZones)
		}
	}
}

func TestSearchProgressReportsWorkerEvents(t *testing.T) {
	zones := []map[string]any{
		{"fqdn": "one.example.com", "view": "default", "zone_format": "FORWARD", "primary_type": "Grid"},
		{"fqdn": "two.example.com", "view": "default", "zone_format": "FORWARD", "primary_type": "Grid"},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/zone_auth"):
			if fqdn := r.URL.Query().Get("fqdn"); fqdn != "" {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"result": []map[string]any{
						{"fqdn": fqdn, "view": "default", "zone_format": "FORWARD", "primary_type": "Grid", "soa_serial_number": "2026050801"},
					},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"result": zones})
		case strings.HasSuffix(r.URL.Path, "/allrecords"):
			zone := r.URL.Query().Get("zone")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{
					{
						"type":    "HOST_IPV4ADDR",
						"name":    "test." + zone,
						"address": "192.0.2.10",
						"zone":    zone,
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	app := testApp(t)
	client := &WapiClient{
		Server:      server.URL,
		ReadServer:  server.URL,
		WAPIVersion: defaultWAPIVersion,
		View:        "default",
		httpClient:  server.Client(),
	}

	var mu sync.Mutex
	var events []SearchProgressEvent
	progress := func(event SearchProgressEvent) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, event)
	}
	records, err := app.collectSearchResults(Profile{DNSView: "default"}, client, SearchOptions{Keyword: "test", Global: true, Progress: progress})
	if err != nil {
		t.Fatalf("collect search results: %v", err)
	}
	if len(records) != len(zones) {
		t.Fatalf("records = %d, want %d", len(records), len(zones))
	}

	mu.Lock()
	defer mu.Unlock()
	var starts, done, matches int
	var sawWorkersStage bool
	for _, event := range events {
		switch event.Kind {
		case searchProgressStage:
			if event.Stage == "Starting workers" && event.TotalZones == len(zones) {
				sawWorkersStage = true
			}
		case searchProgressWorkerStart:
			starts++
		case searchProgressWorkerDone:
			done++
			if event.Source != recordCacheSourceAllRecords {
				t.Fatalf("worker source = %q, want %q", event.Source, recordCacheSourceAllRecords)
			}
		case searchProgressZoneMatched:
			matches += event.Matches
		}
	}
	if !sawWorkersStage {
		t.Fatalf("missing worker start stage in events: %#v", events)
	}
	if starts != len(zones) || done != len(zones) {
		t.Fatalf("worker events starts=%d done=%d want %d; events=%#v", starts, done, len(zones), events)
	}
	if matches != len(zones) {
		t.Fatalf("matches = %d, want %d; events=%#v", matches, len(zones), events)
	}
}

func TestSearchWorkerPrimaryReadSplit(t *testing.T) {
	client := &WapiClient{Server: "https://primary.example", ReadServer: "https://read.example"}
	tests := []struct {
		workerCount int
		wantCount   int
		wantIDs     []int
	}{
		{workerCount: 10, wantCount: 0, wantIDs: nil},
		{workerCount: 11, wantCount: 2, wantIDs: []int{6, 11}},
		{workerCount: 16, wantCount: 3, wantIDs: []int{6, 11, 16}},
		{workerCount: 24, wantCount: 5, wantIDs: []int{5, 10, 15, 20, 24}},
	}
	for _, tt := range tests {
		if got := primaryReadWorkerCount(tt.workerCount, defaultDNSSearchPrimaryReadPercent); got != tt.wantCount {
			t.Fatalf("primaryReadWorkerCount(%d, %d) = %d, want %d", tt.workerCount, defaultDNSSearchPrimaryReadPercent, got, tt.wantCount)
		}
		var gotIDs []int
		for id := 1; id <= tt.workerCount; id++ {
			if searchWorkerUsesPrimaryReads(client, id, tt.workerCount, defaultDNSSearchPrimaryReadPercent) {
				gotIDs = append(gotIDs, id)
			}
		}
		if fmt.Sprint(gotIDs) != fmt.Sprint(tt.wantIDs) {
			t.Fatalf("primary worker IDs for %d workers = %v, want %v", tt.workerCount, gotIDs, tt.wantIDs)
		}
	}
}

func TestSearchWorkerPrimaryReadSplitUsesConfiguredPercent(t *testing.T) {
	client := &WapiClient{Server: "https://primary.example", ReadServer: "https://read.example"}
	for _, tt := range []struct {
		percent   int
		wantCount int
		wantIDs   []int
	}{
		{percent: 0, wantCount: 0, wantIDs: nil},
		{percent: 50, wantCount: 8, wantIDs: []int{2, 4, 6, 8, 10, 12, 14, 16}},
		{percent: 100, wantCount: 16, wantIDs: []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}},
	} {
		if got := primaryReadWorkerCount(16, tt.percent); got != tt.wantCount {
			t.Fatalf("primaryReadWorkerCount(16, %d) = %d, want %d", tt.percent, got, tt.wantCount)
		}
		var gotIDs []int
		for id := 1; id <= 16; id++ {
			if searchWorkerUsesPrimaryReads(client, id, 16, tt.percent) {
				gotIDs = append(gotIDs, id)
			}
		}
		if fmt.Sprint(gotIDs) != fmt.Sprint(tt.wantIDs) {
			t.Fatalf("primary worker IDs for percent %d = %v, want %v", tt.percent, gotIDs, tt.wantIDs)
		}
	}
}

func TestSearchWorkerPrimaryReadSplitRequiresRealReadServer(t *testing.T) {
	for _, client := range []*WapiClient{
		nil,
		{Server: "https://primary.example"},
		{Server: "https://primary.example", ReadServer: "https://primary.example"},
		{Server: "https://primary.example/", ReadServer: "https://primary.example"},
	} {
		if searchWorkerUsesPrimaryReads(client, 11, 16, defaultDNSSearchPrimaryReadPercent) {
			t.Fatalf("searchWorkerUsesPrimaryReads(%#v) = true, want false", client)
		}
	}
}

func TestSearchWorkerClientForPrimaryReadsSharesHTTPClient(t *testing.T) {
	httpClient := &http.Client{}
	client := &WapiClient{Server: "https://primary.example", ReadServer: "https://read.example", httpClient: httpClient}
	workerClient := searchWorkerClient(client, 6, 16, defaultDNSSearchPrimaryReadPercent)
	if workerClient == client {
		t.Fatal("primary-read worker reused base client, want clone")
	}
	if !workerClient.ForcePrimaryReads {
		t.Fatal("primary-read worker did not force primary reads")
	}
	if workerClient.httpClient != httpClient {
		t.Fatal("primary-read worker did not share HTTP client")
	}
	readWorkerClient := searchWorkerClient(client, 1, 16, defaultDNSSearchPrimaryReadPercent)
	if readWorkerClient != client {
		t.Fatal("read worker cloned client, want base client")
	}
}

func TestSearchZoneRecordBatchesSplitsWorkerGETsBetweenPrimaryAndReadServer(t *testing.T) {
	var primaryGETs, readGETs int
	var requestsMu sync.Mutex
	handler := func(counter *int) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Fatalf("method = %s, want GET", r.Method)
			}
			requestsMu.Lock()
			*counter = *counter + 1
			requestsMu.Unlock()
			switch {
			case strings.HasSuffix(r.URL.Path, "/zone_auth"):
				zone := r.URL.Query().Get("fqdn")
				_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{{
					"fqdn":              zone,
					"view":              "default",
					"zone_format":       "FORWARD",
					"primary_type":      "Grid",
					"soa_serial_number": "2026061001",
				}}})
			case strings.HasSuffix(r.URL.Path, "/allrecords"):
				zone := r.URL.Query().Get("zone")
				_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{{
					"type":    "HOST_IPV4ADDR",
					"name":    "test." + zone,
					"address": "192.0.2.10",
					"zone":    zone,
				}}})
			default:
				http.NotFound(w, r)
			}
		}
	}

	primary := httptest.NewServer(handler(&primaryGETs))
	defer primary.Close()
	read := httptest.NewServer(handler(&readGETs))
	defer read.Close()

	app := testApp(t)
	writeConfigForSettings(t, app, ConfigSettings{DNSSearchWorkerLimit: 11})
	profile := Profile{Name: defaultProfileName, DNSView: "default"}
	client := &WapiClient{
		Server:      primary.URL,
		ReadServer:  read.URL,
		WAPIVersion: defaultWAPIVersion,
		View:        "default",
		httpClient:  primary.Client(),
	}

	zones := make([]map[string]any, 0, 11)
	for i := 1; i <= 11; i++ {
		zones = append(zones, map[string]any{
			"fqdn":              fmt.Sprintf("zone-%02d.example.com", i),
			"view":              "default",
			"zone_format":       "FORWARD",
			"primary_type":      "Grid",
			"soa_serial_number": "2026061001",
		})
	}
	batches, err := app.searchZoneRecordBatches(profile, client, zones, false, nil)
	if err != nil {
		t.Fatalf("search zone batches: %v", err)
	}
	if len(batches) != len(zones) {
		t.Fatalf("batches = %d, want %d", len(batches), len(zones))
	}
	requestsMu.Lock()
	defer requestsMu.Unlock()
	if primaryGETs != 4 {
		t.Fatalf("primary GETs = %d, want 4", primaryGETs)
	}
	if readGETs != 18 {
		t.Fatalf("read GETs = %d, want 18", readGETs)
	}
}

func TestSearchAcrossZonesUsesFreshPrefetchedRecordCache(t *testing.T) {
	zones := []map[string]any{
		{"fqdn": "one.example.com", "view": "default", "zone_format": "FORWARD", "primary_type": "Grid"},
		{"fqdn": "two.example.com", "view": "default", "zone_format": "FORWARD", "primary_type": "Grid"},
	}
	var requestsMu sync.Mutex
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestsMu.Lock()
		requests++
		requestsMu.Unlock()
		http.Error(w, "cache should satisfy this search", http.StatusInternalServerError)
	}))
	defer server.Close()

	app := testApp(t)
	profile := Profile{Name: defaultProfileName, DNSView: "default"}
	now := time.Now()
	if err := app.writeCachedZones(profile, zones, now); err != nil {
		t.Fatalf("write cached zones: %v", err)
	}
	for _, zone := range zones {
		zoneName := cleanString(zone["fqdn"])
		if err := app.writeCachedRecords(profile, zoneName, "2026050801", []map[string]any{
			{"type": "HOST_IPV4ADDR", "name": "test." + zoneName, "address": "192.0.2.10", "zone": zoneName},
		}, now); err != nil {
			t.Fatalf("write cached records for %s: %v", zoneName, err)
		}
	}

	var eventsMu sync.Mutex
	var events []SearchProgressEvent
	records, err := app.collectSearchResults(profile, testWapiClient(server), SearchOptions{
		Keyword: "test",
		Global:  true,
		Progress: func(event SearchProgressEvent) {
			eventsMu.Lock()
			events = append(events, event)
			eventsMu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("collect search results: %v", err)
	}
	if len(records) != len(zones) {
		t.Fatalf("records = %d, want %d: %#v", len(records), len(zones), records)
	}
	requestsMu.Lock()
	defer requestsMu.Unlock()
	if requests != 0 {
		t.Fatalf("WAPI requests = %d, want 0", requests)
	}
	eventsMu.Lock()
	defer eventsMu.Unlock()
	var freshCacheHits int
	for _, event := range events {
		if event.Kind == searchProgressWorkerDone && event.Source == recordCacheSourceFreshCache {
			freshCacheHits++
		}
	}
	if freshCacheHits != len(zones) {
		t.Fatalf("fresh cache hits = %d, want %d; events=%#v", freshCacheHits, len(zones), events)
	}
}

func TestSearchAcrossZonesRenewsStalePrefetchedRecordCacheWithZoneSerial(t *testing.T) {
	zones := []map[string]any{
		{"fqdn": "one.example.com", "view": "default", "zone_format": "FORWARD", "primary_type": "Grid", "soa_serial_number": "2026050801"},
		{"fqdn": "two.example.com", "view": "default", "zone_format": "FORWARD", "primary_type": "Grid", "soa_serial_number": "2026050801"},
	}
	var requestsMu sync.Mutex
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestsMu.Lock()
		requests++
		requestsMu.Unlock()
		http.Error(w, "zone serial cache should satisfy this search", http.StatusInternalServerError)
	}))
	defer server.Close()

	app := testApp(t)
	profile := Profile{Name: defaultProfileName, DNSView: "default"}
	now := time.Now()
	if err := app.writeCachedZones(profile, zones, now); err != nil {
		t.Fatalf("write cached zones: %v", err)
	}
	for _, zone := range zones {
		zoneName := cleanString(zone["fqdn"])
		if err := app.writeCachedRecordsEntry(profile, zoneName, "2026050801", []map[string]any{
			{"type": "HOST_IPV4ADDR", "name": "test." + zoneName, "address": "192.0.2.10", "zone": zoneName},
		}, now.Add(-time.Hour).Unix(), now.Add(time.Hour).Unix()); err != nil {
			t.Fatalf("write stale cached records for %s: %v", zoneName, err)
		}
	}
	app.backgroundRecordRevalidator = func(profile Profile, zone string) error {
		t.Fatalf("unexpected background revalidation for %s", zone)
		return nil
	}

	var eventsMu sync.Mutex
	var events []SearchProgressEvent
	records, err := app.collectSearchResults(profile, testWapiClient(server), SearchOptions{
		Keyword: "test",
		Global:  true,
		Progress: func(event SearchProgressEvent) {
			eventsMu.Lock()
			events = append(events, event)
			eventsMu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("collect search results: %v", err)
	}
	if len(records) != len(zones) {
		t.Fatalf("records = %d, want %d: %#v", len(records), len(zones), records)
	}
	requestsMu.Lock()
	defer requestsMu.Unlock()
	if requests != 0 {
		t.Fatalf("WAPI requests = %d, want 0", requests)
	}
	eventsMu.Lock()
	defer eventsMu.Unlock()
	var serialCacheHits int
	for _, event := range events {
		if event.Kind == searchProgressWorkerDone && event.Source == recordCacheSourceSerialCache {
			serialCacheHits++
		}
	}
	if serialCacheHits != len(zones) {
		t.Fatalf("serial cache hits = %d, want %d; events=%#v", serialCacheHits, len(zones), events)
	}
	for _, zone := range zones {
		entry, err := app.readCachedRecords(profile, cleanString(zone["fqdn"]))
		if err != nil {
			t.Fatalf("read renewed cache: %v", err)
		}
		if !app.cacheEntryFresh(entry, time.Now()) {
			t.Fatalf("cache for %s was not renewed", cleanString(zone["fqdn"]))
		}
	}
}

func TestSearchAcrossZonesBatchesDeferredStaleRevalidation(t *testing.T) {
	zones := []map[string]any{
		{"fqdn": "one.example.com", "view": "default", "zone_format": "FORWARD", "primary_type": "Grid"},
		{"fqdn": "two.example.com", "view": "default", "zone_format": "FORWARD", "primary_type": "Grid"},
	}
	var requestsMu sync.Mutex
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestsMu.Lock()
		requests++
		requestsMu.Unlock()
		http.Error(w, "stale cache should satisfy this search", http.StatusInternalServerError)
	}))
	defer server.Close()

	app := testApp(t)
	profile := Profile{Name: defaultProfileName, DNSView: "default"}
	now := time.Now()
	if err := app.writeCachedZones(profile, zones, now); err != nil {
		t.Fatalf("write cached zones: %v", err)
	}
	for _, zone := range zones {
		zoneName := cleanString(zone["fqdn"])
		if err := app.writeCachedRecordsEntry(profile, zoneName, "2026050801", []map[string]any{
			{"type": "HOST_IPV4ADDR", "name": "test." + zoneName, "address": "192.0.2.10", "zone": zoneName},
		}, now.Add(-time.Hour).Unix(), now.Add(time.Hour).Unix()); err != nil {
			t.Fatalf("write stale cached records for %s: %v", zoneName, err)
		}
	}
	app.backgroundRecordRevalidator = func(profile Profile, zone string) error {
		t.Fatalf("unexpected per-zone background revalidation for %s", zone)
		return nil
	}
	batches := make(chan []string, 1)
	app.backgroundRecordBatchRevalidator = func(profile Profile, zones []string) error {
		batches <- append([]string(nil), zones...)
		return nil
	}

	records, err := app.collectSearchResults(profile, testWapiClient(server), SearchOptions{Keyword: "test", Global: true})
	if err != nil {
		t.Fatalf("collect search results: %v", err)
	}
	if len(records) != len(zones) {
		t.Fatalf("records = %d, want %d: %#v", len(records), len(zones), records)
	}
	requestsMu.Lock()
	defer requestsMu.Unlock()
	if requests != 0 {
		t.Fatalf("WAPI requests = %d, want 0", requests)
	}
	var batch []string
	select {
	case batch = <-batches:
	default:
		t.Fatalf("batch revalidation was not queued")
	}
	sort.Strings(batch)
	if strings.Join(batch, ",") != "one.example.com,two.example.com" {
		t.Fatalf("batch zones = %#v", batch)
	}
}

func TestSearchAcrossZonesSkipsSecondaryDataUnavailableError(t *testing.T) {
	zones := []map[string]any{
		{"fqdn": "primary.example.com", "view": "default", "zone_format": "FORWARD", "primary_type": "Grid"},
		{"fqdn": "blocked.example.com", "view": "default", "zone_format": "FORWARD", "primary_type": "Grid"},
	}
	var mu sync.Mutex
	requestedZones := map[string]int{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/zone_auth"):
			if fqdn := r.URL.Query().Get("fqdn"); fqdn != "" {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"result": []map[string]any{
						{"fqdn": fqdn, "view": "default", "zone_format": "FORWARD", "primary_type": "Grid", "soa_serial_number": "2026050801"},
					},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"result": zones})
		case strings.HasSuffix(r.URL.Path, "/allrecords"):
			zone := r.URL.Query().Get("zone")
			mu.Lock()
			requestedZones[zone]++
			mu.Unlock()
			if zone == "blocked.example.com" {
				http.Error(w, `{"text":"Secondary zone data unavailable."}`, http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{
					{"type": "HOST_IPV4ADDR", "name": "test.primary.example.com", "address": "192.0.2.10", "zone": zone},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	app := testApp(t)
	records, err := app.collectSearchResults(Profile{DNSView: "default"}, testWapiClient(server), SearchOptions{Keyword: "test", Global: true})
	if err != nil {
		t.Fatalf("collect search results: %v", err)
	}
	if len(records) != 1 || recordName(records[0].Item, records[0].Type) != "test.primary.example.com" {
		t.Fatalf("records = %#v", records)
	}
	mu.Lock()
	defer mu.Unlock()
	if requestedZones["primary.example.com"] != 1 || requestedZones["blocked.example.com"] != 1 {
		t.Fatalf("requested zones = %#v", requestedZones)
	}
}

func TestSearchAcrossZonesReturnsFatalZoneError(t *testing.T) {
	zones := []map[string]any{
		{"fqdn": "good.example.com", "view": "default", "zone_format": "FORWARD", "primary_type": "Grid"},
		{"fqdn": "bad.example.com", "view": "default", "zone_format": "FORWARD", "primary_type": "Grid"},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/zone_auth"):
			if fqdn := r.URL.Query().Get("fqdn"); fqdn != "" {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"result": []map[string]any{
						{"fqdn": fqdn, "view": "default", "zone_format": "FORWARD", "primary_type": "Grid", "soa_serial_number": "2026050801"},
					},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"result": zones})
		case strings.HasSuffix(r.URL.Path, "/allrecords"):
			if r.URL.Query().Get("zone") == "bad.example.com" {
				http.Error(w, `{"text":"fatal search failure"}`, http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{
					{"type": "HOST_IPV4ADDR", "name": "test.good.example.com", "address": "192.0.2.10", "zone": "good.example.com"},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	app := testApp(t)
	_, err := app.collectSearchResults(Profile{DNSView: "default"}, testWapiClient(server), SearchOptions{Keyword: "test", Global: true})
	if err == nil {
		t.Fatal("collect search results succeeded, want fatal error")
	}
	if !strings.Contains(err.Error(), "fatal search failure") {
		t.Fatalf("error = %v", err)
	}
}

func TestReversePointerIPv4(t *testing.T) {
	address := mustAddr(t, "192.0.2.10")
	if got := reversePointer(address); got != "10.2.0.192.in-addr.arpa" {
		t.Fatalf("reverse pointer = %q", got)
	}
}

func mustAddr(t *testing.T, value string) netip.Addr {
	t.Helper()
	addr, err := netip.ParseAddr(value)
	if err != nil {
		t.Fatal(err)
	}
	return addr
}

func testRecords(count int) []TypedRecord {
	records := make([]TypedRecord, 0, count)
	for i := 1; i <= count; i++ {
		records = append(records, TypedRecord{
			Type: "host",
			Item: map[string]any{
				"name":    fmt.Sprintf("app%d.example.com", i),
				"address": fmt.Sprintf("192.0.2.%d", i),
				"zone":    "example.com",
			},
		})
	}
	return records
}

func assertSortedRecordNames(t *testing.T, records []TypedRecord, want []string) {
	t.Helper()
	got := make([]string, 0, len(records))
	for _, record := range records {
		got = append(got, recordName(record.Item, record.Type))
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("record names = %#v, want %#v", got, want)
	}
}

func testZones(count int) []map[string]any {
	zones := make([]map[string]any, 0, count)
	for i := 1; i <= count; i++ {
		zones = append(zones, map[string]any{
			"fqdn":        fmt.Sprintf("zone%d.example.com", i),
			"view":        "default",
			"zone_format": "FORWARD",
			"ns_group":    "default",
			"comment":     "",
		})
	}
	return zones
}

func searchScopeServer(t *testing.T, zones []map[string]any, requestedZones map[string]int, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	return searchScopeServerWithRecords(t, zones, requestedZones, mu, func(zone string) []map[string]any {
		return []map[string]any{
			{
				"type":    "HOST_IPV4ADDR",
				"name":    "test." + zone,
				"address": "192.0.2.10",
				"zone":    zone,
			},
		}
	})
}

func searchScopeServerWithRecords(t *testing.T, zones []map[string]any, requestedZones map[string]int, mu *sync.Mutex, recordsForZone func(string) []map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/zone_auth"):
			if fqdn := r.URL.Query().Get("fqdn"); fqdn != "" {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"result": []map[string]any{
						{"fqdn": fqdn, "view": "default", "zone_format": "FORWARD", "primary_type": "Grid", "soa_serial_number": "2026050801"},
					},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"result": zones})
		case strings.HasSuffix(r.URL.Path, "/allrecords"):
			zone := r.URL.Query().Get("zone")
			mu.Lock()
			requestedZones[zone]++
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": recordsForZone(zone),
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

func zoneInfoTestApp(t *testing.T, output string) (*App, *bytes.Buffer) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/zone_auth") {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": []map[string]any{
				{
					"_ref":              "zone_auth/ref:example.com/default",
					"fqdn":              "example.com",
					"view":              "default",
					"zone_format":       "FORWARD",
					"ns_group":          "default",
					"network_view":      "default",
					"soa_serial_number": 2.026050701e+09,
					"member_soa_mnames": []string{"ns1.example.com"},
					"soa_email":         "dns@example.com",
					"soa_refresh":       10800,
					"soa_retry":         3600,
					"soa_expire":        2.4192e+06,
					"soa_negative_ttl":  900,
					"comment":           "primary test zone",
				},
			},
		})
	}))
	t.Cleanup(server.Close)

	app := testApp(t)
	profiles := map[string]Profile{
		defaultProfileName: {
			Name:        defaultProfileName,
			Server:      server.URL,
			Username:    "admin",
			Password:    "secret",
			WAPIVersion: defaultWAPIVersion,
			DNSView:     "default",
			VerifySSL:   true,
			Timeout:     defaultTimeoutSeconds,
		},
	}
	if err := app.writeConfigProfiles(defaultProfileName, profiles); err != nil {
		t.Fatalf("write profile: %v", err)
	}
	var stdout bytes.Buffer
	app.Output = output
	app.Stdout = &stdout
	app.Stderr = &bytes.Buffer{}
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)
	return app, &stdout
}

func dnsCommandTestApp(t *testing.T, serverURL, defaultZone string) *App {
	t.Helper()
	app := testApp(t)
	profiles := map[string]Profile{
		defaultProfileName: {
			Name:        defaultProfileName,
			Server:      serverURL,
			Username:    "admin",
			Password:    "secret",
			WAPIVersion: defaultWAPIVersion,
			DNSView:     "default",
			DefaultZone: defaultZone,
			VerifySSL:   true,
			Timeout:     defaultTimeoutSeconds,
		},
	}
	if err := app.writeConfigProfiles(defaultProfileName, profiles); err != nil {
		t.Fatalf("write profile: %v", err)
	}
	app.Stdout = &bytes.Buffer{}
	app.Stderr = &bytes.Buffer{}
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)
	return app
}

func assertQueuedRecordRefreshes(t *testing.T, refreshes <-chan string, wantZones ...string) {
	t.Helper()
	for _, wantZone := range wantZones {
		select {
		case zone := <-refreshes:
			if zone != wantZone {
				t.Fatalf("record refresh zone = %q, want %q", zone, wantZone)
			}
		default:
			t.Fatalf("record refresh for %s was not queued", wantZone)
		}
	}
}
