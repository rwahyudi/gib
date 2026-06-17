package ibcli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDNSCreateWorkflowPostsToPrimaryWithoutMandatoryTTL(t *testing.T) {
	var postPayload map[string]any
	var primaryRequests []string
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryRequests = append(primaryRequests, r.Method+" "+trimWAPIPath(r.URL.Path))
		if r.Method != http.MethodPost || trimWAPIPath(r.URL.Path) != "record:a" {
			t.Fatalf("primary request = %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&postPayload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		_ = json.NewEncoder(w).Encode("record:a/ref")
	}))
	defer primary.Close()

	read := emptyReadServer(t)
	defer read.Close()

	app, _ := dnsWorkflowApp(t, primary.URL, read.URL)
	profile := mustLoadProfile(t, app)
	writeWorkflowRecordCache(t, app, profile)
	refreshes := captureRecordRefreshes(app)

	if err := app.Execute([]string{"dns", "create", "a", "app", "192.0.2.10", "--noptr"}); err != nil {
		t.Fatalf("create: %v", err)
	}

	for key, want := range map[string]any{
		"name":     "app.example.com",
		"ipv4addr": "192.0.2.10",
		"view":     "default",
	} {
		if postPayload[key] != want {
			t.Fatalf("payload[%s] = %#v, want %#v; payload = %#v", key, postPayload[key], want, postPayload)
		}
	}
	for _, key := range []string{"ttl", "use_ttl"} {
		if _, ok := postPayload[key]; ok {
			t.Fatalf("payload unexpectedly included %s: %#v", key, postPayload)
		}
	}
	if strings.Join(primaryRequests, ",") != "POST record:a" {
		t.Fatalf("primary requests = %#v", primaryRequests)
	}
	assertRecordCacheInvalidated(t, app, profile, "example.com")
	assertRecordRefreshQueued(t, refreshes, "example.com")
}

func TestDNSCreateCNAMEQualifiesShortTarget(t *testing.T) {
	var postPayload map[string]any
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || trimWAPIPath(r.URL.Path) != "record:cname" {
			t.Fatalf("primary request = %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&postPayload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		_ = json.NewEncoder(w).Encode("record:cname/ref")
	}))
	defer primary.Close()

	read := emptyReadServer(t)
	defer read.Close()

	stubLookupHost(t, "computer1.example.com")
	app, _ := dnsWorkflowApp(t, primary.URL, read.URL)
	profile := mustLoadProfile(t, app)
	refreshes := captureRecordRefreshes(app)

	if err := app.Execute([]string{"dns", "create", "cname", "hostalias1", "computer1"}); err != nil {
		t.Fatalf("create cname: %v", err)
	}

	for key, want := range map[string]any{
		"name":      "hostalias1.example.com",
		"canonical": "computer1.example.com",
		"view":      "default",
	} {
		if postPayload[key] != want {
			t.Fatalf("payload[%s] = %#v, want %#v; payload = %#v", key, postPayload[key], want, postPayload)
		}
	}
	assertRecordCacheInvalidated(t, app, profile, "example.com")
	assertRecordRefreshQueued(t, refreshes, "example.com")
}

func TestDNSCreateUsesDNSContextOverrides(t *testing.T) {
	var postPayload map[string]any
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || trimWAPIPath(r.URL.Path) != "record:a" {
			t.Fatalf("primary request = %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&postPayload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		_ = json.NewEncoder(w).Encode("record:a/ref")
	}))
	defer primary.Close()

	read := emptyReadServer(t)
	defer read.Close()

	app, _ := dnsWorkflowApp(t, primary.URL, read.URL)
	if err := app.Execute([]string{"dns", "--zone", "override.example.com", "--view", "DNS Alt View", "create", "a", "app", "192.0.2.10", "--noptr"}); err != nil {
		t.Fatalf("create with context overrides: %v", err)
	}

	for key, want := range map[string]any{
		"name": "app.override.example.com",
		"view": "DNS Alt View",
	} {
		if postPayload[key] != want {
			t.Fatalf("payload[%s] = %#v, want %#v; payload = %#v", key, postPayload[key], want, postPayload)
		}
	}
}

func TestDNSCreateWorkflowRejectsOldNameTypeValueOrder(t *testing.T) {
	var primaryRequests []string
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryRequests = append(primaryRequests, r.Method+" "+trimWAPIPath(r.URL.Path))
		http.NotFound(w, r)
	}))
	defer primary.Close()

	read := emptyReadServer(t)
	defer read.Close()

	app, _ := dnsWorkflowApp(t, primary.URL, read.URL)
	err := app.Execute([]string{"dns", "create", "app", "a", "192.0.2.10", "--noptr"})
	if err == nil {
		t.Fatal("old NAME TYPE VALUE order succeeded, want unsupported record type error")
	}
	if !strings.Contains(err.Error(), `unsupported record type "app"`) {
		t.Fatalf("error = %v", err)
	}
	if len(primaryRequests) != 0 {
		t.Fatalf("old order made primary requests: %#v", primaryRequests)
	}
}

func TestDNSEditWorkflowRejectsOldNameTypeValueOrder(t *testing.T) {
	var readRequests []string
	read := recordLookupServer(t, &readRequests)
	defer read.Close()

	primary := emptyReadServer(t)
	defer primary.Close()

	app, _ := dnsWorkflowApp(t, primary.URL, read.URL)
	err := app.Execute([]string{"dns", "edit", "app", "a", "192.0.2.20"})
	if err == nil {
		t.Fatal("old NAME TYPE VALUE edit order succeeded, want unsupported record type error")
	}
	if !strings.Contains(err.Error(), `unsupported record type "app"`) {
		t.Fatalf("error = %v", err)
	}
	if len(readRequests) != 0 {
		t.Fatalf("old edit order made read requests: %#v", readRequests)
	}
}

func TestDNSEditWorkflowReadsFromReadServerAndWritesPrimary(t *testing.T) {
	var putPayload map[string]any
	var primaryRequests []string
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryRequests = append(primaryRequests, r.Method+" "+trimWAPIPath(r.URL.Path))
		if r.Method != http.MethodPut || trimWAPIPath(r.URL.Path) != "record:a/ref" {
			t.Fatalf("primary request = %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&putPayload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"_ref": "record:a/ref"})
	}))
	defer primary.Close()

	var readRequests []string
	read := recordLookupServer(t, &readRequests)
	defer read.Close()

	app, _ := dnsWorkflowApp(t, primary.URL, read.URL)
	profile := mustLoadProfile(t, app)
	writeWorkflowRecordCache(t, app, profile)
	refreshes := captureRecordRefreshes(app)

	if err := app.Execute([]string{"dns", "edit", "a", "app", "192.0.2.20", "--ttl", "600", "--comment", "Updated", "--noptr"}); err != nil {
		t.Fatalf("edit: %v", err)
	}

	for key, want := range map[string]any{
		"ipv4addr": "192.0.2.20",
		"ttl":      float64(600),
		"use_ttl":  true,
		"comment":  "Updated",
	} {
		if putPayload[key] != want {
			t.Fatalf("payload[%s] = %#v, want %#v; payload = %#v", key, putPayload[key], want, putPayload)
		}
	}
	if strings.Join(primaryRequests, ",") != "PUT record:a/ref" {
		t.Fatalf("primary requests = %#v", primaryRequests)
	}
	if len(readRequests) == 0 {
		t.Fatalf("expected record lookup requests on read server")
	}
	assertRecordCacheInvalidated(t, app, profile, "example.com")
	assertRecordRefreshQueued(t, refreshes, "example.com")
}

