package ibcli

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
)

const (
	allRecordsReturnFields = "name,type,view,zone,ttl,comment,address,record"
	zoneReturnFields       = "fqdn,view,zone_format,comment,ns_group,primary_type"
	zoneSerialFields       = zoneReturnFields + ",soa_serial_number"
	zoneDetailFields       = zoneReturnFields + ",member_soa_mnames,soa_email,soa_expire,soa_negative_ttl,soa_refresh,soa_retry,soa_serial_number,network_view"
	viewReturnFields       = "name"
	dnsSearchWorkerLimit   = defaultDNSSearchWorkerLimit
	recordValueWrapWidth   = 48
	recordCommentWrapWidth = 40
)

var (
	commentAllowedRE = regexp.MustCompile(`^[A-Za-z0-9 .,_:;@#%+=/()[\]'&-]*$`)

	recordTypeColors = map[string]lipgloss.Color{
		"a":           "#22c55e",
		"aaaa":        "#16a34a",
		"host":        "#06b6d4",
		"cname":       "#a78bfa",
		"txt":         "#f59e0b",
		"mx":          "#3b82f6",
		"ptr":         "#38bdf8",
		"ns":          "#14b8a6",
		"soa":         "#ec4899",
		"srv":         "#f97316",
		"unsupported": "#ef4444",
	}
	defaultRecordTypeColor = lipgloss.Color("#94a3b8")
)

type RecordSpec struct {
	Object            string
	ValueField        string
	SearchValueFields []string
	ReturnFields      string
}

var recordTypes = map[string]RecordSpec{
	"a": {
		Object:            "record:a",
		ValueField:        "ipv4addr",
		SearchValueFields: []string{"ipv4addr"},
		ReturnFields:      "name,ipv4addr,ttl,use_ttl,view,zone,comment",
	},
	"aaaa": {
		Object:            "record:aaaa",
		ValueField:        "ipv6addr",
		SearchValueFields: []string{"ipv6addr"},
		ReturnFields:      "name,ipv6addr,ttl,use_ttl,view,zone,comment",
	},
	"cname": {
		Object:            "record:cname",
		ValueField:        "canonical",
		SearchValueFields: []string{"canonical"},
		ReturnFields:      "name,canonical,ttl,use_ttl,view,zone,comment",
	},
	"txt": {
		Object:            "record:txt",
		ValueField:        "text",
		SearchValueFields: []string{"text"},
		ReturnFields:      "name,text,ttl,use_ttl,view,zone,comment",
	},
	"mx": {
		Object:            "record:mx",
		SearchValueFields: []string{"mail_exchanger"},
		ReturnFields:      "name,mail_exchanger,preference,ttl,use_ttl,view,zone,comment",
	},
	"srv": {
		Object:            "record:srv",
		SearchValueFields: []string{"target"},
		ReturnFields:      "name,target,priority,weight,port,ttl,use_ttl,view,zone,comment",
	},
	"host": {
		Object:            "record:host",
		SearchValueFields: []string{"ipv4addrs", "ipv6addrs"},
		ReturnFields:      "name,ipv4addrs,ipv6addrs,ttl,use_ttl,view,zone,comment",
	},
	"ptr": {
		Object:            "record:ptr",
		SearchValueFields: []string{"ptrdname", "ipv4addr", "ipv6addr"},
		ReturnFields:      "name,ptrdname,ipv4addr,ipv6addr,ttl,use_ttl,view,zone,comment",
	},
}

func supportedRecordTypes() []string {
	types := make([]string, 0, len(recordTypes))
	for recordType := range recordTypes {
		types = append(types, recordType)
	}
	sort.Strings(types)
	return types
}

func forwardRecordType(recordType string) bool {
	return strings.ToLower(recordType) != "ptr"
}

func normalizeZoneName(zoneName string) (string, error) {
	zone := strings.TrimRight(strings.TrimSpace(zoneName), ".")
	if zone == "" {
		return "", cliError("zone is required")
	}
	return zone, nil
}

func fqdn(recordName, zoneName string) (string, error) {
	name := strings.TrimRight(strings.TrimSpace(recordName), ".")
	zone, err := normalizeZoneName(zoneName)
	if err != nil {
		return "", err
	}
	if name == "" {
		return "", cliError("record name is required")
	}
	if name == "@" {
		return zone, nil
	}
	if name == zone || strings.HasSuffix(name, "."+zone) {
		return name, nil
	}
	return name + "." + zone, nil
}

func normalizeComment(comment string) (string, error) {
	comment = strings.TrimSpace(comment)
	if comment == "" {
		return "", nil
	}
	if !commentAllowedRE.MatchString(comment) {
		return "", cliError("comment may only contain letters, numbers, spaces, and common punctuation .,_-:;@#%%+=/()[]'&")
	}
	return comment, nil
}

func ttlPayload(ttl int) (map[string]any, error) {
	if ttl == -1 {
		return map[string]any{}, nil
	}
	if ttl < 0 {
		return nil, cliError("ttl must be 0 or greater")
	}
	return map[string]any{"ttl": ttl, "use_ttl": true}, nil
}

func createPayload(recordType, value, name, zone string, ttl int, comment string, client *WapiClient) (string, map[string]any, error) {
	recordType = strings.ToLower(recordType)
	spec, ok := recordTypes[recordType]
	if !ok {
		return "", nil, cliError("unsupported record type %q. Supported: %s", recordType, strings.Join(supportedRecordTypes(), ", "))
	}
	recordComment, err := normalizeComment(comment)
	if err != nil {
		return "", nil, err
	}
	ttlFields, err := ttlPayload(ttl)
	if err != nil {
		return "", nil, err
	}
	objectType := spec.Object

	if recordType == "ptr" {
		payload := ptrPayload(name, value)
		payload["view"] = client.View
		for key, item := range ttlFields {
			payload[key] = item
		}
		if recordComment != "" {
			payload["comment"] = recordComment
		}
		return objectType, payload, nil
	}

	recordName, err := fqdn(name, zone)
	if err != nil {
		return "", nil, err
	}
	payload := map[string]any{
		"name": recordName,
		"view": client.View,
	}
	for key, item := range ttlFields {
		payload[key] = item
	}
	if recordComment != "" {
		payload["comment"] = recordComment
	}

	switch recordType {
	case "mx":
		fields, err := mxPayload(value)
		if err != nil {
			return "", nil, err
		}
		for key, item := range fields {
			payload[key] = item
		}
	case "srv":
		fields, err := srvPayload(value)
		if err != nil {
			return "", nil, err
		}
		for key, item := range fields {
			payload[key] = item
		}
	case "host":
		fields, err := hostPayload(value)
		if err != nil {
			return "", nil, err
		}
		for key, item := range fields {
			payload[key] = item
		}
	case "cname":
		payload[spec.ValueField] = strings.TrimRight(value, ".")
	default:
		payload[spec.ValueField] = value
	}
	return objectType, payload, nil
}

func mxPayload(value string) (map[string]any, error) {
	parts := strings.Fields(value)
	if len(parts) != 2 {
		return nil, cliError("MX value must be quoted as '<preference> <mail_exchanger>'")
	}
	preference, err := strconv.Atoi(parts[0])
	if err != nil {
		return nil, cliError("MX preference must be an integer")
	}
	return map[string]any{"preference": preference, "mail_exchanger": strings.TrimRight(parts[1], ".")}, nil
}

func srvPayload(value string) (map[string]any, error) {
	parts := strings.Fields(value)
	if len(parts) != 4 {
		return nil, cliError("SRV value must be quoted as '<priority> <weight> <port> <target>'")
	}
	priority, err := parseSRVInt("priority", parts[0])
	if err != nil {
		return nil, err
	}
	weight, err := parseSRVInt("weight", parts[1])
	if err != nil {
		return nil, err
	}
	port, err := parseSRVInt("port", parts[2])
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"priority": priority,
		"weight":   weight,
		"port":     port,
		"target":   strings.TrimRight(parts[3], "."),
	}, nil
}

func parseSRVInt(label, value string) (int, error) {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 || parsed > 65535 {
		return 0, cliError("SRV %s must be an integer between 0 and 65535", label)
	}
	return parsed, nil
}

func ptrPayload(name, value string) map[string]any {
	field := "ipv4addr"
	if strings.Contains(name, ":") {
		field = "ipv6addr"
	}
	return map[string]any{field: strings.TrimSpace(name), "ptrdname": strings.TrimRight(strings.TrimSpace(value), ".")}
}

func hostPayload(value string) (map[string]any, error) {
	address, err := netip.ParseAddr(strings.TrimSpace(value))
	if err != nil {
		return nil, cliError("host value must be an IPv4 or IPv6 address")
	}
	if address.Is4() {
		return map[string]any{"ipv4addrs": []map[string]any{{"ipv4addr": address.String()}}}, nil
	}
	return map[string]any{"ipv6addrs": []map[string]any{{"ipv6addr": address.String()}}}, nil
}

