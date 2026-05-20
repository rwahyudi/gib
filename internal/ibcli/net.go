package ibcli

import (
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

const (
	networkViewReturnFields = "name,comment"
	ipv4AddressReturnFields = "ip_address,network,network_view,status,types,names,mac_address,lease_state,usage,comment"
	defaultNetSortField     = "network"
	netCacheKindViews       = "network_views"
	netCacheKindNetworks    = "networks"
	netCacheKindContainers  = "network_containers"
	netCacheKindAddresses   = "ipv4_addresses"
	ipamTypeNetwork         = "network"
	ipamTypeContainer       = "container"
)

var (
	networkViewOutputColumns   = []string{"name", "comment"}
	networkOutputColumns       = []string{"type", "network", "network_view", "comment"}
	networkDetailOutputColumns = []string{"type", "network", "network_view", "comment"}
	ipv4AddressOutputColumns   = []string{"ip", "network", "container", "network_view", "status", "types", "names", "mac_address", "lease_state", "comment"}
	netNextIPOutputColumns     = []string{"type", "network", "network_view", "ip"}
	netSortFields              = []string{"type", "network", "network_view", "comment"}
)

type NetSort struct {
	Enabled bool
	Field   string
	Desc    bool
}

func (a *App) runNetViewList() error {
	profile, client, err := a.configuredClient()
	if err != nil {
		return err
	}
	views, err := a.cachedNetworkViews(profile, client)
	if err != nil {
		return err
	}
	rows := make([]map[string]any, 0, len(views))
	for _, view := range views {
		rows = append(rows, map[string]any{
			"name":    cleanString(view["name"]),
			"comment": cleanString(view["comment"]),
		})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return compareCaseInsensitiveText(cleanString(rows[i]["name"]), cleanString(rows[j]["name"])) < 0
	})
	if len(rows) == 0 && a.isTableOutput() {
		a.PrintWarning("No IPAM network views found.")
	}
	return a.emitRows(fmt.Sprintf("IPAM Network Views (%d)", len(rows)), networkViewOutputColumns, rows)
}

func (a *App) runNetList(search string, networkView string, option NetSort, columns []string) error {
	profile, client, err := a.configuredClient()
	if err != nil {
		return err
	}
	networks, err := a.cachedNetworks(profile, client, networkView)
	if err != nil {
		return err
	}
	containers, err := a.cachedNetworkContainers(profile, client, networkView)
	if err != nil {
		return err
	}
	objects := filterNetworks(networkObjectRows(networks, containers), search)
	rows := make([]map[string]any, 0, len(objects))
	for _, object := range objects {
		rows = append(rows, networkOutputRow(object))
	}
	sortNetworkRows(rows)
	applyNetSort(rows, option)
	rows = selectNetworkOutputRows(rows, columns)
	if len(rows) == 0 && a.isTableOutput() {
		a.PrintWarning("No IPAM networks or containers found.")
	}
	return a.emitRows(fmt.Sprintf("IPAM Networks and Containers (%d)", len(rows)), columns, rows)
}

func (a *App) runNetShow(network string, networkView string) error {
	profile, client, err := a.configuredClient()
	if err != nil {
		return err
	}
	matchedNetwork, err := a.cachedFindNetworkObject(profile, client, network, networkView)
	if err != nil {
		return err
	}
	row := networkDetailRow(matchedNetwork)
	title := ipamObjectTitle(row)
	if a.isTableOutput() {
		fmt.Fprintln(a.Stdout, renderTable(title, []string{"Field", "Value"}, objectDetailRows(networkDetailOutputColumns, row)))
		return nil
	}
	return a.emitObject(title, networkDetailOutputColumns, row)
}

