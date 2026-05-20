package ibcli

import (
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
)

func (a *App) configCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "config",
		Aliases: []string{"configure"},
		Short:   "Manage Infoblox configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runConfigOverview()
		},
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "new [PROFILE]",
		Short: "Create a new Infoblox profile",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			makeDefault, _ := cmd.Flags().GetBool("default")
			profileName := ""
			if len(args) > 0 {
				profileName = args[0]
			}
			return a.saveConfigInteractive(profileName, true, makeDefault)
		},
	})
	cmd.Commands()[0].Flags().Bool("default", false, "make this profile the default")

	editCmd := &cobra.Command{
		Use:               "edit [PROFILE]",
		Short:             "Edit an existing Infoblox profile",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.profileArgCompletion(true),
		RunE: func(cmd *cobra.Command, args []string) error {
			makeDefault, _ := cmd.Flags().GetBool("default")
			profileName := ""
			if len(args) > 0 {
				profileName = args[0]
			}
			return a.saveConfigInteractive(profileName, false, makeDefault)
		},
	}
	editCmd.Flags().Bool("default", false, "make this profile the default")
	cmd.AddCommand(editCmd)
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List configured profiles",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.listProfiles()
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:               "use PROFILE",
		Short:             "Set the default profile",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: a.profileArgCompletion(true),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.useProfile(args[0])
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:               "delete PROFILE",
		Short:             "Delete a non-default profile",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: a.profileArgCompletion(false),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.deleteProfile(args[0])
		},
	})
	cmd.AddCommand(a.completionCommand())
	cmd.AddCommand(a.cacheCommand())
	return cmd
}

func (a *App) cacheCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "cache", Short: "Manage local SQLite cache"}
	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show local cache status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			snapshot, err := a.cacheStatusSnapshot()
			if err != nil {
				return err
			}
			if len(snapshot.Entries) == 0 && a.isTableOutput() {
				a.PrintWarning("No cache entries found.")
			}
			return a.emitCacheStatus(snapshot)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "clear",
		Short: "Clear local cache entries",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.clearCache(); err != nil {
				return err
			}
			if !a.isTableOutput() {
				return a.emitObject("Action", []string{"status", "action", "type", "name", "zone", "view", "message"}, actionRow("clear", "CACHE", "local", "", "", "cleared local cache"))
			}
			a.PrintSuccess("SUCCESS: cleared local cache")
			return nil
		},
	})
	revalidateCmd := &cobra.Command{
		Use:    "revalidate-record",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			profileName, _ := cmd.Flags().GetString("profile")
			view, _ := cmd.Flags().GetString("view")
			zone, _ := cmd.Flags().GetString("zone")
			return a.runRecordCacheRevalidate(profileName, view, zone)
		},
	}
	revalidateCmd.Flags().String("profile", "", "profile name")
	revalidateCmd.Flags().String("view", "", "DNS view")
	revalidateCmd.Flags().String("zone", "", "DNS zone")
	cmd.AddCommand(revalidateCmd)
	refreshZonesCmd := &cobra.Command{
		Use:    "refresh-zones",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			profileName, _ := cmd.Flags().GetString("profile")
			view, _ := cmd.Flags().GetString("view")
			return a.runZoneCacheRefresh(profileName, view)
		},
	}
	refreshZonesCmd.Flags().String("profile", "", "profile name")
	refreshZonesCmd.Flags().String("view", "", "DNS view")
	cmd.AddCommand(refreshZonesCmd)
	refreshNetCmd := &cobra.Command{
		Use:    "refresh-net",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			profileName, _ := cmd.Flags().GetString("profile")
			kind, _ := cmd.Flags().GetString("kind")
			networkView, _ := cmd.Flags().GetString("network-view")
			ip, _ := cmd.Flags().GetString("ip")
			return a.runNetCacheRefresh(profileName, kind, networkView, ip)
		},
	}
	refreshNetCmd.Flags().String("profile", "", "profile name")
	refreshNetCmd.Flags().String("kind", "", "net cache kind")
	refreshNetCmd.Flags().String("network-view", "", "IPAM network view")
	refreshNetCmd.Flags().String("ip", "", "IPv4 address")
	cmd.AddCommand(refreshNetCmd)
	return cmd
}

func (a *App) completionCommand() *cobra.Command {
	return &cobra.Command{
		Use:               "completion [bash|zsh|fish|windows]",
		Short:             "Generate or install shell completion",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: shellNameCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				a.PrintNote("Shell completion setup:")
				fmt.Fprintln(a.Stdout, `  Bash: ib config completion bash > ~/.ib-complete.bash && printf '\n# ib shell completion\n. ~/.ib-complete.bash\n' >> ~/.bashrc`)
				fmt.Fprintln(a.Stdout, `  Zsh:  ib config completion zsh > ~/.ib-complete.zsh && printf '\n# ib shell completion\n. ~/.ib-complete.zsh\n' >> ~/.zshrc`)
				fmt.Fprintln(a.Stdout, `  Fish: ib config completion fish > ~/.config/fish/completions/ib.fish`)
				if windowsCompletionAvailable() {
					fmt.Fprintln(a.Stdout, `  PowerShell: ib config completion windows`)
				}
				return nil
			}
			switch args[0] {
			case "bash":
				_, err := fmt.Fprint(a.Stdout, dynamicBashCompletionScript())
				return err
			case "zsh":
				_, err := fmt.Fprint(a.Stdout, dynamicZshCompletionScript())
				return err
			case "fish":
				_, err := fmt.Fprint(a.Stdout, dynamicFishCompletionScript())
				return err
			case "windows":
				return a.runWindowsCompletionSetup()
			default:
				return cliError("unsupported shell %q; use bash, zsh, fish, or windows", args[0])
			}
		},
	}
}

func (a *App) dnsCommand() *cobra.Command {
	a.dnsZoneOverride = ""
	a.dnsViewOverride = ""
	cmd := &cobra.Command{
		Use:   "dns",
		Short: "Manage Infoblox DNS records",
	}
	cmd.PersistentFlags().StringVarP(&a.dnsZoneOverride, "zone", "z", "", "DNS zone override for this command")
	cmd.PersistentFlags().StringVarP(&a.dnsViewOverride, "view", "v", "", "DNS view override for this command")
	_ = cmd.RegisterFlagCompletionFunc("zone", a.zoneFlagCompletion)
	cmd.AddCommand(a.dnsViewCommand())
	cmd.AddCommand(a.dnsZoneCommand())
	cmd.AddCommand(a.dnsNextIPCommand())
	cmd.AddCommand(a.dnsCreateCommand())
	cmd.AddCommand(a.dnsEditCommand())
	cmd.AddCommand(a.dnsListCommand())
	cmd.AddCommand(a.dnsSearchCommand())
	cmd.AddCommand(a.dnsDeleteCommand())
	return cmd
}

func (a *App) netCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "net",
		Short: "Manage Infoblox IPAM networks",
	}
	cmd.AddCommand(a.netViewCommand())
	cmd.AddCommand(a.netListCommand())
	cmd.AddCommand(a.netSearchCommand())
	cmd.AddCommand(a.netShowCommand())
	cmd.AddCommand(a.netAddressCommand())
	cmd.AddCommand(a.netNextIPCommand())
	return cmd
}

func (a *App) netViewCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "view", Short: "Manage IPAM network views"}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List IPAM network views",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runNetViewList()
		},
	})
	return cmd
}

func (a *App) netListCommand() *cobra.Command {
	var networkView string
	var sortRaw string
	var columnsRaw string
	cmd := &cobra.Command{
		Use:               "list [SEARCH]",
		Short:             "List IPAM networks and containers",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: zoneListArgCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			netSort, err := parseNetSort(sortRaw, cmd.Flags().Changed("sort"))
			if err != nil {
				return err
			}
			columns, err := parseNetworkColumns(columnsRaw)
			if err != nil {
				return err
			}
			search := ""
			if len(args) > 0 {
				search = args[0]
			}
			return a.runNetList(search, networkView, netSort, columns)
		},
	}
	cmd.Flags().StringVar(&networkView, "network-view", "", "network view filter")
	addNetSortFlag(cmd, &sortRaw)
	addNetworkColumnsFlag(cmd, &columnsRaw)
	return cmd
}

func (a *App) netSearchCommand() *cobra.Command {
	var networkView string
	var sortRaw string
	var columnsRaw string
	cmd := &cobra.Command{
		Use:               "search KEYWORD",
		Short:             "Search IPAM networks and containers by type, CIDR, view, or comment",
		Args:              exactArgsOrUsage(1),
		ValidArgsFunction: completeFlagsAfterArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			netSort, err := parseNetSort(sortRaw, cmd.Flags().Changed("sort"))
			if err != nil {
				return err
			}
			columns, err := parseNetworkColumns(columnsRaw)
			if err != nil {
				return err
			}
			return a.runNetList(args[0], networkView, netSort, columns)
		},
	}
	cmd.Flags().StringVar(&networkView, "network-view", "", "network view filter")
	addNetSortFlag(cmd, &sortRaw)
	addNetworkColumnsFlag(cmd, &columnsRaw)
	return cmd
}

