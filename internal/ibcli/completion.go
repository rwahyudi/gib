package ibcli

import (
	"fmt"
	"net/netip"
	"os"
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
		args = completionArgsBeforeCurrent(args, toComplete)
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

func (a *App) dnsListArgCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	args = completionArgsBeforeCurrent(args, toComplete)
	trimmed := strings.TrimSpace(toComplete)
	if len(args) > 0 || strings.HasPrefix(trimmed, "-") {
		return flagCompletions(cmd, toComplete), cobra.ShellCompDirectiveNoFileComp
	}
	zones := a.completeZoneNames(cmd, toComplete)
	if trimmed != "" {
		return zones, cobra.ShellCompDirectiveNoFileComp
	}
	rows := flagCompletions(cmd, toComplete)
	rows = append(rows, zones...)
	return rows, cobra.ShellCompDirectiveNoFileComp
}

func shellNameCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 || strings.HasPrefix(strings.TrimSpace(toComplete), "-") {
		return flagCompletions(cmd, toComplete), cobra.ShellCompDirectiveNoFileComp
	}
	candidates := shellCompletionCandidates()
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
	args = completionArgsBeforeCurrent(args, toComplete)
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
	args = completionArgsBeforeCurrent(args, toComplete)
	if strings.HasPrefix(strings.TrimSpace(toComplete), "-") {
		return flagCompletions(cmd, toComplete), cobra.ShellCompDirectiveNoFileComp
	}
	switch len(args) {
	case 0:
		return a.completeRecordNames(cmd, commandZoneFlag(cmd), toComplete), cobra.ShellCompDirectiveNoFileComp
	case 1:
		return a.completeZoneNames(cmd, toComplete), cobra.ShellCompDirectiveNoFileComp
	default:
		return flagCompletions(cmd, toComplete), cobra.ShellCompDirectiveNoFileComp
	}
}

func (a *App) editArgCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	args = completionArgsBeforeCurrent(args, toComplete)
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

func (a *App) networkArgCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	args = completionArgsBeforeCurrent(args, toComplete)
	if len(args) > 0 || strings.HasPrefix(strings.TrimSpace(toComplete), "-") {
		return flagCompletions(cmd, toComplete), cobra.ShellCompDirectiveNoFileComp
	}
	return a.completeNetworkCIDRs(cmd, toComplete), cobra.ShellCompDirectiveNoFileComp
}

func completionArgsBeforeCurrent(args []string, toComplete string) []string {
	if len(args) == 0 {
		return args
	}
	if args[len(args)-1] == toComplete {
		return args[:len(args)-1]
	}
	return args
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

var netSortDescriptions = map[string]string{
	"type":         "IPAM object type",
	"network":      "network CIDR",
	"network_view": "IPAM network view",
	"comment":      "network comment",
}

func netSortFlagCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return netSortCompletions(toComplete), cobra.ShellCompDirectiveNoFileComp
}