func (a *App) runNetAddress(address string, networkView string) error {
	ip, err := normalizeIPv4Address(address)
	if err != nil {
		return err
	}
	profile, client, err := a.configuredClient()
	if err != nil {
		return err
	}
	results, err := a.cachedIPv4Addresses(profile, client, ip, networkView)
	if err != nil {
		return err
	}
	if len(results) == 0 {
		return cliError("no IPv4 address found for %s", ip)
	}
	containers, err := a.cachedNetworkContainers(profile, client, networkView)
	if err != nil {
		return err
	}
	rows := make([]map[string]any, 0, len(results))
	for _, result := range results {
		row := ipv4AddressOutputRow(result)
		row["container"] = containingContainerForIP(ip, cleanString(row["network_view"]), containers)
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if result := compareCaseInsensitiveText(cleanString(rows[i]["network_view"]), cleanString(rows[j]["network_view"])); result != 0 {
			return result < 0
		}
		return compareCaseInsensitiveText(cleanString(rows[i]["network"]), cleanString(rows[j]["network"])) < 0
	})
	return a.emitRows(fmt.Sprintf("IPAM Addresses (%d)", len(rows)), ipv4AddressOutputColumns, rows)
}

func (a *App) runDNSNextIP(network string, networkView string, num int, exclude []string) error {
	return a.runNextIP(network, networkView, num, exclude, true, false)
}

func (a *App) runNetNextIP(network string, networkView string, num int, exclude []string) error {
	return a.runNextIP(network, networkView, num, exclude, false, true)
}

func (a *App) runNextIP(network string, networkView string, num int, exclude []string, printContext bool, cachedLookup bool) error {
	profile, client, err := a.configuredClient()
	if err != nil {
		return err
	}
	var rows []map[string]any
	if cachedLookup {
		matchedNetwork, findErr := a.cachedFindNetworkObject(profile, client, network, networkView)
		if findErr != nil {
			return findErr
		}
		rows, err = nextAvailableIPRowsForNetwork(client, matchedNetwork, network, num, exclude)
	} else {
		rows, err = nextAvailableIPRows(client, network, networkView, num, exclude)
	}
	if err != nil {
		return err
	}
	if printContext && a.isTableOutput() {
		a.PrintContext()
	}
	fields := nextIPOutputColumns
	if cachedLookup {
		fields = netNextIPOutputColumns
	}
	return a.emitRows("Next Available IPs", fields, rows)
}

func queryNetworkViews(client *WapiClient) ([]map[string]any, error) {
	params := url.Values{"_return_fields": []string{networkViewReturnFields}}
	return pagedQuery(client, networkViewObject, params)
}

func queryNetworkContainers(client *WapiClient, networkView string) ([]map[string]any, error) {
	params := url.Values{}
	params.Set("_return_fields", networkReturnFields)
	if strings.TrimSpace(networkView) != "" {
		params.Set("network_view", strings.TrimSpace(networkView))
	}
	return pagedQuery(client, networkContainerObject, params)
}

func (a *App) cachedNetworkViews(profile Profile, client *WapiClient) ([]map[string]any, error) {
	return a.cachedNetRows(
		profile,
		netCacheKindViews,
		"",
		"",
		func() (cachedPayload, error) { return a.readCachedNetworkViews(profile) },
		func() ([]map[string]any, error) { return a.refreshNetworkViewCache(profile, client) },
	)
}

func (a *App) cachedNetworks(profile Profile, client *WapiClient, networkView string) ([]map[string]any, error) {
	networkView = strings.TrimSpace(networkView)
	return a.cachedNetRows(
		profile,
		netCacheKindNetworks,
		networkView,
		"",
		func() (cachedPayload, error) { return a.readCachedNetworks(profile, networkView) },
		func() ([]map[string]any, error) { return a.refreshNetworkCache(profile, client, networkView) },
	)
}

func (a *App) cachedNetworkContainers(profile Profile, client *WapiClient, networkView string) ([]map[string]any, error) {
	networkView = strings.TrimSpace(networkView)
	return a.cachedNetRows(
		profile,
		netCacheKindContainers,
		networkView,
		"",
		func() (cachedPayload, error) { return a.readCachedNetworkContainers(profile, networkView) },
		func() ([]map[string]any, error) { return a.refreshNetworkContainerCache(profile, client, networkView) },
	)
}

