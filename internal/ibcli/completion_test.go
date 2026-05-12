package ibcli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func TestCompleteZoneNamesUsesCacheAndFilters(t *testing.T) {
	app := testApp(t)
	profile := writeCompletionProfile(t, app, "https://infoblox.invalid")
	if err := app.writeCachedZones(profile, []map[string]any{
		{"fqdn": "example.com"},
		{"fqdn": "prod.example.com"},
		{"fqdn": "example.net"},
	}, time.Now()); err != nil {
		t.Fatalf("write cache: %v", err)
	}

	matches := app.completeZoneNames(&cobra.Command{}, "ex")
	if strings.Join(matches, ",") != "example.com,example.net" {
		t.Fatalf("matches = %#v", matches)
	}
}

func TestCachedZoneNamesRefreshesExpiredCache(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/wapi/"+defaultWAPIVersion+"/"+zoneObject {
			t.Fatalf("path = %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": []map[string]any{
				{"fqdn": "zeta.example.com"},
				{"fqdn": "alpha.example.com"},
			},
		})
	}))
	defer server.Close()

	app := testApp(t)
	profile := writeCompletionProfile(t, app, server.URL)
	if err := app.writeCachedZones(profile, []map[string]any{
		{"fqdn": "stale.example.com"},
	}, time.Now().Add(-10*time.Minute)); err != nil {
		t.Fatalf("write cache: %v", err)
	}

	names, err := app.cachedZoneNames(profile)
	if err != nil {
		t.Fatalf("cached zones: %v", err)
	}
	if strings.Join(names, ",") != "alpha.example.com,zeta.example.com" {
		t.Fatalf("names = %#v", names)
	}
	if requests != 1 {
		t.Fatalf("requests = %d", requests)
	}
}

func TestRecordCompletionRefreshesExpiredCache(t *testing.T) {
	var allRecordRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/zone_auth"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{
					{"fqdn": "example.com", "view": "default", "zone_format": "FORWARD", "soa_serial_number": "2026050802"},
				},
			})
		case strings.HasSuffix(r.URL.Path, "/allrecords"):
			allRecordRequests++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{
					{"type": "record:a", "name": "live", "address": "192.0.2.20", "zone": "example.com"},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	app := testApp(t)
	profile := writeCompletionProfile(t, app, server.URL)
	now := time.Now()
	if err := app.writeCachedRecordsEntry(profile, "example.com", "2026050801", []map[string]any{
		{"type": "record:a", "name": "stale", "address": "192.0.2.10", "zone": "example.com"},
	}, now.Add(-time.Hour).Unix(), now.Add(-time.Second).Unix()); err != nil {
		t.Fatalf("write stale record cache: %v", err)
	}

	records, err := app.cachedRecordNamesForCompletion(profile, "example.com")
	if err != nil {
		t.Fatalf("record completion cache: %v", err)
	}
	matches := matchingRecordNames(records, "")
	if strings.Join(matches, ",") != "live\tA 192.0.2.20" {
		t.Fatalf("matches = %#v", matches)
	}
	if allRecordRequests != 1 {
		t.Fatalf("allrecords requests = %d", allRecordRequests)
	}
}

func TestZoneCompletionFailsQuietlyWithoutConfig(t *testing.T) {
	app := testApp(t)
	if matches := app.completeZoneNames(&cobra.Command{}, "ex"); len(matches) != 0 {
		t.Fatalf("matches without config = %#v", matches)
	}
}

func TestZoneCompletionsAreWiredToCommandsAndFlags(t *testing.T) {
	tests := [][]string{
		{"__complete", "dns", "zone", "use", "ex"},
		{"__complete", "dns", "zone", "info", "ex"},
		{"__complete", "dns", "zone", "delete", "ex"},
		{"__complete", "dns", "list", "ex"},
		{"__complete", "dns", "delete", "app", "ex"},
		{"__complete", "dns", "create", "app", "host", "192.0.2.10", "--zone", "ex"},
		{"__complete", "dns", "edit", "app", "host", "192.0.2.10", "--zone", "ex"},
		{"__complete", "dns", "search", "app", "--zone", "ex"},
		{"__complete", "dns", "search", "app", "-z", "ex"},
	}

	for _, args := range tests {
		app, stdout := completionAppWithZones(t)
		if err := app.Execute(args); err != nil {
			t.Fatalf("completion %v: %v", args, err)
		}
		output := stdout.String()
		if !strings.Contains(output, "example.com") || strings.Contains(output, "prod.example.com") {
			t.Fatalf("completion %v output =\n%s", args, output)
		}
		if !strings.Contains(output, ":4") {
			t.Fatalf("completion %v did not disable file completion:\n%s", args, output)
		}
	}
}

