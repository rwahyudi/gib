package ibcli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	app := &App{
		ConfigDir:     dir,
		ConfigFile:    filepath.Join(dir, "config"),
		ConfigKeyFile: filepath.Join(dir, "key"),
		Output:        tableOutput,
		Stdout:        os.Stdout,
		Stderr:        os.Stderr,
		Stdin:         strings.NewReader(""),
	}
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)
	return app
}

func writeConfigForSettings(t *testing.T, app *App, settings ConfigSettings) {
	t.Helper()
	profiles := map[string]Profile{
		"default": {
			Name:        "default",
			Server:      "https://infoblox.example",
			Username:    "admin",
			Password:    "secret-password",
			WAPIVersion: defaultWAPIVersion,
			DNSView:     "default",
			DefaultZone: "example.com",
			VerifySSL:   true,
			Timeout:     defaultTimeoutSeconds,
		},
	}
	if err := app.writeConfigProfilesWithSettings("default", profiles, settings); err != nil {
		t.Fatalf("write config settings: %v", err)
	}
}

func TestWriteConfigProfilesEncryptsPasswordAndReadsItBack(t *testing.T) {
	app := testApp(t)
	profiles := map[string]Profile{
		"default": {
			Name:        "default",
			Server:      "https://infoblox.example",
			Username:    "admin",
			Password:    "secret-password",
			WAPIVersion: defaultWAPIVersion,
			DNSView:     "default",
			DefaultZone: "example.com",
			VerifySSL:   true,
			Timeout:     defaultTimeoutSeconds,
		},
	}
	if err := app.writeConfigProfiles("default", profiles); err != nil {
		t.Fatalf("write profiles: %v", err)
	}
	raw, err := os.ReadFile(app.ConfigFile)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if strings.Contains(string(raw), "secret-password") {
		t.Fatalf("password was written in plaintext:\n%s", raw)
	}
	for _, want := range []string{
		"cache_ttl = 300",
		"dns_search_worker_limit = 16",
		"records_cache_swr_ttl = 259200",
	} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("config missing %q:\n%s", want, raw)
		}
	}
	defaultProfile, loaded, _, err := app.readConfigProfiles(true)
	if err != nil {
		t.Fatalf("read profiles: %v", err)
	}
	if defaultProfile != "default" {
		t.Fatalf("default profile = %q", defaultProfile)
	}
	if loaded["default"].Password != "secret-password" {
		t.Fatalf("loaded password = %q", loaded["default"].Password)
	}
}