func (a *App) cachedIPv4Addresses(profile Profile, client *WapiClient, ip string, networkView string) ([]map[string]any, error) {
	networkView = strings.TrimSpace(networkView)
	return a.cachedNetRows(
		profile,
		netCacheKindAddresses,
		networkView,
		ip,
		func() (cachedPayload, error) { return a.readCachedIPv4Addresses(profile, ip, networkView) },
		func() ([]map[string]any, error) { return a.refreshIPv4AddressCache(profile, client, ip, networkView) },
	)
}

func (a *App) cachedNetRows(profile Profile, kind string, networkView string, ip string, read func() (cachedPayload, error), refresh func() ([]map[string]any, error)) ([]map[string]any, error) {
	now := time.Now()
	entry, err := read()
	if err == nil && entry.CacheFound {
		if a.cacheEntryFresh(entry, now) {
			return entry.Rows, nil
		}
		// IPAM has no cheap serial equivalent. Inside SWR, return cached data and
		// let one lease-protected background worker re-download the same WAPI data.
		if now.Unix() < entry.StaleExpiresAt {
			a.startNetCacheRefreshAsync(profile, kind, networkView, ip)
			return entry.Rows, nil
		}
	}

	if waited, waitErr := a.waitForActiveNetRefresh(profile, kind, networkView, ip, a.maxBackgroundWorkerWait(), 2*time.Millisecond); waitErr == nil && waited {
		now = time.Now()
		entry, err = read()
		if err == nil && entry.CacheFound {
			if a.cacheEntryFresh(entry, now) {
				return entry.Rows, nil
			}
			if now.Unix() < entry.StaleExpiresAt {
				a.startNetCacheRefreshAsync(profile, kind, networkView, ip)
				return entry.Rows, nil
			}
		}
	}
	return refresh()
}

func (a *App) refreshNetworkViewCache(profile Profile, client *WapiClient) ([]map[string]any, error) {
	rows, err := queryNetworkViews(client)
	if err != nil {
		return nil, err
	}
	_ = a.writeCachedNetworkViews(profile, rows, time.Now())
	return rows, nil
}

func (a *App) refreshNetworkCache(profile Profile, client *WapiClient, networkView string) ([]map[string]any, error) {
	rows, err := queryNetworks(client, networkView)
	if err != nil {
		return nil, err
	}
	_ = a.writeCachedNetworks(profile, networkView, rows, time.Now())
	return rows, nil
}

func (a *App) refreshNetworkContainerCache(profile Profile, client *WapiClient, networkView string) ([]map[string]any, error) {
	rows, err := queryNetworkContainers(client, networkView)
	if err != nil {
		return nil, err
	}
	_ = a.writeCachedNetworkContainers(profile, networkView, rows, time.Now())
	return rows, nil
}

func (a *App) refreshIPv4AddressCache(profile Profile, client *WapiClient, ip string, networkView string) ([]map[string]any, error) {
	params := url.Values{"_return_fields": []string{ipv4AddressReturnFields}, "ip_address": []string{ip}}
	if strings.TrimSpace(networkView) != "" {
		params.Set("network_view", strings.TrimSpace(networkView))
	}
	rows, err := pagedQuery(client, ipv4AddressObject, params)
	if err != nil {
		return nil, err
	}
	_ = a.writeCachedIPv4Addresses(profile, ip, networkView, rows, time.Now())
	return rows, nil
}

func (a *App) cachedFindNetwork(profile Profile, client *WapiClient, network string, networkView string) (map[string]any, error) {
	cidr, err := normalizeNextIPNetwork(network)
	if err != nil {
		return nil, err
	}
	networks, err := a.cachedNetworks(profile, client, networkView)
	if err != nil {
		return nil, err
	}
	return findNetworkInRows(networks, cidr)
}

