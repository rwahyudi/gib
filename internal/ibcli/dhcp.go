package ibcli

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

const fixedAddressObject = "fixedaddress"

var (
	dhcpReservationReturnFields = "ipv4addr,mac,name,network,network_view,comment"
	dhcpReservationOutputFields = []string{"ip", "mac", "name", "network", "network_view", "comment"}
	dhcpReservationActionFields = []string{"status", "action", "type", "ip", "mac", "name", "network_view", "comment", "message"}
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
	var networkView, name, comment string
	cmd := &cobra.Command{Use: "list [SEARCH]", Short: "List DHCP fixed-address reservations", Args: cobra.MaximumNArgs(1), ValidArgsFunction: completeFlagsAfterArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		search := ""
		if len(args) == 1 {
			search = args[0]
		}
		return a.runDHCPReservationList(search, networkView, name, comment)
	}}
	a.addNetworkViewFlag(cmd, &networkView, "network view filter")
	cmd.Flags().StringVar(&name, "name", "", "reservation name filter")
	cmd.Flags().StringVarP(&comment, "comment", "c", "", "reservation comment filter")
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
	_, client, err := a.configuredClient()
	if err != nil {
		return err
	}
	reservations, err := queryDHCPReservations(client, networkView, "")
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
	if len(rows) == 0 && a.isTableOutput() {
		a.PrintWarning("No DHCP reservations found.")
	}
	return a.emitRows(fmt.Sprintf("DHCP Reservations (%d)", len(rows)), dhcpReservationOutputFields, rows)
}
func (a *App) runDHCPReservationShow(ip, networkView string) error {
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
	networkView, nextIP = strings.TrimSpace(networkView), strings.TrimSpace(nextIP)
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
	_, client, err := a.configuredClient()
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
	if nextIP != "" && !authoritative {
		return cliError("fixedaddress create response did not include the allocated IPv4 address")
	}
	if !a.isTableOutput() {
		return a.emitObject("Action", dhcpReservationActionFields, dhcpReservationActionRow("create", row, "created DHCP reservation"))
	}
	a.PrintSuccess("SUCCESS: created DHCP reservation " + cleanString(row["ip"]))
	return nil
}
func (a *App) runDHCPReservationEdit(ip, networkView string, updates map[string]any) error {
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
	_, client, err := a.configuredClient()
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
	row := dhcpReservationOutputRow(reservation)
	if !a.isTableOutput() {
		return a.emitObject("Action", dhcpReservationActionFields, dhcpReservationActionRow("delete", row, "deleted DHCP reservation"))
	}
	a.PrintSuccess("SUCCESS: deleted DHCP reservation " + ip)
	return nil
}
func queryDHCPReservations(client *WapiClient, networkView, ip string) ([]map[string]any, error) {
	params := url.Values{}
	params.Set("_return_fields", dhcpReservationReturnFields)
	if networkView = strings.TrimSpace(networkView); networkView != "" {
		params.Set("network_view", networkView)
	}
	if ip = strings.TrimSpace(ip); ip != "" {
		params.Set("ipv4addr", ip)
	}
	return pagedQuery(client, fixedAddressObject, params)
}
func findDHCPReservation(client *WapiClient, ip, networkView string) (map[string]any, error) {
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
		left, leftErr := netip.ParseAddr(cleanString(rows[i]["ip"]))
		right, rightErr := netip.ParseAddr(cleanString(rows[j]["ip"]))
		if leftErr == nil && rightErr == nil && left.Is4() && right.Is4() && left.Compare(right) != 0 {
			return left.Compare(right) < 0
		}
		return compareCaseInsensitiveText(cleanString(rows[i]["ip"]), cleanString(rows[j]["ip"])) < 0
	})
}