func TestZoneCreateDoesNotCompleteExistingZones(t *testing.T) {
	app, stdout := completionAppWithZones(t)
	if err := app.Execute([]string{"__complete", "dns", "zone", "create", "ex"}); err != nil {
		t.Fatalf("completion: %v", err)
	}
	if strings.Contains(stdout.String(), "example.com") {
		t.Fatalf("zone create completed existing zones:\n%s", stdout.String())
	}
}

func TestDNSCreateCompletesRecordTypesAfterName(t *testing.T) {
	app := testApp(t)
	var stdout bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &bytes.Buffer{}
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"__complete", "dns", "create", "app", "h"}); err != nil {
		t.Fatalf("completion: %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, "host\thost record") {
		t.Fatalf("completion output missing host record type:\n%s", output)
	}
	if strings.Contains(output, "txt\ttext record") {
		t.Fatalf("completion output did not filter by prefix:\n%s", output)
	}
	if !strings.Contains(output, ":4") {
		t.Fatalf("completion did not disable file completion:\n%s", output)
	}
}

func TestDNSCreateDoesNotCompleteRecordTypesForName(t *testing.T) {
	app := testApp(t)
	var stdout bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &bytes.Buffer{}
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"__complete", "dns", "create", "h"}); err != nil {
		t.Fatalf("completion: %v", err)
	}
	output := stdout.String()
	if strings.Contains(output, "host\thost record") {
		t.Fatalf("name completion unexpectedly included record types:\n%s", output)
	}
	if !strings.Contains(output, ":4") {
		t.Fatalf("completion did not disable file completion:\n%s", output)
	}
}

func TestDNSCreateCompletesFlagsWhenRequested(t *testing.T) {
	app := testApp(t)
	var stdout bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &bytes.Buffer{}
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"__complete", "dns", "create", "-"}); err != nil {
		t.Fatalf("completion: %v", err)
	}
	output := stdout.String()
	for _, want := range []string{
		"--zone\tDNS zone override for this command",
		"-z\tDNS zone override for this command",
		"--view\tDNS view override for this command",
		"-v\tDNS view override for this command",
		"--ttl\toptional record TTL in seconds",
		"-t\toptional record TTL in seconds",
		"--comment\trecord comment",
		"-c\trecord comment",
		"--noptr\tdo not manage PTR records for A/AAAA workflows",
		"--output\toutput format: table, json, or csv",
		":4",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("completion output missing %q:\n%s", want, output)
		}
	}
}

func TestDNSCreateCompletesFlagsAfterPositionalArgs(t *testing.T) {
	app := testApp(t)
	var stdout bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &bytes.Buffer{}
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"__complete", "dns", "create", "app", "host", "192.0.2.10", ""}); err != nil {
		t.Fatalf("completion: %v", err)
	}
	output := stdout.String()
	for _, want := range []string{
		"-t\toptional record TTL in seconds",
		"-c\trecord comment",
		"-z\tDNS zone override for this command",
		"-v\tDNS view override for this command",
		"--noptr\tdo not manage PTR records for A/AAAA workflows",
		":4",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("completion output missing flag %q after args:\n%s", want, output)
		}
	}
}

func TestDNSRecordSortCompletesValues(t *testing.T) {
	app := testApp(t)
	var stdout bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &bytes.Buffer{}
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"__complete", "dns", "list", "--sort", ""}); err != nil {
		t.Fatalf("completion: %v", err)
	}
	output := stdout.String()
	for _, want := range []string{
		"name\trecord name ascending",
		"-name\trecord name descending",
		"ttl\trecord TTL ascending",
		"-ttl\trecord TTL descending",
		":4",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("completion output missing %q:\n%s", want, output)
		}
	}

	stdout.Reset()
	if err := app.Execute([]string{"__complete", "dns", "search", "test", "-s", "-"}); err != nil {
		t.Fatalf("completion: %v", err)
	}
	output = stdout.String()
	for _, want := range []string{
		"-name\trecord name descending",
		"-comment\trecord comment descending",
		":4",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("completion output missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "\nname\trecord name ascending") {
		t.Fatalf("descending completion included ascending name:\n%s", output)
	}
}