func TestLoadConfigBackfillsMissingGlobalSettings(t *testing.T) {
	app := testApp(t)
	raw := `[meta]
default_profile = default

[profile:default]
server = https://infoblox.example
read_server = 
username = admin
password = secret-password
wapi_version = v2.12.3
dns_view = default
default_zone = example.com
verify_ssl = true
timeout = 30
`
	if err := os.WriteFile(app.ConfigFile, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	profile, err := app.loadConfig(true)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if profile.Name != "default" {
		t.Fatalf("profile name = %q", profile.Name)
	}
	updated, err := os.ReadFile(app.ConfigFile)
	if err != nil {
		t.Fatalf("read updated config: %v", err)
	}
	for _, want := range []string{
		"cache_ttl = 300",
		"dns_search_worker_limit = 16",
		"records_cache_swr_ttl = 259200",
	} {
		if !strings.Contains(string(updated), want) {
			t.Fatalf("updated config missing %q:\n%s", want, updated)
		}
	}
	if strings.Contains(string(updated), "secret-password") {
		t.Fatalf("updated config left password plaintext:\n%s", updated)
	}
}

func TestReadConfigSettingsFallsBackForInvalidValues(t *testing.T) {
	app := testApp(t)
	raw := `[meta]
default_profile = default
cache_ttl = not-a-number
dns_search_worker_limit = -5
records_cache_swr_ttl = 0
`
	if err := os.WriteFile(app.ConfigFile, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	settings, missing, err := app.readConfigSettings()
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if !missing {
		t.Fatalf("missing = false, want true for invalid values")
	}
	if settings.CacheTTLSeconds != defaultCacheTTLSeconds {
		t.Fatalf("cache ttl = %d, want %d", settings.CacheTTLSeconds, defaultCacheTTLSeconds)
	}
	if settings.DNSSearchWorkerLimit != defaultDNSSearchWorkerLimit {
		t.Fatalf("worker limit = %d, want %d", settings.DNSSearchWorkerLimit, defaultDNSSearchWorkerLimit)
	}
	if settings.RecordsCacheSWRSeconds != defaultRecordsCacheSWRSeconds {
		t.Fatalf("records cache swr ttl = %d, want %d", settings.RecordsCacheSWRSeconds, defaultRecordsCacheSWRSeconds)
	}
}

func TestDNSContextLineUsesCompactColonFormat(t *testing.T) {
	app := testApp(t)
	profiles := map[string]Profile{
		"TestDemo": {
			Name:        "TestDemo",
			Server:      "https://infoblox.example",
			Username:    "admin",
			Password:    "secret-password",
			WAPIVersion: defaultWAPIVersion,
			DNSView:     "DNS Zone View",
			DefaultZone: "example.com",
			VerifySSL:   true,
			Timeout:     defaultTimeoutSeconds,
		},
	}
	if err := app.writeConfigProfiles("TestDemo", profiles); err != nil {
		t.Fatalf("write profiles: %v", err)
	}

	line := app.dnsContextLine()
	for _, want := range []string{
		"Current Context:",
		"Profile: TestDemo",
		"View: DNS Zone View",
		"Zone: example.com",
		"(configured default)",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("context line missing %q:\n%s", want, line)
		}
	}
	for _, unwanted := range []string{"Current DNS Context", "Profile=", "View=", "Zone="} {
		if strings.Contains(line, unwanted) {
			t.Fatalf("context line contains old format %q:\n%s", unwanted, line)
		}
	}
}

func TestConfigOverviewWithoutProfilesUsesSetupPanel(t *testing.T) {
	app := testApp(t)
	var stdout, stderr bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &stderr
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"configure"}); err != nil {
		t.Fatalf("configure overview: %v", err)
	}
	output := stdout.String()
	for _, want := range []string{
		"Config Usage",
		"Create a profile first; credentials are encrypted.",
		"ib config new [PROFILE]",
		"server, username, password, WAPI/TLS",
		"read endpoint, DNS view, default zone",
		"runs before saving; retry on failure",
		"ib config completion [bash|zsh|fish]",
		"ib config cache status|clear",
		"ib config <command> --help",
		"ib config / configure",
		"Key file",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("configure overview missing %q:\n%s", want, output)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("configure overview wrote stderr:\n%s", stderr.String())
	}
}

func TestConfigOverviewWithoutProfilesKeepsMachineReadableOutput(t *testing.T) {
	app := testApp(t)
	var stdout, stderr bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &stderr
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"-o", "jq", "configure"}); err != nil {
		t.Fatalf("configure overview: %v", err)
	}
	output := strings.TrimSpace(stdout.String())
	if output != "[]" {
		t.Fatalf("configure jq output = %q, want []", output)
	}
	if stderr.Len() != 0 {
		t.Fatalf("configure jq wrote stderr:\n%s", stderr.String())
	}
}

