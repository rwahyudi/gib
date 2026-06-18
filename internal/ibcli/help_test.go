package ibcli

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestRootHelpUsesModules(t *testing.T) {
	app := testApp(t)
	var stdout bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &bytes.Buffer{}
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"--help"}); err != nil {
		t.Fatalf("help: %v", err)
	}
	output := stdout.String()
	for _, want := range []string{
		"ib <module>",
		"Modules",
		"config  Manage Infoblox configuration",
		"dns     Manage Infoblox DNS records",
		"net     Manage Infoblox IPAM networks",
		"--debug",
		`Use "ib <module> --help" for more detail.`,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("root help output missing %q:\n%s", want, output)
		}
	}
	for _, unwanted := range []string{
		"ib <command>",
		"completion  Generate shell completion",
		`Use "ib <command> --help" for more detail.`,
	} {
		if strings.Contains(output, unwanted) {
			t.Fatalf("root help output contains old wording %q:\n%s", unwanted, output)
		}
	}
}

func TestRootWithoutArgsPrintsUsageThenCurrentContext(t *testing.T) {
	app := testApp(t)
	writePlainTestConfig(t, app.ConfigFile, "demo", map[string]Profile{
		"demo": plainTestProfile("demo", "https://infoblox.example"),
	}, "")
	var stdout bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &bytes.Buffer{}
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{}); err != nil {
		t.Fatalf("root command: %v", err)
	}
	output := stdout.String()
	for _, want := range []string{"Usage", "ib [flags]", "Modules", "config  Manage Infoblox configuration", "Current Context:", "Profile:", "demo", "View:", "default", "Zone:", "demo.example"} {
		if !strings.Contains(output, want) {
			t.Fatalf("root output missing %q:\n%s", want, output)
		}
	}
	contextIndex := strings.LastIndex(output, "Current Context:")
	usageIndex := strings.Index(output, "Usage")
	if contextIndex < 0 || usageIndex < 0 || contextIndex < usageIndex {
		t.Fatalf("root output should print current context after usage:\n%s", output)
	}
	if !strings.HasSuffix(strings.TrimSpace(output), "Current Context: Profile: demo | View: default | Zone: demo.example (configured default)") {
		t.Fatalf("root output should end with current context:\n%s", output)
	}
}

func TestRootVersionFlagPrintsVersionAndBuildDate(t *testing.T) {
	oldVersion, oldBuildDate := Version, BuildDate
	Version = "1.2.3"
	BuildDate = "2026-05-23T10:20:30Z"
	t.Cleanup(func() {
		Version = oldVersion
		BuildDate = oldBuildDate
	})
	app := testApp(t)
	var stdout bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &bytes.Buffer{}
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"-v"}); err != nil {
		t.Fatalf("version flag: %v", err)
	}
	output := stdout.String()
	if want := "ib 1.2.3 (built 2026-05-23T20:20:30+10:00 AEST)"; !strings.Contains(output, want) {
		t.Fatalf("version output missing %q:\n%s", want, output)
	}
	if strings.Contains(output, "Current Context:") {
		t.Fatalf("version output should not print current context:\n%s", output)
	}
}

func TestFormatBuildDateAESTHandlesCommonReleaseFormats(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "rfc3339 utc", raw: "2026-05-23T10:20:30Z", want: "2026-05-23T20:20:30+10:00 AEST"},
		{name: "rfc3339 offset", raw: "2026-05-23T10:20:30+08:00", want: "2026-05-23T12:20:30+10:00 AEST"},
		{name: "unknown format", raw: "build-time", want: "build-time"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := formatBuildDateAEST(test.raw); got != test.want {
				t.Fatalf("formatBuildDateAEST(%q) = %q, want %q", test.raw, got, test.want)
			}
		})
	}
}

