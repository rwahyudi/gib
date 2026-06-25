package ibcli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dgraph-io/badger/v4"
)

func TestCacheStatusAndClear(t *testing.T) {
	app := testApp(t)
	profile := Profile{Name: "default", DNSView: "default"}
	if err := app.writeCachedZones(profile, []map[string]any{{"fqdn": "example.com"}}, time.Now()); err != nil {
		t.Fatalf("write zone cache: %v", err)
	}
	if err := app.writeCachedRecords(profile, "example.com", "2026050801", []map[string]any{{"name": "app.example.com"}}, time.Now()); err != nil {
		t.Fatalf("write record cache: %v", err)
	}
	if err := app.writeCachedNetworkViews(profile, []map[string]any{{"name": "default"}}, time.Now()); err != nil {
		t.Fatalf("write network view cache: %v", err)
	}
	if err := app.writeCachedNetworks(profile, "default", []map[string]any{{"network": "192.0.2.0/24"}}, time.Now()); err != nil {
		t.Fatalf("write network cache: %v", err)
	}
	if err := app.writeCachedNetworkContainers(profile, "default", []map[string]any{{"network": "192.0.0.0/16"}}, time.Now()); err != nil {
		t.Fatalf("write container cache: %v", err)
	}
	if err := app.writeCachedIPv4Addresses(profile, "192.0.2.10", "default", []map[string]any{{"ip_address": "192.0.2.10"}}, time.Now()); err != nil {
		t.Fatalf("write address cache: %v", err)
	}

	rows, err := app.cacheStatusRows()
	if err != nil {
		t.Fatalf("cache status: %v", err)
	}
	if len(rows) != 6 {
		t.Fatalf("status rows = %#v", rows)
	}
	for _, row := range rows {
		if row["kind"] == "records" && row["serial"] != "2026050801" {
			t.Fatalf("serial = %#v, want integer text", row["serial"])
		}
		if _, ok := row["fresh_until"]; ok {
			t.Fatalf("cache status still exposes fresh_until: %#v", row)
		}
		if _, ok := row["expires"]; ok {
			t.Fatalf("cache status still exposes expires: %#v", row)
		}
		if row["kind"] == "records" && row["stale_expires"] == "" {
			t.Fatalf("record status missing stale_expires: %#v", row)
		}
	}
	if err := app.clearCache(); err != nil {
		t.Fatalf("clear cache: %v", err)
	}
	rows, err = app.cacheStatusRows()
	if err != nil {
		t.Fatalf("cache status after clear: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("status rows after clear = %#v", rows)
	}
}

func TestCacheBadgerOptionsMinimizeValueLogUse(t *testing.T) {
	path := t.TempDir()
	options := cacheBadgerOptions(path)
	lsmOptions := badger.LSMOnlyOptions(path)
	if options.Dir != path || options.ValueDir != path {
		t.Fatalf("cache dirs = %q/%q, want %q", options.Dir, options.ValueDir, path)
	}
	if options.ValueThreshold != lsmOptions.ValueThreshold {
		t.Fatalf("value threshold = %d, want LSM-only threshold %d", options.ValueThreshold, lsmOptions.ValueThreshold)
	}
	if !options.CompactL0OnClose {
		t.Fatal("CompactL0OnClose = false, want true")
	}
	if options.Logger != nil {
		t.Fatal("cache Badger logger should be disabled")
	}
}

func TestCacheStatusNormalizesScientificSerial(t *testing.T) {
	app := testApp(t)
	profile := Profile{Name: "default", DNSView: "default"}
	if err := app.writeCachedRecords(profile, "example.com", "2.02509301e+09", []map[string]any{{"name": "app.example.com"}}, time.Now()); err != nil {
		t.Fatalf("write record cache: %v", err)
	}
	rows, err := app.cacheStatusRows()
	if err != nil {
		t.Fatalf("cache status: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("status rows = %#v", rows)
	}
	if rows[0]["serial"] != "2025093010" {
		t.Fatalf("serial = %#v, want integer text", rows[0]["serial"])
	}
}

func TestCacheStatusStatisticsFooterAndMachineReadableOutput(t *testing.T) {
	app := testApp(t)
	writeConfigForSettings(t, app, ConfigSettings{
		CacheTTLSeconds:        3600,
		DNSSearchWorkerLimit:   defaultDNSSearchWorkerLimit,
		RecordsCacheSWRSeconds: 7200,
	})
	profile := Profile{Name: "default", DNSView: "default"}
	now := time.Now()
	if err := app.writeCachedZones(profile, []map[string]any{{"fqdn": "example.com"}}, now.Add(-time.Minute)); err != nil {
		t.Fatalf("write zone cache: %v", err)
	}
	if err := app.writeCachedRecords(profile, "fresh.example.com", "2026050801", []map[string]any{{"name": "app1"}, {"name": "app2"}}, now.Add(-time.Minute)); err != nil {
		t.Fatalf("write fresh record cache: %v", err)
	}
	if err := app.writeCachedRecords(profile, "stale.example.com", "2026050802", []map[string]any{{"name": "old1"}}, now.Add(-4000*time.Second)); err != nil {
		t.Fatalf("write stale record cache: %v", err)
	}
	if err := app.writeCachedRecords(profile, "expired.example.com", "2026050803", []map[string]any{{"name": "dead1"}, {"name": "dead2"}, {"name": "dead3"}}, now.Add(-8000*time.Second)); err != nil {
		t.Fatalf("write expired record cache: %v", err)
	}
	acquired, err := app.tryAcquireRecordRefreshLease(profile, "stale.example.com", now, recordRefreshLeaseTTL)
	if err != nil {
		t.Fatalf("acquire refresh lease: %v", err)
	}
	if !acquired {
		t.Fatal("refresh lease was not acquired")
	}

	snapshot, err := app.cacheStatusSnapshot()
	if err != nil {
		t.Fatalf("cache status snapshot: %v", err)
	}
	stats := snapshot.Statistics
	if stats.CacheEntries != 4 || stats.ZoneEntries != 1 || stats.RecordEntries != 3 {
		t.Fatalf("entry stats = %#v, want 4 total, 1 zone, 3 record", stats)
	}
	if stats.CachedRecords != 6 {
		t.Fatalf("cached records = %d, want 6", stats.CachedRecords)
	}
	if stats.FreshEntries != 2 || stats.SWRStaleEntries != 1 || stats.ExpiredEntries != 1 || stats.ActiveRefreshes != 1 {
		t.Fatalf("state stats = %#v, want fresh=2 stale=1 expired=1 active=1", stats)
	}

	var stdout bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &bytes.Buffer{}
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)
	if err := app.Execute([]string{"config", "cache", "status"}); err != nil {
		t.Fatalf("cache status table: %v", err)
	}
	tableOutput := stdout.String()
	for _, want := range []string{"Cache Status", "Cache entries 4", "Cached records 6", "Fresh 2", "SWR stale 1", "Expired 1", "Refreshing 1"} {
		if !strings.Contains(tableOutput, want) {
			t.Fatalf("table output missing %q:\n%s", want, tableOutput)
		}
	}

	stdout.Reset()
	if err := app.Execute([]string{"-o", "json", "config", "cache", "status"}); err != nil {
		t.Fatalf("cache status json: %v", err)
	}
	var payload cacheStatusSnapshot
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode cache status json: %v\n%s", err, stdout.String())
	}
	if payload.Statistics.CachedRecords != 6 || len(payload.Entries) != 4 {
		t.Fatalf("json payload = %#v", payload)
	}

	stdout.Reset()
	if err := app.Execute([]string{"-o", "csv", "config", "cache", "status"}); err != nil {
		t.Fatalf("cache status csv: %v", err)
	}
	csvOutput := stdout.String()
	if strings.Contains(csvOutput, "Cache entries") || strings.Contains(csvOutput, "Cached records") {
		t.Fatalf("csv output should stay row-only:\n%s", csvOutput)
	}
	if !strings.HasPrefix(csvOutput, "kind,profile,view,zone,serial,items,age,stale_expires\n") {
		t.Fatalf("csv output missing row header:\n%s", csvOutput)
	}
}