func TestDNSEditAManagedPTRDeletesOldPTRAndRefreshesReverseCaches(t *testing.T) {
	var primaryRequests []string
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryRequests = append(primaryRequests, r.Method+" "+trimWAPIPath(r.URL.Path))
		switch {
		case r.Method == http.MethodPut && trimWAPIPath(r.URL.Path) == "record:a/ref":
			_ = json.NewEncoder(w).Encode(map[string]any{"_ref": "record:a/ref"})
		case r.Method == http.MethodGet && trimWAPIPath(r.URL.Path) == zoneObject:
			switch r.URL.Query().Get("fqdn") {
			case "100.51.198.in-addr.arpa":
				_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{{
					"fqdn":        "100.51.198.in-addr.arpa",
					"view":        "default",
					"zone_format": "IPV4",
				}}})
			case "2.0.192.in-addr.arpa":
				_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{{
					"fqdn":        "2.0.192.in-addr.arpa",
					"view":        "default",
					"zone_format": "IPV4",
				}}})
			default:
				_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{}})
			}
		case r.Method == http.MethodGet && trimWAPIPath(r.URL.Path) == "record:ptr":
			switch r.URL.Query().Get("ipv4addr") {
			case "198.51.100.20":
				_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{}})
			case "192.0.2.10":
				_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{{
					"_ref":     "record:ptr/old-ref",
					"ipv4addr": "192.0.2.10",
					"ptrdname": "app.example.com",
					"zone":     "2.0.192.in-addr.arpa",
				}}})
			default:
				if r.URL.Query().Get("name") == "20.100.51.198.in-addr.arpa" {
					_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{}})
					return
				}
				t.Fatalf("unexpected PTR lookup query: %s", r.URL.RawQuery)
			}
		case r.Method == http.MethodPost && trimWAPIPath(r.URL.Path) == "record:ptr":
			_ = json.NewEncoder(w).Encode("record:ptr/new-ref")
		case r.Method == http.MethodDelete && trimWAPIPath(r.URL.Path) == "record:ptr/old-ref":
			_ = json.NewEncoder(w).Encode("record:ptr/old-ref")
		default:
			t.Fatalf("primary request = %s %s", r.Method, r.URL.Path)
		}
	}))
	defer primary.Close()

	var readRequests []string
	read := recordLookupServer(t, &readRequests)
	defer read.Close()

	app, _ := dnsWorkflowApp(t, primary.URL, read.URL)
	profile := mustLoadProfile(t, app)
	writeWorkflowRecordCache(t, app, profile)
	if err := app.writeCachedRecords(profile, "2.0.192.in-addr.arpa", "2026050801", []map[string]any{
		{"type": "record:ptr", "name": "10", "ipv4addr": "192.0.2.10", "ptrdname": "app.example.com", "zone": "2.0.192.in-addr.arpa"},
	}, time.Now()); err != nil {
		t.Fatalf("write old reverse record cache: %v", err)
	}
	if err := app.writeCachedRecords(profile, "100.51.198.in-addr.arpa", "2026050801", []map[string]any{
		{"type": "record:ptr", "name": "20", "ipv4addr": "198.51.100.20", "ptrdname": "old.example.com", "zone": "100.51.198.in-addr.arpa"},
	}, time.Now()); err != nil {
		t.Fatalf("write new reverse record cache: %v", err)
	}
	refreshes := captureRecordRefreshes(app)

	if err := app.Execute([]string{"dns", "edit", "a", "app", "198.51.100.20"}); err != nil {
		t.Fatalf("edit: %v", err)
	}

	for _, want := range []string{"PUT record:a/ref", "POST record:ptr", "DELETE record:ptr/old-ref"} {
		if !containsString(primaryRequests, want) {
			t.Fatalf("primary requests missing %q: %#v", want, primaryRequests)
		}
	}
	if len(readRequests) == 0 {
		t.Fatalf("expected record lookup requests on read server")
	}
	assertRecordCacheInvalidated(t, app, profile, "example.com")
	assertRecordCacheInvalidated(t, app, profile, "2.0.192.in-addr.arpa")
	assertRecordCacheInvalidated(t, app, profile, "100.51.198.in-addr.arpa")
	assertRecordRefreshQueued(t, refreshes, "example.com")
	assertRecordRefreshQueued(t, refreshes, "100.51.198.in-addr.arpa")
	assertRecordRefreshQueued(t, refreshes, "2.0.192.in-addr.arpa")
}

func TestDNSEditCNAMEQualifiesShortTarget(t *testing.T) {
	var putPayload map[string]any
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || trimWAPIPath(r.URL.Path) != "record:cname/ref" {
			t.Fatalf("primary request = %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&putPayload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"_ref": "record:cname/ref"})
	}))
	defer primary.Close()

	read := cnameRecordLookupServer(t)
	defer read.Close()

	stubLookupHost(t, "computer1.example.com")
	app, _ := dnsWorkflowApp(t, primary.URL, read.URL)
	profile := mustLoadProfile(t, app)
	refreshes := captureRecordRefreshes(app)

	if err := app.Execute([]string{"dns", "edit", "cname", "hostalias1", "computer1"}); err != nil {
		t.Fatalf("edit cname: %v", err)
	}

	if putPayload["canonical"] != "computer1.example.com" {
		t.Fatalf("canonical = %#v, want computer1.example.com; payload = %#v", putPayload["canonical"], putPayload)
	}
	assertRecordCacheInvalidated(t, app, profile, "example.com")
	assertRecordRefreshQueued(t, refreshes, "example.com")
}

func TestDNSDeleteWorkflowReadsFromReadServerAndWritesPrimary(t *testing.T) {
	var primaryRequests []string
	reverseZone := "2.0.192.in-addr.arpa"
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryRequests = append(primaryRequests, r.Method+" "+trimWAPIPath(r.URL.Path))
		switch {
		case r.Method == http.MethodDelete && trimWAPIPath(r.URL.Path) == "record:a/ref":
			_ = json.NewEncoder(w).Encode(map[string]any{"_ref": "record:a/ref"})
		case r.Method == http.MethodGet && trimWAPIPath(r.URL.Path) == zoneObject:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{
					{"fqdn": reverseZone, "view": "default", "zone_format": "IPV4"},
				},
			})
		default:
			t.Fatalf("primary request = %s %s", r.Method, r.URL.Path)
		}
	}))
	defer primary.Close()

	var readRequests []string
	read := recordLookupServer(t, &readRequests)
	defer read.Close()

	app, _ := dnsWorkflowApp(t, primary.URL, read.URL)
	profile := mustLoadProfile(t, app)
	writeWorkflowRecordCache(t, app, profile)
	if err := app.writeCachedZones(profile, []map[string]any{{"fqdn": reverseZone, "zone_format": "IPV4", "view": "default"}}, time.Now()); err != nil {
		t.Fatalf("write zone cache: %v", err)
	}
	if err := app.writeCachedRecords(profile, reverseZone, "2026050801", []map[string]any{
		{"type": "record:ptr", "name": "10", "address": "192.0.2.10", "ptrdname": "app.example.com", "zone": reverseZone},
	}, time.Now()); err != nil {
		t.Fatalf("write reverse record cache: %v", err)
	}
	refreshes := captureRecordRefreshes(app)
	app.dnsDeleteConfirmer = func(target string, record TypedRecord) (bool, error) {
		t.Fatalf("confirmation prompt should be skipped when -y is provided")
		return false, nil
	}

	if err := app.Execute([]string{"dns", "delete", "a", "app", "-y"}); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if strings.Join(primaryRequests, ",") != "DELETE record:a/ref" {
		t.Fatalf("primary requests = %#v", primaryRequests)
	}
	if len(readRequests) == 0 {
		t.Fatalf("expected record lookup requests on read server")
	}
	assertRecordCacheInvalidated(t, app, profile, "example.com")
	assertRecordCacheInvalidated(t, app, profile, reverseZone)
	assertRecordRefreshQueued(t, refreshes, "example.com")
	assertRecordRefreshQueued(t, refreshes, reverseZone)
}

func TestDNSDeleteRejectsMissingType(t *testing.T) {
	app := testApp(t)
	var stderr bytes.Buffer
	app.Stderr = &stderr
	err := app.Execute([]string{"dns", "delete", "app", "-y"})
	if err == nil {
		t.Fatal("delete without type succeeded")
	}
	if !strings.Contains(stderr.String(), "ib dns delete TYPE NAME [ZONE]") {
		t.Fatalf("delete without type usage missing type-first usage:\n%s\nerr=%v", stderr.String(), err)
	}
}

