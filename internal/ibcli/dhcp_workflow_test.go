package ibcli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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

func TestDHCPReservationCreateValidatesAllocationSourceAndMAC(t *testing.T) {
	app := testApp(t)
	if err := app.runDHCPReservationCreate("192.0.2.25", "192.0.2.0/24", "", "00:11:22:33:44:55", "", ""); err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("both allocation sources error = %v", err)
	}
	if err := app.runDHCPReservationCreate("192.0.2.25", "", "", "", "", ""); err == nil || !strings.Contains(err.Error(), "--mac is required") {
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