func TestConfiguredCacheTTLControlsComputedFreshness(t *testing.T) {
	app := testApp(t)
	writeConfigForSettings(t, app, ConfigSettings{
		CacheTTLSeconds:        42,
		DNSSearchWorkerLimit:   defaultDNSSearchWorkerLimit,
		RecordsCacheSWRSeconds: 77,
	})
	profile := Profile{Name: "default", DNSView: "default"}
	now := time.Now()
	if err := app.writeCachedRecords(profile, "example.com", "2026050801", []map[string]any{{"name": "app.example.com"}}, now); err != nil {
		t.Fatalf("write record cache: %v", err)
	}
	entry, err := app.readCachedRecords(profile, "example.com")
	if err != nil {
		t.Fatalf("read record cache: %v", err)
	}
	if app.cacheFreshUntil(entry.CachedAt)-entry.CachedAt != 42 {
		t.Fatalf("cache ttl = %d seconds, want 42", app.cacheFreshUntil(entry.CachedAt)-entry.CachedAt)
	}
	if !app.cacheEntryFresh(entry, time.Unix(entry.CachedAt+41, 0)) {
		t.Fatalf("cache should be fresh before cached_at + cache_ttl")
	}
	if app.cacheEntryFresh(entry, time.Unix(entry.CachedAt+43, 0)) {
		t.Fatalf("cache should be stale after cached_at + cache_ttl")
	}
	if entry.StaleExpiresAt-entry.CachedAt != 77 {
		t.Fatalf("cache swr ttl = %d seconds, want 77", entry.StaleExpiresAt-entry.CachedAt)
	}
	if err := app.writeCachedZones(profile, []map[string]any{{"fqdn": "example.com"}}, now); err != nil {
		t.Fatalf("write zone cache: %v", err)
	}
	zoneEntry, err := app.readCachedZones(profile)
	if err != nil {
		t.Fatalf("read zone cache: %v", err)
	}
	if app.cacheFreshUntil(zoneEntry.CachedAt)-zoneEntry.CachedAt != 42 {
		t.Fatalf("zone cache ttl = %d seconds, want 42", app.cacheFreshUntil(zoneEntry.CachedAt)-zoneEntry.CachedAt)
	}
}

