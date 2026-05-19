package ibcli

import (
	"fmt"
	"net/netip"
	"net/url"
	"sort"
	"strings"
)

const (
	networkViewReturnFields = "name,comment"
	ipv4AddressReturnFields = "ip_address,network,network_view,status,types,names,mac_address,lease_state,usage,comment"
	defaultNetSortField     = "network"
)

var (
	networkViewOutputColumns   = []string{"name", "comment"}
	networkOutputColumns       = []string{"network", "network_view", "comment"}
	networkDetailOutputColumns = []string{"network", "network_view", "comment"}
	ipv4AddressOutputColumns   = []string{"ip", "network", "network_view", "status", "types", "names", "mac_address", "lease_state", "comment"}
	netSortFields              = []string{"network", "network_view", "comment"}
)

type NetSort struct {
	Enabled bool
	Field   string
	Desc    bool
}

func (a *App) runNetViewList() error {
	_, client, err := a.configuredClient()
	if err != nil {
		return err
	}
	views, err := queryNetworkViews(client)
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
	_, client, err := a.configuredClient()
	if err != nil {
		return err
	}
	networks, err := queryNetworks(client, networkView)
	if err != nil {
		return err
	}
	networks = filterNetworks(networks, search)
	rows := make([]map[string]any, 0, len(networks))
	for _, network := range networks {
		rows = append(rows, networkOutputRow(network))
	}
	sortNetworkRows(rows)
	applyNetSort(rows, option)
	rows = selectNetworkOutputRows(rows, columns)
	if len(rows) == 0 && a.isTableOutput() {
		a.PrintWarning("No IPAM networks found.")
	}
	return a.emitRows(fmt.Sprintf("IPAM Networks (%d)", len(rows)), columns, rows)
}

func (a *App) runNetShow(network string, networkView string) error {
	_, client, err := a.configuredClient()
	if err != nil {
		return err
	}
	matchedNetwork, err := findNetwork(client, network, networkView)
	if err != nil {
		return err
	}
	row := networkDetailRow(matchedNetwork)
	title := "IPAM Network: " + cleanString(row["network"])
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
	_, client, err := a.configuredClient()
	if err != nil {
		return err
	}
	params := url.Values{"_return_fields": []string{ipv4AddressReturnFields}, "ip_address": []string{ip}}
	if strings.TrimSpace(networkView) != "" {
		params.Set("network_view", strings.TrimSpace(networkView))
	}
	results, err := pagedQuery(client, ipv4AddressObject, params)
	if err != nil {
		return err
	}
	if len(results) == 0 {
		return cliError("no IPv4 address found for %s", ip)
	}
	rows := make([]map[string]any, 0, len(results))
	for _, result := range results {
		rows = append(rows, ipv4AddressOutputRow(result))
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
	return a.runNextIP(network, networkView, num, exclude, true)
}

func (a *App) runNetNextIP(network string, networkView string, num int, exclude []string) error {
	return a.runNextIP(network, networkView, num, exclude, false)
}

func (a *App) runNextIP(network string, networkView string, num int, exclude []string, printContext bool) error {
	_, client, err := a.configuredClient()
	if err != nil {
		return err
	}
	rows, err := nextAvailableIPRows(client, network, networkView, num, exclude)
	if err != nil {
		return err
	}
	if printContext && a.isTableOutput() {
		a.PrintContext()
	}
	return a.emitRows("Next Available IPs", nextIPOutputColumns, rows)
}

func queryNetworkViews(client *WapiClient) ([]map[string]any, error) {
	params := url.Values{"_return_fields": []string{networkViewReturnFields}}
	return pagedQuery(client, networkViewObject, params)
}

func filterNetworks(networks []map[string]any, search string) []map[string]any {
	search = strings.TrimSpace(search)
	if search == "" {
		return networks
	}
	filtered := make([]map[string]any, 0, len(networks))
	for _, network := range networks {
		values := []string{
			cleanString(network["network"]),
			cleanString(network["network_view"]),
			cleanString(network["comment"]),
		}
		if searchValuesMatch(values, search, false, false) {
			filtered = append(filtered, network)
		}
	}
	return filtered
}

func networkOutputRow(network map[string]any) map[string]any {
	return map[string]any{
		"network":      cleanString(network["network"]),
		"network_view": cleanString(network["network_view"]),
		"comment":      cleanString(network["comment"]),
	}
}

func networkDetailRow(network map[string]any) map[string]any {
	return map[string]any{
		"network":      cleanString(network["network"]),
		"network_view": cleanString(network["network_view"]),
		"comment":      cleanString(network["comment"]),
	}
}

func ipv4AddressOutputRow(item map[string]any) map[string]any {
	return map[string]any{
		"ip":           cleanString(firstNonEmpty(cleanString(item["ip_address"]), cleanString(item["ipv4addr"]))),
		"network":      cleanString(item["network"]),
		"network_view": cleanString(item["network_view"]),
		"status":       cleanString(item["status"]),
		"types":        strings.Join(stringValues(item["types"]), ", "),
		"names":        strings.Join(stringValues(item["names"]), ", "),
		"mac_address":  cleanString(item["mac_address"]),
		"lease_state":  cleanString(item["lease_state"]),
		"comment":      cleanString(item["comment"]),
	}
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