func (a *App) netShowCommand() *cobra.Command {
	var networkView string
	cmd := &cobra.Command{
		Use:               "show NETWORK",
		Short:             "Show IPAM network or container details",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: a.networkArgCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runNetShow(args[0], networkView)
		},
	}
	cmd.Flags().StringVar(&networkView, "network-view", "", "network view for the target network")
	return cmd
}

func (a *App) netAddressCommand() *cobra.Command {
	var networkView string
	cmd := &cobra.Command{
		Use:               "address IP",
		Short:             "Show IPAM address details",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeFlagsAfterArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runNetAddress(args[0], networkView)
		},
	}
	cmd.Flags().StringVar(&networkView, "network-view", "", "network view for the address lookup")
	return cmd
}

func (a *App) dnsViewCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "view", Short: "Manage active DNS views"}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List DNS views",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			profile, client, err := a.configuredClient()
			if err != nil {
				return err
			}
			params := url.Values{"_return_fields": []string{viewReturnFields}}
			results, err := pagedQuery(client, viewObject, params)
			if err != nil {
				return err
			}
			active := profile.DNSView
			rows := make([]map[string]any, 0, len(results))
			for _, result := range results {
				view := cleanString(result["name"])
				rows = append(rows, map[string]any{"active": view == active, "view": view})
			}
			sort.Slice(rows, func(i, j int) bool {
				return strings.ToLower(fmt.Sprint(rows[i]["view"])) < strings.ToLower(fmt.Sprint(rows[j]["view"]))
			})
			if a.isTableOutput() {
				a.PrintContext()
			}
			return a.emitRows(fmt.Sprintf("DNS Views (%d)", len(rows)), []string{"active", "view"}, rows)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "use VIEW",
		Short: "Set the active DNS view for this shell session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, client, err := a.configuredClient()
			if err != nil {
				return err
			}
			viewNames, err := queryViewNames(client)
			if err != nil {
				return err
			}
			selected, err := matchChoice(args[0], viewNames, "DNS view")
			if err != nil {
				return err
			}
			if err := a.writeSessionView(selected); err != nil {
				return err
			}
			if !a.isTableOutput() {
				return a.emitObject("Action", []string{"status", "action", "type", "name", "zone", "view", "message"}, actionRow("use", "VIEW", selected, "", selected, "active DNS view set"))
			}
			a.PrintSuccess("SUCCESS: active DNS view set to " + selected)
			a.PrintNote("This applies to the current shell session only.")
			return nil
		},
	})
	return cmd
}

func (a *App) dnsZoneCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "zone", Short: "Manage authoritative DNS zones"}
	createCmd := &cobra.Command{
		Use:   "create ZONE",
		Short: "Create a DNS zone",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			zoneFormat, _ := cmd.Flags().GetString("format")
			comment, _ := cmd.Flags().GetString("comment")
			nsGroup, _ := cmd.Flags().GetString("ns-group")
			return a.runZoneCreate(args[0], zoneFormat, comment, nsGroup)
		},
	}
	createCmd.Flags().String("format", "FORWARD", "zone format: FORWARD, IPV4, or IPV6")
	createCmd.Flags().String("comment", "", "zone comment")
	createCmd.Flags().String("ns-group", "", "name server group")
	cmd.AddCommand(createCmd)
	var zoneTypeFilter string
	var zoneExclude []string
	var zoneSortRaw string
	var zoneColumnsRaw string
	listCmd := &cobra.Command{
		Use:               "list [SEARCH]",
		Short:             "List DNS zones",
		Annotations:       map[string]string{disableZoneOverrideAnnotation: "true"},
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: zoneListArgCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			formats, err := parseZoneFormats(zoneTypeFilter)
			if err != nil {
				return err
			}
			zoneSort, err := parseZoneSort(zoneSortRaw, cmd.Flags().Changed("sort"))
			if err != nil {
				return err
			}
			zoneColumns, err := parseZoneColumns(zoneColumnsRaw)
			if err != nil {
				return err
			}
			search := ""
			if len(args) > 0 {
				search = args[0]
			}
			profile, client, err := a.configuredClient()
			if err != nil {
				return err
			}
			if a.isTableOutput() {
				a.PrintContext()
			}
			var zones []map[string]any
			if err := a.withSpinner("Loading DNS zones...", func() error {
				var loadErr error
				zones, loadErr = a.cachedZones(profile, client, search)
				return loadErr
			}); err != nil {
				return err
			}
			zones = filterListedZones(zones, formats, zoneExclude)
			applyZoneSort(zones, zoneSort)
			if len(zones) == 0 && a.isTableOutput() {
				a.PrintWarning("No zones found.")
			}
			return a.emitZones(zones, zoneColumns)
		},
	}
	listCmd.Flags().StringVarP(&zoneTypeFilter, "type", "t", "", "zone format filter, comma-separated")
	_ = listCmd.RegisterFlagCompletionFunc("type", zoneFormatFlagCompletion)
	listCmd.Flags().StringArrayVarP(&zoneExclude, "exclude", "e", nil, "exclude zones matching keyword")
	addZoneSortFlag(listCmd, &zoneSortRaw)
	addZoneColumnsFlag(listCmd, &zoneColumnsRaw)
	cmd.AddCommand(listCmd)
	infoCmd := &cobra.Command{
		Use:   "info ZONE",
		Short: "Show DNS zone details",
		Args:  cobra.ExactArgs(1),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return flagCompletions(cmd, toComplete), cobra.ShellCompDirectiveNoFileComp
			}
			return a.zoneArgCompletion(cmd, args, toComplete)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runZoneInfo(args[0])
		},
	}
	cmd.AddCommand(infoCmd)
	deleteCmd := &cobra.Command{
		Use:   "delete ZONE",
		Short: "Delete a DNS zone",
		Args:  cobra.ExactArgs(1),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return flagCompletions(cmd, toComplete), cobra.ShellCompDirectiveNoFileComp
			}
			return a.zoneArgCompletion(cmd, args, toComplete)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runZoneDelete(args[0])
		},
	}
	cmd.AddCommand(deleteCmd)
	useCmd := &cobra.Command{
		Use:   "use ZONE",
		Short: "Set the active DNS zone for this shell session",
		Args:  cobra.ExactArgs(1),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return flagCompletions(cmd, toComplete), cobra.ShellCompDirectiveNoFileComp
			}
			return a.zoneArgCompletion(cmd, args, toComplete)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			zone, err := normalizeZoneName(args[0])
			if err != nil {
				return err
			}
			profile := a.defaultConfigValues()
			if err := a.writeSessionZone(zone, profile.Name); err != nil {
				return err
			}
			if !a.isTableOutput() {
				return a.emitObject("Action", []string{"status", "action", "type", "name", "zone", "view", "message"}, actionRow("use", "ZONE", zone, zone, "", "active DNS zone set"))
			}
			a.PrintSuccess("SUCCESS: active DNS zone set to " + zone)
			a.PrintNote("This applies to the current shell session and selected profile only.")
			a.PrintNote("For an explicit environment override, run: export " + defaultZoneEnv + "=" + shellQuote(zone))
			return nil
		},
	}
	cmd.AddCommand(useCmd)
	return cmd
}

func (a *App) dnsCreateCommand() *cobra.Command {
	var comment string
	var ttl int
	var noptr bool
	cmd := &cobra.Command{
		Use:               "create NAME TYPE VALUE",
		Short:             "Create a DNS record",
		Args:              cobra.ExactArgs(3),
		ValidArgsFunction: createArgCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runDNSCreate(args[1], args[0], args[2], a.dnsZoneOverride, ttl, noptr, comment)
		},
	}
	cmd.Flags().IntVarP(&ttl, "ttl", "t", -1, "optional record TTL in seconds")
	cmd.Flags().BoolVar(&noptr, "noptr", false, "do not manage PTR records for A/AAAA workflows")
	cmd.Flags().StringVarP(&comment, "comment", "c", "", "record comment")
	return cmd
}

func (a *App) dnsNextIPCommand() *cobra.Command {
	return a.nextIPCommand(a.runDNSNextIP)
}

func (a *App) netNextIPCommand() *cobra.Command {
	cmd := a.nextIPCommand(a.runNetNextIP)
	cmd.Short = "Find the next available IP in a network or container"
	if flag := cmd.Flags().Lookup("network-view"); flag != nil {
		flag.Usage = "network view for the target network or container"
	}
	return cmd
}