func TestCachedRecordsForZoneUsesFreshCacheWithoutSerialValidation(t *testing.T) {
	var allRecordRequests int
	var zoneRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/zone_auth"):
			zoneRequests++
			_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{{"fqdn": r.URL.Query().Get("fqdn"), "view": "default", "zone_format": "FORWARD", "soa_serial_number": "2026050801"}}})
		case strings.HasSuffix(r.URL.Path, "/allrecords"):
			allRecordRequests++
			_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{{"type": "HOST_IPV4ADDR", "name": "live.example.com", "address": "192.0.2.20"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	app := testApp(t)
	profile := Profile{Name: "default", DNSView: "default"}
	if err := app.writeCachedRecords(profile, "example.com", "2026050801", []map[string]any{{"type": "HOST_IPV4ADDR", "name": "cached.example.com", "address": "192.0.2.10"}}, time.Now()); err != nil {
		t.Fatalf("write record cache: %v", err)
	}

	records, err := app.cachedRecordsForZone(profile, testWapiClient(server), "example.com")
	if err != nil {
		t.Fatalf("cached records: %v", err)
	}
	if zoneRequests != 0 {
		t.Fatalf("zone serial requests = %d, want 0 for fresh cache", zoneRequests)
	}
	if allRecordRequests != 0 {
		t.Fatalf("allrecords requests = %d", allRecordRequests)
	}
	if len(records) != 1 || recordName(records[0].Item, records[0].Type) != "cached.example.com" {
		t.Fatalf("records = %#v", records)
	}
}

func TestCachedRecordsForZoneWithSourceReportsCachePath(t *testing.T) {
	var allRecordRequests int
	server := recordCacheServer(t, "2026050801", []map[string]any{{"type": "HOST_IPV4ADDR", "name": "live.example.com", "address": "192.0.2.20"}}, &allRecordRequests)
	defer server.Close()

	app := testApp(t)
	profile := Profile{Name: "default", DNSView: "default"}
	if err := app.writeCachedRecords(profile, "fresh.example.com", "2026050801", []map[string]any{{"type": "HOST_IPV4ADDR", "name": "cached.example.com", "address": "192.0.2.10"}}, time.Now()); err != nil {
		t.Fatalf("write fresh cache: %v", err)
	}
	_, source, err := app.cachedRecordsForZoneWithSource(profile, testWapiClient(server), "fresh.example.com")
	if err != nil {
		t.Fatalf("fresh cache records: %v", err)
	}
	if source != recordCacheSourceFreshCache {
		t.Fatalf("fresh source = %q, want %q", source, recordCacheSourceFreshCache)
	}

	before := time.Now()
	if err := app.writeCachedRecordsEntry(profile, "serial.example.com", "2026050801", []map[string]any{{"type": "HOST_IPV4ADDR", "name": "cached.example.com", "address": "192.0.2.10"}}, before.Add(-time.Hour).Unix(), before.Add(-time.Second).Unix()); err != nil {
		t.Fatalf("write expired cache: %v", err)
	}
	_, source, err = app.cachedRecordsForZoneWithSource(profile, testWapiClient(server), "serial.example.com")
	if err != nil {
		t.Fatalf("serial cache records: %v", err)
	}
	if source != recordCacheSourceSerialCache {
		t.Fatalf("serial source = %q, want %q", source, recordCacheSourceSerialCache)
	}
	if allRecordRequests != 0 {
		t.Fatalf("allrecords requests = %d, want 0", allRecordRequests)
	}
}

func TestCachedRecordsForZoneReturnsStaleCacheAndLaunchesRevalidation(t *testing.T) {
	app := testApp(t)
	profile := Profile{Name: "default", DNSView: "default"}
	now := time.Now()
	if err := app.writeCachedRecordsEntry(profile, "example.com", "2026050801", []map[string]any{{"type": "HOST_IPV4ADDR", "name": "cached.example.com", "address": "192.0.2.10"}}, now.Add(-time.Hour).Unix(), now.Add(time.Hour).Unix()); err != nil {
		t.Fatalf("write stale record cache: %v", err)
	}

	type revalidationCall struct {
		Profile Profile
		Zone    string
	}
	started := make(chan revalidationCall, 2)
	app.backgroundRecordRevalidator = func(profile Profile, zone string) error {
		started <- revalidationCall{Profile: profile, Zone: zone}
		return nil
	}

	records, source, err := app.cachedRecordsForZoneWithSource(profile, &WapiClient{View: "default"}, "example.com")
	if err != nil {
		t.Fatalf("stale cached records: %v", err)
	}
	if source != recordCacheSourceStaleCache {
		t.Fatalf("source = %q, want %q", source, recordCacheSourceStaleCache)
	}
	if len(records) != 1 || recordName(records[0].Item, records[0].Type) != "cached.example.com" {
		t.Fatalf("records = %#v", records)
	}

	var call revalidationCall
	select {
	case call = <-started:
	default:
		t.Fatalf("revalidation launch did not happen before stale response returned")
	}
	if call.Zone != "example.com" || call.Profile.Name != "default" || call.Profile.DNSView != "default" {
		t.Fatalf("revalidation target profile=%#v zone=%q", call.Profile, call.Zone)
	}

	_, source, err = app.cachedRecordsForZoneWithSource(profile, &WapiClient{View: "default"}, "example.com")
	if err != nil {
		t.Fatalf("second stale cached records: %v", err)
	}
	if source != recordCacheSourceStaleCache {
		t.Fatalf("second source = %q, want %q", source, recordCacheSourceStaleCache)
	}
	select {
	case call := <-started:
		t.Fatalf("duplicate revalidation started while lease is active: profile=%#v zone=%q", call.Profile, call.Zone)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestRecordRefreshLeaseScopesByZoneAndExpires(t *testing.T) {
	app := testApp(t)
	profile := Profile{Name: "default", DNSView: "default"}
	now := time.Now()

	acquired, err := app.tryAcquireRecordRefreshLease(profile, "example.com", now, recordRefreshLeaseTTL)
	if err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	if !acquired {
		t.Fatalf("first lease was not acquired")
	}
	acquired, err = app.tryAcquireRecordRefreshLease(profile, "example.com", now.Add(time.Second), recordRefreshLeaseTTL)
	if err != nil {
		t.Fatalf("acquire duplicate lease: %v", err)
	}
	if acquired {
		t.Fatalf("duplicate lease was acquired")
	}
	acquired, err = app.tryAcquireRecordRefreshLease(profile, "other.example.com", now.Add(time.Second), recordRefreshLeaseTTL)
	if err != nil {
		t.Fatalf("acquire other zone lease: %v", err)
	}
	if !acquired {
		t.Fatalf("other zone lease was not acquired")
	}
	acquired, err = app.tryAcquireRecordRefreshLease(profile, "example.com", now.Add(recordRefreshLeaseTTL+time.Second), recordRefreshLeaseTTL)
	if err != nil {
		t.Fatalf("acquire expired lease: %v", err)
	}
	if !acquired {
		t.Fatalf("expired lease was not replaced")
	}
}

func TestWaitForActiveRecordRefreshTimesOut(t *testing.T) {
	app := testApp(t)
	profile := Profile{Name: "default", DNSView: "default"}
	acquired, err := app.tryAcquireRecordRefreshLease(profile, "example.com", time.Now(), recordRefreshLeaseTTL)
	if err != nil || !acquired {
		t.Fatalf("acquire lease = %v, %v", acquired, err)
	}

	start := time.Now()
	waited, err := app.waitForActiveRecordRefresh(profile, "example.com", 20*time.Millisecond, 2*time.Millisecond)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("wait for refresh: %v", err)
	}
	if waited {
		t.Fatalf("waited = true, want false for timeout")
	}
	if elapsed < 15*time.Millisecond {
		t.Fatalf("wait returned too early after %s", elapsed)
	}
}

func TestCachedRecordsWaitsForActiveRefreshAndUsesCache(t *testing.T) {
	var allRecordRequests int
	server := recordCacheServer(t, "2026050802", []map[string]any{{"type": "HOST_IPV4ADDR", "name": "live.example.com", "address": "192.0.2.20"}}, &allRecordRequests)
	defer server.Close()

	app := testApp(t)
	profile := Profile{Name: "default", DNSView: "default"}
	acquired, err := app.tryAcquireRecordRefreshLease(profile, "example.com", time.Now(), recordRefreshLeaseTTL)
	if err != nil || !acquired {
		t.Fatalf("acquire lease = %v, %v", acquired, err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(10 * time.Millisecond)
		_ = app.writeCachedRecords(profile, "example.com", "2026050802", []map[string]any{
			{"type": "HOST_IPV4ADDR", "name": "cached.example.com", "address": "192.0.2.10"},
		}, time.Now())
		_ = app.releaseRecordRefreshLease(profile, "example.com")
	}()

	records, source, err := app.cachedRecordsForZoneWithSource(profile, testWapiClient(server), "example.com")
	if err != nil {
		t.Fatalf("cached records: %v", err)
	}
	<-done
	if source != recordCacheSourceFreshCache {
		t.Fatalf("source = %q, want %q", source, recordCacheSourceFreshCache)
	}
	if allRecordRequests != 0 {
		t.Fatalf("allrecords requests = %d, want 0", allRecordRequests)
	}
	if len(records) != 1 || recordName(records[0].Item, records[0].Type) != "cached.example.com" {
		t.Fatalf("records = %#v", records)
	}
}

func TestCachedRecordsDoesNotWaitForOtherZoneRefresh(t *testing.T) {
	var allRecordRequests int
	server := recordCacheServer(t, "2026050802", []map[string]any{{"type": "HOST_IPV4ADDR", "name": "live.example.com", "address": "192.0.2.20"}}, &allRecordRequests)
	defer server.Close()

	app := testApp(t)
	profile := Profile{Name: "default", DNSView: "default"}
	acquired, err := app.tryAcquireRecordRefreshLease(profile, "other.example.com", time.Now(), recordRefreshLeaseTTL)
	if err != nil || !acquired {
		t.Fatalf("acquire other lease = %v, %v", acquired, err)
	}

	start := time.Now()
	_, source, err := app.cachedRecordsForZoneWithSource(profile, testWapiClient(server), "example.com")
	if err != nil {
		t.Fatalf("cached records: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("record load waited for unrelated refresh: %s", elapsed)
	}
	if source != recordCacheSourceAllRecords {
		t.Fatalf("source = %q, want %q", source, recordCacheSourceAllRecords)
	}
	if allRecordRequests != 1 {
		t.Fatalf("allrecords requests = %d, want 1", allRecordRequests)
	}
}

func TestRunRecordCacheRevalidateReleasesLeaseWhenSerialMatches(t *testing.T) {
	var allRecordRequests int
	server := recordCacheServer(t, "2026050801", []map[string]any{{"type": "HOST_IPV4ADDR", "name": "live.example.com", "address": "192.0.2.20"}}, &allRecordRequests)
	defer server.Close()

	app := testApp(t)
	profile := writeCompletionProfile(t, app, server.URL)
	now := time.Now()
	if err := app.writeCachedRecordsEntry(profile, "example.com", "2026050801", []map[string]any{{"type": "HOST_IPV4ADDR", "name": "cached.example.com", "address": "192.0.2.10"}}, now.Add(-time.Hour).Unix(), now.Add(time.Hour).Unix()); err != nil {
		t.Fatalf("write stale record cache: %v", err)
	}
	acquired, err := app.tryAcquireRecordRefreshLease(profile, "example.com", now, recordRefreshLeaseTTL)
	if err != nil || !acquired {
		t.Fatalf("acquire lease = %v, %v", acquired, err)
	}

	if err := app.runRecordCacheRevalidate(profile.Name, profile.DNSView, "example.com"); err != nil {
		t.Fatalf("run revalidate: %v", err)
	}
	if allRecordRequests != 0 {
		t.Fatalf("allrecords requests = %d, want 0", allRecordRequests)
	}
	acquired, err = app.tryAcquireRecordRefreshLease(profile, "example.com", time.Now(), recordRefreshLeaseTTL)
	if err != nil {
		t.Fatalf("reacquire released lease: %v", err)
	}
	if !acquired {
		t.Fatalf("lease was not released after same-serial revalidation")
	}
}

func TestRunRecordCacheRevalidateReleasesLeaseWhenSerialChanges(t *testing.T) {
	var allRecordRequests int
	server := recordCacheServer(t, "2026050802", []map[string]any{{"type": "HOST_IPV4ADDR", "name": "live.example.com", "address": "192.0.2.20"}}, &allRecordRequests)
	defer server.Close()

	app := testApp(t)
	profile := writeCompletionProfile(t, app, server.URL)
	now := time.Now()
	if err := app.writeCachedRecordsEntry(profile, "example.com", "2026050801", []map[string]any{{"type": "HOST_IPV4ADDR", "name": "cached.example.com", "address": "192.0.2.10"}}, now.Add(-time.Hour).Unix(), now.Add(time.Hour).Unix()); err != nil {
		t.Fatalf("write stale record cache: %v", err)
	}
	acquired, err := app.tryAcquireRecordRefreshLease(profile, "example.com", now, recordRefreshLeaseTTL)
	if err != nil || !acquired {
		t.Fatalf("acquire lease = %v, %v", acquired, err)
	}

	if err := app.runRecordCacheRevalidate(profile.Name, profile.DNSView, "example.com"); err != nil {
		t.Fatalf("run revalidate: %v", err)
	}
	if allRecordRequests != 1 {
		t.Fatalf("allrecords requests = %d, want 1", allRecordRequests)
	}
	acquired, err = app.tryAcquireRecordRefreshLease(profile, "example.com", time.Now(), recordRefreshLeaseTTL)
	if err != nil {
		t.Fatalf("reacquire released lease: %v", err)
	}
	if !acquired {
		t.Fatalf("lease was not released after changed-serial revalidation")
	}
}

func TestRunRecordCacheRevalidateReleasesLeaseOnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "zone serial failed", http.StatusInternalServerError)
	}))
	defer server.Close()

	app := testApp(t)
	profile := writeCompletionProfile(t, app, server.URL)
	now := time.Now()
	if err := app.writeCachedRecordsEntry(profile, "example.com", "2026050801", []map[string]any{{"type": "HOST_IPV4ADDR", "name": "cached.example.com", "address": "192.0.2.10"}}, now.Add(-time.Hour).Unix(), now.Add(time.Hour).Unix()); err != nil {
		t.Fatalf("write stale record cache: %v", err)
	}
	acquired, err := app.tryAcquireRecordRefreshLease(profile, "example.com", now, recordRefreshLeaseTTL)
	if err != nil || !acquired {
		t.Fatalf("acquire lease = %v, %v", acquired, err)
	}

	if err := app.runRecordCacheRevalidate(profile.Name, profile.DNSView, "example.com"); err == nil {
		t.Fatalf("run revalidate error = nil, want error")
	}
	acquired, err = app.tryAcquireRecordRefreshLease(profile, "example.com", time.Now(), recordRefreshLeaseTTL)
	if err != nil {
		t.Fatalf("reacquire released lease: %v", err)
	}
	if !acquired {
		t.Fatalf("lease was not released after revalidation error")
	}
}