func updateValuePayload(recordType, value string) (map[string]any, error) {
	recordType = strings.ToLower(recordType)
	spec, ok := recordTypes[recordType]
	if !ok {
		return nil, cliError("unsupported record type %q. Supported: %s", recordType, strings.Join(supportedRecordTypes(), ", "))
	}
	switch recordType {
	case "mx":
		return mxPayload(value)
	case "srv":
		return srvPayload(value)
	case "host":
		return hostPayload(value)
	case "ptr":
		return map[string]any{"ptrdname": strings.TrimRight(value, ".")}, nil
	case "cname":
		return map[string]any{spec.ValueField: strings.TrimRight(value, ".")}, nil
	default:
		return map[string]any{spec.ValueField: value}, nil
	}
}

func updatePayload(recordType string, value *string, ttl int, comment string) (map[string]any, error) {
	payload := map[string]any{}
	if ttl >= 0 {
		payload["ttl"] = ttl
		payload["use_ttl"] = true
	}
	if value != nil {
		fields, err := updateValuePayload(recordType, *value)
		if err != nil {
			return nil, err
		}
		for key, item := range fields {
			payload[key] = item
		}
	}
	recordComment, err := normalizeComment(comment)
	if err != nil {
		return nil, err
	}
	if recordComment != "" {
		payload["comment"] = recordComment
	}
	if len(payload) == 0 {
		return nil, cliError("nothing to update. Provide TYPE VALUE, -t/--ttl, or -c/--comment")
	}
	return payload, nil
}

func objectQueryParams(spec RecordSpec, client *WapiClient, extra map[string]string) url.Values {
	params := url.Values{}
	params.Set("_return_fields", spec.ReturnFields)
	params.Set("view", client.View)
	for key, value := range extra {
		params.Set(key, value)
	}
	return params
}

func zoneQueryParams(client *WapiClient, returnFields string, extra map[string]string) url.Values {
	params := url.Values{}
	params.Set("_return_fields", returnFields)
	params.Set("view", client.View)
	for key, value := range extra {
		params.Set(key, value)
	}
	return params
}

func allRecordsQueryParams(client *WapiClient, zoneName string) url.Values {
	params := url.Values{}
	params.Set("_return_fields", allRecordsReturnFields)
	params.Set("view", client.View)
	if zoneName != "" {
		params.Set("zone", zoneName)
	}
	return params
}

