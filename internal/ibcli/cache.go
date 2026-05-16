package ibcli

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	_ "github.com/mattn/go-sqlite3"
)

const (
	cacheFileName         = "cache.sqlite3"
	cacheSchemaVersion    = "6"
	recordRefreshLeaseTTL = 300 * time.Second
	zoneRefreshLockName   = "<zone-cache>"
)

const cacheSchema = `
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS cache_meta (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

INSERT OR IGNORE INTO cache_meta (key, value)
VALUES ('schema_version', '6');

CREATE TABLE IF NOT EXISTS zone_cache (
  cache_key TEXT PRIMARY KEY,
  profile TEXT NOT NULL,
  view TEXT NOT NULL,
  cached_at INTEGER NOT NULL,
  payload_json TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_zone_cache_scope
ON zone_cache (profile, view);

CREATE TABLE IF NOT EXISTS record_cache (
  cache_key TEXT PRIMARY KEY,
  profile TEXT NOT NULL,
  view TEXT NOT NULL,
  zone TEXT NOT NULL,
  zone_serial TEXT,
  cached_at INTEGER NOT NULL,
  stale_expires_at INTEGER NOT NULL,
  payload_json TEXT NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_record_cache_scope_zone
ON record_cache (profile, view, zone);

CREATE INDEX IF NOT EXISTS idx_record_cache_zone
ON record_cache (zone);

CREATE TABLE IF NOT EXISTS record_refresh_locks (
  cache_key TEXT PRIMARY KEY,
  profile TEXT NOT NULL,
  view TEXT NOT NULL,
  zone TEXT NOT NULL,
  started_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_record_refresh_locks_scope_zone
ON record_refresh_locks (profile, view, zone);
`

var (
	cacheStatsEntryStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#E5E9F0")).Background(lipgloss.Color("#5E81AC")).Padding(0, 1)
	cacheStatsRecordStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#2E3440")).Background(lipgloss.Color("#A3BE8C")).Padding(0, 1)
	cacheStatsFreshStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#2E3440")).Background(lipgloss.Color("#8FBCBB")).Padding(0, 1)
	cacheStatsStaleStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#2E3440")).Background(lipgloss.Color("#EBCB8B")).Padding(0, 1)
	cacheStatsExpiredStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ECEFF4")).Background(lipgloss.Color("#BF616A")).Padding(0, 1)
	cacheStatsRefreshingStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ECEFF4")).Background(lipgloss.Color("#B48EAD")).Padding(0, 1)
)

// cachedPayload is the common in-memory form for zone-list and record-cache
// rows. Freshness is computed from CachedAt plus the current cache_ttl setting;
// only record rows also carry a stale-while-revalidate deadline.
type cachedPayload struct {
	Rows           []map[string]any
	Serial         string
	CachedAt       int64
	StaleExpiresAt int64
	CacheFound     bool
}

type cacheStatusSnapshot struct {
	Statistics cacheStatusStatistics `json:"statistics"`
	Entries    []map[string]any      `json:"entries"`
}

type cacheStatusStatistics struct {
	CacheEntries    int `json:"cache_entries"`
	ZoneEntries     int `json:"zone_entries"`
	RecordEntries   int `json:"record_entries"`
	CachedRecords   int `json:"cached_records"`
	FreshEntries    int `json:"fresh_entries"`
	SWRStaleEntries int `json:"swr_stale_entries"`
	ExpiredEntries  int `json:"expired_entries"`
	ActiveRefreshes int `json:"active_refreshes"`
}

func (a *App) cachePath() string {
	return filepath.Join(a.ConfigDir, cacheFileName)
}