func (a *App) cachedFindNetworkObject(profile Profile, client *WapiClient, network string, networkView string) (map[string]any, error) {
	cidr, err := normalizeNextIPNetwork(network)
	if err != nil {
		return nil, err
	}
	networks, err := a.cachedNetworks(profile, client, networkView)
	if err != nil {
		return nil, err
	}
	containers, err := a.cachedNetworkContainers(profile, client, networkView)
	if err != nil {
		return nil, err
	}
	return findNetworkObjectInRows(networkObjectRows(networks, containers), cidr)
}

func findNetworkInRows(networks []map[string]any, cidr string) (map[string]any, error) {
	var matches []map[string]any
	for _, network := range networks {
		if cleanString(network["network"]) == cidr {
			matches = append(matches, network)
		}
	}
	if len(matches) == 0 {
		return nil, cliError("no network found for %s", cidr)
	}
	if len(matches) > 1 {
		return nil, cliError("multiple networks found for %s; use --network-view to choose one", cidr)
	}
	return matches[0], nil
}

func findNetworkObjectInRows(objects []map[string]any, cidr string) (map[string]any, error) {
	var matches []map[string]any
	for _, object := range objects {
		if cleanString(object["network"]) == cidr {
			matches = append(matches, object)
		}
	}
	if len(matches) == 0 {
		return nil, cliError("no network or container found for %s", cidr)
	}
	containers := make([]map[string]any, 0, len(matches))
	for _, match := range matches {
		if cleanString(match["type"]) == ipamTypeContainer {
			containers = append(containers, match)
		}
	}
	if len(containers) > 0 {
		matches = containers
	}
	if len(matches) > 1 {
		return nil, cliError("multiple networks or containers found for %s; use --network-view to choose one", cidr)
	}
	return matches[0], nil
}

func (a *App) startNetCacheRefreshAsync(profile Profile, kind string, networkView string, ip string) {
	_ = a.startNetCacheRefresh(profile, kind, networkView, ip)
}

func (a *App) startNetCacheRefresh(profile Profile, kind string, networkView string, ip string) error {
	acquired, err := a.tryAcquireNetRefreshLease(profile, kind, networkView, ip, time.Now(), recordRefreshLeaseTTL)
	if err != nil || !acquired {
		return err
	}
	if a.backgroundNetRefresher != nil {
		if err := a.backgroundNetRefresher(profile, kind, networkView, ip); err != nil {
			_ = a.releaseNetRefreshLease(profile, kind, networkView, ip)
			return err
		}
		return nil
	}
	executable, err := os.Executable()
	if err != nil {
		_ = a.releaseNetRefreshLease(profile, kind, networkView, ip)
		return err
	}
	args := []string{
		"config", "cache", "refresh-net",
		"--profile", firstNonEmpty(strings.TrimSpace(profile.Name), defaultProfileName),
		"--kind", kind,
	}
	if strings.TrimSpace(networkView) != "" {
		args = append(args, "--network-view", strings.TrimSpace(networkView))
	}
	if strings.TrimSpace(ip) != "" {
		args = append(args, "--ip", strings.TrimSpace(ip))
	}
	cmd := exec.Command(executable, args...) // #nosec G204 -- executable is this ib binary and args are fixed internal cache-refresh flags
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		_ = a.releaseNetRefreshLease(profile, kind, networkView, ip)
		return err
	}
	go func() {
		_ = cmd.Wait()
	}()
	return nil
}

