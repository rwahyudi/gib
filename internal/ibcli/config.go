package ibcli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var profileNameRE = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

const (
	defaultCacheTTLSeconds                = 300
	defaultDNSSearchWorkerLimit           = 16
	defaultRecordsCacheSWRSeconds         = 3 * 24 * 60 * 60
	defaultMaxBackgroundWorkerWaitSeconds = 3
	configCacheTTLKey                     = "cache_ttl"
	configDNSSearchWorkerLimitKey         = "dns_search_worker_limit"
	configRecordsCacheSWRKey              = "records_cache_swr_ttl"
	configMaxBackgroundWorkerWaitKey      = "max_background_worker_wait"
	configCompletionCachePrefetchKey      = "completion_cache_prefetch"
)

type Profile struct {
	Name        string
	Server      string
	ReadServer  string
	Username    string
	Password    string
	WAPIVersion string
	DNSView     string
	DefaultZone string
	VerifySSL   bool
	Timeout     int
}

type ConfigSettings struct {
	CacheTTLSeconds                int
	DNSSearchWorkerLimit           int
	RecordsCacheSWRSeconds         int
	MaxBackgroundWorkerWaitSeconds int
	CompletionCachePrefetch        bool
	completionCachePrefetchSet     bool
}

func defaultConfigSettings() ConfigSettings {
	return ConfigSettings{
		CacheTTLSeconds:                defaultCacheTTLSeconds,
		DNSSearchWorkerLimit:           defaultDNSSearchWorkerLimit,
		RecordsCacheSWRSeconds:         defaultRecordsCacheSWRSeconds,
		MaxBackgroundWorkerWaitSeconds: defaultMaxBackgroundWorkerWaitSeconds,
		CompletionCachePrefetch:        true,
		completionCachePrefetchSet:     true,
	}
}

func (s ConfigSettings) complete() ConfigSettings {
	defaults := defaultConfigSettings()
	if s.CacheTTLSeconds <= 0 {
		s.CacheTTLSeconds = defaults.CacheTTLSeconds
	}
	if s.DNSSearchWorkerLimit <= 0 {
		s.DNSSearchWorkerLimit = defaults.DNSSearchWorkerLimit
	}
	if s.RecordsCacheSWRSeconds <= 0 {
		s.RecordsCacheSWRSeconds = defaults.RecordsCacheSWRSeconds
	}
	if s.MaxBackgroundWorkerWaitSeconds <= 0 {
		s.MaxBackgroundWorkerWaitSeconds = defaults.MaxBackgroundWorkerWaitSeconds
	}
	if !s.completionCachePrefetchSet {
		s.CompletionCachePrefetch = defaults.CompletionCachePrefetch
		s.completionCachePrefetchSet = true
	}
	return s
}

func configSettingsFromSections(sections map[string]map[string]string) (ConfigSettings, bool) {
	settings := defaultConfigSettings()
	missing := false
	meta, ok := sections["meta"]
	if !ok {
		return settings, true
	}
	settings.CacheTTLSeconds, missing = positiveIntSetting(meta, configCacheTTLKey, settings.CacheTTLSeconds, missing)
	settings.DNSSearchWorkerLimit, missing = positiveIntSetting(meta, configDNSSearchWorkerLimitKey, settings.DNSSearchWorkerLimit, missing)
	settings.RecordsCacheSWRSeconds, missing = positiveIntSetting(meta, configRecordsCacheSWRKey, settings.RecordsCacheSWRSeconds, missing)
	settings.MaxBackgroundWorkerWaitSeconds, missing = positiveIntSetting(meta, configMaxBackgroundWorkerWaitKey, settings.MaxBackgroundWorkerWaitSeconds, missing)
	settings.CompletionCachePrefetch, settings.completionCachePrefetchSet, missing = boolSetting(meta, configCompletionCachePrefetchKey, settings.CompletionCachePrefetch, missing)
	return settings.complete(), missing
}

