package ibcli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/dgraph-io/badger/v4"
)

const (
	cacheDirName          = "cache.badger"
	cacheValueLogFileSize = 1 << 20
	recordRefreshLeaseTTL = 300 * time.Second
	zoneRefreshLockName   = "<zone-cache>"
)

var (
	cacheStatsEntryStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#E5E9F0")).Background(lipgloss.Color("#5E81AC")).Padding(0, 1)
	cacheStatsRecordStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#2E3440")).Background(lipgloss.Color("#A3BE8C")).Padding(0, 1)
	cacheStatsFreshStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#2E3440")).Background(lipgloss.Color("#8FBCBB")).Padding(0, 1)
	cacheStatsStaleStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#2E3440")).Background(lipgloss.Color("#EBCB8B")).Padding(0, 1)
	cacheStatsExpiredStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ECEFF4")).Background(lipgloss.Color("#BF616A")).Padding(0, 1)
	cacheStatsRefreshingStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ECEFF4")).Background(lipgloss.Color("#B48EAD")).Padding(0, 1)
)

// cachedPayload is the common in-memory form for zone-list, record-cache, and
// IPAM cache entries. Freshness is computed from CachedAt plus cache_ttl; all
// non-zone rows also carry a stale-while-revalidate deadline.
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

type badgerCacheEntry struct {
	Kind           string           `json:"kind"`
	Profile        string           `json:"profile"`
	View           string           `json:"view,omitempty"`
	Zone           string           `json:"zone,omitempty"`
	Serial         string           `json:"serial,omitempty"`
	NetworkView    string           `json:"network_view,omitempty"`
	IP             string           `json:"ip,omitempty"`
	CachedAt       int64            `json:"cached_at"`
	StaleExpiresAt int64            `json:"stale_expires_at,omitempty"`
	Rows           []map[string]any `json:"rows"`
}