func TestConfigOverviewWithProfilesShowsActionPanel(t *testing.T) {
	app := testApp(t)
	profiles := map[string]Profile{
		"default": {
			Name:        "default",
			Server:      "https://infoblox.example",
			Username:    "admin",
			Password:    "secret-password",
			WAPIVersion: defaultWAPIVersion,
			DNSView:     "DNS Zone View",
			DefaultZone: "example.com",
			VerifySSL:   true,
			Timeout:     defaultTimeoutSeconds,
		},
	}
	if err := app.writeConfigProfiles("default", profiles); err != nil {
		t.Fatalf("write profiles: %v", err)
	}
	var stdout bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &bytes.Buffer{}
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"configure"}); err != nil {
		t.Fatalf("configure overview: %v", err)
	}
	output := stdout.String()
	for _, want := range []string{
		"Infoblox Profiles (1)",
		"Config Usage",
		"Manage saved profiles, shell completion, and local cache data.",
		"ib config new [PROFILE]",
		"ib config edit [PROFILE]",
		"ib config use PROFILE",
		"ib config list",
		"ib config delete PROFILE",
		"ib config completion [bash|zsh|fish]",
		"ib config cache status|clear",
		"ib config <command> --help",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("configure overview missing %q:\n%s", want, output)
		}
	}
	for _, unwanted := range []string{"Validation", "Retry"} {
		if strings.Contains(output, unwanted) {
			t.Fatalf("configure overview contains unwanted %q:\n%s", unwanted, output)
		}
	}
}

func TestConfigDeleteClearsProfileCache(t *testing.T) {
	app := testApp(t)
	profiles := map[string]Profile{
		"default": {
			Name:        "default",
			Server:      "https://infoblox.example",
			Username:    "admin",
			Password:    "secret-password",
			WAPIVersion: defaultWAPIVersion,
			DNSView:     "default",
			DefaultZone: "example.com",
			VerifySSL:   true,
			Timeout:     defaultTimeoutSeconds,
		},
		"old": {
			Name:        "old",
			Server:      "https://old-infoblox.example",
			Username:    "admin",
			Password:    "old-secret-password",
			WAPIVersion: defaultWAPIVersion,
			DNSView:     "default",
			DefaultZone: "old.example.com",
			VerifySSL:   true,
			Timeout:     defaultTimeoutSeconds,
		},
	}
	if err := app.writeConfigProfiles("default", profiles); err != nil {
		t.Fatalf("write profiles: %v", err)
	}
	defaultProfile := profiles["default"]
	oldProfile := profiles["old"]
	now := time.Now()
	if err := app.writeCachedZones(defaultProfile, []map[string]any{{"fqdn": "example.com"}}, now); err != nil {
		t.Fatalf("write default zone cache: %v", err)
	}
	if err := app.writeCachedRecords(defaultProfile, "example.com", "2026050801", []map[string]any{{"name": "app.example.com"}}, now); err != nil {
		t.Fatalf("write default record cache: %v", err)
	}
	if err := app.writeCachedZones(oldProfile, []map[string]any{{"fqdn": "old.example.com"}}, now); err != nil {
		t.Fatalf("write old zone cache: %v", err)
	}
	if err := app.writeCachedRecords(oldProfile, "old.example.com", "2026050801", []map[string]any{{"name": "app.old.example.com"}}, now); err != nil {
		t.Fatalf("write old record cache: %v", err)
	}
	acquired, err := app.tryAcquireRecordRefreshLease(oldProfile, "old.example.com", now, time.Minute)
	if err != nil {
		t.Fatalf("acquire old refresh lease: %v", err)
	}
	if !acquired {
		t.Fatalf("old refresh lease was not acquired")
	}
	var stdout bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &bytes.Buffer{}
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"config", "delete", "old"}); err != nil {
		t.Fatalf("config delete old: %v", err)
	}
	if !strings.Contains(stdout.String(), "deleted and cache cleared") {
		t.Fatalf("delete output did not mention cache clear:\n%s", stdout.String())
	}
	_, loadedProfiles, _, err := app.readConfigProfiles(false)
	if err != nil {
		t.Fatalf("read profiles after delete: %v", err)
	}
	if _, ok := loadedProfiles["old"]; ok {
		t.Fatalf("old profile still exists after delete: %#v", loadedProfiles)
	}
	rows, err := app.cacheStatusRows()
	if err != nil {
		t.Fatalf("cache status after delete: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("cache status row count = %d, want only default zone and record rows: %#v", len(rows), rows)
	}
	for _, row := range rows {
		if row["profile"] == "old" {
			t.Fatalf("old profile cache row was not cleared: %#v", rows)
		}
		if row["profile"] != "default" {
			t.Fatalf("unexpected cache row after delete: %#v", row)
		}
	}
	acquired, err = app.tryAcquireRecordRefreshLease(oldProfile, "old.example.com", now.Add(time.Second), time.Minute)
	if err != nil {
		t.Fatalf("re-acquire old refresh lease after delete: %v", err)
	}
	if !acquired {
		t.Fatalf("old profile refresh lease was not cleared")
	}
}

