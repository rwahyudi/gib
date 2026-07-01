package ibcli

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

const (
	// vlanNetworkReturnFields extends the base network/container return fields
	// with the real WAPI `vlans` array field. Stock NIOS exposes VLAN metadata
	// on network/networkcontainer rows, not as a top-level vlan object.
	vlanNetworkReturnFields = baseNetworkReturnFields + ",vlans"
	vlanCacheKind           = "vlans"
	defaultVLANSortField    = "vlan_id"
	defaultVLANEnv          = "IB_VLAN"
)

var (
	vlanOutputColumns           = []string{"vlan_id", "name", "networks", "comment"}
	vlanSelectableOutputColumns = []string{"vlan_id", "name", "network_view", "networks", "comment"}
	vlanDetailOutputColumns     = []string{"vlan_id", "name", "network_view", "networks", "comment"}
	vlanSortFields              = []string{"vlan_id", "name", "network_view", "networks", "comment"}

	vlanIDColor = lipgloss.Color("#22c55e")
)

type VLANSort struct {
	Enabled bool
	Field   string
	Desc    bool
}

type vlanCacheOptions = netCacheOptions

// flattenVLANFields extracts VLAN id and name from the WAPI `vlans` array on a
// network/networkcontainer row. The array elements are objects with `vlan`
// (integer ID) and `name` (string). Some grids return a bare integer list; both
// shapes are handled. Returns the flattened assigned_vlan id, assigned_vlan_name
// (first entry), and the full list of VLAN entries. The "first entry" is used to
// back the ib net list assigned_vlan/assigned_vlan_name columns for backward
// compatibility; the full list backs ib vlan list.
func flattenVLANFields(raw any) (assignedVLAN string, assignedVLANName string, entries []vlanEntry) {
	switch typed := raw.(type) {
	case []any:
		for _, item := range typed {
			entry, ok := parseVLANEntry(item)
			if !ok {
				continue
			}
			entries = append(entries, entry)
		}
	case []map[string]any:
		for _, item := range typed {
			if entry, ok := parseVLANEntry(item); ok {
				entries = append(entries, entry)
			}
		}
	case map[string]any:
		if entry, ok := parseVLANEntry(typed); ok {
			entries = append(entries, entry)
		}
	}
	if len(entries) > 0 {
		assignedVLAN = entries[0].ID
		assignedVLANName = entries[0].Name
	}
	return assignedVLAN, assignedVLANName, entries
}

type vlanEntry struct {
	ID   string
	Name string
}

func parseVLANEntry(item any) (vlanEntry, bool) {
	switch typed := item.(type) {
	case map[string]any:
		id := cleanString(typed["vlan"])
		if id == "" {
			id = cleanString(typed["id"])
		}
		name := cleanString(typed["name"])
		if id == "" && name == "" {
			return vlanEntry{}, false
		}
		return vlanEntry{ID: normalizeVLANID(id), Name: name}, true
	case string:
		text := strings.TrimSpace(typed)
		if text == "" {
			return vlanEntry{}, false
		}
		return vlanEntry{ID: normalizeVLANID(text)}, true
	case float64:
		return vlanEntry{ID: strconv.Itoa(int(typed))}, true
	case int:
		return vlanEntry{ID: strconv.Itoa(typed)}, true
	default:
		id := cleanString(item)
		if id == "" {
			return vlanEntry{}, false
		}
		return vlanEntry{ID: normalizeVLANID(id)}, true
	}
}

func normalizeVLANID(raw string) string {
	text := strings.TrimSpace(raw)
	if text == "" {
		return ""
	}
	if value, err := strconv.Atoi(text); err == nil {
		return strconv.Itoa(value)
	}
	return text
}

