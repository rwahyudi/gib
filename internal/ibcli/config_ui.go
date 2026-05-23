package ibcli

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const (
	configSuccessPanelMinWidth          = 64
	configSuccessPanelHorizontalPadding = 2
)

var (
	configPanelStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("#38bdf8")).
				Width(64).
				Padding(0, 1)
	configKickerStyle       = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#67e8f9"))
	configTitleStyle        = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#4ade80"))
	configCaptionStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#cbd5e1"))
	configSectionIndexStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#facc15"))
	configSectionTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#67e8f9"))
	configSectionTextStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#94a3b8"))
	configFieldStyle        = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#cbd5e1"))
	configValueStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("#a7f3d0"))
	configEmptyValueStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#94a3b8"))
	configCommandStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#facc15"))
	configRetryPanelStyle   = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("#ef4444")).
				Width(64).
				Padding(0, 1)
	configRetryTitleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#f87171"))
	configSuccessPanelStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("#22c55e")).
				Width(configSuccessPanelMinWidth).
				Padding(0, 1)
	configSuccessTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#4ade80"))
)

func (a *App) printConfigEmptyState() {
	if !a.isTableOutput() {
		return
	}
	fmt.Fprintln(a.Stdout, renderConfigPanel(
		"Config Usage",
		"Create a profile first; credentials are encrypted.",
		[][]string{
			{"Create profile", "ib config new [PROFILE]"},
			{"Questions", "server/TLS, username, password, WAPI"},
			{"DNS defaults", "auto GCM read endpoint, DNS view, default zone"},
			{"Connection test", "runs before saving; retry on failure"},
			{"Shell completion", "ib config completion [SHELL]"},
			{"Cache tools", "ib config cache status|clear"},
			{"More help", "ib config <command> --help"},
			{"Config file", a.ConfigFile},
			{"Key file", a.ConfigKeyFile},
		},
	))
}

func (a *App) printConfigActions() {
	if !a.isTableOutput() {
		return
	}
	fmt.Fprintln(a.Stdout)
	fmt.Fprintln(a.Stdout, renderConfigPanel(
		"Config Usage",
		"Manage saved profiles, shell completion, and local cache data.",
		[][]string{
			{"Create", "ib config new [PROFILE]"},
			{"Edit", "ib config edit [PROFILE]"},
			{"Use", "ib config use PROFILE"},
			{"List", "ib config list"},
			{"Delete", "ib config delete PROFILE"},
			{"Completion", "ib config completion [SHELL]"},
			{"Cache", "ib config cache status|clear"},
			{"More help", "ib config <command> --help"},
		},
	))
}

func (a *App) printConfigureIntro(create bool, profileName string) {
	if !a.isTableOutput() {
		return
	}
	mode := "Edit existing profile"
	if create {
		mode = "Create new profile"
	}
	profile := profileName
	if profile == "" {
		profile = "prompted"
	}
	fmt.Fprintln(a.Stdout, renderConfigPanel(
		"Guided Infoblox Setup",
		"Answers are validated before saving.",
		[][]string{
			{"Mode", mode},
			{"Profile", profile},
			{"Config file", a.ConfigFile},
		},
	))
	fmt.Fprintln(a.Stdout)
}

func (a *App) printConfigureStep(index int, title string, detail string) {
	if !a.isTableOutput() {
		return
	}
	prefix := configSectionIndexStyle.Render(fmt.Sprintf("%02d", index))
	heading := configSectionTitleStyle.Render(title)
	fmt.Fprintln(a.Stdout, prefix+" "+heading)
	if strings.TrimSpace(detail) != "" {
		fmt.Fprintln(a.Stdout, "   "+configSectionTextStyle.Render(detail))
	}
}

func (a *App) printConfigureNote(message string) {
	if !a.isTableOutput() {
		a.PrintNote(message)
		return
	}
	fmt.Fprintln(a.Stdout, "   "+noteStyle.Render(message))
}

func (a *App) printConfigureSuccess(message string) {
	if !a.isTableOutput() {
		a.PrintSuccess(message)
		return
	}
	fmt.Fprintln(a.Stdout, "   "+successStyle.Render(message))
}

func (a *App) printConfigureInfo(message string) {
	if !a.isTableOutput() {
		fmt.Fprintln(a.Stdout, successStyle.Render(message))
		return
	}
	fmt.Fprintln(a.Stdout, "   "+successStyle.Render(message))
}

func (a *App) printConfigureWarning(message string) {
	if !a.isTableOutput() {
		a.PrintWarning(message)
		return
	}
	fmt.Fprintln(a.Stdout, "   "+warningStyle.Render(message))
}

