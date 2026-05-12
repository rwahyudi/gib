package ibcli

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func (a *App) zoneArgCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return a.completeZoneNames(cmd, toComplete), cobra.ShellCompDirectiveNoFileComp
}

func (a *App) optionalZoneArgCompletion(zoneArgIndex int) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) != zoneArgIndex {
			if len(args) > zoneArgIndex {
				return flagCompletions(cmd, toComplete), cobra.ShellCompDirectiveNoFileComp
			}
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return a.completeZoneNames(cmd, toComplete), cobra.ShellCompDirectiveNoFileComp
	}
}

func (a *App) zoneFlagCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return a.completeZoneNames(cmd, toComplete), cobra.ShellCompDirectiveNoFileComp
}

func shellNameCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 || strings.HasPrefix(strings.TrimSpace(toComplete), "-") {
		return flagCompletions(cmd, toComplete), cobra.ShellCompDirectiveNoFileComp
	}
	candidates := []string{
		"bash\tBash completion script",
		"zsh\tZsh completion script",
		"fish\tFish completion script",
	}
	prefix := strings.ToLower(strings.TrimSpace(toComplete))
	if prefix == "" {
		return candidates, cobra.ShellCompDirectiveNoFileComp
	}
	matches := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		value := strings.SplitN(candidate, "\t", 2)[0]
		if strings.HasPrefix(value, prefix) {
			matches = append(matches, candidate)
		}
	}
	return matches, cobra.ShellCompDirectiveNoFileComp
}

func (a *App) profileArgCompletion(includeDefault bool) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 || strings.HasPrefix(strings.TrimSpace(toComplete), "-") {
			return flagCompletions(cmd, toComplete), cobra.ShellCompDirectiveNoFileComp
		}
		return a.completeProfileNames(toComplete, includeDefault), cobra.ShellCompDirectiveNoFileComp
	}
}

func completeFlagsAfterArgs(minArgs int) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) < minArgs {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return flagCompletions(cmd, toComplete), cobra.ShellCompDirectiveNoFileComp
	}
}

func zoneListArgCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 || strings.HasPrefix(strings.TrimSpace(toComplete), "-") {
		return flagCompletions(cmd, toComplete), cobra.ShellCompDirectiveNoFileComp
	}
	return nil, cobra.ShellCompDirectiveNoFileComp
}

func createArgCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if strings.HasPrefix(strings.TrimSpace(toComplete), "-") {
		return flagCompletions(cmd, toComplete), cobra.ShellCompDirectiveNoFileComp
	}
	switch {
	case len(args) == 1:
		return recordTypeCompletions(toComplete), cobra.ShellCompDirectiveNoFileComp
	case len(args) >= 3:
		return flagCompletions(cmd, toComplete), cobra.ShellCompDirectiveNoFileComp
	default:
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
}

func (a *App) existingRecordArgCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if strings.HasPrefix(strings.TrimSpace(toComplete), "-") {
		return flagCompletions(cmd, toComplete), cobra.ShellCompDirectiveNoFileComp
	}
	switch len(args) {
	case 0:
		return a.completeRecordNames(cmd, "", toComplete), cobra.ShellCompDirectiveNoFileComp
	case 1:
		return a.completeZoneNames(cmd, toComplete), cobra.ShellCompDirectiveNoFileComp
	default:
		return flagCompletions(cmd, toComplete), cobra.ShellCompDirectiveNoFileComp
	}
}

func (a *App) editArgCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if strings.HasPrefix(strings.TrimSpace(toComplete), "-") {
		return flagCompletions(cmd, toComplete), cobra.ShellCompDirectiveNoFileComp
	}
	switch len(args) {
	case 0:
		return a.completeRecordNames(cmd, commandZoneFlag(cmd), toComplete), cobra.ShellCompDirectiveNoFileComp
	case 1:
		return recordTypeCompletions(toComplete), cobra.ShellCompDirectiveNoFileComp
	case 2:
		return nil, cobra.ShellCompDirectiveNoFileComp
	default:
		return flagCompletions(cmd, toComplete), cobra.ShellCompDirectiveNoFileComp
	}
}