// vlanRowsFromNetworkObjects flattens raw network/container WAPI rows into one
// VLAN row per distinct (vlan_id, network_view). Each VLAN row collects the
// CIDRs that carry it into the `networks` field.
func vlanRowsFromNetworkObjects(networks []map[string]any, containers []map[string]any) []map[string]any {
	type vlanKey struct {
		id          string
		networkView string
	}
	ordered := make([]vlanKey, 0)
	rows := map[vlanKey]*map[string]any{}

	addVLAN := func(id, name, networkView, cidr string) {
		id = normalizeVLANID(id)
		if id == "" {
			return
		}
		key := vlanKey{id: id, networkView: networkView}
		if existing, ok := rows[key]; ok {
			appendVLANNetwork(existing, cidr)
			if cleanString((*existing)["name"]) == "" && name != "" {
				(*existing)["name"] = name
			}
			return
		}
		row := map[string]any{
			"vlan_id":      id,
			"name":         name,
			"network_view": networkView,
			"networks":     []string{cidr},
			"comment":      "",
		}
		rows[key] = &row
		ordered = append(ordered, key)
	}

	flatten := func(item map[string]any) {
		networkView := cleanString(item["network_view"])
		cidr := cleanString(item["network"])
		_, _, entries := flattenVLANFields(item["vlans"])
		for _, entry := range entries {
			addVLAN(entry.ID, entry.Name, networkView, cidr)
		}
	}
	for _, container := range containers {
		flatten(container)
	}
	for _, network := range networks {
		flatten(network)
	}

	result := make([]map[string]any, 0, len(ordered))
	for _, key := range ordered {
		result = append(result, *rows[key])
	}
	return result
}

func appendVLANNetwork(row *map[string]any, cidr string) {
	if cidr == "" {
		return
	}
	current, _ := (*row)["networks"].([]string)
	for _, existing := range current {
		if existing == cidr {
			return
		}
	}
	(*row)["networks"] = append(current, cidr)
}

func vlanOutputRow(vlan map[string]any) map[string]any {
	return map[string]any{
		"vlan_id":      cleanString(vlan["vlan_id"]),
		"name":         cleanString(vlan["name"]),
		"network_view": cleanString(vlan["network_view"]),
		"networks":     joinVLANNetworks(vlan["networks"]),
		"comment":      cleanString(vlan["comment"]),
	}
}

func vlanDetailRow(vlan map[string]any) map[string]any {
	return vlanOutputRow(vlan)
}

func joinVLANNetworks(value any) string {
	switch typed := value.(type) {
	case []string:
		return strings.Join(typed, ", ")
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := cleanString(item); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, ", ")
	default:
		return cleanString(value)
	}
}

func (a *App) runVLANList(search string, networkView string, option VLANSort, columns []string, refresh bool) error {
	return a.runVLANObjectList(search, networkView, option, columns, refresh)
}

func (a *App) runVLANSarch(search string, networkView string, option VLANSort, columns []string, refresh bool) error {
	return a.runVLANObjectList(search, networkView, option, columns, refresh)
}

func (a *App) runVLANObjectList(search string, networkView string, option VLANSort, columns []string, refresh bool) error {
	profile, client, err := a.configuredClient()
	if err != nil {
		return err
	}
	staleCacheServed := false
	cacheOptions := vlanCacheOptions{
		ForceRefresh: refresh,
		ServeExpired: !refresh,
		OnStaleServed: func() {
			staleCacheServed = true
		},
	}
	var vlans []map[string]any
	if err := a.withSpinner("Loading VLANs...", func() error {
		var loadErr error
		vlans, loadErr = a.cachedVLANs(profile, client, networkView, cacheOptions)
		return loadErr
	}); err != nil {
		return err
	}
	vlans = filterVLANs(vlans, search)
	rows := make([]map[string]any, 0, len(vlans))
	for _, vlan := range vlans {
		rows = append(rows, vlanOutputRow(vlan))
	}
	sortVLANRows(rows)
	applyVLANSort(rows, option)
	rows = selectVLANOutputRows(rows, columns)
	if len(rows) == 0 && a.isTableOutput() {
		a.PrintWarning("No VLANs found.")
	}
	if a.isTableOutput() {
		if err := a.emitVLANRows(fmt.Sprintf("VLANs (%d)", len(rows)), columns, rows); err != nil {
			return err
		}
		if staleCacheServed {
			a.PrintInfo("INFO: showing cached VLAN data; refresh queued in background")
		}
		return nil
	}
	return a.emitRows(fmt.Sprintf("VLANs (%d)", len(rows)), columns, rows)
}

