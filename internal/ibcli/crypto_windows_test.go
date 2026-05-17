//go:build windows

package ibcli

import (
	"os"
	"strings"
	"testing"
)

func TestWindowsDPAPIEncryptDecryptRoundTrip(t *testing.T) {
	app := testApp(t)
	encrypted, err := app.encryptCurrentPassword("secret-password")
	if err != nil {
		t.Fatalf("encrypt DPAPI password: %v", err)
	}
	if !strings.HasPrefix(encrypted, encryptedWindowsDPAPIPrefix) {
		t.Fatalf("encrypted password prefix = %q", encrypted)
	}
	plaintext, err := decryptWindowsDPAPIPassword(encrypted)
	if err != nil {
		t.Fatalf("decrypt DPAPI password: %v", err)
	}
	if plaintext != "secret-password" {
		t.Fatalf("plaintext = %q", plaintext)
	}
}

func TestWindowsEncryptPasswordMigratesLegacyFernet(t *testing.T) {
	app := testApp(t)
	if err := app.ensureConfigDir(); err != nil {
		t.Fatalf("ensure config dir: %v", err)
	}
	key, err := generateFernetKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	if err := os.WriteFile(app.ConfigKeyFile, []byte(key+"\n"), 0o600); err != nil {
		t.Fatalf("write legacy key: %v", err)
	}
	legacy, err := encryptFernet(key, "secret-password")
	if err != nil {
		t.Fatalf("encrypt legacy password: %v", err)
	}
	raw := `[meta]
default_profile = default
cache_ttl = 300
dns_search_worker_limit = 16
records_cache_swr_ttl = 259200
max_background_worker_wait = 3
completion_cache_prefetch = true

[profile:default]
server = https://infoblox.example
read_server =
username = admin
password = ` + legacy + `
wapi_version = v2.12.3
dns_view = default
default_zone = example.com
verify_ssl = true
timeout = 30
`
	if err := os.WriteFile(app.ConfigFile, []byte(raw), 0o600); err != nil {
		t.Fatalf("write legacy config: %v", err)
	}
	defaultProfile, profiles, _, err := app.readConfigProfiles(true)
	if err != nil {
		t.Fatalf("read legacy profiles: %v", err)
	}
	if profiles[defaultProfile].Password != "secret-password" {
		t.Fatalf("legacy password = %q", profiles[defaultProfile].Password)
	}
	if err := app.writeConfigProfilesWithSettings(defaultProfile, profiles, defaultConfigSettings()); err != nil {
		t.Fatalf("rewrite profiles: %v", err)
	}
	updated, err := os.ReadFile(app.ConfigFile)
	if err != nil {
		t.Fatalf("read updated config: %v", err)
	}
	if strings.Contains(string(updated), encryptedPasswordPrefix) {
		t.Fatalf("updated config still contains legacy password:\n%s", updated)
	}
	if !strings.Contains(string(updated), encryptedWindowsDPAPIPrefix) {
		t.Fatalf("updated config missing DPAPI password:\n%s", updated)
	}
}
