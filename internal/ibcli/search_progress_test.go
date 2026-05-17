package ibcli

import (
	"bytes"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestClearSearchProgressViewClearsRenderedLines(t *testing.T) {
	events := make(chan SearchProgressEvent)
	model := newSearchProgressModel(events)
	model.apply(SearchProgressEvent{Kind: searchProgressStage, Stage: "Loading zone records", TotalZones: 2, WorkerCount: 2})
	model.apply(SearchProgressEvent{Kind: searchProgressWorkerStart, WorkerID: 1, Zone: "example.com", Stage: "Checking cache"})
	model.apply(SearchProgressEvent{Kind: searchProgressWorkerDone, WorkerID: 1, Zone: "example.com", Source: recordCacheSourceFreshCache, Records: 3})

	view := strings.TrimRight(model.View(), "\n")
	wantLines := strings.Count(view, "\n") + 1

	var output bytes.Buffer
	clearSearchProgressView(&output, model)
	got := output.String()

	if clears := strings.Count(got, "\x1b[2K"); clears != wantLines {
		t.Fatalf("clear sequences = %d, want %d; output=%q", clears, wantLines, got)
	}
	if ups := strings.Count(got, "\x1b[1A"); ups != wantLines-1 {
		t.Fatalf("cursor-up sequences = %d, want %d; output=%q", ups, wantLines-1, got)
	}
	if !strings.HasPrefix(got, "\r") {
		t.Fatalf("clear output should return to column 0 first: %q", got)
	}
}

func TestClearSearchProgressViewSkipsEmptyView(t *testing.T) {
	var output bytes.Buffer
	clearSearchProgressView(&output, emptyTeaModel{})
	if output.Len() != 0 {
		t.Fatalf("empty view produced clear output: %q", output.String())
	}
}

func TestSearchDebugEventWritesPersistentCacheSource(t *testing.T) {
	t.Setenv("IB_SEARCH_DEBUG", "1")
	app := testApp(t)
	var stderr bytes.Buffer
	app.Stderr = &stderr

	if !app.searchDebugEnabled() {
		t.Fatal("search debug should be enabled")
	}
	app.writeSearchDebugEvent(SearchProgressEvent{
		Kind:    searchProgressWorkerDone,
		Zone:    "example.com",
		Source:  recordCacheSourceFreshCache,
		Records: 3,
	})

	got := stderr.String()
	for _, want := range []string{"zone=example.com", "source=fresh cache", "records=3"} {
		if !strings.Contains(got, want) {
			t.Fatalf("debug output missing %q: %q", want, got)
		}
	}
}

type emptyTeaModel struct{}

func (emptyTeaModel) Init() tea.Cmd {
	return nil
}

func (emptyTeaModel) Update(tea.Msg) (tea.Model, tea.Cmd) {
	return emptyTeaModel{}, nil
}

func (emptyTeaModel) View() string {
	return ""
}