func (a *App) runVLANShow(vlan string, networkView string) error {
	profile, client, err := a.configuredClient()
	if err != nil {
		return err
	}
	var vlans []map[string]any
	if err := a.withSpinner("Loading VLAN details...", func() error {
		var loadErr error
		vlans, loadErr = a.cachedVLANs(profile, client, networkView, vlanCacheOptions{})
		return loadErr
	}); err != nil {
		return err
	}
	matched, err := findVLANInRows(vlans, vlan)
	if err != nil {
		return err
	}
	row := vlanDetailRow(matched)
	title := vlanObjectTitle(row)
	if a.isTableOutput() {
		fmt.Fprintln(a.Stdout, renderTable(title, []string{"Field", "Value"}, vlanDetailTableRows(vlanDetailOutputColumns, row)))
		a.printVLANTableFooter(1)
		return nil
	}
	return a.emitObject(title, vlanDetailOutputColumns, row)
}

func (a *App) runVLANUse(vlan string) error {
	profile, client, err := a.configuredClient()
	if err != nil {
		return err
	}
	vlans, err := a.cachedVLANs(profile, client, "", vlanCacheOptions{})
	if err != nil {
		return err
	}
	choices := vlanIdentifierChoices(vlans)
	selected, err := matchChoice(vlan, choices, "VLAN")
	if err != nil {
		return err
	}
	if err := a.writeSessionVLAN(selected, profile.Name); err != nil {
		return err
	}
	if !a.isTableOutput() {
		return a.emitObject("Action", []string{"status", "action", "type", "name", "zone", "view", "message"}, actionRow("use", "VLAN", selected, "", selected, "active VLAN set"))
	}
	a.PrintSuccess("SUCCESS: active VLAN set to " + selected)
	a.PrintNote("This applies to the current shell session and selected profile only.")
	a.PrintNote("For an explicit environment override, run: export " + defaultVLANEnv + "=" + shellQuote(selected))
	return nil
}

func (a *App) runVLANCreate(vlanID string, name string, networkView string, comment string) error {
	return unsupportedVLANWriteError("create")
}

func (a *App) runVLANEdit(vlan string, name string, comment string, networkView string) error {
	return unsupportedVLANWriteError("edit")
}

func (a *App) runVLANDelete(vlan string, networkView string) error {
	return unsupportedVLANWriteError("delete")
}

func unsupportedVLANWriteError(verb string) error {
	return cliError("NIOS WAPI does not support VLAN %s directly; VLANs are managed via network device discovery or the Infoblox UI", verb)
}

func vlanIdentifierChoices(vlans []map[string]any) []string {
	seen := map[string]bool{}
	choices := make([]string, 0, len(vlans)*2)
	for _, vlan := range vlans {
		for _, value := range []string{cleanString(vlan["vlan_id"]), cleanString(vlan["name"])} {
			if value == "" || seen[value] {
				continue
			}
			seen[value] = true
			choices = append(choices, value)
		}
	}
	return choices
}

func findVLANInRows(vlans []map[string]any, target string) (map[string]any, error) {
	target = strings.TrimSpace(target)
	var matches []map[string]any
	for _, vlan := range vlans {
		if cleanString(vlan["vlan_id"]) == target || strings.EqualFold(cleanString(vlan["name"]), target) {
			matches = append(matches, vlan)
		}
	}
	if len(matches) == 0 {
		return nil, cliError("no VLAN found for %q", target)
	}
	if len(matches) > 1 {
		return nil, cliError("multiple VLANs found for %q; use --network-view to choose one", target)
	}
	return matches[0], nil
}

func vlanObjectTitle(row map[string]any) string {
	id := cleanString(row["vlan_id"])
	name := cleanString(row["name"])
	if name != "" {
		return "VLAN " + id + " (" + name + ")"
	}
	return "VLAN " + id
}

