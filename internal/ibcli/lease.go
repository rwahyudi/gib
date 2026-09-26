package ibcli

import (
	"fmt"
	"net/netip"
	"net/url"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

const dhcpLeasePageSize = 20000

var (
	dhcpLeaseReturnFields = "address,hardware,client_hostname,binding_state,network,network_view,starts,ends"
	dhcpLeaseOutputFields = []string{"ip", "mac", "hostname", "state", "network", "network_view", "starts", "ends"}
	dhcpLeaseTableColumns = []string{"ip", "mac", "hostname", "state", "network", "starts", "ends"}
	dhcpLeaseSortFields   = []string{"ip", "mac", "hostname", "state", "network", "network_view", "starts", "ends"}
)

func (a *App) dhcpLeaseCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "lease", Short: "Inspect live DHCP leases"}
	cmd.AddCommand(a.dhcpLeaseListCommand(), a.dhcpLeaseShowCommand())
	return cmd
}

func (a *App) dhcpLeaseListCommand() *cobra.Command {
	var networkView, network, mac, hostname, state, sortRaw, columnsRaw string
	cmd := &cobra.Command{Use: "list [SEARCH]", Short: "List live DHCP leases", Args: cobra.MaximumNArgs(1), ValidArgsFunction: completeFlagsAfterArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		search := ""
		if len(args) == 1 {
			search = args[0]
		}
		leaseSort, err := parseDHCPLeaseSort(sortRaw, cmd.Flags().Changed("sort"))
		if err != nil {
			return err
		}
		leaseColumns, err := parseDHCPLeaseColumns(columnsRaw)
		if err != nil {
			return err
		}
		return a.runDHCPLeaseList(search, networkView, network, mac, hostname, state, leaseSort, leaseColumns)
	}}
	a.addNetworkViewFlag(cmd, &networkView, "network view filter")
	cmd.Flags().StringVar(&network, "network", "", "network filter")
	cmd.Flags().StringVar(&mac, "mac", "", "client MAC filter")
	cmd.Flags().StringVar(&hostname, "hostname", "", "client hostname filter")
	cmd.Flags().StringVar(&state, "state", "", "binding state filter")
	cmd.Flags().StringVarP(&sortRaw, "sort", "s", "", "sort leases by field: ip, mac, hostname, state, network, network_view, starts, or ends; prefix with - for descending")
	_ = cmd.RegisterFlagCompletionFunc("sort", dhcpLeaseSortFlagCompletion)
	cmd.Flags().StringVarP(&columnsRaw, "columns", "C", "", "lease output columns, comma-separated")
	_ = cmd.RegisterFlagCompletionFunc("columns", dhcpLeaseColumnFlagCompletion)
	return cmd
}

func (a *App) dhcpLeaseShowCommand() *cobra.Command {
	var networkView string
	cmd := &cobra.Command{Use: "show IP", Short: "Show a live DHCP lease", Args: cobra.ExactArgs(1), ValidArgsFunction: completeFlagsAfterArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		_, client, err := a.configuredClient()
		if err != nil {
			return err
		}
		lease, err := findDHCPLease(client, effectiveDHCPNetworkView(networkView), args[0])
		if err != nil {
			return err
		}
		return a.emitObject("DHCP Lease", dhcpLeaseOutputFields, dhcpLeaseOutputRow(lease))
	}}
	a.addNetworkViewFlag(cmd, &networkView, "network view filter")
	return cmd
}

func (a *App) runDHCPLeaseList(search, networkView, network, mac, hostname, state string, leaseSort dhcpLeaseSort, columns []string) error {
	_, client, err := a.configuredClient()
	if err != nil {
		return err
	}
	view := effectiveDHCPNetworkView(networkView)
	leases, err := queryDHCPLeases(client, view, network, mac, hostname, state, "")
	if err != nil {
		return err
	}
	search = strings.ToLower(strings.TrimSpace(search))
	rows := make([]map[string]any, 0, len(leases))
	for _, lease := range leases {
		row := dhcpLeaseOutputRow(lease)
		if search != "" && !dhcpLeaseRowContains(row, search) {
			continue
		}
		rows = append(rows, row)
	}
	sortDHCPLeases(rows)
	applyDHCPLeaseSort(rows, leaseSort)
	if len(columns) == 0 {
		columns = defaultDHCPLeaseColumns()
	}
	rows = selectDHCPLeaseOutputRows(rows, columns)
	if len(rows) == 0 && a.isTableOutput() {
		a.PrintWarning("No DHCP leases found.")
	}
	if a.isTableOutput() {
		return a.emitDHCPLeaseTable(rows, view, columns)
	}
	return a.emitRows(fmt.Sprintf("DHCP Leases (%d)", len(rows)), columns, rows)
}