func TestCachedRecordsForZoneExtendsExpiredCacheWhenSerialMatches(t *testing.T) {
	var allRecordRequests int
	server := recordCacheServer(t, "2026050801", []map[string]any{{"type": "HOST_IPV4ADDR", "name": "live.example.com", "address": "192.0.2.20"}}, &allRecordRequests)
	defer server.Close()

	app := testApp(t)
	profile := Profile{Name: "default", DNSView: "default"}
	before := time.Now()
	if err := app.writeCachedRecordsEntry(profile, "example.com", "2026050801", []map[string]any{{"type": "HOST_IPV4ADDR", "name": "cached.example.com", "address": "192.0.2.10"}}, before.Add(-time.Hour).Unix(), before.Add(-time.Second).Unix()); err != nil {
		t.Fatalf("write expired record cache: %v", err)
	}

	records, err := app.cachedRecordsForZone(profile, testWapiClient(server), "example.com")
	if err != nil {
		t.Fatalf("cached records: %v", err)
	}
	if allRecordRequests != 0 {
		t.Fatalf("allrecords requests = %d, want 0 for unchanged serial", allRecordRequests)
	}
	if len(records) != 1 || recordName(records[0].Item, records[0].Type) != "cached.example.com" {
		t.Fatalf("records = %#v", records)
	}
	entry, err := app.readCachedRecords(profile, "example.com")
	if err != nil {
		t.Fatalf("read record cache: %v", err)
	}
	minCachedAt := before.Add(-2 * time.Second).Unix()
	maxCachedAt := time.Now().Add(2 * time.Second).Unix()
	if entry.CachedAt < minCachedAt || entry.CachedAt > maxCachedAt {
		t.Fatalf("renewed cached_at = %d, want between %d and %d", entry.CachedAt, minCachedAt, maxCachedAt)
	}
	if !app.cacheEntryFresh(entry, time.Now()) {
		t.Fatalf("renewed cache should be fresh from computed cached_at + cache_ttl")
	}
	minStaleExpires := before.Add(app.recordsCacheSWRTTL() - 2*time.Second).Unix()
	maxStaleExpires := time.Now().Add(app.recordsCacheSWRTTL() + 2*time.Second).Unix()
	if entry.StaleExpiresAt < minStaleExpires || entry.StaleExpiresAt > maxStaleExpires {
		t.Fatalf("renewed stale_expires_at = %d, want between %d and %d", entry.StaleExpiresAt, minStaleExpires, maxStaleExpires)
	}
}