func TestZoneCreateQueuesZoneAndRecordCacheRefresh(t *testing.T) {
	var primaryRequests []string
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryRequests = append(primaryRequests, r.Method+" "+trimWAPIPath(r.URL.Path))
		if r.Method != http.MethodPost || trimWAPIPath(r.URL.Path) != zoneObject {
			t.Fatalf("primary request = %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode("zone_auth/ref")
	}))
	defer primary.Close()

	read := emptyReadServer(t)
	defer read.Close()

	app, _ := dnsWorkflowApp(t, primary.URL, read.URL)
	profile := mustLoadProfile(t, app)
	if err := app.writeCachedZones(profile, []map[string]any{{"fqdn": "old.example.com"}}, time.Now()); err != nil {
		t.Fatalf("write zone cache: %v", err)
	}
	recordRefreshes := captureRecordRefreshes(app)
	zoneRefreshes := captureZoneRefreshes(app)

	if err := app.runZoneCreate("new.example.com", "FORWARD", "", ""); err != nil {
		t.Fatalf("zone create: %v", err)
	}

	if strings.Join(primaryRequests, ",") != "POST zone_auth" {
		t.Fatalf("primary requests = %#v", primaryRequests)
	}
	assertZoneCacheInvalidated(t, app, profile)
	assertZoneRefreshQueued(t, zoneRefreshes, "default")
	assertRecordRefreshQueued(t, recordRefreshes, "new.example.com")
}

func TestZoneDeleteQueuesZoneRefreshAndClearsRecordCache(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+trimWAPIPath(r.URL.Path))
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/zone_auth"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{{
					"_ref":        "zone_auth/ref",
					"fqdn":        "old.example.com",
					"view":        "default",
					"zone_format": "FORWARD",
				}},
			})
		case r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/zone_auth/ref"):
			_ = json.NewEncoder(w).Encode(map[string]any{"_ref": "zone_auth/ref"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	app, _ := dnsWorkflowApp(t, server.URL, server.URL)
	profile := mustLoadProfile(t, app)
	if err := app.writeCachedZones(profile, []map[string]any{{"fqdn": "old.example.com"}}, time.Now()); err != nil {
		t.Fatalf("write zone cache: %v", err)
	}
	if err := app.writeCachedRecords(profile, "old.example.com", "2026050801", []map[string]any{{"name": "old.example.com"}}, time.Now()); err != nil {
		t.Fatalf("write record cache: %v", err)
	}
	recordRefreshes := captureRecordRefreshes(app)
	zoneRefreshes := captureZoneRefreshes(app)

	if err := app.runZoneDelete("old.example.com"); err != nil {
		t.Fatalf("zone delete: %v", err)
	}

	if strings.Join(requests, ",") != "GET zone_auth,DELETE zone_auth/ref" {
		t.Fatalf("requests = %#v", requests)
	}
	assertZoneCacheInvalidated(t, app, profile)
	assertRecordCacheInvalidated(t, app, profile, "old.example.com")
	assertZoneRefreshQueued(t, zoneRefreshes, "default")
	assertNoRecordRefreshQueued(t, recordRefreshes)
}

func TestDNSDeleteRequiresConfirmationBeforeDelete(t *testing.T) {
	var primaryRequests []string
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryRequests = append(primaryRequests, r.Method+" "+trimWAPIPath(r.URL.Path))
		http.NotFound(w, r)
	}))
	defer primary.Close()

	var readRequests []string
	read := recordLookupServer(t, &readRequests)
	defer read.Close()

	app, _ := dnsWorkflowApp(t, primary.URL, read.URL)
	err := app.Execute([]string{"dns", "delete", "a", "app"})
	if err == nil {
		t.Fatal("delete succeeded without confirmation in non-interactive mode")
	}
	for _, want := range []string{"delete confirmation requires an interactive terminal", "-y"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error missing %q: %v", want, err)
		}
	}
	if len(primaryRequests) != 0 {
		t.Fatalf("unconfirmed delete made primary requests: %#v", primaryRequests)
	}
	if len(readRequests) == 0 {
		t.Fatalf("expected record lookup requests on read server")
	}
}

func TestDNSDeleteCanceledBeforePrimaryDelete(t *testing.T) {
	var primaryRequests []string
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryRequests = append(primaryRequests, r.Method+" "+trimWAPIPath(r.URL.Path))
		http.NotFound(w, r)
	}))
	defer primary.Close()

	var readRequests []string
	read := recordLookupServer(t, &readRequests)
	defer read.Close()

	app, stdout := dnsWorkflowApp(t, primary.URL, read.URL)
	app.Output = tableOutput
	refreshes := captureRecordRefreshes(app)
	app.dnsDeleteConfirmer = func(target string, record TypedRecord) (bool, error) {
		if target != "app.example.com" {
			t.Fatalf("target = %q, want app.example.com", target)
		}
		if ref := cleanString(record.Item["_ref"]); ref != "record:a/ref" {
			t.Fatalf("confirmation record ref = %q, want record:a/ref", ref)
		}
		return false, nil
	}

	if err := app.Execute([]string{"dns", "delete", "a", "app"}); err != nil {
		t.Fatalf("cancelled delete returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "INFO: delete cancelled") {
		t.Fatalf("cancelled delete output missing info line:\n%s", stdout.String())
	}
	if len(primaryRequests) != 0 {
		t.Fatalf("canceled delete made primary requests: %#v", primaryRequests)
	}
	if len(readRequests) == 0 {
		t.Fatalf("expected record lookup requests on read server")
	}
	assertNoRecordRefreshQueued(t, refreshes)
}

func TestDNSDeleteDuplicateSelectionCanceledPrintsInfo(t *testing.T) {
	var primaryRequests []string
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryRequests = append(primaryRequests, r.Method+" "+trimWAPIPath(r.URL.Path))
		http.NotFound(w, r)
	}))
	defer primary.Close()

	var readRequests []string
	read := duplicateRecordLookupServer(t, &readRequests)
	defer read.Close()

	app, stdout := dnsWorkflowApp(t, primary.URL, read.URL)
	app.Output = tableOutput
	app.dnsDeleteRecordSelector = func(target string, matches []TypedRecord) (TypedRecord, bool, error) {
		if target != "app.example.com" {
			t.Fatalf("target = %q, want app.example.com", target)
		}
		if len(matches) != 2 {
			t.Fatalf("matches = %#v, want two duplicate candidates", matches)
		}
		return TypedRecord{}, false, nil
	}

	if err := app.Execute([]string{"dns", "delete", "cname", "app"}); err != nil {
		t.Fatalf("cancelled duplicate delete returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "INFO: delete cancelled") {
		t.Fatalf("cancelled duplicate delete output missing info line:\n%s", stdout.String())
	}
	if len(primaryRequests) != 0 {
		t.Fatalf("canceled duplicate delete made primary requests: %#v", primaryRequests)
	}
	if len(readRequests) == 0 {
		t.Fatalf("expected duplicate lookup requests on read server")
	}
}

func TestDNSDeleteCNAMELookupOmitsUnsupportedTTLReturnFields(t *testing.T) {
	var primaryRequests []string
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryRequests = append(primaryRequests, r.Method+" "+trimWAPIPath(r.URL.Path))
		if r.Method != http.MethodDelete || trimWAPIPath(r.URL.Path) != "record:cname/ref" {
			t.Fatalf("primary request = %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"_ref": "record:cname/ref"})
	}))
	defer primary.Close()

	var readRequests []string
	read := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("read request = %s %s", r.Method, r.URL.Path)
		}
		object := trimWAPIPath(r.URL.Path)
		readRequests = append(readRequests, object+" fields="+r.URL.Query().Get("_return_fields"))
		if strings.Contains(r.URL.Query().Get("_return_fields"), "ttl") {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"text": "Unknown argument/field: 'ttl'"})
			return
		}
		if object == "record:cname" && r.URL.Query().Get("name") == "cnametest2.example.com" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{{
					"_ref":      "record:cname/ref",
					"name":      "cnametest2.example.com",
					"canonical": "target.example.com",
					"view":      "default",
					"zone":      "example.com",
				}},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{}})
	}))
	defer read.Close()

	app, _ := dnsWorkflowApp(t, primary.URL, read.URL)
	profile := mustLoadProfile(t, app)
	writeWorkflowRecordCache(t, app, profile)
	refreshes := captureRecordRefreshes(app)
	app.dnsDeleteConfirmer = func(target string, record TypedRecord) (bool, error) {
		if target != "cnametest2.example.com" {
			t.Fatalf("confirmation target = %q, want cnametest2.example.com", target)
		}
		return true, nil
	}

	if err := app.Execute([]string{"dns", "delete", "cname", "cnametest2"}); err != nil {
		t.Fatalf("delete cname: %v", err)
	}
	if strings.Join(primaryRequests, ",") != "DELETE record:cname/ref" {
		t.Fatalf("primary requests = %#v", primaryRequests)
	}
	joined := strings.Join(readRequests, ",")
	if !strings.Contains(joined, "record:cname fields=name,canonical,view,zone,comment") {
		t.Fatalf("CNAME lookup did not use minimal return fields: %#v", readRequests)
	}
	assertRecordCacheInvalidated(t, app, profile, "example.com")
	assertRecordRefreshQueued(t, refreshes, "example.com")
}