func queryDHCPLeases(client *WapiClient, networkView, network, mac, hostname, state, address string) ([]map[string]any, error) {
	params := url.Values{}
	params.Set("_return_fields", dhcpLeaseReturnFields)
	params.Set("network_view", effectiveDHCPNetworkView(networkView))
	if network = strings.TrimSpace(network); network != "" {
		params.Set("network", network)
	}
	if mac = strings.TrimSpace(mac); mac != "" {
		params.Set("hardware", mac)
	}
	if hostname = strings.TrimSpace(hostname); hostname != "" {
		params.Set("client_hostname", hostname)
	}
	if state = strings.TrimSpace(state); state != "" {
		params.Set("binding_state", state)
	}
	if address = strings.TrimSpace(address); address != "" {
		params.Set("address", address)
	}
	rows, _, err := pagedQueryWithPageSizeComplete(client, "lease", params, dhcpLeasePageSize)
	return rows, err
}

func findDHCPLease(client *WapiClient, networkView, address string) (map[string]any, error) {
	ip, err := normalizeIPv4Address(address)
	if err != nil {
		return nil, err
	}
	leases, err := queryDHCPLeases(client, networkView, "", "", "", "", ip)
	if err != nil {
		return nil, err
	}
	if len(leases) == 0 {
		return nil, cliError("no DHCP lease found for %s", ip)
	}
	if len(leases) > 1 {
		return nil, cliError("multiple DHCP leases found for %s; specify --network-view", ip)
	}
	return leases[0], nil
}

func dhcpLeaseOutputRow(item map[string]any) map[string]any {
	return map[string]any{"ip": firstNonEmpty(cleanString(item["address"]), cleanString(item["ip"])), "mac": firstNonEmpty(cleanString(item["hardware"]), cleanString(item["mac"])), "hostname": firstNonEmpty(cleanString(item["client_hostname"]), cleanString(item["hostname"])), "state": firstNonEmpty(cleanString(item["binding_state"]), cleanString(item["state"])), "network": cleanString(item["network"]), "network_view": cleanString(item["network_view"]), "starts": cleanString(item["starts"]), "ends": cleanString(item["ends"])}
}
func dhcpLeaseRowContains(row map[string]any, search string) bool {
	for _, field := range dhcpLeaseOutputFields {
		if strings.Contains(strings.ToLower(cleanString(row[field])), search) {
			return true
		}
	}
	return false
}

func selectDHCPLeaseOutputRows(rows []map[string]any, columns []string) []map[string]any {
	selected := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		output := make(map[string]any, len(columns))
		for _, column := range columns {
			output[column] = row[column]
		}
		selected = append(selected, output)
	}
	return selected
}

type dhcpLeaseSort struct {
	Enabled bool
	Field   string
	Desc    bool
}

