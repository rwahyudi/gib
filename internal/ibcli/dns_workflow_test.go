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

	if err := app.Execute([]string{"dns", "create", "app", "a", "192.0.2.10", "--noptr"}); err != nil {
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
}

func TestDNSCreateWorkflowRejectsOldTypeNameValueOrder(t *testing.T) {
	var primaryRequests []string
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryRequests = append(primaryRequests, r.Method+" "+trimWAPIPath(r.URL.Path))
		http.NotFound(w, r)
	}))
	defer primary.Close()

	read := emptyReadServer(t)
	defer read.Close()

	app, _ := dnsWorkflowApp(t, primary.URL, read.URL)
	err := app.Execute([]string{"dns", "create", "a", "app", "192.0.2.10", "--noptr"})
	if err == nil {
		t.Fatal("old TYPE NAME VALUE order succeeded, want unsupported record type error")
	}
	if !strings.Contains(err.Error(), `unsupported record type "app"`) {
		t.Fatalf("error = %v", err)
	}
	if len(primaryRequests) != 0 {
		t.Fatalf("old order made primary requests: %#v", primaryRequests)
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

	if err := app.Execute([]string{"dns", "edit", "app", "a", "192.0.2.20", "--ttl", "600", "--comment", "Updated", "--noptr"}); err != nil {
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
}

func TestDNSDeleteWorkflowReadsFromReadServerAndWritesPrimary(t *testing.T) {
	var primaryRequests []string
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryRequests = append(primaryRequests, r.Method+" "+trimWAPIPath(r.URL.Path))
		if r.Method != http.MethodDelete || trimWAPIPath(r.URL.Path) != "record:a/ref" {
			t.Fatalf("primary request = %s %s", r.Method, r.URL.Path)
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

	if err := app.Execute([]string{"dns", "delete", "app"}); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if strings.Join(primaryRequests, ",") != "DELETE record:a/ref" {
		t.Fatalf("primary requests = %#v", primaryRequests)
	}
	if len(readRequests) == 0 {
		t.Fatalf("expected record lookup requests on read server")
	}
	assertRecordCacheInvalidated(t, app, profile, "example.com")
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