func (a *App) nextIPCommand(run func(string, string, int, []string) error) *cobra.Command {
	var networkView string
	var num int
	var exclude []string
	cmd := &cobra.Command{
		Use:               "next-ip NETWORK",
		Short:             "Find the next available IP in a network or container",
		Annotations:       map[string]string{disableDNSContextAnnotation: "true"},
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: a.networkArgCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(args[0], networkView, num, exclude)
		},
	}
	cmd.Flags().StringVar(&networkView, "network-view", "", "network view for the target network")
	cmd.Flags().IntVarP(&num, "num", "n", 1, "number of IP addresses to request, 1-20")
	cmd.Flags().StringArrayVarP(&exclude, "exclude", "e", nil, "IP address to exclude from allocation; repeatable")
	return cmd
}

func (a *App) dnsEditCommand() *cobra.Command {
	var comment string
	var ttl int
	var noptr bool
	cmd := &cobra.Command{
		Use:               "edit NAME [TYPE] [VALUE]",
		Short:             "Edit an existing DNS record",
		Args:              cobra.RangeArgs(1, 3),
		ValidArgsFunction: a.editArgCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			recordName := args[0]
			recordType := ""
			var value *string
			if len(args) >= 2 {
				recordType = strings.ToLower(args[1])
				if _, ok := recordTypes[recordType]; !ok {
					return cliError("unsupported record type %q. Supported: %s", recordType, strings.Join(supportedRecordTypes(), ", "))
				}
			}
			if len(args) == 3 {
				value = &args[2]
			}
			return a.runDNSEdit(recordName, recordType, value, a.dnsZoneOverride, ttl, noptr, comment)
		},
	}
	cmd.Flags().IntVarP(&ttl, "ttl", "t", -1, "optional record TTL in seconds")
	cmd.Flags().BoolVar(&noptr, "noptr", false, "do not manage PTR records for A/AAAA workflows")
	cmd.Flags().StringVarP(&comment, "comment", "c", "", "record comment")
	return cmd
}

func (a *App) dnsListCommand() *cobra.Command {
	var details bool
	var recursive bool
	var typeFilter string
	var exclude []string
	var sortRaw string
	var columnsRaw string
	cmd := &cobra.Command{
		Use:               "list [ZONE]",
		Short:             "List DNS records in a zone",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.dnsListArgCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			types, err := parseRecordTypes(typeFilter)
			if err != nil {
				return err
			}
			recordSort, err := parseRecordSort(sortRaw, cmd.Flags().Changed("sort"))
			if err != nil {
				return err
			}
			recordColumns, err := parseRecordColumns(columnsRaw)
			if err != nil {
				return err
			}
			zone := ""
			if len(args) > 0 {
				zone = args[0]
			}
			profile, client, err := a.configuredClient()
			if err != nil {
				return err
			}
			target, err := a.resolveDNSZone(profile, zone)
			if err != nil {
				return err
			}
			var records []TypedRecord
			if err := a.withSpinner("Loading DNS records...", func() error {
				var loadErr error
				records, loadErr = a.listRecordsForZone(profile, client, target, recursive, details)
				return loadErr
			}); err != nil {
				return err
			}
			records = filterListedRecords(records, SearchOptions{Types: types, Exclude: exclude})
			applyRecordSort(records, recordSort)
			if len(records) == 0 && a.isTableOutput() {
				scope := "zone " + target
				if recursive {
					scope += " or child zones"
				}
				a.PrintWarning("No records found in " + scope + ".")
			}
			return a.emitRecordsWithContext(records, true, recordColumns)
		},
	}
	cmd.Flags().BoolVar(&details, "details", false, "load per-record details such as explicit TTLs; slower for large zones")
	cmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "include child authoritative zones")
	cmd.Flags().StringVarP(&typeFilter, "type", "t", "", "record type filter, comma-separated")
	_ = cmd.RegisterFlagCompletionFunc("type", recordTypeFlagCompletion)
	cmd.Flags().StringArrayVarP(&exclude, "exclude", "e", nil, "exclude records matching keyword")
	addRecordSortFlag(cmd, &sortRaw)
	addRecordColumnsFlag(cmd, &columnsRaw)
	return cmd
}

func (a *App) dnsSearchCommand() *cobra.Command {
	var options SearchOptions
	var typeFilter string
	var sortRaw string
	var columnsRaw string
	cmd := &cobra.Command{
		Use:   "search <keyword>",
		Short: "Search DNS records by name, value, or comment",
		Example: strings.TrimSpace(`ib dns search app
ib dns search app -t host
ib dns search app -z example.com
ib dns search app -z example.com -r
ib dns search app --global`),
		Args:              exactArgsOrUsage(1),
		ValidArgsFunction: completeFlagsAfterArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			options.Keyword = args[0]
			types, err := parseRecordTypes(typeFilter)
			if err != nil {
				return err
			}
			recordSort, err := parseRecordSort(sortRaw, cmd.Flags().Changed("sort"))
			if err != nil {
				return err
			}
			recordColumns, err := parseRecordColumns(columnsRaw)
			if err != nil {
				return err
			}
			options.Types = types
			options.Sort = recordSort
			options.Zone = strings.TrimSpace(a.dnsZoneOverride)
			options.View = strings.TrimSpace(a.dnsViewOverride)
			profile, err := a.loadConfig(true)
			if err != nil {
				return err
			}
			client := a.newClient(profile)
			records, err := a.runDNSSearch(profile, client, options)
			if err != nil {
				return err
			}
			if len(records) == 0 && a.isTableOutput() {
				a.PrintWarning("No records found.")
				a.printRecordTableFooter(true, len(records))
				return nil
			}
			return a.emitRecordsWithContext(records, true, recordColumns)
		},
	}
	cmd.Flags().BoolVarP(&options.CaseSensitive, "case-sensitive", "i", false, "use case-sensitive matching")
	cmd.Flags().BoolVarP(&options.Global, "global", "g", false, "search across the selected DNS view")
	cmd.Flags().BoolVarP(&options.Recursive, "recursive", "r", false, "include child authoritative zones")
	cmd.Flags().BoolVarP(&options.Fuzzy, "fuzzy", "f", false, "enable fuzzy matching")
	cmd.Flags().StringVarP(&typeFilter, "type", "t", "", "record type filter, comma-separated")
	_ = cmd.RegisterFlagCompletionFunc("type", recordTypeFlagCompletion)
	cmd.Flags().StringArrayVarP(&options.Exclude, "exclude", "e", nil, "exclude records matching keyword")
	addRecordSortFlag(cmd, &sortRaw)
	addRecordColumnsFlag(cmd, &columnsRaw)
	return cmd
}

func addRecordSortFlag(cmd *cobra.Command, target *string) {
	cmd.Flags().StringVarP(target, "sort", "s", "", "sort records by field: name, type, value, zone, ttl, or comment; prefix with - for descending")
	_ = cmd.RegisterFlagCompletionFunc("sort", recordSortFlagCompletion)
}

func addRecordColumnsFlag(cmd *cobra.Command, target *string) {
	cmd.Flags().StringVarP(target, "columns", "C", "", "record output columns, comma-separated")
	_ = cmd.RegisterFlagCompletionFunc("columns", recordColumnFlagCompletion)
}

func addZoneSortFlag(cmd *cobra.Command, target *string) {
	cmd.Flags().StringVarP(target, "sort", "s", "", "sort zones by field: zone, view, format, ns_group, or comment; prefix with - for descending")
	_ = cmd.RegisterFlagCompletionFunc("sort", zoneSortFlagCompletion)
}

func addZoneColumnsFlag(cmd *cobra.Command, target *string) {
	cmd.Flags().StringVarP(target, "columns", "C", "", "zone output columns, comma-separated")
	_ = cmd.RegisterFlagCompletionFunc("columns", zoneColumnFlagCompletion)
}

func addNetSortFlag(cmd *cobra.Command, target *string) {
	cmd.Flags().StringVarP(target, "sort", "s", "", "sort networks by field: type, network, network_view, or comment; prefix with - for descending")
	_ = cmd.RegisterFlagCompletionFunc("sort", netSortFlagCompletion)
}

func addNetworkColumnsFlag(cmd *cobra.Command, target *string) {
	cmd.Flags().StringVarP(target, "columns", "C", "", "network output columns, comma-separated")
	_ = cmd.RegisterFlagCompletionFunc("columns", networkColumnFlagCompletion)
}

func (a *App) dnsDeleteCommand() *cobra.Command {
	var skipConfirm bool
	cmd := &cobra.Command{
		Use:               "delete NAME [ZONE]",
		Short:             "Delete a DNS record by name",
		Args:              cobra.RangeArgs(1, 2),
		ValidArgsFunction: a.existingRecordArgCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			zone := ""
			if len(args) > 1 {
				zone = args[1]
			}
			return a.runDNSDelete(args[0], zone, skipConfirm)
		},
	}
	cmd.Flags().BoolVarP(&skipConfirm, "yes", "y", false, "skip delete confirmation prompt")
	return cmd
}

func (a *App) configuredClient() (Profile, *WapiClient, error) {
	profile, err := a.loadConfig(true)
	if err != nil {
		return Profile{}, nil, err
	}
	return profile, a.newClient(profile), nil
}