func (a *App) runNetCacheRefresh(profileName string, kind string, networkView string, ip string) error {
	releaseProfile := Profile{Name: strings.TrimSpace(profileName)}
	if releaseProfile.Name == "" {
		releaseProfile.Name = defaultProfileName
	}
	defer func() {
		_ = a.releaseNetRefreshLease(releaseProfile, kind, networkView, ip)
	}()
	profile, err := a.loadConfigProfile(profileName, true)
	if err != nil {
		return err
	}
	releaseProfile = profile
	client := a.newClient(profile)
	switch kind {
	case netCacheKindViews:
		_, err = a.refreshNetworkViewCache(profile, client)
	case netCacheKindNetworks:
		_, err = a.refreshNetworkCache(profile, client, networkView)
	case netCacheKindContainers:
		_, err = a.refreshNetworkContainerCache(profile, client, networkView)
	case netCacheKindAddresses:
		if strings.TrimSpace(ip) == "" {
			err = cliError("--ip is required for %s refresh", netCacheKindAddresses)
			break
		}
		_, err = a.refreshIPv4AddressCache(profile, client, ip, networkView)
	default:
		err = cliError("unsupported net cache refresh kind %q", kind)
	}
	return err
}

func filterNetworks(networks []map[string]any, search string) []map[string]any {
	search = strings.TrimSpace(search)
	if search == "" {
		return networks
	}
	filtered := make([]map[string]any, 0, len(networks))
	seen := map[string]bool{}
	for _, network := range networks {
		values := []string{
			cleanString(network["type"]),
			cleanString(network["network"]),
			cleanString(network["network_view"]),
			cleanString(network["comment"]),
		}
		if searchValuesMatch(values, search, false, false) {
			appendNetworkSearchRow(&filtered, seen, network)
			if shouldExpandNetworkHierarchyForSearch(network, search) {
				appendRelatedNetworkSearchRows(&filtered, seen, network, networks)
			}
		}
	}
	return filtered
}

func appendNetworkSearchRow(rows *[]map[string]any, seen map[string]bool, row map[string]any) {
	key := strings.Join([]string{
		cleanString(row["type"]),
		cleanString(row["network_view"]),
		cleanString(row["network"]),
		cleanString(row["_ref"]),
	}, "\x00")
	if seen[key] {
		return
	}
	seen[key] = true
	*rows = append(*rows, row)
}

func shouldExpandNetworkHierarchyForSearch(network map[string]any, search string) bool {
	return textMatches(cleanString(network["network"]), search, false, false)
}

func appendRelatedNetworkSearchRows(rows *[]map[string]any, seen map[string]bool, network map[string]any, objects []map[string]any) {
	targetPrefix, ok := parseIPv4Prefix(cleanString(network["network"]))
	if !ok {
		return
	}
	networkView := cleanString(network["network_view"])
	for _, object := range objects {
		if cleanString(object["network_view"]) != networkView {
			continue
		}
		objectPrefix, ok := parseIPv4Prefix(cleanString(object["network"]))
		if !ok || objectPrefix == targetPrefix {
			continue
		}
		if networkPrefixesRelated(targetPrefix, objectPrefix) {
			appendNetworkSearchRow(rows, seen, object)
		}
	}
}

func networkPrefixesRelated(target netip.Prefix, candidate netip.Prefix) bool {
	switch {
	case candidate.Bits() < target.Bits():
		return candidate.Contains(target.Addr())
	case candidate.Bits() > target.Bits():
		return target.Contains(candidate.Addr())
	default:
		return false
	}
}

func networkObjectRows(networks []map[string]any, containers []map[string]any) []map[string]any {
	rows := make([]map[string]any, 0, len(networks)+len(containers))
	for _, container := range containers {
		rows = append(rows, networkObjectRow(container, ipamTypeContainer))
	}
	for _, network := range networks {
		rows = append(rows, networkObjectRow(network, ipamTypeNetwork))
	}
	return rows
}

func networkObjectRow(item map[string]any, itemType string) map[string]any {
	return map[string]any{
		"_ref":         cleanString(item["_ref"]),
		"type":         firstNonEmpty(cleanString(item["type"]), itemType),
		"network":      cleanString(item["network"]),
		"network_view": cleanString(item["network_view"]),
		"comment":      cleanString(item["comment"]),
	}
}

