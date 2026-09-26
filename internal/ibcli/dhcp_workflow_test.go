package ibcli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func dhcpWorkflowApp(t *testing.T, primaryURL, readURL, output string) *App {
	t.Helper()
	app := testApp(t)
	profile := Profile{
		Name:                defaultProfileName,
		Server:              primaryURL,
		ReadServer:          readURL,
		ReadServerVerifySSL: true,
		Username:            "admin",
		Password:            "secret",
		WAPIVersion:         defaultWAPIVersion,
		VerifySSL:           true,
		Timeout:             defaultTimeoutSeconds,
	}
	if err := app.writeConfigProfiles(defaultProfileName, map[string]Profile{defaultProfileName: profile}); err != nil {
		t.Fatalf("write profile: %v", err)
	}
	app.Output = output
	app.Stdout = &bytes.Buffer{}
	app.Stderr = &bytes.Buffer{}
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)
	return app
}

func TestDHCPReservationListUsesReadServer(t *testing.T) {
	var primaryHits, readHits int
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryHits++
		http.Error(w, "unexpected primary request", http.StatusInternalServerError)
	}))
	defer primary.Close()
	read := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		readHits++
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/fixedaddress") {
			t.Fatalf("read request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("_max_results"); got != "20000" {
			t.Fatalf("max results = %q, want 20000", got)
		}
		if got := r.URL.Query().Get("network_view"); got != "default" {
			t.Fatalf("network view = %q, want default", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{{"ipv4addr": "192.0.2.25", "mac": "00:11:22:33:44:55", "name": "printer", "network_view": "default", "comment": "lab"}}})
	}))
	defer read.Close()

	app := dhcpWorkflowApp(t, primary.URL, read.URL, jsonOutput)
	if err := app.runDHCPReservationList("printer", "", "", ""); err != nil {
		t.Fatalf("list: %v", err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(app.Stdout.(*bytes.Buffer).Bytes(), &rows); err != nil {
		t.Fatalf("decode list output: %v", err)
	}
	if len(rows) != 1 || rows[0]["ip"] != "192.0.2.25" {
		t.Fatalf("list rows = %#v", rows)
	}
	if readHits != 1 || primaryHits != 0 {
		t.Fatalf("request routing read=%d primary=%d", readHits, primaryHits)
	}
}

func TestDHCPReservationCreateWritesPrimaryAndNormalizesMAC(t *testing.T) {
	var primaryHits, readHits int
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryHits++
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/fixedaddress") {
			t.Fatalf("primary request = %s %s", r.Method, r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if payload["ipv4addr"] != "192.0.2.25" || payload["mac"] != "00:11:22:33:44:55" || payload["network_view"] != "default" {
			t.Fatalf("payload = %#v", payload)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"_ref": "fixedaddress/abc"})
	}))
	defer primary.Close()
	read := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		readHits++
		http.Error(w, "unexpected read request", http.StatusInternalServerError)
	}))
	defer read.Close()

	app := dhcpWorkflowApp(t, primary.URL, read.URL, jsonOutput)
	if err := app.runDHCPReservationCreate("192.0.2.25", "", "default", "0011.2233.4455", "printer", "lab"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if primaryHits != 1 || readHits != 0 {
		t.Fatalf("request routing read=%d primary=%d", readHits, primaryHits)
	}
	var row map[string]any
	if err := json.Unmarshal(app.Stdout.(*bytes.Buffer).Bytes(), &row); err != nil {
		t.Fatalf("decode create output: %v", err)
	}
	if row["action"] != "create" || row["ip"] != "192.0.2.25" {
		t.Fatalf("create output = %#v", row)
	}
}