func (a *App) runConfigOverview() error {
	if _, err := os.Stat(a.ConfigFile); err != nil {
		if !a.isTableOutput() {
			return a.emitRows("Infoblox Profiles (0)", []string{"profile", "default", "server", "read_server", "dns_view", "default_zone"}, []map[string]any{})
		}
		a.printConfigEmptyState()
		return nil
	}
	if err := a.listProfiles(); err != nil {
		return err
	}
	a.printConfigActions()
	return nil
}

func (a *App) listProfiles() error {
	if _, err := os.Stat(a.ConfigFile); err != nil {
		if a.isTableOutput() {
			a.PrintWarning("No profiles configured. Run: ib config new [PROFILE]")
			return nil
		}
		return a.emitRows("Infoblox Profiles (0)", []string{"profile", "default", "server", "read_server", "dns_view", "default_zone"}, []map[string]any{})
	}
	defaultProfile, profiles, _, err := a.readConfigProfiles(false)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(profiles))
	for name := range profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	rows := make([]map[string]any, 0, len(names))
	for _, name := range names {
		profile := profiles[name]
		rows = append(rows, map[string]any{
			"profile":      name,
			"default":      name == defaultProfile,
			"server":       profile.Server,
			"read_server":  profile.ReadServer,
			"dns_view":     profile.DNSView,
			"default_zone": profile.DefaultZone,
		})
	}
	return a.emitRows(fmt.Sprintf("Infoblox Profiles (%d)", len(rows)), []string{"profile", "default", "server", "read_server", "dns_view", "default_zone"}, rows)
}

func (a *App) useProfile(profileName string) error {
	selected, err := normalizeProfileName(profileName)
	if err != nil {
		return err
	}
	_, profiles, _, err := a.readConfigProfiles(false)
	if err != nil {
		return err
	}
	if _, ok := profiles[selected]; !ok {
		return cliError("profile %q does not exist", selected)
	}
	if err := a.writeConfigProfiles(selected, profiles); err != nil {
		return err
	}
	if !a.isTableOutput() {
		return a.emitObject("Action", []string{"status", "action", "profile", "default", "message"}, map[string]any{
			"status": "success", "action": "use", "profile": selected, "default": true, "message": "default profile set",
		})
	}
	a.PrintSuccess("SUCCESS: default profile set to '" + selected + "'.")
	return nil
}

func (a *App) deleteProfile(profileName string) error {
	selected, err := normalizeProfileName(profileName)
	if err != nil {
		return err
	}
	defaultProfile, profiles, _, err := a.readConfigProfiles(false)
	if err != nil {
		return err
	}
	if _, ok := profiles[selected]; !ok {
		return cliError("profile %q does not exist", selected)
	}
	if selected == defaultProfile {
		return cliError("cannot delete default profile %q. Run: ib config use OTHER_PROFILE", selected)
	}
	delete(profiles, selected)
	if err := a.writeConfigProfiles(defaultProfile, profiles); err != nil {
		return err
	}
	if err := a.clearProfileCache(selected); err != nil {
		return err
	}
	if !a.isTableOutput() {
		return a.emitObject("Action", []string{"status", "action", "profile", "default", "message"}, map[string]any{
			"status": "success", "action": "delete", "profile": selected, "default": false, "message": "profile deleted and cache cleared",
		})
	}
	a.PrintSuccess("SUCCESS: profile '" + selected + "' deleted and cache cleared.")
	return nil
}

func (a *App) saveConfigInteractive(profileName string, create bool, makeDefault bool) error {
	defaultProfile := defaultProfileName
	profiles := map[string]Profile{}
	if _, err := os.Stat(a.ConfigFile); err == nil {
		loadedDefault, loadedProfiles, _, err := a.readConfigProfiles(true)
		if err != nil {
			return err
		}
		defaultProfile, profiles = loadedDefault, loadedProfiles
	}

	selected := profileName
	a.printConfigureIntro(create, selected)
	step := 1
	detailsStartStep := step
	var err error
	if selected == "" && create {
		selected, err = a.gum.Input("Profile name", "", false)
		if err != nil {
			return err
		}
	}
	if selected == "" {
		selected = defaultProfile
	}
	selected, err = normalizeProfileName(selected)
	if err != nil {
		return err
	}
	if create {
		if _, ok := profiles[selected]; ok {
			return cliError("profile %q already exists", selected)
		}
	} else if len(profiles) > 0 {
		if _, ok := profiles[selected]; !ok {
			return cliError("profile %q does not exist", selected)
		}
	}

	for {
		// Do not write partial profile updates when validation fails. The user can
		// retry the same flow until the primary connection and selected defaults
		// all validate successfully.
		err := a.saveConfigInteractiveDetails(selected, defaultProfile, profiles, makeDefault, detailsStartStep)
		if err == nil {
			return nil
		}
		if configInputCanceled(err) {
			return err
		}
		if retry := a.promptConfigRetry(err); !retry {
			return err
		}
	}
}

func (a *App) saveConfigInteractiveDetails(selected string, defaultProfile string, profiles map[string]Profile, makeDefault bool, startStep int) error {
	step := startStep
	current := profiles[selected].complete()
	a.printConfigureStep(step, "Infoblox Endpoint", "Enter the Grid Master URL; the WAPI suffix is normalized automatically.")
	step++
	server, err := a.gum.Input("Infoblox server", current.Server, false)
	if err != nil {
		return err
	}
	server, err = normalizeServer(server)
	if err != nil {
		return err
	}
	a.printConfigureStep(step, "Credentials", "Username and password are required; the password is encrypted before it is written.")
	step++
	username, err := a.gum.Input("Username", current.Username, false)
	if err != nil {
		return err
	}
	passwordLabel := "Password"
	allowPasswordBlank := current.Password != ""
	if allowPasswordBlank {
		passwordLabel = "Password (leave blank to keep current)"
	}
	password, err := a.gum.Input(passwordLabel, "", true)
	if err != nil {
		return err
	}
	if password == "" && allowPasswordBlank {
		password = current.Password
	}
	if username == "" || password == "" {
		return cliError("username and password are required")
	}
	a.printConfigureStep(step, "WAPI and TLS", "Confirm the WAPI version and certificate verification before testing the connection.")
	step++
	wapiVersion, err := a.gum.Input("WAPI version", firstNonEmpty(current.WAPIVersion, defaultWAPIVersion), false)
	if err != nil {
		return err
	}
	verifySSL, err := a.gum.Confirm("Verify SSL certificates", current.VerifySSL)
	if err != nil {
		return err
	}
	probe := Profile{
		Name:        selected,
		Server:      server,
		Username:    username,
		Password:    password,
		WAPIVersion: wapiVersion,
		DNSView:     firstNonEmpty(current.DNSView, "default"),
		VerifySSL:   verifySSL,
		Timeout:     firstNonZero(current.Timeout, defaultTimeoutSeconds),
	}.complete()
	a.printConfigureStep(step, "Connection Test", "The profile is tested before any changes are saved.")
	step++
	a.printConfigureNote("Checking Infoblox credentials and WAPI access...")
	if err := a.withSpinner("Testing Infoblox connection...", func() error {
		return a.testConnection(probe)
	}); err != nil {
		return err
	}
	a.printConfigureSuccess("SUCCESS: Infoblox connection test passed.")

	a.printConfigureStep(step, "Read Endpoint", "Automatically test Grid Master Candidates for read-only GET routing.")
	step++
	// read_server is saved only after a direct read-only GET probe succeeds.
	// Otherwise leaving it blank keeps every request on the primary server.
	readServer, _ := a.promptReadServer(probe, current.ReadServer)
	probe.ReadServer = readServer
	a.printConfigureStep(step, "DNS View", "Pick the default DNS view for DNS commands.")
	step++
	dnsView := current.DNSView
	if dnsView == "" {
		dnsView = "default"
	}
	if viewNames, err := queryViewNames(a.newClient(probe)); err == nil && len(viewNames) > 0 {
		if len(viewNames) == 1 {
			dnsView = viewNames[0]
			a.printConfigureInfo("INFO: only one DNS view found; using " + dnsView + ".")
		} else {
			selectedView, err := a.gum.ListFilter("Default DNS View", viewNames, dnsView, "view")
			if err != nil {
				return err
			}
			dnsView = selectedView
		}
	} else {
		dnsView, err = a.gum.Input("Default DNS View", dnsView, false)
		if err != nil {
			return err
		}
	}
	probe.DNSView = dnsView

	a.printConfigureStep(step, "Default DNS Zone", "Pick the default forward primary zone for DNS commands.")
	step++
	defaultZone := a.promptDefaultZone(probe, current.DefaultZone)

	savedProfile := Profile{
		Name:        selected,
		Server:      server,
		ReadServer:  readServer,
		Username:    username,
		Password:    password,
		WAPIVersion: wapiVersion,
		DNSView:     dnsView,
		DefaultZone: defaultZone,
		VerifySSL:   verifySSL,
		Timeout:     probe.Timeout,
	}.complete()
	profiles[selected] = savedProfile
	if makeDefault || len(profiles) == 1 {
		defaultProfile = selected
	}
	if err := a.writeConfigProfiles(defaultProfile, profiles); err != nil {
		return err
	}
	isDefault := defaultProfile == selected
	if a.isTableOutput() {
		a.printConfigureSummary(savedProfile, isDefault)
	} else if isDefault {
		a.PrintSuccess("SUCCESS: profile '" + selected + "' saved and set as default.")
	} else {
		a.PrintSuccess("SUCCESS: profile '" + selected + "' saved.")
	}
	return nil
}

