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
			rows, err := a.cacheStatusRows()
			if err != nil {
				return err
			}
			if len(rows) == 0 && a.isTableOutput() {
				a.PrintWarning("No cache entries found.")
			}
			return a.emitRows("Cache Status", []string{"kind", "profile", "view", "zone", "serial", "items", "age", "stale_expires"}, rows)
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
	return cmd
}

func (a *App) completionCommand() *cobra.Command {
	return &cobra.Command{
		Use:               "completion [bash|zsh|fish]",
		Short:             "Generate shell completion or print setup instructions",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: shellNameCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				a.PrintNote("Shell completion setup:")
				fmt.Fprintln(a.Stdout, `  Bash: ib config completion bash > ~/.ib-complete.bash && printf '\n# ib shell completion\n. ~/.ib-complete.bash\n' >> ~/.bashrc`)
				fmt.Fprintln(a.Stdout, `  Zsh:  ib config completion zsh > ~/.ib-complete.zsh && printf '\n# ib shell completion\n. ~/.ib-complete.zsh\n' >> ~/.zshrc`)
				fmt.Fprintln(a.Stdout, `  Fish: ib config completion fish > ~/.config/fish/completions/ib.fish`)
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
			default:
				return cliError("unsupported shell %q; use bash, zsh, or fish", args[0])
			}
		},
	}
}

func (a *App) dnsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dns",
		Short: "Manage Infoblox DNS records",
	}
	cmd.AddCommand(a.dnsViewCommand())
	cmd.AddCommand(a.dnsZoneCommand())
	cmd.AddCommand(a.dnsCreateCommand())
	cmd.AddCommand(a.dnsEditCommand())
	cmd.AddCommand(a.dnsListCommand())
	cmd.AddCommand(a.dnsSearchCommand())
	cmd.AddCommand(a.dnsDeleteCommand())
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
	listCmd := &cobra.Command{
		Use:               "list [SEARCH]",
		Short:             "List DNS zones",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeFlagsAfterArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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
			if len(zones) == 0 && a.isTableOutput() {
				a.PrintWarning("No zones found.")
			}
			return a.emitZones(zones)
		},
	}
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
	var zone, comment string
	var ttl int
	var noptr bool
	cmd := &cobra.Command{
		Use:               "create NAME TYPE VALUE",
		Short:             "Create a DNS record",
		Args:              cobra.ExactArgs(3),
		ValidArgsFunction: createArgCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runDNSCreate(args[1], args[0], args[2], zone, ttl, noptr, comment)
		},
	}
	cmd.Flags().StringVar(&zone, "zone", "", "DNS zone; defaults to active/default zone")
	_ = cmd.RegisterFlagCompletionFunc("zone", a.zoneFlagCompletion)
	cmd.Flags().IntVarP(&ttl, "ttl", "t", -1, "optional record TTL in seconds")
	cmd.Flags().BoolVar(&noptr, "noptr", false, "do not manage PTR records for A/AAAA workflows")
	cmd.Flags().StringVarP(&comment, "comment", "c", "", "record comment")
	return cmd
}

func (a *App) dnsEditCommand() *cobra.Command {
	var zone, comment string
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
			return a.runDNSEdit(recordName, recordType, value, zone, ttl, noptr, comment)
		},
	}
	cmd.Flags().StringVar(&zone, "zone", "", "DNS zone; defaults to active/default zone")
	_ = cmd.RegisterFlagCompletionFunc("zone", a.zoneFlagCompletion)
	cmd.Flags().IntVarP(&ttl, "ttl", "t", -1, "optional record TTL in seconds")
	cmd.Flags().BoolVar(&noptr, "noptr", false, "do not manage PTR records for A/AAAA workflows")
	cmd.Flags().StringVarP(&comment, "comment", "c", "", "record comment")
	return cmd
}

func (a *App) dnsListCommand() *cobra.Command {
	var details bool
	var recursive bool
	cmd := &cobra.Command{
		Use:   "list [ZONE]",
		Short: "List DNS records in a zone",
		Args:  cobra.MaximumNArgs(1),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return flagCompletions(cmd, toComplete), cobra.ShellCompDirectiveNoFileComp
			}
			return a.zoneArgCompletion(cmd, args, toComplete)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
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
			if len(records) == 0 && a.isTableOutput() {
				scope := "zone " + target
				if recursive {
					scope += " or child zones"
				}
				a.PrintWarning("No records found in " + scope + ".")
			}
			return a.emitRecordsWithContext(records, true)
		},
	}
	cmd.Flags().BoolVar(&details, "details", false, "load per-record details such as explicit TTLs; slower for large zones")
	cmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "include child authoritative zones")
	return cmd
}