func TestNetModuleHelpShowsIPAMCommands(t *testing.T) {
	app := testApp(t)
	var stdout bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &bytes.Buffer{}
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"net", "--help"}); err != nil {
		t.Fatalf("net help: %v", err)
	}
	output := stdout.String()
	for _, want := range []string{
		"ib net <command>",
		"IPAM Usage",
		"ib net list [SEARCH] lists IPv4 networks and containers",
		"CIDR matches include related parent and child networks or containers in the same view",
		"ib net next-ip NETWORK requests available IPv4 addresses",
		"Commands",
		"address",
		"Show IPAM address details",
		"next-ip",
		"Find the next available IP in a network or container",
		"view",
		"Manage IPAM network views",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("net help output missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "Current Context:") {
		t.Fatalf("net help output should not show DNS context:\n%s", output)
	}
}

func TestModuleHelpStillUsesCommands(t *testing.T) {
	app := testApp(t)
	var stdout bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &bytes.Buffer{}
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"dns", "--help"}); err != nil {
		t.Fatalf("help: %v", err)
	}
	output := stdout.String()
	for _, want := range []string{
		"ib dns <command>",
		"Context Overrides",
		"--zone/-z overrides",
		"--view/-v overrides",
		"-z, --zone STRING",
		"-v, --view STRING",
		"Commands",
		"create",
		"Create a DNS record",
		"next-ip",
		"Find the next available IP in a network",
		`Use "ib dns <command> --help" for more detail.`,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("dns help output missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "ib dns <module>") {
		t.Fatalf("dns help output used module wording for nested commands:\n%s", output)
	}
}

func TestDNSDeleteHelpShowsConfirmationSkip(t *testing.T) {
	app := testApp(t)
	var stdout bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &bytes.Buffer{}
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"dns", "delete", "--help"}); err != nil {
		t.Fatalf("dns delete help: %v", err)
	}
	output := stdout.String()
	for _, want := range []string{
		"Delete Record Usage",
		"ib dns delete <type> <record-name> [zone]",
		"ib dns delete a app",
		"prompts before deleting; use -y to skip",
		"-y, --yes",
		"skip delete confirmation prompt",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("dns delete help output missing %q:\n%s", want, output)
		}
	}
}

func TestDNSListHelpShowsFilters(t *testing.T) {
	app := testApp(t)
	var stdout bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &bytes.Buffer{}
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"dns", "list", "--help"}); err != nil {
		t.Fatalf("dns list help: %v", err)
	}
	output := stdout.String()
	for _, want := range []string{
		"List Records Usage",
		"-t host or --type a,txt filters record types",
		"-e keyword excludes matching name, value, or comment",
		"-s name or --sort=-name sorts by field",
		"-C name,value prints selected output columns",
		"--details loads explicit TTL/detail fields",
		"-C, --columns STRING",
		"-t, --type STRING",
		"-e, --exclude STRINGARRAY",
		"-s, --sort STRING",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("dns list help output missing %q:\n%s", want, output)
		}
	}
}

func TestDNSSearchHelpShowsSort(t *testing.T) {
	app := testApp(t)
	var stdout bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &bytes.Buffer{}
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"dns", "search", "--help"}); err != nil {
		t.Fatalf("dns search help: %v", err)
	}
	output := stdout.String()
	for _, want := range []string{
		"Search Records Usage",
		"FQDN can infer zone",
		"-s name or --sort=-name sorts by field",
		"-C name,value prints selected output columns",
		"-C, --columns STRING",
		"-s, --sort STRING",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("dns search help output missing %q:\n%s", want, output)
		}
	}
}

func TestDNSNextIPHelpShowsOptions(t *testing.T) {
	app := testApp(t)
	var stdout bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &bytes.Buffer{}
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"dns", "next-ip", "--help"}); err != nil {
		t.Fatalf("dns next-ip help: %v", err)
	}
	output := stdout.String()
	for _, want := range []string{
		"Next IP Usage",
		"IPv4 network or container CIDR such as 192.0.2.0/24",
		"--network-view chooses the IPAM network view",
		"networks and containers can both request next_available_ip",
		"-n 3 or --num 3 requests multiple addresses",
		"-e 192.0.2.10 excludes an address",
		"--network-view STRING",
		"-n, --num INT",
		"-e, --exclude STRINGARRAY",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("dns next-ip help output missing %q:\n%s", want, output)
		}
	}
	for _, unwanted := range []string{
		"--zone STRING",
		"--view STRING",
	} {
		if strings.Contains(output, unwanted) {
			t.Fatalf("dns next-ip help output contains unused DNS context flag %q:\n%s", unwanted, output)
		}
	}
}