func TestRecordCacheRevalidateRenewsSameSerial(t *testing.T) {
	var allRecordRequests int
	server := recordCacheServer(t, "2026050801", []map[string]any{{"type": "HOST_IPV4ADDR", "name": "live.example.com", "address": "192.0.2.20"}}, &allRecordRequests)
	defer server.Close()

	app := testApp(t)
	profile := Profile{Name: "default", DNSView: "default"}
	before := time.Now()
	if err := app.writeCachedRecordsEntry(profile, "example.com", "2026050801", []map[string]any{{"type": "HOST_IPV4ADDR", "name": "cached.example.com", "address": "192.0.2.10"}}, before.Add(-time.Hour).Unix(), before.Add(time.Hour).Unix()); err != nil {
		t.Fatalf("write stale record cache: %v", err)
	}

	if err := app.revalidateRecordCache(profile, testWapiClient(server), "example.com"); err != nil {
		t.Fatalf("revalidate record cache: %v", err)
	}
	if allRecordRequests != 0 {
		t.Fatalf("allrecords requests = %d, want 0 for unchanged serial", allRecordRequests)
	}
	entry, err := app.readCachedRecords(profile, "example.com")
	if err != nil {
		t.Fatalf("read revalidated cache: %v", err)
	}
	if entry.Serial != "2026050801" {
		t.Fatalf("serial = %q, want 2026050801", entry.Serial)
	}
	if !app.cacheEntryFresh(entry, time.Now()) {
		t.Fatalf("revalidated cache should be fresh from computed cached_at + cache_ttl")
	}
	if entry.StaleExpiresAt <= before.Add(app.recordsCacheSWRTTL()-2*time.Second).Unix() {
		t.Fatalf("stale_expires_at was not extended: %d", entry.StaleExpiresAt)
	}
}

