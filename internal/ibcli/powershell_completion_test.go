package ibcli

import (
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
		"Register-ArgumentCompleter -Native -CommandName 'ib'",
		"$env:IB_ACTIVE_HELP = '0'",
		"$env:IB_SHELL_PID = [string]$PID",
		"$requestArgs = @('__complete')",
		"[System.Management.Automation.CompletionResult]::new",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("PowerShell completion script missing %q:\n%s", want, script)
		}
	}
}