var recordTypeDescriptions = map[string]string{
	"a":     "IPv4 address record",
	"aaaa":  "IPv6 address record",
	"cname": "canonical name alias",
	"host":  "host record",
	"mx":    "mail exchanger record",
	"ptr":   "reverse pointer record",
	"srv":   "service locator record",
	"txt":   "text record",
}

func recordTypeFlagCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return recordTypeFilterCompletions(toComplete), cobra.ShellCompDirectiveNoFileComp
}

func recordTypeCompletions(toComplete string) []string {
	prefix := strings.ToLower(strings.TrimSpace(toComplete))
	var rows []string
	for _, recordType := range supportedRecordTypes() {
		if prefix != "" && !strings.HasPrefix(recordType, prefix) {
			continue
		}
		description := recordTypeDescriptions[recordType]
		if description == "" {
			rows = append(rows, recordType)
			continue
		}
		rows = append(rows, recordType+"\t"+description)
	}
	return rows
}

func recordTypeFilterCompletions(toComplete string) []string {
	raw := strings.ToLower(strings.TrimSpace(toComplete))
	parts := strings.Split(raw, ",")
	prefix := parts[len(parts)-1]
	selected := map[string]bool{}
	for _, part := range parts[:len(parts)-1] {
		part = strings.TrimSpace(part)
		if part != "" {
			selected[part] = true
		}
	}
	base := ""
	if len(parts) > 1 {
		base = strings.Join(parts[:len(parts)-1], ",") + ","
	}

	var rows []string
	for _, recordType := range supportedRecordTypes() {
		if selected[recordType] {
			continue
		}
		if prefix != "" && !strings.HasPrefix(recordType, prefix) {
			continue
		}
		candidate := base + recordType
		description := recordTypeDescriptions[recordType]
		if description == "" {
			rows = append(rows, candidate)
			continue
		}
		rows = append(rows, candidate+"\t"+description)
	}
	return rows
}

var recordSortDescriptions = map[string]string{
	"name":    "record name",
	"type":    "record type",
	"value":   "record value",
	"zone":    "DNS zone",
	"ttl":     "record TTL",
	"comment": "record comment",
}

func recordSortFlagCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return recordSortCompletions(toComplete), cobra.ShellCompDirectiveNoFileComp
}

func (a *App) completeRecordSortValue(args []string) bool {
	prefix, ok := recordSortCompletionPrefix(args)
	if !ok {
		return false
	}
	rows := recordSortCompletions(prefix)
	noDescriptions := args[0] == "__completeNoDesc"
	for _, row := range rows {
		if noDescriptions {
			row = strings.SplitN(row, "\t", 2)[0]
		}
		fmt.Fprintln(a.Stdout, row)
	}
	fmt.Fprintln(a.Stdout, ":4")
	return true
}

func recordSortCompletionPrefix(args []string) (string, bool) {
	if len(args) < 4 || !strings.HasPrefix(args[0], "__complete") {
		return "", false
	}
	if args[1] != "dns" || (args[2] != "list" && args[2] != "search") {
		return "", false
	}
	current := args[len(args)-1]
	if current == "--sort" || current == "-s" {
		return "", true
	}
	if value, ok := strings.CutPrefix(current, "--sort="); ok {
		return value, true
	}
	if value, ok := strings.CutPrefix(current, "-s="); ok {
		return value, true
	}
	previous := args[len(args)-2]
	if previous == "--sort" || previous == "-s" {
		return current, true
	}
	return "", false
}

func recordSortCompletions(toComplete string) []string {
	prefix := strings.ToLower(strings.TrimSpace(toComplete))
	var rows []string
	for _, field := range recordSortFields {
		appendRecordSortCompletion(&rows, field, "ascending", prefix)
		appendRecordSortCompletion(&rows, "-"+field, "descending", prefix)
	}
	return rows
}