type badgerLeaseEntry struct {
	Profile   string `json:"profile"`
	View      string `json:"view,omitempty"`
	Zone      string `json:"zone,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Scope     string `json:"scope,omitempty"`
	StartedAt int64  `json:"started_at"`
	ExpiresAt int64  `json:"expires_at"`
}

func (a *App) cachePath() string {
	return filepath.Join(a.ConfigDir, cacheDirName)
}

func (a *App) openCacheDB() (*badger.DB, error) {
	if err := a.ensureConfigDir(); err != nil {
		return nil, err
	}
	path := a.cachePath()
	if err := os.MkdirAll(path, 0o700); err != nil {
		return nil, err
	}
	if err := a.protectCacheFileForScope(false); err != nil {
		return nil, err
	}
	a.cacheDBMu.Lock()
	defer a.cacheDBMu.Unlock()
	if a.cacheDBs == nil {
		a.cacheDBs = map[string]*badger.DB{}
	}
	if db := a.cacheDBs[path]; db != nil {
		return db, nil
	}
	options := cacheBadgerOptions(path)
	deadline := time.Now().Add(5 * time.Second)
	for {
		db, err := badger.Open(options)
		if err == nil {
			if err := a.protectCacheFileForScope(false); err != nil {
				_ = db.Close()
				return nil, err
			}
			a.cacheDBs[path] = db
			a.runValueLogGC(db)
			return db, nil
		}
		if !badgerOpenLockError(err) || time.Now().After(deadline) {
			return nil, err
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func (a *App) closeCacheDB(path string) error {
	a.cacheDBMu.Lock()
	db := a.cacheDBs[path]
	delete(a.cacheDBs, path)
	a.cacheDBMu.Unlock()
	if db == nil {
		return nil
	}
	return db.Close()
}

func cacheBadgerOptions(path string) badger.Options {
	// ib cache values are rewritten wholesale and rarely need Badger's separate
	// value-log storage. LSMOnlyOptions keeps values in the LSM tree when
	// possible so .vlog files mostly act as the write-ahead log instead of
	// accumulating cache payloads.
	return badger.LSMOnlyOptions(path).
		WithLogger(nil).
		WithCompactL0OnClose(true).
		WithValueLogFileSize(cacheValueLogFileSize)
}

func badgerOpenLockError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "lock") || strings.Contains(text, "resource temporarily unavailable")
}

// runValueLogGC reclaims discardable space in Badger value log files. Each call
// rewrites the most recyclable vlog; loop until ErrNoRewrite means nothing is
// left. This runs best-effort on DB open so vlog files do not accumulate.
func (a *App) runValueLogGC(db *badger.DB) {
	for {
		err := db.RunValueLogGC(0.5)
		if errors.Is(err, badger.ErrNoRewrite) {
			return
		}
		if err != nil {
			a.debugEvent("value log gc error", df("error", err.Error()))
			return
		}
	}
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

func badgerKey(parts ...string) []byte {
	return []byte(cacheKey(parts...))
}

func cacheEntryKey(kind string, profile string, parts ...string) []byte {
	all := append([]string{kind, profile}, parts...)
	return badgerKey(all...)
}

func recordCacheKey(profile string, view string, zone string) []byte {
	return cacheEntryKey("records", profile, view, normalizeCacheZone(zone))
}

func recordLeaseKey(profile string, view string, zone string) []byte {
	return cacheEntryKey("record_refresh_locks", profile, view, normalizeCacheZone(zone))
}

func netLeaseKey(profile string, kind string, scope string) []byte {
	return cacheEntryKey("net_refresh_locks", profile, kind, scope)
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

func (a *App) readBadgerCacheEntry(key []byte) (badgerCacheEntry, bool, error) {
	db, err := a.openCacheDB()
	if err != nil {
		return badgerCacheEntry{}, false, err
	}

	var entry badgerCacheEntry
	err = db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(key)
		if errors.Is(err, badger.ErrKeyNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		return item.Value(func(raw []byte) error {
			return json.Unmarshal(raw, &entry)
		})
	})
	if err != nil {
		return badgerCacheEntry{}, false, err
	}
	if entry.Kind == "" {
		return badgerCacheEntry{}, false, nil
	}
	return entry, true, nil
}

func (a *App) writeBadgerCacheEntry(key []byte, entry badgerCacheEntry) error {
	raw, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	db, err := a.openCacheDB()
	if err != nil {
		return err
	}
	return db.Update(func(txn *badger.Txn) error {
		return txn.Set(key, raw)
	})
}

func cachedPayloadFromBadger(entry badgerCacheEntry) cachedPayload {
	return cachedPayload{
		Rows:           entry.Rows,
		Serial:         entry.Serial,
		CachedAt:       entry.CachedAt,
		StaleExpiresAt: entry.StaleExpiresAt,
		CacheFound:     true,
	}
}

func (a *App) readCachedZones(profile Profile) (cachedPayload, error) {
	started := time.Now()
	profileName, view := cacheScope(profile)
	entry, found, err := a.readBadgerCacheEntry(cacheEntryKey("zones", profileName, view))
	if err != nil {
		a.debugEvent("cache zones error", df("profile", profileName), df("view", view), df("duration", time.Since(started)), df("error", err.Error()))
		return cachedPayload{}, err
	}
	if !found {
		a.debugEvent("cache zones miss", df("profile", profileName), df("view", view), df("duration", time.Since(started)))
		return cachedPayload{}, nil
	}
	payload := cachedPayloadFromBadger(entry)
	a.debugEvent("cache zones hit", df("profile", profileName), df("view", view), df("rows", len(payload.Rows)), df("fresh", a.cacheEntryFresh(payload, time.Now())), df("duration", time.Since(started)))
	return payload, nil
}

func (a *App) writeCachedZones(profile Profile, rows []map[string]any, now time.Time) error {
	started := time.Now()
	profileName, view := cacheScope(profile)
	err := a.writeBadgerCacheEntry(cacheEntryKey("zones", profileName, view), badgerCacheEntry{
		Kind:     "zones",
		Profile:  profileName,
		View:     view,
		CachedAt: now.Unix(),
		Rows:     rows,
	})
	if err != nil {
		a.debugEvent("cache zones write error", df("profile", profileName), df("view", view), df("rows", len(rows)), df("duration", time.Since(started)), df("error", err.Error()))
	} else {
		a.debugEvent("cache zones write", df("profile", profileName), df("view", view), df("rows", len(rows)), df("duration", time.Since(started)))
	}
	return err
}

func (a *App) readCachedNetworkViews(profile Profile) (cachedPayload, error) {
	profileName := cacheProfileName(profile)
	entry, found, err := a.readBadgerCacheEntry(cacheEntryKey("network_views", profileName))
	if err != nil {
		return cachedPayload{}, err
	}
	if !found {
		return cachedPayload{}, nil
	}
	return cachedPayloadFromBadger(entry), nil
}

func (a *App) writeCachedNetworkViews(profile Profile, rows []map[string]any, now time.Time) error {
	return a.writeCachedNetworkViewsEntry(profile, rows, now.Unix(), now.Add(a.recordsCacheSWRTTL()).Unix())
}

func (a *App) writeCachedNetworkViewsEntry(profile Profile, rows []map[string]any, cachedAt int64, staleExpiresAt int64) error {
	profileName := cacheProfileName(profile)
	return a.writeBadgerCacheEntry(cacheEntryKey("network_views", profileName), badgerCacheEntry{
		Kind:           "network_views",
		Profile:        profileName,
		CachedAt:       cachedAt,
		StaleExpiresAt: staleExpiresAt,
		Rows:           rows,
	})
}

func (a *App) readCachedNetworks(profile Profile, networkView string) (cachedPayload, error) {
	profileName := cacheProfileName(profile)
	networkView = strings.TrimSpace(networkView)
	entry, found, err := a.readBadgerCacheEntry(cacheEntryKey("networks", profileName, networkView))
	if err != nil {
		return cachedPayload{}, err
	}
	if !found {
		return cachedPayload{}, nil
	}
	return cachedPayloadFromBadger(entry), nil
}

func (a *App) writeCachedNetworks(profile Profile, networkView string, rows []map[string]any, now time.Time) error {
	return a.writeCachedNetworksEntry(profile, networkView, rows, now.Unix(), now.Add(a.recordsCacheSWRTTL()).Unix())
}

func (a *App) writeCachedNetworksEntry(profile Profile, networkView string, rows []map[string]any, cachedAt int64, staleExpiresAt int64) error {
	profileName := cacheProfileName(profile)
	networkView = strings.TrimSpace(networkView)
	return a.writeBadgerCacheEntry(cacheEntryKey("networks", profileName, networkView), badgerCacheEntry{
		Kind:           "networks",
		Profile:        profileName,
		NetworkView:    networkView,
		CachedAt:       cachedAt,
		StaleExpiresAt: staleExpiresAt,
		Rows:           rows,
	})
}

func (a *App) readCachedNetworkContainers(profile Profile, networkView string) (cachedPayload, error) {
	profileName := cacheProfileName(profile)
	networkView = strings.TrimSpace(networkView)
	entry, found, err := a.readBadgerCacheEntry(cacheEntryKey("network_containers", profileName, networkView))
	if err != nil {
		return cachedPayload{}, err
	}
	if !found {
		return cachedPayload{}, nil
	}
	return cachedPayloadFromBadger(entry), nil
}

func (a *App) writeCachedNetworkContainers(profile Profile, networkView string, rows []map[string]any, now time.Time) error {
	return a.writeCachedNetworkContainersEntry(profile, networkView, rows, now.Unix(), now.Add(a.recordsCacheSWRTTL()).Unix())
}

func (a *App) writeCachedNetworkContainersEntry(profile Profile, networkView string, rows []map[string]any, cachedAt int64, staleExpiresAt int64) error {
	profileName := cacheProfileName(profile)
	networkView = strings.TrimSpace(networkView)
	return a.writeBadgerCacheEntry(cacheEntryKey("network_containers", profileName, networkView), badgerCacheEntry{
		Kind:           "network_containers",
		Profile:        profileName,
		NetworkView:    networkView,
		CachedAt:       cachedAt,
		StaleExpiresAt: staleExpiresAt,
		Rows:           rows,
	})
}

func (a *App) readCachedIPv4Addresses(profile Profile, ip string, networkView string) (cachedPayload, error) {
	profileName := cacheProfileName(profile)
	ip = strings.TrimSpace(ip)
	networkView = strings.TrimSpace(networkView)
	entry, found, err := a.readBadgerCacheEntry(cacheEntryKey("ipv4_addresses", profileName, networkView, ip))
	if err != nil {
		return cachedPayload{}, err
	}
	if !found {
		return cachedPayload{}, nil
	}
	return cachedPayloadFromBadger(entry), nil
}

func (a *App) writeCachedIPv4Addresses(profile Profile, ip string, networkView string, rows []map[string]any, now time.Time) error {
	return a.writeCachedIPv4AddressesEntry(profile, ip, networkView, rows, now.Unix(), now.Add(a.recordsCacheSWRTTL()).Unix())
}

func (a *App) writeCachedIPv4AddressesEntry(profile Profile, ip string, networkView string, rows []map[string]any, cachedAt int64, staleExpiresAt int64) error {
	profileName := cacheProfileName(profile)
	ip = strings.TrimSpace(ip)
	networkView = strings.TrimSpace(networkView)
	return a.writeBadgerCacheEntry(cacheEntryKey("ipv4_addresses", profileName, networkView, ip), badgerCacheEntry{
		Kind:           "ipv4_addresses",
		Profile:        profileName,
		NetworkView:    networkView,
		IP:             ip,
		CachedAt:       cachedAt,
		StaleExpiresAt: staleExpiresAt,
		Rows:           rows,
	})
}

func (a *App) readCachedRecords(profile Profile, zone string) (cachedPayload, error) {
	started := time.Now()
	profileName, view := cacheScope(profile)
	zone = normalizeCacheZone(zone)
	entry, found, err := a.readBadgerCacheEntry(recordCacheKey(profileName, view, zone))
	if err != nil {
		a.debugEvent("cache records error", df("profile", profileName), df("view", view), df("zone", zone), df("duration", time.Since(started)), df("error", err.Error()))
		return cachedPayload{}, err
	}
	if !found {
		a.debugEvent("cache records miss", df("profile", profileName), df("view", view), df("zone", zone), df("duration", time.Since(started)))
		return cachedPayload{}, nil
	}
	payload := cachedPayloadFromBadger(entry)
	a.debugEvent("cache records hit", df("profile", profileName), df("view", view), df("zone", zone), df("serial", payload.Serial), df("rows", len(payload.Rows)), df("fresh", a.cacheEntryFresh(payload, time.Now())), df("stale_valid", time.Now().Unix() < payload.StaleExpiresAt), df("duration", time.Since(started)))
	return payload, nil
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

	_ = db.View(func(txn *badger.Txn) error {
		for _, zone := range normalizedZones {
			item, err := txn.Get(recordCacheKey(profileName, view, zone))
			if err != nil {
				continue
			}
			var entry badgerCacheEntry
			if err := item.Value(func(raw []byte) error {
				return json.Unmarshal(raw, &entry)
			}); err != nil {
				continue
			}
			entries[zone] = cachedPayloadFromBadger(entry)
		}
		return nil
	})
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
	profileName, view := cacheScope(profile)
	zone = normalizeCacheZone(zone)
	serial = cleanIntegerString(serial)
	err := a.writeBadgerCacheEntry(recordCacheKey(profileName, view, zone), badgerCacheEntry{
		Kind:           "records",
		Profile:        profileName,
		View:           view,
		Zone:           zone,
		Serial:         serial,
		CachedAt:       cachedAt,
		StaleExpiresAt: staleExpiresAt,
		Rows:           rows,
	})
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
	db, err := a.openCacheDB()
	if err != nil {
		return err
	}
	err = db.Update(func(txn *badger.Txn) error {
		key := recordCacheKey(profileName, view, zone)
		item, err := txn.Get(key)
		if errors.Is(err, badger.ErrKeyNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		var entry badgerCacheEntry
		if err := item.Value(func(raw []byte) error {
			return json.Unmarshal(raw, &entry)
		}); err != nil {
			return err
		}
		entry.CachedAt = cachedAt.Unix()
		entry.StaleExpiresAt = staleExpiresAt.Unix()
		raw, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		return txn.Set(key, raw)
	})
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
	_ = db.Update(func(txn *badger.Txn) error {
		return txn.Delete(cacheEntryKey("zones", profileName, view))
	})
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
	_ = db.Update(func(txn *badger.Txn) error {
		return txn.Delete(recordCacheKey(profileName, view, zone))
	})
}

func (a *App) invalidateRecordCache(profile Profile, zone string) {
	profileName, view := cacheScope(profile)
	zone = normalizeCacheZone(zone)
	db, err := a.openCacheDB()
	if err != nil {
		return
	}
	if zone == "" {
		_ = deleteBadgerPrefix(db, badgerKey("records", profileName, view))
		_ = deleteBadgerPrefix(db, badgerKey("record_refresh_locks", profileName, view))
		return
	}
	_ = db.Update(func(txn *badger.Txn) error {
		if err := txn.Delete(recordCacheKey(profileName, view, zone)); err != nil && !errors.Is(err, badger.ErrKeyNotFound) {
			return err
		}
		if err := txn.Delete(recordLeaseKey(profileName, view, zone)); err != nil && !errors.Is(err, badger.ErrKeyNotFound) {
			return err
		}
		return nil
	})
}

func (a *App) clearCache() error {
	path := a.cachePath()
	if err := a.closeCacheDB(path); err != nil {
		return err
	}
	// A full cache clear should reclaim Badger storage files too. Prefix deletes
	// or DropAll remove rows but can leave a large active sparse value-log file
	// behind, which is surprising for users running `ib config cache clear` to
	// free space.
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	return a.protectCacheFileForScope(false)
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
	for _, prefix := range []string{
		"zones",
		"records",
		"record_refresh_locks",
		"network_views",
		"networks",
		"network_containers",
		"ipv4_addresses",
		"net_refresh_locks",
	} {
		if err := deleteBadgerPrefix(db, badgerKey(prefix, profileName)); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) tryAcquireRecordRefreshLease(profile Profile, zone string, now time.Time, ttl time.Duration) (bool, error) {
	profileName, view := cacheScope(profile)
	zone = normalizeCacheZone(zone)
	key := recordLeaseKey(profileName, view, zone)
	db, err := a.openCacheDB()
	if err != nil {
		return false, err
	}

	nowUnix := now.Unix()
	// The lease is advisory and local to the cache DB. It prevents a burst of
	// list/search commands from spawning duplicate refresh subprocesses for the
	// same profile, view, and zone while still expiring after crashes.
	acquired := false
	err = db.Update(func(txn *badger.Txn) error {
		item, err := txn.Get(key)
		if err == nil {
			var existing badgerLeaseEntry
			if valueErr := item.Value(func(raw []byte) error {
				return json.Unmarshal(raw, &existing)
			}); valueErr != nil {
				return valueErr
			}
			if existing.ExpiresAt > nowUnix {
				return nil
			}
		} else if !errors.Is(err, badger.ErrKeyNotFound) {
			return err
		}
		raw, err := json.Marshal(badgerLeaseEntry{
			Profile:   profileName,
			View:      view,
			Zone:      zone,
			StartedAt: nowUnix,
			ExpiresAt: now.Add(ttl).Unix(),
		})
		if err != nil {
			return err
		}
		if err := txn.Set(key, raw); err != nil {
			return err
		}
		acquired = true
		return nil
	})
	return acquired, err
}

func (a *App) releaseRecordRefreshLease(profile Profile, zone string) error {
	profileName, view := cacheScope(profile)
	zone = normalizeCacheZone(zone)
	key := recordLeaseKey(profileName, view, zone)
	db, err := a.openCacheDB()
	if err != nil {
		return err
	}
	return db.Update(func(txn *badger.Txn) error {
		err := txn.Delete(key)
		if errors.Is(err, badger.ErrKeyNotFound) {
			return nil
		}
		return err
	})
}

func (a *App) recordRefreshLeaseActive(profile Profile, zone string, now time.Time) (bool, error) {
	profileName, view := cacheScope(profile)
	zone = normalizeCacheZone(zone)
	key := recordLeaseKey(profileName, view, zone)
	db, err := a.openCacheDB()
	if err != nil {
		return false, err
	}

	var lease badgerLeaseEntry
	err = db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(key)
		if errors.Is(err, badger.ErrKeyNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		return item.Value(func(raw []byte) error {
			return json.Unmarshal(raw, &lease)
		})
	})
	if err != nil || lease.ExpiresAt == 0 {
		return false, err
	}
	return lease.ExpiresAt > now.Unix(), nil
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
	key := netLeaseKey(profileName, kind, scope)
	db, err := a.openCacheDB()
	if err != nil {
		return false, err
	}

	nowUnix := now.Unix()
	acquired := false
	err = db.Update(func(txn *badger.Txn) error {
		item, err := txn.Get(key)
		if err == nil {
			var existing badgerLeaseEntry
			if valueErr := item.Value(func(raw []byte) error {
				return json.Unmarshal(raw, &existing)
			}); valueErr != nil {
				return valueErr
			}
			if existing.ExpiresAt > nowUnix {
				return nil
			}
		} else if !errors.Is(err, badger.ErrKeyNotFound) {
			return err
		}
		raw, err := json.Marshal(badgerLeaseEntry{
			Profile:   profileName,
			Kind:      kind,
			Scope:     scope,
			StartedAt: nowUnix,
			ExpiresAt: now.Add(ttl).Unix(),
		})
		if err != nil {
			return err
		}
		if err := txn.Set(key, raw); err != nil {
			return err
		}
		acquired = true
		return nil
	})
	return acquired, err
}

func (a *App) releaseNetRefreshLease(profile Profile, kind string, networkView string, ip string) error {
	profileName := cacheProfileName(profile)
	scope := netRefreshScope(kind, networkView, ip)
	key := netLeaseKey(profileName, kind, scope)
	db, err := a.openCacheDB()
	if err != nil {
		return err
	}
	return db.Update(func(txn *badger.Txn) error {
		err := txn.Delete(key)
		if errors.Is(err, badger.ErrKeyNotFound) {
			return nil
		}
		return err
	})
}

func (a *App) netRefreshLeaseActive(profile Profile, kind string, networkView string, ip string, now time.Time) (bool, error) {
	profileName := cacheProfileName(profile)
	scope := netRefreshScope(kind, networkView, ip)
	key := netLeaseKey(profileName, kind, scope)
	db, err := a.openCacheDB()
	if err != nil {
		return false, err
	}

	var lease badgerLeaseEntry
	err = db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(key)
		if errors.Is(err, badger.ErrKeyNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		return item.Value(func(raw []byte) error {
			return json.Unmarshal(raw, &lease)
		})
	})
	if err != nil || lease.ExpiresAt == 0 {
		return false, err
	}
	return lease.ExpiresAt > now.Unix(), nil
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

	nowTime := time.Now()
	now := nowTime.Unix()
	snapshot := cacheStatusSnapshot{Entries: []map[string]any{}}
	if err := db.View(func(txn *badger.Txn) error {
		for _, kind := range []string{"zones", "records", "network_views", "networks", "network_containers", "ipv4_addresses"} {
			options := badger.DefaultIteratorOptions
			options.PrefetchValues = true
			iterator := txn.NewIterator(options)
			prefix := badgerKey(kind)
			for iterator.Seek(prefix); iterator.ValidForPrefix(prefix); iterator.Next() {
				item := iterator.Item()
				var entry badgerCacheEntry
				if err := item.Value(func(raw []byte) error {
					return json.Unmarshal(raw, &entry)
				}); err != nil {
					iterator.Close()
					return err
				}
				itemCount := len(entry.Rows)
				staleExpiry := ""
				if cacheKindUsesSWR(kind) {
					staleExpiry = cacheExpiryText(now, entry.StaleExpiresAt)
				}
				zone := entry.Zone
				if kind == "ipv4_addresses" {
					zone = entry.IP
				}
				view := entry.View
				if view == "" {
					view = entry.NetworkView
				}
				snapshot.Entries = append(snapshot.Entries, map[string]any{
					"kind":          kind,
					"profile":       entry.Profile,
					"view":          view,
					"zone":          zone,
					"serial":        cleanIntegerString(entry.Serial),
					"items":         strconv.Itoa(itemCount),
					"age":           cacheRelativeDuration(now - entry.CachedAt),
					"stale_expires": staleExpiry,
				})
				snapshot.Statistics.addEntry(kind, itemCount, entry.StaleExpiresAt, a.cacheFreshUntil(entry.CachedAt), nowTime)
			}
			iterator.Close()
		}
		for _, prefix := range [][]byte{badgerKey("record_refresh_locks"), badgerKey("net_refresh_locks")} {
			options := badger.DefaultIteratorOptions
			iterator := txn.NewIterator(options)
			for iterator.Seek(prefix); iterator.ValidForPrefix(prefix); iterator.Next() {
				item := iterator.Item()
				var lease badgerLeaseEntry
				if err := item.Value(func(raw []byte) error {
					return json.Unmarshal(raw, &lease)
				}); err != nil {
					iterator.Close()
					return err
				}
				if lease.ExpiresAt > now {
					snapshot.Statistics.ActiveRefreshes++
				}
			}
			iterator.Close()
		}
		return nil
	}); err != nil {
		return cacheStatusSnapshot{}, err
	}
	sortCacheStatusEntries(snapshot.Entries)
	return snapshot, nil
}

func deleteBadgerPrefixes(db *badger.DB, prefixes []string) error {
	for _, prefix := range prefixes {
		if err := deleteBadgerPrefix(db, badgerKey(prefix)); err != nil {
			return err
		}
	}
	return nil
}

func deleteBadgerPrefix(db *badger.DB, prefix []byte) error {
	return db.Update(func(txn *badger.Txn) error {
		options := badger.DefaultIteratorOptions
		options.PrefetchValues = false
		iterator := txn.NewIterator(options)
		defer iterator.Close()
		var keys [][]byte
		for iterator.Seek(prefix); iterator.ValidForPrefix(prefix); iterator.Next() {
			key := append([]byte(nil), iterator.Item().Key()...)
			keys = append(keys, key)
		}
		for _, key := range keys {
			if err := txn.Delete(key); err != nil && !errors.Is(err, badger.ErrKeyNotFound) {
				return err
			}
		}
		return nil
	})
}

func sortCacheStatusEntries(entries []map[string]any) {
	sort.SliceStable(entries, func(i, j int) bool {
		for _, key := range []string{"kind", "profile", "view", "zone"} {
			left := cleanString(entries[i][key])
			right := cleanString(entries[j][key])
			if left == right {
				continue
			}
			return left < right
		}
		return false
	})
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