func networkOutputRow(network map[string]any) map[string]any {
	return map[string]any{
		"type":         cleanString(network["type"]),
		"network":      cleanString(network["network"]),
		"network_view": cleanString(network["network_view"]),
		"comment":      cleanString(network["comment"]),
	}
}

func networkDetailRow(network map[string]any) map[string]any {
	return map[string]any{
		"type":         cleanString(network["type"]),
		"network":      cleanString(network["network"]),
		"network_view": cleanString(network["network_view"]),
		"comment":      cleanString(network["comment"]),
	}
}

func ipamObjectTitle(row map[string]any) string {
	name := cleanString(row["network"])
	if cleanString(row["type"]) == ipamTypeContainer {
		return "IPAM Container: " + name
	}
	return "IPAM Network: " + name
}

func ipv4AddressOutputRow(item map[string]any) map[string]any {
	return map[string]any{
		"ip":           cleanString(firstNonEmpty(cleanString(item["ip_address"]), cleanString(item["ipv4addr"]))),
		"network":      cleanString(item["network"]),
		"container":    cleanString(item["container"]),
		"network_view": cleanString(item["network_view"]),
		"status":       cleanString(item["status"]),
		"types":        strings.Join(stringValues(item["types"]), ", "),
		"names":        strings.Join(stringValues(item["names"]), ", "),
		"mac_address":  cleanString(item["mac_address"]),
		"lease_state":  cleanString(item["lease_state"]),
		"comment":      cleanString(item["comment"]),
	}
}

func containingContainerForIP(ip string, networkView string, containers []map[string]any) string {
	address, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil || !address.Is4() {
		return ""
	}
	networkView = strings.TrimSpace(networkView)
	var best netip.Prefix
	var bestCIDR string
	for _, container := range containers {
		if networkView != "" && cleanString(container["network_view"]) != networkView {
			continue
		}
		cidr := cleanString(container["network"])
		prefix, ok := parseIPv4Prefix(cidr)
		if !ok || !prefix.Contains(address) {
			continue
		}
		if bestCIDR == "" || prefix.Bits() > best.Bits() {
			best = prefix
			bestCIDR = cidr
		}
	}
	return bestCIDR
}

func normalizeIPv4Address(raw string) (string, error) {
	address, err := netip.ParseAddr(strings.TrimSpace(raw))
	if err != nil || !address.Is4() {
		return "", cliError("address must be an IPv4 address")
	}
	return address.String(), nil
}

func parseNetSort(raw string, enabled bool) (NetSort, error) {
	if !enabled {
		return NetSort{}, nil
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = defaultNetSortField
	}
	desc := strings.HasPrefix(raw, "-")
	if desc {
		raw = strings.TrimPrefix(raw, "-")
	}
	field := strings.ToLower(strings.TrimSpace(raw))
	if !isNetSortField(field) {
		return NetSort{}, cliError("unsupported network sort field %q. Supported: %s", field, strings.Join(netSortFields, ", "))
	}
	return NetSort{Enabled: true, Field: field, Desc: desc}, nil
}

func isNetSortField(field string) bool {
	field = strings.ToLower(strings.TrimSpace(field))
	for _, candidate := range netSortFields {
		if field == candidate {
			return true
		}
	}
	return false
}

func sortNetworkRows(rows []map[string]any) {
	sort.SliceStable(rows, func(i, j int) bool {
		result := compareNetworkRows(rows[i], rows[j], defaultNetSortField, false)
		if result == 0 {
			return false
		}
		return result < 0
	})
}

func applyNetSort(rows []map[string]any, option NetSort) {
	if !option.Enabled || len(rows) < 2 {
		return
	}
	sort.SliceStable(rows, func(i, j int) bool {
		result := compareNetworkRows(rows[i], rows[j], option.Field, option.Desc)
		if result == 0 {
			return false
		}
		return result < 0
	})
}

