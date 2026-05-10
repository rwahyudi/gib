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
	"regexp"
	"strings"
	"time"
)

var wapiSuffixRE = regexp.MustCompile(`/wapi/v[0-9][^/]*$`)

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
}

func (a *App) newClient(profile Profile) *WapiClient {
	timeout := profile.Timeout
	if timeout == 0 {
		timeout = defaultTimeoutSeconds
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if !profile.VerifySSL {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // operator-controlled Infoblox profile setting
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
	}
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

	// Operators often paste a full WAPI URL. Store only the appliance base URL
	// so versioned paths are generated consistently by endpoint().
	parsed.Path = wapiSuffixRE.ReplaceAllString(strings.TrimRight(parsed.Path, "/"), "")
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/"), nil
}

func (c *WapiClient) Request(method, objectPath string, params url.Values, payload any) (any, error) {
	method = strings.ToUpper(strings.TrimSpace(method))

	// Only GET is allowed to use the read endpoint. All write verbs stay on the
	// primary Grid Master because GCM read-only API support is not writable.
	base := c.Server
	if method == http.MethodGet && c.ReadServer != "" {
		base = c.ReadServer
	}
	endpoint := c.endpoint(base, objectPath, params)

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
		return nil, cliError("cannot reach Infoblox: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, &WapiError{Status: resp.StatusCode, Text: formatWapiError(raw)}
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	var result any
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return result, nil
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