func (a *App) openCacheDB() (*sql.DB, error) {
	if err := a.ensureConfigDir(); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite3", a.cachePath())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	// The CLI can launch background refresh helpers while an interactive command
	// is reading the cache. A single connection plus busy_timeout keeps SQLite
	// locking predictable without surfacing transient "database is locked" errors.
	if _, err := db.Exec("PRAGMA busy_timeout = 5000"); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(cacheSchema); err != nil {
		db.Close()
		return nil, err
	}
	if err := migrateCacheDB(db); err != nil {
		db.Close()
		return nil, err
	}
	_ = os.Chmod(a.cachePath(), 0o600)
	return db, nil
}

func migrateCacheDB(db *sql.DB) error {
	var version string
	err := db.QueryRow(`SELECT value FROM cache_meta WHERE key = 'schema_version'`).Scan(&version)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	current, err := cacheSchemaIsCurrent(db)
	if err != nil {
		return err
	}
	if version == cacheSchemaVersion && current {
		return nil
	}
	if err := migrateRecordCacheSWR(db); err != nil {
		return err
	}
	if err := migrateCacheFreshExpiry(db); err != nil {
		return err
	}
	current, err = cacheSchemaIsCurrent(db)
	if err != nil {
		return err
	}
	if current {
		_, err := db.Exec(`INSERT OR REPLACE INTO cache_meta (key, value) VALUES ('schema_version', ?)`, cacheSchemaVersion)
		return err
	}
	return resetCacheSchema(db)
}

func cacheSchemaIsCurrent(db *sql.DB) (bool, error) {
	zoneColumns, err := tableColumnSet(db, "zone_cache")
	if err != nil {
		return false, err
	}
	recordColumns, err := tableColumnSet(db, "record_cache")
	if err != nil {
		return false, err
	}
	lockColumns, err := tableColumnSet(db, "record_refresh_locks")
	if err != nil {
		return false, err
	}
	if zoneColumns["expires_at"] || recordColumns["expires_at"] {
		return false, nil
	}
	for _, column := range []string{"cache_key", "profile", "view", "cached_at", "payload_json"} {
		if !zoneColumns[column] {
			return false, nil
		}
	}
	for _, column := range []string{"cache_key", "profile", "view", "zone", "zone_serial", "cached_at", "stale_expires_at", "payload_json"} {
		if !recordColumns[column] {
			return false, nil
		}
	}
	for _, column := range []string{"cache_key", "profile", "view", "zone", "started_at", "expires_at"} {
		if !lockColumns[column] {
			return false, nil
		}
	}
	return true, nil
}