func appendRecordSortCompletion(rows *[]string, value string, direction string, prefix string) {
	if prefix != "" && !strings.HasPrefix(value, prefix) {
		return
	}
	field := strings.TrimPrefix(value, "-")
	description := recordSortDescriptions[field]
	if description == "" {
		*rows = append(*rows, value)
		return
	}
	*rows = append(*rows, value+"\t"+description+" "+direction)
}

var recordColumnDescriptions = map[string]string{
	"type":    "record type",
	"name":    "record name",
	"value":   "record value",
	"zone":    "DNS zone",
	"ttl":     "record TTL",
	"comment": "record comment",
}

func recordColumnFlagCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return recordColumnCompletions(toComplete), cobra.ShellCompDirectiveNoFileComp
}

func recordColumnCompletions(toComplete string) []string {
	raw := strings.ToLower(strings.TrimSpace(toComplete))
	parts := strings.Split(raw, ",")
	prefix := parts[len(parts)-1]
	selected := map[string]bool{}
	for _, part := range parts[:len(parts)-1] {
		part = strings.TrimSpace(part)
		if part != "" {
			selected[part] = true
		}
	}
	base := ""
	if len(parts) > 1 {
		base = strings.Join(parts[:len(parts)-1], ",") + ","
	}

	var rows []string
	for _, column := range recordOutputColumns {
		if selected[column] {
			continue
		}
		if prefix != "" && !strings.HasPrefix(column, prefix) {
			continue
		}
		candidate := base + column
		description := recordColumnDescriptions[column]
		if description == "" {
			rows = append(rows, candidate)
			continue
		}
		rows = append(rows, candidate+"\t"+description)
	}
	return rows
}

var zoneFormatDescriptions = map[string]string{
	"FORWARD": "forward DNS zone",
	"IPV4":    "IPv4 reverse DNS zone",
	"IPV6":    "IPv6 reverse DNS zone",
}

func zoneFormatFlagCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return zoneFormatCompletions(toComplete), cobra.ShellCompDirectiveNoFileComp
}

func zoneFormatCompletions(toComplete string) []string {
	raw := strings.ToUpper(strings.TrimSpace(toComplete))
	parts := strings.Split(raw, ",")
	prefix := parts[len(parts)-1]
	selected := map[string]bool{}
	for _, part := range parts[:len(parts)-1] {
		part = strings.TrimSpace(part)
		if part != "" {
			selected[part] = true
		}
	}
	base := ""
	if len(parts) > 1 {
		base = strings.Join(parts[:len(parts)-1], ",") + ","
	}

	var rows []string
	for _, format := range zoneFormatTypes {
		if selected[format] {
			continue
		}
		if prefix != "" && !strings.HasPrefix(format, prefix) {
			continue
		}
		candidate := base + format
		description := zoneFormatDescriptions[format]
		if description == "" {
			rows = append(rows, candidate)
			continue
		}
		rows = append(rows, candidate+"\t"+description)
	}
	return rows
}

var zoneSortDescriptions = map[string]string{
	"zone":     "zone name",
	"view":     "DNS view",
	"format":   "zone format",
	"ns_group": "name server group",
	"comment":  "zone comment",
}

func zoneSortFlagCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return zoneSortCompletions(toComplete), cobra.ShellCompDirectiveNoFileComp
}

func (a *App) completeZoneSortValue(args []string) bool {
	prefix, ok := zoneSortCompletionPrefix(args)
	if !ok {
		return false
	}
	rows := zoneSortCompletions(prefix)
	noDescriptions := args[0] == "__completeNoDesc"
	for _, row := range rows {
		if noDescriptions {
			row = strings.SplitN(row, "\t", 2)[0]
		}
		fmt.Fprintln(a.Stdout, row)
	}
	fmt.Fprintln(a.Stdout, ":4")
	return true
}

