package ibcli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	if err := app.writeCachedDHCPReservations(profile, "default", []map[string]any{{"ipv4addr": "192.0.2.25", "network_view": "default"}}, time.Now()); err != nil {
		t.Fatalf("write DHCP reservation cache: %v", err)
	}

	rows, err := app.cacheStatusRows()
	if err != nil {
		t.Fatalf("cache status: %v", err)
	}
	if len(rows) != 7 {
		t.Fatalf("status rows = %#v", rows)
	}
	var foundDHCP bool
	for _, row := range rows {
		if row["kind"] == dhcpReservationCacheKind {
			foundDHCP = true
		}
	}
	if !foundDHCP {
		t.Fatalf("status rows missing DHCP reservation cache: %#v", rows)
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
	matches, err := filepath.Glob(filepath.Join(app.cachePath(), "*.vlog"))
	if err != nil {
		t.Fatalf("glob vlog files: %v", err)
	}
	for _, match := range matches {
		info, err := os.Stat(match)
		if err != nil {
			t.Fatalf("stat vlog file: %v", err)
		}
		if info.Size() > cacheValueLogFileSize*2 {
			t.Fatalf("cache clear left large vlog %s at %d bytes", match, info.Size())
		}
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
	if options.ValueLogFileSize != cacheValueLogFileSize {
		t.Fatalf("value log file size = %d, want %d", options.ValueLogFileSize, cacheValueLogFileSize)
	}
	if options.Logger != nil {
		t.Fatal("cache Badger logger should be disabled")
	}
}

func TestCacheScopeDoesNotReuseRowsAcrossServers(t *testing.T) {
	app := testApp(t)
	first := Profile{Name: "default", Server: "https://first.example", Username: "first-admin", DNSView: "default"}
	second := Profile{Name: "default", Server: "https://second.example", DNSView: "default"}
	if err := app.writeCachedRecords(first, "example.com", "1", []map[string]any{{"name": "first.example.com"}}, time.Now()); err != nil {
		t.Fatalf("write first cache: %v", err)
	}
	entry, err := app.readCachedRecords(first, "example.com")
	if err != nil {
		t.Fatalf("read first cache: %v", err)
	}
	if !entry.CacheFound || len(entry.Rows) != 1 || entry.Rows[0]["name"] != "first.example.com" {
		t.Fatalf("first server cache = %#v, want its cached row", entry)
	}
	entry, err = app.readCachedRecords(second, "example.com")
	if err != nil {
		t.Fatalf("read second cache: %v", err)
	}
	if entry.CacheFound {
		t.Fatalf("second server read first server cache: %#v", entry)
	}
	second = first
	second.Username = "second-admin"
	entry, err = app.readCachedRecords(second, "example.com")
	if err != nil {
		t.Fatalf("read second user cache: %v", err)
	}
	if entry.CacheFound {
		t.Fatalf("second user read first user cache: %#v", entry)
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

func TestRunRecordCacheRevalidateReleasesBadgerLockDuringWAPI(t *testing.T) {
	started := make(chan struct{})
	continueRequest := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/zone_auth"):
			close(started)
			<-continueRequest
			_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{{
				"fqdn":              "example.com",
				"view":              "default",
				"soa_serial_number": "2026050802",
			}}})
		case strings.HasSuffix(r.URL.Path, "/allrecords"):
			_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{{
				"type":    "HOST_IPV4ADDR",
				"name":    "live.example.com",
				"address": "192.0.2.20",
			}}})
		default:
			http.NotFound(w, r)
		}
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
	if err := app.closeCacheDB(app.cachePath()); err != nil {
		t.Fatalf("close seeded cache DB: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- app.runRecordCacheRevalidate(profile.Name, profile.DNSView, "example.com")
	}()
	<-started

	probe, err := badger.Open(cacheBadgerOptions(app.cachePath()))
	if err != nil {
		t.Fatalf("Badger remained locked during WAPI request: %v", err)
	}
	if err := probe.Close(); err != nil {
		t.Fatalf("close Badger probe: %v", err)
	}
	close(continueRequest)
	if err := <-done; err != nil {
		t.Fatalf("run revalidate: %v", err)
	}
}