func TestNetHelpShowsIPAMOptions(t *testing.T) {
	tests := []struct {
		args []string
		want []string
	}{
		{
			args: []string{"net", "list", "--help"},
			want: []string{
				"Network List Usage",
				"omit --network-view to scan all IPAM views, or set it to one view",
				"expired cache is shown immediately; --refresh waits for fresh WAPI data",
				"-s network or --sort=-comment sorts by field",
				"-C network,type,comment prints selected output columns",
				"--network-view STRING",
				"--refresh",
				"-C, --columns STRING",
				"-s, --sort STRING",
			},
		},
		{
			args: []string{"net", "address", "--help"},
			want: []string{
				"Address Details Usage",
				"IPv4 address such as 192.0.2.10",
				"--network-view narrows the lookup",
				"--network-view STRING",
			},
		},
		{
			args: []string{"net", "next-ip", "--help"},
			want: []string{
				"Next IP Usage",
				"IPv4 CIDR such as 192.0.2.0/24",
				"--network-view chooses the IPAM network view",
				"networks and containers can both request next_available_ip",
				"-n 3 or --num 3 requests multiple addresses",
				"-e 192.0.2.10 excludes an address",
				"--network-view STRING",
				"-n, --num INT",
				"-e, --exclude STRINGARRAY",
			},
		},
	}
	for _, tt := range tests {
		app := testApp(t)
		var stdout bytes.Buffer
		app.Stdout = &stdout
		app.Stderr = &bytes.Buffer{}
		app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

		if err := app.Execute(tt.args); err != nil {
			t.Fatalf("%v help: %v", tt.args, err)
		}
		output := stdout.String()
		for _, want := range tt.want {
			if !strings.Contains(output, want) {
				t.Fatalf("%v help output missing %q:\n%s", tt.args, want, output)
			}
		}
		if strings.Contains(output, "--zone STRING") || strings.Contains(output, "--view STRING") {
			t.Fatalf("%v help output contains DNS context flags:\n%s", tt.args, output)
		}
	}
}

func TestDNSZoneListHelpShowsFilters(t *testing.T) {
	app := testApp(t)
	var stdout bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &bytes.Buffer{}
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"dns", "zone", "list", "--help"}); err != nil {
		t.Fatalf("dns zone list help: %v", err)
	}
	output := stdout.String()
	for _, want := range []string{
		"Zone List Usage",
		"-t FORWARD or --type IPV4,IPV6 filters zone formats",
		"-e keyword excludes matching zone name or comment",
		"-s zone or --sort=-comment sorts by field",
		"-C zone,comment prints selected output columns",
		"-C, --columns STRING",
		"-e, --exclude STRINGARRAY",
		"-s, --sort STRING",
		"-t, --type STRING",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("dns zone list help output missing %q:\n%s", want, output)
		}
	}
	for _, unwanted := range []string{
		"-z, --zone STRING",
		"--zone/-z",
	} {
		if strings.Contains(output, unwanted) {
			t.Fatalf("dns zone list help output contains disabled zone override %q:\n%s", unwanted, output)
		}
	}
}

func TestDNSZoneListRejectsZoneOverride(t *testing.T) {
	app := testApp(t)
	app.Stderr = &bytes.Buffer{}
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	err := app.Execute([]string{"dns", "--zone", "example.com", "zone", "list"})
	if err == nil {
		t.Fatal("dns zone list accepted --zone override")
	}
	if !strings.Contains(err.Error(), "--zone/-z cannot be used with ib dns zone list") {
		t.Fatalf("dns zone list error = %v", err)
	}
}

