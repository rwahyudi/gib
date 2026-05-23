package ibcli

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

func TestGumConfirmArgsIncludeDefault(t *testing.T) {
	got := gumConfirmArgs("Trust this Infoblox HTTPS certificate for this profile?", false)
	want := []string{"confirm", "--default=false", "Trust this Infoblox HTTPS certificate for this profile?"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("confirm args = %#v, want %#v", got, want)
	}
}

func TestGumChoiceArgsIncludeSelectedDefault(t *testing.T) {
	choices := []string{"default", "DNS Zone View"}
	got := gumChoiceArgs("choose", "Default DNS View", choices, "DNS Zone View")
	want := []string{"choose", "--header", "Default DNS View", "--selected", "DNS Zone View", "default", "DNS Zone View"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("choice args = %#v, want %#v", got, want)
	}
}

func TestGumChoiceArgsSkipUnknownDefault(t *testing.T) {
	choices := []string{"default", "DNS Zone View"}
	got := gumChoiceArgs("filter", "Default DNS Zone", choices, "missing")
	want := []string{"filter", "--header", "Default DNS Zone", "default", "DNS Zone View"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filter args = %#v, want %#v", got, want)
	}
}

func TestFallbackInputEndsEOFPromptWithNewline(t *testing.T) {
	var stdout bytes.Buffer
	gum := NewGum(strings.NewReader(""), &stdout, &bytes.Buffer{})

	value, err := gum.fallbackInput("Infoblox server", "", false)
	if err != nil {
		t.Fatalf("fallback input: %v", err)
	}
	if value != "" {
		t.Fatalf("fallback input value = %q, want empty", value)
	}
	if !strings.HasSuffix(stdout.String(), "\n") {
		t.Fatalf("fallback input prompt did not end with newline: %q", stdout.String())
	}
}

func TestFallbackInputReusesReaderAcrossPrompts(t *testing.T) {
	var stdout bytes.Buffer
	gum := NewGum(strings.NewReader("first\nsecond\n"), &stdout, &bytes.Buffer{})

	first, err := gum.fallbackInput("First", "", false)
	if err != nil {
		t.Fatalf("first fallback input: %v", err)
	}
	second, err := gum.fallbackInput("Second", "", false)
	if err != nil {
		t.Fatalf("second fallback input: %v", err)
	}
	if first != "first" || second != "second" {
		t.Fatalf("fallback values = %q, %q; want first, second", first, second)
	}
}

func TestBubbleTextInputModelAcceptsEnteredValue(t *testing.T) {
	model := newBubbleTextInputModel("Infoblox server", "", false)

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("infoblox.example")})
	model = updated.(bubbleTextInputModel)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(bubbleTextInputModel)

	if !model.done {
		t.Fatalf("model did not finish on enter")
	}
	if got := model.input.Value(); got != "infoblox.example" {
		t.Fatalf("model value = %q, want infoblox.example", got)
	}
}

func TestBubbleTextInputModelKeepsDefaultValue(t *testing.T) {
	model := newBubbleTextInputModel("WAPI version", defaultWAPIVersion, false)

	if got := model.input.Value(); got != defaultWAPIVersion {
		t.Fatalf("model default value = %q, want %q", got, defaultWAPIVersion)
	}
}

func TestBubbleTextInputModelStartsBlankWhenDefaultIsEmpty(t *testing.T) {
	model := newBubbleTextInputModel("Profile name", "", false)

	if got := model.input.Value(); got != "" {
		t.Fatalf("model value = %q, want empty", got)
	}
	if strings.Contains(model.View(), "default") {
		t.Fatalf("blank profile prompt rendered a default value:\n%s", model.View())
	}
}

func TestBubbleTextInputModelViewUsesSingleLinePrompt(t *testing.T) {
	model := newBubbleTextInputModel("Infoblox server", "", false)

	view := strings.TrimSuffix(model.View(), "\n")
	if strings.Count(view, "\n") != 0 {
		t.Fatalf("bubble input prompt should be one line:\n%s", view)
	}
	for _, want := range []string{"   ", "Infoblox server:"} {
		if !strings.Contains(view, want) {
			t.Fatalf("bubble input prompt missing %q:\n%s", want, view)
		}
	}
	for _, unwanted := range []string{"? ", "type value", "type password"} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("bubble input prompt contains %q:\n%s", unwanted, view)
		}
	}
}

func TestBubbleTextInputWidthShrinksForLongLabels(t *testing.T) {
	short := newBubbleTextInputModel("Username", "", false)
	long := newBubbleTextInputModel("Password (leave blank to keep current)", "", true)

	if long.input.Width >= short.input.Width {
		t.Fatalf("long label input width = %d, want less than short label width %d", long.input.Width, short.input.Width)
	}
	if long.input.Width < 24 {
		t.Fatalf("long label input width = %d, want minimum 24", long.input.Width)
	}
}