func TestDNSDeleteDuplicateRecordsUsesSelectedRef(t *testing.T) {
	var primaryRequests []string
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryRequests = append(primaryRequests, r.Method+" "+trimWAPIPath(r.URL.Path))
		if r.Method != http.MethodDelete || trimWAPIPath(r.URL.Path) != "record:cname/ref-cname" {
			t.Fatalf("primary request = %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"_ref": "record:cname/ref-cname"})
	}))
	defer primary.Close()

	var readRequests []string
	read := duplicateRecordLookupServer(t, &readRequests)
	defer read.Close()

	app, _ := dnsWorkflowApp(t, primary.URL, read.URL)
	app.Output = tableOutput
	profile := mustLoadProfile(t, app)
	writeWorkflowRecordCache(t, app, profile)
	refreshes := captureRecordRefreshes(app)
	app.dnsDeleteRecordSelector = func(target string, matches []TypedRecord) (TypedRecord, bool, error) {
		if target != "app.example.com" {
			t.Fatalf("target = %q, want app.example.com", target)
		}
		if len(matches) != 2 {
			t.Fatalf("matches = %#v, want two duplicate candidates", matches)
		}
		for _, match := range matches {
			if cleanString(match.Item["_ref"]) == "record:cname/ref-cname" {
				return match, true, nil
			}
		}
		t.Fatalf("CNAME duplicate candidate missing: %#v", matches)
		return TypedRecord{}, false, nil
	}
	app.dnsDeleteConfirmer = func(target string, record TypedRecord) (bool, error) {
		if target != "app.example.com" {
			t.Fatalf("confirmation target = %q, want app.example.com", target)
		}
		if ref := cleanString(record.Item["_ref"]); ref != "record:cname/ref-cname" {
			t.Fatalf("confirmation record ref = %q, want record:cname/ref-cname", ref)
		}
		return true, nil
	}

	if err := app.Execute([]string{"dns", "delete", "cname", "app"}); err != nil {
		t.Fatalf("delete duplicate: %v", err)
	}

	if strings.Join(primaryRequests, ",") != "DELETE record:cname/ref-cname" {
		t.Fatalf("primary requests = %#v", primaryRequests)
	}
	if len(readRequests) == 0 {
		t.Fatalf("expected duplicate lookup requests on read server")
	}
	assertRecordCacheInvalidated(t, app, profile, "example.com")
	assertRecordRefreshQueued(t, refreshes, "example.com")
}

func TestDNSDeleteDuplicateRecordsFailsSafelyWhenNonInteractive(t *testing.T) {
	var primaryRequests []string
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryRequests = append(primaryRequests, r.Method+" "+trimWAPIPath(r.URL.Path))
		http.NotFound(w, r)
	}))
	defer primary.Close()

	var readRequests []string
	read := duplicateRecordLookupServer(t, &readRequests)
	defer read.Close()

	app, _ := dnsWorkflowApp(t, primary.URL, read.URL)
	app.Output = jsonOutput

	err := app.Execute([]string{"dns", "delete", "cname", "app"})
	if err == nil {
		t.Fatal("duplicate delete succeeded in non-interactive mode")
	}
	message := err.Error()
	for _, want := range []string{
		"multiple records found for app.example.com",
		"run in an interactive terminal to choose one",
		"ref=record:cname/ref-cname",
		"ref=record:cname/ref-cname-2",
		"value=alias.example.com",
		"value=alias2.example.com",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("duplicate error missing %q:\n%s", want, message)
		}
	}
	if len(primaryRequests) != 0 {
		t.Fatalf("non-interactive duplicate delete made primary requests: %#v", primaryRequests)
	}
	if len(readRequests) == 0 {
		t.Fatalf("expected duplicate lookup requests on read server")
	}
}

func TestDNSListDefaultsToFastAllRecordsOnly(t *testing.T) {
	var allRecordRequests int
	var detailRequests int
	server := dnsListDetailServer(t, &allRecordRequests, &detailRequests)
	defer server.Close()

	app, stdout := dnsWorkflowApp(t, server.URL, server.URL)
	if err := app.Execute([]string{"dns", "list"}); err != nil {
		t.Fatalf("dns list: %v\nstdout:\n%s", err, stdout.String())
	}
	if allRecordRequests != 1 {
		t.Fatalf("allrecords requests = %d, want 1", allRecordRequests)
	}
	if detailRequests != 0 {
		t.Fatalf("detail requests = %d, want 0 for default fast list", detailRequests)
	}
	if strings.Contains(stdout.String(), "300") {
		t.Fatalf("default fast list should not include enriched ttl:\n%s", stdout.String())
	}
}

func TestDNSListDetailsLoadsPerRecordDetails(t *testing.T) {
	var allRecordRequests int
	var detailRequests int
	server := dnsListDetailServer(t, &allRecordRequests, &detailRequests)
	defer server.Close()

	app, stdout := dnsWorkflowApp(t, server.URL, server.URL)
	if err := app.Execute([]string{"dns", "list", "--details"}); err != nil {
		t.Fatalf("dns list --details: %v\nstdout:\n%s", err, stdout.String())
	}
	if allRecordRequests != 1 {
		t.Fatalf("allrecords requests = %d, want 1", allRecordRequests)
	}
	if detailRequests != 1 {
		t.Fatalf("detail requests = %d, want 1 for --details", detailRequests)
	}
	if !strings.Contains(stdout.String(), "300") {
		t.Fatalf("details list should include enriched ttl:\n%s", stdout.String())
	}
}

func TestDNSListRecursiveIncludesChildZones(t *testing.T) {
	var mu sync.Mutex
	requestedZones := map[string]int{}
	detailRequests := map[string]int{}
	server := dnsListRecursiveServer(t, requestedZones, detailRequests, &mu)
	defer server.Close()

	app, stdout := dnsWorkflowApp(t, server.URL, server.URL)
	if err := app.Execute([]string{"dns", "list", "-r", "--details"}); err != nil {
		t.Fatalf("dns list -r --details: %v\nstdout:\n%s", err, stdout.String())
	}
	output := stdout.String()
	for _, want := range []string{"test.example.com", "test.child.example.com", "300"} {
		if !strings.Contains(output, want) {
			t.Fatalf("recursive list output missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "test.other.com") {
		t.Fatalf("recursive list included unrelated zone:\n%s", output)
	}

	mu.Lock()
	defer mu.Unlock()
	if requestedZones["example.com"] != 1 || requestedZones["child.example.com"] != 1 {
		t.Fatalf("recursive list zone requests = %#v", requestedZones)
	}
	if requestedZones["other.com"] != 0 {
		t.Fatalf("recursive list queried unrelated zone: %#v", requestedZones)
	}
	if detailRequests["example.com"] != 1 || detailRequests["child.example.com"] != 1 {
		t.Fatalf("recursive list detail requests = %#v", detailRequests)
	}
}

func TestDNSListUsesFreshCacheWithoutSerialValidation(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+trimWAPIPath(r.URL.Path))
		http.NotFound(w, r)
	}))
	defer server.Close()

	app, stdout := dnsWorkflowApp(t, server.URL, server.URL)
	profile := mustLoadProfile(t, app)
	if err := app.writeCachedRecords(profile, "example.com", "2026050801", []map[string]any{
		{"type": "HOST_IPV4ADDR", "name": "cached.example.com", "address": "192.0.2.10", "zone": "example.com"},
	}, time.Now()); err != nil {
		t.Fatalf("write record cache: %v", err)
	}

	if err := app.Execute([]string{"dns", "list"}); err != nil {
		t.Fatalf("dns list: %v\nstdout:\n%s", err, stdout.String())
	}
	if len(requests) != 0 {
		t.Fatalf("fresh cache should avoid server requests, got %#v", requests)
	}
	if !strings.Contains(stdout.String(), "cached.example.com") {
		t.Fatalf("dns list did not render cached record:\n%s", stdout.String())
	}
}

