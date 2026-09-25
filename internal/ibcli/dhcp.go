package ibcli

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const (
	fixedAddressObject          = "fixedaddress"
	dhcpReservationCacheKind    = "dhcp_reservations"
	dhcpReservationPageSize     = 20000
	dhcpReservationNameWidth    = 50
	dhcpReservationCommentWidth = 60
)

var (
	dhcpReservationReturnFields = "ipv4addr,mac,name,network,network_view,comment"
	dhcpReservationOutputFields = []string{"ip", "mac", "name", "network", "network_view", "comment"}
	dhcpReservationActionFields = []string{"status", "action", "type", "ip", "mac", "name", "network_view", "comment", "message"}
	dhcpReservationTableColumns = []string{"ip", "mac", "name", "network", "comment"}
	dhcpReservationSortFields   = []string{"ip", "mac", "name", "network", "network_view", "comment"}
)

func (a *App) dhcpCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "dhcp", Short: "Manage Infoblox DHCP reservations"}
	cmd.AddCommand(a.dhcpReservationCommand())
	return cmd
}
func (a *App) dhcpReservationCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "reservation", Short: "Manage DHCP fixed-address reservations"}
	cmd.AddCommand(a.dhcpReservationListCommand(), a.dhcpReservationShowCommand(), a.dhcpReservationCreateCommand(), a.dhcpReservationEditCommand(), a.dhcpReservationDeleteCommand())
	return cmd
}
func (a *App) dhcpReservationListCommand() *cobra.Command {
	var networkView, name, comment, sortRaw, columnsRaw string
	var refresh bool
	cmd := &cobra.Command{Use: "list [SEARCH]", Short: "List DHCP fixed-address reservations", Args: cobra.MaximumNArgs(1), ValidArgsFunction: completeFlagsAfterArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		search := ""
		if len(args) == 1 {
			search = args[0]
		}
		reservationSort, err := parseDHCPReservationSort(sortRaw, cmd.Flags().Changed("sort"))
		if err != nil {
			return err
		}
		reservationColumns, err := parseDHCPReservationColumns(columnsRaw)
		if err != nil {
			return err
		}
		return a.runDHCPReservationListWithOptions(search, networkView, name, comment, refresh, reservationSort, reservationColumns)
	}}
	a.addNetworkViewFlag(cmd, &networkView, "network view filter")
	cmd.Flags().StringVar(&name, "name", "", "reservation name filter")
	cmd.Flags().StringVarP(&comment, "comment", "c", "", "reservation comment filter")
	cmd.Flags().BoolVar(&refresh, "refresh", false, "refresh DHCP reservation cache before listing")
	cmd.Flags().StringVarP(&sortRaw, "sort", "s", "", "sort reservations by field: ip, mac, name, network, network_view, or comment; prefix with - for descending")
	_ = cmd.RegisterFlagCompletionFunc("sort", dhcpReservationSortFlagCompletion)
	cmd.Flags().StringVarP(&columnsRaw, "columns", "C", "", "reservation output columns, comma-separated")
	_ = cmd.RegisterFlagCompletionFunc("columns", dhcpReservationColumnFlagCompletion)
	return cmd
}
func (a *App) dhcpReservationShowCommand() *cobra.Command {
	var networkView string
	cmd := &cobra.Command{Use: "show IP", Short: "Show a DHCP fixed-address reservation", Args: cobra.ExactArgs(1), ValidArgsFunction: completeFlagsAfterArgs(1), RunE: func(cmd *cobra.Command, args []string) error { return a.runDHCPReservationShow(args[0], networkView) }}
	a.addNetworkViewFlag(cmd, &networkView, "network view for the reservation")
	return cmd
}
func (a *App) dhcpReservationCreateCommand() *cobra.Command {
	var nextIP, networkView, mac, name, comment string
	cmd := &cobra.Command{Use: "create [IP]", Short: "Create a DHCP fixed-address reservation", Example: "ib dhcp reservation create 192.0.2.25 --mac 00:11:22:33:44:55\nib dhcp reservation create --next-ip 192.0.2.0/24 --mac 00:11:22:33:44:55", Args: cobra.MaximumNArgs(1), ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return flagCompletions(cmd, toComplete), cobra.ShellCompDirectiveNoFileComp
	}, RunE: func(cmd *cobra.Command, args []string) error {
		ip := ""
		if len(args) == 1 {
			ip = args[0]
		}
		return a.runDHCPReservationCreate(ip, nextIP, networkView, mac, name, comment)
	}}
	a.addNetworkViewFlag(cmd, &networkView, "network view for the reservation")
	cmd.Flags().StringVar(&nextIP, "next-ip", "", "allocate the next available IP from NETWORK")
	cmd.Flags().StringVarP(&mac, "mac", "m", "", "client MAC address (required)")
	cmd.Flags().StringVar(&name, "name", "", "reservation name")
	cmd.Flags().StringVarP(&comment, "comment", "c", "", "reservation comment")
	return cmd
}
func (a *App) dhcpReservationEditCommand() *cobra.Command {
	var mac, networkView, name, comment string
	cmd := &cobra.Command{Use: "edit IP", Short: "Edit a DHCP fixed-address reservation", Args: cobra.ExactArgs(1), ValidArgsFunction: completeFlagsAfterArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		updates := map[string]any{}
		if cmd.Flags().Changed("mac") {
			updates["mac"] = mac
		}
		if cmd.Flags().Changed("name") {
			updates["name"] = name
		}
		if cmd.Flags().Changed("comment") {
			updates["comment"] = comment
		}
		return a.runDHCPReservationEdit(args[0], networkView, updates)
	}}
	cmd.Flags().StringVarP(&mac, "mac", "m", "", "new client MAC address")
	cmd.Flags().StringVar(&networkView, "network-view", "", "network view filter for the source reservation")
	_ = cmd.RegisterFlagCompletionFunc("network-view", a.networkViewFlagCompletion)
	cmd.Flags().StringVar(&name, "name", "", "new reservation name")
	cmd.Flags().StringVarP(&comment, "comment", "c", "", "new reservation comment")
	return cmd
}
func (a *App) dhcpReservationDeleteCommand() *cobra.Command {
	var networkView string
	var skipConfirm bool
	cmd := &cobra.Command{Use: "delete IP", Short: "Delete a DHCP fixed-address reservation", Args: cobra.ExactArgs(1), ValidArgsFunction: completeFlagsAfterArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return a.runDHCPReservationDelete(args[0], networkView, skipConfirm)
	}}
	a.addNetworkViewFlag(cmd, &networkView, "network view for the reservation")
	cmd.Flags().BoolVarP(&skipConfirm, "yes", "y", false, "skip delete confirmation prompt")
	return cmd
}

