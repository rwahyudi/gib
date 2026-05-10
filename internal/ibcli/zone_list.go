package ibcli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const zoneListVisibleItems = 8

var (
	zoneListTitleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#67e8f9"))
	zoneListHelpStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#94a3b8"))
	zoneListSelectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#4ade80"))
	zoneListNormalStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#cbd5e1"))
	zoneListEmptyStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#facc15"))
)

type zoneListModel struct {
	title    string
	itemName string
	input    textinput.Model
	choices  []string
	filtered []string
	cursor   int
	selected string
	minRows  int
	done     bool
	canceled bool
}

func (g *Gum) bubbleZoneList(title string, choices []string, current string) (string, bool, error) {
	return g.bubbleSimpleList(title, choices, current, "zone")
}

func (g *Gum) bubbleSimpleList(title string, choices []string, current string, itemName string) (string, bool, error) {
	program := tea.NewProgram(
		newSimpleListModel(title, choices, current, itemName),
		tea.WithInput(g.in),
		tea.WithOutput(g.out),
	)
	result, err := program.Run()
	if err != nil {
		return "", false, err
	}
	model, ok := result.(zoneListModel)
	if !ok {
		return "", false, fmt.Errorf("unexpected Bubble Tea model %T", result)
	}
	return model.selected, model.canceled, nil
}

func newZoneListModel(title string, choices []string, current string) zoneListModel {
	return newSimpleListModel(title, choices, current, "zone")
}

func newSimpleListModel(title string, choices []string, current string, itemName string) zoneListModel {
	cleanChoices := uniqueSortedStrings(choices)
	itemName = strings.TrimSpace(itemName)
	if itemName == "" {
		itemName = "item"
	}
	input := textinput.New()
	input.Prompt = "   Filter: "
	input.Placeholder = "type " + itemName + " prefix"
	input.TextStyle = configValueStyle
	input.PlaceholderStyle = configEmptyValueStyle
	input.PromptStyle = configFieldStyle
	input.Width = 48
	input.Focus()

	model := zoneListModel{
		title:    title,
		itemName: itemName,
		input:    input,
		choices:  cleanChoices,
	}
	if itemName == "zone" {
		model.minRows = zoneListVisibleItems
	}
	model.applyFilter()
	model.selectValue(current)
	return model
}

func uniqueSortedStrings(values []string) []string {
	seen := map[string]bool{}
	var cleaned []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		cleaned = append(cleaned, value)
	}
	sort.Strings(cleaned)
	return cleaned
}

func (m zoneListModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m zoneListModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.canceled = true
			return m, tea.Quit
		case "enter":
			if len(m.filtered) == 0 {
				return m, nil
			}
			m.selected = m.filtered[m.cursor]
			m.done = true
			return m, tea.Quit
		case "up", "ctrl+p":
			m.moveCursor(-1)
			return m, nil
		case "down", "ctrl+n":
			m.moveCursor(1)
			return m, nil
		}
	}

	oldFilter := m.input.Value()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if m.input.Value() != oldFilter {
		m.applyFilter()
		m.cursor = 0
	}
	return m, cmd
}

func (m *zoneListModel) applyFilter() {
	filter := strings.ToLower(strings.TrimSpace(m.input.Value()))
	if filter == "" {
		m.filtered = append([]string(nil), m.choices...)
	} else {
		m.filtered = m.filtered[:0]
		for _, choice := range m.choices {
			if strings.HasPrefix(strings.ToLower(choice), filter) {
				m.filtered = append(m.filtered, choice)
			}
		}
	}
	if m.cursor >= len(m.filtered) {
		m.cursor = len(m.filtered) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func (m *zoneListModel) selectValue(value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	for i, choice := range m.filtered {
		if choice == value {
			m.cursor = i
			return
		}
	}
}

func (m *zoneListModel) moveCursor(delta int) {
	if len(m.filtered) == 0 {
		m.cursor = 0
		return
	}
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = len(m.filtered) - 1
	}
	if m.cursor >= len(m.filtered) {
		m.cursor = 0
	}
}

func (m zoneListModel) View() string {
	if m.done || m.canceled {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("   " + zoneListTitleStyle.Render(m.title) + "\n")
	builder.WriteString(m.input.View() + "\n")
	renderedListRows := 0
	if len(m.filtered) == 0 {
		builder.WriteString("   " + zoneListEmptyStyle.Render("No "+m.itemName+"s match this filter") + "\n")
		renderedListRows = 1
	} else {
		for _, row := range m.visibleRows() {
			marker := " "
			style := zoneListNormalStyle
			if row.index == m.cursor {
				marker = ">"
				style = zoneListSelectedStyle
			}
			builder.WriteString(fmt.Sprintf("   %s %s\n", marker, style.Render(row.value)))
			renderedListRows++
		}
	}
	for renderedListRows < m.minRows {
		builder.WriteString("     \n")
		renderedListRows++
	}
	builder.WriteString("   " + zoneListHelpStyle.Render("type prefix to filter • up/down move • enter select • esc cancel") + "\n")
	return builder.String()
}

type zoneListRow struct {
	index int
	value string
}

func (m zoneListModel) visibleRows() []zoneListRow {
	if len(m.filtered) == 0 {
		return nil
	}
	start := 0
	if m.cursor >= zoneListVisibleItems {
		start = m.cursor - zoneListVisibleItems + 1
	}
	end := start + zoneListVisibleItems
	if end > len(m.filtered) {
		end = len(m.filtered)
	}
	rows := make([]zoneListRow, 0, end-start)
	for i := start; i < end; i++ {
		rows = append(rows, zoneListRow{index: i, value: m.filtered[i]})
	}
	return rows
}

var _ tea.Model = zoneListModel{}