func caseInsensitiveLiteralPattern(keyword string) string {
	var builder strings.Builder
	for _, r := range keyword {
		if r >= 'a' && r <= 'z' {
			builder.WriteString("[")
			builder.WriteRune(r)
			builder.WriteRune(r - 32)
			builder.WriteString("]")
		} else if r >= 'A' && r <= 'Z' {
			builder.WriteString("[")
			builder.WriteRune(r + 32)
			builder.WriteRune(r)
			builder.WriteString("]")
		} else {
			builder.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	return builder.String()
}

func queryZones(client *WapiClient, search string) ([]map[string]any, error) {
	if search == "" {
		return pagedQuery(client, zoneObject, zoneQueryParams(client, zoneReturnFields, nil))
	}
	pattern := caseInsensitiveLiteralPattern(search)
	seen := map[string]bool{}
	var zones []map[string]any
	for _, field := range []string{"fqdn", "comment"} {
		params := zoneQueryParams(client, zoneReturnFields, map[string]string{field + "~": pattern})
		results, err := pagedQuery(client, zoneObject, params)
		if err != nil {
			return nil, err
		}
		for _, zone := range results {
			key := zoneKey(zone)
			if seen[key] {
				continue
			}
			seen[key] = true
			zones = append(zones, zone)
		}
	}
	sortZones(zones)
	return zones, nil
}

func (a *App) cachedZones(profile Profile, client *WapiClient, search string) ([]map[string]any, error) {
	now := time.Now()
	entry, err := a.readCachedZones(profile)
	if err == nil && entry.CacheFound && a.cacheEntryFresh(entry, now) {
		return filterZones(entry.Rows, search), nil
	}

	zones, err := queryZones(client, "")
	if err != nil {
		return nil, err
	}
	_ = a.writeCachedZones(profile, zones, now)
	return filterZones(zones, search), nil
}

func (a *App) queueZoneCacheRefresh(profile Profile) {
	a.invalidateZoneCache(profile)
	a.startZoneCacheRefreshAsync(profile)
}

func (a *App) startZoneCacheRefreshAsync(profile Profile) {
	_ = a.startZoneCacheRefresh(profile)
}

func (a *App) startZoneCacheRefresh(profile Profile) error {
	acquired, err := a.tryAcquireZoneRefreshLease(profile, time.Now(), recordRefreshLeaseTTL)
	if err != nil || !acquired {
		return err
	}
	if a.backgroundZoneRefresher != nil {
		if err := a.backgroundZoneRefresher(profile); err != nil {
			_ = a.releaseZoneRefreshLease(profile)
			return err
		}
		return nil
	}
	executable, err := os.Executable()
	if err != nil {
		_ = a.releaseZoneRefreshLease(profile)
		return err
	}
	args := []string{
		"config", "cache", "refresh-zones",
		"--profile", firstNonEmpty(strings.TrimSpace(profile.Name), defaultProfileName),
		"--view", strings.TrimSpace(profile.DNSView),
	}
	cmd := exec.Command(executable, args...) // #nosec G204 -- executable is this ib binary and args are fixed internal cache-refresh flags
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		_ = a.releaseZoneRefreshLease(profile)
		return err
	}
	go func() {
		_ = cmd.Wait()
	}()
	return nil
}

func (a *App) runZoneCacheRefresh(profileName string, view string) error {
	releaseProfile := Profile{Name: strings.TrimSpace(profileName), DNSView: strings.TrimSpace(view)}
	if releaseProfile.Name == "" {
		releaseProfile.Name = defaultProfileName
	}
	defer func() {
		_ = a.releaseZoneRefreshLease(releaseProfile)
	}()
	profile, err := a.loadConfigProfile(profileName, true)
	if err != nil {
		return err
	}
	if strings.TrimSpace(view) != "" {
		profile.DNSView = strings.TrimSpace(view)
	}
	releaseProfile = profile
	return a.refreshZoneCache(profile, a.newClient(profile))
}

func (a *App) refreshZoneCache(profile Profile, client *WapiClient) error {
	zones, err := queryZones(client, "")
	if err != nil {
		return err
	}
	return a.writeCachedZones(profile, zones, time.Now())
}

func filterZones(zones []map[string]any, search string) []map[string]any {
	search = strings.ToLower(strings.TrimSpace(search))
	if search == "" {
		return zones
	}
	filtered := make([]map[string]any, 0, len(zones))
	for _, zone := range zones {
		name := strings.ToLower(cleanString(zone["fqdn"]))
		comment := strings.ToLower(cleanString(zone["comment"]))
		if strings.Contains(name, search) || strings.Contains(comment, search) {
			filtered = append(filtered, zone)
		}
	}
	return filtered
}

func sortZones(zones []map[string]any) {
	sort.Slice(zones, func(i, j int) bool {
		return strings.ToLower(fmt.Sprint(zones[i]["fqdn"])) < strings.ToLower(fmt.Sprint(zones[j]["fqdn"]))
	})
}

func zoneKey(zone map[string]any) string {
	if ref := strings.TrimSpace(fmt.Sprint(zone["_ref"])); ref != "" && ref != "<nil>" {
		return ref
	}
	raw, _ := json.Marshal(zone)
	return string(raw)
}

func findZone(client *WapiClient, zoneName string, fields string) ([]map[string]any, error) {
	target, err := normalizeZoneName(zoneName)
	if err != nil {
		return nil, err
	}
	return pagedQuery(client, zoneObject, zoneQueryParams(client, fields, map[string]string{"fqdn": target}))
}

func forwardZoneCandidates(recordName string) []string {
	name := strings.TrimRight(strings.TrimSpace(recordName), ".")
	if name == "" || name == "@" || !strings.Contains(name, ".") {
		return nil
	}
	labels := strings.Split(name, ".")
	candidates := make([]string, 0, len(labels))
	for i := range labels {
		if labels[i] == "" {
			return nil
		}
		candidates = append(candidates, strings.Join(labels[i:], "."))
	}
	return candidates
}

func matchingForwardZone(client *WapiClient, recordName string) (string, error) {
	for _, candidate := range forwardZoneCandidates(recordName) {
		matches, err := findZone(client, candidate, zoneReturnFields)
		if err != nil {
			return "", err
		}
		for _, zone := range matches {
			zoneFormat := strings.ToUpper(strings.TrimSpace(fmt.Sprint(zone["zone_format"])))
			if zoneFormat == "" || zoneFormat == "FORWARD" {
				return normalizeZoneName(fmt.Sprint(zone["fqdn"]))
			}
		}
	}
	return "", nil
}

func (a *App) resolveCreateZone(profile Profile, client *WapiClient, recordType, recordName, explicitZone string) (string, error) {
	if explicitZone != "" {
		return a.resolveDNSZone(profile, explicitZone)
	}
	if forwardRecordType(recordType) {
		matchedZone, err := matchingForwardZone(client, recordName)
		if err != nil {
			return "", err
		}
		if matchedZone != "" {
			return matchedZone, nil
		}
	}
	return a.resolveDNSZone(profile, "")
}

func warnIfCNAMEUnresolved(app *App, recordType, value string) {
	if strings.ToLower(recordType) != "cname" {
		return
	}
	target := strings.TrimRight(strings.TrimSpace(value), ".")
	if target == "" {
		app.PrintWarning("WARNING: CNAME target is empty; DNS resolution check was skipped.")
		return
	}
	if _, err := net.LookupHost(target); err != nil {
		app.PrintWarning(fmt.Sprintf("WARNING: CNAME target %s does not resolve from this system: %v. Record creation will continue.", target, err))
	}
}

func recordTypeFromAllRecord(item map[string]any) string {
	value := strings.ToLower(strings.TrimSpace(fmt.Sprint(item["type"])))
	value = strings.TrimPrefix(value, "record:")
	if value == "unsupported" {
		return unsupportedAllRecordType(item)
	}
	if value == "host_ipv4addr" || value == "host_ipv6addr" {
		return "host"
	}
	if strings.HasPrefix(value, "sharedrecord:") {
		return strings.Replace(value, "sharedrecord:", "shared-", 1)
	}
	value = strings.TrimPrefix(value, "shared:")
	if value == "" || value == "<nil>" {
		if ref := strings.ToLower(fmt.Sprint(item["_ref"])); strings.Contains(ref, "record:") {
			parts := strings.Split(ref, "/")
			if len(parts) > 0 {
				return strings.TrimPrefix(parts[0], "record:")
			}
		}
		return recordOutputType
	}
	return value
}

func unsupportedAllRecordType(item map[string]any) string {
	referenceText := strings.ToLower(strings.Join([]string{
		cleanString(item["_ref"]),
		decodedAllRecordsRef(item),
		cleanString(item["record"]),
		cleanString(nestedRecord(item)["_ref"]),
	}, " "))
	if strings.Contains(referenceText, "fake_bind_ns") || strings.Contains(referenceText, "bind_ns") || strings.Contains(referenceText, "record:ns/") {
		return "ns"
	}
	if strings.Contains(referenceText, "bind_soa") || strings.Contains(referenceText, "record:soa/") {
		return "soa"
	}
	return "unsupported"
}

func decodedAllRecordsRef(item map[string]any) string {
	ref := cleanString(item["_ref"])
	if ref == "" {
		return ""
	}
	tokens := strings.FieldsFunc(ref, func(r rune) bool {
		return r == '/' || r == ':' || r == ',' || r == ' ' || r == '\t'
	})
	var decoded []string
	for _, token := range tokens {
		text, ok := decodeReferenceToken(strings.Trim(token, `"'`))
		if ok {
			decoded = append(decoded, text)
		}
	}
	return strings.Join(decoded, " ")
}

func decodeReferenceToken(token string) (string, bool) {
	if len(token) < 8 {
		return "", false
	}
	candidates := []struct {
		value    string
		encoding *base64.Encoding
	}{
		{token, base64.RawURLEncoding},
		{token, base64.RawStdEncoding},
		{paddedBase64(token), base64.URLEncoding},
		{paddedBase64(token), base64.StdEncoding},
	}
	for _, candidate := range candidates {
		raw, err := candidate.encoding.DecodeString(candidate.value)
		if err != nil {
			continue
		}
		text, ok := printableDecodedText(raw)
		if ok {
			return text, true
		}
	}
	return "", false
}

func paddedBase64(token string) string {
	if remainder := len(token) % 4; remainder != 0 {
		return token + strings.Repeat("=", 4-remainder)
	}
	return token
}

func printableDecodedText(raw []byte) (string, bool) {
	if len(raw) == 0 || !utf8.Valid(raw) {
		return "", false
	}
	text := string(raw)
	printable := 0
	total := 0
	for _, r := range text {
		total++
		if r == '\n' || r == '\r' || r == '\t' || (r >= 32 && r != 127) {
			printable++
		}
	}
	return text, total > 0 && float64(printable)/float64(total) >= 0.85
}

func nestedRecord(item map[string]any) map[string]any {
	if nested, ok := item["record"].(map[string]any); ok {
		return nested
	}
	return map[string]any{}
}

func recordName(item map[string]any, recordType string) string {
	if name := reverseRecordName(item); name != "" {
		return name
	}
	if name := strings.TrimSpace(fmt.Sprint(item["name"])); name != "" && name != "<nil>" {
		return name
	}
	if name := strings.TrimSpace(fmt.Sprint(nestedRecord(item)["name"])); name != "" && name != "<nil>" {
		return name
	}
	if record := displayRecordField(item); record != "" {
		return record
	}
	return ""
}

func recordValue(recordType string, item map[string]any) string {
	recordType = strings.ToLower(recordType)
	if nested := nestedRecord(item); len(nested) > 0 {
		for key, value := range nested {
			if _, exists := item[key]; !exists {
				item[key] = value
			}
		}
	}
	if value := reverseRecordValue(item); value != "" {
		return value
	}
	if address := strings.TrimSpace(fmt.Sprint(item["address"])); address != "" && address != "<nil>" {
		return address
	}
	if record := displayRecordField(item); record != "" && record != recordName(item, recordType) {
		return record
	}
	if recordType == "host" {
		var values []string
		for _, key := range []string{"ipv4addrs", "ipv6addrs"} {
			for _, nested := range mapSliceFromAny(item[key]) {
				for _, nestedKey := range []string{"ipv4addr", "ipv6addr"} {
					value := strings.TrimSpace(fmt.Sprint(nested[nestedKey]))
					if value != "" && value != "<nil>" {
						values = append(values, value)
					}
				}
			}
		}
		return strings.Join(values, ", ")
	}
	if recordType == "mx" {
		exchanger := strings.TrimSpace(fmt.Sprint(item["mail_exchanger"]))
		if exchanger != "" && exchanger != "<nil>" {
			if pref := strings.TrimSpace(fmt.Sprint(item["preference"])); pref != "" && pref != "<nil>" {
				return pref + " " + exchanger
			}
			return exchanger
		}
	}
	if recordType == "srv" {
		parts := []string{}
		for _, key := range []string{"priority", "weight", "port", "target"} {
			value := strings.TrimSpace(fmt.Sprint(item[key]))
			if value != "" && value != "<nil>" {
				parts = append(parts, value)
			}
		}
		return strings.Join(parts, " ")
	}
	for _, key := range []string{"ipv4addr", "ipv6addr", "canonical", "text", "mail_exchanger", "ptrdname", "target"} {
		value := strings.TrimSpace(fmt.Sprint(item[key]))
		if value != "" && value != "<nil>" {
			return value
		}
	}
	return ""
}

func reverseRecordName(item map[string]any) string {
	if !reverseZoneName(cleanString(item["zone"])) {
		return ""
	}
	return reverseRecordAddress(item)
}

func reverseRecordValue(item map[string]any) string {
	if !reverseZoneName(cleanString(item["zone"])) {
		return ""
	}
	if target := cleanDNSName(firstNonEmpty(cleanString(item["ptrdname"]), cleanString(nestedRecord(item)["ptrdname"]))); target != "" {
		return target
	}
	if target := cleanDNSName(firstHostName(item)); target != "" {
		return target
	}
	return ""
}

func reverseRecordAddress(item map[string]any) string {
	for _, value := range []string{
		cleanString(item["address"]),
		cleanString(item["ipv4addr"]),
		cleanString(item["ipv6addr"]),
		cleanString(nestedRecord(item)["address"]),
		cleanString(nestedRecord(item)["ipv4addr"]),
		cleanString(nestedRecord(item)["ipv6addr"]),
		addressFromNestedAddresses(item, "ipv4addrs", "ipv4addr"),
		addressFromNestedAddresses(item, "ipv6addrs", "ipv6addr"),
	} {
		if addr, err := netip.ParseAddr(value); err == nil {
			return addr.String()
		}
	}
	if addr := addressFromReverseOwner(cleanString(item["name"]), cleanString(item["zone"])); addr != "" {
		return addr
	}
	if addr := addressFromReverseOwner(cleanString(nestedRecord(item)["name"]), cleanString(item["zone"])); addr != "" {
		return addr
	}
	return ""
}

func addressFromNestedAddresses(item map[string]any, listKey, addressKey string) string {
	for _, nested := range mapSliceFromAny(item[listKey]) {
		if value := cleanString(nested[addressKey]); value != "" {
			return value
		}
	}
	for _, nested := range mapSliceFromAny(nestedRecord(item)[listKey]) {
		if value := cleanString(nested[addressKey]); value != "" {
			return value
		}
	}
	return ""
}

func firstHostName(item map[string]any) string {
	nested := nestedRecord(item)
	for _, value := range []string{
		cleanString(item["host"]),
		cleanString(nested["host"]),
		cleanString(nested["name"]),
	} {
		if value != "" && !reversePointerName(value) {
			return value
		}
	}
	for _, key := range []string{"ipv4addrs", "ipv6addrs"} {
		for _, nestedAddress := range mapSliceFromAny(item[key]) {
			if host := cleanString(nestedAddress["host"]); host != "" {
				return host
			}
		}
		for _, nestedAddress := range mapSliceFromAny(nested[key]) {
			if host := cleanString(nestedAddress["host"]); host != "" {
				return host
			}
		}
	}
	return ""
}

func cleanDNSName(value string) string {
	return strings.TrimRight(strings.TrimSpace(value), ".")
}

func reverseZoneName(zone string) bool {
	zone = strings.ToLower(strings.TrimSpace(zone))
	if zone == "" {
		return false
	}
	if strings.HasSuffix(zone, "in-addr.arpa") || strings.HasSuffix(zone, "ip6.arpa") {
		return true
	}
	if _, err := netip.ParsePrefix(zone); err == nil {
		return true
	}
	return false
}

func reversePointerName(name string) bool {
	name = strings.ToLower(strings.TrimRight(strings.TrimSpace(name), "."))
	return strings.HasSuffix(name, "in-addr.arpa") || strings.HasSuffix(name, "ip6.arpa")
}

func addressFromReverseOwner(name, zone string) string {
	name = strings.TrimRight(strings.TrimSpace(name), ".")
	zone = strings.TrimRight(strings.TrimSpace(zone), ".")
	if name == "" || zone == "" {
		return ""
	}
	if addr, err := netip.ParseAddr(name); err == nil {
		return addr.String()
	}
	if addr := addressFromCIDRReverseOwner(name, zone); addr != "" {
		return addr
	}
	if addr := addressFromArpaReverseOwner(name, zone); addr != "" {
		return addr
	}
	return ""
}

func addressFromCIDRReverseOwner(name, zone string) string {
	prefix, err := netip.ParsePrefix(zone)
	if err != nil || !prefix.Addr().Is4() || prefix.Bits()%8 != 0 {
		return ""
	}
	base := prefix.Addr().As4()
	octets := []string{}
	for index := 0; index < prefix.Bits()/8; index++ {
		octets = append(octets, strconv.Itoa(int(base[index])))
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" {
			return ""
		}
		octet, err := strconv.Atoi(label)
		if err != nil || octet < 0 || octet > 255 {
			return ""
		}
		octets = append(octets, strconv.Itoa(octet))
	}
	if len(octets) != 4 {
		return ""
	}
	addr, err := netip.ParseAddr(strings.Join(octets, "."))
	if err != nil {
		return ""
	}
	return addr.String()
}