func (a *App) promptConfigRetry(err error) bool {
	if isConnectionTestFailure(err) {
		return a.promptConnectionTestRetry(err)
	}
	a.PrintWarning("Configuration failed: " + configRetryErrorMessage(err))
	retry, retryErr := a.gum.Confirm("Try entering the details again", true)
	if retryErr != nil {
		return false
	}
	return retry
}

func configRetryErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimPrefix(err.Error(), "ERROR: ")
}

func configInputCanceled(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "input canceled")
}

type connectionTestError struct {
	err error
}

func (e *connectionTestError) Error() string {
	return "Infoblox connection test failed: " + e.err.Error()
}

func (e *connectionTestError) Unwrap() error {
	return e.err
}

func isConnectionTestFailure(err error) bool {
	var target *connectionTestError
	return errors.As(err, &target)
}

func firstNonZero(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func (a *App) testConnection(profile Profile) error {
	client := a.newClient(profile)
	params := url.Values{"_return_fields": []string{"name"}, "_max_results": []string{"1"}}
	_, err := client.Request(http.MethodGet, gridObject, params, nil)
	if err != nil {
		return &connectionTestError{err: err}
	}
	return nil
}

func (a *App) promptReadServer(profile Profile, _ string) (string, bool) {
	candidates, disabled, err := gcmReadServers(a.newClient(profile))
	if err != nil {
		a.printConfigureInfo("INFO: could not discover Grid Master Candidates; read queries will use the primary server: " + err.Error())
		return "", true
	}
	if len(disabled) > 0 {
		for _, host := range disabled {
			a.printConfigureInfo("INFO: Grid Master Candidate " + host + " has Read-Only API disabled and will not be used.")
		}
	}
	if len(candidates) == 0 {
		a.printConfigureInfo("INFO: no usable Grid Master Candidate found; read queries will use the primary server.")
		return "", true
	}
	declined := false
	for _, candidate := range candidates {
		if err := a.testReadServer(profile, candidate); err != nil {
			a.printConfigureInfo("INFO: Grid Master Candidate " + candidate + " failed read-only API probe and will not be used: " + err.Error())
			continue
		}
		useCandidate, err := a.gum.Confirm("Use "+candidate+" for read-only DNS queries?", true)
		if err != nil {
			return "", true
		}
		if !useCandidate {
			declined = true
			a.printConfigureInfo("INFO: Grid Master Candidate " + candidate + " was not selected; checking the next candidate.")
			continue
		}
		a.printConfigureInfo("INFO: read-only GET requests will use Grid Master Candidate " + candidate + ".")
		return candidate, true
	}
	if declined {
		a.printConfigureInfo("INFO: no Grid Master Candidate was selected; read queries will use the primary server.")
		return "", true
	}
	a.printConfigureInfo("INFO: no Grid Master Candidate passed read-only API probe; read queries will use the primary server.")
	return "", true
}

func (a *App) testReadServer(profile Profile, readServer string) error {
	probe := profile
	probe.ReadServer = readServer
	client := a.newClient(probe)
	params := url.Values{"_return_fields": []string{"name"}, "_max_results": []string{"1"}}
	_, err := client.Request(http.MethodGet, gridObject, params, nil)
	return err
}

func gcmReadServers(client *WapiClient) ([]string, []string, error) {
	params := url.Values{"master_candidate": []string{"true"}, "_return_fields": []string{"host_name,master_candidate,enable_ro_api_access"}}
	results, err := pagedQuery(client, memberObject, params)
	if err != nil {
		return nil, nil, err
	}
	var candidates []string
	var disabled []string
	for _, result := range results {
		host := cleanString(result["host_name"])
		if host == "" || !parseBool(cleanString(result["master_candidate"]), false) {
			continue
		}
		if !parseBool(cleanString(result["enable_ro_api_access"]), false) {
			disabled = append(disabled, host)
			continue
		}
		server, err := normalizeServer(host)
		if err == nil {
			candidates = append(candidates, server)
		}
	}
	sort.Strings(candidates)
	sort.Strings(disabled)
	return candidates, disabled, nil
}

func (a *App) promptDefaultZone(profile Profile, current string) string {
	client := a.newClient(profile)
	zones, err := queryZones(client, "")
	if err != nil {
		a.printConfigureWarning("WARNING: could not load DNS zones from Infoblox: " + err.Error())
		zone, _ := a.gum.Input("Default DNS zone (optional)", current, false)
		return strings.TrimRight(zone, ".")
	}
	var choices []string
	for _, zone := range zones {
		if isForwardZone(zone) && !isSecondaryZone(zone) {
			choices = append(choices, cleanString(zone["fqdn"]))
		}
	}
	if len(choices) == 0 {
		zone, _ := a.gum.Input("Default DNS zone (optional)", current, false)
		return strings.TrimRight(zone, ".")
	}
	if len(choices) == 1 {
		a.printConfigureInfo("INFO: only one DNS zone found; using " + choices[0] + ".")
		return strings.TrimRight(choices[0], ".")
	}
	selected, err := a.gum.ZoneFilter("Default DNS Zone", choices, current)
	if err != nil {
		return current
	}
	return strings.TrimRight(selected, ".")
}

func queryViewNames(client *WapiClient) ([]string, error) {
	params := url.Values{"_return_fields": []string{viewReturnFields}}
	results, err := pagedQuery(client, viewObject, params)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var names []string
	for _, result := range results {
		name := cleanString(result["name"])
		if name != "" && !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}

func matchChoice(requested string, choices []string, label string) (string, error) {
	requested = strings.TrimSpace(requested)
	for _, choice := range choices {
		if choice == requested {
			return choice, nil
		}
	}
	var matches []string
	for _, choice := range choices {
		if strings.EqualFold(choice, requested) {
			matches = append(matches, choice)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	return "", cliError("%s %q was not found", label, requested)
}

func (a *App) runZoneCreate(zoneName, zoneFormat, comment, nsGroup string) error {
	profile, client, err := a.configuredClient()
	if err != nil {
		return err
	}
	zone, err := normalizeZoneName(zoneName)
	if err != nil {
		return err
	}
	zoneFormat = strings.ToUpper(strings.TrimSpace(zoneFormat))
	if zoneFormat == "" {
		zoneFormat = "FORWARD"
	}
	if !containsString([]string{"FORWARD", "IPV4", "IPV6"}, zoneFormat) {
		return cliError("zone format must be FORWARD, IPV4, or IPV6")
	}
	payload := map[string]any{"fqdn": zone, "view": client.View, "zone_format": zoneFormat}
	if comment != "" {
		payload["comment"] = comment
	}
	if nsGroup != "" {
		payload["ns_group"] = nsGroup
	}
	ref, err := client.Request(http.MethodPost, zoneObject, nil, payload)
	if err != nil {
		return err
	}
	a.queueZoneCacheRefresh(profile)
	a.queueRecordCacheRefreshAfterWrite(profile, zone)
	if !a.isTableOutput() {
		row := actionRow("create", "ZONE", zone, zone, client.View, "created DNS zone")
		row["ref"] = ref
		return a.emitObject("Action", []string{"status", "action", "type", "name", "zone", "view", "message"}, row)
	}
	a.PrintSuccess("SUCCESS: created DNS zone " + zone)
	a.PrintNote("Reference: " + fmt.Sprint(ref))
	return nil
}

func (a *App) runZoneInfo(zoneName string) error {
	_, client, err := a.configuredClient()
	if err != nil {
		return err
	}
	var matches []map[string]any
	if err := a.withSpinner("Loading DNS zone info...", func() error {
		var findErr error
		matches, findErr = findZone(client, zoneName, zoneDetailFields)
		return findErr
	}); err != nil {
		return err
	}
	if len(matches) == 0 {
		return cliError("no DNS zone found for %s in view %s", zoneName, client.View)
	}
	if len(matches) > 1 {
		return cliError("multiple zones found for %s; target is ambiguous", zoneName)
	}
	zone := matches[0]
	fields := []string{"zone", "view", "format", "ns_group", "network_view", "serial_number", "soa_mname", "soa_rname", "refresh", "retry", "expiry", "negative_caching_ttl", "comment"}
	row := map[string]any{
		"zone":         cleanString(zone["fqdn"]),
		"view":         cleanString(zone["view"]),
		"format":       cleanString(zone["zone_format"]),
		"ns_group":     cleanString(zone["ns_group"]),
		"network_view": cleanString(zone["network_view"]),
		// Infoblox may encode SOA serials as JSON numbers, which Go can print
		// in scientific notation. Keep zone info operator output integer-like.
		"serial_number":        cleanIntegerString(zone["soa_serial_number"]),
		"soa_mname":            stringify(zone["member_soa_mnames"]),
		"soa_rname":            cleanString(zone["soa_email"]),
		"refresh":              cleanIntegerString(zone["soa_refresh"]),
		"retry":                cleanIntegerString(zone["soa_retry"]),
		"expiry":               cleanIntegerString(zone["soa_expire"]),
		"negative_caching_ttl": cleanIntegerString(zone["soa_negative_ttl"]),
		"comment":              cleanString(zone["comment"]),
	}
	if a.isTableOutput() {
		a.PrintContext()
		fmt.Fprintln(a.Stdout, renderTable("DNS Zone: "+cleanString(zone["fqdn"]), []string{"Field", "Value"}, objectDetailRows(fields, row)))
		return nil
	}
	return a.emitObject("DNS Zone: "+cleanString(zone["fqdn"]), fields, row)
}

func objectDetailRows(fields []string, row map[string]any) [][]string {
	labels := titleCaseFields(fields)
	rows := make([][]string, 0, len(fields))
	for i, field := range fields {
		value := stringify(row[field])
		if zoneInfoDurationField(field) {
			value = formatSecondsWithHumanDuration(value)
		}
		rows = append(rows, []string{labels[i], value})
	}
	return rows
}

func zoneInfoDurationField(field string) bool {
	switch field {
	case "refresh", "retry", "expiry", "negative_caching_ttl":
		return true
	default:
		return false
	}
}

func formatSecondsWithHumanDuration(value string) string {
	seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return value
	}
	return fmt.Sprintf("%d ( %s )", seconds, humanDuration(seconds))
}

func cleanIntegerString(value any) string {
	switch typed := value.(type) {
	case int:
		return strconv.FormatInt(int64(typed), 10)
	case int64:
		return strconv.FormatInt(typed, 10)
	case int32:
		return strconv.FormatInt(int64(typed), 10)
	case float64:
		return strconv.FormatInt(int64(typed), 10)
	case float32:
		return strconv.FormatInt(int64(typed), 10)
	}
	text := cleanString(value)
	if text == "" {
		return ""
	}
	if parsed, err := strconv.ParseFloat(text, 64); err == nil {
		return strconv.FormatInt(int64(parsed), 10)
	}
	return text
}

func humanDuration(seconds int64) string {
	if seconds == 0 {
		return "0 seconds"
	}
	units := []struct {
		name    string
		seconds int64
	}{
		{"day", 86400},
		{"hour", 3600},
		{"minute", 60},
		{"second", 1},
	}
	remaining := seconds
	parts := []string{}
	for _, unit := range units {
		count := remaining / unit.seconds
		if count == 0 {
			continue
		}
		parts = append(parts, pluralizeDurationUnit(count, unit.name))
		remaining %= unit.seconds
	}
	return strings.Join(parts, " ")
}

func pluralizeDurationUnit(count int64, unit string) string {
	if count == 1 {
		return fmt.Sprintf("%d %s", count, unit)
	}
	return fmt.Sprintf("%d %ss", count, unit)
}

func (a *App) runZoneDelete(zoneName string) error {
	profile, client, err := a.configuredClient()
	if err != nil {
		return err
	}
	target, err := normalizeZoneName(zoneName)
	if err != nil {
		return err
	}
	matches, err := findZone(client, target, zoneReturnFields)
	if err != nil {
		return err
	}
	if len(matches) == 0 {
		return cliError("no DNS zone found for %s in view %s", target, client.View)
	}
	if len(matches) > 1 {
		return cliError("multiple zones found for %s; delete target is ambiguous", target)
	}
	ref := cleanString(matches[0]["_ref"])
	if ref == "" {
		return cliError("matched zone does not include an _ref")
	}
	if _, err := client.Request(http.MethodDelete, ref, nil, nil); err != nil {
		return err
	}
	a.queueZoneCacheRefresh(profile)
	a.invalidateRecordCache(profile, target)
	if !a.isTableOutput() {
		return a.emitObject("Action", []string{"status", "action", "type", "name", "zone", "view", "message"}, actionRow("delete", "ZONE", target, target, client.View, "deleted DNS zone"))
	}
	a.PrintSuccess("SUCCESS: deleted DNS zone " + target)
	return nil
}

func (a *App) runDNSCreate(recordType, name, value, zone string, ttl int, noptr bool, comment string) error {
	profile, client, err := a.configuredClient()
	if err != nil {
		return err
	}
	recordType = strings.ToLower(recordType)
	if recordType == "ptr" {
		return a.runDNSCreatePTR(profile, client, name, value, zone, ttl, noptr, comment)
	}
	resolvedZone, err := a.resolveCreateZone(profile, client, recordType, name, zone)
	if err != nil {
		return err
	}
	warnValue := value
	if recordType == "cname" {
		warnValue, err = cnameTargetValue(value, resolvedZone)
		if err != nil {
			return err
		}
	}
	warnIfCNAMEUnresolved(a, recordType, warnValue)
	objectType, payload, err := createPayload(recordType, value, name, resolvedZone, ttl, comment, client)
	if err != nil {
		return err
	}
	var ptrAddress netip.Addr
	syncPTR := ptrManagedRecordType(recordType) && !noptr
	if syncPTR {
		ptrAddress, err = managedPTRAddressFromValue(recordType, value)
		if err != nil {
			return err
		}
	}
	if _, err := client.Request(http.MethodPost, objectType, nil, payload); err != nil {
		return err
	}
	a.queueRecordCacheRefreshAfterWrite(profile, resolvedZone)
	if noptr && recordType != "a" && recordType != "aaaa" {
		a.PrintWarning("WARNING: --noptr only applies to A/AAAA workflows and was ignored.")
	}
	targetName := cleanString(payload["name"])
	if targetName == "" {
		targetName = name
	}
	if syncPTR {
		if _, err := a.syncPTRForAddress(profile, client, ptrAddress, targetName, ttl, comment); err != nil {
			return cliError("created %s record %s, but PTR sync failed: %v", strings.ToUpper(recordType), targetName, err)
		}
	}
	if !a.isTableOutput() {
		return a.emitObject("Action", []string{"status", "action", "type", "name", "zone", "view", "message"}, actionRow("create", strings.ToUpper(recordType), targetName, resolvedZone, client.View, "created DNS record"))
	}
	a.PrintSuccess("SUCCESS: created " + strings.ToUpper(recordType) + " record")
	return nil
}

func (a *App) runDNSCreatePTR(profile Profile, client *WapiClient, name, value, zone string, ttl int, noptr bool, comment string) error {
	address, err := netip.ParseAddr(strings.TrimSpace(name))
	if err != nil {
		return cliError("full IP address is required. Use: ib dns create <ip-address> ptr <ptr-target>")
	}
	reverseZone := ""
	if zone != "" {
		reverseZone, err = normalizeZoneName(zone)
		if err != nil {
			return err
		}
	} else {
		reverseZone, err = reverseZoneForIP(client, address)
		if err != nil {
			return err
		}
	}
	objectType, payload, err := createPayload("ptr", value, address.String(), reverseZone, ttl, comment, client)
	if err != nil {
		return err
	}
	if _, err := client.Request(http.MethodPost, objectType, nil, payload); err != nil {
		return err
	}
	a.queueRecordCacheRefreshAfterWrite(profile, reverseZone)
	if noptr {
		a.PrintWarning("WARNING: --noptr only applies to A/AAAA workflows and was ignored.")
	}
	if !a.isTableOutput() {
		return a.emitObject("Action", []string{"status", "action", "type", "name", "zone", "view", "message"}, actionRow("create", "PTR", address.String(), reverseZone, client.View, "created PTR record"))
	}
	a.PrintSuccess("SUCCESS: created PTR record")
	return nil
}

func (a *App) runDNSEdit(recordNameValue, requestedType string, value *string, zone string, ttl int, noptr bool, comment string) error {
	if value != nil && requestedType == "" {
		return cliError("record type is required when updating the record value")
	}
	profile, client, err := a.configuredClient()
	if err != nil {
		return err
	}
	target, matches, allMatches, err := a.findForwardRecords(profile, client, recordNameValue, zone)
	if err != nil {
		return err
	}
	if requestedType == "ptr" || (requestedType == "" && len(matches) == 0) {
		if address, parseErr := netip.ParseAddr(strings.TrimSpace(recordNameValue)); parseErr == nil {
			ptrTarget, ptrMatches, err := ptrMatches(client, address)
			if err != nil {
				return err
			}
			target, matches, allMatches = ptrTarget, ptrMatches, ptrMatches
		}
	}
	if requestedType != "" {
		filtered := matches[:0]
		for _, match := range matches {
			if match.Type == requestedType {
				filtered = append(filtered, match)
			}
		}
		matches = filtered
	}
	if len(matches) == 0 {
		if requestedType != "" && len(allMatches) > 0 {
			return cliError("%s has existing record types %s; requested edit type was %s", target, recordTypeList(allMatches), strings.ToUpper(requestedType))
		}
		return cliError("no DNS record found for %s in view %s", target, client.View)
	}
	if len(matches) > 1 {
		return cliError("multiple records found for %s. Edit one _ref manually", target)
	}
	record := matches[0]
	ref, err := recordRef(record)
	if err != nil {
		return err
	}
	updateZone := ""
	if value != nil {
		warnValue := *value
		if strings.EqualFold(record.Type, "cname") {
			if cnameTargetNeedsZone(*value) {
				updateZone, err = a.resolveDNSZone(profile, zone)
				if err != nil {
					return err
				}
			}
			warnValue, err = cnameTargetValue(*value, updateZone)
			if err != nil {
				return err
			}
		}
		warnIfCNAMEUnresolved(a, record.Type, warnValue)
	}
	payload, err := updatePayload(record.Type, value, updateZone, ttl, comment)
	if err != nil {
		return err
	}
	var ptrAddress netip.Addr
	var oldPTRAddress netip.Addr
	oldPTRAddressChanged := false
	ptrName := target
	syncPTR := ptrManagedRecordType(record.Type) && !noptr
	if syncPTR {
		ptrAddress, err = managedPTRAddress(record.Type, value, record.Item)
		if err != nil {
			return err
		}
		oldPTRAddress, oldPTRAddressChanged, err = changedManagedPTRAddress(record.Type, value, record.Item)
		if err != nil {
			return err
		}
		if name := recordName(record.Item, record.Type); name != "" {
			ptrName = name
		}
	}
	if _, err := client.Request(http.MethodPut, ref, nil, payload); err != nil {
		return err
	}
	a.queueRecordCacheRefreshAfterWrite(profile, cleanString(record.Item["zone"]))
	if noptr && record.Type != "a" && record.Type != "aaaa" {
		a.PrintWarning("WARNING: --noptr only applies to A/AAAA workflows and was ignored.")
	}
	if syncPTR {
		if _, err := a.syncPTRForAddress(profile, client, ptrAddress, ptrName, ttl, comment); err != nil {
			return cliError("updated %s record %s, but PTR sync failed: %v", strings.ToUpper(record.Type), target, err)
		}
		if oldPTRAddressChanged {
			if _, err := a.deleteManagedPTRForAddress(profile, client, oldPTRAddress, ptrName); err != nil {
				return cliError("updated %s record %s and synced PTR for %s, but old PTR cleanup failed: %v", strings.ToUpper(record.Type), target, ptrAddress, err)
			}
		}
	}
	if !a.isTableOutput() {
		return a.emitObject("Action", []string{"status", "action", "type", "name", "zone", "view", "message"}, actionRow("edit", strings.ToUpper(record.Type), target, cleanString(record.Item["zone"]), client.View, "updated DNS record"))
	}
	a.PrintSuccess("SUCCESS: updated " + strings.ToUpper(record.Type) + " record " + target)
	return nil
}

func ptrManagedRecordType(recordType string) bool {
	switch strings.ToLower(recordType) {
	case "a", "aaaa":
		return true
	default:
		return false
	}
}

func managedPTRAddress(recordType string, value *string, item map[string]any) (netip.Addr, error) {
	if value != nil {
		return managedPTRAddressFromValue(recordType, *value)
	}
	return managedPTRAddressFromValue(recordType, recordValue(recordType, item))
}

func changedManagedPTRAddress(recordType string, value *string, item map[string]any) (netip.Addr, bool, error) {
	if value == nil {
		return netip.Addr{}, false, nil
	}
	oldAddress, err := managedPTRAddress(recordType, nil, item)
	if err != nil {
		return netip.Addr{}, false, err
	}
	newAddress, err := managedPTRAddressFromValue(recordType, *value)
	if err != nil {
		return netip.Addr{}, false, err
	}
	if oldAddress == newAddress {
		return netip.Addr{}, false, nil
	}
	return oldAddress, true, nil
}

func managedPTRAddressFromValue(recordType, value string) (netip.Addr, error) {
	address, err := netip.ParseAddr(strings.TrimSpace(value))
	if err != nil {
		return netip.Addr{}, cliError("%s value must be an IP address before PTR can be managed", strings.ToUpper(recordType))
	}
	recordType = strings.ToLower(recordType)
	if recordType == "a" && !address.Is4() {
		return netip.Addr{}, cliError("A value must be an IPv4 address")
	}
	if recordType == "aaaa" && address.Is4() {
		return netip.Addr{}, cliError("AAAA value must be an IPv6 address")
	}
	return address, nil
}

func (a *App) syncPTRForAddress(profile Profile, client *WapiClient, address netip.Addr, ptrdname string, ttl int, comment string) (string, error) {
	ptrdname = cleanDNSName(ptrdname)
	if ptrdname == "" {
		return "", cliError("PTR target name is required")
	}
	lookupClient := primaryReadClient(client)
	reverseZone, err := reverseZoneForIP(lookupClient, address)
	if err != nil {
		return "", err
	}
	matches, err := ptrMatchesInZone(lookupClient, address, reverseZone)
	if err != nil {
		return "", err
	}
	if len(matches) > 1 {
		return "", cliError("multiple PTR records found for %s in reverse zone %s. Update one _ref manually", address, reverseZone)
	}
	if len(matches) == 0 {
		objectType, payload, err := createPayload("ptr", ptrdname, address.String(), reverseZone, ttl, comment, client)
		if err != nil {
			return "", err
		}
		if _, err := client.Request(http.MethodPost, objectType, nil, payload); err != nil {
			return "", err
		}
		a.queueRecordCacheRefreshAfterWrite(profile, reverseZone)
		return reverseZone, nil
	}
	ref, err := recordRef(matches[0])
	if err != nil {
		return "", err
	}
	payload, err := updatePayload("ptr", &ptrdname, "", ttl, comment)
	if err != nil {
		return "", err
	}
	if _, err := client.Request(http.MethodPut, ref, nil, payload); err != nil {
		return "", err
	}
	a.queueRecordCacheRefreshAfterWrite(profile, reverseZone)
	return reverseZone, nil
}

func (a *App) deleteManagedPTRForAddress(profile Profile, client *WapiClient, address netip.Addr, ptrdname string) (string, error) {
	ptrdname = cleanDNSName(ptrdname)
	if ptrdname == "" {
		return "", cliError("old PTR target name is required")
	}
	lookupClient := primaryReadClient(client)
	reverseZone, err := reverseZoneForIP(lookupClient, address)
	if err != nil {
		return "", err
	}
	matches, err := ptrMatchesInZone(lookupClient, address, reverseZone)
	if err != nil {
		return "", err
	}
	var targetMatches []TypedRecord
	for _, match := range matches {
		if strings.EqualFold(cleanDNSName(recordValue("ptr", match.Item)), ptrdname) {
			targetMatches = append(targetMatches, match)
		}
	}
	if len(targetMatches) == 0 {
		a.queueRecordCacheRefreshAfterWrite(profile, reverseZone)
		return reverseZone, nil
	}
	if len(targetMatches) > 1 {
		return "", cliError("multiple old PTR records found for %s in reverse zone %s. Delete one _ref manually", address, reverseZone)
	}
	ref, err := recordRef(targetMatches[0])
	if err != nil {
		return "", err
	}
	if _, err := client.Request(http.MethodDelete, ref, nil, nil); err != nil {
		return "", err
	}
	a.queueRecordCacheRefreshAfterWrite(profile, reverseZone)
	return reverseZone, nil
}

func primaryReadClient(client *WapiClient) *WapiClient {
	cloned := *client
	cloned.ReadServer = client.Server
	return &cloned
}

func recordTypeList(records []TypedRecord) string {
	seen := map[string]bool{}
	var types []string
	for _, record := range records {
		t := strings.ToUpper(record.Type)
		if !seen[t] {
			seen[t] = true
			types = append(types, t)
		}
	}
	sort.Strings(types)
	return strings.Join(types, ", ")
}

func (a *App) runDNSDelete(recordName, zone string, skipConfirm bool) error {
	if strings.EqualFold(recordName, "ptr") {
		if zone == "" {
			return cliError("full IP address is required. Use: ib dns delete ptr <ip-address>")
		}
		return a.runDNSDeletePTR(zone, skipConfirm)
	}
	profile, client, err := a.configuredClient()
	if err != nil {
		return err
	}
	target, matches, _, err := a.findForwardRecords(profile, client, recordName, zone)
	if err != nil {
		return err
	}
	if len(matches) == 0 {
		return cliError("no forward DNS record found for %s in view %s\nHINT: To delete a reverse DNS PTR entry, run: ib dns delete ptr <ip-address>", target, client.View)
	}
	if len(matches) > 1 {
		record, err := a.selectDuplicateDeleteRecord(target, matches)
		if err != nil {
			if errors.Is(err, errDeleteCancelled) {
				a.PrintInfo("INFO: delete cancelled")
				return nil
			}
			return err
		}
		matches = []TypedRecord{record}
	}
	record := matches[0]
	ref, err := recordRef(record)
	if err != nil {
		return err
	}
	if err := a.confirmDNSDelete(target, record, skipConfirm); err != nil {
		if errors.Is(err, errDeleteCancelled) {
			a.PrintInfo("INFO: delete cancelled")
			return nil
		}
		return err
	}
	if _, err := client.Request(http.MethodDelete, ref, nil, nil); err != nil {
		return err
	}
	a.queueRecordCacheRefreshAfterWrite(profile, cleanString(record.Item["zone"]))
	a.queueManagedPTRCacheRefreshAfterDelete(profile, client, record)
	if !a.isTableOutput() {
		return a.emitObject("Action", []string{"status", "action", "type", "name", "zone", "view", "message"}, actionRow("delete", strings.ToUpper(record.Type), target, cleanString(record.Item["zone"]), client.View, "deleted DNS record"))
	}
	a.PrintSuccess("SUCCESS: deleted " + strings.ToUpper(record.Type) + " record " + target)
	return nil
}

func (a *App) queueManagedPTRCacheRefreshAfterDelete(profile Profile, client *WapiClient, record TypedRecord) {
	if !ptrManagedRecordType(record.Type) {
		return
	}
	// Forward A/AAAA deletes can also make reverse data stale when the matching
	// PTR is managed outside this request path. Keep the reverse-zone cache on
	// the same clear-and-refresh path as direct PTR writes, but do not fail the
	// already-completed delete if the reverse zone cannot be resolved.
	address, err := managedPTRAddress(record.Type, nil, record.Item)
	if err != nil {
		return
	}
	reverseZone, err := a.reverseZoneForIPForCacheRefresh(profile, primaryReadClient(client), address)
	if err != nil {
		return
	}
	a.queueRecordCacheRefreshAfterWrite(profile, reverseZone)
}

func (a *App) selectDuplicateDeleteRecord(target string, matches []TypedRecord) (TypedRecord, error) {
	if a.dnsDeleteRecordSelector != nil {
		record, selected, err := a.dnsDeleteRecordSelector(target, matches)
		if err != nil {
			return TypedRecord{}, err
		}
		if !selected {
			return TypedRecord{}, errDeleteCancelled
		}
		return record, nil
	}
	if !a.isTableOutput() || a.gum == nil || !a.gum.interactive() {
		return TypedRecord{}, duplicateDeleteError(target, matches)
	}

	selected, err := a.huhDuplicateDeleteSelect(target, matches)
	if err != nil {
		return TypedRecord{}, err
	}
	return selected, nil
}

func (a *App) huhDuplicateDeleteSelect(target string, records []TypedRecord) (TypedRecord, error) {
	if len(records) == 0 {
		return TypedRecord{}, cliError("no duplicate records available to select")
	}
	options := duplicateDeleteOptions(records)
	selected := 0
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[int]().
				Title("Multiple records found for " + target + "; select one to delete").
				Options(options...).
				Value(&selected).
				Height(8),
		),
	).
		WithInput(a.Stdin).
		WithOutput(a.Stdout)
	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return TypedRecord{}, errDeleteCancelled
		}
		return TypedRecord{}, err
	}
	if selected < 0 || selected >= len(records) {
		return TypedRecord{}, cliError("invalid duplicate record selection")
	}
	return records[selected], nil
}