func TestDNSDeleteCompletesRecordNames(t *testing.T) {
	app, stdout := completionAppWithRecords(t)
	if err := app.Execute([]string{"__complete", "dns", "delete", "ap"}); err != nil {
		t.Fatalf("completion: %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, "app\tA 192.0.2.10") {
		t.Fatalf("completion output missing app record:\n%s", output)
	}
	if strings.Contains(output, "db\t") {
		t.Fatalf("completion output did not filter by prefix:\n%s", output)
	}
	if !strings.Contains(output, ":4") {
		t.Fatalf("completion did not disable file completion:\n%s", output)
	}
}

func TestDNSEditCompletesRecordNamesAndTypes(t *testing.T) {
	app, stdout := completionAppWithRecords(t)
	if err := app.Execute([]string{"__complete", "dns", "edit", "ap"}); err != nil {
		t.Fatalf("record completion: %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, "app\tA 192.0.2.10") || !strings.Contains(output, ":4") {
		t.Fatalf("record completion output =\n%s", output)
	}

	stdout.Reset()
	if err := app.Execute([]string{"__complete", "dns", "edit", "app", "h"}); err != nil {
		t.Fatalf("type completion: %v", err)
	}
	output = stdout.String()
	if !strings.Contains(output, "host\thost record") || strings.Contains(output, "txt\ttext record") {
		t.Fatalf("type completion output =\n%s", output)
	}
}

func TestSearchCompletesFlagsAfterGlobalFlag(t *testing.T) {
	app := testApp(t)
	var stdout bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &bytes.Buffer{}
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"__complete", "dns", "search", "test", "-g", ""}); err != nil {
		t.Fatalf("completion: %v", err)
	}
	output := stdout.String()
	for _, want := range []string{
		"--global\tsearch across the selected DNS view",
		"-g\tsearch across the selected DNS view",
		"--recursive\tinclude child authoritative zones",
		"-r\tinclude child authoritative zones",
		"--output\toutput format: table, json, or csv",
		":4",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("completion output missing %q:\n%s", want, output)
		}
	}
}

func TestDNSListCompletesTypeFlagValues(t *testing.T) {
	app := testApp(t)
	var stdout bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &bytes.Buffer{}
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"__complete", "dns", "list", "-t", "h"}); err != nil {
		t.Fatalf("completion: %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, "host\thost record") || strings.Contains(output, "txt\ttext record") {
		t.Fatalf("list type completion output =\n%s", output)
	}
	if !strings.Contains(output, ":4") {
		t.Fatalf("completion did not disable file completion:\n%s", output)
	}
}

func TestDNSRecordColumnsCompleteValues(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wants    []string
		notWants []string
	}{
		{
			name: "empty",
			args: []string{"__complete", "dns", "list", "--columns", ""},
			wants: []string{
				"type\trecord type",
				"name\trecord name",
				"value\trecord value",
				":4",
			},
		},
		{
			name: "prefix",
			args: []string{"__complete", "dns", "search", "test", "-C", "c"},
			wants: []string{
				"comment\trecord comment",
				":4",
			},
			notWants: []string{
				"name\trecord name",
			},
		},
		{
			name: "comma separated",
			args: []string{"__complete", "dns", "list", "--columns", "name,"},
			wants: []string{
				"name,type\trecord type",
				"name,value\trecord value",
				":4",
			},
			notWants: []string{
				"name,name\trecord name",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := testApp(t)
			var stdout bytes.Buffer
			app.Stdout = &stdout
			app.Stderr = &bytes.Buffer{}
			app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

			if err := app.Execute(tt.args); err != nil {
				t.Fatalf("completion: %v", err)
			}
			output := stdout.String()
			for _, want := range tt.wants {
				if !strings.Contains(output, want) {
					t.Fatalf("completion output missing %q:\n%s", want, output)
				}
			}
			for _, notWant := range tt.notWants {
				if strings.Contains(output, notWant) {
					t.Fatalf("completion output contains %q:\n%s", notWant, output)
				}
			}
		})
	}
}