func addressFromArpaReverseOwner(name, zone string) string {
	full := strings.ToLower(name)
	lowerZone := strings.ToLower(zone)
	if !strings.HasSuffix(full, lowerZone) {
		full = strings.TrimRight(name+"."+zone, ".")
	}
	full = strings.TrimRight(full, ".")
	if strings.HasSuffix(full, ".in-addr.arpa") {
		labels := strings.Split(strings.TrimSuffix(full, ".in-addr.arpa"), ".")
		if len(labels) != 4 {
			return ""
		}
		for left, right := 0, len(labels)-1; left < right; left, right = left+1, right-1 {
			labels[left], labels[right] = labels[right], labels[left]
		}
		addr, err := netip.ParseAddr(strings.Join(labels, "."))
		if err != nil {
			return ""
		}
		return addr.String()
	}
	return ""
}

func recordTTL(item map[string]any) string {
	if recordUsesDefaultTTL(item) {
		return ""
	}
	if ttl := cleanIntegerString(item["ttl"]); ttl != "" {
		return ttl
	}
	return cleanIntegerString(nestedRecord(item)["ttl"])
}

func recordUsesDefaultTTL(item map[string]any) bool {
	if useTTL, ok := boolFromAny(item["use_ttl"]); ok {
		return !useTTL
	}
	if useTTL, ok := boolFromAny(nestedRecord(item)["use_ttl"]); ok {
		return !useTTL
	}
	return false
}

func boolFromAny(value any) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "1", "yes":
			return true, true
		case "false", "0", "no":
			return false, true
		default:
			return false, false
		}
	case int:
		return typed != 0, true
	case int64:
		return typed != 0, true
	case float64:
		return typed != 0, true
	default:
		return false, false
	}
}

func displayRecordField(item map[string]any) string {
	switch value := item["record"].(type) {
	case nil:
		return ""
	case map[string]any:
		return ""
	case string:
		text := cleanString(value)
		if infobloxReferenceText(text) {
			return ""
		}
		return text
	default:
		text := cleanString(value)
		if infobloxReferenceText(text) {
			return ""
		}
		return text
	}
}

func infobloxReferenceText(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" || lower == "<nil>" {
		return true
	}
	if strings.HasPrefix(lower, "record:") ||
		strings.HasPrefix(lower, "allrecords/") ||
		strings.Contains(lower, "/record:") ||
		strings.Contains(lower, "bind_ns") ||
		strings.Contains(lower, "bind_soa") ||
		strings.Contains(lower, "_ref:") {
		return true
	}
	for _, token := range strings.FieldsFunc(lower, func(r rune) bool {
		return r == '/' || r == ':' || r == ',' || r == ' ' || r == '\t'
	}) {
		decoded, ok := decodeReferenceToken(strings.Trim(token, `"'`))
		if !ok {
			continue
		}
		decoded = strings.ToLower(decoded)
		if strings.Contains(decoded, "record:") ||
			strings.Contains(decoded, "bind_ns") ||
			strings.Contains(decoded, "bind_soa") {
			return true
		}
	}
	return false
}

func recordOutputRow(recordType string, item map[string]any) map[string]any {
	return map[string]any{
		"type":    strings.ToUpper(recordType),
		"name":    recordName(item, recordType),
		"value":   recordValue(recordType, item),
		"zone":    cleanString(item["zone"]),
		"ttl":     recordTTL(item),
		"comment": cleanString(item["comment"]),
	}
}

func recordTypeColor(recordType string) lipgloss.Color {
	recordType = canonicalDisplayRecordType(recordType)
	if color, ok := recordTypeColors[recordType]; ok {
		return color
	}
	if strings.HasPrefix(recordType, "shared-") {
		if color, ok := recordTypeColors[strings.TrimPrefix(recordType, "shared-")]; ok {
			return color
		}
	}
	return defaultRecordTypeColor
}

func styledRecordType(recordType string) string {
	label := displayRecordTypeLabel(recordType)
	if label == "" {
		return ""
	}
	return lipgloss.NewStyle().Bold(true).Foreground(recordTypeColor(recordType)).Render(label)
}

func canonicalDisplayRecordType(recordType string) string {
	value := strings.ToLower(strings.TrimSpace(recordType))
	value = strings.TrimPrefix(value, "record:")
	value = strings.TrimPrefix(value, "shared:")
	value = strings.Replace(value, "sharedrecord:", "shared-", 1)
	if value == "host_ipv4addr" || value == "host_ipv6addr" {
		return "host"
	}
	return value
}

func displayRecordTypeLabel(recordType string) string {
	value := canonicalDisplayRecordType(recordType)
	if value == "" || value == "<nil>" {
		return ""
	}
	return strings.ToUpper(value)
}

func cleanString(value any) string {
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "<nil>" {
		return ""
	}
	return text
}

func (a *App) emitRecords(records []TypedRecord) error {
	return a.emitRecordsWithContext(records, true)
}

func (a *App) emitRecordsWithContext(records []TypedRecord, showContext bool) error {
	rows := make([]map[string]any, 0, len(records))
	for _, record := range records {
		rows = append(rows, recordOutputRow(record.Type, record.Item))
	}
	if a.isTableOutput() {
		displayRows := make([][]string, 0, len(rows))
		for _, row := range rows {
			displayRows = append(displayRows, []string{
				styledRecordType(stringify(row["type"])),
				stringify(row["name"]),
				wrapRecordTableCell(stringify(row["value"]), recordValueWrapWidth),
				stringify(row["zone"]),
				stringify(row["ttl"]),
				wrapRecordTableCell(stringify(row["comment"]), recordCommentWrapWidth),
			})
		}
		fmt.Fprintln(a.Stdout, renderTable("DNS Records", []string{"Type", "Name", "Value", "Zone", "Ttl", "Comment"}, displayRows))
		a.printRecordTableFooter(showContext, len(rows))
		return nil
	}
	return a.emitRows(fmt.Sprintf("DNS Records (%d)", len(rows)), []string{"type", "name", "value", "zone", "ttl", "comment"}, rows)
}