func filterVLANs(vlans []map[string]any, search string) []map[string]any {
	search = strings.TrimSpace(search)
	if search == "" {
		return vlans
	}
	filtered := make([]map[string]any, 0, len(vlans))
	for _, vlan := range vlans {
		values := []string{
			cleanString(vlan["vlan_id"]),
			cleanString(vlan["name"]),
			cleanString(vlan["network_view"]),
			joinVLANNetworks(vlan["networks"]),
			cleanString(vlan["comment"]),
		}
		if searchValuesMatch(values, search, false, false) {
			filtered = append(filtered, vlan)
		}
	}
	return filtered
}

func sortVLANRows(rows []map[string]any) {
	sort.SliceStable(rows, func(i, j int) bool {
		result := compareVLANRows(rows[i], rows[j], defaultVLANSortField, false)
		if result == 0 {
			return false
		}
		return result < 0
	})
}

func applyVLANSort(rows []map[string]any, option VLANSort) {
	if !option.Enabled || len(rows) < 2 {
		return
	}
	sort.SliceStable(rows, func(i, j int) bool {
		result := compareVLANRows(rows[i], rows[j], option.Field, option.Desc)
		if result == 0 {
			return false
		}
		return result < 0
	})
}

func compareVLANRows(left map[string]any, right map[string]any, field string, desc bool) int {
	var result int
	switch field {
	case "vlan_id":
		result = compareVLANID(cleanString(left["vlan_id"]), cleanString(right["vlan_id"]))
	case "name":
		result = compareCaseInsensitiveText(cleanString(left["name"]), cleanString(right["name"]))
	case "network_view":
		result = compareCaseInsensitiveText(cleanString(left["network_view"]), cleanString(right["network_view"]))
	case "networks":
		result = compareCaseInsensitiveText(joinVLANNetworks(left["networks"]), joinVLANNetworks(right["networks"]))
	case "comment":
		result = compareCaseInsensitiveText(cleanString(left["comment"]), cleanString(right["comment"]))
	default:
		result = compareCaseInsensitiveText(cleanString(left[field]), cleanString(right[field]))
	}
	return applySortDirection(result, desc)
}

func compareVLANID(left string, right string) int {
	leftInt, leftErr := strconv.Atoi(left)
	rightInt, rightErr := strconv.Atoi(right)
	switch {
	case leftErr == nil && rightErr == nil:
		return leftInt - rightInt
	case leftErr == nil:
		return -1
	case rightErr == nil:
		return 1
	default:
		return compareCaseInsensitiveText(left, right)
	}
}

func parseVLANSort(raw string, enabled bool) (VLANSort, error) {
	if !enabled {
		return VLANSort{}, nil
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = defaultVLANSortField
	}
	desc := strings.HasPrefix(raw, "-")
	if desc {
		raw = strings.TrimPrefix(raw, "-")
	}
	field := strings.ToLower(strings.TrimSpace(raw))
	if !isVLANSortField(field) {
		return VLANSort{}, cliError("unsupported VLAN sort field %q. Supported: %s", field, strings.Join(vlanSortFields, ", "))
	}
	return VLANSort{Enabled: true, Field: field, Desc: desc}, nil
}

func isVLANSortField(field string) bool {
	field = strings.ToLower(strings.TrimSpace(field))
	for _, candidate := range vlanSortFields {
		if field == candidate {
			return true
		}
	}
	return false
}

func defaultVLANColumns() []string {
	return append([]string(nil), vlanOutputColumns...)
}

func parseVLANColumns(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultVLANColumns(), nil
	}
	seen := map[string]bool{}
	var columns []string
	for _, part := range strings.Split(raw, ",") {
		column := strings.ToLower(strings.TrimSpace(part))
		if column == "" {
			return nil, cliError("VLAN column cannot be empty. Supported: %s", strings.Join(vlanSelectableOutputColumns, ", "))
		}
		if !isVLANOutputColumn(column) {
			return nil, cliError("unsupported VLAN column %q. Supported: %s", column, strings.Join(vlanSelectableOutputColumns, ", "))
		}
		if seen[column] {
			return nil, cliError("duplicate VLAN column %q", column)
		}
		seen[column] = true
		columns = append(columns, column)
	}
	return columns, nil
}

func isVLANOutputColumn(column string) bool {
	column = strings.ToLower(strings.TrimSpace(column))
	for _, candidate := range vlanSelectableOutputColumns {
		if column == candidate {
			return true
		}
	}
	return false
}