func TestConfigureSummaryOmitsPassword(t *testing.T) {
	app := testApp(t)
	var stdout bytes.Buffer
	app.Stdout = &stdout

	app.printConfigureSummary(Profile{
		Name:        "default",
		Server:      "https://infoblox.example",
		Username:    "admin",
		Password:    "secret-password",
		WAPIVersion: defaultWAPIVersion,
		DNSView:     "DNS Zone View",
		VerifySSL:   false,
		Timeout:     defaultTimeoutSeconds,
	}, true)

	output := stdout.String()
	for _, want := range []string{
		"Profile Saved",
		"The profile is ready for DNS commands.",
		"Profile",
		"default",
		"Password",
		"encrypted",
		"primary server",
		"not set",
		"Verify SSL",
		"no",
		"Edit later",
		"ib config edit default",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("configure summary missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "secret-password") {
		t.Fatalf("configure summary exposed password:\n%s", output)
	}
}

func TestConfigureNewProfileNamePromptStartsBlankAndDefaultsOnEnter(t *testing.T) {
	server := newConfigSuccessServer(t)
	app := testApp(t)
	var stdout, stderr bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &stderr
	app.Stdin = strings.NewReader(strings.Join([]string{
		"",
		server.URL,
		"admin",
		"secret",
		"",
		"n",
		"",
		"",
	}, "\n") + "\n")
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"configure", "new"}); err != nil {
		t.Fatalf("configure new: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "Profile name:") {
		t.Fatalf("configure new did not prompt for profile name:\n%s", output)
	}
	if strings.Contains(output, "Profile name [default]") {
		t.Fatalf("profile prompt was prepopulated with default:\n%s", output)
	}
	for _, want := range []string{
		"\n   Checking Infoblox credentials and WAPI access...",
		"\n   SUCCESS: Infoblox connection test passed.",
		"07 Default DNS Zone",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("configure status line missing indentation %q:\n%s", want, output)
		}
	}
	for _, unwanted := range []string{
		"\nChecking Infoblox credentials and WAPI access...",
		"\nSUCCESS: Infoblox connection test passed.",
	} {
		if strings.Contains(output, unwanted) {
			t.Fatalf("configure status line is not indented %q:\n%s", unwanted, output)
		}
	}
	defaultProfile, profiles, _, err := app.readConfigProfiles(true)
	if err != nil {
		t.Fatalf("read profiles: %v", err)
	}
	if defaultProfile != defaultProfileName {
		t.Fatalf("default profile = %q, want %q", defaultProfile, defaultProfileName)
	}
	if _, ok := profiles[defaultProfileName]; !ok {
		t.Fatalf("default profile was not created: %#v", profiles)
	}
}