func TestDNSListFiltersByTypeAndExclude(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("dns list should use fresh cache, got %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	app, stdout := dnsWorkflowApp(t, server.URL, server.URL)
	profile := mustLoadProfile(t, app)
	if err := app.writeCachedRecords(profile, "example.com", "2026050801", []map[string]any{
		{"type": "record:a", "name": "app.example.com", "address": "192.0.2.10", "zone": "example.com", "comment": "keep"},
		{"type": "record:a", "name": "skip.example.com", "address": "192.0.2.11", "zone": "example.com", "comment": "remove me"},
		{"type": "record:txt", "name": "txt.example.com", "text": "keep me", "zone": "example.com", "comment": "keep"},
	}, time.Now()); err != nil {
		t.Fatalf("write record cache: %v", err)
	}

	if err := app.Execute([]string{"dns", "list", "--type", "a", "--exclude", "remove"}); err != nil {
		t.Fatalf("dns list filter: %v\nstdout:\n%s", err, stdout.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "app.example.com") {
		t.Fatalf("filtered list missing included record:\n%s", output)
	}
	for _, unwanted := range []string{"skip.example.com", "txt.example.com"} {
		if strings.Contains(output, unwanted) {
			t.Fatalf("filtered list included %q:\n%s", unwanted, output)
		}
	}
}

func TestDNSListSortsRecords(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("dns list should use fresh cache, got %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	app, stdout := dnsWorkflowApp(t, server.URL, server.URL)
	profile := mustLoadProfile(t, app)
	if err := app.writeCachedRecords(profile, "example.com", "2026050801", []map[string]any{
		{"type": "record:a", "name": "bravo.example.com", "address": "192.0.2.20", "zone": "example.com"},
		{"type": "record:a", "name": "alpha.example.com", "address": "192.0.2.10", "zone": "example.com"},
		{"type": "record:a", "name": "charlie.example.com", "address": "192.0.2.30", "zone": "example.com"},
	}, time.Now()); err != nil {
		t.Fatalf("write record cache: %v", err)
	}

	if err := app.Execute([]string{"-o", "json", "dns", "list", "--sort", "name"}); err != nil {
		t.Fatalf("dns list ascending sort: %v\nstdout:\n%s", err, stdout.String())
	}
	assertJSONRecordNames(t, stdout.String(), []string{"alpha.example.com", "bravo.example.com", "charlie.example.com"})

	stdout.Reset()
	if err := app.Execute([]string{"-o", "json", "dns", "list", "--sort", "-name"}); err != nil {
		t.Fatalf("dns list descending sort: %v\nstdout:\n%s", err, stdout.String())
	}
	assertJSONRecordNames(t, stdout.String(), []string{"charlie.example.com", "bravo.example.com", "alpha.example.com"})

	stdout.Reset()
	if err := app.Execute([]string{"-o", "json", "dns", "list", "--sort"}); err != nil {
		t.Fatalf("dns list default sort field: %v\nstdout:\n%s", err, stdout.String())
	}
	assertJSONRecordNames(t, stdout.String(), []string{"alpha.example.com", "bravo.example.com", "charlie.example.com"})
}

func TestDNSListSortsReverseRecordsNumericallyByDefault(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("dns list should use fresh cache, got %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	app, stdout := dnsWorkflowApp(t, server.URL, server.URL)
	profile := mustLoadProfile(t, app)
	reverseZone := "192.0.2.0/24"
	if err := app.writeCachedRecords(profile, reverseZone, "2026050801", []map[string]any{
		{"type": "record:ptr", "name": "10", "address": "192.0.2.10", "ptrdname": "ten.example.com", "zone": reverseZone},
		{"type": "record:ptr", "name": "100", "address": "192.0.2.100", "ptrdname": "hundred.example.com", "zone": reverseZone},
		{"type": "record:ptr", "name": "2", "address": "192.0.2.2", "ptrdname": "two.example.com", "zone": reverseZone},
	}, time.Now()); err != nil {
		t.Fatalf("write record cache: %v", err)
	}

	if err := app.Execute([]string{"-o", "json", "dns", "list", reverseZone}); err != nil {
		t.Fatalf("dns list reverse default sort: %v\nstdout:\n%s", err, stdout.String())
	}
	assertJSONRecordNames(t, stdout.String(), []string{"192.0.2.2", "192.0.2.10", "192.0.2.100"})
}

func TestDNSListCIDRParentScopeIncludesChildReverseZones(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("dns list should use fresh cache, got %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	app, stdout := dnsWorkflowApp(t, server.URL, server.URL)
	profile := mustLoadProfile(t, app)
	now := time.Now()
	if err := app.writeCachedZones(profile, []map[string]any{
		{"fqdn": "10.128.48.0/24", "view": "default", "zone_format": "IPV4"},
		{"fqdn": "10.128.49.0/24", "view": "default", "zone_format": "IPV4"},
	}, now); err != nil {
		t.Fatalf("write zone cache: %v", err)
	}
	if err := app.writeCachedRecords(profile, "10.128.48.0/24", "2026050801", []map[string]any{
		{"type": "record:ptr", "name": "10", "ptrdname": "host48.example.com", "zone": "10.128.48.0/24"},
	}, now); err != nil {
		t.Fatalf("write 48 record cache: %v", err)
	}
	if err := app.writeCachedRecords(profile, "10.128.49.0/24", "2026050801", []map[string]any{
		{"type": "record:ptr", "name": "10", "ptrdname": "host49.example.com", "zone": "10.128.49.0/24"},
	}, now); err != nil {
		t.Fatalf("write 49 record cache: %v", err)
	}

	if err := app.Execute([]string{"-o", "json", "dns", "list", "10.128.48.0/23", "--columns", "zone,name,value"}); err != nil {
		t.Fatalf("dns list parent CIDR scope: %v\nstdout:\n%s", err, stdout.String())
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &rows); err != nil {
		t.Fatalf("decode records: %v\n%s", err, stdout.String())
	}
	if len(rows) != 2 {
		t.Fatalf("record rows = %#v", rows)
	}
	if cleanString(rows[0]["zone"]) != "10.128.48.0/24" || cleanString(rows[1]["zone"]) != "10.128.49.0/24" {
		t.Fatalf("record zones = %#v", rows)
	}
}

func TestDNSListColumnsLimitJSONOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("dns list should use fresh cache, got %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	app, stdout := dnsWorkflowApp(t, server.URL, server.URL)
	profile := mustLoadProfile(t, app)
	if err := app.writeCachedRecords(profile, "example.com", "2026050801", []map[string]any{
		{"type": "record:a", "name": "app.example.com", "address": "192.0.2.10", "zone": "example.com", "comment": "selected"},
	}, time.Now()); err != nil {
		t.Fatalf("write record cache: %v", err)
	}

	if err := app.Execute([]string{"-o", "json", "dns", "list", "--columns", "name,value"}); err != nil {
		t.Fatalf("dns list columns: %v\nstdout:\n%s", err, stdout.String())
	}
	assertJSONRecordColumns(t, stdout.String(), []string{"name", "value"})
}

func TestDNSSearchColumnsLimitJSONOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("dns search should use fresh cache, got %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	app, stdout := dnsWorkflowApp(t, server.URL, server.URL)
	profile := mustLoadProfile(t, app)
	if err := app.writeCachedZones(profile, []map[string]any{
		{"fqdn": "example.com", "view": "default", "zone_format": "FORWARD", "primary_type": "Grid"},
	}, time.Now()); err != nil {
		t.Fatalf("write zone cache: %v", err)
	}
	if err := app.writeCachedRecords(profile, "example.com", "2026050801", []map[string]any{
		{"type": "record:a", "name": "app.example.com", "address": "192.0.2.10", "zone": "example.com", "comment": "search hit"},
	}, time.Now()); err != nil {
		t.Fatalf("write record cache: %v", err)
	}

	if err := app.Execute([]string{"-o", "json", "dns", "search", "app", "--columns", "name,comment"}); err != nil {
		t.Fatalf("dns search columns: %v\nstdout:\n%s", err, stdout.String())
	}
	assertJSONRecordColumns(t, stdout.String(), []string{"name", "comment"})
}

func TestDNSSearchNoRecordsSkipsTableOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("dns search should use fresh cache, got %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	app, stdout := dnsWorkflowApp(t, server.URL, server.URL)
	app.Output = tableOutput
	var stderr bytes.Buffer
	app.Stderr = &stderr
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)
	profile := mustLoadProfile(t, app)
	if err := app.writeCachedZones(profile, []map[string]any{
		{"fqdn": "example.com", "view": "default", "zone_format": "FORWARD", "primary_type": "Grid"},
	}, time.Now()); err != nil {
		t.Fatalf("write zone cache: %v", err)
	}
	if err := app.writeCachedRecords(profile, "example.com", "2026050801", []map[string]any{
		{"type": "record:a", "name": "app.example.com", "address": "192.0.2.10", "zone": "example.com", "comment": "search miss"},
	}, time.Now()); err != nil {
		t.Fatalf("write record cache: %v", err)
	}

	if err := app.Execute([]string{"dns", "search", "missing"}); err != nil {
		t.Fatalf("dns search: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), "DNS Records") {
		t.Fatalf("empty search rendered record table:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Current Context:") {
		t.Fatalf("empty search did not print current context:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "No records found.") {
		t.Fatalf("empty search warning missing:\n%s", stderr.String())
	}
}

func TestDNSNextIPRoutesLookupToReadAndFunctionToPrimary(t *testing.T) {
	var primaryRequests []string
	var readRequests []string
	var functionPayload map[string]any

	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryRequests = append(primaryRequests, r.Method+" "+trimWAPIPath(r.URL.Path))
		if r.Method != http.MethodPost || trimWAPIPath(r.URL.Path) != "network/ref" {
			t.Fatalf("primary request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("_function"); got != "next_available_ip" {
			t.Fatalf("_function = %q, want next_available_ip", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&functionPayload); err != nil {
			t.Fatalf("decode next-ip payload: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ips": []string{"192.0.2.20", "192.0.2.21"}})
	}))
	defer primary.Close()

	read := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		readRequests = append(readRequests, r.Method+" "+trimWAPIPath(r.URL.Path))
		if r.Method != http.MethodGet {
			t.Fatalf("read request = %s %s", r.Method, r.URL.Path)
		}
		objectType := trimWAPIPath(r.URL.Path)
		if objectType != networkObject && objectType != networkContainerObject {
			t.Fatalf("read request path = %s, want network or networkcontainer", r.URL.Path)
		}
		if got := r.URL.Query().Get("network"); got != "192.0.2.0/24" {
			t.Fatalf("network query = %q, want normalized CIDR", got)
		}
		if got := r.URL.Query().Get("network_view"); got != "default" {
			t.Fatalf("network_view query = %q, want default", got)
		}
		if objectType == networkContainerObject {
			_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{}})
			return
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
	if err := app.Execute([]string{"-o", "json", "dns", "next-ip", "192.0.2.99/24", "--network-view", "default", "--num", "2", "--exclude", "192.0.2.10"}); err != nil {
		t.Fatalf("dns next-ip: %v\nstdout:\n%s", err, stdout.String())
	}
	if strings.Join(readRequests, ",") != "GET network,GET networkcontainer" {
		t.Fatalf("read requests = %#v", readRequests)
	}
	if strings.Join(primaryRequests, ",") != "POST network/ref" {
		t.Fatalf("primary requests = %#v", primaryRequests)
	}
	if functionPayload["num"] != float64(2) {
		t.Fatalf("payload num = %#v, want 2; payload = %#v", functionPayload["num"], functionPayload)
	}
	excluded, ok := functionPayload["exclude"].([]any)
	if !ok || len(excluded) != 1 || excluded[0] != "192.0.2.10" {
		t.Fatalf("payload exclude = %#v, want [192.0.2.10]; payload = %#v", functionPayload["exclude"], functionPayload)
	}
	assertJSONNextIPRows(t, stdout.String(), []string{"192.0.2.20", "192.0.2.21"}, "192.0.2.0/24", "default")
}

func TestDNSNextIPCanUseNetworkContainer(t *testing.T) {
	var postPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/"+networkObject):
			_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{}})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/"+networkContainerObject):
			_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{{
				"_ref":         "networkcontainer/ref",
				"network":      "192.0.0.0/16",
				"network_view": "default",
			}}})
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/networkcontainer/ref"):
			postPath = trimWAPIPath(r.URL.Path)
			if got := r.URL.Query().Get("_function"); got != "next_available_ip" {
				t.Fatalf("_function = %q, want next_available_ip", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ips": []string{"192.0.2.20"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	app, stdout := dnsWorkflowApp(t, server.URL, server.URL)
	if err := app.Execute([]string{"-o", "json", "dns", "next-ip", "192.0.0.0/16", "--network-view", "default"}); err != nil {
		t.Fatalf("dns next-ip container: %v\nstdout:\n%s", err, stdout.String())
	}
	if postPath != "networkcontainer/ref" {
		t.Fatalf("post path = %q, want networkcontainer/ref", postPath)
	}
	assertJSONNextIPRows(t, stdout.String(), []string{"192.0.2.20"}, "192.0.0.0/16", "default")
}

func TestDNSNextIPTablePrintsContextAndRows(t *testing.T) {
	server := dnsNextIPServer(t, []map[string]any{{
		"_ref":         "network/ref",
		"network":      "192.0.2.0/24",
		"network_view": "default",
	}}, map[string]any{"ips": []string{"192.0.2.20"}})
	defer server.Close()

	app, stdout := dnsWorkflowApp(t, server.URL, server.URL)
	app.Output = tableOutput
	if err := app.Execute([]string{"dns", "next-ip", "192.0.2.0/24"}); err != nil {
		t.Fatalf("dns next-ip table: %v\nstdout:\n%s", err, stdout.String())
	}
	output := stdout.String()
	for _, want := range []string{"Current Context:", "Next Available IPs", "network", "network view", "ip", "192.0.2.0/24", "default", "192.0.2.20"} {
		if !strings.Contains(output, want) {
			t.Fatalf("table output missing %q:\n%s", want, output)
		}
	}
}

func TestDNSNextIPCSVOutput(t *testing.T) {
	server := dnsNextIPServer(t, []map[string]any{{
		"_ref":         "network/ref",
		"network":      "192.0.2.0/24",
		"network_view": "default",
	}}, map[string]any{"ips": []string{"192.0.2.20"}})
	defer server.Close()

	app, stdout := dnsWorkflowApp(t, server.URL, server.URL)
	if err := app.Execute([]string{"-o", "csv", "dns", "next-ip", "192.0.2.0/24"}); err != nil {
		t.Fatalf("dns next-ip csv: %v\nstdout:\n%s", err, stdout.String())
	}
	if got, want := stdout.String(), "network,network_view,ip\n192.0.2.0/24,default,192.0.2.20\n"; got != want {
		t.Fatalf("csv output = %q, want %q", got, want)
	}
}

func TestDNSNextIPRequiresNetworkViewWhenNetworkIsAmbiguous(t *testing.T) {
	server := dnsNextIPServer(t, []map[string]any{
		{"_ref": "network/ref1", "network": "192.0.2.0/24", "network_view": "default"},
		{"_ref": "network/ref2", "network": "192.0.2.0/24", "network_view": "lab"},
	}, map[string]any{"ips": []string{"192.0.2.20"}})
	defer server.Close()

	app, stdout := dnsWorkflowApp(t, server.URL, server.URL)
	err := app.Execute([]string{"dns", "next-ip", "192.0.2.0/24"})
	if err == nil {
		t.Fatal("ambiguous network succeeded, want error")
	}
	if !strings.Contains(err.Error(), "multiple networks or containers found for 192.0.2.0/24; use --network-view to choose one") {
		t.Fatalf("error = %v\nstdout:\n%s", err, stdout.String())
	}
}

func TestDNSNextIPReturnsClearErrorWhenNetworkIsMissing(t *testing.T) {
	server := dnsNextIPServer(t, nil, map[string]any{"ips": []string{"192.0.2.20"}})
	defer server.Close()

	app, stdout := dnsWorkflowApp(t, server.URL, server.URL)
	err := app.Execute([]string{"dns", "next-ip", "192.0.2.0/24"})
	if err == nil {
		t.Fatal("missing network succeeded, want error")
	}
	if !strings.Contains(err.Error(), "no network or container found for 192.0.2.0/24") {
		t.Fatalf("error = %v\nstdout:\n%s", err, stdout.String())
	}
}

func TestDNSNextIPRejectsMalformedFunctionResponse(t *testing.T) {
	server := dnsNextIPServer(t, []map[string]any{{
		"_ref":         "network/ref",
		"network":      "192.0.2.0/24",
		"network_view": "default",
	}}, map[string]any{"result": []string{"not-ips"}})
	defer server.Close()

	app, stdout := dnsWorkflowApp(t, server.URL, server.URL)
	err := app.Execute([]string{"dns", "next-ip", "192.0.2.0/24"})
	if err == nil {
		t.Fatal("malformed next_available_ip response succeeded, want error")
	}
	if !strings.Contains(err.Error(), "next_available_ip response did not include an ips list") {
		t.Fatalf("error = %v\nstdout:\n%s", err, stdout.String())
	}
}

func TestDNSZoneListFiltersSortsAndSelectsColumns(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("dns zone list should use fresh cache, got %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	app, stdout := dnsWorkflowApp(t, server.URL, server.URL)
	profile := mustLoadProfile(t, app)
	if err := app.writeCachedZones(profile, []map[string]any{
		{"fqdn": "alpha.example.com", "view": "default", "zone_format": "FORWARD", "ns_group": "default", "comment": "keep"},
		{"fqdn": "beta.example.com", "view": "default", "zone_format": "IPV4", "ns_group": "default", "comment": "keep"},
		{"fqdn": "zeta.example.com", "view": "default", "zone_format": "FORWARD", "ns_group": "default", "comment": "keep"},
		{"fqdn": "skip.example.com", "view": "default", "zone_format": "FORWARD", "ns_group": "default", "comment": "skip this"},
	}, time.Now()); err != nil {
		t.Fatalf("write zone cache: %v", err)
	}

	if err := app.Execute([]string{"-o", "json", "dns", "zone", "list", "--type", "FORWARD", "--exclude", "skip", "--sort", "-zone", "--columns", "zone,format"}); err != nil {
		t.Fatalf("dns zone list: %v\nstdout:\n%s", err, stdout.String())
	}
	assertJSONZoneRows(t, stdout.String(), []string{"zeta.example.com", "alpha.example.com"}, []string{"zone", "format"})
}

func TestDNSSearchKeepsBufferedStderrClean(t *testing.T) {
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
					{"fqdn": "example.com", "view": "default", "zone_format": "FORWARD", "primary_type": "Grid"},
				},
			})
		case strings.HasSuffix(r.URL.Path, "/allrecords"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{
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

	app, stdout := dnsWorkflowApp(t, server.URL, server.URL)
	app.Output = tableOutput
	var stderr bytes.Buffer
	app.Stderr = &stderr
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"dns", "search", "app", "--zone", "example.com"}); err != nil {
		t.Fatalf("dns search: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if stderr.String() != "" {
		t.Fatalf("search wrote progress to buffered stderr: %q", stderr.String())
	}
	if !strings.Contains(stdout.String(), "app.example.com") {
		t.Fatalf("search output missing record:\n%s", stdout.String())
	}
}

func assertJSONRecordNames(t *testing.T, output string, want []string) {
	t.Helper()
	var rows []map[string]any
	if err := json.Unmarshal([]byte(output), &rows); err != nil {
		t.Fatalf("decode records JSON: %v\n%s", err, output)
	}
	var got []string
	for _, row := range rows {
		got = append(got, cleanString(row["name"]))
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("record names = %#v, want %#v\noutput:\n%s", got, want, output)
	}
}

func assertJSONNextIPRows(t *testing.T, output string, wantIPs []string, wantNetwork string, wantNetworkView string) {
	t.Helper()
	var rows []map[string]any
	if err := json.Unmarshal([]byte(output), &rows); err != nil {
		t.Fatalf("decode next IP JSON: %v\n%s", err, output)
	}
	if len(rows) != len(wantIPs) {
		t.Fatalf("rows = %d, want %d: %#v", len(rows), len(wantIPs), rows)
	}
	for index, wantIP := range wantIPs {
		row := rows[index]
		if cleanString(row["ip"]) != wantIP ||
			cleanString(row["network"]) != wantNetwork ||
			cleanString(row["network_view"]) != wantNetworkView {
			t.Fatalf("row %d = %#v, want ip=%s network=%s network_view=%s", index, row, wantIP, wantNetwork, wantNetworkView)
		}
	}
}

func assertJSONZoneRows(t *testing.T, output string, wantZones []string, wantColumns []string) {
	t.Helper()
	var rows []map[string]any
	if err := json.Unmarshal([]byte(output), &rows); err != nil {
		t.Fatalf("decode zones JSON: %v\n%s", err, output)
	}
	if len(rows) != len(wantZones) {
		t.Fatalf("zone rows = %d, want %d: %#v", len(rows), len(wantZones), rows)
	}
	wantSet := map[string]bool{}
	for _, column := range wantColumns {
		wantSet[column] = true
	}
	for index, row := range rows {
		if cleanString(row["zone"]) != wantZones[index] {
			t.Fatalf("row %d zone = %q, want %q: %#v", index, cleanString(row["zone"]), wantZones[index], rows)
		}
		if len(row) != len(wantSet) {
			t.Fatalf("row columns = %#v, want %#v", row, wantColumns)
		}
		for column := range wantSet {
			if _, ok := row[column]; !ok {
				t.Fatalf("row missing column %q: %#v", column, row)
			}
		}
	}
}

func assertJSONRecordColumns(t *testing.T, output string, want []string) {
	t.Helper()
	var rows []map[string]any
	if err := json.Unmarshal([]byte(output), &rows); err != nil {
		t.Fatalf("decode records JSON: %v\n%s", err, output)
	}
	if len(rows) == 0 {
		t.Fatalf("no JSON rows in output:\n%s", output)
	}
	wantSet := map[string]bool{}
	for _, column := range want {
		wantSet[column] = true
	}
	for _, row := range rows {
		if len(row) != len(wantSet) {
			t.Fatalf("row columns = %#v, want %#v\noutput:\n%s", row, want, output)
		}
		for column := range wantSet {
			if _, ok := row[column]; !ok {
				t.Fatalf("row missing column %q: %#v", column, row)
			}
		}
	}
}

func dnsNextIPServer(t *testing.T, networks []map[string]any, functionResponse map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/"+networkObject):
			_ = json.NewEncoder(w).Encode(map[string]any{"result": networks})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/"+networkContainerObject):
			_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{}})
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/network/ref"):
			if got := r.URL.Query().Get("_function"); got != "next_available_ip" {
				t.Fatalf("_function = %q, want next_available_ip", got)
			}
			_ = json.NewEncoder(w).Encode(functionResponse)
		default:
			http.NotFound(w, r)
		}
	}))
}