func TestDNSNextIPRejectsDNSContextOverrides(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{args: []string{"dns", "--zone", "example.com", "next-ip", "192.0.2.0/24"}, want: "--zone/-z cannot be used with ib dns next-ip"},
		{args: []string{"dns", "--view", "DNS Zone View", "next-ip", "192.0.2.0/24"}, want: "--view/-v cannot be used with ib dns next-ip; use --network-view"},
	}
	for _, tt := range tests {
		app := testApp(t)
		app.Stderr = &bytes.Buffer{}
		app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

		err := app.Execute(tt.args)
		if err == nil {
			t.Fatalf("Execute(%v) accepted DNS context override", tt.args)
		}
		if !strings.Contains(err.Error(), tt.want) {
			t.Fatalf("Execute(%v) error = %v, want %q", tt.args, err, tt.want)
		}
	}
}

func TestConfigHelpCoversWorkflowAndStorage(t *testing.T) {
	app := testApp(t)
	var stdout bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &bytes.Buffer{}
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"config", "--help"}); err != nil {
		t.Fatalf("config help: %v", err)
	}
	output := stdout.String()
	for _, want := range []string{
		"Configuration Usage",
		"ib config new [PROFILE]",
		"ib config edit [PROFILE]",
		"ib config cache status|clear",
		"Profile Details",
		"server reachability/TLS trust, unauth/auth WAPI discovery, validated credentials, auto GCM read endpoint, DNS view, default zone, audit logging",
		"merges when present",
		"selected global profiles use",
		"encrypted at rest",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("config help output missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "connection test runs before saving; failures show a retry prompt") {
		t.Fatalf("config help output contains removed validation text:\n%s", output)
	}
}

func TestConfigSubcommandHelpCoversGuidedPrompts(t *testing.T) {
	tests := []struct {
		args []string
		want []string
	}{
		{
			args: []string{"config", "new", "--help"},
			want: []string{
				"Create Profile",
				"blank prompt creates profile 'default'",
				"endpoint reachability",
				"TLS trust",
				"credential validation",
				"auto GCM",
				"audit logging",
				"connection test must pass before saving",
				"failed connection test shows a retry prompt",
				"--global-config writes Linux shared config",
				"requires root",
				"ib config new prod --default",
			},
		},
		{
			args: []string{"config", "edit", "--help"},
			want: []string{
				"Edit Profile",
				"omit to edit the current default profile",
				"leave blank to keep the existing encrypted password",
				"Windows Event Log audit logging",
				"failed connection test shows a retry prompt",
				"--global-config edits Linux shared config",
				"requires root",
			},
		},
		{
			args: []string{"config", "list", "--help"},
			want: []string{
				"List Profiles",
				"profile, default, username, server, read endpoint, WAPI, SSL, DNS view, default zone",
				"table output also shows merged config metadata",
				"-o table, -o json, or -o csv",
			},
		},
		{
			args: []string{"config", "use", "--help"},
			want: []string{
				"Select Default Profile",
				"updates the local default_profile override",
				"can select local profiles or global profiles",
				"PROFILE completes from configured profile names",
			},
		},
		{
			args: []string{"config", "delete", "--help"},
			want: []string{
				"Delete Profile",
				"clears cache entries for the deleted local profile",
				"the current default profile cannot be deleted",
				"ib config use OTHER_PROFILE",
			},
		},
	}

	for _, tt := range tests {
		app := testApp(t)
		var stdout bytes.Buffer
		app.Stdout = &stdout
		app.Stderr = &bytes.Buffer{}
		app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

		if err := app.Execute(tt.args); err != nil {
			t.Fatalf("help %v: %v", tt.args, err)
		}
		output := stdout.String()
		for _, want := range tt.want {
			if !strings.Contains(output, want) {
				t.Fatalf("help %v missing %q:\n%s", tt.args, want, output)
			}
		}
	}
}