func (a *App) printRecordTableFooter(showContext bool, count int) {
	parts := make([]string, 0, 2)
	if showContext {
		parts = append(parts, a.dnsContextLine())
	}
	if count > 5 {
		parts = append(parts, recordTotalBadgeStyle.Render(fmt.Sprintf("Total records: %d", count)))
	}
	if len(parts) == 0 {
		return
	}
	fmt.Fprintln(a.Stdout, strings.Join(parts, "  "))
}

func wrapRecordTableCell(value string, width int) string {
	value = strings.TrimSpace(value)
	if value == "" || width <= 0 || lipgloss.Width(value) <= width {
		return value
	}
	var wrapped []string
	for _, line := range strings.Split(value, "\n") {
		wrapped = append(wrapped, wrapRecordTableLine(line, width)...)
	}
	return strings.Join(wrapped, "\n")
}

func wrapRecordTableLine(value string, width int) []string {
	words := strings.Fields(value)
	if len(words) == 0 {
		return []string{""}
	}
	var lines []string
	current := ""
	for _, word := range words {
		for lipgloss.Width(word) > width {
			prefix, rest := splitDisplayWidth(word, width)
			if current != "" {
				lines = append(lines, current)
				current = ""
			}
			lines = append(lines, prefix)
			word = rest
		}
		if current == "" {
			current = word
			continue
		}
		if lipgloss.Width(current)+1+lipgloss.Width(word) <= width {
			current += " " + word
			continue
		}
		lines = append(lines, current)
		current = word
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}

func splitDisplayWidth(value string, width int) (string, string) {
	if width <= 0 {
		return value, ""
	}
	currentWidth := 0
	for index, char := range value {
		charWidth := lipgloss.Width(string(char))
		if currentWidth > 0 && currentWidth+charWidth > width {
			return value[:index], value[index:]
		}
		currentWidth += charWidth
	}
	return value, ""
}

func (a *App) emitZones(zones []map[string]any) error {
	rows := make([]map[string]any, 0, len(zones))
	for _, zone := range zones {
		rows = append(rows, map[string]any{
			"zone":     cleanString(zone["fqdn"]),
			"view":     cleanString(zone["view"]),
			"format":   cleanString(zone["zone_format"]),
			"ns_group": cleanString(zone["ns_group"]),
			"comment":  cleanString(zone["comment"]),
		})
	}
	if a.isTableOutput() {
		displayRows := make([][]string, 0, len(rows))
		for _, row := range rows {
			displayRows = append(displayRows, []string{
				stringify(row["zone"]),
				stringify(row["view"]),
				stringify(row["format"]),
				stringify(row["ns_group"]),
				stringify(row["comment"]),
			})
		}
		fmt.Fprintln(a.Stdout, renderTable("DNS Zones", []string{"Zone", "View", "Format", "Ns Group", "Comment"}, displayRows))
		a.printTableTotal("zones", len(rows))
		return nil
	}
	return a.emitRows(fmt.Sprintf("DNS Zones (%d)", len(rows)), []string{"zone", "view", "format", "ns_group", "comment"}, rows)
}

func (a *App) printTableTotal(label string, count int) {
	if count <= 5 {
		return
	}
	fmt.Fprintf(a.Stdout, "Total %s: %d\n", label, count)
}

type TypedRecord struct {
	Type string
	Item map[string]any
}

type cachedRecordLoadResult struct {
	Records []TypedRecord
	Source  string
}

func recordsForZone(client *WapiClient, zoneName string) ([]TypedRecord, error) {
	results, err := allRecordRowsForZone(client, zoneName, true)
	if err != nil {
		return nil, err
	}
	return recordsFromAllRecordRows(results), nil
}

func allRecordRowsForZone(client *WapiClient, zoneName string, enrich bool) ([]map[string]any, error) {
	// /allrecords is the fast path for large zones because it returns mixed
	// record types in one paged query. Detail enrichment is optional because it
	// requires per-record GETs and can be much slower.
	rows, err := pagedQuery(client, allRecordsObject, allRecordsQueryParams(client, zoneName))
	if err != nil {
		return nil, err
	}
	if enrich {
		enrichAllRecordRows(client, rows)
	}
	return rows, nil
}

func enrichAllRecordRows(client *WapiClient, rows []map[string]any) bool {
	// Infoblox omits some concrete-record fields from /allrecords. When callers
	// ask for details, fetch only rows missing TTL/detail data and merge those
	// fields back into the cached /allrecords shape.
	changed := false
	for _, item := range rows {
		if recordHasTTLDetail(item) {
			continue
		}
		recordType := recordTypeFromAllRecord(item)
		ref := concreteRecordRef(item)
		spec, ok := recordSpecForDetail(recordType, ref)
		if !ok || ref == "" {
			continue
		}
		detail, err := recordDetailByRef(client, ref, spec)
		if err != nil {
			continue
		}
		mergeRecordDetail(item, detail)
		if len(detail) > 0 {
			changed = true
		}
	}
	return changed
}

func recordHasTTLDetail(item map[string]any) bool {
	if recordTTL(item) != "" {
		return true
	}
	nested := nestedRecord(item)
	return cleanString(item["use_ttl"]) != "" || cleanString(nested["use_ttl"]) != ""
}

func concreteRecordRef(item map[string]any) string {
	candidates := []any{nestedRecord(item)["_ref"], item["record"]}
	for _, candidate := range candidates {
		ref := cleanString(candidate)
		lower := strings.ToLower(ref)
		if strings.HasPrefix(lower, "record:") || strings.HasPrefix(lower, "sharedrecord:") {
			return ref
		}
	}
	return ""
}

func recordSpecForDetail(recordType, ref string) (RecordSpec, bool) {
	if spec, ok := recordTypes[canonicalDisplayRecordType(recordType)]; ok {
		return spec, true
	}
	ref = strings.ToLower(strings.TrimSpace(ref))
	for _, spec := range recordTypes {
		if strings.HasPrefix(ref, spec.Object+"/") {
			return spec, true
		}
	}
	return RecordSpec{}, false
}

func recordDetailByRef(client *WapiClient, ref string, spec RecordSpec) (map[string]any, error) {
	params := url.Values{}
	params.Set("_return_fields", spec.ReturnFields)
	response, err := client.Request(http.MethodGet, ref, params, nil)
	if err != nil {
		return nil, err
	}
	if detail, ok := response.(map[string]any); ok {
		return detail, nil
	}
	return nil, nil
}

func mergeRecordDetail(item map[string]any, detail map[string]any) {
	if len(detail) == 0 {
		return
	}
	nested := nestedRecord(item)
	if len(nested) == 0 {
		nested = map[string]any{}
	}
	for key, value := range detail {
		if _, exists := nested[key]; !exists || cleanString(nested[key]) == "" {
			nested[key] = value
		}
	}
	item["record"] = nested

	for _, key := range []string{
		"ttl", "use_ttl", "comment",
		"ipv4addr", "ipv6addr", "canonical", "text", "mail_exchanger", "preference",
		"priority", "weight", "port", "target", "ptrdname", "ipv4addrs", "ipv6addrs",
	} {
		if value, exists := detail[key]; exists && cleanString(item[key]) == "" {
			item[key] = value
		}
	}
}

func recordsFromAllRecordRows(rows []map[string]any) []TypedRecord {
	records := make([]TypedRecord, 0, len(rows))
	seen := map[string]bool{}
	for _, item := range rows {
		recordType := recordTypeFromAllRecord(item)
		key := recordKey(recordType, item)
		if seen[key] {
			continue
		}
		seen[key] = true
		records = append(records, TypedRecord{Type: recordType, Item: item})
	}
	sortRecords(records)
	return records
}

func (a *App) cachedRecordsForZone(profile Profile, client *WapiClient, zoneName string) ([]TypedRecord, error) {
	result, err := a.cachedRecordsForZoneLoad(profile, client, zoneName, false)
	return result.Records, err
}

func (a *App) cachedRecordsForZoneWithDetails(profile Profile, client *WapiClient, zoneName string, enrich bool) ([]TypedRecord, error) {
	result, err := a.cachedRecordsForZoneLoad(profile, client, zoneName, enrich)
	return result.Records, err
}

func (a *App) cachedRecordsForZoneWithSource(profile Profile, client *WapiClient, zoneName string) ([]TypedRecord, string, error) {
	return a.cachedRecordsForZoneWithSourceAndDetails(profile, client, zoneName, false)
}

func (a *App) cachedRecordsForZoneWithSourceAndDetails(profile Profile, client *WapiClient, zoneName string, enrich bool) ([]TypedRecord, string, error) {
	result, err := a.cachedRecordsForZoneLoad(profile, client, zoneName, enrich)
	return result.Records, result.Source, err
}

func (a *App) cachedRecordsForZoneLoad(profile Profile, client *WapiClient, zoneName string, enrich bool) (cachedRecordLoadResult, error) {
	now := time.Now()
	entry, err := a.readCachedRecords(profile, zoneName)
	if err == nil && entry.CacheFound {
		// Fast path: fresh cached rows are returned without touching Infoblox.
		if a.cacheEntryFresh(entry, now) {
			source := recordCacheSourceFreshCache
			if enrich && enrichAllRecordRows(client, entry.Rows) {
				_ = a.writeCachedRecords(profile, zoneName, entry.Serial, entry.Rows, now)
				source = recordCacheSourceFreshEnriched
			}
			return cachedRecordLoadResult{Records: recordsFromAllRecordRows(entry.Rows), Source: source}, nil
		}

		// Stale-while-revalidate path: return cached rows immediately, then start
		// exactly one detached revalidation process for this profile/view/zone.
		if now.Unix() < entry.StaleExpiresAt {
			a.startRecordCacheRevalidationAsync(profile, zoneName)
			return cachedRecordLoadResult{Records: recordsFromAllRecordRows(entry.Rows), Source: recordCacheSourceStaleCache}, nil
		}
	}

	// Outside SWR, foreground work is required so callers do not see data older
	// than the configured stale window. A matching SOA serial lets us renew the
	// cache without downloading /allrecords again.
	currentSerial, hasSerial, err := currentZoneSerial(client, zoneName)
	if err != nil {
		return cachedRecordLoadResult{}, err
	}
	if entry.CacheFound && hasSerial && entry.Serial != "" && entry.Serial == currentSerial {
		staleExpiresAt := now.Add(a.recordsCacheSWRTTL())
		source := recordCacheSourceSerialCache
		if enrich && enrichAllRecordRows(client, entry.Rows) {
			_ = a.writeCachedRecordsEntry(profile, zoneName, currentSerial, entry.Rows, now.Unix(), staleExpiresAt.Unix())
			source = recordCacheSourceSerialEnriched
		} else {
			_ = a.renewCachedRecordsAge(profile, zoneName, now, staleExpiresAt)
		}
		return cachedRecordLoadResult{Records: recordsFromAllRecordRows(entry.Rows), Source: source}, nil
	}

	rows, err := allRecordRowsForZone(client, zoneName, enrich)
	if err != nil {
		return cachedRecordLoadResult{}, err
	}
	serial := ""
	if hasSerial {
		serial = currentSerial
	}
	_ = a.writeCachedRecords(profile, zoneName, serial, rows, now)
	return cachedRecordLoadResult{Records: recordsFromAllRecordRows(rows), Source: recordCacheSourceAllRecords}, nil
}

func (a *App) queueRecordCacheRefreshAfterWrite(profile Profile, zoneName string) {
	zoneName, err := normalizeZoneName(zoneName)
	if err != nil {
		return
	}

	// Writes make the affected zone cache immediately unsafe to serve as fresh.
	// Delete it first, then let the same lease-protected background path repopulate it.
	a.deleteRecordCacheEntry(profile, zoneName)
	a.startRecordCacheRevalidationAsync(profile, zoneName)
}

func (a *App) startRecordCacheRevalidationAsync(profile Profile, zoneName string) {
	_ = a.startRecordCacheRevalidation(profile, zoneName)
}

func (a *App) startRecordCacheRevalidation(profile Profile, zoneName string) error {
	acquired, err := a.tryAcquireRecordRefreshLease(profile, zoneName, time.Now(), recordRefreshLeaseTTL)
	if err != nil || !acquired {
		return err
	}
	if a.backgroundRecordRevalidator != nil {
		if err := a.backgroundRecordRevalidator(profile, zoneName); err != nil {
			_ = a.releaseRecordRefreshLease(profile, zoneName)
			return err
		}
		return nil
	}
	executable, err := os.Executable()
	if err != nil {
		_ = a.releaseRecordRefreshLease(profile, zoneName)
		return err
	}

	// Start the helper synchronously so the parent knows the refresh was handed
	// off, then return without waiting so list/search/edit remain responsive.
	args := []string{
		"config", "cache", "revalidate-record",
		"--profile", firstNonEmpty(strings.TrimSpace(profile.Name), defaultProfileName),
		"--view", strings.TrimSpace(profile.DNSView),
		"--zone", strings.TrimSpace(zoneName),
	}
	cmd := exec.Command(executable, args...) // #nosec G204 -- executable is this ib binary and args are fixed internal cache-refresh flags
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		_ = a.releaseRecordRefreshLease(profile, zoneName)
		return err
	}
	go func() {
		_ = cmd.Wait()
	}()
	return nil
}