func (a *App) completeNetSortValue(args []string) bool {
	prefix, ok := netSortCompletionPrefix(args)
	if !ok {
		return false
	}
	rows := netSortCompletions(prefix)
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

func netSortCompletionPrefix(args []string) (string, bool) {
	if len(args) < 4 || !strings.HasPrefix(args[0], "__complete") || !isNetListOrSearchArgs(args) {
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

func netSortCompletions(toComplete string) []string {
	prefix := strings.ToLower(strings.TrimSpace(toComplete))
	var rows []string
	for _, field := range netSortFields {
		appendNetSortCompletion(&rows, field, "ascending", prefix)
		appendNetSortCompletion(&rows, "-"+field, "descending", prefix)
	}
	return rows
}

func appendNetSortCompletion(rows *[]string, value string, direction string, prefix string) {
	if prefix != "" && !strings.HasPrefix(value, prefix) {
		return
	}
	field := strings.TrimPrefix(value, "-")
	description := netSortDescriptions[field]
	if description == "" {
		*rows = append(*rows, value)
		return
	}
	*rows = append(*rows, value+"\t"+description+" "+direction)
}

var networkColumnDescriptions = map[string]string{
	"type":         "IPAM object type",
	"network":      "network CIDR",
	"network_view": "IPAM network view",
	"comment":      "network comment",
}

func networkColumnFlagCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return networkColumnCompletions(toComplete), cobra.ShellCompDirectiveNoFileComp
}

func networkColumnCompletions(toComplete string) []string {
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
	for _, column := range networkOutputColumns {
		if selected[column] {
			continue
		}
		if prefix != "" && !strings.HasPrefix(column, prefix) {
			continue
		}
		candidate := base + column
		description := networkColumnDescriptions[column]
		if description == "" {
			rows = append(rows, candidate)
			continue
		}
		rows = append(rows, candidate+"\t"+description)
	}
	return rows
}

func commandZoneFlag(cmd *cobra.Command) string {
	return commandCompletionFlagValue(cmd, "zone")
}

func (a *App) completeRecordNames(cmd *cobra.Command, explicitZone, toComplete string) []string {
	profile, err := a.loadConfig(true)
	if err != nil {
		return nil
	}
	profile.DNSView = a.resolveDNSView(profile)
	if view := commandCompletionFlagValue(cmd, "view"); view != "" {
		profile.DNSView = view
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
	if err != nil {
		return nil, err
	}
	prefetchEnabled := a.completionCachePrefetchEnabled()
	if !entry.CacheFound {
		if prefetchEnabled {
			a.startRecordCacheRevalidationAsync(profile, zone)
		}
		return nil, nil
	}
	if prefetchEnabled && !a.cacheEntryFresh(entry, time.Now()) {
		a.startRecordCacheRevalidationAsync(profile, zone)
	}
	return recordsFromAllRecordRows(entry.Rows), nil
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
	if view := commandCompletionFlagValue(cmd, "view"); view != "" {
		profile.DNSView = view
	}
	zones, err := a.cachedZoneNames(profile)
	if err != nil {
		return nil
	}
	return matchingZoneNames(zones, toComplete)
}

func commandCompletionFlagValue(cmd *cobra.Command, name string) string {
	for _, flags := range []*pflag.FlagSet{cmd.Flags(), cmd.InheritedFlags(), cmd.PersistentFlags()} {
		if flags == nil {
			continue
		}
		if flag := flags.Lookup(name); flag != nil {
			return strings.TrimSpace(flag.Value.String())
		}
	}
	return ""
}

func (a *App) completeNetworkCIDRs(cmd *cobra.Command, toComplete string) []string {
	profile, err := a.loadConfig(true)
	if err != nil {
		return nil
	}
	networkView := commandCompletionFlagValue(cmd, "network-view")
	networks, err := a.cachedNetworkCIDRsForCompletion(profile, networkView, networkCompletionIncludesContainers(cmd))
	if err != nil {
		return nil
	}
	return matchingNetworkCIDRs(networks, toComplete)
}

func networkCompletionIncludesContainers(cmd *cobra.Command) bool {
	commandPath := cmd.CommandPath()
	return strings.HasPrefix(commandPath, "ib net") || commandPath == "ib dns next-ip"
}

func (a *App) cachedNetworkCIDRsForCompletion(profile Profile, networkView string, includeContainers bool) ([]map[string]any, error) {
	networkView = strings.TrimSpace(networkView)
	networkEntry, err := a.readCachedNetworks(profile, networkView)
	if err != nil {
		return nil, err
	}
	containerEntry := cachedPayload{}
	if includeContainers {
		containerEntry, err = a.readCachedNetworkContainers(profile, networkView)
		if err != nil {
			return nil, err
		}
	}
	prefetchEnabled := a.completionCachePrefetchEnabled()
	if prefetchEnabled {
		if !networkEntry.CacheFound || !a.cacheEntryFresh(networkEntry, time.Now()) {
			a.startNetCacheRefreshAsync(profile, netCacheKindNetworks, networkView, "")
		}
		if includeContainers && (!containerEntry.CacheFound || !a.cacheEntryFresh(containerEntry, time.Now())) {
			a.startNetCacheRefreshAsync(profile, netCacheKindContainers, networkView, "")
		}
	}
	if !networkEntry.CacheFound && (!includeContainers || !containerEntry.CacheFound) {
		return nil, nil
	}
	if !networkEntry.CacheFound {
		networkEntry.Rows = nil
	}
	if !containerEntry.CacheFound {
		containerEntry.Rows = nil
	}
	return networkObjectRows(networkEntry.Rows, containerEntry.Rows), nil
}

func (a *App) prefetchNetworkCacheForCompletion(profile Profile, networkView string) {
	entry, err := a.readCachedNetworks(profile, networkView)
	if err == nil && entry.CacheFound && a.cacheEntryFresh(entry, time.Now()) {
		return
	}
	if err != nil {
		return
	}
	a.startNetCacheRefreshAsync(profile, netCacheKindNetworks, networkView, "")
}

func (a *App) prefetchNetworkContainerCacheForCompletion(profile Profile, networkView string) {
	entry, err := a.readCachedNetworkContainers(profile, networkView)
	if err == nil && entry.CacheFound && a.cacheEntryFresh(entry, time.Now()) {
		return
	}
	if err != nil {
		return
	}
	a.startNetCacheRefreshAsync(profile, netCacheKindContainers, networkView, "")
}

func (a *App) cachedZoneNames(profile Profile) ([]string, error) {
	entry, err := a.readCachedZones(profile)
	if err != nil {
		return nil, err
	}
	prefetchEnabled := a.completionCachePrefetchEnabled()
	if !entry.CacheFound {
		if prefetchEnabled {
			a.startZoneCacheRefreshAsync(profile)
		}
		return nil, nil
	}
	if prefetchEnabled && !a.cacheEntryFresh(entry, time.Now()) {
		a.startZoneCacheRefreshAsync(profile)
	}
	return zoneNamesFromRows(entry.Rows), nil
}

func (a *App) startCompletionCachePrefetch(args []string) {
	if len(args) == 0 || !strings.HasPrefix(args[0], "__complete") {
		return
	}
	// Completion prefetch is intentionally configurable: it improves the next
	// DNS command after tab completion, but some operators prefer completion to
	// be a local cache read only and never start detached refresh helpers.
	if !a.completionCachePrefetchEnabled() {
		return
	}
	profile, err := a.completionProfile()
	if err != nil {
		return
	}
	if view := completionFlagValue(args, "view", "v"); view != "" {
		profile.DNSView = view
	}

	a.prefetchZoneCacheForCompletion(profile)

	zone, err := a.resolveCompletionDNSZone(profile, completionFlagValue(args, "zone", "z"))
	if err != nil {
		return
	}
	a.prefetchRecordCacheForCompletion(profile, zone)
}

func (a *App) completionProfile() (Profile, error) {
	// Completion prefetch runs before Cobra parses flags. Ignore any previous
	// command's in-memory overrides so the active shell/session context is used.
	previousZone, previousView := a.dnsZoneOverride, a.dnsViewOverride
	a.dnsZoneOverride, a.dnsViewOverride = "", ""
	profile, err := a.loadConfig(true)
	a.dnsZoneOverride, a.dnsViewOverride = previousZone, previousView
	return profile, err
}

func (a *App) prefetchZoneCacheForCompletion(profile Profile) {
	entry, err := a.readCachedZones(profile)
	if err == nil && entry.CacheFound && a.cacheEntryFresh(entry, time.Now()) {
		return
	}
	if err != nil {
		return
	}
	a.startZoneCacheRefreshAsync(profile)
}

func (a *App) prefetchRecordCacheForCompletion(profile Profile, zone string) {
	entry, err := a.readCachedRecords(profile, zone)
	if err == nil && entry.CacheFound && a.cacheEntryFresh(entry, time.Now()) {
		return
	}
	if err != nil {
		return
	}
	a.startRecordCacheRevalidationAsync(profile, zone)
}

func (a *App) resolveCompletionDNSZone(profile Profile, explicit string) (string, error) {
	if explicit != "" {
		return normalizeZoneName(explicit)
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
	return "", cliError("DNS zone is required")
}

func completionFlagValue(args []string, longName string, shortName string) string {
	if len(args) < 3 {
		return ""
	}
	end := len(args) - 1
	longFlag := "--" + longName
	shortFlag := "-" + shortName
	for index := 1; index < end; index++ {
		arg := strings.TrimSpace(args[index])
		if arg == "--" {
			return ""
		}
		if value, ok := strings.CutPrefix(arg, longFlag+"="); ok {
			return strings.TrimSpace(value)
		}
		if shortName != "" {
			if value, ok := strings.CutPrefix(arg, shortFlag+"="); ok {
				return strings.TrimSpace(value)
			}
			if strings.HasPrefix(arg, shortFlag) && len(arg) > len(shortFlag) {
				return strings.TrimSpace(strings.TrimPrefix(arg, shortFlag))
			}
		}
		if arg != longFlag && (shortName == "" || arg != shortFlag) {
			continue
		}
		if index+1 >= end {
			return ""
		}
		return strings.TrimSpace(args[index+1])
	}
	return ""
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

func matchingNetworkCIDRs(networks []map[string]any, toComplete string) []string {
	prefix := strings.ToLower(strings.TrimSpace(toComplete))
	targetPrefix, targetOK := parseIPv4Prefix(prefix)
	targetViews := map[string]bool{}
	if targetOK {
		for _, network := range networks {
			objectPrefix, ok := parseIPv4Prefix(cleanString(network["network"]))
			if ok && objectPrefix == targetPrefix {
				targetViews[cleanString(network["network_view"])] = true
			}
		}
	}
	seen := map[string]bool{}
	rows := make([]string, 0, len(networks))
	for _, network := range networks {
		cidr := cleanString(network["network"])
		if cidr == "" || seen[cidr] {
			continue
		}
		if prefix != "" && !strings.HasPrefix(strings.ToLower(cidr), prefix) && !networkCIDRHierarchyCompletionMatch(targetPrefix, targetOK, targetViews, network) {
			continue
		}
		seen[cidr] = true
		var descriptionParts []string
		if itemType := cleanString(network["type"]); itemType != "" {
			descriptionParts = append(descriptionParts, itemType)
		}
		if view := cleanString(network["network_view"]); view != "" {
			descriptionParts = append(descriptionParts, view)
		}
		description := strings.Join(descriptionParts, " ")
		if description != "" {
			rows = append(rows, cidr+"\t"+description)
			continue
		}
		rows = append(rows, cidr)
	}
	sort.Slice(rows, func(i, j int) bool {
		return strings.ToLower(recordCompletionName(rows[i])) < strings.ToLower(recordCompletionName(rows[j]))
	})
	return rows
}

func networkCIDRHierarchyCompletionMatch(targetPrefix netip.Prefix, targetOK bool, targetViews map[string]bool, network map[string]any) bool {
	if !targetOK || len(targetViews) == 0 || !targetViews[cleanString(network["network_view"])] {
		return false
	}
	objectPrefix, ok := parseIPv4Prefix(cleanString(network["network"]))
	if !ok || objectPrefix == targetPrefix {
		return false
	}
	return objectPrefix.Bits() > targetPrefix.Bits() && targetPrefix.Contains(objectPrefix.Addr())
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
    local cur cmd out line directive comp value flag_prefix allow_non_prefix
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

    out=$(IB_ACTIVE_HELP=0 IB_SHELL_PID=$$ "${cmd}" __completeNoDesc "${args[@]}" 2>/dev/null) || return 0

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
    allow_non_prefix=0
    if [[ "${value}" == */* ]]; then
        case "${COMP_WORDS[1]} ${COMP_WORDS[2]}" in
            "dns next-ip"|"net next-ip"|"net show")
                allow_non_prefix=1
                ;;
        esac
    fi

    for comp in "${lines[@]}"; do
        [[ -z "${comp}" ]] && continue
        if [[ "${allow_non_prefix}" -eq 1 || "${comp}" == "${value}"* ]]; then
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
    local line directive
    local -a requestArgs out completions

    words=("${=words[1,CURRENT]}")
    requestArgs=("${(@)words[2,-1]}")
    if [[ "${words[-1]}" == "" ]]; then
        requestArgs+=("")
    fi

    out=("${(@f)$(IB_ACTIVE_HELP=0 IB_SHELL_PID=$$ "${words[1]}" __complete "${requestArgs[@]}" 2>/dev/null)}")
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
    set -l cmd $args[1]
    set -e args[1]
    set -l output
    if test -z "$last_arg"
        set output (env IB_ACTIVE_HELP=0 IB_SHELL_PID=$fish_pid $cmd __complete $args "" 2>/dev/null)
    else
        set output (env IB_ACTIVE_HELP=0 IB_SHELL_PID=$fish_pid $cmd __complete $args $last_arg 2>/dev/null)
    end

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
