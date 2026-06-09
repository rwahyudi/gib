package ibcli

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

type WapiError struct {
	Status int
	Text   string
}

func (e *WapiError) Error() string {
	if e.Status > 0 {
		return fmt.Sprintf("Infoblox WAPI %d: %s", e.Status, e.Text)
	}
	return e.Text
}

type WapiClient struct {
	Server      string
	ReadServer  string
	WAPIVersion string
	Username    string
	Password    string
	View        string
	httpClient  *http.Client
	debug       func(string, ...debugField)
}

const minWAPIIdleConns = 32

func (a *App) newClient(profile Profile) *WapiClient {
	timeout := profile.Timeout
	if timeout == 0 {
		timeout = defaultTimeoutSeconds
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	a.tuneWAPITransport(transport)
	if !profile.VerifySSL || a.tlsRootCAs != nil {
		tlsConfig := &tls.Config{}
		if !profile.VerifySSL {
			tlsConfig.InsecureSkipVerify = true // #nosec G402 -- operator-controlled Infoblox profile setting
		}
		if a.tlsRootCAs != nil {
			tlsConfig.RootCAs = a.tlsRootCAs
		}
		transport.TLSClientConfig = tlsConfig
	}

	// read_server is intentionally optional. When config did not find a usable
	// Grid Master Candidate, reads and writes both go to the primary server.
	readServer := profile.ReadServer
	if readServer == "" {
		readServer = profile.Server
	}
	return &WapiClient{
		Server:      profile.Server,
		ReadServer:  readServer,
		WAPIVersion: strings.TrimLeft(profile.WAPIVersion, "/"),
		Username:    profile.Username,
		Password:    profile.Password,
		View:        profile.DNSView,
		httpClient: &http.Client{
			Timeout:   time.Duration(timeout) * time.Second,
			Transport: transport,
		},
		debug: a.debugEvent,
	}
}

func (a *App) tuneWAPITransport(transport *http.Transport) {
	if transport == nil {
		return
	}
	workerLimit := a.dnsSearchWorkerLimit()
	desiredIdleConns := workerLimit * 2
	if desiredIdleConns < minWAPIIdleConns {
		desiredIdleConns = minWAPIIdleConns
	}
	if transport.MaxIdleConns < desiredIdleConns {
		transport.MaxIdleConns = desiredIdleConns
	}
	transport.MaxIdleConnsPerHost = desiredIdleConns
	transport.MaxConnsPerHost = desiredIdleConns
}

func normalizeServer(value string) (string, error) {
	server := strings.TrimRight(strings.TrimSpace(value), "/")
	if server == "" {
		return "", cliError("Infoblox server is required")
	}
	if !strings.Contains(server, "://") {
		server = "https://" + server
	}
	parsed, err := url.Parse(server)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", cliError("invalid Infoblox server URL: %s", value)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return "", cliError("invalid Infoblox server URL scheme %q; use https:// or http://", parsed.Scheme)
	}

	// Operators often paste a full WAPI URL. Store only the appliance base URL
	// so versioned paths are generated consistently by endpoint().
	parsed.Path = stripWAPIPath(strings.TrimRight(parsed.Path, "/"))
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/"), nil
}

func stripWAPIPath(path string) string {
	lower := strings.ToLower(path)
	if index := strings.Index(lower, "/wapi/v"); index >= 0 {
		return strings.TrimRight(path[:index], "/")
	}
	return path
}

func (c *WapiClient) Request(method, objectPath string, params url.Values, payload any) (any, error) {
	method = strings.ToUpper(strings.TrimSpace(method))

	// Only GET is allowed to use the read endpoint. All write verbs stay on the
	// primary Grid Master because GCM read-only API support is not writable.
	base := c.Server
	if method == http.MethodGet && c.ReadServer != "" {
		base = c.ReadServer
	}
	target := "primary"
	if method == http.MethodGet && c.ReadServer != "" && base == c.ReadServer {
		target = "read"
	}
	endpoint := c.endpoint(base, objectPath, params)
	started := time.Now()
	c.debugEvent("wapi start", df("method", method), df("object", objectPath), df("target", target), df("params", safeWAPIParamSummary(params)), df("payload", payload != nil))

	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, endpoint, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(c.Username+":"+c.Password)))
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		wrapped := cliError("cannot reach Infoblox: %v", err)
		c.debugEvent("wapi error", df("method", method), df("object", objectPath), df("target", target), df("duration", time.Since(started)), df("error", wrapped.Error()))
		return nil, wrapped
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		c.debugEvent("wapi error", df("method", method), df("object", objectPath), df("target", target), df("status", resp.StatusCode), df("duration", time.Since(started)), df("error", err.Error()))
		return nil, err
	}
	if resp.StatusCode >= 400 {
		wapiErr := &WapiError{Status: resp.StatusCode, Text: formatWapiError(raw)}
		c.debugEvent("wapi error", df("method", method), df("object", objectPath), df("target", target), df("status", resp.StatusCode), df("bytes", len(raw)), df("duration", time.Since(started)), df("error", wapiErr.Error()))
		return nil, wapiErr
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		c.debugEvent("wapi done", df("method", method), df("object", objectPath), df("target", target), df("status", resp.StatusCode), df("bytes", len(raw)), df("duration", time.Since(started)))
		return nil, nil
	}
	var result any
	if err := json.Unmarshal(raw, &result); err != nil {
		c.debugEvent("wapi error", df("method", method), df("object", objectPath), df("target", target), df("status", resp.StatusCode), df("duration", time.Since(started)), df("error", err.Error()))
		return nil, nonJSONWapiResponseError(method, objectPath, resp, raw)
	}
	c.debugEvent("wapi done", df("method", method), df("object", objectPath), df("target", target), df("status", resp.StatusCode), df("bytes", len(raw)), df("duration", time.Since(started)))
	return result, nil
}