func (a *App) runRecordCacheRevalidate(profileName string, view string, zoneName string) error {
	zoneName, err := normalizeZoneName(zoneName)
	if err != nil {
		return err
	}
	releaseProfile := Profile{Name: strings.TrimSpace(profileName), DNSView: strings.TrimSpace(view)}
	if releaseProfile.Name == "" {
		releaseProfile.Name = defaultProfileName
	}
	defer func() {
		_ = a.releaseRecordRefreshLease(releaseProfile, zoneName)
	}()
	profile, err := a.loadConfigProfile(profileName, true)
	if err != nil {
		return err
	}
	if strings.TrimSpace(view) != "" {
		profile.DNSView = strings.TrimSpace(view)
	}
	releaseProfile = profile
	return a.revalidateRecordCache(profile, a.newClient(profile), zoneName)
}

func (a *App) revalidateRecordCache(profile Profile, client *WapiClient, zoneName string) error {
	now := time.Now()
	entry, err := a.readCachedRecords(profile, zoneName)
	if err != nil {
		return err
	}
	currentSerial, hasSerial, err := currentZoneSerial(client, zoneName)
	if err != nil {
		if isZoneNotFoundError(err) {
			a.invalidateRecordCache(profile, zoneName)
			return nil
		}
		return err
	}
	if entry.CacheFound && hasSerial && entry.Serial != "" && entry.Serial == currentSerial {
		// Nothing changed on Infoblox. Renew timestamps only; the cached payload
		// remains valid and avoids another large /allrecords download.
		return a.renewCachedRecordsAge(profile, zoneName, now, now.Add(a.recordsCacheSWRTTL()))
	}
	rows, err := allRecordRowsForZone(client, zoneName, false)
	if err != nil {
		return err
	}
	serial := ""
	if hasSerial {
		serial = currentSerial
	}
	return a.writeCachedRecords(profile, zoneName, serial, rows, now)
}

type zoneNotFoundError struct {
	zone string
	view string
}

func (e zoneNotFoundError) Error() string {
	return fmt.Sprintf("no DNS zone found for %s in view %s", e.zone, e.view)
}

func isZoneNotFoundError(err error) bool {
	var target zoneNotFoundError
	return errors.As(err, &target)
}

func currentZoneSerial(client *WapiClient, zoneName string) (string, bool, error) {
	matches, err := findZone(client, zoneName, zoneSerialFields)
	if err != nil {
		return "", false, err
	}
	if len(matches) == 0 {
		return "", false, zoneNotFoundError{zone: zoneName, view: client.View}
	}
	if len(matches) > 1 {
		return "", false, cliError("multiple zones found for %s; target is ambiguous", zoneName)
	}
	serial := cleanString(matches[0]["soa_serial_number"])
	serial = cleanIntegerString(serial)
	return serial, serial != "", nil
}

func sortRecords(records []TypedRecord) {
	sort.Slice(records, func(i, j int) bool {
		left := strings.ToLower(records[i].ItemString("zone") + records[i].ItemString("name") + records[i].Type + recordValue(records[i].Type, records[i].Item))
		right := strings.ToLower(records[j].ItemString("zone") + records[j].ItemString("name") + records[j].Type + recordValue(records[j].Type, records[j].Item))
		return left < right
	})
}

func (r TypedRecord) ItemString(key string) string {
	return cleanString(r.Item[key])
}

func recordKey(recordType string, item map[string]any) string {
	if ref := cleanString(item["_ref"]); ref != "" {
		return ref
	}
	return recordType + "|" + recordName(item, recordType) + "|" + recordValue(recordType, item) + "|" + cleanString(item["zone"])
}

func isZoneOrChild(zoneName, parentZone string) bool {
	zone := strings.ToLower(strings.TrimRight(zoneName, "."))
	parent := strings.ToLower(strings.TrimRight(parentZone, "."))
	return zone == parent || strings.HasSuffix(zone, "."+parent)
}

func isSameZone(zoneName, targetZone string) bool {
	zone := strings.ToLower(strings.TrimRight(strings.TrimSpace(zoneName), "."))
	target := strings.ToLower(strings.TrimRight(strings.TrimSpace(targetZone), "."))
	return zone != "" && zone == target
}

func isForwardZone(zone map[string]any) bool {
	format := strings.ToUpper(cleanString(zone["zone_format"]))
	name := strings.ToLower(cleanString(zone["fqdn"]))
	return (format == "" || format == "FORWARD") && !strings.HasSuffix(name, "in-addr.arpa") && !strings.HasSuffix(name, "ip6.arpa")
}