func dnsWorkflowApp(t *testing.T, server, readServer string) (*App, *bytes.Buffer) {
	t.Helper()
	app := testApp(t)
	var stdout bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &bytes.Buffer{}
	app.Output = jsonOutput
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)
	profile := Profile{
		Name:        defaultProfileName,
		Server:      server,
		ReadServer:  readServer,
		Username:    "admin",
		Password:    "secret",
		WAPIVersion: defaultWAPIVersion,
		DNSView:     "default",
		DefaultZone: "example.com",
		VerifySSL:   false,
		Timeout:     defaultTimeoutSeconds,
	}
	if err := app.writeConfigProfiles(defaultProfileName, map[string]Profile{defaultProfileName: profile}); err != nil {
		t.Fatalf("write profile: %v", err)
	}
	return app, &stdout
}

func dnsListDetailServer(t *testing.T, allRecordRequests *int, detailRequests *int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/zone_auth"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{{
					"fqdn":              r.URL.Query().Get("fqdn"),
					"view":              "default",
					"zone_format":       "FORWARD",
					"primary_type":      "Grid",
					"soa_serial_number": "2026050801",
				}},
			})
		case strings.HasSuffix(r.URL.Path, "/allrecords"):
			*allRecordRequests++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{{
					"type":    "record:a",
					"name":    "app",
					"address": "192.0.2.10",
					"zone":    "example.com",
					"record": map[string]any{
						"_ref":     "record:a/ref",
						"name":     "app.example.com",
						"ipv4addr": "192.0.2.10",
					},
				}},
			})
		case strings.HasSuffix(r.URL.Path, "/record:a/ref"):
			*detailRequests++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"_ref":     "record:a/ref",
				"name":     "app.example.com",
				"ipv4addr": "192.0.2.10",
				"ttl":      300,
				"use_ttl":  true,
				"view":     "default",
				"zone":     "example.com",
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