func TestDHCPReservationMutationReportsCacheFailureWithoutRetryError(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("mutation request = %s", r.Method)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ipv4addr": "192.0.2.25", "network_view": "default"})
	}))
	defer primary.Close()
	read := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unexpected read request", http.StatusInternalServerError)
	}))
	defer read.Close()
	app := dhcpWorkflowApp(t, primary.URL, read.URL, jsonOutput)
	app.backgroundNetRefresher = func(Profile, string, string, string) error {
		return cliError("simulated cache dispatch failure")
	}
	if err := app.runDHCPReservationCreate("192.0.2.25", "", "default", "00:11:22:33:44:55", "", ""); err != nil {
		t.Fatalf("mutation returned cache-only error: %v", err)
	}
	if !strings.Contains(app.Stderr.(*bytes.Buffer).String(), "do not retry") {
		t.Fatalf("missing partial-success warning: %s", app.Stderr.(*bytes.Buffer).String())
	}
	var row map[string]any
	if err := json.Unmarshal(app.Stdout.(*bytes.Buffer).Bytes(), &row); err != nil {
		t.Fatalf("decode successful mutation output: %v", err)
	}
	if row["action"] != "create" {
		t.Fatalf("mutation output = %#v", row)
	}
}

func TestDHCPReservationCreateValidatesAllocationSourceAndMAC(t *testing.T) {
	app := testApp(t)
	if err := app.runDHCPReservationCreate("192.0.2.25", "192.0.2.0/24", "", "00:11:22:33:44:55", "", ""); err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("both allocation sources error = %v", err)
	}
	if err := app.runDHCPReservationCreate("192.0.2.25", "", "", "", "", ""); err == nil || !strings.Contains(err.Error(), "MAC address is required") {
		t.Fatalf("missing MAC error = %v", err)
	}
}

func TestDHCPReservationCreateNextIPIsAtomicAndUsesPrimary(t *testing.T) {
	var primaryHits, readHits int
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryHits++
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/fixedaddress") {
			t.Fatalf("primary request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("_return_fields"); got != dhcpReservationReturnFields {
			t.Fatalf("return fields = %q, want %q", got, dhcpReservationReturnFields)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if payload["ipv4addr"] != "func:nextavailableip:192.0.2.0/24,default" || payload["network_view"] != "default" || payload["mac"] != "00:11:22:33:44:66" {
			t.Fatalf("atomic payload = %#v", payload)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ipv4addr": "192.0.2.42", "mac": "00:11:22:33:44:66", "network_view": "default", "name": "phone"})
	}))
	defer primary.Close()
	read := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		readHits++
		http.Error(w, "unexpected read request", http.StatusInternalServerError)
	}))
	defer read.Close()

	app := dhcpWorkflowApp(t, primary.URL, read.URL, jsonOutput)
	if err := app.runDHCPReservationCreate("", "192.0.2.0/24", "default", "00:11:22:33:44:66", "phone", ""); err != nil {
		t.Fatalf("next-ip create: %v", err)
	}
	var row map[string]any
	if err := json.Unmarshal(app.Stdout.(*bytes.Buffer).Bytes(), &row); err != nil {
		t.Fatalf("decode next-ip output: %v", err)
	}
	if row["ip"] != "192.0.2.42" || row["network_view"] != "default" {
		t.Fatalf("next-ip output = %#v", row)
	}
	if primaryHits != 1 || readHits != 0 {
		t.Fatalf("request routing read=%d primary=%d", readHits, primaryHits)
	}
}

func TestDHCPReservationCreateNextIPRecoversMinimalResponseOnPrimary(t *testing.T) {
	var primaryHits, readHits int
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryHits++
		switch r.Method {
		case http.MethodPost:
			_ = json.NewEncoder(w).Encode(map[string]any{"_ref": "fixedaddress/recover"})
		case http.MethodGet:
			if !strings.HasSuffix(r.URL.Path, "/fixedaddress/recover") {
				t.Fatalf("recovery path = %s", r.URL.Path)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ipv4addr": "192.0.2.43", "mac": "00:11:22:33:44:66", "network_view": "default"})
		default:
			t.Fatalf("unexpected primary method %s", r.Method)
		}
	}))
	defer primary.Close()
	read := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		readHits++
		http.Error(w, "unexpected read request", http.StatusInternalServerError)
	}))
	defer read.Close()
	app := dhcpWorkflowApp(t, primary.URL, read.URL, jsonOutput)
	if err := app.runDHCPReservationCreate("", "192.0.2.0/24", "default", "00:11:22:33:44:66", "", ""); err != nil {
		t.Fatalf("next-ip minimal response: %v", err)
	}
	var row map[string]any
	if err := json.Unmarshal(app.Stdout.(*bytes.Buffer).Bytes(), &row); err != nil {
		t.Fatalf("decode recovered output: %v", err)
	}
	if row["ip"] != "192.0.2.43" {
		t.Fatalf("recovered output = %#v", row)
	}
	if primaryHits != 2 || readHits != 0 {
		t.Fatalf("recovery routing primary=%d read=%d", primaryHits, readHits)
	}
}