func TestConfigCacheAndCompletionHelpCoversOperationalDetails(t *testing.T) {
	tests := []struct {
		args []string
		want []string
	}{
		{
			args: []string{"config", "completion", "--help"},
			want: []string{
				"Completion Usage",
				"no arg   print setup instructions",
				"bash     generate Bash completion",
				"windows  install PowerShell completion on Windows",
				"ib config completion bash > ~/.ib-complete.bash",
				"ib config completion windows",
			},
		},
		{
			args: []string{"config", "cache", "--help"},
			want: []string{
				"Cache Usage",
				"show DNS and IPAM cache entries with age and expiry",
				"DNS caches use profile/view/zone; IPAM caches use profile/network view/IP",
			},
		},
		{
			args: []string{"config", "cache", "status", "--help"},
			want: []string{
				"Cache Status",
				"kind, profile, view/network view, zone/IP, serial, items, age, stale_expires",
				"table output adds a colored summary footer",
				"returns statistics and entries",
				"keeps row-only output for scripts",
				"-o table, -o json, or -o csv",
			},
		},
		{
			args: []string{"config", "cache", "clear", "--help"},
			want: []string{
				"Cache Clear",
				"local DNS and IPAM cache entries",
				"configuration profiles, encryption key, and active session files",
			},
		},
	}

	for _, tt := range tests {
		app := testApp(t)
		var stdout bytes.Buffer
		app.Stdout = &stdout
		app.Stderr = &bytes.Buffer{}
		app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

		if err := app.Execute(tt.args); err != nil {
			t.Fatalf("help %v: %v", tt.args, err)
		}
		output := stdout.String()
		for _, want := range tt.want {
			if !strings.Contains(output, want) {
				t.Fatalf("help %v missing %q:\n%s", tt.args, want, output)
			}
		}
	}
}

func TestDNSSearchArgumentErrorsPrintUsage(t *testing.T) {
	for _, args := range [][]string{
		{"dns", "search"},
		{"dns", "search", "one", "two"},
	} {
		app := testApp(t)
		var stdout, stderr bytes.Buffer
		app.Stdout = &stdout
		app.Stderr = &stderr
		app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

		err := app.Execute(args)
		if !errors.Is(err, errUsageDisplayed) {
			t.Fatalf("Execute(%v) error = %v, want usage sentinel", args, err)
		}
		if stdout.Len() != 0 {
			t.Fatalf("Execute(%v) wrote stdout:\n%s", args, stdout.String())
		}
		output := stderr.String()
		for _, want := range []string{
			"Usage",
			"ib dns search <keyword> [flags]",
			"Example",
			"ib dns search app",
			"ib dns search ben-dr-vss.net.latrobe.edu.au",
			`Use "ib dns search -h" for more info.`,
		} {
			if !strings.Contains(output, want) {
				t.Fatalf("Execute(%v) usage missing %q:\n%s", args, want, output)
			}
		}
		for _, unwanted := range []string{
			"scope    active zone by default",
			"type     -t host",
			"exclude  -e keyword",
			"ib dns search app --global",
		} {
			if strings.Contains(output, unwanted) {
				t.Fatalf("Execute(%v) usage is too verbose with %q:\n%s", args, unwanted, output)
			}
		}
		if strings.Contains(output, "ERROR:") || strings.Contains(output, "accepts 1 arg") {
			t.Fatalf("Execute(%v) printed argument error instead of usage only:\n%s", args, output)
		}
	}
}

func TestArgumentErrorsPrintUsage(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"dns", "zone", "info"}, "ib dns zone info ZONE"},
		{[]string{"dns", "create", "host", "app"}, "ib dns create TYPE NAME VALUE"},
	}
	for _, tt := range tests {
		app := testApp(t)
		var stdout, stderr bytes.Buffer
		app.Stdout = &stdout
		app.Stderr = &stderr
		app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

		err := app.Execute(tt.args)
		if !errors.Is(err, errUsageDisplayed) {
			t.Fatalf("Execute(%v) error = %v, want usage sentinel", tt.args, err)
		}
		if stdout.Len() != 0 {
			t.Fatalf("Execute(%v) wrote stdout:\n%s", tt.args, stdout.String())
		}
		output := stderr.String()
		for _, want := range []string{"Usage", tt.want} {
			if !strings.Contains(output, want) {
				t.Fatalf("Execute(%v) usage missing %q:\n%s", tt.args, want, output)
			}
		}
		if strings.Contains(output, "ERROR:") || strings.Contains(output, "accepts ") || strings.Contains(output, "requires ") {
			t.Fatalf("Execute(%v) printed raw argument error instead of usage only:\n%s", tt.args, output)
		}
	}
}