func TestDNSZoneListCompletesFilterSortAndColumns(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wants    []string
		notWants []string
	}{
		{
			name: "type",
			args: []string{"__complete", "dns", "zone", "list", "-t", "I"},
			wants: []string{
				"IPV4\tIPv4 reverse DNS zone",
				"IPV6\tIPv6 reverse DNS zone",
				":4",
			},
			notWants: []string{
				"FORWARD\tforward DNS zone",
			},
		},
		{
			name: "sort descending",
			args: []string{"__complete", "dns", "zone", "list", "-s", "-"},
			wants: []string{
				"-zone\tzone name descending",
				"-comment\tzone comment descending",
				":4",
			},
			notWants: []string{
				"\nzone\tzone name ascending",
			},
		},
		{
			name: "columns",
			args: []string{"__complete", "dns", "zone", "list", "--columns", "zone,"},
			wants: []string{
				"zone,view\tDNS view",
				"zone,format\tzone format",
				":4",
			},
			notWants: []string{
				"zone,zone\tzone name",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := testApp(t)
			var stdout bytes.Buffer
			app.Stdout = &stdout
			app.Stderr = &bytes.Buffer{}
			app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

			if err := app.Execute(tt.args); err != nil {
				t.Fatalf("completion: %v", err)
			}
			output := stdout.String()
			for _, want := range tt.wants {
				if !strings.Contains(output, want) {
					t.Fatalf("completion output missing %q:\n%s", want, output)
				}
			}
			for _, notWant := range tt.notWants {
				if strings.Contains(output, notWant) {
					t.Fatalf("completion output contains %q:\n%s", notWant, output)
				}
			}
		})
	}
}

func TestDNSSearchCompletesTypeFlagValues(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wants    []string
		notWants []string
	}{
		{
			name: "empty",
			args: []string{"__complete", "dns", "search", "test", "-t", ""},
			wants: []string{
				"a\tIPv4 address record",
				"host\thost record",
				"txt\ttext record",
				":4",
			},
		},
		{
			name: "prefix",
			args: []string{"__complete", "dns", "search", "test", "-t", "h"},
			wants: []string{
				"host\thost record",
				":4",
			},
			notWants: []string{
				"txt\ttext record",
			},
		},
		{
			name: "comma separated",
			args: []string{"__complete", "dns", "search", "test", "-t", "a,"},
			wants: []string{
				"a,host\thost record",
				"a,txt\ttext record",
				":4",
			},
			notWants: []string{
				"a,a\tIPv4 address record",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := testApp(t)
			var stdout bytes.Buffer
			app.Stdout = &stdout
			app.Stderr = &bytes.Buffer{}
			app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

			if err := app.Execute(tt.args); err != nil {
				t.Fatalf("completion: %v", err)
			}
			output := stdout.String()
			for _, want := range tt.wants {
				if !strings.Contains(output, want) {
					t.Fatalf("completion output missing %q:\n%s", want, output)
				}
			}
			for _, notWant := range tt.notWants {
				if strings.Contains(output, notWant) {
					t.Fatalf("completion output contains %q:\n%s", notWant, output)
				}
			}
		})
	}
}