func zoneSortCompletionPrefix(args []string) (string, bool) {
	if len(args) < 5 || !strings.HasPrefix(args[0], "__complete") || !isZoneListArgs(args) {
		return "", false
	}
	current := args[len(args)-1]
	if current == "--sort" || current == "-s" {
		return "", true
	}
	if value, ok := strings.CutPrefix(current, "--sort="); ok {
		return value, true
	}
	if value, ok := strings.CutPrefix(current, "-s="); ok {
		return value, true
	}
	previous := args[len(args)-2]
	if previous == "--sort" || previous == "-s" {
		return current, true
	}
	return "", false
}

func (a *App) completeZoneListFlagNames(args []string) bool {
	if len(args) < 5 || !strings.HasPrefix(args[0], "__complete") || !completionArgsContainDNSZoneList(args) {
		return false
	}
	if zoneListZoneOverrideValueCompletion(args) {
		fmt.Fprintln(a.Stdout, ":4")
		return true
	}
	current := args[len(args)-1]
	if !strings.HasPrefix(strings.TrimSpace(current), "-") {
		return false
	}
	root := a.RootCommand()
	cmd, _, err := root.Find([]string{"dns", "zone", "list"})
	if err != nil || cmd == nil {
		return false
	}
	noDescriptions := args[0] == "__completeNoDesc"
	for _, row := range flagCompletions(cmd, current) {
		if noDescriptions {
			row = strings.SplitN(row, "\t", 2)[0]
		}
		fmt.Fprintln(a.Stdout, row)
	}
	fmt.Fprintln(a.Stdout, ":4")
	return true
}

func completionArgsContainDNSZoneList(args []string) bool {
	sawDNS := false
	for index, arg := range args {
		if arg == "dns" {
			sawDNS = true
			continue
		}
		if sawDNS && arg == "zone" && index+1 < len(args) && args[index+1] == "list" {
			return true
		}
	}
	return false
}

func zoneListZoneOverrideValueCompletion(args []string) bool {
	current := args[len(args)-1]
	if strings.HasPrefix(current, "--zone=") || strings.HasPrefix(current, "-z=") {
		return true
	}
	previous := args[len(args)-2]
	return previous == "--zone" || previous == "-z"
}

func zoneSortCompletions(toComplete string) []string {
	prefix := strings.ToLower(strings.TrimSpace(toComplete))
	var rows []string
	for _, field := range zoneSortFields {
		appendZoneSortCompletion(&rows, field, "ascending", prefix)
		appendZoneSortCompletion(&rows, "-"+field, "descending", prefix)
	}
	return rows
}

func appendZoneSortCompletion(rows *[]string, value string, direction string, prefix string) {
	if prefix != "" && !strings.HasPrefix(value, prefix) {
		return
	}
	field := strings.TrimPrefix(value, "-")
	description := zoneSortDescriptions[field]
	if description == "" {
		*rows = append(*rows, value)
		return
	}
	*rows = append(*rows, value+"\t"+description+" "+direction)
}

var zoneColumnDescriptions = map[string]string{
	"zone":     "zone name",
	"view":     "DNS view",
	"format":   "zone format",
	"ns_group": "name server group",
	"comment":  "zone comment",
}

func zoneColumnFlagCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return zoneColumnCompletions(toComplete), cobra.ShellCompDirectiveNoFileComp
}

func zoneColumnCompletions(toComplete string) []string {
	raw := strings.ToLower(strings.TrimSpace(toComplete))
	parts := strings.Split(raw, ",")
	prefix := parts[len(parts)-1]
	selected := map[string]bool{}
	for _, part := range parts[:len(parts)-1] {
		part = strings.TrimSpace(part)
		if part != "" {
			selected[part] = true
		}
	}
	base := ""
	if len(parts) > 1 {
		base = strings.Join(parts[:len(parts)-1], ",") + ","
	}

	var rows []string
	for _, column := range zoneOutputColumns {
		if selected[column] {
			continue
		}
		if prefix != "" && !strings.HasPrefix(column, prefix) {
			continue
		}
		candidate := base + column
		description := zoneColumnDescriptions[column]
		if description == "" {
			rows = append(rows, candidate)
			continue
		}
		rows = append(rows, candidate+"\t"+description)
	}
	return rows
}