func TestCreateHelpUsesStyledUsagePanel(t *testing.T) {
	app := testApp(t)
	var stdout bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &bytes.Buffer{}
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"dns", "create", "--help"}); err != nil {
		t.Fatalf("help: %v", err)
	}
	output := stdout.String()
	for _, want := range []string{
		"Create Record Usage",
		"Current Context",
		"ib dns create ptr <ip-address> <ptr-target>",
		"ib dns create ns <child-zone> <nameserver> <address>",
		"creates a delegation record",
		"reverse zone is auto-detected",
		"ttl      optional; omit to use the zone default",
		`ib dns create host app 192.0.2.10 -c "Application host"`,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("help output missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "default -1") {
		t.Fatalf("help output exposed TTL sentinel:\n%s", output)
	}
	if !strings.Contains(output, "-h, --help") {
		t.Fatalf("help output missing global help option:\n%s", output)
	}
}

func TestCompletionMovedUnderConfigModule(t *testing.T) {
	app := testApp(t)
	var stdout bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &bytes.Buffer{}
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"config", "completion"}); err != nil {
		t.Fatalf("config completion: %v", err)
	}
	output := stdout.String()
	for _, want := range []string{
		"Shell completion setup:",
		"ib config completion bash > ~/.ib-complete.bash",
		"ib config completion zsh > ~/.ib-complete.zsh",
		"ib config completion fish > ~/.config/fish/completions/ib.fish",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("config completion output missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "ib completion bash") {
		t.Fatalf("config completion output contains old root path:\n%s", output)
	}
}

func TestRootCompletionCommandIsRemoved(t *testing.T) {
	app := testApp(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &stderr
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	err := app.Execute([]string{"completion", "bash"})
	if err == nil {
		t.Fatalf("root completion command unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), `unknown module "completion" for "ib"`) {
		t.Fatalf("root completion returned unexpected error: %v", err)
	}
}

func TestGlobalOutputFlagCompletesValuesOnSubcommands(t *testing.T) {
	for _, args := range [][]string{
		{"__complete", "dns", "create", "--output", ""},
		{"__complete", "dns", "create", "-o", ""},
		{"__complete", "dns", "create", "--output", "j"},
	} {
		app := testApp(t)
		var stdout bytes.Buffer
		app.Stdout = &stdout
		app.Stderr = &bytes.Buffer{}
		app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

		if err := app.Execute(args); err != nil {
			t.Fatalf("completion %v: %v", args, err)
		}
		output := stdout.String()
		if !strings.Contains(output, "json\tJSON output") {
			t.Fatalf("completion %v missing json value:\n%s", args, output)
		}
		if strings.Contains(output, "jq") {
			t.Fatalf("completion %v still exposes jq value:\n%s", args, output)
		}
		if args[len(args)-1] == "" {
			for _, want := range []string{"table\tstyled table output", "csv\tCSV output"} {
				if !strings.Contains(output, want) {
					t.Fatalf("completion %v missing %q:\n%s", args, want, output)
				}
			}
		}
		if !strings.Contains(output, ":4") {
			t.Fatalf("completion %v did not disable file completion:\n%s", args, output)
		}
	}
}

func TestGlobalOutputFlagRejectsJQValue(t *testing.T) {
	app := testApp(t)
	var stdout, stderr bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &stderr
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	err := app.Execute([]string{"-o", "jq", "config", "list"})
	if err == nil {
		t.Fatal("old jq output value succeeded")
	}
	if !strings.Contains(err.Error(), `unsupported output format "jq"; use table, json, or csv`) {
		t.Fatalf("unexpected jq rejection error: %v", err)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("jq rejection wrote output stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}