func dnsListRecursiveServer(t *testing.T, requestedZones map[string]int, detailRequests map[string]int, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	zones := []map[string]any{
		{"fqdn": "example.com", "view": "default", "zone_format": "FORWARD", "primary_type": "Grid"},
		{"fqdn": "child.example.com", "view": "default", "zone_format": "FORWARD", "primary_type": "Grid"},
		{"fqdn": "other.com", "view": "default", "zone_format": "FORWARD", "primary_type": "Grid"},
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/zone_auth"):
			if fqdn := r.URL.Query().Get("fqdn"); fqdn != "" {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"result": []map[string]any{{
						"fqdn":              fqdn,
						"view":              "default",
						"zone_format":       "FORWARD",
						"primary_type":      "Grid",
						"soa_serial_number": "2026050801",
					}},
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
				"result": []map[string]any{{
					"type":    "record:a",
					"name":    "test." + zone,
					"address": "192.0.2.10",
					"zone":    zone,
					"record": map[string]any{
						"_ref":     "record:a/ref-" + zone,
						"name":     "test." + zone,
						"ipv4addr": "192.0.2.10",
					},
				}},
			})
		case strings.Contains(r.URL.Path, "/record:a/ref-"):
			zone := strings.TrimPrefix(path.Base(r.URL.Path), "ref-")
			mu.Lock()
			detailRequests[zone]++
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"_ref":     "record:a/" + path.Base(r.URL.Path),
				"name":     "test." + zone,
				"ipv4addr": "192.0.2.10",
				"ttl":      300,
				"use_ttl":  true,
				"view":     "default",
				"zone":     zone,
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

func emptyReadServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("read request = %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{}})
	}))
}

