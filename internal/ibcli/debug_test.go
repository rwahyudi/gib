package ibcli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestDebugFlagWritesStderrAndLeavesJSONStdoutValid(t *testing.T) {
	app := testApp(t)
	writePlainTestConfig(t, app.ConfigFile, "demo", map[string]Profile{
		"demo": plainTestProfile("demo", "https://infoblox.example"),
	}, "")
	var stdout, stderr bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &stderr
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"--debug", "-o", "json", "config", "list"}); err != nil {
		t.Fatalf("config list debug json: %v", err)
	}
	if !json.Valid(bytes.TrimSpace(stdout.Bytes())) {
		t.Fatalf("stdout is not valid JSON:\n%s", stdout.String())
	}
	output := stderr.String()
	for _, want := range []string{
		"DEBUG ",
		"execute start",
		"command start",
		"path=\"ib config list\"",
		"command done",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("stderr debug output missing %q:\n%s", want, output)
		}
	}
}