func (a *App) runDHCPReservationList(search, networkView, name, comment string) error {
	return a.runDHCPReservationListWithRefresh(search, networkView, name, comment, false)
}

func (a *App) runDHCPReservationListWithRefresh(search, networkView, name, comment string, refresh bool) error {
	return a.runDHCPReservationListWithOptions(search, networkView, name, comment, refresh, dhcpReservationSort{}, defaultDHCPReservationColumns())
}

func (a *App) runDHCPReservationListWithOptions(search, networkView, name, comment string, refresh bool, reservationSort dhcpReservationSort, columns []string) error {
	profile, client, err := a.configuredClient()
	if err != nil {
		return err
	}
	reservations, err := a.cachedDHCPReservations(profile, client, networkView, refresh)
	if err != nil {
		return err
	}
	search, name, comment = strings.ToLower(strings.TrimSpace(search)), strings.ToLower(strings.TrimSpace(name)), strings.ToLower(strings.TrimSpace(comment))
	rows := make([]map[string]any, 0, len(reservations))
	for _, reservation := range reservations {
		row := dhcpReservationOutputRow(reservation)
		if search != "" && !dhcpReservationRowContains(row, search) {
			continue
		}
		if name != "" && !strings.Contains(strings.ToLower(cleanString(row["name"])), name) {
			continue
		}
		if comment != "" && !strings.Contains(strings.ToLower(cleanString(row["comment"])), comment) {
			continue
		}
		rows = append(rows, row)
	}
	sortDHCPReservationRows(rows)
	applyDHCPReservationSort(rows, reservationSort)
	if len(rows) == 0 && a.isTableOutput() {
		a.PrintWarning("No DHCP reservations found.")
	}
	if a.isTableOutput() {
		return a.emitDHCPReservationTable(rows, networkView, columns)
	}
	return a.emitRows(fmt.Sprintf("DHCP Reservations (%d)", len(rows)), dhcpReservationOutputFields, rows)
}
func (a *App) cachedDHCPReservations(profile Profile, client *WapiClient, networkView string, forceRefresh bool) ([]map[string]any, error) {
	networkView = effectiveDHCPNetworkView(networkView)
	return a.cachedNetRows(
		profile,
		dhcpReservationCacheKind,
		networkView,
		"",
		func() (cachedPayload, error) { return a.readCachedDHCPReservations(profile, networkView) },
		func() ([]map[string]any, error) { return a.refreshDHCPReservationCache(profile, client, networkView) },
		netCacheOptions{ForceRefresh: forceRefresh, PropagateReadError: true},
	)
}