func TestRunRecordCacheRevalidateBatchKeepsBadgerUnlockedUntilAllWAPICompletes(t *testing.T) {
	started := make(chan string, 2)
	oneContinue := make(chan struct{})
	twoContinue := make(chan struct{})
	oneAllRecords := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		zone := r.URL.Query().Get("fqdn")
		switch {
		case strings.HasSuffix(r.URL.Path, "/zone_auth"):
			started <- zone
			if zone == "one.example.com" {
				<-oneContinue
			} else {
				<-twoContinue
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{{
				"fqdn":              zone,
				"view":              "default",
				"soa_serial_number": "2026050802",
			}}})
		case strings.HasSuffix(r.URL.Path, "/allrecords"):
			if r.URL.Query().Get("zone") == "one.example.com" {
				oneAllRecords <- struct{}{}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{{
				"type":    "HOST_IPV4ADDR",
				"name":    "live." + r.URL.Query().Get("zone"),
				"address": "192.0.2.20",
			}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	app := testApp(t)
	profile := writeCompletionProfile(t, app, server.URL)
	now := time.Now()
	for _, zoneName := range []string{"one.example.com", "two.example.com"} {
		if err := app.writeCachedRecordsEntry(profile, zoneName, "2026050801", []map[string]any{{"type": "HOST_IPV4ADDR", "name": "cached." + zoneName, "address": "192.0.2.10"}}, now.Add(-time.Hour).Unix(), now.Add(time.Hour).Unix()); err != nil {
			t.Fatalf("write stale record cache for %s: %v", zoneName, err)
		}
		acquired, err := app.tryAcquireRecordRefreshLease(profile, zoneName, now, recordRefreshLeaseTTL)
		if err != nil || !acquired {
			t.Fatalf("acquire lease for %s = %v, %v", zoneName, acquired, err)
		}
	}
	if err := app.closeCacheDB(app.cachePath()); err != nil {
		t.Fatalf("close seeded cache DB: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- app.runRecordCacheRevalidateBatch(profile.Name, profile.DNSView, []string{"one.example.com", "two.example.com"})
	}()
	<-started
	<-started
	assertBadgerCacheUnlocked(t, app)

	close(oneContinue)
	<-oneAllRecords
	assertBadgerCacheUnlocked(t, app)

	close(twoContinue)
	if err := <-done; err != nil {
		t.Fatalf("run batch revalidate: %v", err)
	}
}

func TestRecordCacheRevalidationPlanDoesNotOverwriteForegroundCacheChange(t *testing.T) {
	app := testApp(t)
	profile := Profile{Name: defaultProfileName, DNSView: "default"}
	now := time.Now()
	if err := app.writeCachedRecords(profile, "example.com", "2026050801", []map[string]any{{"type": "HOST_IPV4ADDR", "name": "cached.example.com", "address": "192.0.2.10"}}, now); err != nil {
		t.Fatalf("write cached records: %v", err)
	}
	snapshot, err := app.readCachedRecords(profile, "example.com")
	if err != nil {
		t.Fatalf("read cache snapshot: %v", err)
	}
	if err := app.writeCachedRecords(profile, "example.com", "2026050802", []map[string]any{{"type": "HOST_IPV4ADDR", "name": "foreground.example.com", "address": "192.0.2.20"}}, now.Add(time.Second)); err != nil {
		t.Fatalf("write foreground cache change: %v", err)
	}

	plan := recordCacheRevalidationPlan{
		zoneName:    "example.com",
		snapshot:    snapshot,
		refreshedAt: now.Add(2 * time.Second),
		serial:      "2026050803",
		rows:        []map[string]any{{"type": "HOST_IPV4ADDR", "name": "background.example.com", "address": "192.0.2.30"}},
	}
	if err := app.applyRecordCacheRevalidationPlan(profile, plan); err != nil {
		t.Fatalf("apply revalidation plan: %v", err)
	}
	entry, err := app.readCachedRecords(profile, "example.com")
	if err != nil {
		t.Fatalf("read cached records: %v", err)
	}
	if entry.Serial != "2026050802" || len(entry.Rows) != 1 || cleanString(entry.Rows[0]["name"]) != "foreground.example.com" {
		t.Fatalf("foreground cache change was overwritten: %#v", entry)
	}
}

func assertBadgerCacheUnlocked(t *testing.T, app *App) {
	t.Helper()
	probe, err := badger.Open(cacheBadgerOptions(app.cachePath()))
	if err != nil {
		t.Fatalf("Badger remained locked during WAPI request: %v", err)
	}
	if err := probe.Close(); err != nil {
		t.Fatalf("close Badger probe: %v", err)
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

func TestDHCPReservationCacheFreshStaleExpiredAndForceRefresh(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/fixedaddress") {
			t.Fatalf("WAPI request = %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{{"ipv4addr": fmt.Sprintf("192.0.2.%d", 20+requests), "mac": "00:11:22:33:44:55", "network_view": "default"}}})
	}))
	defer server.Close()

	app := testApp(t)
	writeConfigForSettings(t, app, ConfigSettings{CacheTTLSeconds: 60, DNSSearchWorkerLimit: defaultDNSSearchWorkerLimit, RecordsCacheSWRSeconds: 120})
	profile := Profile{Name: "default", Server: server.URL, DNSView: "default"}
	client := testWapiClient(server)
	now := time.Now()
	if err := app.writeCachedDHCPReservations(profile, "default", []map[string]any{{"ipv4addr": "192.0.2.10", "network_view": "default"}}, now); err != nil {
		t.Fatalf("write fresh DHCP cache: %v", err)
	}
	if _, err := app.cachedDHCPReservations(profile, client, "default", false); err != nil {
		t.Fatalf("fresh DHCP cache: %v", err)
	}
	if requests != 0 {
		t.Fatalf("fresh cache WAPI requests = %d, want 0", requests)
	}

	refreshes := make(chan string, 4)
	app.backgroundNetRefresher = func(_ Profile, kind, networkView, ip string) error {
		refreshes <- kind + ":" + networkView + ":" + ip
		return nil
	}
	if err := app.writeCachedDHCPReservations(profile, "default", []map[string]any{{"ipv4addr": "192.0.2.11", "network_view": "default"}}, now.Add(-90*time.Second)); err != nil {
		t.Fatalf("write stale DHCP cache: %v", err)
	}
	if _, err := app.cachedDHCPReservations(profile, client, "default", false); err != nil {
		t.Fatalf("stale DHCP cache: %v", err)
	}
	if _, err := app.cachedDHCPReservations(profile, client, "default", false); err != nil {
		t.Fatalf("second stale DHCP cache: %v", err)
	}
	select {
	case got := <-refreshes:
		if got != dhcpReservationCacheKind+":default:" {
			t.Fatalf("refresh scope = %q", got)
		}
	default:
		t.Fatal("stale cache did not queue refresh")
	}
	select {
	case got := <-refreshes:
		t.Fatalf("duplicate stale refresh = %q", got)
	default:
	}

	app.backgroundNetRefresher = nil
	if _, err := app.cachedDHCPReservations(profile, client, "default", true); err != nil {
		t.Fatalf("forced DHCP refresh: %v", err)
	}
	if requests != 1 {
		t.Fatalf("forced refresh requests = %d, want 1", requests)
	}
	if err := app.writeCachedDHCPReservations(profile, "default", []map[string]any{{"ipv4addr": "192.0.2.12", "network_view": "default"}}, now.Add(-300*time.Second)); err != nil {
		t.Fatalf("write expired DHCP cache: %v", err)
	}
	if _, err := app.cachedDHCPReservations(profile, client, "default", false); err != nil {
		t.Fatalf("expired DHCP cache: %v", err)
	}
	if requests != 2 {
		t.Fatalf("expired refresh requests = %d, want 2", requests)
	}
	if err := app.clearCache(); err != nil {
		t.Fatalf("clear DHCP cache before miss: %v", err)
	}
	if _, err := app.cachedDHCPReservations(profile, client, "default", false); err != nil {
		t.Fatalf("missing DHCP cache: %v", err)
	}
	if requests != 3 {
		t.Fatalf("missing refresh requests = %d, want 3", requests)
	}
}

func TestDHCPReservationCacheInvalidationAndRefreshLease(t *testing.T) {
	app := testApp(t)
	profile := Profile{Name: "default", Server: "https://grid.example", DNSView: "default"}
	now := time.Now()
	if err := app.writeCachedDHCPReservations(profile, "default", []map[string]any{{"ipv4addr": "192.0.2.10"}}, now); err != nil {
		t.Fatalf("write scoped DHCP cache: %v", err)
	}
	if err := app.writeCachedDHCPReservations(profile, "", []map[string]any{{"ipv4addr": "192.0.2.11"}}, now); err != nil {
		t.Fatalf("write unscoped DHCP cache: %v", err)
	}
	refreshes := make(chan string, 4)
	app.backgroundNetRefresher = func(_ Profile, kind, networkView, ip string) error {
		refreshes <- kind + ":" + networkView + ":" + ip
		return nil
	}
	if err := app.queueDHCPReservationCacheRefresh(profile, "default"); err != nil {
		t.Fatalf("queue DHCP refresh: %v", err)
	}
	oldToken, err := app.netRefreshLeaseToken(profile, dhcpReservationCacheKind, "default", "")
	if err != nil || oldToken == 0 {
		t.Fatalf("read initial refresh token: token=%d err=%v", oldToken, err)
	}
	for _, want := range []string{dhcpReservationCacheKind + ":default:", dhcpReservationCacheKind + "::"} {
		select {
		case got := <-refreshes:
			if got != want {
				t.Fatalf("refresh scope = %q, want %q", got, want)
			}
		default:
			t.Fatalf("missing refresh %q", want)
		}
	}
	if entry, err := app.readCachedDHCPReservations(profile, "default"); err != nil || entry.CacheFound {
		t.Fatalf("scoped cache after invalidation = %#v, err=%v", entry, err)
	}
	if entry, err := app.readCachedDHCPReservations(profile, ""); err != nil || entry.CacheFound {
		t.Fatalf("unscoped cache after invalidation = %#v, err=%v", entry, err)
	}
	if err := app.queueDHCPReservationCacheRefresh(profile, "default"); err != nil {
		t.Fatalf("queue second DHCP refresh: %v", err)
	}
	for range []string{"default", ""} {
		select {
		case <-refreshes:
		default:
			t.Fatal("missing replacement refresh")
		}
	}
	if err := app.releaseNetRefreshLeaseIfOwner(cacheProfileName(profile), dhcpReservationCacheKind, "default", "", oldToken); err != nil {
		t.Fatalf("release old refresh lease: %v", err)
	}
	active, err := app.netRefreshLeaseActive(profile, dhcpReservationCacheKind, "default", "", time.Now())
	if err != nil {
		t.Fatalf("check replacement refresh lease: %v", err)
	}
	if !active {
		t.Fatal("old refresh released replacement DHCP lease")
	}
	acquired, err := app.tryAcquireNetRefreshLease(profile, dhcpReservationCacheKind, "default", "", time.Now(), recordRefreshLeaseTTL)
	if err != nil {
		t.Fatalf("check refresh lease: %v", err)
	}
	if acquired {
		t.Fatal("refresh lease was reacquired while background refresh was active")
	}
}

func TestDHCPReservationCreateWithoutViewFencesDefaultAndUnscopedCaches(t *testing.T) {
	app := testApp(t)
	profile := Profile{Name: "default", Server: "https://grid.example"}
	now := time.Now()
	if err := app.writeCachedDHCPReservations(profile, "default", []map[string]any{{"ipv4addr": "192.0.2.10"}}, now); err != nil {
		t.Fatalf("write default cache: %v", err)
	}
	if err := app.writeCachedDHCPReservations(profile, "", []map[string]any{{"ipv4addr": "192.0.2.11"}}, now); err != nil {
		t.Fatalf("write unscoped cache: %v", err)
	}
	var views []string
	app.backgroundNetRefresher = func(_ Profile, _ string, networkView, _ string) error {
		views = append(views, networkView)
		return nil
	}
	if err := app.queueDHCPReservationCacheRefresh(profile, ""); err != nil {
		t.Fatalf("queue omitted-view refresh: %v", err)
	}
	if len(views) != 2 || views[0] != "default" || views[1] != "" {
		t.Fatalf("refresh views = %#v, want default and unscoped", views)
	}
	for _, view := range []string{"default", ""} {
		entry, err := app.readCachedDHCPReservations(profile, view)
		if err != nil {
			t.Fatalf("read invalidated view %q: %v", view, err)
		}
		if entry.CacheFound {
			t.Fatalf("cache view %q survived omitted-view invalidation", view)
		}
	}
}

func TestDHCPReservationCacheDoesNotFallbackAcrossEndpointIdentity(t *testing.T) {
	app := testApp(t)
	unknown := Profile{Name: "default"}
	configured := Profile{Name: "default", Server: "https://grid.example", Username: "admin"}
	if err := app.writeCachedDHCPReservations(unknown, "default", []map[string]any{{"ipv4addr": "192.0.2.10"}}, time.Now()); err != nil {
		t.Fatalf("write unknown cache: %v", err)
	}
	entry, err := app.readCachedDHCPReservations(configured, "default")
	if err != nil {
		t.Fatalf("read configured cache: %v", err)
	}
	if entry.CacheFound {
		t.Fatalf("configured profile reused endpoint-less cache: %#v", entry)
	}
}

func TestDHCPReservationCacheSWRStartsAfterFreshTTL(t *testing.T) {
	app := testApp(t)
	profile := Profile{Name: "default", Server: "https://grid.example"}
	settings := defaultConfigSettings()
	settings.CacheTTLSeconds = 60
	settings.RecordsCacheSWRSeconds = 120
	writeConfigForSettings(t, app, settings)
	cachedAt := time.Now().Add(-150 * time.Second)
	if err := app.writeCachedDHCPReservations(profile, "default", []map[string]any{{"ipv4addr": "192.0.2.10"}}, cachedAt); err != nil {
		t.Fatalf("write DHCP cache: %v", err)
	}
	entry, err := app.readCachedDHCPReservations(profile, "default")
	if err != nil {
		t.Fatalf("read DHCP cache: %v", err)
	}
	want := cachedAt.Add(60 * time.Second).Add(120 * time.Second).Unix()
	if entry.StaleExpiresAt != want {
		t.Fatalf("stale expiry = %d, want fresh TTL plus SWR at %d", entry.StaleExpiresAt, want)
	}
	if entry.StaleExpiresAt <= time.Now().Unix() {
		t.Fatal("cache should still be inside SWR after 150 seconds")
	}
}

func TestDHCPReservationCacheClearFencesInFlightRefresh(t *testing.T) {
	app := testApp(t)
	profile := Profile{Name: "default", Server: "https://grid.example"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{{"ipv4addr": "192.0.2.99", "network_view": "default"}}})
	}))
	defer server.Close()
	token, err := app.readDHCPReservationCacheToken(profile, "default")
	if err != nil {
		t.Fatalf("capture cache token: %v", err)
	}
	if err := app.clearCache(); err != nil {
		t.Fatalf("clear cache: %v", err)
	}
	wrote, err := app.writeDHCPReservationCacheIfCurrent(profile, "default", []map[string]any{{"ipv4addr": "192.0.2.99"}}, time.Now(), token)
	if err != nil {
		t.Fatalf("guarded refresh write: %v", err)
	}
	if wrote {
		t.Fatal("stale refresh wrote after invalidation")
	}
	entry, err := app.readCachedDHCPReservations(profile, "default")
	if err != nil {
		t.Fatalf("read cleared cache: %v", err)
	}
	if entry.CacheFound {
		t.Fatalf("refresh resurrected cache after clear: %#v", entry)
	}
}