func TestConfigNewCanRetryDetailsAfterValidationError(t *testing.T) {
	server := newConfigSuccessServer(t)
	app := testApp(t)
	var stdout, stderr bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &stderr
	app.Stdin = strings.NewReader(strings.Join([]string{
		"",
		"",
		server.URL,
		"admin",
		"secret",
		"",
		"n",
		"",
		"",
	}, "\n") + "\n")
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"config", "new", "demo"}); err != nil {
		t.Fatalf("config new retry: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "Configuration failed: Infoblox server is required") {
		t.Fatalf("retry warning missing validation error:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "Try entering the details again") {
		t.Fatalf("retry confirmation prompt missing:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "07 Default DNS Zone") {
		t.Fatalf("default zone was not question 7:\n%s", stdout.String())
	}
	if strings.Contains(stdout.String(), "Configure a default DNS zone for this profile") {
		t.Fatalf("configure default zone confirmation should not be shown:\n%s", stdout.String())
	}
	_, profiles, _, err := app.readConfigProfiles(true)
	if err != nil {
		t.Fatalf("read profiles: %v", err)
	}
	if _, ok := profiles["demo"]; !ok {
		t.Fatalf("demo profile was not created after retry: %#v", profiles)
	}
}

func TestConfigureNewConnectionFailureShowsRetryPopup(t *testing.T) {
	failServer := newConfigFailureServer(t)
	successServer := newConfigSuccessServer(t)
	app := testApp(t)
	var stdout, stderr bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &stderr
	app.Stdin = strings.NewReader(strings.Join([]string{
		"",
		failServer.URL,
		"admin",
		"secret",
		"",
		"n",
		"y",
		successServer.URL,
		"admin",
		"secret",
		"",
		"n",
		"",
		"",
	}, "\n") + "\n")
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"configure", "new"}); err != nil {
		t.Fatalf("configure new retry: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{
		"Connection Test Failed",
		"The profile was not saved",
		"Do you want to retry?",
		"Profile Saved",
		"The profile is ready for DNS commands.",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("configure new retry output missing %q:\n%s", want, output)
		}
	}
}

func TestConfigureEditConnectionFailureShowsRetryPopup(t *testing.T) {
	failServer := newConfigFailureServer(t)
	app := testApp(t)
	profiles := map[string]Profile{
		defaultProfileName: {
			Name:        defaultProfileName,
			Server:      failServer.URL,
			Username:    "admin",
			Password:    "secret",
			WAPIVersion: defaultWAPIVersion,
			DNSView:     "default",
			VerifySSL:   false,
			Timeout:     defaultTimeoutSeconds,
		},
	}
	if err := app.writeConfigProfiles(defaultProfileName, profiles); err != nil {
		t.Fatalf("write profiles: %v", err)
	}
	var stdout, stderr bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &stderr
	app.Stdin = strings.NewReader(strings.Join([]string{
		"",
		"",
		"",
		"",
		"n",
		"n",
	}, "\n") + "\n")
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	err := app.Execute([]string{"configure", "edit"})
	if err == nil {
		t.Fatalf("configure edit returned nil; want connection failure")
	}
	if !isConnectionTestFailure(err) {
		t.Fatalf("configure edit error = %T %v, want connection test failure", err, err)
	}
	output := stdout.String()
	for _, want := range []string{
		"Connection Test Failed",
		"Infoblox connection test failed",
		"Do you want to retry?",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("configure edit retry output missing %q:\n%s", want, output)
		}
	}
}

func TestConfigureViewAndZonePromptsAreIndentedAndSequenced(t *testing.T) {
	server := newConfigViewSuccessServer(t)
	app := testApp(t)
	var stdout, stderr bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &stderr
	app.Stdin = strings.NewReader(strings.Join([]string{
		server.URL,
		"admin",
		"secret",
		"",
		"n",
		"",
		"",
	}, "\n") + "\n")
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"configure", "new", "demo"}); err != nil {
		t.Fatalf("configure new: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{
		"06 DNS View",
		"\n   Default DNS View\n",
		"07 Default DNS Zone",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("configure output missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "\nDefault DNS View\n") {
		t.Fatalf("default DNS view prompt was not indented:\n%s", output)
	}
	if strings.Contains(output, "Configure a default DNS zone for this profile") {
		t.Fatalf("configure default zone confirmation should not be shown:\n%s", output)
	}
}

func TestPromptReadServerPreservesCurrentWhenDiscoveryHasNoCandidates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/member") {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{}})
	}))
	defer server.Close()

	app := testApp(t)
	var stdout, stderr bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &stderr
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)
	profile := Profile{
		Name:        defaultProfileName,
		Server:      server.URL,
		Username:    "admin",
		Password:    "secret",
		WAPIVersion: defaultWAPIVersion,
		DNSView:     "default",
		VerifySSL:   true,
		Timeout:     defaultTimeoutSeconds,
	}
	selected, changed := app.promptReadServer(profile, "https://readonly.example")
	if changed {
		t.Fatalf("read server changed to %q, want no change", selected)
	}
}

