package ibcli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	windowsCompletionScriptName = "ib-completion.ps1"
	powerShellProfileStart      = "# BEGIN ib shell completion"
	powerShellProfileEnd        = "# END ib shell completion"
)

func windowsCompletionAvailable() bool {
	return runtime.GOOS == "windows"
}

func shellCompletionCandidates() []string {
	candidates := []string{
		"bash\tBash completion script",
		"zsh\tZsh completion script",
		"fish\tFish completion script",
	}
	if windowsCompletionAvailable() {
		candidates = append(candidates, "windows\tPowerShell completion installer")
	}
	return candidates
}

func (a *App) runWindowsCompletionSetup() error {
	if !windowsCompletionAvailable() {
		return cliError("windows completion setup is only available on Windows")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return cliError("cannot locate home directory: %v", err)
	}
	scriptPath, profilePaths, err := a.installPowerShellCompletion(home)
	if err != nil {
		return err
	}
	a.PrintSuccess("SUCCESS: installed PowerShell completion")
	fmt.Fprintf(a.Stdout, "  Script: %s\n", scriptPath)
	fmt.Fprintln(a.Stdout, "  Profiles:")
	for _, profilePath := range profilePaths {
		fmt.Fprintf(a.Stdout, "    %s\n", profilePath)
	}
	fmt.Fprintln(a.Stdout, "Open a new PowerShell window to load completion.")
	return nil
}

func (a *App) installPowerShellCompletion(home string) (string, []string, error) {
	if strings.TrimSpace(home) == "" {
		return "", nil, cliError("cannot locate home directory")
	}
	if err := a.ensureConfigDir(); err != nil {
		return "", nil, err
	}
	scriptPath := filepath.Join(a.ConfigDir, windowsCompletionScriptName)
	if err := os.WriteFile(scriptPath, []byte(dynamicPowerShellCompletionScript()), 0o600); err != nil {
		return "", nil, err
	}
	if err := protectPrivateFile(scriptPath); err != nil {
		return "", nil, err
	}

	profilePaths := windowsPowerShellProfilePaths(home)
	block := powerShellProfileBlock(scriptPath)
	for _, profilePath := range profilePaths {
		if err := os.MkdirAll(filepath.Dir(profilePath), 0o700); err != nil {
			return "", nil, err
		}
		raw, err := os.ReadFile(profilePath)
		if err != nil && !os.IsNotExist(err) {
			return "", nil, err
		}
		updated := upsertPowerShellProfileBlock(string(raw), block)
		if err := os.WriteFile(profilePath, []byte(updated), 0o644); err != nil {
			return "", nil, err
		}
	}
	return scriptPath, profilePaths, nil
}

func windowsPowerShellProfilePaths(home string) []string {
	return []string{
		filepath.Join(home, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1"),
		filepath.Join(home, "Documents", "WindowsPowerShell", "Microsoft.PowerShell_profile.ps1"),
	}
}

func powerShellProfileBlock(scriptPath string) string {
	return fmt.Sprintf(`%s
$ibCompletion = %s
if (Test-Path -LiteralPath $ibCompletion) {
  . $ibCompletion
}
%s
`, powerShellProfileStart, powerShellSingleQuote(scriptPath), powerShellProfileEnd)
}

func upsertPowerShellProfileBlock(content string, block string) string {
	for {
		start := strings.Index(content, powerShellProfileStart)
		if start < 0 {
			break
		}
		endRel := strings.Index(content[start:], powerShellProfileEnd)
		if endRel < 0 {
			break
		}
		end := start + endRel + len(powerShellProfileEnd)
		for end < len(content) && (content[end] == '\r' || content[end] == '\n') {
			end++
		}
		before := strings.TrimRight(content[:start], "\r\n")
		after := strings.TrimLeft(content[end:], "\r\n")
		switch {
		case before == "" && after == "":
			content = ""
		case before == "":
			content = after
		case after == "":
			content = before
		default:
			content = before + "\n\n" + after
		}
	}
	content = strings.TrimRight(content, "\r\n")
	if content == "" {
		return block
	}
	return content + "\n\n" + block
}

func powerShellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func dynamicPowerShellCompletionScript() string {
	return `# PowerShell completion for ib
Register-ArgumentCompleter -Native -CommandName 'ib' -ScriptBlock {
  param($wordToComplete, $commandAst, $cursorPosition)

  if ($null -eq $wordToComplete) {
    $wordToComplete = ''
  }

  $commandElements = @($commandAst.CommandElements)
  if ($commandElements.Count -eq 0) {
    return
  }

  $commandName = $commandElements[0].Extent.Text
  if ([string]::IsNullOrWhiteSpace($commandName)) {
    $commandName = 'ib'
  }

  $requestArgs = @('__complete')
  if ($commandElements.Count -gt 1) {
    $requestArgs += @($commandElements[1..($commandElements.Count - 1)] | ForEach-Object { $_.Extent.Text })
  }
  if ($requestArgs.Count -eq 1 -or $requestArgs[-1] -ne $wordToComplete) {
    $requestArgs += $wordToComplete
  }

  $oldActiveHelp = $env:IB_ACTIVE_HELP
  $oldShellPid = $env:IB_SHELL_PID
  try {
    $env:IB_ACTIVE_HELP = '0'
    $env:IB_SHELL_PID = [string]$PID
    $output = & $commandName @requestArgs 2>$null
  } finally {
    if ($null -eq $oldActiveHelp) {
      Remove-Item Env:\IB_ACTIVE_HELP -ErrorAction SilentlyContinue
    } else {
      $env:IB_ACTIVE_HELP = $oldActiveHelp
    }
    if ($null -eq $oldShellPid) {
      Remove-Item Env:\IB_SHELL_PID -ErrorAction SilentlyContinue
    } else {
      $env:IB_SHELL_PID = $oldShellPid
    }
  }

  foreach ($line in $output) {
    if ([string]::IsNullOrWhiteSpace($line) -or $line.StartsWith(':')) {
      continue
    }
    $parts = $line -split "\t", 2
    $value = $parts[0]
    if ([string]::IsNullOrWhiteSpace($value)) {
      continue
    }
    $tooltip = $value
    if ($parts.Count -gt 1 -and -not [string]::IsNullOrWhiteSpace($parts[1])) {
      $tooltip = $parts[1]
    }
    if ($wordToComplete -eq '' -or $value.StartsWith($wordToComplete, [System.StringComparison]::OrdinalIgnoreCase)) {
      [System.Management.Automation.CompletionResult]::new($value, $value, 'ParameterValue', $tooltip)
    }
  }
}
`
}