func TestRecordCacheRevalidateRefreshesChangedSerial(t *testing.T) {
	var allRecordRequests int
	server := recordCacheServer(t, "2026050802", []map[string]any{{"type": "HOST_IPV4ADDR", "name": "live.example.com", "address": "192.0.2.20"}}, &allRecordRequests)
	defer server.Close()

	app := testApp(t)
	profile := Profile{Name: "default", DNSView: "default"}
	before := time.Now()
	if err := app.writeCachedRecordsEntry(profile, "example.com", "2026050801", []map[string]any{{"type": "HOST_IPV4ADDR", "name": "cached.example.com", "address": "192.0.2.10"}}, before.Add(-time.Hour).Unix(), before.Add(time.Hour).Unix()); err != nil {
		t.Fatalf("write stale record cache: %v", err)
	}

	if err := app.revalidateRecordCache(profile, testWapiClient(server), "example.com"); err != nil {
		t.Fatalf("revalidate record cache: %v", err)
	}
	if allRecordRequests != 1 {
		t.Fatalf("allrecords requests = %d, want 1", allRecordRequests)
	}
	entry, err := app.readCachedRecords(profile, "example.com")
	if err != nil {
		t.Fatalf("read refreshed cache: %v", err)
	}
	if entry.Serial != "2026050802" {
		t.Fatalf("serial = %q, want 2026050802", entry.Serial)
	}
	records := recordsFromAllRecordRows(entry.Rows)
	if len(records) != 1 || recordName(records[0].Item, records[0].Type) != "live.example.com" {
		t.Fatalf("records = %#v", records)
	}
}