func TestDHCPReservationProfileClearFencesInFlightRefresh(t *testing.T) {
	app := testApp(t)
	profile := Profile{Name: "default", Server: "https://grid.example"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{{"ipv4addr": "192.0.2.99", "network_view": "default"}}})
	}))
	defer server.Close()
	token, err := app.readDHCPReservationCacheToken(profile, "default")
	if err != nil {
		t.Fatalf("capture cache token: %v", err)
	}
	if err := app.clearProfileCache(profile.Name); err != nil {
		t.Fatalf("clear profile cache: %v", err)
	}
	wrote, err := app.writeDHCPReservationCacheIfCurrent(profile, "default", []map[string]any{{"ipv4addr": "192.0.2.99"}}, time.Now(), token)
	if err != nil {
		t.Fatalf("profile-guarded refresh write: %v", err)
	}
	if wrote {
		t.Fatal("stale refresh wrote after profile clear")
	}
	entry, err := app.readCachedDHCPReservations(profile, "default")
	if err != nil {
		t.Fatalf("read profile-cleared cache: %v", err)
	}
	if entry.CacheFound {
		t.Fatalf("refresh resurrected cache after profile clear: %#v", entry)
	}
}

func TestDHCPReservationCacheGenerationFencesWAPIFlight(t *testing.T) {
	app := testApp(t)
	profile := Profile{Name: "default", Server: "https://grid.example"}
	requestStarted := make(chan struct{})
	releaseResponse := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(requestStarted)
		<-releaseResponse
		_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{{"ipv4addr": "192.0.2.99", "network_view": "default"}}})
	}))
	defer server.Close()
	client := testWapiClient(server)
	refreshErr := make(chan error, 1)
	go func() {
		_, err := app.refreshDHCPReservationCache(profile, client, "default")
		refreshErr <- err
	}()
	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("refresh did not reach WAPI")
	}
	if err := app.invalidateDHCPReservationCache(profile, "default"); err != nil {
		t.Fatalf("invalidate during WAPI request: %v", err)
	}
	close(releaseResponse)
	if err := <-refreshErr; err != nil {
		t.Fatalf("refresh after generation invalidation: %v", err)
	}
	entry, err := app.readCachedDHCPReservations(profile, "default")
	if err != nil {
		t.Fatalf("read generation-fenced cache: %v", err)
	}
	if entry.CacheFound {
		t.Fatalf("in-flight refresh repopulated invalidated cache: %#v", entry)
	}
}

