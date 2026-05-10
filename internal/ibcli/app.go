package ibcli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

const (
	configDirName         = ".ib"
	configFileName        = "config"
	configKeyFileName     = "key"
	defaultWAPIVersion    = "v2.12.3"
	defaultTimeoutSeconds = 30
	defaultProfileName    = "default"
	defaultZoneEnv        = "IB_ZONE"
	defaultViewEnv        = "IB_VIEW"
	tableOutput           = "table"
	jsonOutput            = "jq"
	csvOutput             = "csv"
	recordOutputType      = "RECORD"
	zoneObject            = "zone_auth"
	viewObject            = "view"
	gridObject            = "grid"
	memberObject          = "member"
	allRecordsObject      = "allrecords"
	wapiPageSize          = 2000
)

var (
	errUsageDisplayed  = errors.New("usage displayed")
	errDeleteCancelled = errors.New("delete cancelled")

	successStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#4ade80"))
	warningStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#facc15"))
	errorStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ef4444"))
	noteStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#22d3ee"))

	contextTitleStyle        = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#67e8f9"))
	contextLabelStyle        = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#cbd5e1"))
	contextProfileValueStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#facc15"))
	contextViewValueStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#a78bfa"))
	contextZoneValueStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#4ade80"))
	contextSourceStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("#94a3b8"))
	recordTotalBadgeStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#e0f2fe")).Background(lipgloss.Color("#1d4ed8")).Padding(0, 1)
)

type App struct {
	ConfigDir     string
	ConfigFile    string
	ConfigKeyFile string
	Output        string
	Stdout        io.Writer
	Stderr        io.Writer
	Stdin         io.Reader
	gum           *Gum

	dnsZoneOverride             string
	dnsViewOverride             string
	backgroundRecordRevalidator func(Profile, string) error
	dnsDeleteRecordSelector     func(string, []TypedRecord) (TypedRecord, bool, error)
	dnsDeleteConfirmer          func(string, TypedRecord) (bool, error)
}

func NewDefaultApp() (*App, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cannot locate home directory: %w", err)
	}
	configDir := filepath.Join(home, configDirName)
	app := &App{
		ConfigDir:     configDir,
		ConfigFile:    filepath.Join(configDir, configFileName),
		ConfigKeyFile: filepath.Join(configDir, configKeyFileName),
		Output:        tableOutput,
		Stdout:        os.Stdout,
		Stderr:        os.Stderr,
		Stdin:         os.Stdin,
	}
	app.gum = NewGum(app.Stdin, app.Stdout, app.Stderr)
	return app, nil
}

func (a *App) Execute(args []string) error {
	root := a.RootCommand()
	root.SetOut(a.Stdout)
	root.SetErr(a.Stderr)
	root.SetIn(a.Stdin)
	root.SetArgs(args)
	cmd, err := root.ExecuteC()
	if err == nil {
		return nil
	}
	if errors.Is(err, errUsageDisplayed) {
		return err
	}
	if isUsageError(err) && cmd != nil {
		if usageErr := cmd.Usage(); usageErr != nil {
			return usageErr
		}
		return errUsageDisplayed
	}
	return rewriteRootModuleError(err)
}

func (a *App) RootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:               "ib",
		Short:             "Infoblox DNS command line client",
		SilenceUsage:      true,
		SilenceErrors:     true,
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
		Long: strings.TrimSpace(`Infoblox DNS command line client.

Run "ib config new [PROFILE]" first to save an Infoblox profile with server,
credentials, DNS view, and default zone.

Common usage:
  ib dns view list
  ib dns view use "DNS Zone View"
  ib dns zone list
	  ib dns zone use example.com
	  ib dns list
	  ib dns search app
	  ib dns create app host 192.0.2.10 -c "Application host"
	  ib dns edit app host 192.0.2.20 -t 300 -c "Application host"
	  ib dns delete app`),
	}

	root.PersistentFlags().StringVarP(
		&a.Output,
		"output",
		"o",
		tableOutput,
		"output format: table, jq, or csv",
	)
	_ = root.RegisterFlagCompletionFunc("output", outputFormatCompletion)
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		a.Output = strings.ToLower(strings.TrimSpace(a.Output))
		switch a.Output {
		case "", tableOutput:
			a.Output = tableOutput
		case jsonOutput, csvOutput:
		default:
			return fmt.Errorf("unsupported output format %q; use table, jq, or csv", a.Output)
		}
		return nil
	}

	root.AddCommand(a.configCommand())
	root.AddCommand(a.dnsCommand())
	a.installHelp(root)
	return root
}

func (a *App) PrintError(err error) {
	if err == nil {
		return
	}
	if errors.Is(err, errUsageDisplayed) {
		return
	}
	var cliErr *CliError
	if errors.As(err, &cliErr) {
		fmt.Fprintln(a.Stderr, errorStyle.Render(cliErr.Error()))
		return
	}
	fmt.Fprintln(a.Stderr, errorStyle.Render("ERROR: "+err.Error()))
}

func (a *App) PrintSuccess(message string) {
	fmt.Fprintln(a.Stdout, successStyle.Render(message))
}

func (a *App) PrintInfo(message string) {
	fmt.Fprintln(a.Stdout, noteStyle.Render(message))
}

func (a *App) PrintWarning(message string) {
	fmt.Fprintln(a.Stderr, warningStyle.Render(message))
}

func (a *App) PrintNote(message string) {
	fmt.Fprintln(a.Stdout, noteStyle.Render(message))
}

func (a *App) PrintContext() {
	fmt.Fprintln(a.Stdout, a.dnsContextLine())
}

func renderContextPair(label, value string, valueStyle lipgloss.Style) string {
	return contextLabelStyle.Render(label+":") + " " + valueStyle.Render(value)
}

func exactArgsOrUsage(want int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) == want {
			return nil
		}
		if err := cmd.Usage(); err != nil {
			return err
		}
		return errUsageDisplayed
	}
}

func isUsageError(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.HasPrefix(message, "accepts ") ||
		strings.HasPrefix(message, "requires ") ||
		strings.Contains(message, " arg(s)") ||
		strings.Contains(message, "only received")
}

func (a *App) isTableOutput() bool {
	return a.Output == "" || a.Output == tableOutput
}

type CliError struct {
	Message string
}

func (e *CliError) Error() string {
	if strings.HasPrefix(e.Message, "ERROR: ") {
		return e.Message
	}
	return "ERROR: " + e.Message
}

func cliError(format string, args ...any) *CliError {
	return &CliError{Message: fmt.Sprintf(format, args...)}
}

func rewriteRootModuleError(err error) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	if strings.HasPrefix(message, "unknown command ") && strings.HasSuffix(message, ` for "ib"`) {
		return cliError("%s", strings.Replace(message, "unknown command", "unknown module", 1))
	}
	return err
}

func outputFormatCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	candidates := []string{
		tableOutput + "\tstyled table output",
		jsonOutput + "\tJSON output for jq",
		csvOutput + "\tCSV output",
	}
	toComplete = strings.ToLower(strings.TrimSpace(toComplete))
	if toComplete == "" {
		return candidates, cobra.ShellCompDirectiveNoFileComp
	}
	filtered := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		value := strings.SplitN(candidate, "\t", 2)[0]
		if strings.HasPrefix(value, toComplete) {
			filtered = append(filtered, candidate)
		}
	}
	return filtered, cobra.ShellCompDirectiveNoFileComp
}