func TestRecordCacheRevalidatePopulatesMissingCache(t *testing.T) {
	var allRecordRequests int
	server := recordCacheServer(t, "2026050802", []map[string]any{{"type": "HOST_IPV4ADDR", "name": "live.example.com", "address": "192.0.2.20"}}, &allRecordRequests)
	defer server.Close()

	app := testApp(t)
	profile := Profile{Name: "default", DNSView: "default"}

	if err := app.revalidateRecordCache(profile, testWapiClient(server), "example.com"); err != nil {
		t.Fatalf("revalidate missing record cache: %v", err)
	}
	if allRecordRequests != 1 {
		t.Fatalf("allrecords requests = %d, want 1", allRecordRequests)
	}
	entry, err := app.readCachedRecords(profile, "example.com")
	if err != nil {
		t.Fatalf("read refreshed cache: %v", err)
	}
	if !entry.CacheFound {
		t.Fatalf("record cache was not populated")
	}
	if entry.Serial != "2026050802" {
		t.Fatalf("serial = %q, want 2026050802", entry.Serial)
	}
	records := recordsFromAllRecordRows(entry.Rows)
	if len(records) != 1 || recordName(records[0].Item, records[0].Type) != "live.example.com" {
		t.Fatalf("records = %#v", records)
	}
}

func TestRecordCacheRefreshAfterWriteSkipsDuplicateLease(t *testing.T) {
	app := testApp(t)
	profile := Profile{Name: "default", DNSView: "default"}
	refreshes := make(chan string, 2)
	app.backgroundRecordRevalidator = func(profile Profile, zone string) error {
		refreshes <- zone
		return nil
	}

	app.queueRecordCacheRefreshAfterWrite(profile, "example.com")
	app.queueRecordCacheRefreshAfterWrite(profile, "example.com")

	assertRecordRefreshQueued(t, refreshes, "example.com")
	assertNoRecordRefreshQueued(t, refreshes)
}

func TestCachedRecordsForZoneEnrichesTTLFromRecordRef(t *testing.T) {
	var allRecordRequests int
	var detailRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/zone_auth"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{
					{"fqdn": r.URL.Query().Get("fqdn"), "view": "default", "zone_format": "FORWARD", "soa_serial_number": "2026050801"},
				},
			})
		case strings.HasSuffix(r.URL.Path, "/allrecords"):
			allRecordRequests++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{
					{
						"type":    "record:a",
						"name":    "app",
						"address": "192.0.2.10",
						"zone":    "example.com",
						"record": map[string]any{
							"_ref":     "record:a/ref",
							"name":     "app.example.com",
							"ipv4addr": "192.0.2.10",
						},
					},
				},
			})
		case strings.HasSuffix(r.URL.Path, "/record:a/ref"):
			detailRequests++
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
	defer server.Close()

	app := testApp(t)
	profile := Profile{Name: "default", DNSView: "default"}
	client := testWapiClient(server)
	records, err := app.cachedRecordsForZoneWithDetails(profile, client, "example.com", true)
	if err != nil {
		t.Fatalf("cached records: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records = %#v", records)
	}
	row := recordOutputRow(records[0].Type, records[0].Item)
	if row["ttl"] != "300" {
		t.Fatalf("ttl = %#v, want 300", row["ttl"])
	}
	if allRecordRequests != 1 || detailRequests != 1 {
		t.Fatalf("requests allrecords=%d detail=%d, want 1 each", allRecordRequests, detailRequests)
	}

	records, err = app.cachedRecordsForZoneWithDetails(profile, client, "example.com", true)
	if err != nil {
		t.Fatalf("cached records second call: %v", err)
	}
	row = recordOutputRow(records[0].Type, records[0].Item)
	if row["ttl"] != "300" {
		t.Fatalf("cached ttl = %#v, want 300", row["ttl"])
	}
	if allRecordRequests != 1 || detailRequests != 1 {
		t.Fatalf("cache was not reused: allrecords=%d detail=%d", allRecordRequests, detailRequests)
	}
}

func TestCachedRecordsForZoneCanEnrichFastCachedResult(t *testing.T) {
	var allRecordRequests int
	var detailRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/zone_auth"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{
					{"fqdn": r.URL.Query().Get("fqdn"), "view": "default", "zone_format": "FORWARD", "soa_serial_number": "2026050801"},
				},
			})
		case strings.HasSuffix(r.URL.Path, "/allrecords"):
			allRecordRequests++
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
			detailRequests++
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
	defer server.Close()

	app := testApp(t)
	profile := Profile{Name: "default", DNSView: "default"}
	client := testWapiClient(server)
	records, err := app.cachedRecordsForZoneWithDetails(profile, client, "example.com", false)
	if err != nil {
		t.Fatalf("fast cached records: %v", err)
	}
	if allRecordRequests != 1 || detailRequests != 0 {
		t.Fatalf("fast requests allrecords=%d detail=%d, want 1 and 0", allRecordRequests, detailRequests)
	}
	row := recordOutputRow(records[0].Type, records[0].Item)
	if row["ttl"] != "" {
		t.Fatalf("fast ttl = %#v, want empty", row["ttl"])
	}

	records, err = app.cachedRecordsForZoneWithDetails(profile, client, "example.com", true)
	if err != nil {
		t.Fatalf("enriched cached records: %v", err)
	}
	if allRecordRequests != 1 || detailRequests != 1 {
		t.Fatalf("enrich requests allrecords=%d detail=%d, want 1 and 1", allRecordRequests, detailRequests)
	}
	row = recordOutputRow(records[0].Type, records[0].Item)
	if row["ttl"] != "300" {
		t.Fatalf("enriched ttl = %#v, want 300", row["ttl"])
	}

	_, err = app.cachedRecordsForZoneWithDetails(profile, client, "example.com", true)
	if err != nil {
		t.Fatalf("reused enriched records: %v", err)
	}
	if allRecordRequests != 1 || detailRequests != 1 {
		t.Fatalf("reused enriched cache made requests allrecords=%d detail=%d", allRecordRequests, detailRequests)
	}
}