func duplicateDeleteOptions(records []TypedRecord) []huh.Option[int] {
	options := make([]huh.Option[int], 0, len(records))
	for i, record := range records {
		option := huh.NewOption(duplicateDeleteChoice(i+1, record), i)
		if i == 0 {
			option = option.Selected(true)
		}
		options = append(options, option)
	}
	return options
}

func duplicateDeleteChoice(index int, record TypedRecord) string {
	fields := duplicateDeleteFields(record)
	return fmt.Sprintf("%d. %s %s | %s | zone=%s | ref=%s%s",
		index,
		fields["type"],
		fields["name"],
		fields["value"],
		fields["zone"],
		fields["ref"],
		duplicateDeleteCommentSuffix(fields["comment"]),
	)
}

func duplicateDeleteError(target string, records []TypedRecord) error {
	var builder strings.Builder
	fmt.Fprintf(&builder, "multiple records found for %s; run in an interactive terminal to choose one:\n", target)
	for i, record := range records {
		fields := duplicateDeleteFields(record)
		fmt.Fprintf(&builder, "  %d. type=%s name=%s value=%s zone=%s ref=%s%s\n",
			i+1,
			fields["type"],
			fields["name"],
			fields["value"],
			fields["zone"],
			fields["ref"],
			duplicateDeleteCommentSuffix(fields["comment"]),
		)
	}
	return cliError("%s", strings.TrimRight(builder.String(), "\n"))
}