func positiveIntSetting(values map[string]string, key string, fallback int, missing bool) (int, bool) {
	raw, ok := values[key]
	if !ok || strings.TrimSpace(raw) == "" {
		return fallback, true
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || parsed <= 0 {
		return fallback, true
	}
	return parsed, missing
}

func boolSetting(values map[string]string, key string, fallback bool, missing bool) (bool, bool, bool) {
	raw, ok := values[key]
	if !ok || strings.TrimSpace(raw) == "" {
		return fallback, false, true
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "y", "on", "enable", "enabled":
		return true, true, missing
	case "0", "false", "no", "n", "off", "disable", "disabled":
		return false, true, missing
	default:
		return fallback, false, true
	}
}

func (a *App) readConfigSettings() (ConfigSettings, bool, error) {
	sections, err := readINI(a.ConfigFile)
	if err != nil {
		if os.IsNotExist(err) {
			return defaultConfigSettings(), true, nil
		}
		return ConfigSettings{}, false, err
	}
	settings, missing := configSettingsFromSections(sections)
	return settings, missing, nil
}

func (a *App) configSettings() ConfigSettings {
	settings, _, err := a.readConfigSettings()
	if err != nil {
		return defaultConfigSettings()
	}
	return settings
}

func (a *App) cacheTTL() time.Duration {
	return time.Duration(a.configSettings().CacheTTLSeconds) * time.Second
}

func (a *App) dnsSearchWorkerLimit() int {
	return a.configSettings().DNSSearchWorkerLimit
}

func (a *App) recordsCacheSWRTTL() time.Duration {
	return time.Duration(a.configSettings().RecordsCacheSWRSeconds) * time.Second
}

func (a *App) maxBackgroundWorkerWait() time.Duration {
	return time.Duration(a.configSettings().MaxBackgroundWorkerWaitSeconds) * time.Second
}

func (a *App) completionCachePrefetchEnabled() bool {
	return a.configSettings().CompletionCachePrefetch
}

func normalizeProfileName(profileName string) (string, error) {
	name := strings.TrimSpace(profileName)
	if name == "" {
		return "", cliError("profile name is required")
	}
	if !profileNameRE.MatchString(name) {
		return "", cliError("profile name may only contain letters, numbers, dots, underscores, and dashes")
	}
	return name, nil
}

func (p Profile) complete() Profile {
	if p.WAPIVersion == "" {
		p.WAPIVersion = defaultWAPIVersion
	}
	if p.DNSView == "" {
		p.DNSView = "default"
	}
	if p.Timeout == 0 {
		p.Timeout = defaultTimeoutSeconds
	}
	return p
}

func (p Profile) values() map[string]string {
	p = p.complete()
	return map[string]string{
		"server":       p.Server,
		"read_server":  p.ReadServer,
		"username":     p.Username,
		"password":     p.Password,
		"wapi_version": p.WAPIVersion,
		"dns_view":     p.DNSView,
		"default_zone": p.DefaultZone,
		"verify_ssl":   strconv.FormatBool(p.VerifySSL),
		"timeout":      strconv.Itoa(p.Timeout),
	}
}

func profileFromValues(name string, values map[string]string) Profile {
	timeout, _ := strconv.Atoi(strings.TrimSpace(values["timeout"]))
	profile := Profile{
		Name:        name,
		Server:      values["server"],
		ReadServer:  values["read_server"],
		Username:    values["username"],
		Password:    values["password"],
		WAPIVersion: values["wapi_version"],
		DNSView:     values["dns_view"],
		DefaultZone: values["default_zone"],
		VerifySSL:   parseBool(values["verify_ssl"], true),
		Timeout:     timeout,
	}
	return profile.complete()
}

func parseBool(value string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	case "":
		return fallback
	default:
		return fallback
	}
}

func (a *App) ensureConfigDir() error {
	if err := os.MkdirAll(a.ConfigDir, 0o700); err != nil {
		return err
	}
	return os.Chmod(a.ConfigDir, 0o700)
}

func (a *App) getOrCreateConfigKey() (string, error) {
	if raw, err := os.ReadFile(a.ConfigKeyFile); err == nil {
		_ = os.Chmod(a.ConfigKeyFile, 0o600)
		return strings.TrimSpace(string(raw)), nil
	}
	if err := a.ensureConfigDir(); err != nil {
		return "", err
	}
	key, err := generateFernetKey()
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(a.ConfigKeyFile, []byte(key+"\n"), 0o600); err != nil {
		return "", err
	}
	_ = os.Chmod(a.ConfigKeyFile, 0o600)
	return key, nil
}