func TestConfigureAliasCompletesCacheCommands(t *testing.T) {
	tests := []struct {
		args  []string
		wants []string
	}{
		{
			args: []string{"__complete", "configure", "ca"},
			wants: []string{
				"cache\tManage local SQLite cache",
				":4",
			},
		},
		{
			args: []string{"__complete", "configure", "cache", ""},
			wants: []string{
				"clear\tClear local cache entries",
				"status\tShow local cache status",
				":4",
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
			t.Fatalf("completion %v: %v", tt.args, err)
		}
		output := stdout.String()
		for _, want := range tt.wants {
			if !strings.Contains(output, want) {
				t.Fatalf("completion %v missing %q:\n%s", tt.args, want, output)
			}
		}
	}
}

func TestConfigCompletionCompletesShellNames(t *testing.T) {
	tests := []struct {
		args    []string
		wants   []string
		omitted []string
	}{
		{
			args: []string{"__complete", "config", "completion", ""},
			wants: []string{
				"bash\tBash completion script",
				"zsh\tZsh completion script",
				"fish\tFish completion script",
				":4",
			},
		},
		{
			args: []string{"__complete", "config", "completion", "b"},
			wants: []string{
				"bash\tBash completion script",
				":4",
			},
			omitted: []string{
				"zsh\tZsh completion script",
				"fish\tFish completion script",
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
			t.Fatalf("completion %v: %v", tt.args, err)
		}
		output := stdout.String()
		for _, want := range tt.wants {
			if !strings.Contains(output, want) {
				t.Fatalf("completion %v missing %q:\n%s", tt.args, want, output)
			}
		}
		for _, omitted := range tt.omitted {
			if strings.Contains(output, omitted) {
				t.Fatalf("completion %v unexpectedly included %q:\n%s", tt.args, omitted, output)
			}
		}
	}
}

func TestConfigProfileCommandsCompleteProfiles(t *testing.T) {
	app := testApp(t)
	writeCompletionProfiles(t, app)

	tests := []struct {
		args    []string
		wants   []string
		omitted []string
	}{
		{
			args: []string{"__complete", "config", "use", ""},
			wants: []string{
				"default",
				"lab",
				":4",
			},
		},
		{
			args: []string{"__complete", "config", "edit", ""},
			wants: []string{
				"default",
				"lab",
				":4",
			},
		},
		{
			args: []string{"__complete", "config", "edit", "l"},
			wants: []string{
				"lab",
				":4",
			},
			omitted: []string{"default"},
		},
		{
			args: []string{"__complete", "config", "delete", ""},
			wants: []string{
				"lab",
				":4",
			},
			omitted: []string{"default"},
		},
	}

	for _, tt := range tests {
		var stdout bytes.Buffer
		app.Stdout = &stdout
		app.Stderr = &bytes.Buffer{}
		app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

		if err := app.Execute(tt.args); err != nil {
			t.Fatalf("completion %v: %v", tt.args, err)
		}
		output := stdout.String()
		for _, want := range tt.wants {
			if !strings.Contains(output, want) {
				t.Fatalf("completion %v missing %q:\n%s", tt.args, want, output)
			}
		}
		for _, omitted := range tt.omitted {
			if strings.Contains(output, omitted) {
				t.Fatalf("completion %v unexpectedly included %q:\n%s", tt.args, omitted, output)
			}
		}
	}
}

func TestNoDescCompletionMatchesGeneratedBashPaths(t *testing.T) {
	tests := []struct {
		args  []string
		wants []string
	}{
		{
			args: []string{"__completeNoDesc", "configure", "cache", ""},
			wants: []string{
				"clear",
				"status",
				":4",
			},
		},
		{
			args: []string{"__completeNoDesc", "config", "completion", "b"},
			wants: []string{
				"bash",
				":4",
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
			t.Fatalf("completion %v: %v", tt.args, err)
		}
		output := stdout.String()
		if strings.Contains(output, "\t") {
			t.Fatalf("NoDesc completion %v included descriptions:\n%s", tt.args, output)
		}
		for _, want := range tt.wants {
			if !strings.Contains(output, want) {
				t.Fatalf("completion %v missing %q:\n%s", tt.args, want, output)
			}
		}
	}
}

func TestNoDescRecordCompletionMatchesGeneratedBashPath(t *testing.T) {
	app, stdout := completionAppWithRecords(t)
	if err := app.Execute([]string{"__completeNoDesc", "dns", "delete", "ap"}); err != nil {
		t.Fatalf("completion: %v", err)
	}
	output := stdout.String()
	if strings.Contains(output, "\t") {
		t.Fatalf("NoDesc record completion included descriptions:\n%s", output)
	}
	for _, want := range []string{"app", ":4"} {
		if !strings.Contains(output, want) {
			t.Fatalf("completion missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "db") {
		t.Fatalf("completion output did not filter by prefix:\n%s", output)
	}
}

func TestGeneratedBashCompletionScript(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	app := testApp(t)
	var script bytes.Buffer
	app.Stdout = &script
	app.Stderr = &bytes.Buffer{}
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)
	if err := app.Execute([]string{"config", "completion", "bash"}); err != nil {
		t.Fatalf("generate bash completion: %v", err)
	}
	output := script.String()
	for _, want := range []string{
		"__ib_dynamic_completion",
		"__ib_create_usage_on_second_tab",
		"__completeNoDesc",
		`${COMP_WORDS[0]}`,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("generated bash completion missing %q", want)
		}
	}
	for _, unwanted := range []string{
		"_ib_dns_create",
		"commands=()",
		"flags=()",
		`command_aliases+=("configure")`,
	} {
		if strings.Contains(output, unwanted) {
			t.Fatalf("generated bash completion contains static Cobra tree marker %q:\n%s", unwanted, output)
		}
	}
	cmd := exec.Command("bash", "-n")
	cmd.Stdin = strings.NewReader(output)
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bash completion syntax check failed: %v\n%s", err, raw)
	}
}

func TestDynamicBashCompletionCreateNameSlotPrintsUsageOnSecondTab(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	dir := t.TempDir()
	fakeIB := filepath.Join(dir, "ib")
	if err := os.WriteFile(fakeIB, []byte(`#!/bin/sh
if [ "$1" = "dns" ] && [ "$2" = "create" ] && [ "$3" = "--help" ]; then
  printf 'Usage\n'
  printf '  ib dns create NAME TYPE VALUE [flags]\n'
  exit 0
fi
printf ':4\n'
`), 0o755); err != nil {
		t.Fatalf("write fake ib: %v", err)
	}
	scriptPath := filepath.Join(dir, "ib-complete.bash")
	if err := os.WriteFile(scriptPath, []byte(dynamicBashCompletionScript()), 0o644); err != nil {
		t.Fatalf("write completion script: %v", err)
	}
	cmd := exec.Command("bash", "-lc", `source "$1"
COMP_WORDS=("$2" "dns" "create" "")
COMP_CWORD=3
COMP_LINE="$2 dns create "
COMP_POINT=${#COMP_LINE}
__ib_dynamic_completion
__ib_dynamic_completion
printf '\nreply-count=%d\n' "${#COMPREPLY[@]}"
`, "bash", scriptPath, fakeIB)
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run bash completion simulation: %v\n%s", err, raw)
	}
	output := string(raw)
	if strings.Count(output, "\nUsage\n") != 1 {
		t.Fatalf("create usage should print once on the second tab:\n%s", output)
	}
	if !strings.Contains(output, "ib dns create NAME TYPE VALUE [flags]") {
		t.Fatalf("create usage missing command shape:\n%s", output)
	}
	if !strings.Contains(output, "\nib dns create \nreply-count=0") {
		t.Fatalf("create usage did not reprint the typed command:\n%s", output)
	}
	if !strings.Contains(output, "reply-count=0") {
		t.Fatalf("create name slot should not insert a completion candidate:\n%s", output)
	}
}

func TestDynamicBashCompletionCreatePreservesGlobalFlagCompletion(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	dir := t.TempDir()
	fakeIB := filepath.Join(dir, "ib")
	if err := os.WriteFile(fakeIB, []byte(`#!/bin/sh
if [ "$1" = "__completeNoDesc" ] && [ "$2" = "dns" ] && [ "$3" = "create" ] && [ "$4" = "-" ]; then
  printf '%s\n' --output -o --help -h --zone -z --view -v --ttl -t --comment -c --noptr :4
  exit 0
fi
if [ "$1" = "__completeNoDesc" ] && [ "$2" = "dns" ] && [ "$3" = "create" ] && [ "$7" = "--output" ]; then
  printf '%s\n' table json csv :4
  exit 0
fi
printf ':4\n'
`), 0o755); err != nil {
		t.Fatalf("write fake ib: %v", err)
	}
	scriptPath := filepath.Join(dir, "ib-complete.bash")
	if err := os.WriteFile(scriptPath, []byte(dynamicBashCompletionScript()), 0o644); err != nil {
		t.Fatalf("write completion script: %v", err)
	}
	cmd := exec.Command("bash", "-lc", `source "$1"
COMP_WORDS=("$2" "dns" "create" "-")
COMP_CWORD=3
COMP_LINE="$2 dns create -"
COMP_POINT=${#COMP_LINE}
__ib_dynamic_completion
printf 'flags:%s\n' "${COMPREPLY[*]}"
COMPREPLY=()
COMP_WORDS=("$2" "dns" "create" "app" "host" "192.0.2.10" "--output" "")
COMP_CWORD=7
COMP_LINE="$2 dns create app host 192.0.2.10 --output "
COMP_POINT=${#COMP_LINE}
__ib_dynamic_completion
printf 'outputs:%s\n' "${COMPREPLY[*]}"
`, "bash", scriptPath, fakeIB)
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run bash completion simulation: %v\n%s", err, raw)
	}
	output := string(raw)
	for _, want := range []string{
		"flags:--output -o --help -h --zone -z --view -v --ttl -t --comment -c --noptr",
		"outputs:table json csv",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("generated bash completion missing %q:\n%s", want, output)
		}
	}
}