func commandZoneFlag(cmd *cobra.Command) string {
	if flag := cmd.Flags().Lookup("zone"); flag != nil {
		return strings.TrimSpace(flag.Value.String())
	}
	return ""
}

func (a *App) completeRecordNames(cmd *cobra.Command, explicitZone, toComplete string) []string {
	profile, err := a.loadConfig(true)
	if err != nil {
		return nil
	}
	profile.DNSView = a.resolveDNSView(profile)
	if flag := cmd.Flags().Lookup("view"); flag != nil {
		if view := strings.TrimSpace(flag.Value.String()); view != "" {
			profile.DNSView = view
		}
	}
	zone, err := a.resolveDNSZone(profile, explicitZone)
	if err != nil {
		return nil
	}

	records, err := a.cachedRecordNamesForCompletion(profile, zone)
	if err != nil {
		return nil
	}
	return matchingRecordNames(records, toComplete)
}

func (a *App) cachedRecordNamesForCompletion(profile Profile, zone string) ([]TypedRecord, error) {
	entry, err := a.readCachedRecords(profile, zone)
	if err == nil && entry.CacheFound && a.cacheEntryFresh(entry, time.Now()) {
		return recordsFromAllRecordRows(entry.Rows), nil
	}

	// Completion is dynamic by design: when the local cache cannot satisfy a
	// record-name request, fall back to the same cache/SWR loader used by dns
	// list/search. Keep this in sync with README and NOTES if completion policy changes.
	records, err := a.cachedRecordsForZone(profile, a.newClient(profile), zone)
	if err != nil {
		return nil, err
	}
	return records, nil
}

func matchingRecordNames(records []TypedRecord, toComplete string) []string {
	prefix := strings.ToLower(strings.TrimSpace(toComplete))
	seen := map[string]bool{}
	rows := make([]string, 0, len(records))
	for _, record := range records {
		name := cleanString(recordName(record.Item, record.Type))
		if name == "" || seen[name] {
			continue
		}
		if prefix != "" && !strings.HasPrefix(strings.ToLower(name), prefix) {
			continue
		}
		seen[name] = true
		description := strings.ToUpper(record.Type)
		if value := cleanString(recordValue(record.Type, record.Item)); value != "" {
			description += " " + value
		}
		rows = append(rows, name+"\t"+description)
	}
	sort.Slice(rows, func(i, j int) bool {
		return strings.ToLower(recordCompletionName(rows[i])) < strings.ToLower(recordCompletionName(rows[j]))
	})
	return rows
}

func recordCompletionName(row string) string {
	return strings.SplitN(row, "\t", 2)[0]
}

func flagCompletions(cmd *cobra.Command, toComplete string) []string {
	prefix := strings.TrimSpace(toComplete)
	if prefix != "" && !strings.HasPrefix(prefix, "-") {
		return nil
	}
	seen := map[string]bool{}
	var rows []string
	addFlagCompletions(&rows, seen, cmd.InheritedFlags(), prefix, cmd)
	addFlagCompletions(&rows, seen, cmd.Flags(), prefix, cmd)
	sort.Slice(rows, func(i, j int) bool {
		return flagCompletionName(rows[i]) < flagCompletionName(rows[j])
	})
	return rows
}