func (a *App) dnsSearchCommand() *cobra.Command {
	var options SearchOptions
	var typeFilter string
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
			options.Types = types
			profile, err := a.loadConfig(true)
			if err != nil {
				return err
			}
			if options.View != "" {
				profile.DNSView = strings.TrimSpace(options.View)
			}
			client := a.newClient(profile)
			records, err := a.runDNSSearch(profile, client, options)
			if err != nil {
				return err
			}
			if len(records) == 0 && a.isTableOutput() {
				a.PrintWarning("No records found.")
			}
			return a.emitRecordsWithContext(records, true)
		},
	}
	cmd.Flags().BoolVarP(&options.CaseSensitive, "case-sensitive", "i", false, "use case-sensitive matching")
	cmd.Flags().BoolVarP(&options.Global, "global", "g", false, "search across the selected DNS view")
	cmd.Flags().BoolVarP(&options.Recursive, "recursive", "r", false, "include child authoritative zones")
	cmd.Flags().StringVarP(&options.Zone, "zone", "z", "", "search this zone")
	_ = cmd.RegisterFlagCompletionFunc("zone", a.zoneFlagCompletion)
	cmd.Flags().StringVarP(&options.View, "view", "v", "", "search this DNS view")
	cmd.Flags().BoolVarP(&options.Fuzzy, "fuzzy", "f", false, "enable fuzzy matching")
	cmd.Flags().StringVarP(&typeFilter, "type", "t", "", "record type filter, comma-separated")
	_ = cmd.RegisterFlagCompletionFunc("type", recordTypeFlagCompletion)
	cmd.Flags().StringArrayVarP(&options.Exclude, "exclude", "e", nil, "exclude records matching keyword")
	return cmd
}