func duplicateDeleteFields(record TypedRecord) map[string]string {
	return map[string]string{
		"type":    displayRecordTypeLabel(record.Type),
		"name":    recordName(record.Item, record.Type),
		"value":   recordValue(record.Type, record.Item),
		"zone":    cleanString(record.Item["zone"]),
		"comment": cleanString(record.Item["comment"]),
		"ref":     cleanString(record.Item["_ref"]),
	}
}

func duplicateDeleteCommentSuffix(comment string) string {
	if strings.TrimSpace(comment) == "" {
		return ""
	}
	return " | comment=" + comment
}

func (a *App) confirmDNSDelete(target string, record TypedRecord, skipConfirm bool) error {
	if skipConfirm {
		return nil
	}
	confirmed, err := a.promptDNSDeleteConfirmation(target, record)
	if err != nil {
		return err
	}
	if !confirmed {
		return errDeleteCancelled
	}
	return nil
}

func (a *App) promptDNSDeleteConfirmation(target string, record TypedRecord) (bool, error) {
	if a.dnsDeleteConfirmer != nil {
		return a.dnsDeleteConfirmer(target, record)
	}
	if !a.isTableOutput() || a.gum == nil || !a.gum.interactive() {
		return false, cliError("delete confirmation requires an interactive terminal; rerun with -y to skip confirmation")
	}

	return a.huhDNSDeleteConfirm(target, record)
}

