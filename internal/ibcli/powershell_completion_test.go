package ibcli

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPowerShellProfileBlockQuotesScriptPath(t *testing.T) {
	block := powerShellProfileBlock(`C:\Users\O'Brien\.ib\ib-completion.ps1`)
	want := `$ibCompletion = 'C:\Users\O''Brien\.ib\ib-completion.ps1'`
	if !strings.Contains(block, want) {
		t.Fatalf("PowerShell profile block missing quoted path %q:\n%s", want, block)
	}
}

func TestUpsertPowerShellProfileBlockAppendsAndReplaces(t *testing.T) {
	first := powerShellProfileBlock(`/tmp/one/ib-completion.ps1`)
	second := powerShellProfileBlock(`/tmp/two/ib-completion.ps1`)

	updated := upsertPowerShellProfileBlock("Write-Host 'before'\n", first)
	if !strings.Contains(updated, "Write-Host 'before'") {
		t.Fatalf("profile content was not preserved:\n%s", updated)
	}
	if !strings.Contains(updated, `/tmp/one/ib-completion.ps1`) {
		t.Fatalf("profile block was not appended:\n%s", updated)
	}

	updated = upsertPowerShellProfileBlock(updated, second)
	if strings.Count(updated, powerShellProfileStart) != 1 {
		t.Fatalf("profile block was duplicated:\n%s", updated)
	}
	if strings.Contains(updated, `/tmp/one/ib-completion.ps1`) {
		t.Fatalf("old profile block remained after update:\n%s", updated)
	}
	if !strings.Contains(updated, `/tmp/two/ib-completion.ps1`) {
		t.Fatalf("new profile block missing after update:\n%s", updated)
	}
}

func TestDynamicPowerShellCompletionScriptUsesCobraCompletion(t *testing.T) {
	script := dynamicPowerShellCompletionScript()
	for _, want := range []string{
		"Register-ArgumentCompleter -Native -CommandName @('ib', 'ib.exe')",
		"function __ibCompletionWords",
		"[System.Management.Automation.PSParser]::Tokenize",
		"$requestArgs += @($commandWords[1..($commandWords.Count - 1)])",
		"$env:IB_ACTIVE_HELP = '0'",
		"$env:IB_SHELL_PID = [string]$PID",
		"$requestArgs = @('__complete')",
		"$parts = $line -split ([char]9), 2",
		"$line.StartsWith('Completion ended with directive:')",
		"[System.Management.Automation.CompletionResult]::new",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("PowerShell completion script missing %q:\n%s", want, script)
		}
	}
	if strings.Contains(script, `-split "\t"`) {
		t.Fatalf("PowerShell completion script uses literal backslash-t split:\n%s", script)
	}
}

func TestWindowsPowerShellProfilePathsIncludesKnownAndDiscoveredLocations(t *testing.T) {
	home := t.TempDir()
	oneDrive := filepath.Join(home, "OneDrive")
	discovered := filepath.Join(home, "RedirectedDocuments", "PowerShell", "Microsoft.PowerShell_profile.ps1")
	t.Setenv("OneDrive", oneDrive)
	t.Setenv("OneDriveCommercial", "")
	t.Setenv("OneDriveConsumer", "")

	oldDiscoverer := powerShellProfilePathDiscoverer
	powerShellProfilePathDiscoverer = func(string) []string {
		return []string{
			filepath.Join(oneDrive, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1"),
			discovered,
		}
	}
	t.Cleanup(func() {
		powerShellProfilePathDiscoverer = oldDiscoverer
	})

	paths := windowsPowerShellProfilePaths(home)
	for _, want := range []string{
		filepath.Join(home, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1"),
		filepath.Join(home, "Documents", "WindowsPowerShell", "Microsoft.PowerShell_profile.ps1"),
		filepath.Join(oneDrive, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1"),
		filepath.Join(oneDrive, "Documents", "WindowsPowerShell", "Microsoft.PowerShell_profile.ps1"),
		discovered,
	} {
		if !containsPath(paths, want) {
			t.Fatalf("profile paths missing %q: %#v", want, paths)
		}
	}
	if countPath(paths, filepath.Join(oneDrive, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1")) != 1 {
		t.Fatalf("profile paths did not deduplicate OneDrive PowerShell profile: %#v", paths)
	}
}

func containsPath(paths []string, want string) bool {
	return countPath(paths, want) > 0
}

func countPath(paths []string, want string) int {
	count := 0
	for _, path := range paths {
		if strings.EqualFold(filepath.Clean(path), filepath.Clean(want)) {
			count++
		}
	}
	return count
}
