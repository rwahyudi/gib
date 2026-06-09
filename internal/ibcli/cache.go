package ibcli

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

const (
	cacheFileName         = "cache.sqlite3"
	cacheSchemaVersion    = "8"
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
VALUES ('schema_version', '8');

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

CREATE TABLE IF NOT EXISTS network_view_cache (
  cache_key TEXT PRIMARY KEY,
  profile TEXT NOT NULL,
  cached_at INTEGER NOT NULL,
  stale_expires_at INTEGER NOT NULL,
  payload_json TEXT NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_network_view_cache_profile
ON network_view_cache (profile);

CREATE TABLE IF NOT EXISTS network_cache (
  cache_key TEXT PRIMARY KEY,
  profile TEXT NOT NULL,
  network_view TEXT NOT NULL,
  cached_at INTEGER NOT NULL,
  stale_expires_at INTEGER NOT NULL,
  payload_json TEXT NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_network_cache_scope
ON network_cache (profile, network_view);

CREATE TABLE IF NOT EXISTS network_container_cache (
  cache_key TEXT PRIMARY KEY,
  profile TEXT NOT NULL,
  network_view TEXT NOT NULL,
  cached_at INTEGER NOT NULL,
  stale_expires_at INTEGER NOT NULL,
  payload_json TEXT NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_network_container_cache_scope
ON network_container_cache (profile, network_view);

CREATE TABLE IF NOT EXISTS ipv4_address_cache (
  cache_key TEXT PRIMARY KEY,
  profile TEXT NOT NULL,
  network_view TEXT NOT NULL,
  ip TEXT NOT NULL,
  cached_at INTEGER NOT NULL,
  stale_expires_at INTEGER NOT NULL,
  payload_json TEXT NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_ipv4_address_cache_scope
ON ipv4_address_cache (profile, network_view, ip);

CREATE TABLE IF NOT EXISTS net_refresh_locks (
  cache_key TEXT PRIMARY KEY,
  profile TEXT NOT NULL,
  kind TEXT NOT NULL,
  scope TEXT NOT NULL,
  started_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_net_refresh_locks_scope
ON net_refresh_locks (profile, kind, scope);
`

var (
	cacheStatsEntryStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#E5E9F0")).Background(lipgloss.Color("#5E81AC")).Padding(0, 1)
	cacheStatsRecordStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#2E3440")).Background(lipgloss.Color("#A3BE8C")).Padding(0, 1)
	cacheStatsFreshStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#2E3440")).Background(lipgloss.Color("#8FBCBB")).Padding(0, 1)
	cacheStatsStaleStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#2E3440")).Background(lipgloss.Color("#EBCB8B")).Padding(0, 1)
	cacheStatsExpiredStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ECEFF4")).Background(lipgloss.Color("#BF616A")).Padding(0, 1)
	cacheStatsRefreshingStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ECEFF4")).Background(lipgloss.Color("#B48EAD")).Padding(0, 1)

	cacheReadyMu    sync.Mutex
	cacheReadyPaths = map[string]bool{}
)

type sqlExecer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

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
	CacheEntries       int `json:"cache_entries"`
	ZoneEntries        int `json:"zone_entries"`
	RecordEntries      int `json:"record_entries"`
	NetworkViewEntries int `json:"network_view_entries"`
	NetworkEntries     int `json:"network_entries"`
	ContainerEntries   int `json:"container_entries"`
	IPv4AddressEntries int `json:"ipv4_address_entries"`
	CachedRecords      int `json:"cached_records"`
	FreshEntries       int `json:"fresh_entries"`
	SWRStaleEntries    int `json:"swr_stale_entries"`
	ExpiredEntries     int `json:"expired_entries"`
	ActiveRefreshes    int `json:"active_refreshes"`
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
	if err := a.ensureCacheDBReady(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func (a *App) ensureCacheDBReady(db *sql.DB) error {
	path := filepath.Clean(a.cachePath())
	cacheReadyMu.Lock()
	if cacheReadyPaths[path] {
		cacheReadyMu.Unlock()
		return nil
	}
	defer cacheReadyMu.Unlock()

	if err := execSQLScript(db, cacheSchema); err != nil {
		return err
	}
	if err := migrateCacheDB(db); err != nil {
		return err
	}
	if err := a.protectCacheFileForScope(false); err != nil {
		return err
	}

	cacheReadyPaths[path] = true
	return nil
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
	networkViewColumns, err := tableColumnSet(db, "network_view_cache")
	if err != nil {
		return false, err
	}
	networkColumns, err := tableColumnSet(db, "network_cache")
	if err != nil {
		return false, err
	}
	containerColumns, err := tableColumnSet(db, "network_container_cache")
	if err != nil {
		return false, err
	}
	addressColumns, err := tableColumnSet(db, "ipv4_address_cache")
	if err != nil {
		return false, err
	}
	netLockColumns, err := tableColumnSet(db, "net_refresh_locks")
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
	for _, column := range []string{"cache_key", "profile", "cached_at", "stale_expires_at", "payload_json"} {
		if !networkViewColumns[column] {
			return false, nil
		}
	}
	for _, column := range []string{"cache_key", "profile", "network_view", "cached_at", "stale_expires_at", "payload_json"} {
		if !networkColumns[column] {
			return false, nil
		}
	}
	for _, column := range []string{"cache_key", "profile", "network_view", "cached_at", "stale_expires_at", "payload_json"} {
		if !containerColumns[column] {
			return false, nil
		}
	}
	for _, column := range []string{"cache_key", "profile", "network_view", "ip", "cached_at", "stale_expires_at", "payload_json"} {
		if !addressColumns[column] {
			return false, nil
		}
	}
	for _, column := range []string{"cache_key", "profile", "kind", "scope", "started_at", "expires_at"} {
		if !netLockColumns[column] {
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

	if err := execSQLScript(tx, `
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

func execSQLScript(execer sqlExecer, script string) error {
	for _, statement := range strings.Split(script, ";") {
		statement = strings.TrimSpace(statement)
		if statement == "" {
			continue
		}
		if _, err := execer.Exec(statement); err != nil {
			return err
		}
	}
	return nil
}

func resetCacheSchema(db *sql.DB) error {
	if err := execSQLScript(db, `
DROP TABLE IF EXISTS record_cache_addresses;
DROP TABLE IF EXISTS record_cache_records;
DROP TABLE IF EXISTS zone_cache_entries;
DROP TABLE IF EXISTS zone_cache;
DROP TABLE IF EXISTS record_cache;
DROP TABLE IF EXISTS record_refresh_locks;
DROP TABLE IF EXISTS network_view_cache;
DROP TABLE IF EXISTS network_cache;
DROP TABLE IF EXISTS network_container_cache;
DROP TABLE IF EXISTS ipv4_address_cache;
DROP TABLE IF EXISTS net_refresh_locks;
DELETE FROM cache_meta WHERE key = 'schema_version';
`); err != nil {
		return err
	}
	return execSQLScript(db, cacheSchema)
}

func cacheScope(profile Profile) (string, string) {
	name := cacheProfileName(profile)
	view := strings.TrimSpace(profile.DNSView)
	if view == "" {
		view = "default"
	}
	return name, view
}

func cacheProfileName(profile Profile) string {
	name := strings.TrimSpace(profile.Name)
	if name == "" {
		name = defaultProfileName
	}
	return name
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
	started := time.Now()
	profileName, view := cacheScope(profile)
	key := cacheKey("zones", profileName, view)
	db, err := a.openCacheDB()
	if err != nil {
		a.debugEvent("cache zones error", df("profile", profileName), df("view", view), df("duration", time.Since(started)), df("error", err.Error()))
		return cachedPayload{}, err
	}
	defer db.Close()

	var raw string
	var cachedAt int64
	err = db.QueryRow(`SELECT payload_json, cached_at FROM zone_cache WHERE cache_key = ?`, key).Scan(&raw, &cachedAt)
	if err == sql.ErrNoRows {
		a.debugEvent("cache zones miss", df("profile", profileName), df("view", view), df("duration", time.Since(started)))
		return cachedPayload{}, nil
	}
	if err != nil {
		a.debugEvent("cache zones error", df("profile", profileName), df("view", view), df("duration", time.Since(started)), df("error", err.Error()))
		return cachedPayload{}, err
	}
	rows, err := rowsFromJSON(raw)
	if err != nil {
		a.debugEvent("cache zones error", df("profile", profileName), df("view", view), df("duration", time.Since(started)), df("error", err.Error()))
		return cachedPayload{}, err
	}
	a.debugEvent("cache zones hit", df("profile", profileName), df("view", view), df("rows", len(rows)), df("fresh", a.cacheEntryFresh(cachedPayload{CachedAt: cachedAt}, time.Now())), df("duration", time.Since(started)))
	return cachedPayload{Rows: rows, CachedAt: cachedAt, CacheFound: true}, nil
}

func (a *App) writeCachedZones(profile Profile, rows []map[string]any, now time.Time) error {
	started := time.Now()
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
	if err != nil {
		a.debugEvent("cache zones write error", df("profile", profileName), df("view", view), df("rows", len(rows)), df("duration", time.Since(started)), df("error", err.Error()))
	} else {
		a.debugEvent("cache zones write", df("profile", profileName), df("view", view), df("rows", len(rows)), df("duration", time.Since(started)))
	}
	return err
}

func (a *App) readCachedNetworkViews(profile Profile) (cachedPayload, error) {
	profileName := cacheProfileName(profile)
	key := cacheKey("network-views", profileName)
	db, err := a.openCacheDB()
	if err != nil {
		return cachedPayload{}, err
	}
	defer db.Close()

	var raw string
	var cachedAt, staleExpiresAt int64
	err = db.QueryRow(`SELECT payload_json, cached_at, stale_expires_at FROM network_view_cache WHERE cache_key = ?`, key).Scan(&raw, &cachedAt, &staleExpiresAt)
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
	return cachedPayload{Rows: rows, CachedAt: cachedAt, StaleExpiresAt: staleExpiresAt, CacheFound: true}, nil
}

func (a *App) writeCachedNetworkViews(profile Profile, rows []map[string]any, now time.Time) error {
	return a.writeCachedNetworkViewsEntry(profile, rows, now.Unix(), now.Add(a.recordsCacheSWRTTL()).Unix())
}

func (a *App) writeCachedNetworkViewsEntry(profile Profile, rows []map[string]any, cachedAt int64, staleExpiresAt int64) error {
	payload, err := json.Marshal(rows)
	if err != nil {
		return err
	}
	profileName := cacheProfileName(profile)
	key := cacheKey("network-views", profileName)
	db, err := a.openCacheDB()
	if err != nil {
		return err
	}
	defer db.Close()

	_, err = db.Exec(`
INSERT OR REPLACE INTO network_view_cache (cache_key, profile, cached_at, stale_expires_at, payload_json)
VALUES (?, ?, ?, ?, ?)`, key, profileName, cachedAt, staleExpiresAt, string(payload))
	return err
}

func (a *App) readCachedNetworks(profile Profile, networkView string) (cachedPayload, error) {
	profileName := cacheProfileName(profile)
	networkView = strings.TrimSpace(networkView)
	key := cacheKey("networks", profileName, networkView)
	db, err := a.openCacheDB()
	if err != nil {
		return cachedPayload{}, err
	}
	defer db.Close()

	var raw string
	var cachedAt, staleExpiresAt int64
	err = db.QueryRow(`SELECT payload_json, cached_at, stale_expires_at FROM network_cache WHERE cache_key = ?`, key).Scan(&raw, &cachedAt, &staleExpiresAt)
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
	return cachedPayload{Rows: rows, CachedAt: cachedAt, StaleExpiresAt: staleExpiresAt, CacheFound: true}, nil
}

func (a *App) writeCachedNetworks(profile Profile, networkView string, rows []map[string]any, now time.Time) error {
	return a.writeCachedNetworksEntry(profile, networkView, rows, now.Unix(), now.Add(a.recordsCacheSWRTTL()).Unix())
}

func (a *App) writeCachedNetworksEntry(profile Profile, networkView string, rows []map[string]any, cachedAt int64, staleExpiresAt int64) error {
	payload, err := json.Marshal(rows)
	if err != nil {
		return err
	}
	profileName := cacheProfileName(profile)
	networkView = strings.TrimSpace(networkView)
	key := cacheKey("networks", profileName, networkView)
	db, err := a.openCacheDB()
	if err != nil {
		return err
	}
	defer db.Close()

	_, err = db.Exec(`
INSERT OR REPLACE INTO network_cache (cache_key, profile, network_view, cached_at, stale_expires_at, payload_json)
VALUES (?, ?, ?, ?, ?, ?)`, key, profileName, networkView, cachedAt, staleExpiresAt, string(payload))
	return err
}

func (a *App) readCachedNetworkContainers(profile Profile, networkView string) (cachedPayload, error) {
	profileName := cacheProfileName(profile)
	networkView = strings.TrimSpace(networkView)
	key := cacheKey("network-containers", profileName, networkView)
	db, err := a.openCacheDB()
	if err != nil {
		return cachedPayload{}, err
	}
	defer db.Close()

	var raw string
	var cachedAt, staleExpiresAt int64
	err = db.QueryRow(`SELECT payload_json, cached_at, stale_expires_at FROM network_container_cache WHERE cache_key = ?`, key).Scan(&raw, &cachedAt, &staleExpiresAt)
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
	return cachedPayload{Rows: rows, CachedAt: cachedAt, StaleExpiresAt: staleExpiresAt, CacheFound: true}, nil
}

func (a *App) writeCachedNetworkContainers(profile Profile, networkView string, rows []map[string]any, now time.Time) error {
	return a.writeCachedNetworkContainersEntry(profile, networkView, rows, now.Unix(), now.Add(a.recordsCacheSWRTTL()).Unix())
}

func (a *App) writeCachedNetworkContainersEntry(profile Profile, networkView string, rows []map[string]any, cachedAt int64, staleExpiresAt int64) error {
	payload, err := json.Marshal(rows)
	if err != nil {
		return err
	}
	profileName := cacheProfileName(profile)
	networkView = strings.TrimSpace(networkView)
	key := cacheKey("network-containers", profileName, networkView)
	db, err := a.openCacheDB()
	if err != nil {
		return err
	}
	defer db.Close()

	_, err = db.Exec(`
INSERT OR REPLACE INTO network_container_cache (cache_key, profile, network_view, cached_at, stale_expires_at, payload_json)
VALUES (?, ?, ?, ?, ?, ?)`, key, profileName, networkView, cachedAt, staleExpiresAt, string(payload))
	return err
}

func (a *App) readCachedIPv4Addresses(profile Profile, ip string, networkView string) (cachedPayload, error) {
	profileName := cacheProfileName(profile)
	ip = strings.TrimSpace(ip)
	networkView = strings.TrimSpace(networkView)
	key := cacheKey("ipv4-address", profileName, networkView, ip)
	db, err := a.openCacheDB()
	if err != nil {
		return cachedPayload{}, err
	}
	defer db.Close()

	var raw string
	var cachedAt, staleExpiresAt int64
	err = db.QueryRow(`SELECT payload_json, cached_at, stale_expires_at FROM ipv4_address_cache WHERE cache_key = ?`, key).Scan(&raw, &cachedAt, &staleExpiresAt)
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
	return cachedPayload{Rows: rows, CachedAt: cachedAt, StaleExpiresAt: staleExpiresAt, CacheFound: true}, nil
}

func (a *App) writeCachedIPv4Addresses(profile Profile, ip string, networkView string, rows []map[string]any, now time.Time) error {
	return a.writeCachedIPv4AddressesEntry(profile, ip, networkView, rows, now.Unix(), now.Add(a.recordsCacheSWRTTL()).Unix())
}

func (a *App) writeCachedIPv4AddressesEntry(profile Profile, ip string, networkView string, rows []map[string]any, cachedAt int64, staleExpiresAt int64) error {
	payload, err := json.Marshal(rows)
	if err != nil {
		return err
	}
	profileName := cacheProfileName(profile)
	ip = strings.TrimSpace(ip)
	networkView = strings.TrimSpace(networkView)
	key := cacheKey("ipv4-address", profileName, networkView, ip)
	db, err := a.openCacheDB()
	if err != nil {
		return err
	}
	defer db.Close()

	_, err = db.Exec(`
INSERT OR REPLACE INTO ipv4_address_cache (cache_key, profile, network_view, ip, cached_at, stale_expires_at, payload_json)
VALUES (?, ?, ?, ?, ?, ?, ?)`, key, profileName, networkView, ip, cachedAt, staleExpiresAt, string(payload))
	return err
}

func (a *App) readCachedRecords(profile Profile, zone string) (cachedPayload, error) {
	started := time.Now()
	profileName, view := cacheScope(profile)
	zone = normalizeCacheZone(zone)
	key := cacheKey("records", profileName, view, zone)
	db, err := a.openCacheDB()
	if err != nil {
		a.debugEvent("cache records error", df("profile", profileName), df("view", view), df("zone", zone), df("duration", time.Since(started)), df("error", err.Error()))
		return cachedPayload{}, err
	}
	defer db.Close()

	var serial sql.NullString
	var raw string
	var cachedAt, staleExpiresAt int64
	err = db.QueryRow(`SELECT payload_json, zone_serial, cached_at, stale_expires_at FROM record_cache WHERE cache_key = ?`, key).Scan(&raw, &serial, &cachedAt, &staleExpiresAt)
	if err == sql.ErrNoRows {
		a.debugEvent("cache records miss", df("profile", profileName), df("view", view), df("zone", zone), df("duration", time.Since(started)))
		return cachedPayload{}, nil
	}
	if err != nil {
		a.debugEvent("cache records error", df("profile", profileName), df("view", view), df("zone", zone), df("duration", time.Since(started)), df("error", err.Error()))
		return cachedPayload{}, err
	}
	rows, err := rowsFromJSON(raw)
	if err != nil {
		a.debugEvent("cache records error", df("profile", profileName), df("view", view), df("zone", zone), df("duration", time.Since(started)), df("error", err.Error()))
		return cachedPayload{}, err
	}
	entry := cachedPayload{Rows: rows, CachedAt: cachedAt, StaleExpiresAt: staleExpiresAt, CacheFound: true}
	if serial.Valid {
		entry.Serial = serial.String
	}
	a.debugEvent("cache records hit", df("profile", profileName), df("view", view), df("zone", zone), df("serial", entry.Serial), df("rows", len(rows)), df("fresh", a.cacheEntryFresh(entry, time.Now())), df("stale_valid", time.Now().Unix() < staleExpiresAt), df("duration", time.Since(started)))
	return entry, nil
}

func (a *App) readCachedRecordsForZones(profile Profile, zones []string) map[string]cachedPayload {
	started := time.Now()
	entries := map[string]cachedPayload{}
	profileName, view := cacheScope(profile)
	normalizedZones := make([]string, 0, len(zones))
	seen := map[string]bool{}
	for _, zone := range zones {
		zone = normalizeCacheZone(zone)
		if zone == "" || seen[zone] {
			continue
		}
		seen[zone] = true
		normalizedZones = append(normalizedZones, zone)
	}
	if len(normalizedZones) == 0 {
		a.debugEvent("cache records batch skip", df("profile", profileName), df("view", view), df("zones", 0), df("duration", time.Since(started)))
		return entries
	}

	db, err := a.openCacheDB()
	if err != nil {
		a.debugEvent("cache records batch error", df("profile", profileName), df("view", view), df("zones", len(normalizedZones)), df("duration", time.Since(started)), df("error", err.Error()))
		return entries
	}
	defer db.Close()

	for _, zone := range normalizedZones {
		key := cacheKey("records", profileName, view, zone)
		var serial sql.NullString
		var raw string
		var cachedAt, staleExpiresAt int64
		err := db.QueryRow(`SELECT payload_json, zone_serial, cached_at, stale_expires_at FROM record_cache WHERE cache_key = ?`, key).Scan(&raw, &serial, &cachedAt, &staleExpiresAt)
		if err != nil {
			continue
		}
		rows, err := rowsFromJSON(raw)
		if err != nil {
			continue
		}
		entry := cachedPayload{Rows: rows, CachedAt: cachedAt, StaleExpiresAt: staleExpiresAt, CacheFound: true}
		if serial.Valid {
			entry.Serial = serial.String
		}
		entries[zone] = entry
	}
	a.debugEvent("cache records batch", df("profile", profileName), df("view", view), df("zones", len(normalizedZones)), df("hits", len(entries)), df("duration", time.Since(started)))
	return entries
}

func (a *App) writeCachedRecords(profile Profile, zone string, serial string, rows []map[string]any, now time.Time) error {
	// A freshly downloaded zone gets a normal freshness window and a longer SWR
	// window. After the normal window expires, list/search can still return this
	// payload while a detached process checks the zone serial.
	return a.writeCachedRecordsEntry(profile, zone, serial, rows, now.Unix(), now.Add(a.recordsCacheSWRTTL()).Unix())
}

func (a *App) writeCachedRecordsEntry(profile Profile, zone string, serial string, rows []map[string]any, cachedAt int64, staleExpiresAt int64) error {
	started := time.Now()
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
	if err != nil {
		a.debugEvent("cache records write error", df("profile", profileName), df("view", view), df("zone", zone), df("rows", len(rows)), df("serial", serial), df("duration", time.Since(started)), df("error", err.Error()))
	} else {
		a.debugEvent("cache records write", df("profile", profileName), df("view", view), df("zone", zone), df("rows", len(rows)), df("serial", serial), df("duration", time.Since(started)))
	}
	return err
}

func (a *App) renewCachedRecordsAge(profile Profile, zone string, cachedAt time.Time, staleExpiresAt time.Time) error {
	started := time.Now()
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
	if err != nil {
		a.debugEvent("cache records renew error", df("profile", profileName), df("view", view), df("zone", zone), df("duration", time.Since(started)), df("error", err.Error()))
	} else {
		a.debugEvent("cache records renew", df("profile", profileName), df("view", view), df("zone", zone), df("duration", time.Since(started)))
	}
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
	return execSQLScript(db, `
DELETE FROM zone_cache;
DELETE FROM record_cache;
DELETE FROM record_refresh_locks;
DELETE FROM network_view_cache;
DELETE FROM network_cache;
DELETE FROM network_container_cache;
DELETE FROM ipv4_address_cache;
DELETE FROM net_refresh_locks;
`)
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
	for _, query := range []string{
		`DELETE FROM zone_cache WHERE profile = ?`,
		`DELETE FROM record_cache WHERE profile = ?`,
		`DELETE FROM record_refresh_locks WHERE profile = ?`,
		`DELETE FROM network_view_cache WHERE profile = ?`,
		`DELETE FROM network_cache WHERE profile = ?`,
		`DELETE FROM network_container_cache WHERE profile = ?`,
		`DELETE FROM ipv4_address_cache WHERE profile = ?`,
		`DELETE FROM net_refresh_locks WHERE profile = ?`,
	} {
		if _, err := db.Exec(query, profileName); err != nil {
			return err
		}
	}
	return nil
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

func netRefreshScope(kind string, networkView string, ip string) string {
	return cacheKey(kind, strings.TrimSpace(networkView), strings.TrimSpace(ip))
}

func (a *App) tryAcquireNetRefreshLease(profile Profile, kind string, networkView string, ip string, now time.Time, ttl time.Duration) (bool, error) {
	profileName := cacheProfileName(profile)
	scope := netRefreshScope(kind, networkView, ip)
	key := cacheKey("net-refresh", profileName, kind, scope)
	db, err := a.openCacheDB()
	if err != nil {
		return false, err
	}
	defer db.Close()

	nowUnix := now.Unix()
	_, _ = db.Exec(`DELETE FROM net_refresh_locks WHERE expires_at <= ?`, nowUnix)
	_, err = db.Exec(`
INSERT INTO net_refresh_locks (cache_key, profile, kind, scope, started_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?)`, key, profileName, kind, scope, nowUnix, now.Add(ttl).Unix())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (a *App) releaseNetRefreshLease(profile Profile, kind string, networkView string, ip string) error {
	profileName := cacheProfileName(profile)
	scope := netRefreshScope(kind, networkView, ip)
	key := cacheKey("net-refresh", profileName, kind, scope)
	db, err := a.openCacheDB()
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec(`DELETE FROM net_refresh_locks WHERE cache_key = ?`, key)
	return err
}

func (a *App) netRefreshLeaseActive(profile Profile, kind string, networkView string, ip string, now time.Time) (bool, error) {
	profileName := cacheProfileName(profile)
	scope := netRefreshScope(kind, networkView, ip)
	key := cacheKey("net-refresh", profileName, kind, scope)
	db, err := a.openCacheDB()
	if err != nil {
		return false, err
	}
	defer db.Close()

	var expiresAt int64
	err = db.QueryRow(`SELECT expires_at FROM net_refresh_locks WHERE cache_key = ?`, key).Scan(&expiresAt)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return expiresAt > now.Unix(), nil
}

func (a *App) waitForActiveNetRefresh(profile Profile, kind string, networkView string, ip string, maxWait time.Duration, pollInterval time.Duration) (bool, error) {
	if maxWait <= 0 {
		return false, nil
	}
	if pollInterval <= 0 {
		pollInterval = 2 * time.Millisecond
	}
	active, err := a.netRefreshLeaseActive(profile, kind, networkView, ip, time.Now())
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
		active, err = a.netRefreshLeaseActive(profile, kind, networkView, ip, time.Now())
		if err != nil {
			return false, err
		}
		if !active {
			return true, nil
		}
	}
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
UNION ALL
SELECT 'network_views' AS kind, profile, '' AS view, '' AS zone, '' AS serial, cached_at, stale_expires_at, payload_json
FROM network_view_cache
UNION ALL
SELECT 'networks' AS kind, profile, network_view AS view, '' AS zone, '' AS serial, cached_at, stale_expires_at, payload_json
FROM network_cache
UNION ALL
SELECT 'network_containers' AS kind, profile, network_view AS view, '' AS zone, '' AS serial, cached_at, stale_expires_at, payload_json
FROM network_container_cache
UNION ALL
SELECT 'ipv4_addresses' AS kind, profile, network_view AS view, ip AS zone, '' AS serial, cached_at, stale_expires_at, payload_json
FROM ipv4_address_cache
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
		if cacheKindUsesSWR(kind) {
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
	var recordRefreshes int
	if err := db.QueryRow(`SELECT COUNT(*) FROM record_refresh_locks WHERE expires_at > ?`, now).Scan(&recordRefreshes); err != nil {
		return cacheStatusSnapshot{}, err
	}
	var netRefreshes int
	if err := db.QueryRow(`SELECT COUNT(*) FROM net_refresh_locks WHERE expires_at > ?`, now).Scan(&netRefreshes); err != nil {
		return cacheStatusSnapshot{}, err
	}
	snapshot.Statistics.ActiveRefreshes = recordRefreshes + netRefreshes
	return snapshot, nil
}

func cacheKindUsesSWR(kind string) bool {
	return kind != "zones"
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
	case "network_views":
		s.NetworkViewEntries++
	case "networks":
		s.NetworkEntries++
	case "network_containers":
		s.ContainerEntries++
	case "ipv4_addresses":
		s.IPv4AddressEntries++
	}
	if nowUnix < freshUntil {
		s.FreshEntries++
		return
	}
	if cacheKindUsesSWR(kind) && nowUnix < staleExpiresAt {
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
		renderCacheStatusBadge(cacheStatsEntryStyle, "Network views", stats.NetworkViewEntries),
		renderCacheStatusBadge(cacheStatsEntryStyle, "Networks", stats.NetworkEntries),
		renderCacheStatusBadge(cacheStatsEntryStyle, "Containers", stats.ContainerEntries),
		renderCacheStatusBadge(cacheStatsEntryStyle, "IPv4 addresses", stats.IPv4AddressEntries),
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