func defaultDHCPLeaseColumns() []string { return append([]string(nil), dhcpLeaseTableColumns...) }
func parseDHCPLeaseColumns(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultDHCPLeaseColumns(), nil
	}
	seen := map[string]bool{}
	var columns []string
	for _, part := range strings.Split(raw, ",") {
		column := strings.ToLower(strings.TrimSpace(part))
		if column == "" {
			return nil, cliError("lease column cannot be empty. Supported: %s", strings.Join(dhcpLeaseOutputFields, ", "))
		}
		if !containsDHCPLeaseString(dhcpLeaseOutputFields, column) {
			return nil, cliError("unsupported column %q. Supported: %s", column, strings.Join(dhcpLeaseOutputFields, ", "))
		}
		if seen[column] {
			return nil, cliError("duplicate column %q", column)
		}
		seen[column] = true
		columns = append(columns, column)
	}
	return columns, nil
}
func parseDHCPLeaseSort(raw string, enabled bool) (dhcpLeaseSort, error) {
	if !enabled {
		return dhcpLeaseSort{}, nil
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "ip"
	}
	desc := strings.HasPrefix(raw, "-")
	if desc {
		raw = strings.TrimPrefix(raw, "-")
	}
	field := strings.ToLower(strings.TrimSpace(raw))
	if !containsDHCPLeaseString(dhcpLeaseSortFields, field) {
		return dhcpLeaseSort{}, cliError("unsupported sort field %q. Supported: %s", field, strings.Join(dhcpLeaseSortFields, ", "))
	}
	return dhcpLeaseSort{Enabled: true, Field: field, Desc: desc}, nil
}
func sortDHCPLeases(rows []map[string]any) {
	sort.SliceStable(rows, func(i, j int) bool {
		return compareDHCPLeaseIP(cleanString(rows[i]["ip"]), cleanString(rows[j]["ip"])) < 0
	})
}
func applyDHCPLeaseSort(rows []map[string]any, option dhcpLeaseSort) {
	if !option.Enabled || len(rows) < 2 {
		return
	}
	sort.SliceStable(rows, func(i, j int) bool {
		left, right := cleanString(rows[i][option.Field]), cleanString(rows[j][option.Field])
		result := compareCaseInsensitiveText(left, right)
		if option.Field == "ip" {
			result = compareDHCPLeaseIP(left, right)
		}
		if option.Desc {
			result = -result
		}
		return result < 0
	})
}
func compareDHCPLeaseIP(left, right string) int {
	leftIP, leftErr := netip.ParseAddr(left)
	rightIP, rightErr := netip.ParseAddr(right)
	if leftErr == nil && rightErr == nil && leftIP.Compare(rightIP) != 0 {
		return leftIP.Compare(rightIP)
	}
	return compareCaseInsensitiveText(left, right)
}

func (a *App) emitDHCPLeaseTable(rows []map[string]any, networkView string, columns []string) error {
	displayRows := make([][]string, 0, len(rows))
	for _, row := range rows {
		display := make([]string, 0, len(columns))
		for _, column := range columns {
			value := stringify(row[column])
			if column == "hostname" {
				value = wrapRecordTableCell(value, dhcpReservationNameWidth)
			}
			display = append(display, value)
		}
		displayRows = append(displayRows, display)
	}
	fmt.Fprintln(a.Stdout, renderTable(fmt.Sprintf("DHCP Leases (%d)", len(rows)), titleCaseFields(columns), displayRows))
	a.printDHCPReservationTableFooter(networkView, len(rows))
	return nil
}
func dhcpLeaseSortFlagCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	prefix := strings.ToLower(strings.TrimSpace(toComplete))
	var rows []string
	for _, field := range dhcpLeaseSortFields {
		for _, value := range []string{field, "-" + field} {
			if prefix == "" || strings.HasPrefix(value, prefix) {
				rows = append(rows, value)
			}
		}
	}
	return rows, cobra.ShellCompDirectiveNoFileComp
}
func dhcpLeaseColumnFlagCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	raw := strings.ToLower(strings.TrimSpace(toComplete))
	parts := strings.Split(raw, ",")
	prefix := parts[len(parts)-1]
	selected := map[string]bool{}
	for _, part := range parts[:len(parts)-1] {
		if part = strings.TrimSpace(part); part != "" {
			selected[part] = true
		}
	}
	base := ""
	if len(parts) > 1 {
		base = strings.Join(parts[:len(parts)-1], ",") + ","
	}
	var rows []string
	for _, column := range dhcpLeaseOutputFields {
		if !selected[column] && (prefix == "" || strings.HasPrefix(column, prefix)) {
			rows = append(rows, base+column)
		}
	}
	return rows, cobra.ShellCompDirectiveNoFileComp
}
func containsDHCPLeaseString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