func TestBubbleTextInputModelMasksSecretInput(t *testing.T) {
	model := newBubbleTextInputModel("Password", "", true)

	if model.input.EchoMode != textinput.EchoPassword {
		t.Fatalf("secret input echo mode = %v, want EchoPassword", model.input.EchoMode)
	}
	if model.input.EchoCharacter != '*' {
		t.Fatalf("secret input echo character = %q, want *", model.input.EchoCharacter)
	}
}

func TestBubbleTextInputModelCancels(t *testing.T) {
	model := newBubbleTextInputModel("Infoblox server", "", false)

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	model = updated.(bubbleTextInputModel)

	if !model.canceled {
		t.Fatalf("model did not cancel on ctrl+c")
	}
}

func TestZoneListModelFiltersByTypingAndSelects(t *testing.T) {
	model := newZoneListModel("Default DNS Zone", []string{
		"example.com",
		"dev.example.com",
		"corp.example.edu",
	}, "")

	for _, char := range "dev" {
		updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{char}})
		model = updated.(zoneListModel)
	}
	if len(model.filtered) != 1 || model.filtered[0] != "dev.example.com" {
		t.Fatalf("filtered zones = %#v, want dev.example.com", model.filtered)
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(zoneListModel)
	if !model.done {
		t.Fatalf("zone list did not finish on enter")
	}
	if model.selected != "dev.example.com" {
		t.Fatalf("selected zone = %q, want dev.example.com", model.selected)
	}
}

func TestZoneListModelFiltersByPrefixOnly(t *testing.T) {
	model := newZoneListModel("Default DNS Zone", []string{
		"example.com",
		"dev.example.com",
		"corp.example.edu",
	}, "")

	for _, char := range "example" {
		updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{char}})
		model = updated.(zoneListModel)
	}
	if len(model.filtered) != 1 || model.filtered[0] != "example.com" {
		t.Fatalf("filtered zones = %#v, want only example.com", model.filtered)
	}
	if strings.Contains(model.View(), "dev.example.com") || strings.Contains(model.View(), "corp.example.edu") {
		t.Fatalf("prefix filter rendered non-prefix matches:\n%s", model.View())
	}
}

func TestZoneListModelFilteringSelectsTopMatch(t *testing.T) {
	model := newZoneListModel("Default DNS Zone", []string{
		"dev-a.example.com",
		"dev-b.example.com",
		"prod.example.com",
	}, "dev-b.example.com")

	for _, char := range "dev" {
		updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{char}})
		model = updated.(zoneListModel)
	}
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(zoneListModel)

	if model.selected != "dev-a.example.com" {
		t.Fatalf("selected zone = %q, want top filtered match dev-a.example.com", model.selected)
	}
}

func TestSimpleListModelCanRenderDNSViews(t *testing.T) {
	model := newSimpleListModel("Default DNS View", []string{
		"default",
		"DNS Zone View",
	}, "default", "view")

	view := model.View()
	for _, want := range []string{
		"Default DNS View",
		"type view prefix",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("simple list view missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "type zone prefix") {
		t.Fatalf("DNS view picker rendered zone placeholder:\n%s", view)
	}
}

func TestZoneListModelKeepsEightRowListArea(t *testing.T) {
	model := newZoneListModel("Default DNS Zone", []string{
		"corp.example.edu",
		"dev.example.com",
		"example.com",
	}, "")

	if got := simpleListAreaLineCount(model.View()); got != zoneListVisibleItems {
		t.Fatalf("zone list area rows = %d, want %d:\n%s", got, zoneListVisibleItems, model.View())
	}

	viewModel := newSimpleListModel("Default DNS View", []string{
		"default",
		"DNS Zone View",
	}, "default", "view")
	if got := simpleListAreaLineCount(viewModel.View()); got != 2 {
		t.Fatalf("DNS view list area rows = %d, want 2:\n%s", got, viewModel.View())
	}
}

func simpleListAreaLineCount(view string) int {
	lines := strings.Split(view, "\n")
	inList := false
	count := 0
	for _, line := range lines {
		if strings.Contains(line, "Filter:") {
			inList = true
			continue
		}
		if strings.Contains(line, "type prefix to filter") {
			return count
		}
		if inList {
			count++
		}
	}
	return count
}

func TestZoneListModelPreselectsCurrentZone(t *testing.T) {
	model := newZoneListModel("Default DNS Zone", []string{
		"corp.example.edu",
		"dev.example.com",
		"example.com",
	}, "example.com")

	if got := model.filtered[model.cursor]; got != "example.com" {
		t.Fatalf("cursor zone = %q, want example.com", got)
	}
}

func TestZoneFilterFallsBackWhenNonInteractive(t *testing.T) {
	var stdout bytes.Buffer
	gum := NewGum(strings.NewReader("2\n"), &stdout, &bytes.Buffer{})

	selected, err := gum.ZoneFilter("Default DNS Zone", []string{"example.com", "dev.example.com"}, "")
	if err != nil {
		t.Fatalf("zone filter: %v", err)
	}
	if selected != "dev.example.com" {
		t.Fatalf("selected zone = %q, want dev.example.com", selected)
	}
}