func (a *App) refreshDHCPReservationCache(profile Profile, client *WapiClient, networkView string) ([]map[string]any, error) {
	networkView = effectiveDHCPNetworkView(networkView)
	// Capture the cache generation before WAPI work. The guarded write compares
	// this token inside the same Badger transaction as the cache write.
	token, err := a.readDHCPReservationCacheToken(profile, networkView)
	if err != nil {
		return nil, err
	}
	rows, complete, err := queryDHCPReservationsWithCompleteness(client, networkView, "")
	if err != nil {
		return nil, err
	}
	canonical := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		canonical = append(canonical, dhcpReservationOutputRow(row))
	}
	if !complete {
		return nil, cliError("DHCP reservation list is incomplete: Infoblox returned an ambiguous plain response at the %d-row page limit", dhcpReservationPageSize)
	}
	wrote, err := a.writeDHCPReservationCacheIfCurrent(profile, networkView, canonical, time.Now(), token)
	if err != nil {
		return nil, err
	}
	if !wrote {
		// A mutation or cache clear invalidated this refresh while its WAPI
		// request was in flight. Never publish pre-mutation rows.
		return canonical, nil
	}
	return canonical, nil
}

func (a *App) reportDHCPReservationCacheFailure(err error) {
	if err == nil {
		return
	}
	a.debugEvent("dhcp reservation cache warning", df("error", err.Error()))
	a.PrintWarning("WARNING: DHCP reservation operation succeeded, but cache invalidation/refresh failed; do not retry the operation: " + err.Error())
}