func TestDynamicBashCompletionZoneListOptions(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	dir := t.TempDir()
	fakeIB := filepath.Join(dir, "ib")
	if err := os.WriteFile(fakeIB, []byte(`#!/bin/sh
if [ "$1" = "__completeNoDesc" ] && [ "$2" = "dns" ] && [ "$3" = "zone" ] && [ "$4" = "list" ] && [ "$5" = "-" ]; then
  printf '%s\n' --type -t --exclude -e --sort -s --columns -C :4
  exit 0
fi
if [ "$1" = "__completeNoDesc" ] && [ "$2" = "dns" ] && [ "$3" = "zone" ] && [ "$4" = "list" ] && [ "$5" = "--type" ]; then
  printf '%s\n' FORWARD IPV4 IPV6 :4
  exit 0
fi
if [ "$1" = "__completeNoDesc" ] && [ "$2" = "dns" ] && [ "$3" = "zone" ] && [ "$4" = "list" ] && [ "$5" = "-s" ]; then
  printf '%s\n' zone -zone comment -comment :4
  exit 0
fi
if [ "$1" = "__completeNoDesc" ] && [ "$2" = "dns" ] && [ "$3" = "zone" ] && [ "$4" = "list" ] && [ "$5" = "-C" ]; then
  printf '%s\n' zone,view zone,format zone,comment :4
  exit 0
fi
printf ':4\n'
`), 0o755); err != nil {
		t.Fatalf("write fake ib: %v", err)
	}
	scriptPath := filepath.Join(dir, "ib-complete.bash")
	if err := os.WriteFile(scriptPath, []byte(dynamicBashCompletionScript()), 0o644); err != nil {
		t.Fatalf("write completion script: %v", err)
	}
	cmd := exec.Command("bash", "-lc", `source "$1"
COMP_WORDS=("$2" "dns" "zone" "list" "-")
COMP_CWORD=4
COMP_LINE="$2 dns zone list -"
COMP_POINT=${#COMP_LINE}
__ib_dynamic_completion
printf 'flags:%s\n' "${COMPREPLY[*]}"
COMPREPLY=()
COMP_WORDS=("$2" "dns" "zone" "list" "--type" "")
COMP_CWORD=5
COMP_LINE="$2 dns zone list --type "
COMP_POINT=${#COMP_LINE}
__ib_dynamic_completion
printf 'types:%s\n' "${COMPREPLY[*]}"
COMPREPLY=()
COMP_WORDS=("$2" "dns" "zone" "list" "-s" "-")
COMP_CWORD=5
COMP_LINE="$2 dns zone list -s -"
COMP_POINT=${#COMP_LINE}
__ib_dynamic_completion
printf 'sorts:%s\n' "${COMPREPLY[*]}"
COMPREPLY=()
COMP_WORDS=("$2" "dns" "zone" "list" "-C" "zone,")
COMP_CWORD=5
COMP_LINE="$2 dns zone list -C zone,"
COMP_POINT=${#COMP_LINE}
__ib_dynamic_completion
printf 'columns:%s\n' "${COMPREPLY[*]}"
`, "bash", scriptPath, fakeIB)
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run bash completion simulation: %v\n%s", err, raw)
	}
	output := string(raw)
	for _, want := range []string{
		"flags:--type -t --exclude -e --sort -s --columns -C",
		"types:FORWARD IPV4 IPV6",
		"sorts:-zone -comment",
		"columns:zone,view zone,format zone,comment",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("generated bash completion missing %q:\n%s", want, output)
		}
	}
}