func TestDHCPReservationCacheMutationFencesInFlightRefresh(t *testing.T) {
	app := testApp(t)
	profile := Profile{Name: "default", Server: "https://grid.example"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{{"ipv4addr": "192.0.2.99", "network_view": "default"}}})
	}))
	defer server.Close()
	token, err := app.readDHCPReservationCacheToken(profile, "default")
	if err != nil {
		t.Fatalf("capture cache token: %v", err)
	}
	if err := app.invalidateDHCPReservationCache(profile, "default"); err != nil {
		t.Fatalf("invalidate DHCP cache: %v", err)
	}
	wrote, err := app.writeDHCPReservationCacheIfCurrent(profile, "default", []map[string]any{{"ipv4addr": "192.0.2.99"}}, time.Now(), token)
	if err != nil {
		t.Fatalf("guarded refresh write: %v", err)
	}
	if wrote {
		t.Fatal("stale refresh wrote after invalidation")
	}
	entry, err := app.readCachedDHCPReservations(profile, "default")
	if err != nil {
		t.Fatalf("read fenced cache: %v", err)
	}
	if entry.CacheFound {
		t.Fatalf("fenced pre-mutation refresh repopulated cache: %#v", entry)
	}
}

func TestDHCPReservationCacheDoesNotCacheAmbiguousPlainPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rows := make([]map[string]any, dhcpReservationPageSize)
		for i := range rows {
			rows[i] = map[string]any{"ipv4addr": fmt.Sprintf("192.0.%d.%d", 2+i/254, 1+i%254), "network_view": "default"}
		}
		_ = json.NewEncoder(w).Encode(rows)
	}))
	defer server.Close()
	app := testApp(t)
	profile := Profile{Name: "default", Server: server.URL}
	if _, err := app.refreshDHCPReservationCache(profile, testWapiClient(server), "default"); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("ambiguous plain refresh error = %v, want explicit incomplete error", err)
	}
	entry, err := app.readCachedDHCPReservations(profile, "default")
	if err != nil {
		t.Fatalf("read ambiguous cache: %v", err)
	}
	if entry.CacheFound {
		t.Fatal("ambiguous plain page was cached")
	}
}

