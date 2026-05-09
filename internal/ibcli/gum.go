package ibcli

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-isatty"
)

type Gum struct {
	in     io.Reader
	out    io.Writer
	err    io.Writer
	reader *bufio.Reader
}

var (
	gumPromptIndent        = "   "
	gumPromptMarkerStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#67e8f9"))
	gumPromptLabelStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#cbd5e1"))
	gumPromptDefaultStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#94a3b8"))
	gumChoiceHeaderStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#67e8f9"))
	gumChoiceIndexStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#facc15"))
	gumChoiceSelectedStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#4ade80"))
)

func NewGum(in io.Reader, out io.Writer, err io.Writer) *Gum {
	return &Gum{in: in, out: out, err: err, reader: bufio.NewReader(in)}
}

func (g *Gum) installed() bool {
	_, err := exec.LookPath("gum")
	return err == nil
}

func (g *Gum) interactive() bool {
	input, inputOK := g.in.(*os.File)
	output, outputOK := g.out.(*os.File)
	if !inputOK || !outputOK || input == nil || output == nil {
		return false
	}
	return isatty.IsTerminal(input.Fd()) && isatty.IsTerminal(output.Fd())
}

func (g *Gum) Input(label, defaultValue string, secret bool) (string, error) {
	if g.bubbleInputAvailable() {
		value, canceled, err := g.bubbleTextInput(label, defaultValue, secret)
		if err == nil {
			if canceled {
				return "", cliError("input canceled")
			}
			return strings.TrimSpace(value), nil
		}
	}
	return g.fallbackInput(label, defaultValue, secret)
}

func (g *Gum) Confirm(label string, defaultValue bool) (bool, error) {
	if g.interactive() && g.installed() {
		cmd := exec.Command("gum", gumConfirmArgs(label, defaultValue)...)
		cmd.Stdin = g.in
		cmd.Stdout = g.out
		cmd.Stderr = g.err
		err := cmd.Run()
		if err == nil {
			return true, nil
		}
		if _, ok := err.(*exec.ExitError); ok {
			return false, nil
		}
	}
	return g.fallbackConfirm(label, defaultValue)
}

func (g *Gum) Choose(label string, choices []string, defaultValue string) (string, error) {
	if len(choices) == 0 {
		return "", cliError("no choices available for %s", label)
	}
	if g.interactive() && g.installed() {
		args := gumChoiceArgs("choose", label, choices, defaultValue)
		if value, err := g.capture(args...); err == nil {
			value = strings.TrimSpace(value)
			if value != "" {
				return value, nil
			}
		}
	}
	return g.fallbackChoose(label, choices, defaultValue)
}

func (g *Gum) Filter(label string, choices []string, defaultValue string) (string, error) {
	if len(choices) == 0 {
		return "", cliError("no choices available for %s", label)
	}
	if g.interactive() && g.installed() {
		args := gumChoiceArgs("filter", label, choices, defaultValue)
		if value, err := g.capture(args...); err == nil {
			value = strings.TrimSpace(value)
			if value != "" {
				return value, nil
			}
		}
	}
	return g.fallbackChoose(label, choices, defaultValue)
}

func (g *Gum) ZoneFilter(label string, choices []string, defaultValue string) (string, error) {
	return g.ListFilter(label, choices, defaultValue, "zone")
}

func (g *Gum) ListFilter(label string, choices []string, defaultValue string, itemName string) (string, error) {
	if len(choices) == 0 {
		return "", cliError("no choices available for %s", label)
	}
	if g.interactive() {
		value, canceled, err := g.bubbleSimpleList(label, choices, defaultValue, itemName)
		if err == nil {
			if canceled {
				return defaultValue, nil
			}
			if value != "" {
				return value, nil
			}
		}
	}
	return g.Filter(gumPromptIndent+label, choices, defaultValue)
}

func (g *Gum) capture(args ...string) (string, error) {
	cmd := exec.Command("gum", args...)
	var stdout bytes.Buffer
	cmd.Stdin = g.in
	cmd.Stdout = &stdout
	cmd.Stderr = g.err
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return stdout.String(), nil
}

func gumConfirmArgs(label string, defaultValue bool) []string {
	return []string{"confirm", "--default=" + strconv.FormatBool(defaultValue), label}
}

func gumChoiceArgs(command, label string, choices []string, defaultValue string) []string {
	args := []string{command, "--header", label}
	if choiceExists(choices, defaultValue) {
		args = append(args, "--selected", defaultValue)
	}
	return append(args, choices...)
}

func choiceExists(choices []string, value string) bool {
	for _, choice := range choices {
		if choice == value {
			return true
		}
	}
	return false
}

func (g *Gum) fallbackInput(label, defaultValue string, secret bool) (string, error) {
	prompt := gumPromptIndent + gumPromptLabelStyle.Render(label)
	if defaultValue != "" && !secret {
		prompt += " " + gumPromptDefaultStyle.Render("["+defaultValue+"]")
	}
	fmt.Fprintf(g.out, "%s: ", prompt)
	if g.reader == nil {
		g.reader = bufio.NewReader(g.in)
	}
	value, err := g.reader.ReadString('\n')
	if err == io.EOF {
		fmt.Fprintln(g.out)
	} else if err != nil {
		return "", err
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return defaultValue, nil
	}
	return value, nil
}

func (g *Gum) fallbackConfirm(label string, defaultValue bool) (bool, error) {
	suffix := "y/N"
	if defaultValue {
		suffix = "Y/n"
	}
	value, err := g.fallbackInput(fmt.Sprintf("%s [%s]", label, suffix), "", false)
	if err != nil {
		return false, err
	}
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return defaultValue, nil
	}
	return value == "y" || value == "yes" || value == "true", nil
}

func (g *Gum) fallbackChoose(label string, choices []string, defaultValue string) (string, error) {
	fmt.Fprintln(g.out, gumChoiceHeaderStyle.Render(label))
	defaultIndex := 1
	for i, choice := range choices {
		marker := " "
		if choice == defaultValue {
			marker = gumChoiceSelectedStyle.Render("*")
			defaultIndex = i + 1
		}
		fmt.Fprintf(g.out, "  %s %s. %s\n", marker, gumChoiceIndexStyle.Render(strconv.Itoa(i+1)), choice)
	}
	value, err := g.fallbackInput("Select number", strconv.Itoa(defaultIndex), false)
	if err != nil {
		return "", err
	}
	index, err := strconv.Atoi(value)
	if err != nil || index < 1 || index > len(choices) {
		return "", cliError("invalid selection %q", value)
	}
	return choices[index-1], nil
}