func TestDynamicBashCompletionCompletesRootCommands(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	dir := t.TempDir()
	fakeIB := filepath.Join(dir, "ib")
	if err := os.WriteFile(fakeIB, []byte(`#!/bin/sh
if [ "$1" = "__completeNoDesc" ] && [ "$2" = "" ] && [ "$#" -eq 2 ]; then
  printf 'config\n'
  printf 'dns\n'
  printf ':4\n'
  exit 0
fi
printf ':0\n'
`), 0o755); err != nil {
		t.Fatalf("write fake ib: %v", err)
	}
	scriptPath := filepath.Join(dir, "ib-complete.bash")
	if err := os.WriteFile(scriptPath, []byte(dynamicBashCompletionScript()), 0o644); err != nil {
		t.Fatalf("write completion script: %v", err)
	}
	cmd := exec.Command("bash", "-lc", `source "$1"; COMP_WORDS=("$2" ""); COMP_CWORD=1; __ib_dynamic_completion; printf "%s\n" "${COMPREPLY[@]}"`, "bash", scriptPath, fakeIB)
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run bash completion simulation: %v\n%s", err, raw)
	}
	output := string(raw)
	for _, want := range []string{"config", "dns"} {
		if !strings.Contains(output, want) {
			t.Fatalf("root completion missing %q:\n%s", want, output)
		}
	}
}