func (c *WapiClient) debugEvent(event string, fields ...debugField) {
	if c != nil && c.debug != nil {
		c.debug(event, fields...)
	}
}

func (c *WapiClient) endpoint(base, objectPath string, params url.Values) string {
	path := strings.TrimRight(base, "/") + "/wapi/" + strings.Trim(c.WAPIVersion, "/") + "/" + strings.TrimLeft(objectPath, "/")
	if encoded := params.Encode(); encoded != "" {
		path += "?" + encoded
	}
	return path
}

func formatWapiError(raw []byte) string {
	text := strings.TrimSpace(string(raw))
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err == nil {
		for _, key := range []string{"text", "Error", "error"} {
			if value := strings.TrimSpace(fmt.Sprint(payload[key])); value != "" && value != "<nil>" {
				return value
			}
		}
	}
	if text == "" {
		return "empty error response"
	}
	return text
}

func nonJSONWapiResponseError(method, objectPath string, resp *http.Response, raw []byte) error {
	details := []string{fmt.Sprintf("status %d", resp.StatusCode)}
	if contentType := strings.TrimSpace(resp.Header.Get("Content-Type")); contentType != "" {
		details = append(details, "content-type "+contentType)
	}
	message := fmt.Sprintf(
		"non-JSON response for %s %s (%s)",
		strings.ToUpper(strings.TrimSpace(method)),
		strings.TrimLeft(objectPath, "/"),
		strings.Join(details, ", "),
	)
	if snippet := responseSnippet(raw); snippet != "" {
		message += ": " + snippet
	}
	message += ". Check the configured Infoblox server, WAPI version, authentication, and any proxy or login page in front of WAPI."
	return &WapiError{Status: resp.StatusCode, Text: message}
}

func responseSnippet(raw []byte) string {
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return ""
	}
	text = strings.Join(strings.Fields(text), " ")
	const maxSnippet = 180
	if len(text) > maxSnippet {
		return text[:maxSnippet] + "..."
	}
	return text
}

func pagedQuery(client *WapiClient, objectType string, params url.Values) ([]map[string]any, error) {
	// Infoblox returns either a plain list or an object with result/next_page_id
	// depending on paging support and object type. Handle both shapes so callers
	// can request "all rows" without duplicating pagination branches.
	pageParams := cloneValues(params)
	pageParams.Set("_paging", "1")
	pageParams.Set("_return_as_object", "1")
	pageParams.Set("_max_results", fmt.Sprint(wapiPageSize))

	var results []map[string]any
	pageID := ""
	for {
		requestParams := cloneValues(pageParams)
		if pageID != "" {
			requestParams.Set("_page_id", pageID)
		}
		response, err := client.Request(http.MethodGet, objectType, requestParams, nil)
		if err != nil {
			return nil, err
		}
		switch typed := response.(type) {
		case []any:
			return append(results, mapSliceFromAny(typed)...), nil
		case map[string]any:
			results = append(results, mapSliceFromAny(typed["result"])...)
			pageID = strings.TrimSpace(fmt.Sprint(typed["next_page_id"]))
			if pageID == "" || pageID == "<nil>" {
				return results, nil
			}
		default:
			return results, nil
		}
	}
}

func mapSliceFromAny(value any) []map[string]any {
	var rows []map[string]any
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			if row, ok := item.(map[string]any); ok {
				rows = append(rows, row)
			}
		}
	case []map[string]any:
		rows = append(rows, typed...)
	}
	return rows
}

func cloneValues(values url.Values) url.Values {
	cloned := url.Values{}
	for key, list := range values {
		for _, value := range list {
			cloned.Add(key, value)
		}
	}
	return cloned
}

func safeWAPIParamSummary(params url.Values) string {
	if len(params) == 0 {
		return ""
	}
	safeKeys := []string{"_function", "_max_results", "_page_id", "_return_fields", "fqdn", "name", "network", "network_view", "view"}
	var parts []string
	for _, key := range safeKeys {
		values := params[key]
		if len(values) == 0 {
			continue
		}
		parts = append(parts, key+"="+strings.Join(values, ","))
	}
	if len(parts) == 0 {
		for key := range params {
			parts = append(parts, key)
		}
		sort.Strings(parts)
		return strings.Join(parts, ",")
	}
	return strings.Join(parts, ",")
}