func (a *App) searchZones(profile Profile, client *WapiClient, rootZone string, recursive bool) ([]map[string]any, error) {
	zones, err := a.cachedZones(profile, client, "")
	if err != nil {
		return nil, err
	}
	zones = searchableRecordZones(zones)
	if rootZone == "" {
		return zones, nil
	}
	var scoped []map[string]any
	for _, zone := range zones {
		zoneName := cleanString(zone["fqdn"])
		if recursive && isZoneOrChild(zoneName, rootZone) {
			scoped = append(scoped, zone)
			continue
		}
		if !recursive && isSameZone(zoneName, rootZone) {
			scoped = append(scoped, zone)
		}
	}
	return scoped, nil
}

func searchableRecordZones(zones []map[string]any) []map[string]any {
	searchable := make([]map[string]any, 0, len(zones))
	for _, zone := range zones {
		if isSecondaryZone(zone) {
			continue
		}
		searchable = append(searchable, zone)
	}
	return searchable
}

func isSecondaryZone(zone map[string]any) bool {
	switch strings.ToLower(cleanString(zone["primary_type"])) {
	case "external", "none":
		return true
	default:
		return false
	}
}

type SearchOptions struct {
	Keyword       string
	CaseSensitive bool
	Global        bool
	Fuzzy         bool
	Recursive     bool
	Zone          string
	View          string
	Types         []string
	Exclude       []string
	Progress      SearchProgressFunc
}

type SearchProgressFunc func(SearchProgressEvent)

type SearchProgressEvent struct {
	Kind        string
	Stage       string
	WorkerID    int
	Zone        string
	Source      string
	Records     int
	Matches     int
	TotalZones  int
	WorkerCount int
	Err         error
}

const (
	searchProgressStage       = "stage"
	searchProgressWorkerStart = "worker_start"
	searchProgressWorkerDone  = "worker_done"
	searchProgressWorkerSkip  = "worker_skip"
	searchProgressWorkerError = "worker_error"
	searchProgressZoneMatched = "zone_matched"

	recordCacheSourceFreshCache     = "fresh cache"
	recordCacheSourceStaleCache     = "stale cache"
	recordCacheSourceSerialCache    = "serial-valid cache"
	recordCacheSourceAllRecords     = "allrecords"
	recordCacheSourceFreshEnriched  = "fresh cache + details"
	recordCacheSourceSerialEnriched = "serial-valid cache + details"
)

func reportSearchProgress(progress SearchProgressFunc, event SearchProgressEvent) {
	if progress != nil {
		progress(event)
	}
}

type zoneRecordBatch struct {
	ZoneName string
	Records  []TypedRecord
}

type zoneRecordJob struct {
	index    int
	zoneName string
}

type zoneRecordResult struct {
	index    int
	zoneName string
	records  []TypedRecord
	err      error
}

func (a *App) collectSearchResults(profile Profile, client *WapiClient, options SearchOptions) ([]TypedRecord, error) {
	if options.Global && options.Zone != "" {
		return nil, cliError("--zone cannot be used with -g/--global search")
	}
	if options.Global && options.Recursive {
		return nil, cliError("--recursive cannot be used with -g/--global search")
	}
	reportSearchProgress(options.Progress, SearchProgressEvent{Kind: searchProgressStage, Stage: "Resolving search scope"})
	rootZone := ""
	var err error
	if !options.Global {
		rootZone, err = a.resolveDNSZone(profile, options.Zone)
		if err != nil {
			return nil, err
		}
	}
	reportSearchProgress(options.Progress, SearchProgressEvent{Kind: searchProgressStage, Stage: "Loading searchable zones"})
	zones, err := a.searchZones(profile, client, rootZone, options.Recursive)
	if err != nil {
		return nil, err
	}
	reportSearchProgress(options.Progress, SearchProgressEvent{Kind: searchProgressStage, Stage: "Loading zone records", TotalZones: len(zones)})
	typeFilter := map[string]bool{}
	for _, item := range options.Types {
		if item == "" {
			continue
		}
		typeFilter[strings.ToLower(item)] = true
	}
	batches, err := a.searchZoneRecordBatches(profile, client, zones, false, options.Progress)
	if err != nil {
		return nil, err
	}
	reportSearchProgress(options.Progress, SearchProgressEvent{Kind: searchProgressStage, Stage: "Matching records", TotalZones: len(zones)})
	seen := map[string]bool{}
	var records []TypedRecord
	for _, batch := range batches {
		matches := 0
		for _, record := range batch.Records {
			record.Item["zone"] = firstNonEmpty(cleanString(record.Item["zone"]), batch.ZoneName)
			if len(typeFilter) > 0 && !typeFilter[strings.ToLower(record.Type)] {
				continue
			}
			if !recordMatches(record, options) {
				continue
			}
			matches++
			key := recordKey(record.Type, record.Item)
			if seen[key] {
				continue
			}
			seen[key] = true
			records = append(records, record)
		}
		reportSearchProgress(options.Progress, SearchProgressEvent{Kind: searchProgressZoneMatched, Zone: batch.ZoneName, Matches: matches})
	}
	sortRecords(records)
	reportSearchProgress(options.Progress, SearchProgressEvent{Kind: searchProgressStage, Stage: "Search complete", TotalZones: len(zones), Matches: len(records)})
	return records, nil
}

func (a *App) listRecordsForZone(profile Profile, client *WapiClient, zoneName string, recursive bool, enrich bool) ([]TypedRecord, error) {
	if !recursive {
		return a.cachedRecordsForZoneWithDetails(profile, client, zoneName, enrich)
	}
	zones, err := a.searchZones(profile, client, zoneName, true)
	if err != nil {
		return nil, err
	}
	batches, err := a.searchZoneRecordBatches(profile, client, zones, enrich, nil)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	records := make([]TypedRecord, 0)
	for _, batch := range batches {
		for _, record := range batch.Records {
			record.Item["zone"] = firstNonEmpty(cleanString(record.Item["zone"]), batch.ZoneName)
			key := recordKey(record.Type, record.Item)
			if seen[key] {
				continue
			}
			seen[key] = true
			records = append(records, record)
		}
	}
	sortRecords(records)
	return records, nil
}

func (a *App) searchZoneRecordBatches(profile Profile, client *WapiClient, zones []map[string]any, enrich bool, progress SearchProgressFunc) ([]zoneRecordBatch, error) {
	if len(zones) == 0 {
		reportSearchProgress(progress, SearchProgressEvent{Kind: searchProgressStage, Stage: "No searchable zones"})
		return nil, nil
	}
	if len(zones) == 1 {
		zoneName := cleanString(zones[0]["fqdn"])
		reportSearchProgress(progress, SearchProgressEvent{Kind: searchProgressStage, Stage: "Starting workers", TotalZones: 1, WorkerCount: 1})
		reportSearchProgress(progress, SearchProgressEvent{Kind: searchProgressWorkerStart, WorkerID: 1, Zone: zoneName, Stage: "Checking cache"})
		records, source, err := a.cachedRecordsForZoneWithSourceAndDetails(profile, client, zoneName, enrich)
		if err != nil {
			if isSecondaryZoneDataUnavailable(err) {
				reportSearchProgress(progress, SearchProgressEvent{Kind: searchProgressWorkerSkip, WorkerID: 1, Zone: zoneName, Stage: "Secondary zone data unavailable", Err: err})
				return nil, nil
			}
			reportSearchProgress(progress, SearchProgressEvent{Kind: searchProgressWorkerError, WorkerID: 1, Zone: zoneName, Err: err})
			return nil, err
		}
		reportSearchProgress(progress, SearchProgressEvent{Kind: searchProgressWorkerDone, WorkerID: 1, Zone: zoneName, Source: source, Records: len(records)})
		return []zoneRecordBatch{{ZoneName: zoneName, Records: records}}, nil
	}

	workerCount := a.dnsSearchWorkerLimit()
	if len(zones) < workerCount {
		workerCount = len(zones)
	}
	reportSearchProgress(progress, SearchProgressEvent{Kind: searchProgressStage, Stage: "Starting workers", TotalZones: len(zones), WorkerCount: workerCount})

	jobs := make(chan zoneRecordJob)
	results := make(chan zoneRecordResult, len(zones))
	done := make(chan struct{})
	var doneOnce sync.Once
	cancel := func() {
		doneOnce.Do(func() {
			close(done)
		})
	}

	var wg sync.WaitGroup
	for id := 1; id <= workerCount; id++ {
		workerID := id
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				case job, ok := <-jobs:
					if !ok {
						return
					}
					reportSearchProgress(progress, SearchProgressEvent{Kind: searchProgressWorkerStart, WorkerID: workerID, Zone: job.zoneName, Stage: "Checking cache"})
					records, source, err := a.cachedRecordsForZoneWithSourceAndDetails(profile, client, job.zoneName, enrich)
					if err != nil {
						if isSecondaryZoneDataUnavailable(err) {
							reportSearchProgress(progress, SearchProgressEvent{Kind: searchProgressWorkerSkip, WorkerID: workerID, Zone: job.zoneName, Stage: "Secondary zone data unavailable", Err: err})
						} else {
							reportSearchProgress(progress, SearchProgressEvent{Kind: searchProgressWorkerError, WorkerID: workerID, Zone: job.zoneName, Err: err})
						}
					} else {
						reportSearchProgress(progress, SearchProgressEvent{Kind: searchProgressWorkerDone, WorkerID: workerID, Zone: job.zoneName, Source: source, Records: len(records)})
					}
					results <- zoneRecordResult{index: job.index, zoneName: job.zoneName, records: records, err: err}
				}
			}
		}()
	}

	go func() {
		defer close(jobs)
		for index, zone := range zones {
			zoneName := cleanString(zone["fqdn"])
			select {
			case <-done:
				return
			case jobs <- zoneRecordJob{index: index, zoneName: zoneName}:
			}
		}
	}()

	go func() {
		wg.Wait()
		close(results)
	}()

	batchesByIndex := make([]*zoneRecordBatch, len(zones))
	var firstErr error
	for result := range results {
		if result.err != nil {
			if isSecondaryZoneDataUnavailable(result.err) {
				continue
			}
			if firstErr == nil {
				firstErr = result.err
				cancel()
			}
			continue
		}
		batch := zoneRecordBatch{ZoneName: result.zoneName, Records: result.records}
		batchesByIndex[result.index] = &batch
	}
	if firstErr != nil {
		return nil, firstErr
	}
	batches := make([]zoneRecordBatch, 0, len(batchesByIndex))
	for _, batch := range batchesByIndex {
		if batch == nil {
			continue
		}
		batches = append(batches, *batch)
	}
	return batches, nil
}