func selectVLANOutputRows(rows []map[string]any, columns []string) []map[string]any {
	selected := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		selected = append(selected, selectVLANOutputColumns(row, columns))
	}
	return selected
}

func selectVLANOutputColumns(row map[string]any, columns []string) map[string]any {
	selected := make(map[string]any, len(columns))
	for _, column := range columns {
		selected[column] = row[column]
	}
	return selected
}

func (a *App) emitVLANRows(title string, columns []string, rows []map[string]any) error {
	displayRows := make([][]string, 0, len(rows))
	for _, row := range rows {
		display := make([]string, 0, len(columns))
		for _, field := range columns {
			display = append(display, vlanTableValue(field, row))
		}
		displayRows = append(displayRows, display)
	}
	fmt.Fprintln(a.Stdout, renderTable(title, titleCaseFields(columns), displayRows))
	a.printVLANTableFooter(len(rows))
	return nil
}

func (a *App) printVLANTableFooter(count int) {
	fmt.Fprintln(a.Stdout, a.vlanContextLine(count))
}

func (a *App) vlanContextLine(count int) string {
	profile, scope := a.defaultConfigProfileAndScope()
	profileName := profile.Name
	if profileName == "" {
		profileName = defaultProfileName
	}
	return contextTitleStyle.Render("Current Context:") + " " + strings.Join([]string{
		renderContextPair("Profile", profileContextName(profileName, scope), contextProfileValueStyle),
		renderContextPair("Rows", fmt.Sprint(count), contextViewValueStyle),
	}, " | ")
}

func vlanTableValue(field string, row map[string]any) string {
	switch field {
	case "vlan_id":
		return styledVLANID(stringify(row[field]))
	default:
		return stringify(row[field])
	}
}

func vlanDetailTableRows(fields []string, row map[string]any) [][]string {
	labels := titleCaseFields(fields)
	rows := make([][]string, 0, len(fields))
	for i, field := range fields {
		rows = append(rows, []string{labels[i], vlanTableValue(field, row)})
	}
	return rows
}

func styledVLANID(id string) string {
	label := strings.TrimSpace(id)
	if label == "" {
		return ""
	}
	return lipgloss.NewStyle().Bold(true).Foreground(vlanIDColor).Render(label)
}

func queryVLANs(client *WapiClient, networkView string) ([]map[string]any, error) {
	networks, err := queryVLANNetworkObjects(client, networkObject, networkView)
	if err != nil {
		return nil, err
	}
	containers, err := queryVLANNetworkObjects(client, networkContainerObject, networkView)
	if err != nil {
		return nil, err
	}
	return vlanRowsFromNetworkObjects(networks, containers), nil
}

func queryVLANNetworkObjects(client *WapiClient, objectType string, networkView string) ([]map[string]any, error) {
	params := url.Values{}
	params.Set("_return_fields", vlanNetworkReturnFields)
	if strings.TrimSpace(networkView) != "" {
		params.Set("network_view", strings.TrimSpace(networkView))
	}
	return queryNetworkObjectRows(client, objectType, params)
}

func (a *App) cachedVLANs(profile Profile, client *WapiClient, networkView string, options vlanCacheOptions) ([]map[string]any, error) {
	networkView = strings.TrimSpace(networkView)
	return a.cachedNetRows(
		profile,
		vlanCacheKind,
		networkView,
		"",
		func() (cachedPayload, error) { return a.readCachedVLANs(profile, networkView) },
		func() ([]map[string]any, error) { return a.refreshVLANCache(profile, client, networkView) },
		options,
	)
}

func (a *App) refreshVLANCache(profile Profile, client *WapiClient, networkView string) ([]map[string]any, error) {
	rows, err := queryVLANs(client, networkView)
	if err != nil {
		return nil, err
	}
	_ = a.writeCachedVLANs(profile, networkView, rows, time.Now())
	return rows, nil
}

func (a *App) startVLANCacheRefreshAsync(profile Profile, networkView string) {
	_ = a.startNetCacheRefresh(profile, vlanCacheKind, networkView, "")
}