func migrateCacheFreshExpiry(db *sql.DB) error {
	zoneColumns, err := tableColumnSet(db, "zone_cache")
	if err != nil {
		return err
	}
	recordColumns, err := tableColumnSet(db, "record_cache")
	if err != nil {
		return err
	}
	if !zoneColumns["expires_at"] && !recordColumns["expires_at"] {
		return nil
	}
	for _, column := range []string{"cache_key", "profile", "view", "cached_at", "expires_at", "payload_json"} {
		if !zoneColumns[column] {
			return nil
		}
	}
	for _, column := range []string{"cache_key", "profile", "view", "zone", "zone_serial", "cached_at", "expires_at", "stale_expires_at", "payload_json"} {
		if !recordColumns[column] {
			return nil
		}
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
DROP INDEX IF EXISTS idx_zone_cache_scope;
DROP INDEX IF EXISTS idx_record_cache_scope_zone;
DROP INDEX IF EXISTS idx_record_cache_zone;
DROP INDEX IF EXISTS idx_record_cache_expires;
DROP TABLE IF EXISTS zone_cache_new;
DROP TABLE IF EXISTS record_cache_new;

CREATE TABLE zone_cache_new (
  cache_key TEXT PRIMARY KEY,
  profile TEXT NOT NULL,
  view TEXT NOT NULL,
  cached_at INTEGER NOT NULL,
  payload_json TEXT NOT NULL
);

INSERT INTO zone_cache_new (cache_key, profile, view, cached_at, payload_json)
SELECT cache_key, profile, view, cached_at, payload_json
FROM zone_cache;

CREATE TABLE record_cache_new (
  cache_key TEXT PRIMARY KEY,
  profile TEXT NOT NULL,
  view TEXT NOT NULL,
  zone TEXT NOT NULL,
  zone_serial TEXT,
  cached_at INTEGER NOT NULL,
  stale_expires_at INTEGER NOT NULL,
  payload_json TEXT NOT NULL
);

INSERT INTO record_cache_new (cache_key, profile, view, zone, zone_serial, cached_at, stale_expires_at, payload_json)
SELECT cache_key, profile, view, zone, zone_serial, cached_at, stale_expires_at, payload_json
FROM record_cache;

DROP TABLE zone_cache;
ALTER TABLE zone_cache_new RENAME TO zone_cache;
DROP TABLE record_cache;
ALTER TABLE record_cache_new RENAME TO record_cache;

CREATE INDEX idx_zone_cache_scope
ON zone_cache (profile, view);

CREATE UNIQUE INDEX idx_record_cache_scope_zone
ON record_cache (profile, view, zone);

CREATE INDEX idx_record_cache_zone
ON record_cache (zone);

INSERT OR REPLACE INTO cache_meta (key, value)
VALUES ('schema_version', '6');
`); err != nil {
		return err
	}
	return tx.Commit()
}

func migrateRecordCacheSWR(db *sql.DB) error {
	recordColumns, err := tableColumnSet(db, "record_cache")
	if err != nil {
		return err
	}
	if recordColumns["stale_expires_at"] {
		return nil
	}
	for _, column := range []string{"cache_key", "profile", "view", "zone", "zone_serial", "cached_at", "expires_at", "payload_json"} {
		if !recordColumns[column] {
			return nil
		}
	}
	if _, err := db.Exec(`ALTER TABLE record_cache ADD COLUMN stale_expires_at INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	_, err = db.Exec(`UPDATE record_cache SET stale_expires_at = cached_at + ? WHERE stale_expires_at = 0`, defaultRecordsCacheSWRSeconds)
	return err
}

func tableColumnSet(db *sql.DB, tableName string) (map[string]bool, error) {
	rows, err := db.Query(`PRAGMA table_info(` + tableName + `)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	columns := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return nil, err
		}
		columns[name] = true
	}
	return columns, rows.Err()
}

func resetCacheSchema(db *sql.DB) error {
	if _, err := db.Exec(`
DROP TABLE IF EXISTS record_cache_addresses;
DROP TABLE IF EXISTS record_cache_records;
DROP TABLE IF EXISTS zone_cache_entries;
DROP TABLE IF EXISTS zone_cache;
DROP TABLE IF EXISTS record_cache;
DROP TABLE IF EXISTS record_refresh_locks;
DELETE FROM cache_meta WHERE key = 'schema_version';
`); err != nil {
		return err
	}
	_, err := db.Exec(cacheSchema)
	return err
}

func cacheScope(profile Profile) (string, string) {
	name := strings.TrimSpace(profile.Name)
	if name == "" {
		name = defaultProfileName
	}
	view := strings.TrimSpace(profile.DNSView)
	if view == "" {
		view = "default"
	}
	return name, view
}

func cacheKey(parts ...string) string {
	// Use a non-printing separator so profile/view/zone values cannot collide
	// when one value happens to contain another value's text.
	return strings.Join(parts, "\x1f")
}

func normalizeCacheZone(zone string) string {
	return strings.ToLower(strings.TrimRight(strings.TrimSpace(zone), "."))
}

func (a *App) cacheFreshUntil(cachedAt int64) int64 {
	// Freshness is deliberately derived instead of stored. Changing cache_ttl in
	// config immediately changes how existing cache rows are interpreted.
	return cachedAt + int64(a.cacheTTL()/time.Second)
}

func (a *App) cacheEntryFresh(entry cachedPayload, now time.Time) bool {
	return now.Unix() < a.cacheFreshUntil(entry.CachedAt)
}

func (a *App) readCachedZones(profile Profile) (cachedPayload, error) {
	profileName, view := cacheScope(profile)
	key := cacheKey("zones", profileName, view)
	db, err := a.openCacheDB()
	if err != nil {
		return cachedPayload{}, err
	}
	defer db.Close()

	var raw string
	var cachedAt int64
	err = db.QueryRow(`SELECT payload_json, cached_at FROM zone_cache WHERE cache_key = ?`, key).Scan(&raw, &cachedAt)
	if err == sql.ErrNoRows {
		return cachedPayload{}, nil
	}
	if err != nil {
		return cachedPayload{}, err
	}
	rows, err := rowsFromJSON(raw)
	if err != nil {
		return cachedPayload{}, err
	}
	return cachedPayload{Rows: rows, CachedAt: cachedAt, CacheFound: true}, nil
}

func (a *App) writeCachedZones(profile Profile, rows []map[string]any, now time.Time) error {
	payload, err := json.Marshal(rows)
	if err != nil {
		return err
	}
	profileName, view := cacheScope(profile)
	key := cacheKey("zones", profileName, view)
	db, err := a.openCacheDB()
	if err != nil {
		return err
	}
	defer db.Close()

	cachedAt := now.Unix()
	_, err = db.Exec(`
INSERT OR REPLACE INTO zone_cache (cache_key, profile, view, cached_at, payload_json)
VALUES (?, ?, ?, ?, ?)`, key, profileName, view, cachedAt, string(payload))
	return err
}

func (a *App) readCachedRecords(profile Profile, zone string) (cachedPayload, error) {
	profileName, view := cacheScope(profile)
	zone = normalizeCacheZone(zone)
	key := cacheKey("records", profileName, view, zone)
	db, err := a.openCacheDB()
	if err != nil {
		return cachedPayload{}, err
	}
	defer db.Close()

	var serial sql.NullString
	var raw string
	var cachedAt, staleExpiresAt int64
	err = db.QueryRow(`SELECT payload_json, zone_serial, cached_at, stale_expires_at FROM record_cache WHERE cache_key = ?`, key).Scan(&raw, &serial, &cachedAt, &staleExpiresAt)
	if err == sql.ErrNoRows {
		return cachedPayload{}, nil
	}
	if err != nil {
		return cachedPayload{}, err
	}
	rows, err := rowsFromJSON(raw)
	if err != nil {
		return cachedPayload{}, err
	}
	entry := cachedPayload{Rows: rows, CachedAt: cachedAt, StaleExpiresAt: staleExpiresAt, CacheFound: true}
	if serial.Valid {
		entry.Serial = serial.String
	}
	return entry, nil
}

func (a *App) writeCachedRecords(profile Profile, zone string, serial string, rows []map[string]any, now time.Time) error {
	// A freshly downloaded zone gets a normal freshness window and a longer SWR
	// window. After the normal window expires, list/search can still return this
	// payload while a detached process checks the zone serial.
	return a.writeCachedRecordsEntry(profile, zone, serial, rows, now.Unix(), now.Add(a.recordsCacheSWRTTL()).Unix())
}

func (a *App) writeCachedRecordsEntry(profile Profile, zone string, serial string, rows []map[string]any, cachedAt int64, staleExpiresAt int64) error {
	payload, err := json.Marshal(rows)
	if err != nil {
		return err
	}
	profileName, view := cacheScope(profile)
	zone = normalizeCacheZone(zone)
	serial = cleanIntegerString(serial)
	key := cacheKey("records", profileName, view, zone)
	db, err := a.openCacheDB()
	if err != nil {
		return err
	}
	defer db.Close()

	_, err = db.Exec(`
INSERT OR REPLACE INTO record_cache (cache_key, profile, view, zone, zone_serial, cached_at, stale_expires_at, payload_json)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, key, profileName, view, zone, nullString(serial), cachedAt, staleExpiresAt, string(payload))
	return err
}

func (a *App) renewCachedRecordsAge(profile Profile, zone string, cachedAt time.Time, staleExpiresAt time.Time) error {
	// A matching server serial means the payload is still current. Move cached_at
	// forward so age and cache_ttl-based freshness reflect that validation.
	profileName, view := cacheScope(profile)
	zone = normalizeCacheZone(zone)
	key := cacheKey("records", profileName, view, zone)
	db, err := a.openCacheDB()
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec(`UPDATE record_cache SET cached_at = ?, stale_expires_at = ? WHERE cache_key = ?`, cachedAt.Unix(), staleExpiresAt.Unix(), key)
	return err
}

func (a *App) invalidateZoneCache(profile Profile) {
	profileName, view := cacheScope(profile)
	db, err := a.openCacheDB()
	if err != nil {
		return
	}
	defer db.Close()
	_, _ = db.Exec(`DELETE FROM zone_cache WHERE profile = ? AND view = ?`, profileName, view)
}

func (a *App) deleteRecordCacheEntry(profile Profile, zone string) {
	profileName, view := cacheScope(profile)
	zone = normalizeCacheZone(zone)
	if zone == "" {
		return
	}
	db, err := a.openCacheDB()
	if err != nil {
		return
	}
	defer db.Close()
	_, _ = db.Exec(`DELETE FROM record_cache WHERE profile = ? AND view = ? AND zone = ?`, profileName, view, zone)
}

func (a *App) invalidateRecordCache(profile Profile, zone string) {
	profileName, view := cacheScope(profile)
	zone = normalizeCacheZone(zone)
	db, err := a.openCacheDB()
	if err != nil {
		return
	}
	defer db.Close()
	if zone == "" {
		_, _ = db.Exec(`DELETE FROM record_cache WHERE profile = ? AND view = ?`, profileName, view)
		_, _ = db.Exec(`DELETE FROM record_refresh_locks WHERE profile = ? AND view = ?`, profileName, view)
		return
	}
	_, _ = db.Exec(`DELETE FROM record_cache WHERE profile = ? AND view = ? AND zone = ?`, profileName, view, zone)
	_, _ = db.Exec(`DELETE FROM record_refresh_locks WHERE profile = ? AND view = ? AND zone = ?`, profileName, view, zone)
}

func (a *App) clearCache() error {
	db, err := a.openCacheDB()
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.Exec(`DELETE FROM zone_cache; DELETE FROM record_cache; DELETE FROM record_refresh_locks;`); err != nil {
		return err
	}
	return nil
}

func (a *App) clearProfileCache(profileName string) error {
	profileName, err := normalizeProfileName(profileName)
	if err != nil {
		return err
	}
	db, err := a.openCacheDB()
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec(`
DELETE FROM zone_cache WHERE profile = ?;
DELETE FROM record_cache WHERE profile = ?;
DELETE FROM record_refresh_locks WHERE profile = ?;`, profileName, profileName, profileName)
	return err
}

func (a *App) tryAcquireRecordRefreshLease(profile Profile, zone string, now time.Time, ttl time.Duration) (bool, error) {
	profileName, view := cacheScope(profile)
	zone = normalizeCacheZone(zone)
	key := cacheKey("record-refresh", profileName, view, zone)
	db, err := a.openCacheDB()
	if err != nil {
		return false, err
	}
	defer db.Close()

	nowUnix := now.Unix()
	_, _ = db.Exec(`DELETE FROM record_refresh_locks WHERE expires_at <= ?`, nowUnix)

	// The lease is advisory and local to the cache DB. It prevents a burst of
	// list/search commands from spawning duplicate refresh subprocesses for the
	// same profile, view, and zone while still expiring after crashes.
	_, err = db.Exec(`
INSERT INTO record_refresh_locks (cache_key, profile, view, zone, started_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?)`, key, profileName, view, zone, nowUnix, now.Add(ttl).Unix())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (a *App) releaseRecordRefreshLease(profile Profile, zone string) error {
	profileName, view := cacheScope(profile)
	zone = normalizeCacheZone(zone)
	key := cacheKey("record-refresh", profileName, view, zone)
	db, err := a.openCacheDB()
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec(`DELETE FROM record_refresh_locks WHERE cache_key = ?`, key)
	return err
}

func (a *App) recordRefreshLeaseActive(profile Profile, zone string, now time.Time) (bool, error) {
	profileName, view := cacheScope(profile)
	zone = normalizeCacheZone(zone)
	key := cacheKey("record-refresh", profileName, view, zone)
	db, err := a.openCacheDB()
	if err != nil {
		return false, err
	}
	defer db.Close()

	var expiresAt int64
	err = db.QueryRow(`SELECT expires_at FROM record_refresh_locks WHERE cache_key = ?`, key).Scan(&expiresAt)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return expiresAt > now.Unix(), nil
}

func (a *App) waitForActiveRecordRefresh(profile Profile, zone string, maxWait time.Duration, pollInterval time.Duration) (bool, error) {
	if maxWait <= 0 {
		return false, nil
	}
	if pollInterval <= 0 {
		pollInterval = 2 * time.Millisecond
	}
	active, err := a.recordRefreshLeaseActive(profile, zone, time.Now())
	if err != nil || !active {
		return false, err
	}

	deadline := time.Now().Add(maxWait)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return false, nil
		}
		sleepFor := pollInterval
		if remaining < sleepFor {
			sleepFor = remaining
		}
		time.Sleep(sleepFor)
		active, err = a.recordRefreshLeaseActive(profile, zone, time.Now())
		if err != nil {
			return false, err
		}
		if !active {
			return true, nil
		}
	}
}