func TestCachedRecordsForZoneRefreshesChangedSerial(t *testing.T) {
	var allRecordRequests int
	server := recordCacheServer(t, "2026050802", []map[string]any{{"type": "HOST_IPV4ADDR", "name": "live.example.com", "address": "192.0.2.20"}}, &allRecordRequests)
	defer server.Close()

	app := testApp(t)
	profile := Profile{Name: "default", DNSView: "default"}
	now := time.Now()
	if err := app.writeCachedRecordsEntry(profile, "example.com", "2026050801", []map[string]any{{"type": "HOST_IPV4ADDR", "name": "cached.example.com", "address": "192.0.2.10"}}, now.Add(-time.Hour).Unix(), now.Add(-time.Second).Unix()); err != nil {
		t.Fatalf("write record cache: %v", err)
	}

	records, err := app.cachedRecordsForZone(profile, testWapiClient(server), "example.com")
	if err != nil {
		t.Fatalf("cached records: %v", err)
	}
	if allRecordRequests != 1 {
		t.Fatalf("allrecords requests = %d", allRecordRequests)
	}
	if len(records) != 1 || recordName(records[0].Item, records[0].Type) != "live.example.com" {
		t.Fatalf("records = %#v", records)
	}
	entry, err := app.readCachedRecords(profile, "example.com")
	if err != nil {
		t.Fatalf("read refreshed cache: %v", err)
	}
	if entry.Serial != "2026050802" {
		t.Fatalf("refreshed serial = %q, want 2026050802", entry.Serial)
	}
}

func TestCachedRecordsForZoneFallsBackToTTLWithoutSerial(t *testing.T) {
	var allRecordRequests int
	server := recordCacheServer(t, "", []map[string]any{{"type": "HOST_IPV4ADDR", "name": "live.example.com", "address": "192.0.2.20"}}, &allRecordRequests)
	defer server.Close()

	app := testApp(t)
	profile := Profile{Name: "default", DNSView: "default"}
	if err := app.writeCachedRecords(profile, "example.com", "", []map[string]any{{"type": "HOST_IPV4ADDR", "name": "cached.example.com", "address": "192.0.2.10"}}, time.Now()); err != nil {
		t.Fatalf("write record cache: %v", err)
	}

	records, err := app.cachedRecordsForZone(profile, testWapiClient(server), "example.com")
	if err != nil {
		t.Fatalf("cached records: %v", err)
	}
	if allRecordRequests != 0 {
		t.Fatalf("allrecords requests = %d", allRecordRequests)
	}
	if len(records) != 1 || recordName(records[0].Item, records[0].Type) != "cached.example.com" {
		t.Fatalf("records = %#v", records)
	}
}

func TestCachedRecordsForZoneFetchesExpiredCacheWithoutSerial(t *testing.T) {
	var allRecordRequests int
	server := recordCacheServer(t, "", []map[string]any{{"type": "HOST_IPV4ADDR", "name": "live.example.com", "address": "192.0.2.20"}}, &allRecordRequests)
	defer server.Close()

	app := testApp(t)
	profile := Profile{Name: "default", DNSView: "default"}
	now := time.Now()
	if err := app.writeCachedRecordsEntry(profile, "example.com", "", []map[string]any{{"type": "HOST_IPV4ADDR", "name": "cached.example.com", "address": "192.0.2.10"}}, now.Add(-time.Hour).Unix(), now.Add(-time.Second).Unix()); err != nil {
		t.Fatalf("write expired record cache: %v", err)
	}

	records, err := app.cachedRecordsForZone(profile, testWapiClient(server), "example.com")
	if err != nil {
		t.Fatalf("cached records: %v", err)
	}
	if allRecordRequests != 1 {
		t.Fatalf("allrecords requests = %d, want 1 for expired cache without serial", allRecordRequests)
	}
	if len(records) != 1 || recordName(records[0].Item, records[0].Type) != "live.example.com" {
		t.Fatalf("records = %#v", records)
	}
}

func recordCacheServer(t *testing.T, serial string, liveRows []map[string]any, allRecordRequests *int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/zone_auth"):
			zone := map[string]any{"fqdn": r.URL.Query().Get("fqdn"), "view": "default", "zone_format": "FORWARD"}
			if serial != "" {
				zone["soa_serial_number"] = serial
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{zone}})
		case strings.HasSuffix(r.URL.Path, "/allrecords"):
			*allRecordRequests++
			_ = json.NewEncoder(w).Encode(map[string]any{"result": liveRows})
		default:
			http.NotFound(w, r)
		}
	}))
}

func testWapiClient(server *httptest.Server) *WapiClient {
	return &WapiClient{
		Server:      server.URL,
		ReadServer:  server.URL,
		WAPIVersion: defaultWAPIVersion,
		View:        "default",
		httpClient:  server.Client(),
	}
}