func TestDHCPReservationCreateNextIPRecoversRawStringReference(t *testing.T) {
	var primaryHits int
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryHits++
		switch r.Method {
		case http.MethodPost:
			_ = json.NewEncoder(w).Encode("fixedaddress/raw-recover")
		case http.MethodGet:
			if !strings.HasSuffix(r.URL.Path, "/fixedaddress/raw-recover") {
				t.Fatalf("raw reference recovery path = %s", r.URL.Path)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ipv4addr": "192.0.2.44", "mac": "00:11:22:33:44:68", "network_view": "default"})
		default:
			t.Fatalf("unexpected primary method %s", r.Method)
		}
	}))
	defer primary.Close()
	read := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unexpected read request", http.StatusInternalServerError)
	}))
	defer read.Close()
	app := dhcpWorkflowApp(t, primary.URL, read.URL, jsonOutput)
	if err := app.runDHCPReservationCreate("", "192.0.2.0/24", "default", "00:11:22:33:44:68", "", ""); err != nil {
		t.Fatalf("raw-string next-ip create: %v", err)
	}
	var row map[string]any
	if err := json.Unmarshal(app.Stdout.(*bytes.Buffer).Bytes(), &row); err != nil {
		t.Fatalf("decode raw-string recovery output: %v", err)
	}
	if row["ip"] != "192.0.2.44" {
		t.Fatalf("raw-string recovery output = %#v", row)
	}
	if primaryHits != 2 {
		t.Fatalf("raw-string recovery primary requests = %d, want 2", primaryHits)
	}
}

func TestDHCPReservationCreateNextIPMinimalResponseReportsPartialSuccess(t *testing.T) {
	var primaryHits int
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryHits++
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected recovery request: %s", r.Method)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "created"})
	}))
	defer primary.Close()
	read := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unexpected read request", http.StatusInternalServerError)
	}))
	defer read.Close()
	app := dhcpWorkflowApp(t, primary.URL, read.URL, jsonOutput)
	if err := app.runDHCPReservationCreate("", "192.0.2.0/24", "default", "00:11:22:33:44:67", "", ""); err != nil {
		t.Fatalf("partial-success create returned error: %v", err)
	}
	var row map[string]any
	if err := json.Unmarshal(app.Stdout.(*bytes.Buffer).Bytes(), &row); err != nil {
		t.Fatalf("decode partial-success output: %v", err)
	}
	if row["status"] != "success" || row["ip"] != "" {
		t.Fatalf("partial-success output = %#v", row)
	}
	if !strings.Contains(app.Stderr.(*bytes.Buffer).String(), "do not retry") {
		t.Fatalf("missing no-retry warning: %s", app.Stderr.(*bytes.Buffer).String())
	}
	if primaryHits != 1 {
		t.Fatalf("primary mutation requests = %d, want 1", primaryHits)
	}
}

func TestDHCPReservationEditUsesFilteredReadAndPrimaryWrite(t *testing.T) {
	var primaryHits, readHits int
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryHits++
		if r.Method != http.MethodPut || !strings.HasSuffix(r.URL.Path, "/fixedaddress/abc") {
			t.Fatalf("primary request = %s %s", r.Method, r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode edit payload: %v", err)
		}
		if payload["name"] != "new-name" {
			t.Fatalf("edit payload = %#v", payload)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"_ref": "fixedaddress/abc"})
	}))
	defer primary.Close()
	read := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		readHits++
		if r.Method != http.MethodGet || r.URL.Query().Get("network_view") != "blue" || r.URL.Query().Get("ipv4addr") != "192.0.2.25" {
			t.Fatalf("edit lookup = %s %s", r.Method, r.URL.RequestURI())
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{{"_ref": "fixedaddress/abc", "ipv4addr": "192.0.2.25", "mac": "00:11:22:33:44:55", "network_view": "blue"}}})
	}))
	defer read.Close()

	app := dhcpWorkflowApp(t, primary.URL, read.URL, jsonOutput)
	if err := app.runDHCPReservationEdit("192.0.2.25", "blue", map[string]any{"name": "new-name"}); err != nil {
		t.Fatalf("edit: %v", err)
	}
	if primaryHits != 1 || readHits != 1 {
		t.Fatalf("request routing read=%d primary=%d", readHits, primaryHits)
	}
}