func (a *App) printConfigureSummary(profile Profile, isDefault bool) {
	if !a.isTableOutput() {
		return
	}
	fmt.Fprintln(a.Stdout)
	fmt.Fprintln(a.Stdout, renderConfigSuccessPanel(profile, isDefault))
}

func renderConfigSuccessPanel(profile Profile, isDefault bool) string {
	defaultLabel := "no"
	if isDefault {
		defaultLabel = "yes"
	}
	readServer := profile.ReadServer
	if readServer == "" {
		readServer = "primary server"
	}
	defaultZone := profile.DefaultZone
	if defaultZone == "" {
		defaultZone = "not set"
	}
	rows := []map[string]any{
		{"field": "Profile", "value": profile.Name},
		{"field": "Default", "value": defaultLabel},
		{"field": "Server", "value": profile.Server},
		{"field": "Read endpoint", "value": readServer},
		{"field": "DNS view", "value": profile.DNSView},
		{"field": "Default zone", "value": defaultZone},
		{"field": "Verify SSL", "value": yesNo(profile.VerifySSL)},
		{"field": "Password", "value": "encrypted"},
		{"field": "Edit later", "value": "ib config edit " + profile.Name},
	}
	displayRows := make([][]string, 0, len(rows))
	for _, row := range rows {
		displayRows = append(displayRows, []string{stringify(row["field"]), stringify(row["value"])})
	}
	var lines []string
	lines = append(lines, configKickerStyle.Render("ib config / configure"))
	lines = append(lines, configSuccessTitleStyle.Render("Profile Saved"))
	lines = append(lines, configCaptionStyle.Render("The profile is ready for DNS commands."))
	lines = append(lines, renderConfigRows(displayRows))
	return configSuccessPanelStyle.Width(configSuccessPanelWidth(lines)).Render(strings.Join(lines, "\n"))
}

func configSuccessPanelWidth(lines []string) int {
	width := configSuccessPanelMinWidth
	for _, line := range lines {
		if lineWidth := lipgloss.Width(line) + configSuccessPanelHorizontalPadding; lineWidth > width {
			width = lineWidth
		}
	}
	return width
}

func (a *App) promptConnectionTestRetry(err error) bool {
	if a.isTableOutput() {
		fmt.Fprintln(a.Stdout)
		fmt.Fprintln(a.Stdout, renderConfigRetryPanel(configRetryErrorMessage(err)))
	} else {
		a.PrintWarning("Configuration failed: " + configRetryErrorMessage(err))
	}
	retry, retryErr := a.gum.Confirm("Do you want to retry?", true)
	if retryErr != nil {
		return false
	}
	return retry
}

func renderConfigPanel(title, caption string, rows [][]string) string {
	var lines []string
	lines = append(lines, configKickerStyle.Render("ib config / configure"))
	lines = append(lines, configTitleStyle.Render(title))
	if strings.TrimSpace(caption) != "" {
		lines = append(lines, configCaptionStyle.Render(caption))
	}
	if len(rows) > 0 {
		lines = append(lines, renderConfigRows(rows))
	}
	return configPanelStyle.Render(strings.Join(lines, "\n"))
}

func renderConfigRetryPanel(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		message = "The Infoblox connection test failed."
	}
	rows := [][]string{
		{"Status", "Connection test failed"},
		{"Error", message},
		{"Next", "Do you want to retry?"},
	}
	var lines []string
	lines = append(lines, configKickerStyle.Render("ib config / configure"))
	lines = append(lines, configRetryTitleStyle.Render("Connection Test Failed"))
	lines = append(lines, configCaptionStyle.Render("The profile was not saved. Check the endpoint, credentials, WAPI version, or TLS setting."))
	lines = append(lines, renderConfigRows(rows))
	return configRetryPanelStyle.Render(strings.Join(lines, "\n"))
}

func renderConfigRows(rows [][]string) string {
	labelWidth := 0
	for _, row := range rows {
		if len(row) > 0 && len(row[0]) > labelWidth {
			labelWidth = len(row[0])
		}
	}

	var lines []string
	for _, row := range rows {
		if len(row) == 0 {
			continue
		}
		label := row[0]
		value := ""
		if len(row) > 1 {
			value = row[1]
		}
		lines = append(lines, fmt.Sprintf("  %s  %s",
			configFieldStyle.Render(label+strings.Repeat(" ", labelWidth-len(label))),
			renderConfigValue(value),
		))
	}
	return strings.Join(lines, "\n")
}

func renderConfigValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return configEmptyValueStyle.Render("not set")
	}
	if strings.HasPrefix(value, "ib ") {
		return configCommandStyle.Render(value)
	}
	return configValueStyle.Render(value)
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}