func (a *App) readConfigKey() (string, error) {
	raw, err := os.ReadFile(a.ConfigKeyFile)
	if err != nil {
		return "", cliError("missing encryption key file at %s; run: ib config new [PROFILE]", a.ConfigKeyFile)
	}
	_ = os.Chmod(a.ConfigKeyFile, 0o600)
	return strings.TrimSpace(string(raw)), nil
}

func (a *App) encryptPassword(password string) (string, error) {
	if strings.HasPrefix(password, encryptedPasswordPrefix) {
		return password, nil
	}
	key, err := a.getOrCreateConfigKey()
	if err != nil {
		return "", err
	}
	return encryptFernet(key, password)
}

func (a *App) decryptPassword(password string) (string, error) {
	if !strings.HasPrefix(password, encryptedPasswordPrefix) {
		return password, nil
	}
	key, err := a.readConfigKey()
	if err != nil {
		return "", err
	}
	return decryptFernet(key, password)
}

func readINI(path string) (map[string]map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	sections := map[string]map[string]string{}
	current := ""
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.Contains(line, "]") {
			current = strings.TrimSpace(line[1:strings.Index(line, "]")])
			if _, ok := sections[current]; !ok {
				sections[current] = map[string]string{}
			}
			continue
		}
		if current == "" {
			continue
		}
		index := strings.Index(line, "=")
		if index < 0 {
			index = strings.Index(line, ":")
		}
		if index < 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(line[:index]))
		value := strings.TrimSpace(line[index+1:])
		sections[current][key] = value
	}
	return sections, scanner.Err()
}

func (a *App) readConfigProfiles(decrypt bool) (string, map[string]Profile, bool, error) {
	sections, err := readINI(a.ConfigFile)
	if err != nil {
		return "", nil, false, err
	}

	profiles := map[string]Profile{}
	for section, values := range sections {
		if !strings.HasPrefix(section, "profile:") {
			continue
		}
		name, err := normalizeProfileName(strings.TrimPrefix(section, "profile:"))
		if err != nil {
			return "", nil, false, err
		}
		profile := profileFromValues(name, values)
		if decrypt && profile.Password != "" {
			profile.Password, err = a.decryptPassword(profile.Password)
			if err != nil {
				return "", nil, false, cliError("cannot decrypt password in %s: %v", a.ConfigFile, err)
			}
		}
		profiles[name] = profile
	}

	if len(profiles) > 0 {
		meta, ok := sections["meta"]
		if !ok {
			return "", nil, false, cliError("missing [meta] section in %s", a.ConfigFile)
		}
		defaultProfile, err := normalizeProfileName(meta["default_profile"])
		if err != nil {
			return "", nil, false, err
		}
		if _, ok := profiles[defaultProfile]; !ok {
			return "", nil, false, cliError("default profile %q does not exist in %s", defaultProfile, a.ConfigFile)
		}
		return defaultProfile, profiles, false, nil
	}

	if values, ok := sections["default"]; ok {
		profile := profileFromValues(defaultProfileName, values)
		if decrypt && profile.Password != "" {
			profile.Password, err = a.decryptPassword(profile.Password)
			if err != nil {
				return "", nil, false, err
			}
		}
		return defaultProfileName, map[string]Profile{defaultProfileName: profile}, true, nil
	}

	return "", nil, false, cliError("no profiles configured in %s; run: ib config new [PROFILE]", a.ConfigFile)
}

func (a *App) writeConfigProfiles(defaultProfile string, profiles map[string]Profile) error {
	settings, _, err := a.readConfigSettings()
	if err != nil {
		settings = defaultConfigSettings()
	}
	return a.writeConfigProfilesWithSettings(defaultProfile, profiles, settings)
}