func TestDHCPReservationCacheInvalidationFailurePropagates(t *testing.T) {
	app := testApp(t)
	parent := t.TempDir()
	file := filepath.Join(parent, "not-a-directory")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("create cache path blocker: %v", err)
	}
	app.ConfigDir = file
	if err := app.queueDHCPReservationCacheRefresh(Profile{Name: "default", Server: "https://grid.example"}, "default"); err == nil {
		t.Fatal("cache invalidation failure was swallowed")
	}
}

func TestDHCPReservationRefreshReleasesExactLeaseWhenProfileLoadFails(t *testing.T) {
	app := testApp(t)
	profile := Profile{Name: "default", Server: "https://grid.example", Username: "admin"}
	if acquired, err := app.tryAcquireNetRefreshLease(profile, dhcpReservationCacheKind, "default", "", time.Now(), recordRefreshLeaseTTL); err != nil || !acquired {
		t.Fatalf("acquire DHCP lease: acquired=%v err=%v", acquired, err)
	}
	leaseToken, err := app.netRefreshLeaseToken(profile, dhcpReservationCacheKind, "default", "")
	if err != nil || leaseToken == 0 {
		t.Fatalf("read DHCP lease token: token=%d err=%v", leaseToken, err)
	}
	t.Setenv(cacheRefreshProfileKeyEnv, cacheProfileName(profile))
	t.Setenv(cacheRefreshLeaseTokenEnv, fmt.Sprint(leaseToken))
	if err := app.runNetCacheRefresh(profile.Name, dhcpReservationCacheKind, "default", ""); err == nil {
		t.Fatal("profile load unexpectedly succeeded")
	}
	active, err := app.netRefreshLeaseActive(profile, dhcpReservationCacheKind, "default", "", time.Now())
	if err != nil {
		t.Fatalf("check released lease: %v", err)
	}
	if active {
		t.Fatal("exact acquired DHCP lease remained after profile load failure")
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