func TestDHCPReservationDeleteUsesReadLookupAndPrimaryDelete(t *testing.T) {
	var primaryHits, readHits int
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryHits++
		if r.Method != http.MethodDelete || !strings.HasSuffix(r.URL.Path, "/fixedaddress/abc") {
			t.Fatalf("primary request = %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{})
	}))
	defer primary.Close()
	read := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		readHits++
		if r.Method != http.MethodGet || r.URL.Query().Get("network_view") != "blue" {
			t.Fatalf("delete lookup = %s %s", r.Method, r.URL.RequestURI())
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{{"_ref": "fixedaddress/abc", "ipv4addr": "192.0.2.25", "mac": "00:11:22:33:44:55", "network_view": "blue"}}})
	}))
	defer read.Close()

	app := dhcpWorkflowApp(t, primary.URL, read.URL, jsonOutput)
	if err := app.runDHCPReservationDelete("192.0.2.25", "blue", true); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if primaryHits != 1 || readHits != 1 {
		t.Fatalf("request routing read=%d primary=%d", readHits, primaryHits)
	}
}

func TestDHCPReservationRefreshAfterMutationUsesPrimary(t *testing.T) {
	var primaryGets, readGets int
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/fixedaddress") {
			t.Fatalf("primary refresh request = %s %s", r.Method, r.URL.Path)
		}
		primaryGets++
		_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{{"ipv4addr": "192.0.2.25", "network_view": "default"}}})
	}))
	defer primary.Close()
	read := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		readGets++
		http.Error(w, "refresh must not use read server", http.StatusInternalServerError)
	}))
	defer read.Close()
	app := dhcpWorkflowApp(t, primary.URL, read.URL, jsonOutput)
	t.Setenv(cacheRefreshPrimaryEnv, "1")
	if err := app.runNetCacheRefresh(defaultProfileName, dhcpReservationCacheKind, "default", ""); err != nil {
		t.Fatalf("refresh DHCP cache: %v", err)
	}
	if primaryGets != 1 || readGets != 0 {
		t.Fatalf("refresh routing primary=%d read=%d", primaryGets, readGets)
	}
}

func TestDHCPReservationNetworkViewDisambiguatesReadLookup(t *testing.T) {
	var primaryHits, readHits int
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryHits++
		http.Error(w, "unexpected write", http.StatusInternalServerError)
	}))
	defer primary.Close()
	read := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		readHits++
		if r.URL.Query().Get("network_view") == "blue" {
			_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{{"ipv4addr": "192.0.2.25", "network_view": "blue"}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{{"ipv4addr": "192.0.2.25", "network_view": "blue"}, {"ipv4addr": "192.0.2.25", "network_view": "green"}}})
	}))
	defer read.Close()

	app := dhcpWorkflowApp(t, primary.URL, read.URL, jsonOutput)
	if err := app.runDHCPReservationShow("192.0.2.25", ""); err == nil || !strings.Contains(err.Error(), "multiple DHCP reservations") {
		t.Fatalf("ambiguous lookup error = %v", err)
	}
	app.Stdout = &bytes.Buffer{}
	if err := app.runDHCPReservationShow("192.0.2.25", "blue"); err != nil {
		t.Fatalf("filtered lookup: %v", err)
	}
	if primaryHits != 0 || readHits != 2 {
		t.Fatalf("request routing read=%d primary=%d", readHits, primaryHits)
	}
}