func recordLookupServer(t *testing.T, requests *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("read request = %s %s", r.Method, r.URL.Path)
		}
		object := trimWAPIPath(r.URL.Path)
		*requests = append(*requests, r.Method+" "+object)
		if object == "record:a" && r.URL.Query().Get("name") == "app.example.com" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{{
					"_ref":     "record:a/ref",
					"name":     "app.example.com",
					"ipv4addr": "192.0.2.10",
					"view":     "default",
					"zone":     "example.com",
				}},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{}})
	}))
}

func duplicateRecordLookupServer(t *testing.T, requests *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("read request = %s %s", r.Method, r.URL.Path)
		}
		object := trimWAPIPath(r.URL.Path)
		*requests = append(*requests, r.Method+" "+object)
		if r.URL.Query().Get("name") != "app.example.com" {
			_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{}})
			return
		}
		switch object {
		case "record:a":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{{
					"_ref":     "record:a/ref-a",
					"name":     "app.example.com",
					"ipv4addr": "192.0.2.10",
					"view":     "default",
					"zone":     "example.com",
					"comment":  "primary address",
				}},
			})
		case "record:cname":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{{
					"_ref":      "record:cname/ref-cname",
					"name":      "app.example.com",
					"canonical": "alias.example.com",
					"view":      "default",
					"zone":      "example.com",
					"comment":   "temporary alias",
				}, {
					"_ref":      "record:cname/ref-cname-2",
					"name":      "app.example.com",
					"canonical": "alias2.example.com",
					"view":      "default",
					"zone":      "example.com",
					"comment":   "secondary alias",
				}},
			})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{}})
		}
	}))
}

func cnameRecordLookupServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("read request = %s %s", r.Method, r.URL.Path)
		}
		if trimWAPIPath(r.URL.Path) == "record:cname" && r.URL.Query().Get("name") == "hostalias1.example.com" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{{
					"_ref":      "record:cname/ref",
					"name":      "hostalias1.example.com",
					"canonical": "old.example.com",
					"view":      "default",
					"zone":      "example.com",
				}},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{}})
	}))
}

func stubLookupHost(t *testing.T, wantHost string) {
	t.Helper()
	previous := lookupHost
	lookupHost = func(host string) ([]string, error) {
		if host != wantHost {
			t.Fatalf("lookup host = %q, want %q", host, wantHost)
		}
		return []string{"192.0.2.10"}, nil
	}
	t.Cleanup(func() {
		lookupHost = previous
	})
}

func trimWAPIPath(path string) string {
	return strings.TrimPrefix(path, "/wapi/"+defaultWAPIVersion+"/")
}

func mustLoadProfile(t *testing.T, app *App) Profile {
	t.Helper()
	profile, err := app.loadConfig(true)
	if err != nil {
		t.Fatalf("load profile: %v", err)
	}
	return profile
}

func writeWorkflowRecordCache(t *testing.T, app *App, profile Profile) {
	t.Helper()
	if err := app.writeCachedRecords(profile, "example.com", "2026050801", []map[string]any{
		{"type": "record:a", "name": "cached.example.com", "address": "192.0.2.1", "zone": "example.com"},
	}, time.Now()); err != nil {
		t.Fatalf("write record cache: %v", err)
	}
}

func assertRecordCacheInvalidated(t *testing.T, app *App, profile Profile, zone string) {
	t.Helper()
	entry, err := app.readCachedRecords(profile, zone)
	if err != nil {
		t.Fatalf("read record cache: %v", err)
	}
	if entry.CacheFound {
		t.Fatalf("record cache for %s was not invalidated", zone)
	}
}

func assertZoneCacheInvalidated(t *testing.T, app *App, profile Profile) {
	t.Helper()
	entry, err := app.readCachedZones(profile)
	if err != nil {
		t.Fatalf("read zone cache: %v", err)
	}
	if entry.CacheFound {
		t.Fatalf("zone cache was not invalidated")
	}
}

func captureRecordRefreshes(app *App) chan string {
	refreshes := make(chan string, 8)
	app.backgroundRecordRevalidator = func(profile Profile, zone string) error {
		refreshes <- zone
		return nil
	}
	return refreshes
}

func captureZoneRefreshes(app *App) chan string {
	refreshes := make(chan string, 4)
	app.backgroundZoneRefresher = func(profile Profile) error {
		refreshes <- profile.DNSView
		return nil
	}
	return refreshes
}

func assertRecordRefreshQueued(t *testing.T, refreshes <-chan string, wantZone string) {
	t.Helper()
	select {
	case zone := <-refreshes:
		if zone != wantZone {
			t.Fatalf("record refresh zone = %q, want %q", zone, wantZone)
		}
	default:
		t.Fatalf("record refresh for %s was not queued", wantZone)
	}
}

func assertNoRecordRefreshQueued(t *testing.T, refreshes <-chan string) {
	t.Helper()
	select {
	case zone := <-refreshes:
		t.Fatalf("unexpected record refresh queued for %s", zone)
	default:
	}
}

func assertZoneRefreshQueued(t *testing.T, refreshes <-chan string, wantView string) {
	t.Helper()
	select {
	case view := <-refreshes:
		if view != wantView {
			t.Fatalf("zone refresh view = %q, want %q", view, wantView)
		}
	default:
		t.Fatalf("zone cache refresh for view %s was not queued", wantView)
	}
}