func addFlagCompletions(rows *[]string, seen map[string]bool, flags *pflag.FlagSet, prefix string, cmd *cobra.Command) {
	if flags == nil {
		return
	}
	flags.VisitAll(func(flag *pflag.Flag) {
		if flag.Hidden || suppressFlagForCommand(cmd, flag.Name) {
			return
		}
		long := "--" + flag.Name
		if strings.HasPrefix(long, prefix) && !seen[long] {
			*rows = append(*rows, long+"\t"+flag.Usage)
			seen[long] = true
		}
		if flag.Shorthand == "" {
			return
		}
		short := "-" + flag.Shorthand
		if strings.HasPrefix(short, prefix) && !seen[short] {
			*rows = append(*rows, short+"\t"+flag.Usage)
			seen[short] = true
		}
	})
}

func flagCompletionName(row string) string {
	return strings.SplitN(row, "\t", 2)[0]
}

func (a *App) completeZoneNames(cmd *cobra.Command, toComplete string) []string {
	profile, err := a.loadConfig(true)
	if err != nil {
		return nil
	}
	if flag := cmd.Flags().Lookup("view"); flag != nil {
		if view := strings.TrimSpace(flag.Value.String()); view != "" {
			profile.DNSView = view
		}
	}
	zones, err := a.cachedZoneNames(profile)
	if err != nil {
		return nil
	}
	return matchingZoneNames(zones, toComplete)
}

func (a *App) cachedZoneNames(profile Profile) ([]string, error) {
	// Zone completion shares the normal zone-list cache path so config changes,
	// manual cache clears, and background refreshes behave the same as commands.
	zones, err := a.cachedZones(profile, a.newClient(profile), "")
	if err != nil {
		return nil, err
	}
	return zoneNamesFromRows(zones), nil
}