func TestPromptReadServerClearsCurrentWhenReadOnlyAPIDisabled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/member") {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": []map[string]any{
				{"host_name": "gcm.example.com", "master_candidate": true, "enable_ro_api_access": false},
			},
		})
	}))
	defer server.Close()

	app := testApp(t)
	var stdout, stderr bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &stderr
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)
	profile := Profile{
		Name:        defaultProfileName,
		Server:      server.URL,
		Username:    "admin",
		Password:    "secret",
		WAPIVersion: defaultWAPIVersion,
		DNSView:     "default",
		VerifySSL:   true,
		Timeout:     defaultTimeoutSeconds,
	}
	selected, changed := app.promptReadServer(profile, "https://readonly.example")
	if !changed || selected != "" {
		t.Fatalf("selected=%q changed=%v, want explicit clear", selected, changed)
	}
	output := stdout.String()
	if !strings.Contains(output, "   WARNING: Grid Master Candidate gcm.example.com has Read-Only API disabled and will not be used.") {
		t.Fatalf("read-only warning was not indented:\nstdout:\n%s\nstderr:\n%s", output, stderr.String())
	}
	if strings.Contains(output, "\nWARNING: Grid Master Candidate") || strings.HasPrefix(output, "WARNING: Grid Master Candidate") {
		t.Fatalf("read-only warning should be indented:\n%s", output)
	}
	if stderr.Len() != 0 {
		t.Fatalf("read-only warning should stay in config output:\n%s", stderr.String())
	}
}

func TestPromptDefaultZoneSkipsSecondaryZones(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/zone_auth") {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": []map[string]any{
				{"fqdn": "primary.example.com", "view": "default", "zone_format": "FORWARD", "primary_type": "Grid"},
				{"fqdn": "secondary.example.com", "view": "default", "zone_format": "FORWARD", "primary_type": "External"},
			},
		})
	}))
	defer server.Close()

	app := testApp(t)
	var stdout bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &bytes.Buffer{}
	app.Stdin = strings.NewReader("\n")
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)
	selected := app.promptDefaultZone(Profile{
		Name:        defaultProfileName,
		Server:      server.URL,
		Username:    "admin",
		Password:    "secret",
		WAPIVersion: defaultWAPIVersion,
		DNSView:     "default",
		VerifySSL:   true,
		Timeout:     defaultTimeoutSeconds,
	}, "")

	if selected != "primary.example.com" {
		t.Fatalf("selected zone = %q, want primary.example.com", selected)
	}
	if strings.Contains(stdout.String(), "secondary.example.com") {
		t.Fatalf("secondary zone was shown in default-zone picker:\n%s", stdout.String())
	}
}

func newConfigSuccessServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/grid"):
			_ = json.NewEncoder(w).Encode([]map[string]any{{"name": "grid"}})
		case strings.HasSuffix(r.URL.Path, "/member"),
			strings.HasSuffix(r.URL.Path, "/view"),
			strings.HasSuffix(r.URL.Path, "/zone_auth"):
			_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func newConfigViewSuccessServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/grid"):
			_ = json.NewEncoder(w).Encode([]map[string]any{{"name": "grid"}})
		case strings.HasSuffix(r.URL.Path, "/view"):
			_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{{"name": "default"}, {"name": "DNS Zone View"}}})
		case strings.HasSuffix(r.URL.Path, "/member"),
			strings.HasSuffix(r.URL.Path, "/zone_auth"):
			_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func newConfigFailureServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/grid") {
			http.Error(w, "bad credentials", http.StatusUnauthorized)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)
	return server
}
