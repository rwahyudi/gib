package ibcli

import (
	"fmt"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type bubbleTextInputModel struct {
	label    string
	input    textinput.Model
	done     bool
	canceled bool
}

func (g *Gum) bubbleInputAvailable() bool {
	return g.interactive()
}

func (g *Gum) bubbleTextInput(label, defaultValue string, secret bool) (string, bool, error) {
	program := tea.NewProgram(
		newBubbleTextInputModel(label, defaultValue, secret),
		tea.WithInput(g.in),
		tea.WithOutput(g.out),
	)
	result, err := program.Run()
	if err != nil {
		return "", false, err
	}
	model, ok := result.(bubbleTextInputModel)
	if !ok {
		return "", false, fmt.Errorf("unexpected Bubble Tea model %T", result)
	}
	return model.input.Value(), model.canceled, nil
}

func newBubbleTextInputModel(label, defaultValue string, secret bool) bubbleTextInputModel {
	input := textinput.New()
	input.Prompt = ""
	input.TextStyle = configValueStyle
	input.PlaceholderStyle = configEmptyValueStyle
	input.Width = bubbleInputWidth(label)
	if defaultValue != "" && !secret {
		input.SetValue(defaultValue)
	}
	if secret {
		input.EchoMode = textinput.EchoPassword
		input.EchoCharacter = '*'
	}
	input.Focus()

	return bubbleTextInputModel{
		label: label,
		input: input,
	}
}

func (m bubbleTextInputModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m bubbleTextInputModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "enter":
			m.done = true
			return m, tea.Quit
		case "ctrl+c", "esc":
			m.canceled = true
			return m, tea.Quit
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m bubbleTextInputModel) View() string {
	if m.done || m.canceled {
		return ""
	}
	return gumPromptIndent +
		gumPromptLabelStyle.Render(m.label+": ") +
		m.input.View() +
		"\n"
}

func bubbleInputWidth(label string) int {
	width := 72 - lipgloss.Width(gumPromptIndent+label+": ")
	if width < 24 {
		return 24
	}
	if width > 56 {
		return 56
	}
	return width
}

var _ tea.Model = bubbleTextInputModel{}
