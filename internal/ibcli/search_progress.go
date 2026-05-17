package ibcli

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	bubblespinner "github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	searchProgressTitleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#67e8f9"))
	searchProgressStatusStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#cbd5e1"))
	searchProgressDoneStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#4ade80"))
	searchProgressWorkerStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#94a3b8"))
	searchProgressActiveStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#facc15"))
	searchProgressSourceStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#a7f3d0"))
	searchProgressWarningStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#f87171"))
)

type searchResult struct {
	records []TypedRecord
	err     error
}

type searchProgressMsg struct {
	event SearchProgressEvent
	ok    bool
}

type searchWorkerProgress struct {
	ID      int
	Zone    string
	State   string
	Source  string
	Records int
	Matches int
	Started time.Time
	Updated time.Time
	Done    bool
	Err     string
}

type searchProgressModel struct {
	events      <-chan SearchProgressEvent
	spinner     bubblespinner.Model
	started     time.Time
	status      string
	totalZones  int
	workerCount int
	completed   int
	matches     int
	workers     map[int]*searchWorkerProgress
	zoneWorker  map[string]int
	order       []int
}

func (a *App) runDNSSearch(profile Profile, client *WapiClient, options SearchOptions) ([]TypedRecord, error) {
	if a.searchDebugEnabled() {
		originalProgress := options.Progress
		options.Progress = func(event SearchProgressEvent) {
			a.writeSearchDebugEvent(event)
			if originalProgress != nil {
				originalProgress(event)
			}
		}
		return a.collectSearchResults(profile, client, options)
	}

	if !a.searchProgressEnabled() {
		var records []TypedRecord
		if err := a.withSpinner("Searching DNS records...", func() error {
			var searchErr error
			records, searchErr = a.collectSearchResults(profile, client, options)
			return searchErr
		}); err != nil {
			return nil, err
		}
		return records, nil
	}

	events := make(chan SearchProgressEvent, 1024)
	done := make(chan searchResult, 1)
	options.Progress = func(event SearchProgressEvent) {
		select {
		case events <- event:
		default:
		}
	}

	program := tea.NewProgram(newSearchProgressModel(events), tea.WithOutput(a.Stderr), tea.WithInput(nil))
	go func() {
		records, err := a.collectSearchResults(profile, client, options)
		done <- searchResult{records: records, err: err}
		close(events)
	}()

	finalModel, runErr := program.Run()
	clearSearchProgressView(a.Stderr, finalModel)
	result := <-done
	if runErr != nil && result.err == nil {
		return result.records, runErr
	}
	return result.records, result.err
}

func clearSearchProgressView(writer io.Writer, model tea.Model) {
	if writer == nil || model == nil {
		return
	}
	view := strings.TrimRight(model.View(), "\n")
	if strings.TrimSpace(view) == "" {
		return
	}
	lineCount := strings.Count(view, "\n") + 1
	fmt.Fprint(writer, "\r")
	for line := 0; line < lineCount; line++ {
		if line > 0 {
			fmt.Fprint(writer, "\x1b[1A")
		}
		fmt.Fprint(writer, "\x1b[2K")
	}
}

func (a *App) searchProgressEnabled() bool {
	return a.isTableOutput() && a.spinnerEnabled()
}

func (a *App) searchDebugEnabled() bool {
	return envFlagEnabled("IB_SEARCH_DEBUG") || envFlagEnabled("IB_CACHE_DEBUG")
}

func envFlagEnabled(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "y", "on", "enable", "enabled":
		return true
	default:
		return false
	}
}

func (a *App) writeSearchDebugEvent(event SearchProgressEvent) {
	if a.Stderr == nil {
		return
	}
	switch event.Kind {
	case searchProgressWorkerDone:
		fmt.Fprintf(a.Stderr, "search cache: zone=%s source=%s records=%d\n", event.Zone, event.Source, event.Records)
	case searchProgressWorkerSkip:
		fmt.Fprintf(a.Stderr, "search cache: zone=%s skipped=%s\n", event.Zone, event.Stage)
	case searchProgressWorkerError:
		fmt.Fprintf(a.Stderr, "search cache: zone=%s error=%v\n", event.Zone, event.Err)
	}
}

func newSearchProgressModel(events <-chan SearchProgressEvent) searchProgressModel {
	spin := bubblespinner.New(bubblespinner.WithSpinner(bubblespinner.Line), bubblespinner.WithStyle(noteStyle))
	return searchProgressModel{
		events:     events,
		spinner:    spin,
		started:    time.Now(),
		status:     "Starting DNS search",
		workers:    map[int]*searchWorkerProgress{},
		zoneWorker: map[string]int{},
	}
}

func (m searchProgressModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, waitForSearchProgressEvent(m.events))
}

func waitForSearchProgressEvent(events <-chan SearchProgressEvent) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-events
		return searchProgressMsg{event: event, ok: ok}
	}
}