func (a *App) writeConfigProfilesWithSettings(defaultProfile string, profiles map[string]Profile, settings ConfigSettings) error {
	defaultProfile, err := normalizeProfileName(defaultProfile)
	if err != nil {
		return err
	}
	if _, ok := profiles[defaultProfile]; !ok {
		return cliError("default profile %q does not exist", defaultProfile)
	}
	if err := a.ensureConfigDir(); err != nil {
		return err
	}

	var builder strings.Builder
	settings = settings.complete()
	builder.WriteString("[meta]\n")
	builder.WriteString("default_profile = " + defaultProfile + "\n")
	builder.WriteString(configCacheTTLKey + " = " + strconv.Itoa(settings.CacheTTLSeconds) + "\n")
	builder.WriteString(configDNSSearchWorkerLimitKey + " = " + strconv.Itoa(settings.DNSSearchWorkerLimit) + "\n")
	builder.WriteString(configRecordsCacheSWRKey + " = " + strconv.Itoa(settings.RecordsCacheSWRSeconds) + "\n")
	builder.WriteString(configMaxBackgroundWorkerWaitKey + " = " + strconv.Itoa(settings.MaxBackgroundWorkerWaitSeconds) + "\n")
	builder.WriteString(configCompletionCachePrefetchKey + " = " + strconv.FormatBool(settings.CompletionCachePrefetch) + "\n\n")

	names := make([]string, 0, len(profiles))
	for name := range profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	keys := []string{"server", "read_server", "username", "password", "wapi_version", "dns_view", "default_zone", "verify_ssl", "timeout"}
	for _, name := range names {
		profile := profiles[name].complete()
		encryptedPassword, err := a.encryptPassword(profile.Password)
		if err != nil {
			return err
		}
		profile.Password = encryptedPassword
		values := profile.values()
		builder.WriteString("[profile:" + name + "]\n")
		for _, key := range keys {
			builder.WriteString(key + " = " + values[key] + "\n")
		}
		builder.WriteString("\n")
	}

	if err := os.WriteFile(a.ConfigFile, []byte(builder.String()), 0o600); err != nil {
		return err
	}
	return os.Chmod(a.ConfigFile, 0o600)
}

func (a *App) loadConfig(required bool) (Profile, error) {
	return a.loadConfigProfile("", required)
}

func (a *App) loadConfigProfile(profileName string, required bool) (Profile, error) {
	if _, err := os.Stat(a.ConfigFile); err != nil {
		if required {
			return Profile{}, cliError("no configuration file found. Run: ib config new [PROFILE]")
		}
		return Profile{}, err
	}
	defaultProfile, profiles, legacy, err := a.readConfigProfiles(true)
	if err != nil {
		return Profile{}, err
	}
	settings, settingsMissing, err := a.readConfigSettings()
	if err != nil {
		return Profile{}, err
	}
	selected := defaultProfile
	if strings.TrimSpace(profileName) != "" {
		selected, err = normalizeProfileName(profileName)
		if err != nil {
			return Profile{}, err
		}
	}
	profile, ok := profiles[selected]
	if !ok {
		return Profile{}, cliError("profile %q does not exist in %s", selected, a.ConfigFile)
	}
	profile = profile.complete()
	if profile.Server == "" || profile.Username == "" || profile.Password == "" {
		return Profile{}, cliError("profile %q is missing server, username, or password in %s", selected, a.ConfigFile)
	}
	profile.Server, err = normalizeServer(profile.Server)
	if err != nil {
		return Profile{}, err
	}
	if profile.ReadServer != "" {
		profile.ReadServer, err = normalizeServer(profile.ReadServer)
		if err != nil {
			return Profile{}, err
		}
	}
	profile.DNSView = a.resolveDNSView(profile)
	if legacy || settingsMissing {
		rewriteProfiles := profiles
		if legacy {
			rewriteProfiles = map[string]Profile{defaultProfile: profile}
		}
		if err := a.writeConfigProfilesWithSettings(defaultProfile, rewriteProfiles, settings); err != nil {
			return Profile{}, err
		}
	}
	return profile, nil
}

func (a *App) defaultConfigValues() Profile {
	defaultProfile, profiles, _, err := a.readConfigProfiles(false)
	if err != nil {
		return Profile{Name: defaultProfileName}
	}
	return profiles[defaultProfile].complete()
}

