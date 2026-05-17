//go:build windows

package ibcli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigCompletionWindowsCommandInstallsPowerShellCompletion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	oldDiscoverer := powerShellProfilePathDiscoverer
	powerShellProfilePathDiscoverer = func(string) []string { return nil }
	t.Cleanup(func() {
		powerShellProfilePathDiscoverer = oldDiscoverer
	})

	app := testApp(t)
	var stdout bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &bytes.Buffer{}
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"config", "completion", "windows"}); err != nil {
		t.Fatalf("windows completion setup: %v", err)
	}
	output := stdout.String()
	scriptPath := filepath.Join(app.ConfigDir, windowsCompletionScriptName)
	for _, want := range []string{
		"SUCCESS: installed PowerShell completion",
		scriptPath,
		filepath.Join(home, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1"),
		filepath.Join(home, "Documents", "WindowsPowerShell", "Microsoft.PowerShell_profile.ps1"),
		"Open a new PowerShell window to load completion.",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("windows completion setup output missing %q:\n%s", want, output)
		}
	}

	raw, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("read PowerShell completion script: %v", err)
	}
	if !strings.Contains(string(raw), "Register-ArgumentCompleter -Native -CommandName 'ib'") {
		t.Fatalf("PowerShell completion script missing argument completer:\n%s", raw)
	}
}

func TestConfigCompletionIncludesWindowsCandidateOnWindows(t *testing.T) {
	app := testApp(t)
	var stdout bytes.Buffer
	app.Stdout = &stdout
	app.Stderr = &bytes.Buffer{}
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)

	if err := app.Execute([]string{"__complete", "config", "completion", ""}); err != nil {
		t.Fatalf("completion: %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, "windows\tPowerShell completion installer") {
		t.Fatalf("completion output missing windows candidate:\n%s", output)
	}
}