func TestDHCPReservationTableWrapsCellsAndShowsContext(t *testing.T) {
	read := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("network_view"); got != "default" {
			t.Fatalf("network view = %q, want default", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{{
			"ipv4addr": "192.0.2.25", "mac": "00:11:22:33:44:55",
			"name": strings.Repeat("n", 75), "network_view": "default",
			"comment": "first line\nsecond line " + strings.Repeat("comment ", 12),
		}}})
	}))
	defer read.Close()
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unexpected primary request", http.StatusInternalServerError)
	}))
	defer primary.Close()
	app := dhcpWorkflowApp(t, primary.URL, read.URL, tableOutput)
	if err := app.runDHCPReservationList("", "", "", ""); err != nil {
		t.Fatalf("list: %v", err)
	}
	out := app.Stdout.(*bytes.Buffer).String()
	if !strings.Contains(out, "Current Context:") || !strings.Contains(out, "View: default") {
		t.Fatalf("missing DHCP context: %s", out)
	}
	if !strings.Contains(out, strings.Repeat("n", 50)+" ") {
		t.Fatalf("name was not wrapped at 50 columns: %s", out)
	}
	if !strings.Contains(out, "first line") || !strings.Contains(out, "second line") {
		t.Fatalf("multiline comment was not preserved: %s", out)
	}
}

func TestDHCPReservationColumnsSortAndCompletion(t *testing.T) {
	columns, err := parseDHCPReservationColumns("ip,network_view,comment")
	if err != nil || strings.Join(columns, ",") != "ip,network_view,comment" {
		t.Fatalf("columns = %#v, err = %v", columns, err)
	}
	if _, err := parseDHCPReservationColumns("ip,ip"); err == nil || !strings.Contains(err.Error(), "duplicate column") {
		t.Fatalf("duplicate columns error = %v", err)
	}
	option, err := parseDHCPReservationSort("-ip", true)
	if err != nil || !option.Desc || option.Field != "ip" {
		t.Fatalf("sort option = %#v, err = %v", option, err)
	}
	if _, err := parseDHCPReservationSort("bogus", true); err == nil || !strings.Contains(err.Error(), "unsupported sort field") {
		t.Fatalf("invalid sort error = %v", err)
	}
	rows := []map[string]any{{"ip": "192.0.2.100", "name": "b"}, {"ip": "192.0.2.2", "name": "a"}}
	applyDHCPReservationSort(rows, dhcpReservationSort{Enabled: true, Field: "ip"})
	if rows[0]["ip"] != "192.0.2.2" {
		t.Fatalf("IP sort = %#v", rows)
	}
	completions, directive := dhcpReservationSortFlagCompletion(nil, nil, "-")
	if directive != cobra.ShellCompDirectiveNoFileComp || len(completions) == 0 || completions[0] != "-ip" {
		t.Fatalf("sort completions = %#v, directive = %v", completions, directive)
	}
}

func TestNormalizeDHCPMACFormatsAndRejectsInvalidAddresses(t *testing.T) {
	cases := map[string]string{
		"00:11:22:aa:bb:cc": "00:11:22:aa:bb:cc",
		"00-11-22-AA-BB-CC": "00:11:22:aa:bb:cc",
		"0011.22AA.BBCC":    "00:11:22:aa:bb:cc",
		"001122AAbbCC":      "00:11:22:aa:bb:cc",
	}
	for input, want := range cases {
		got, err := normalizeDHCPMAC(input)
		if err != nil || got != want {
			t.Errorf("normalizeDHCPMAC(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	for _, input := range []string{"", "00:11:22:33:44", "00:11:22:33:44:55:66", "0011.2233.4455.6677", "00:11:22:33:44:gg", "00:11-22:33-44:55"} {
		if _, err := normalizeDHCPMAC(input); err == nil {
			t.Errorf("normalizeDHCPMAC(%q) accepted malformed MAC", input)
		}
	}
}

func TestDHCPReservationCreateCommandUsesPositionalMACSyntax(t *testing.T) {
	cmd := testApp(t).dhcpReservationCreateCommand()
	if cmd.Use != "create IP MAC" {
		t.Fatalf("Use = %q", cmd.Use)
	}
	if cmd.Flags().Lookup("mac") != nil || cmd.Flags().Lookup("m") != nil {
		t.Fatal("create still exposes --mac/-m")
	}
	name := cmd.Flags().Lookup("name")
	if name == nil || name.Shorthand != "n" {
		t.Fatalf("name flag = %#v, want shorthand -n", name)
	}
	if !strings.Contains(cmd.Example, "create 192.0.2.25 00:11:22:33:44:55") || !strings.Contains(cmd.Example, "--next-ip") {
		t.Fatalf("examples do not show positional syntax: %s", cmd.Example)
	}
}