func (a *App) queueDHCPReservationCacheRefresh(profile Profile, networkView string) error {
	networkView = effectiveDHCPNetworkView(networkView)
	if err := a.invalidateDHCPReservationCache(profile, networkView); err != nil {
		return err
	}
	if err := a.startNetCacheRefreshPrimary(profile, dhcpReservationCacheKind, networkView, ""); err != nil {
		return err
	}
	if networkView != "" {
		if err := a.startNetCacheRefreshPrimary(profile, dhcpReservationCacheKind, "", ""); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) runDHCPReservationShow(ip, networkView string) error {
	networkView = effectiveDHCPNetworkView(networkView)
	ip, err := normalizeIPv4Address(ip)
	if err != nil {
		return err
	}
	_, client, err := a.configuredClient()
	if err != nil {
		return err
	}
	reservation, err := findDHCPReservation(client, ip, networkView)
	if err != nil {
		return err
	}
	return a.emitObject("DHCP Reservation", dhcpReservationOutputFields, dhcpReservationOutputRow(reservation))
}
func (a *App) runDHCPReservationCreate(ip, nextIP, networkView, mac, name, comment string) error {
	networkView, nextIP = effectiveDHCPNetworkView(networkView), strings.TrimSpace(nextIP)
	if strings.TrimSpace(ip) != "" && nextIP != "" {
		return cliError("provide an explicit IP or --next-ip NETWORK, not both")
	}
	if strings.TrimSpace(ip) == "" && nextIP == "" {
		return cliError("an explicit IP or --next-ip NETWORK is required")
	}
	mac, err := normalizeDHCPMAC(mac)
	if err != nil {
		return err
	}
	name, err = normalizeDHCPName(name)
	if err != nil {
		return err
	}
	comment, err = normalizeComment(comment)
	if err != nil {
		return err
	}
	if nextIP != "" {
		network, err := normalizeNextIPNetwork(nextIP)
		if err != nil {
			return err
		}
		selector := "func:nextavailableip:" + network
		if networkView != "" {
			selector += "," + networkView
		}
		ip = selector
	} else {
		ip, err = normalizeIPv4Address(ip)
		if err != nil {
			return err
		}
	}
	profile, client, err := a.configuredClient()
	if err != nil {
		return err
	}
	payload := map[string]any{"ipv4addr": ip, "mac": mac}
	if networkView != "" {
		payload["network_view"] = networkView
	}
	if name != "" {
		payload["name"] = name
	}
	if comment != "" {
		payload["comment"] = comment
	}
	returnFields := url.Values{"_return_fields": []string{dhcpReservationReturnFields}}
	response, err := client.Request(http.MethodPost, fixedAddressObject, returnFields, payload)
	if err != nil {
		return err
	}
	row, authoritative := dhcpReservationCreateResponseRow(response, payload, nextIP != "")
	allocationWarning := ""
	if nextIP != "" && !authoritative {
		if recovered, recoveredAuthoritative, recoverErr := recoverDHCPReservationCreateRow(client, response, payload); recoverErr != nil {
			a.debugEvent("dhcp reservation allocation recovery failed", df("error", recoverErr.Error()))
			allocationWarning = "allocated IPv4 address could not be confirmed; do not retry"
		} else if recoveredAuthoritative {
			row, authoritative = recovered, true
		} else {
			allocationWarning = "allocated IPv4 address could not be confirmed; do not retry"
		}
		if !authoritative {
			row["ip"] = ""
			a.PrintWarning("WARNING: DHCP reservation was created successfully, but " + allocationWarning)
		}
	}
	affectedView := strings.TrimSpace(networkView)
	if affectedView == "" {
		affectedView = cleanString(row["network_view"])
	}
	a.reportDHCPReservationCacheFailure(a.queueDHCPReservationCacheRefresh(profile, affectedView))
	message := "created DHCP reservation"
	if allocationWarning != "" {
		message += "; " + allocationWarning
	}
	if !a.isTableOutput() {
		return a.emitObject("Action", dhcpReservationActionFields, dhcpReservationActionRow("create", row, message))
	}
	if allocationWarning != "" {
		a.PrintSuccess("SUCCESS: created DHCP reservation (" + allocationWarning + ")")
	} else {
		a.PrintSuccess("SUCCESS: created DHCP reservation " + cleanString(row["ip"]))
	}
	return nil
}
func (a *App) runDHCPReservationEdit(ip, networkView string, updates map[string]any) error {
	networkView = effectiveDHCPNetworkView(networkView)
	ip, err := normalizeIPv4Address(ip)
	if err != nil {
		return err
	}
	if len(updates) == 0 {
		return cliError("at least one reservation field must be provided")
	}
	payload := map[string]any{}
	for key, value := range updates {
		switch key {
		case "mac":
			mac, err := normalizeDHCPMAC(fmt.Sprint(value))
			if err != nil {
				return err
			}
			payload[key] = mac
		case "name":
			name, err := normalizeDHCPName(fmt.Sprint(value))
			if err != nil {
				return err
			}
			payload[key] = name
		case "comment":
			comment, err := normalizeComment(fmt.Sprint(value))
			if err != nil {
				return err
			}
			payload[key] = comment
		}
	}
	profile, client, err := a.configuredClient()
	if err != nil {
		return err
	}
	reservation, err := findDHCPReservation(client, ip, networkView)
	if err != nil {
		return err
	}
	ref := cleanString(reservation["_ref"])
	if ref == "" {
		return cliError("DHCP reservation %s does not include an Infoblox reference", ip)
	}
	if _, err := client.Request(http.MethodPut, ref, nil, payload); err != nil {
		return err
	}
	affectedView := cleanString(reservation["network_view"])
	if affectedView == "" {
		affectedView = strings.TrimSpace(networkView)
	}
	a.reportDHCPReservationCacheFailure(a.queueDHCPReservationCacheRefresh(profile, affectedView))
	row := dhcpReservationOutputRow(reservation)
	for key, value := range payload {
		row[key] = value
	}
	row["ip"] = ip
	if !a.isTableOutput() {
		return a.emitObject("Action", dhcpReservationActionFields, dhcpReservationActionRow("edit", row, "updated DHCP reservation"))
	}
	a.PrintSuccess("SUCCESS: updated DHCP reservation " + ip)
	return nil
}
func (a *App) runDHCPReservationDelete(ip, networkView string, skipConfirm bool) error {
	networkView = effectiveDHCPNetworkView(networkView)
	ip, err := normalizeIPv4Address(ip)
	if err != nil {
		return err
	}
	profile, client, err := a.configuredClient()
	if err != nil {
		return err
	}
	reservation, err := findDHCPReservation(client, ip, networkView)
	if err != nil {
		return err
	}
	ref := cleanString(reservation["_ref"])
	if ref == "" {
		return cliError("DHCP reservation %s does not include an Infoblox reference", ip)
	}
	if !skipConfirm {
		if !a.isTableOutput() || a.gum == nil || !a.gum.interactive() {
			return cliError("DHCP reservation delete confirmation requires an interactive terminal; rerun with -y to skip confirmation")
		}
		confirmed, err := a.gum.Confirm(fmt.Sprintf("Delete DHCP reservation %s?", ip), false)
		if err != nil {
			return err
		}
		if !confirmed {
			a.PrintNote("INFO: delete cancelled")
			return nil
		}
	}
	if _, err := client.Request(http.MethodDelete, ref, nil, nil); err != nil {
		return err
	}
	affectedView := cleanString(reservation["network_view"])
	if affectedView == "" {
		affectedView = strings.TrimSpace(networkView)
	}
	a.reportDHCPReservationCacheFailure(a.queueDHCPReservationCacheRefresh(profile, affectedView))
	row := dhcpReservationOutputRow(reservation)
	if !a.isTableOutput() {
		return a.emitObject("Action", dhcpReservationActionFields, dhcpReservationActionRow("delete", row, "deleted DHCP reservation"))
	}
	a.PrintSuccess("SUCCESS: deleted DHCP reservation " + ip)
	return nil
}
func queryDHCPReservations(client *WapiClient, networkView, ip string) ([]map[string]any, error) {
	rows, _, err := queryDHCPReservationsWithCompleteness(client, networkView, ip)
	return rows, err
}

func queryDHCPReservationsWithCompleteness(client *WapiClient, networkView, ip string) ([]map[string]any, bool, error) {
	networkView = effectiveDHCPNetworkView(networkView)
	params := url.Values{}
	params.Set("_return_fields", dhcpReservationReturnFields)
	if networkView = strings.TrimSpace(networkView); networkView != "" {
		params.Set("network_view", networkView)
	}
	if ip = strings.TrimSpace(ip); ip != "" {
		params.Set("ipv4addr", ip)
	}
	return pagedQueryWithPageSizeComplete(client, fixedAddressObject, params, dhcpReservationPageSize)
}
func findDHCPReservation(client *WapiClient, ip, networkView string) (map[string]any, error) {
	networkView = effectiveDHCPNetworkView(networkView)
	reservations, err := queryDHCPReservations(client, networkView, ip)
	if err != nil {
		return nil, err
	}
	if len(reservations) == 0 {
		return nil, cliError("no DHCP reservation found for %s", ip)
	}
	if len(reservations) > 1 {
		return nil, cliError("multiple DHCP reservations found for %s; specify --network-view", ip)
	}
	return reservations[0], nil
}
func recoverDHCPReservationCreateRow(client *WapiClient, response any, payload map[string]any) (map[string]any, bool, error) {
	item := dhcpReservationResponseItem(response)
	ref := cleanString(item["_ref"])
	if ref == "" {
		return nil, false, nil
	}
	// POST writes already use the primary. Force the recovery GET there too so
	// a lagging read server cannot make a successful allocation look missing.
	forcePrimary := client.ForcePrimaryReads
	client.ForcePrimaryReads = true
	recovered, err := client.Request(http.MethodGet, ref, nil, nil)
	client.ForcePrimaryReads = forcePrimary
	if err != nil {
		return nil, false, err
	}
	row, authoritative := dhcpReservationCreateResponseRow(recovered, payload, true)
	return row, authoritative, nil
}

func dhcpReservationCreateResponseRow(response any, payload map[string]any, requireIP bool) (map[string]any, bool) {
	item := dhcpReservationResponseItem(response)
	row := dhcpReservationOutputRow(item)
	fallback := map[string]any{
		"ip":           cleanString(payload["ipv4addr"]),
		"mac":          cleanString(payload["mac"]),
		"name":         cleanString(payload["name"]),
		"network_view": cleanString(payload["network_view"]),
		"comment":      cleanString(payload["comment"]),
	}
	ip := strings.TrimSpace(cleanString(row["ip"]))
	authoritative := ip != "" && !strings.HasPrefix(strings.ToLower(ip), "func:nextavailableip:")
	if !authoritative {
		return fallback, false
	}
	for key, value := range fallback {
		if strings.TrimSpace(cleanString(row[key])) == "" {
			row[key] = value
		}
	}
	if requireIP && strings.TrimSpace(cleanString(row["ip"])) == "" {
		return fallback, false
	}
	return row, true
}

func dhcpReservationResponseItem(response any) map[string]any {
	switch typed := response.(type) {
	case map[string]any:
		if result, ok := typed["result"]; ok {
			return dhcpReservationResponseItem(result)
		}
		return typed
	case []any:
		if len(typed) == 0 {
			return nil
		}
		return dhcpReservationResponseItem(typed[0])
	case []map[string]any:
		if len(typed) == 0 {
			return nil
		}
		return typed[0]
	case string:
		// Infoblox may encode a minimal create response as a JSON string
		// containing only the newly-created object's reference.
		ref := strings.TrimSpace(typed)
		if ref == "" {
			return nil
		}
		return map[string]any{"_ref": ref}
	default:
		return nil
	}
}

func normalizeDHCPMAC(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", cliError("--mac is required")
	}
	parsed, err := net.ParseMAC(value)
	if err != nil || len(parsed) != 6 {
		return "", cliError("--mac must be a valid Ethernet MAC address")
	}
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", parsed[0], parsed[1], parsed[2], parsed[3], parsed[4], parsed[5]), nil
}
func normalizeDHCPName(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if strings.ContainsAny(value, "\r\n") {
		return "", cliError("reservation name must not contain newlines")
	}
	return value, nil
}