func (a *App) completeProfileNames(toComplete string, includeDefault bool) []string {
	defaultProfile, profiles, _, err := a.readConfigProfiles(false)
	if err != nil {
		return nil
	}
	prefix := strings.ToLower(strings.TrimSpace(toComplete))
	names := make([]string, 0, len(profiles))
	for name := range profiles {
		if !includeDefault && name == defaultProfile {
			continue
		}
		if prefix != "" && !strings.HasPrefix(strings.ToLower(name), prefix) {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func zoneNamesFromRows(zones []map[string]any) []string {
	seen := map[string]bool{}
	names := make([]string, 0, len(zones))
	for _, zone := range zones {
		name := cleanString(zone["fqdn"])
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		return strings.ToLower(names[i]) < strings.ToLower(names[j])
	})
	return names
}

func matchingZoneNames(zones []string, toComplete string) []string {
	prefix := strings.ToLower(strings.TrimSpace(toComplete))
	matches := make([]string, 0, len(zones))
	for _, zone := range zones {
		if prefix == "" || strings.HasPrefix(strings.ToLower(zone), prefix) {
			matches = append(matches, zone)
		}
	}
	return matches
}

func dynamicBashCompletionScript() string {
	// The Bash wrapper delegates completion to the current ib binary rather than
	// embedding a static candidate list. Update this script and completion tests
	// together whenever prompt-redraw or second-tab behavior changes.
	return `# bash completion for ib

__ib_create_usage_on_second_tab()
{
    local cmd="$1"
    local key
    key="${COMP_LINE:-${COMP_WORDS[*]}}:${COMP_POINT:-${COMP_CWORD:-0}}"

    if [[ "${__ib_create_usage_key:-}" == "${key}" ]]; then
        __ib_create_usage_count=$(( ${__ib_create_usage_count:-1} + 1 ))
    else
        __ib_create_usage_key="${key}"
        __ib_create_usage_count=1
    fi

    if [[ "${__ib_create_usage_count}" -lt 2 ]]; then
        return 0
    fi

    __ib_create_usage_count=0
    printf '\n' >&2
    IB_ACTIVE_HELP=0 "${cmd}" dns create --help >&2
    printf '\n' >&2
    printf 'ib dns create ' >&2
}

__ib_dynamic_completion()
{
    local cur cmd out line directive comp value flag_prefix
    COMPREPLY=()

    cur="${COMP_WORDS[COMP_CWORD]}"
    cmd="${COMP_WORDS[0]}"
    local args=("${COMP_WORDS[@]:1:COMP_CWORD}")
    if [[ -z "${COMP_WORDS[COMP_CWORD]+set}" ]]; then
        args+=("${cur}")
    fi

    if [[ "${COMP_WORDS[1]}" == "dns" && "${COMP_WORDS[2]}" == "create" && "${COMP_CWORD}" -eq 3 && -z "${cur}" ]]; then
        __ib_create_usage_on_second_tab "${cmd}"
        compopt +o default 2>/dev/null || true
        return 0
    fi

    out=$(IB_ACTIVE_HELP=0 "${cmd}" __completeNoDesc "${args[@]}" 2>/dev/null) || return 0

    local lines=()
    while IFS='' read -r line; do
        [[ -n "${line}" ]] && lines+=("${line}")
    done <<< "${out}"

    local count=${#lines[@]}
    [[ ${count} -eq 0 ]] && return 0

    directive=0
    local last_index=$((count - 1))
    if [[ "${lines[${last_index}]}" == :* ]]; then
        directive="${lines[${last_index}]#:}"
        unset 'lines[${last_index}]'
    fi

    local shellCompDirectiveError=1
    local shellCompDirectiveNoSpace=2
    local shellCompDirectiveNoFileComp=4

    if [[ $((directive & shellCompDirectiveError)) -ne 0 ]]; then
        return 0
    fi
	if [[ $((directive & shellCompDirectiveNoSpace)) -ne 0 ]] && type compopt >/dev/null 2>&1; then
        compopt -o nospace 2>/dev/null || true
    fi
    if [[ $((directive & shellCompDirectiveNoFileComp)) -ne 0 ]] && type compopt >/dev/null 2>&1; then
        compopt +o default 2>/dev/null || true
    fi

    value="${cur}"
    flag_prefix=""
    if [[ "${cur}" == *=* ]]; then
        flag_prefix="${cur%%=*}="
        value="${cur#*=}"
    fi

    for comp in "${lines[@]}"; do
        [[ -z "${comp}" ]] && continue
        if [[ "${comp}" == "${value}"* ]]; then
            COMPREPLY+=("${flag_prefix}${comp}")
        fi
    done
}

complete -o default -F __ib_dynamic_completion ib
`
}

func dynamicZshCompletionScript() string {
	return `#compdef ib
compdef _ib ib

_ib()
{
    local requestComp line directive
    local -a out completions

    words=("${=words[1,CURRENT]}")
    requestComp="${words[1]} __complete ${words[2,-1]}"
    if [[ "${words[-1]}" == "" ]]; then
        requestComp="${requestComp} \"\""
    fi

    out=("${(@f)$(eval ${requestComp} 2>/dev/null)}")
    directive=0
    for line in "${out[@]}"; do
        if [[ "${line}" == :* ]]; then
            directive="${line#:}"
            continue
        fi
        [[ -z "${line}" ]] && continue
        line="${line//:/\\:}"
        line="${line//$'\t'/:}"
        completions+=("${line}")
    done

    if [[ $((directive & 1)) -ne 0 ]]; then
        return 0
    fi
    if [[ ${#completions[@]} -gt 0 ]]; then
        _describe -t completions 'ib completion' completions
    fi
    return 0
}
`
}

func dynamicFishCompletionScript() string {
	return `# fish completion for ib

function __ib_dynamic_completion
    set -l args (commandline -opc)
    set -l last_arg (commandline -ct)
    set -l request "IB_ACTIVE_HELP=0 $args[1] __complete $args[2..-1] $last_arg"
    set -l output (eval $request 2>/dev/null)

    if test (count $output) -eq 0
        return 0
    end

    set -l last_line $output[-1]
    if string match -q ':*' -- $last_line
        set output $output[1..-2]
    end

    for line in $output
        printf "%s\n" $line
    end
end

complete -c ib -e
complete -c ib -f -a "(__ib_dynamic_completion)"
`
}