func (a *App) huhDNSDeleteConfirm(target string, record TypedRecord) (bool, error) {
	confirmed := false
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Delete DNS record?").
				Description(deleteConfirmationDescription(target, record)).
				Affirmative("Delete").
				Negative("Cancel").
				Value(&confirmed),
		),
	).
		WithInput(a.Stdin).
		WithOutput(a.Stdout)
	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return false, errDeleteCancelled
		}
		return false, err
	}
	return confirmed, nil
}

func deleteConfirmationDescription(target string, record TypedRecord) string {
	fields := duplicateDeleteFields(record)
	name := firstNonEmpty(fields["name"], target)
	return fmt.Sprintf("%s %s | %s | zone=%s | ref=%s%s",
		fields["type"],
		name,
		fields["value"],
		fields["zone"],
		fields["ref"],
		duplicateDeleteCommentSuffix(fields["comment"]),
	)
}

func (a *App) runDNSDeletePTR(ipValue string, skipConfirm bool) error {
	profile, client, err := a.configuredClient()
	if err != nil {
		return err
	}
	address, err := netip.ParseAddr(strings.TrimSpace(ipValue))
	if err != nil {
		return cliError("full IP address is required. Use: ib dns delete ptr <ip-address>")
	}
	reverseZone, matches, err := ptrMatches(client, address)
	if err != nil {
		return err
	}
	if len(matches) == 0 {
		return cliError("no PTR record found for %s in reverse zone %s and view %s", address, reverseZone, client.View)
	}
	if len(matches) > 1 {
		return cliError("multiple PTR records found for %s in reverse zone %s. Delete one _ref manually", address, reverseZone)
	}
	ref, err := recordRef(matches[0])
	if err != nil {
		return err
	}
	if err := a.confirmDNSDelete(address.String(), matches[0], skipConfirm); err != nil {
		if errors.Is(err, errDeleteCancelled) {
			a.PrintInfo("INFO: delete cancelled")
			return nil
		}
		return err
	}
	if _, err := client.Request(http.MethodDelete, ref, nil, nil); err != nil {
		return err
	}
	a.queueRecordCacheRefreshAfterWrite(profile, reverseZone)
	if !a.isTableOutput() {
		return a.emitObject("Action", []string{"status", "action", "type", "name", "zone", "view", "message"}, actionRow("delete", "PTR", address.String(), reverseZone, client.View, "deleted PTR record"))
	}
	a.PrintSuccess(fmt.Sprintf("SUCCESS: deleted PTR record %s from reverse zone %s", address, reverseZone))
	return nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
