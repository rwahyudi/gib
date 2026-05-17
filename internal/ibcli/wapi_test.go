package ibcli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestWapiClientRoutesGETToReadServerAndWritesToPrimary(t *testing.T) {
	var primaryMethods []string
	var readMethods []string

	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryMethods = append(primaryMethods, r.Method)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer primary.Close()

	read := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		readMethods = append(readMethods, r.Method)
		_ = json.NewEncoder(w).Encode([]map[string]any{{"name": "default"}})
	}))
	defer read.Close()

	client := &WapiClient{
		Server:      primary.URL,
		ReadServer:  read.URL,
		WAPIVersion: defaultWAPIVersion,
		Username:    "admin",
		Password:    "secret",
		httpClient:  primary.Client(),
	}

	if _, err := client.Request(http.MethodGet, viewObject, url.Values{}, nil); err != nil {
		t.Fatalf("GET: %v", err)
	}
	if _, err := client.Request(http.MethodPost, "record:a", nil, map[string]any{"name": "app.example.com"}); err != nil {
		t.Fatalf("POST: %v", err)
	}
	if _, err := client.Request(http.MethodPut, "record:a/ref", nil, map[string]any{"comment": "updated"}); err != nil {
		t.Fatalf("PUT: %v", err)
	}
	if _, err := client.Request(http.MethodDelete, "record:a/ref", nil, nil); err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	if len(readMethods) != 1 || readMethods[0] != http.MethodGet {
		t.Fatalf("read methods = %#v", readMethods)
	}
	if len(primaryMethods) != 3 ||
		primaryMethods[0] != http.MethodPost ||
		primaryMethods[1] != http.MethodPut ||
		primaryMethods[2] != http.MethodDelete {
		t.Fatalf("primary methods = %#v", primaryMethods)
	}
}

func TestNewClientTunesTransportForParallelSearchReuse(t *testing.T) {
	app := testApp(t)
	writeConfigForSettings(t, app, ConfigSettings{DNSSearchWorkerLimit: 24})
	client := app.newClient(Profile{
		Server:      "https://infoblox.example",
		Username:    "admin",
		Password:    "secret",
		WAPIVersion: defaultWAPIVersion,
		DNSView:     "default",
		VerifySSL:   true,
	})

	transport, ok := client.httpClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T, want *http.Transport", client.httpClient.Transport)
	}
	want := 48
	if transport.MaxIdleConnsPerHost != want {
		t.Fatalf("MaxIdleConnsPerHost = %d, want %d", transport.MaxIdleConnsPerHost, want)
	}
	if transport.MaxConnsPerHost != want {
		t.Fatalf("MaxConnsPerHost = %d, want %d", transport.MaxConnsPerHost, want)
	}
	if transport.MaxIdleConns < want {
		t.Fatalf("MaxIdleConns = %d, want >= %d", transport.MaxIdleConns, want)
	}
}

func TestWapiClientExplainsNonJSONResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html><body>HTML login page</body></html>"))
	}))
	defer server.Close()

	client := &WapiClient{
		Server:      server.URL,
		WAPIVersion: defaultWAPIVersion,
		Username:    "admin",
		Password:    "secret",
		httpClient:  server.Client(),
	}

	_, err := client.Request(http.MethodPost, "record:cname", nil, map[string]any{"name": "cnametest1.example.com"})
	if err == nil {
		t.Fatal("POST with HTML response succeeded, want error")
	}
	message := err.Error()
	for _, want := range []string{
		"Infoblox WAPI 200",
		"non-JSON response for POST record:cname",
		"content-type text/html",
		"HTML login page",
		"proxy or login page",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("error missing %q:\n%s", want, message)
		}
	}
	if strings.Contains(message, "invalid character '<'") {
		t.Fatalf("error exposed raw JSON parser message:\n%s", message)
	}
}

func TestNormalizeServerStripsFullWAPIObjectURL(t *testing.T) {
	normalized, err := normalizeServer("infoblox.example/wapi/v2.12.3/record:a/ZG5zLmJpbmRfYSQuX2RlZmF1bHQuZXhhbXBsZS5jb20")
	if err != nil {
		t.Fatalf("normalize server: %v", err)
	}
	if normalized != "https://infoblox.example" {
		t.Fatalf("normalized server = %q", normalized)
	}

	normalized, err = normalizeServer("https://proxy.example/nios/wapi/v2.12.3/record:a/ref?_return_fields=name#top")
	if err != nil {
		t.Fatalf("normalize proxied server: %v", err)
	}
	if normalized != "https://proxy.example/nios" {
		t.Fatalf("normalized proxied server = %q", normalized)
	}
}

func TestNormalizeServerRejectsUnsupportedScheme(t *testing.T) {
	_, err := normalizeServer("ftp://infoblox.example")
	if err == nil {
		t.Fatal("ftp scheme succeeded")
	}
	if !strings.Contains(err.Error(), "use https:// or http://") {
		t.Fatalf("error = %v", err)
	}
}