func (a *App) dnsDeleteCommand() *cobra.Command {
	return &cobra.Command{
		Use:               "delete NAME [ZONE]",
		Short:             "Delete a DNS record by name",
		Args:              cobra.RangeArgs(1, 2),
		ValidArgsFunction: a.existingRecordArgCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			zone := ""
			if len(args) > 1 {
				zone = args[1]
			}
			return a.runDNSDelete(args[0], zone)
		},
	}
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
	if !a.isTableOutput() {
		return a.emitObject("Action", []string{"status", "action", "profile", "default", "message"}, map[string]any{
			"status": "success", "action": "delete", "profile": selected, "default": false, "message": "profile deleted",
		})
	}
	a.PrintSuccess("SUCCESS: profile '" + selected + "' deleted.")
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

	a.printConfigureStep(step, "Read Endpoint", "Optionally route read-only GET requests to a Grid Master Candidate.")
	step++
	readServer := current.ReadServer
	if selectedReadServer, changed := a.promptReadServer(probe, current.ReadServer); changed {
		readServer = selectedReadServer
	}
	probe.ReadServer = readServer
	a.printConfigureStep(step, "DNS View", "Pick the default DNS view for DNS commands.")
	step++
	dnsView := current.DNSView
	if dnsView == "" {
		dnsView = "default"
	}
	if viewNames, err := queryViewNames(a.newClient(probe)); err == nil && len(viewNames) > 0 {
		selectedView, err := a.gum.ListFilter("Default DNS View", viewNames, dnsView, "view")
		if err != nil {
			return err
		}
		dnsView = selectedView
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

func (a *App) promptReadServer(profile Profile, current string) (string, bool) {
	candidates, disabled, err := gcmReadServers(a.newClient(profile))
	if err != nil {
		a.printConfigureWarning("WARNING: could not discover Grid Master Candidates: " + err.Error())
		return "", false
	}
	if len(disabled) > 0 {
		for _, host := range disabled {
			a.printConfigureWarning("WARNING: Grid Master Candidate " + host + " has Read-Only API disabled and will not be used.")
		}
		return "", true
	}
	if len(candidates) == 0 {
		return "", false
	}
	choices := append([]string{"Do not use a read endpoint"}, candidates...)
	defaultChoice := "Do not use a read endpoint"
	if current != "" {
		defaultChoice = current
	}
	selected, err := a.gum.Choose("Grid Master Candidate for WAPI GET queries", choices, defaultChoice)
	if err != nil || selected == choices[0] {
		return "", err == nil
	}
	return selected, true
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
	a.invalidateZoneCache(profile)
	a.invalidateRecordCache(profile, zone)
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
		"zone":                 cleanString(zone["fqdn"]),
		"view":                 cleanString(zone["view"]),
		"format":               cleanString(zone["zone_format"]),
		"ns_group":             cleanString(zone["ns_group"]),
		"network_view":         cleanString(zone["network_view"]),
		"serial_number":        cleanString(zone["soa_serial_number"]),
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
	a.invalidateZoneCache(profile)
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
	warnIfCNAMEUnresolved(a, recordType, value)
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
	a.invalidateRecordCache(profile, resolvedZone)
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
	a.invalidateRecordCache(profile, reverseZone)
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
	if value != nil {
		warnIfCNAMEUnresolved(a, record.Type, *value)
	}
	payload, err := updatePayload(record.Type, value, ttl, comment)
	if err != nil {
		return err
	}
	var ptrAddress netip.Addr
	ptrName := target
	syncPTR := ptrManagedRecordType(record.Type) && !noptr
	if syncPTR {
		ptrAddress, err = managedPTRAddress(record.Type, value, record.Item)
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
	a.invalidateRecordCache(profile, cleanString(record.Item["zone"]))
	if noptr && record.Type != "a" && record.Type != "aaaa" {
		a.PrintWarning("WARNING: --noptr only applies to A/AAAA workflows and was ignored.")
	}
	if syncPTR {
		if _, err := a.syncPTRForAddress(profile, client, ptrAddress, ptrName, ttl, comment); err != nil {
			return cliError("updated %s record %s, but PTR sync failed: %v", strings.ToUpper(record.Type), target, err)
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
		a.invalidateRecordCache(profile, reverseZone)
		return reverseZone, nil
	}
	ref, err := recordRef(matches[0])
	if err != nil {
		return "", err
	}
	payload, err := updatePayload("ptr", &ptrdname, ttl, comment)
	if err != nil {
		return "", err
	}
	if _, err := client.Request(http.MethodPut, ref, nil, payload); err != nil {
		return "", err
	}
	a.invalidateRecordCache(profile, reverseZone)
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

func (a *App) runDNSDelete(recordName, zone string) error {
	if strings.EqualFold(recordName, "ptr") {
		if zone == "" {
			return cliError("full IP address is required. Use: ib dns delete ptr <ip-address>")
		}
		return a.runDNSDeletePTR(zone)
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
		return cliError("multiple records found for %s. Delete one _ref manually", target)
	}
	record := matches[0]
	ref, err := recordRef(record)
	if err != nil {
		return err
	}
	if _, err := client.Request(http.MethodDelete, ref, nil, nil); err != nil {
		return err
	}
	a.invalidateRecordCache(profile, cleanString(record.Item["zone"]))
	if !a.isTableOutput() {
		return a.emitObject("Action", []string{"status", "action", "type", "name", "zone", "view", "message"}, actionRow("delete", strings.ToUpper(record.Type), target, cleanString(record.Item["zone"]), client.View, "deleted DNS record"))
	}
	a.PrintSuccess("SUCCESS: deleted " + strings.ToUpper(record.Type) + " record " + target)
	return nil
}

func (a *App) runDNSDeletePTR(ipValue string) error {
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
	if _, err := client.Request(http.MethodDelete, ref, nil, nil); err != nil {
		return err
	}
	a.invalidateRecordCache(profile, reverseZone)
	if !a.isTableOutput() {
		return a.emitObject("Action", []string{"status", "action", "type", "name", "zone", "view", "message"}, actionRow("delete", "PTR", address.String(), reverseZone, client.View, "deleted PTR record"))
	}
	a.PrintSuccess(fmt.Sprintf("SUCCESS: deleted PTR record %s from reverse zone %s", address, reverseZone))
	return nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