func TestGeneratedZshAndFishCompletionScriptsAreDynamic(t *testing.T) {
	tests := []struct {
		shell string
		wants []string
	}{
		{
			shell: "zsh",
			wants: []string{"#compdef ib", "__complete", "compdef _ib ib"},
		},
		{
			shell: "fish",
			wants: []string{"complete -c ib", "__complete", "__ib_dynamic_completion"},
		},
	}
	for _, tt := range tests {
		app := testApp(t)
		var stdout bytes.Buffer
		app.Stdout = &stdout
		app.Stderr = &bytes.Buffer{}
		app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)
		if err := app.Execute([]string{"config", "completion", tt.shell}); err != nil {
			t.Fatalf("generate %s completion: %v", tt.shell, err)
		}
		output := stdout.String()
		for _, want := range tt.wants {
			if !strings.Contains(output, want) {
				t.Fatalf("%s completion missing %q:\n%s", tt.shell, want, output)
			}
		}
		for _, unwanted := range []string{"_ib_dns_create", "commands=()", "flags=()"} {
			if strings.Contains(output, unwanted) {
				t.Fatalf("%s completion contains static Cobra tree marker %q:\n%s", tt.shell, unwanted, output)
			}
		}
	}
}

func completionAppWithZones(t *testing.T) (*App, *bytes.Buffer) {
	t.Helper()
	app := testApp(t)
	profile := writeCompletionProfile(t, app, "https://infoblox.invalid")
	if err := app.writeCachedZones(profile, []map[string]any{
		{"fqdn": "example.com"},
		{"fqdn": "prod.example.com"},
	}, time.Now()); err != nil {
		t.Fatalf("write cache: %v", err)
	}
	var stdout bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &bytes.Buffer{}
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)
	return app, &stdout
}

func completionAppWithRecords(t *testing.T) (*App, *bytes.Buffer) {
	t.Helper()
	app, stdout := completionAppWithZones(t)
	profile, err := app.loadConfig(true)
	if err != nil {
		t.Fatalf("load profile: %v", err)
	}
	if err := app.writeCachedRecords(profile, "example.com", "2026050801", []map[string]any{
		{"type": "record:a", "name": "app", "address": "192.0.2.10", "zone": "example.com"},
		{"type": "record:a", "name": "db", "address": "192.0.2.20", "zone": "example.com"},
	}, time.Now()); err != nil {
		t.Fatalf("write record cache: %v", err)
	}
	return app, stdout
}

func writeCompletionProfiles(t *testing.T, app *App) {
	t.Helper()
	profiles := map[string]Profile{
		defaultProfileName: {
			Name:        defaultProfileName,
			Server:      "https://infoblox.invalid",
			Username:    "admin",
			Password:    "secret",
			WAPIVersion: defaultWAPIVersion,
			DNSView:     "default",
			VerifySSL:   true,
			Timeout:     defaultTimeoutSeconds,
		},
		"lab": {
			Name:        "lab",
			Server:      "https://lab-infoblox.invalid",
			Username:    "admin",
			Password:    "secret",
			WAPIVersion: defaultWAPIVersion,
			DNSView:     "default",
			VerifySSL:   true,
			Timeout:     defaultTimeoutSeconds,
		},
	}
	if err := app.writeConfigProfiles(defaultProfileName, profiles); err != nil {
		t.Fatalf("write profiles: %v", err)
	}
}

func writeCompletionProfile(t *testing.T, app *App, server string) Profile {
	t.Helper()
	profiles := map[string]Profile{
		defaultProfileName: {
			Name:        defaultProfileName,
			Server:      server,
			Username:    "admin",
			Password:    "secret",
			WAPIVersion: defaultWAPIVersion,
			DNSView:     "default",
			DefaultZone: "example.com",
			VerifySSL:   true,
			Timeout:     defaultTimeoutSeconds,
		},
	}
	if err := app.writeConfigProfiles(defaultProfileName, profiles); err != nil {
		t.Fatalf("write profiles: %v", err)
	}
	profile, err := app.loadConfig(true)
	if err != nil {
		t.Fatalf("load profile: %v", err)
	}
	return profile
}