type dhcpReservationSort struct {
	Enabled bool
	Field   string
	Desc    bool
}

func defaultDHCPReservationColumns() []string {
	return append([]string(nil), dhcpReservationTableColumns...)
}

func parseDHCPReservationColumns(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultDHCPReservationColumns(), nil
	}
	seen := map[string]bool{}
	var columns []string
	for _, part := range strings.Split(raw, ",") {
		column := strings.ToLower(strings.TrimSpace(part))
		if column == "" {
			return nil, cliError("reservation column cannot be empty. Supported: %s", strings.Join(dhcpReservationOutputFields, ", "))
		}
		if !containsDHCPString(dhcpReservationOutputFields, column) {
			return nil, cliError("unsupported column %q. Supported: %s", column, strings.Join(dhcpReservationOutputFields, ", "))
		}
		if seen[column] {
			return nil, cliError("duplicate column %q", column)
		}
		seen[column] = true
		columns = append(columns, column)
	}
	return columns, nil
}

func parseDHCPReservationSort(raw string, enabled bool) (dhcpReservationSort, error) {
	if !enabled {
		return dhcpReservationSort{}, nil
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
	if !containsDHCPString(dhcpReservationSortFields, field) {
		return dhcpReservationSort{}, cliError("unsupported sort field %q. Supported: %s", field, strings.Join(dhcpReservationSortFields, ", "))
	}
	return dhcpReservationSort{Enabled: true, Field: field, Desc: desc}, nil
}

func (a *App) printDHCPReservationTableFooter(networkView string, count int) {
	parts := []string{a.dhcpReservationContextLine(networkView)}
	if count > 5 {
		parts = append(parts, recordTotalBadgeStyle.Render(fmt.Sprintf("Total records: %d", count)))
	}
	fmt.Fprintln(a.Stdout, strings.Join(parts, "  "))
}

func dhcpReservationSortFlagCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	prefix := strings.ToLower(strings.TrimSpace(toComplete))
	var rows []string
	for _, field := range dhcpReservationSortFields {
		for _, value := range []string{field, "-" + field} {
			if prefix == "" || strings.HasPrefix(value, prefix) {
				rows = append(rows, value)
			}
		}
	}
	return rows, cobra.ShellCompDirectiveNoFileComp
}

func dhcpReservationColumnFlagCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
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
	for _, column := range dhcpReservationOutputFields {
		if !selected[column] && (prefix == "" || strings.HasPrefix(column, prefix)) {
			rows = append(rows, base+column)
		}
	}
	return rows, cobra.ShellCompDirectiveNoFileComp
}

func containsDHCPString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func effectiveDHCPNetworkView(networkView string) string {
	if networkView = strings.TrimSpace(networkView); networkView == "" {
		return "default"
	}
	return networkView
}

func (a *App) emitDHCPReservationTable(rows []map[string]any, networkView string, columns []string) error {
	displayRows := make([][]string, 0, len(rows))
	for _, row := range rows {
		display := make([]string, 0, len(columns))
		for _, column := range columns {
			value := stringify(row[column])
			if column == "name" {
				value = wrapRecordTableCell(value, dhcpReservationNameWidth)
			} else if column == "comment" {
				value = wrapRecordTableCell(value, dhcpReservationCommentWidth)
			}
			display = append(display, value)
		}
		displayRows = append(displayRows, display)
	}
	fmt.Fprintln(a.Stdout, renderTable(fmt.Sprintf("DHCP Reservations (%d)", len(rows)), titleCaseFields(columns), displayRows))
	a.printDHCPReservationTableFooter(networkView, len(rows))
	return nil
}

