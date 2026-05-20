package ibcli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	windowsCompletionScriptName = "ib-completion.ps1"
	powerShellProfileStart      = "# BEGIN ib shell completion"
	powerShellProfileEnd        = "# END ib shell completion"
)

var powerShellProfilePathDiscoverer = discoverPowerShellProfilePaths

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
	var paths []string
	appendPowerShellProfilePaths(&paths, filepath.Join(home, "Documents"))
	for _, key := range []string{"OneDrive", "OneDriveCommercial", "OneDriveConsumer"} {
		if base := strings.TrimSpace(os.Getenv(key)); base != "" {
			appendPowerShellProfilePaths(&paths, filepath.Join(base, "Documents"))
		}
	}
	for _, profilePath := range powerShellProfilePathDiscoverer(home) {
		appendUniquePath(&paths, profilePath)
	}
	return paths
}

func appendPowerShellProfilePaths(paths *[]string, documentsDir string) {
	if strings.TrimSpace(documentsDir) == "" {
		return
	}
	appendUniquePath(paths, filepath.Join(documentsDir, "PowerShell", "Microsoft.PowerShell_profile.ps1"))
	appendUniquePath(paths, filepath.Join(documentsDir, "WindowsPowerShell", "Microsoft.PowerShell_profile.ps1"))
}

func appendUniquePath(paths *[]string, path string) {
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}
	cleaned := filepath.Clean(path)
	for _, existing := range *paths {
		if strings.EqualFold(filepath.Clean(existing), cleaned) {
			return
		}
	}
	*paths = append(*paths, cleaned)
}

func discoverPowerShellProfilePaths(_ string) []string {
	var paths []string
	for _, shell := range []string{"pwsh.exe", "powershell.exe"} {
		profilePath, err := currentUserPowerShellProfilePath(shell)
		if err != nil {
			continue
		}
		appendUniquePath(&paths, profilePath)
	}
	return paths
}

func currentUserPowerShellProfilePath(shell string) (string, error) {
	shellPath, err := exec.LookPath(shell)
	if err != nil {
		return "", err
	}
	cmd := exec.Command(shellPath, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", "$PROFILE.CurrentUserCurrentHost")
	raw, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
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
Register-ArgumentCompleter -Native -CommandName @('ib', 'ib.exe') -ScriptBlock {
  param($wordToComplete, $commandAst, $cursorPosition)

  if ($null -eq $wordToComplete) {
    $wordToComplete = ''
  }

  function __ibCompletionWords {
    param($ast)

    $words = @($ast.CommandElements | ForEach-Object { $_.Extent.Text } | Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
    $line = $ast.Extent.Text
    if ([string]::IsNullOrWhiteSpace($line)) {
      $line = $ast.ToString()
    }

    $parseErrors = $null
    $tokens = @([System.Management.Automation.PSParser]::Tokenize($line, [ref]$parseErrors) |
      Where-Object { $_.Type -in @('Command', 'CommandArgument', 'String', 'Number', 'Parameter') } |
      Sort-Object Start |
      ForEach-Object { $_.Content } |
      Where-Object { -not [string]::IsNullOrWhiteSpace($_) })

    if ($tokens.Count -gt $words.Count) {
      $words = $tokens
    }
    return @($words)
  }

  $commandWords = @(__ibCompletionWords $commandAst)
  if ($commandWords.Count -eq 0) {
    return
  }

  $commandName = $commandWords[0]
  if ([string]::IsNullOrWhiteSpace($commandName)) {
    $commandName = 'ib'
  }

  $requestArgs = @('__complete')
  if ($commandWords.Count -gt 1) {
    $requestArgs += @($commandWords[1..($commandWords.Count - 1)])
  }
  if ($wordToComplete -eq '' -or $requestArgs.Count -eq 1 -or $requestArgs[-1] -ne $wordToComplete) {
    $requestArgs += $wordToComplete
  }
  $allowNonPrefix = $false
  if ($wordToComplete -like '*/*' -and $commandWords.Count -ge 3) {
    $commandPath = "$($commandWords[1]) $($commandWords[2])"
    if ($commandPath -in @('dns next-ip', 'net next-ip', 'net show')) {
      $allowNonPrefix = $true
    }
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
    if ([string]::IsNullOrWhiteSpace($line) -or $line.StartsWith(':') -or $line.StartsWith('Completion ended with directive:')) {
      continue
    }
    $parts = $line -split ([char]9), 2
    $value = $parts[0]
    if ([string]::IsNullOrWhiteSpace($value)) {
      continue
    }
    $tooltip = $value
    if ($parts.Count -gt 1 -and -not [string]::IsNullOrWhiteSpace($parts[1])) {
      $tooltip = $parts[1]
    }
    if ($allowNonPrefix -or $wordToComplete -eq '' -or $value.StartsWith($wordToComplete, [System.StringComparison]::OrdinalIgnoreCase)) {
      [System.Management.Automation.CompletionResult]::new($value, $value, 'ParameterValue', $tooltip)
    }
  }
}
`
}
