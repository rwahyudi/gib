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
		"create  Create a DNS record",
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
		"server, username, password, WAPI version, SSL, auto GCM read endpoint, DNS view, default zone",
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
				"auto GCM read endpoint",
				"connection test must pass before saving",
				"failed connection test shows a retry prompt",
				"ib config new prod --default",
			},
		},
		{
			args: []string{"config", "edit", "--help"},
			want: []string{
				"Edit Profile",
				"omit to edit the current default profile",
				"leave blank to keep the existing encrypted password",
				"failed connection test shows a retry prompt",
			},
		},
		{
			args: []string{"config", "list", "--help"},
			want: []string{
				"List Profiles",
				"profile, default, server, read endpoint, DNS view, default zone",
				"-o table, -o json, or -o csv",
			},
		},
		{
			args: []string{"config", "use", "--help"},
			want: []string{
				"Select Default Profile",
				"updates the persistent default_profile",
				"PROFILE completes from configured profile names",
			},
		},
		{
			args: []string{"config", "delete", "--help"},
			want: []string{
				"Delete Profile",
				"clears local cache entries for the deleted profile",
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
				"no arg  print setup instructions",
				"bash    generate Bash completion",
				"ib config completion bash > ~/.ib-complete.bash",
			},
		},
		{
			args: []string{"config", "cache", "--help"},
			want: []string{
				"Cache Usage",
				"show zone and record cache entries with age and expiry",
				"cache entries are separated by profile, DNS view, and zone",
			},
		},
		{
			args: []string{"config", "cache", "status", "--help"},
			want: []string{
				"Cache Status",
				"kind, profile, view, zone, serial, items, age, stale_expires",
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
				"local zone and record cache entries",
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
		{[]string{"dns", "create", "app", "host"}, "ib dns create NAME TYPE VALUE"},
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
		"ttl      optional; omit to use the zone default",
		`ib dns create app host 192.0.2.10 -c "Application host"`,
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