func isSecondaryZoneDataUnavailable(err error) bool {
	var wapiErr *WapiError
	if !errors.As(err, &wapiErr) {
		return false
	}
	return strings.Contains(strings.ToLower(wapiErr.Text), "secondary zone data unavailable")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func recordMatches(record TypedRecord, options SearchOptions) bool {
	values := []string{
		recordName(record.Item, record.Type),
		recordValue(record.Type, record.Item),
		cleanString(record.Item["comment"]),
	}
	for _, exclude := range options.Exclude {
		if searchValuesMatch(values, exclude, options.CaseSensitive, options.Fuzzy) {
			return false
		}
	}
	return searchValuesMatch(values, options.Keyword, options.CaseSensitive, options.Fuzzy)
}

func searchValuesMatch(values []string, keyword string, caseSensitive bool, fuzzy bool) bool {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return true
	}
	for _, value := range values {
		if textMatches(value, keyword, caseSensitive, fuzzy) {
			return true
		}
	}
	return false
}

func textMatches(value, keyword string, caseSensitive bool, fuzzy bool) bool {
	if !caseSensitive {
		value = strings.ToLower(value)
		keyword = strings.ToLower(keyword)
	}
	if strings.Contains(value, keyword) {
		return true
	}
	if !fuzzy || len(keyword) < 3 {
		return false
	}
	for _, candidate := range fuzzyCandidates(value) {
		if normalizedDistance(candidate, keyword) <= 0.25 {
			return true
		}
	}
	return false
}

func fuzzyCandidates(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && r != '-' && r != '_'
	})
	if len(fields) == 0 {
		return []string{value}
	}
	return fields
}

func normalizedDistance(a, b string) float64 {
	if a == "" && b == "" {
		return 0
	}
	distance := levenshtein(a, b)
	maxLen := math.Max(float64(len(a)), float64(len(b)))
	return float64(distance) / maxLen
}

func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i, ca := range ra {
		curr := make([]int, len(rb)+1)
		curr[0] = i + 1
		for j, cb := range rb {
			cost := 0
			if ca != cb {
				cost = 1
			}
			curr[j+1] = minInt(curr[j]+1, prev[j+1]+1, prev[j]+cost)
		}
		prev = curr
	}
	return prev[len(rb)]
}

func minInt(values ...int) int {
	best := values[0]
	for _, value := range values[1:] {
		if value < best {
			best = value
		}
	}
	return best
}

func parseRecordTypes(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var types []string
	for _, item := range strings.Split(raw, ",") {
		recordType := strings.ToLower(strings.TrimSpace(item))
		if _, ok := recordTypes[recordType]; !ok {
			return nil, cliError("unsupported record type %q. Supported: %s", recordType, strings.Join(supportedRecordTypes(), ", "))
		}
		types = append(types, recordType)
	}
	return types, nil
}

func (a *App) findForwardRecords(profile Profile, client *WapiClient, recordNameValue, zone string) (string, []TypedRecord, []TypedRecord, error) {
	recordNameValue = strings.TrimRight(strings.TrimSpace(recordNameValue), ".")
	if recordNameValue == "" {
		return "", nil, nil, cliError("record name is required")
	}
	var targets []string
	if zone != "" {
		resolvedZone, err := a.resolveDNSZone(profile, zone)
		if err != nil {
			return "", nil, nil, err
		}
		target, err := fqdn(recordNameValue, resolvedZone)
		if err != nil {
			return "", nil, nil, err
		}
		targets = []string{target}
	} else {
		if strings.Contains(recordNameValue, ".") {
			targets = append(targets, recordNameValue)
		}
		activeZone, err := a.resolveDNSZone(profile, "")
		if err == nil {
			target, err := fqdn(recordNameValue, activeZone)
			if err == nil && !containsString(targets, target) {
				targets = append(targets, target)
			}
		}
	}
	var firstTarget string
	var allMatches []TypedRecord
	for _, target := range targets {
		if firstTarget == "" {
			firstTarget = target
		}
		var matches []TypedRecord
		for recordType, spec := range recordTypes {
			if recordType == "ptr" {
				continue
			}
			results, err := pagedQuery(client, spec.Object, objectQueryParams(spec, client, map[string]string{"name": target}))
			if err != nil {
				return firstTarget, nil, nil, err
			}
			for _, result := range results {
				matches = append(matches, TypedRecord{Type: recordType, Item: result})
			}
		}
		if len(matches) > 0 {
			return target, matches, matches, nil
		}
		allMatches = append(allMatches, matches...)
	}
	if firstTarget == "" {
		firstTarget = recordNameValue
	}
	return firstTarget, nil, allMatches, nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func recordRef(record TypedRecord) (string, error) {
	ref := cleanString(record.Item["_ref"])
	if ref == "" {
		return "", cliError("matched record does not include an _ref")
	}
	return ref, nil
}

func reversePointer(address netip.Addr) string {
	if address.Is4() {
		bytes := address.As4()
		return fmt.Sprintf("%d.%d.%d.%d.in-addr.arpa", bytes[3], bytes[2], bytes[1], bytes[0])
	}
	bytes := address.As16()
	nibbles := make([]string, 0, 32)
	for i := len(bytes) - 1; i >= 0; i-- {
		nibbles = append(nibbles, fmt.Sprintf("%x", bytes[i]&0x0f), fmt.Sprintf("%x", bytes[i]>>4))
	}
	return strings.Join(nibbles, ".") + ".ip6.arpa"
}

func reverseZoneForIP(client *WapiClient, address netip.Addr) (string, error) {
	expectedFormat := "IPV6"
	if address.Is4() {
		expectedFormat = "IPV4"
	}
	pointer := reversePointer(address)
	zones, err := queryZones(client, "")
	if err != nil {
		return "", err
	}
	var matches []string
	for _, zone := range zones {
		format := strings.ToUpper(cleanString(zone["zone_format"]))
		if format != "" && format != expectedFormat {
			continue
		}
		zoneName := cleanString(zone["fqdn"])
		if zoneName != "" && isZoneOrChild(pointer, zoneName) {
			matches = append(matches, zoneName)
		}
	}
	if len(matches) == 0 {
		return "", cliError("no %s reverse DNS zone found for %s in view %s", expectedFormat, address, client.View)
	}
	sort.Slice(matches, func(i, j int) bool {
		if len(matches[i]) == len(matches[j]) {
			return matches[i] < matches[j]
		}
		return len(matches[i]) > len(matches[j])
	})
	return matches[0], nil
}

func ptrMatches(client *WapiClient, address netip.Addr) (string, []TypedRecord, error) {
	reverseZone, err := reverseZoneForIP(client, address)
	if err != nil {
		return "", nil, err
	}
	matches, err := ptrMatchesInZone(client, address, reverseZone)
	return reverseZone, matches, err
}

func ptrMatchesInZone(client *WapiClient, address netip.Addr, reverseZone string) ([]TypedRecord, error) {
	field := "ipv6addr"
	if address.Is4() {
		field = "ipv4addr"
	}
	spec := recordTypes["ptr"]
	results, err := pagedQuery(client, spec.Object, objectQueryParams(spec, client, map[string]string{field: address.String()}))
	if err != nil {
		return nil, err
	}
	var matches []TypedRecord
	for _, item := range results {
		if zone := cleanString(item["zone"]); zone != "" && zone != reverseZone {
			continue
		}
		matches = append(matches, TypedRecord{Type: "ptr", Item: item})
	}
	return matches, nil
}