func (a *App) tryAcquireZoneRefreshLease(profile Profile, now time.Time, ttl time.Duration) (bool, error) {
	return a.tryAcquireRecordRefreshLease(profile, zoneRefreshLockName, now, ttl)
}

func (a *App) releaseZoneRefreshLease(profile Profile) error {
	return a.releaseRecordRefreshLease(profile, zoneRefreshLockName)
}

func (a *App) cacheStatusRows() ([]map[string]any, error) {
	snapshot, err := a.cacheStatusSnapshot()
	if err != nil {
		return nil, err
	}
	return snapshot.Entries, nil
}

func (a *App) cacheStatusSnapshot() (cacheStatusSnapshot, error) {
	db, err := a.openCacheDB()
	if err != nil {
		return cacheStatusSnapshot{}, err
	}
	defer db.Close()

	rows, err := db.Query(`
SELECT 'zones' AS kind, profile, view, '' AS zone, '' AS serial, cached_at, 0 AS stale_expires_at, payload_json
FROM zone_cache
UNION ALL
SELECT 'records' AS kind, profile, view, zone, COALESCE(zone_serial, '') AS serial, cached_at, stale_expires_at, payload_json
FROM record_cache
ORDER BY kind, profile, view, zone`)
	if err != nil {
		return cacheStatusSnapshot{}, err
	}
	defer rows.Close()

	nowTime := time.Now()
	now := nowTime.Unix()
	snapshot := cacheStatusSnapshot{Entries: []map[string]any{}}
	for rows.Next() {
		var kind, profile, view, zone, serial string
		var cachedAt, staleExpiresAt int64
		var raw string
		if err := rows.Scan(&kind, &profile, &view, &zone, &serial, &cachedAt, &staleExpiresAt, &raw); err != nil {
			return cacheStatusSnapshot{}, err
		}
		itemCount := payloadItemCount(raw)
		staleExpiry := ""
		if kind == "records" {
			staleExpiry = cacheExpiryText(now, staleExpiresAt)
		}
		snapshot.Entries = append(snapshot.Entries, map[string]any{
			"kind":          kind,
			"profile":       profile,
			"view":          view,
			"zone":          zone,
			"serial":        cleanIntegerString(serial),
			"items":         strconv.Itoa(itemCount),
			"age":           cacheRelativeDuration(now - cachedAt),
			"stale_expires": staleExpiry,
		})
		snapshot.Statistics.addEntry(kind, itemCount, staleExpiresAt, a.cacheFreshUntil(cachedAt), nowTime)
	}
	if err := rows.Err(); err != nil {
		return cacheStatusSnapshot{}, err
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM record_refresh_locks WHERE expires_at > ?`, now).Scan(&snapshot.Statistics.ActiveRefreshes); err != nil {
		return cacheStatusSnapshot{}, err
	}
	return snapshot, nil
}

func (s *cacheStatusStatistics) addEntry(kind string, itemCount int, staleExpiresAt, freshUntil int64, now time.Time) {
	s.CacheEntries++
	nowUnix := now.Unix()
	switch kind {
	case "zones":
		s.ZoneEntries++
	case "records":
		s.RecordEntries++
		s.CachedRecords += itemCount
	}
	if nowUnix < freshUntil {
		s.FreshEntries++
		return
	}
	if kind == "records" && nowUnix < staleExpiresAt {
		s.SWRStaleEntries++
		return
	}
	s.ExpiredEntries++
}

func (a *App) emitCacheStatus(snapshot cacheStatusSnapshot) error {
	fields := []string{"kind", "profile", "view", "zone", "serial", "items", "age", "stale_expires"}
	switch a.Output {
	case "", tableOutput:
		if err := a.emitRows("Cache Status", fields, snapshot.Entries); err != nil {
			return err
		}
		fmt.Fprintln(a.Stdout)
		fmt.Fprintln(a.Stdout, renderCacheStatusStatistics(snapshot.Statistics))
		return nil
	case jsonOutput:
		encoder := json.NewEncoder(a.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(snapshot)
	case csvOutput:
		return a.emitRows("Cache Status", fields, snapshot.Entries)
	default:
		return fmt.Errorf("unsupported output format %q", a.Output)
	}
}

func renderCacheStatusStatistics(stats cacheStatusStatistics) string {
	badges := []string{
		renderCacheStatusBadge(cacheStatsEntryStyle, "Cache entries", stats.CacheEntries),
		renderCacheStatusBadge(cacheStatsRecordStyle, "Cached records", stats.CachedRecords),
		renderCacheStatusBadge(cacheStatsFreshStyle, "Fresh", stats.FreshEntries),
		renderCacheStatusBadge(cacheStatsStaleStyle, "SWR stale", stats.SWRStaleEntries),
		renderCacheStatusBadge(cacheStatsExpiredStyle, "Expired", stats.ExpiredEntries),
		renderCacheStatusBadge(cacheStatsRefreshingStyle, "Refreshing", stats.ActiveRefreshes),
	}
	return strings.Join(badges, "  ")
}

func renderCacheStatusBadge(style lipgloss.Style, label string, value int) string {
	return style.Render(fmt.Sprintf("%s %d", label, value))
}

func rowsFromJSON(raw string) ([]map[string]any, error) {
	var rows []map[string]any
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []map[string]any{}
	}
	return rows, nil
}

func payloadItemCount(raw string) int {
	rows, err := rowsFromJSON(raw)
	if err != nil {
		return 0
	}
	return len(rows)
}

func nullString(value string) sql.NullString {
	value = strings.TrimSpace(value)
	return sql.NullString{String: value, Valid: value != ""}
}

func cacheRelativeDuration(seconds int64) string {
	if seconds < 0 {
		seconds = 0
	}
	return humanDuration(seconds) + " ago"
}

func cacheExpiryText(now int64, expiresAt int64) string {
	if expiresAt <= now {
		return "expired"
	}
	return "in " + humanDuration(expiresAt-now)
}