func sessionBaseDir(kind string) string {
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir != "" {
		if info, err := os.Stat(runtimeDir); err == nil && info.IsDir() {
			return filepath.Join(runtimeDir, "ib", kind)
		}
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("ib-%d", os.Getuid()), kind)
}

func sessionFile(kind, prefix string) string {
	return filepath.Join(sessionBaseDir(kind), fmt.Sprintf("%s-%d.json", prefix, os.Getppid()))
}

func (a *App) readSessionZone(profileName string) string {
	return readSessionValue(sessionFile("active-zones", "active-zone-session"), "zone", profileName)
}

func (a *App) writeSessionZone(zoneName, profileName string) error {
	payload := map[string]any{
		"zone":       zoneName,
		"profile":    profileName,
		"parent_pid": os.Getppid(),
	}
	return writeSessionValue(sessionFile("active-zones", "active-zone-session"), payload)
}

func (a *App) readSessionView() string {
	return readSessionValue(sessionFile("active-views", "active-view-session"), "view", "")
}

func (a *App) writeSessionView(viewName string) error {
	payload := map[string]any{
		"view":       viewName,
		"parent_pid": os.Getppid(),
	}
	return writeSessionValue(sessionFile("active-views", "active-view-session"), payload)
}

func readSessionValue(path, key, profileName string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	if intFromAny(payload["parent_pid"]) != os.Getppid() {
		return ""
	}
	if profileName != "" && strings.TrimSpace(fmt.Sprint(payload["profile"])) != profileName {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(payload[key]))
}

func writeSessionValue(path string, payload map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return err
	}
	_ = os.Chmod(filepath.Dir(path), 0o700)
	return os.Chmod(path, 0o600)
}

func intFromAny(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case float64:
		return int(typed)
	case string:
		parsed, _ := strconv.Atoi(typed)
		return parsed
	default:
		return 0
	}
}

func (a *App) resolveDNSView(profile Profile) string {
	if view := strings.TrimSpace(a.dnsViewOverride); view != "" {
		return view
	}
	if view := strings.TrimSpace(a.readSessionView()); view != "" {
		return view
	}
	if view := strings.TrimSpace(os.Getenv(defaultViewEnv)); view != "" {
		return view
	}
	if profile.DNSView != "" {
		return profile.DNSView
	}
	return "default"
}

func (a *App) resolveDNSZone(profile Profile, explicit string) (string, error) {
	if explicit != "" {
		return normalizeZoneName(explicit)
	}
	if zone := strings.TrimSpace(a.dnsZoneOverride); zone != "" {
		return normalizeZoneName(zone)
	}
	if zone := a.readSessionZone(profile.Name); zone != "" {
		return normalizeZoneName(zone)
	}
	if zone := strings.TrimSpace(os.Getenv(defaultZoneEnv)); zone != "" {
		return normalizeZoneName(zone)
	}
	if profile.DefaultZone != "" {
		return normalizeZoneName(profile.DefaultZone)
	}
	return "", cliError("DNS zone is required. Use --zone, ib dns zone use, export %s, or set a default zone with: ib config new", defaultZoneEnv)
}

func (a *App) dnsContextLine() string {
	profile := a.defaultConfigValues()
	profileName := profile.Name
	if profileName == "" {
		profileName = defaultProfileName
	}
	view := a.resolveDNSView(profile)
	zone := strings.TrimSpace(a.dnsZoneOverride)
	source := "command override"
	if zone == "" {
		zone = a.readSessionZone(profileName)
		source = "shell session"
	}
	if zone == "" {
		zone = strings.TrimSpace(os.Getenv(defaultZoneEnv))
		source = defaultZoneEnv
	}
	if zone == "" {
		zone = profile.DefaultZone
		source = "configured default"
	}
	if zone == "" {
		zone = "not set"
		source = "not set"
	}
	return contextTitleStyle.Render("Current Context:") + " " + strings.Join([]string{
		renderContextPair("Profile", profileName, contextProfileValueStyle),
		renderContextPair("View", view, contextViewValueStyle),
		renderContextPair("Zone", zone, contextZoneValueStyle) + " " + contextSourceStyle.Render("("+source+")"),
	}, " | ")
}