func (m searchProgressModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case bubblespinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case searchProgressMsg:
		if !msg.ok {
			m.status = "Search complete"
			return m, tea.Quit
		}
		m.apply(msg.event)
		return m, waitForSearchProgressEvent(m.events)
	default:
		return m, nil
	}
}

func (m *searchProgressModel) apply(event SearchProgressEvent) {
	switch event.Kind {
	case searchProgressStage:
		if strings.TrimSpace(event.Stage) != "" {
			m.status = event.Stage
		}
		if event.TotalZones > 0 {
			m.totalZones = event.TotalZones
		}
		if event.WorkerCount > 0 {
			m.workerCount = event.WorkerCount
		}
		if event.Matches > 0 {
			m.matches = event.Matches
		}
	case searchProgressWorkerStart:
		worker := m.worker(event.WorkerID, event.Zone)
		worker.State = firstNonEmpty(event.Stage, "Checking cache")
		worker.Source = ""
		worker.Records = 0
		worker.Matches = 0
		worker.Err = ""
		worker.Done = false
		worker.Started = time.Now()
		worker.Updated = worker.Started
		m.status = fmt.Sprintf("Worker %d loading %s", event.WorkerID, event.Zone)
	case searchProgressWorkerDone:
		worker := m.worker(event.WorkerID, event.Zone)
		worker.State = "Done"
		worker.Source = event.Source
		worker.Records = event.Records
		worker.Updated = time.Now()
		m.markComplete(worker)
	case searchProgressWorkerSkip:
		worker := m.worker(event.WorkerID, event.Zone)
		worker.State = firstNonEmpty(event.Stage, "Skipped")
		worker.Err = errorText(event.Err)
		worker.Updated = time.Now()
		m.markComplete(worker)
	case searchProgressWorkerError:
		worker := m.worker(event.WorkerID, event.Zone)
		worker.State = "Error"
		worker.Err = errorText(event.Err)
		worker.Updated = time.Now()
		m.markComplete(worker)
	case searchProgressZoneMatched:
		workerID := m.zoneWorker[event.Zone]
		if workerID == 0 {
			return
		}
		worker := m.workers[workerID]
		worker.Matches = event.Matches
		m.matches += event.Matches
	}
}

func (m *searchProgressModel) worker(id int, zone string) *searchWorkerProgress {
	if id <= 0 {
		id = 1
	}
	worker, ok := m.workers[id]
	if !ok {
		worker = &searchWorkerProgress{ID: id}
		m.workers[id] = worker
		m.order = append(m.order, id)
		sort.Ints(m.order)
	}
	if strings.TrimSpace(zone) != "" {
		worker.Zone = zone
		m.zoneWorker[zone] = id
	}
	return worker
}

func (m *searchProgressModel) markComplete(worker *searchWorkerProgress) {
	if worker.Done {
		return
	}
	worker.Done = true
	m.completed++
}

func (m searchProgressModel) View() string {
	elapsed := time.Since(m.started).Round(time.Second)
	if elapsed < time.Second {
		elapsed = 0
	}
	total := m.totalZones
	if total == 0 {
		total = len(m.workers)
	}
	workers := m.workerCount
	if workers == 0 {
		workers = len(m.workers)
	}

	lines := []string{
		searchProgressTitleStyle.Render("ib dns search"),
		fmt.Sprintf("%s %s", m.spinner.View(), searchProgressStatusStyle.Render(m.status)),
		searchProgressStatusStyle.Render(fmt.Sprintf("Zones %d/%d  Workers %d  Matches %d  Elapsed %s", m.completed, total, workers, m.matches, elapsed)),
	}
	for _, id := range m.order {
		worker := m.workers[id]
		state := worker.State
		if state == "" {
			state = "Waiting"
		}
		style := searchProgressActiveStyle
		if worker.Done {
			style = searchProgressDoneStyle
		}
		if worker.Err != "" || strings.EqualFold(state, "Error") {
			style = searchProgressWarningStyle
		}
		source := worker.Source
		if source == "" {
			source = "-"
		}
		lines = append(lines, fmt.Sprintf("  W%02d %-24s %-26s %5d rec %5d match %s",
			worker.ID,
			style.Render(progressTrim(worker.Zone, 24)),
			searchProgressWorkerStyle.Render(progressTrim(state, 26)),
			worker.Records,
			worker.Matches,
			searchProgressSourceStyle.Render(progressTrim(source, 24)),
		))
	}
	return strings.Join(lines, "\n")
}

func progressTrim(value string, width int) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	if lipgloss.Width(value) <= width {
		return value
	}
	if width <= 3 {
		left, _ := splitDisplayWidth(value, width)
		return left
	}
	left, _ := splitDisplayWidth(value, width-3)
	return left + "..."
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

var _ tea.Model = searchProgressModel{}