func compareNetworkRows(left map[string]any, right map[string]any, field string, desc bool) int {
	var result int
	switch field {
	case "network":
		result = compareNetworkCIDR(cleanString(left["network"]), cleanString(right["network"]))
		if result == 0 {
			result = compareCaseInsensitiveText(cleanString(left["network_view"]), cleanString(right["network_view"]))
		}
	case "type":
		result = compareCaseInsensitiveText(cleanString(left["type"]), cleanString(right["type"]))
	case "network_view":
		result = compareCaseInsensitiveText(cleanString(left["network_view"]), cleanString(right["network_view"]))
	case "comment":
		result = compareCaseInsensitiveText(cleanString(left["comment"]), cleanString(right["comment"]))
	default:
		result = compareCaseInsensitiveText(cleanString(left[field]), cleanString(right[field]))
	}
	return applySortDirection(result, desc)
}

func compareNetworkCIDR(left string, right string) int {
	leftPrefix, leftOK := parseIPv4Prefix(left)
	rightPrefix, rightOK := parseIPv4Prefix(right)
	switch {
	case leftOK && rightOK:
		if result := leftPrefix.Addr().Compare(rightPrefix.Addr()); result != 0 {
			return result
		}
		switch {
		case leftPrefix.Bits() < rightPrefix.Bits():
			return -1
		case leftPrefix.Bits() > rightPrefix.Bits():
			return 1
		default:
			return 0
		}
	case leftOK:
		return -1
	case rightOK:
		return 1
	default:
		return compareCaseInsensitiveText(left, right)
	}
}

func parseIPv4Prefix(raw string) (netip.Prefix, bool) {
	prefix, err := netip.ParsePrefix(strings.TrimSpace(raw))
	if err != nil || !prefix.Addr().Is4() {
		return netip.Prefix{}, false
	}
	return prefix.Masked(), true
}

func defaultNetworkColumns() []string {
	return append([]string(nil), networkOutputColumns...)
}

func parseNetworkColumns(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultNetworkColumns(), nil
	}
	seen := map[string]bool{}
	var columns []string
	for _, part := range strings.Split(raw, ",") {
		column := strings.ToLower(strings.TrimSpace(part))
		if column == "" {
			return nil, cliError("network column cannot be empty. Supported: %s", strings.Join(networkOutputColumns, ", "))
		}
		if !isNetworkOutputColumn(column) {
			return nil, cliError("unsupported network column %q. Supported: %s", column, strings.Join(networkOutputColumns, ", "))
		}
		if seen[column] {
			return nil, cliError("duplicate network column %q", column)
		}
		seen[column] = true
		columns = append(columns, column)
	}
	return columns, nil
}

func isNetworkOutputColumn(column string) bool {
	column = strings.ToLower(strings.TrimSpace(column))
	for _, candidate := range networkOutputColumns {
		if column == candidate {
			return true
		}
	}
	return false
}

func selectNetworkOutputRows(rows []map[string]any, columns []string) []map[string]any {
	selected := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		selected = append(selected, selectNetworkOutputColumns(row, columns))
	}
	return selected
}

func selectNetworkOutputColumns(row map[string]any, columns []string) map[string]any {
	selected := make(map[string]any, len(columns))
	for _, column := range columns {
		selected[column] = row[column]
	}
	return selected
}

func stringValues(value any) []string {
	switch typed := value.(type) {
	case nil:
		return nil
	case []string:
		values := make([]string, 0, len(typed))
		for _, item := range typed {
			item = strings.TrimSpace(item)
			if item != "" {
				values = append(values, item)
			}
		}
		return values
	case []any:
		values := make([]string, 0, len(typed))
		for _, item := range typed {
			text := cleanString(item)
			if text != "" {
				values = append(values, text)
			}
		}
		return values
	default:
		text := cleanString(value)
		if text == "" {
			return nil
		}
		return []string{text}
	}
}