func (a *App) dhcpReservationContextLine(networkView string) string {
	profile, scope := a.defaultConfigProfileAndScope()
	profileName := profile.Name
	if profileName == "" {
		profileName = defaultProfileName
	}
	return contextTitleStyle.Render("Current Context:") + " " + strings.Join([]string{
		renderContextPair("Profile", profileContextName(profileName, scope), contextProfileValueStyle),
		renderContextPair("View", effectiveDHCPNetworkView(networkView), contextViewValueStyle),
	}, " | ")
}

func dhcpReservationOutputRow(item map[string]any) map[string]any {
	return map[string]any{"ip": firstNonEmpty(cleanString(item["ipv4addr"]), cleanString(item["ip"])), "mac": cleanString(item["mac"]), "name": cleanString(item["name"]), "network": cleanString(item["network"]), "network_view": cleanString(item["network_view"]), "comment": cleanString(item["comment"])}
}
func dhcpReservationActionRow(action string, row map[string]any, message string) map[string]any {
	return map[string]any{"status": "success", "action": action, "type": "RESERVATION", "ip": cleanString(row["ip"]), "mac": cleanString(row["mac"]), "name": cleanString(row["name"]), "network_view": cleanString(row["network_view"]), "comment": cleanString(row["comment"]), "message": message}
}
func dhcpReservationRowContains(row map[string]any, search string) bool {
	for _, field := range dhcpReservationOutputFields {
		if strings.Contains(strings.ToLower(cleanString(row[field])), search) {
			return true
		}
	}
	return false
}
func sortDHCPReservationRows(rows []map[string]any) {
	sort.SliceStable(rows, func(i, j int) bool {
		return compareDHCPReservationIP(cleanString(rows[i]["ip"]), cleanString(rows[j]["ip"])) < 0
	})
}

func applyDHCPReservationSort(rows []map[string]any, option dhcpReservationSort) {
	if !option.Enabled || len(rows) < 2 {
		return
	}
	sort.SliceStable(rows, func(i, j int) bool {
		left, right := cleanString(rows[i][option.Field]), cleanString(rows[j][option.Field])
		var result int
		if option.Field == "ip" {
			result = compareDHCPReservationIP(left, right)
		} else {
			result = compareCaseInsensitiveText(left, right)
		}
		if option.Desc {
			result = -result
		}
		return result < 0
	})
}

func compareDHCPReservationIP(left, right string) int {
	leftIP, leftErr := netip.ParseAddr(left)
	rightIP, rightErr := netip.ParseAddr(right)
	if leftErr == nil && rightErr == nil && leftIP.Compare(rightIP) != 0 {
		return leftIP.Compare(rightIP)
	}
	return compareCaseInsensitiveText(left, right)
}
