package ibcli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestCommandSettingsSnapshotAndExternalEdits(t *testing.T) {
	app := testApp(t)
	profiles := map[string]Profile{"default": {Name: "default", Server: "https://grid.invalid"}}
	settings := defaultConfigSettings()
	settings.CacheTTLSeconds = 60
	if err := app.writeConfigProfilesWithSettings("default", profiles, settings); err != nil {
		t.Fatal(err)
	}
	app.beginCommandSettings()
	defer app.endCommandSettings()
	if got := app.cacheTTL(); got != time.Minute {
		t.Fatalf("cache TTL = %s", got)
	}
	// Removing the file makes any accidental re-read return default settings.
	if err := os.Remove(app.ConfigFile); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 100; n++ {
				if got := app.cacheTTL(); got != time.Minute {
					t.Errorf("concurrent snapshot TTL = %s", got)
				}
			}
		}()
	}
	wg.Wait()
	app.endCommandSettings()
	app.beginCommandSettings()
	if got := app.cacheTTL(); got != time.Duration(defaultCacheTTLSeconds)*time.Second {
		t.Fatalf("next command did not see external edit: TTL = %s", got)
	}
}

func TestCommandSettingsInvalidatedByWrite(t *testing.T) {
	app := testApp(t)
	app.beginCommandSettings()
	defer app.endCommandSettings()
	_ = app.configSettings() // Cache the no-config defaults.
	settings := defaultConfigSettings()
	settings.CacheTTLSeconds = 42
	profiles := map[string]Profile{"default": {Name: "default", Server: "https://grid.invalid"}}
	if err := app.writeConfigProfilesWithSettings("default", profiles, settings); err != nil {
		t.Fatal(err)
	}
	if got := app.cacheTTL(); got != 42*time.Second {
		t.Fatalf("settings remained stale after config write: %s", got)
	}
}

func TestDirectSettingsReadsRemainFresh(t *testing.T) {
	app := testApp(t)
	_ = app.configSettings()
	profiles := map[string]Profile{"default": {Name: "default", Server: "https://grid.invalid"}}
	writePlainTestConfig(t, app.ConfigFile, "default", profiles, "")
	raw, err := os.ReadFile(app.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(app.ConfigFile, bytes.Replace(raw, []byte("cache_ttl = 300"), []byte("cache_ttl = 42"), 1), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := app.configSettings().CacheTTLSeconds; got != 42 {
		t.Fatalf("direct helper settings TTL = %d", got)
	}
	if err := os.Remove(app.ConfigFile); err != nil {
		t.Fatal(err)
	}
	if got := app.configSettings().CacheTTLSeconds; got != defaultCacheTTLSeconds {
		t.Fatalf("direct helper did not observe removed config: TTL = %d", got)
	}
	if app.settingsCache != nil {
		t.Fatal("direct helper reads unexpectedly created a persistent snapshot")
	}
}

func TestCommandSettingsPreserveLocalOverGlobalPrecedence(t *testing.T) {
	if !globalConfigSupported() {
		t.Skip("global config not supported")
	}
	app := testApp(t)
	profiles := map[string]Profile{"default": {Name: "default", Server: "https://grid.invalid"}}
	writePlainTestConfig(t, app.GlobalConfigFile, "default", profiles, "")
	settings := defaultConfigSettings()
	settings.CacheTTLSeconds = 42
	if err := app.writeConfigProfilesWithSettings("default", profiles, settings); err != nil {
		t.Fatal(err)
	}
	app.beginCommandSettings()
	defer app.endCommandSettings()
	app.useConfigLocation(app.globalConfigLocation())
	if got := app.cacheTTL(); got != 42*time.Second {
		t.Fatalf("global active profile changed merged local setting: %s", got)
	}
	if err := os.Remove(app.LocalConfigFile); err != nil {
		t.Fatal(err)
	}
	app.useConfigLocation(app.localConfigLocation())
	if got := app.cacheTTL(); got != time.Duration(defaultCacheTTLSeconds)*time.Second {
		t.Fatalf("location change did not invalidate settings snapshot: %s", got)
	}
}

func TestWAPIClientSkipsDisabledDebugHook(t *testing.T) {
	app := testApp(t)
	profile := Profile{Server: "https://grid.invalid"}
	if client := app.newClient(profile); client.debug != nil {
		t.Fatal("disabled debug should not install a request logging hook")
	}
	app.Debug = true
	if client := app.newClient(profile); client.debug == nil {
		t.Fatal("enabled debug did not install a request logging hook")
	}
}

func TestExecuteResetsSettingsSnapshot(t *testing.T) {
	app := testApp(t)
	app.Stdout = io.Discard
	app.beginCommandSettings()
	_ = app.configSettings()
	if err := app.Execute([]string{"-v"}); err != nil {
		t.Fatal(err)
	}
	if app.settingsCache != nil || app.settingsCacheEnabled {
		t.Fatal("Execute retained settings past the command boundary")
	}
	if err := app.Execute([]string{"--unknown-option"}); err == nil {
		t.Fatal("expected usage error")
	}
	if app.settingsCache != nil || app.settingsCacheEnabled {
		t.Fatal("Execute retained settings after an error")
	}
}

func TestCacheHitSkipsDebugOnlyFreshnessComputation(t *testing.T) {
	app := testApp(t)
	profile := Profile{Name: "default", DNSView: "default"}
	if err := app.writeCachedZones(profile, []map[string]any{{"fqdn": "example.com"}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := app.writeCachedRecords(profile, "example.com", "1", []map[string]any{{"name": "app.example.com"}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	app.beginCommandSettings()
	defer app.endCommandSettings()
	if _, err := app.readCachedZones(profile); err != nil {
		t.Fatal(err)
	}
	if _, err := app.readCachedRecords(profile, "example.com"); err != nil {
		t.Fatal(err)
	}
	if app.settingsCache != nil {
		t.Fatal("disabled debug log computed freshness and loaded config")
	}
	app.Debug = true
	var log bytes.Buffer
	app.Stderr = &log
	if _, err := app.readCachedZones(profile); err != nil {
		t.Fatal(err)
	}
	if app.settingsCache == nil || !bytes.Contains(log.Bytes(), []byte("fresh=true")) {
		t.Fatalf("enabled debug did not retain freshness log: %s", log.String())
	}
}

func BenchmarkConfigSettings(b *testing.B) {
	dir := b.TempDir()
	app := &App{ConfigDir: dir, ConfigFile: filepath.Join(dir, "config"), ConfigKeyFile: filepath.Join(dir, "key"), GlobalConfigDir: filepath.Join(dir, "global")}
	raw := "[meta]\ndefault_profile = default\ncache_ttl = 60\n[profile:default]\nserver = https://grid.invalid\n"
	if err := os.WriteFile(app.ConfigFile, []byte(raw), 0o600); err != nil {
		b.Fatal(err)
	}
	for _, cached := range []bool{false, true} {
		b.Run(fmt.Sprintf("snapshot=%v", cached), func(b *testing.B) {
			if cached {
				app.beginCommandSettings()
				defer app.endCommandSettings()
			}
			_ = app.configSettings()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if app.configSettings().CacheTTLSeconds != 60 {
					b.Fatal("wrong settings")
				}
			}
		})
	}
}
