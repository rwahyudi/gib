package ibcli

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func TestNumericTableFieldRecognizesIntegerColumns(t *testing.T) {
	tests := map[string]bool{
		"Ttl":                  true,
		"Serial":               true,
		"Serial Number":        true,
		"Items":                true,
		"Priority":             true,
		"Negative Caching Ttl": true,
		"Zone":                 false,
		"Comment":              false,
	}

	for field, want := range tests {
		if got := numericTableField(field); got != want {
			t.Fatalf("numericTableField(%q) = %v, want %v", field, got, want)
		}
	}
}

func TestRenderTableRightAlignsIntegerColumns(t *testing.T) {
	output := renderTable("", []string{"Name", "Ttl"}, [][]string{
		{"short", "1"},
		{"longer", "300"},
	})
	if !strings.Contains(output, "name") || !strings.Contains(output, "ttl") {
		t.Fatalf("table headers are not lowercase:\n%s", output)
	}
	if strings.Contains(output, "Name") || strings.Contains(output, "Ttl") {
		t.Fatalf("table headers still use title case:\n%s", output)
	}

	shortLine := tableLineContaining(t, output, "short")
	longerLine := tableLineContaining(t, output, "longer")
	shortEnd := strings.Index(shortLine, "1") + len("1")
	longerEnd := strings.Index(longerLine, "300") + len("300")

	if shortEnd != longerEnd {
		t.Fatalf("TTL values are not right aligned:\n%s", output)
	}
}

func TestRenderTableRightAlignsNumericFieldValues(t *testing.T) {
	output := renderTable("", []string{"Field", "Value"}, [][]string{
		{"Serial Number", "1"},
		{"Refresh", "300"},
		{"Comment", "abc"},
	})

	serialLine := tableLineContaining(t, output, "Serial Number")
	refreshLine := tableLineContaining(t, output, "Refresh")
	serialEnd := strings.Index(serialLine, "1") + len("1")
	refreshEnd := strings.Index(refreshLine, "300") + len("300")

	if serialEnd != refreshEnd {
		t.Fatalf("numeric detail values are not right aligned:\n%s", output)
	}
}

func TestRenderTablePreservesTitleCase(t *testing.T) {
	output := renderTable("DNS Records", []string{"Type", "Name"}, [][]string{{"A", "app.example.com"}})
	if !strings.Contains(output, "DNS Records") {
		t.Fatalf("table title was not preserved:\n%s", output)
	}
	if strings.Contains(output, "dns records") {
		t.Fatalf("table title was lowercased:\n%s", output)
	}
	if !strings.Contains(output, "type") || !strings.Contains(output, "name") {
		t.Fatalf("table headers were not lowercased:\n%s", output)
	}
}

func tableLineContaining(t *testing.T, output, needle string) string {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, needle) {
			return line
		}
	}
	t.Fatalf("missing line containing %q in:\n%s", needle, output)
	return ""
}

// renderTableReference preserves the former implementation as a parity oracle.
func renderTableReference(title string, headers []string, rows [][]string, rowStyles map[int]lipgloss.Style, cellStyles map[tableCellPosition]lipgloss.Style) string {
	headers = lowerTableHeaders(headers)
	numericColumns := numericTableColumns(headers)
	t := table.New().Border(lipgloss.RoundedBorder()).BorderStyle(tableBorderStyle).Headers(headers...).Rows(rows...).StyleFunc(func(row, col int) lipgloss.Style {
		rightAlign := numericColumns[col] || numericFieldValueCell(headers, rows, row, col)
		if row == table.HeaderRow {
			style := tableHeaderStyle.Padding(0, 1)
			if rightAlign {
				return style.Align(lipgloss.Right)
			}
			return style
		}
		style := tableCellStyle
		if rightAlign {
			style = style.Align(lipgloss.Right)
		}
		if rowStyle, ok := rowStyles[row]; ok {
			style = style.Inherit(rowStyle)
		}
		if cellStyle, ok := cellStyles[tableCellPosition{row: row, col: col}]; ok {
			style = style.Inherit(cellStyle)
		}
		return style
	})
	if title == "" {
		return t.String()
	}
	return tableTitleStyle.Render(title) + "\n" + t.String()
}

func TestRenderFastTableMatchesLipglossReference(t *testing.T) {
	active := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#052e16")).Background(lipgloss.Color("#4ade80")).ColorWhitespace(true)
	global := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#fee2e2")).Background(lipgloss.Color("#dc2626")).ColorWhitespace(true)
	cases := []struct {
		name    string
		title   string
		headers []string
		rows    [][]string
		row     map[int]lipgloss.Style
		cell    map[tableCellPosition]lipgloss.Style
	}{
		{name: "ordinary", title: "DNS Records", headers: []string{"Name", "TTL", "Status"}, rows: [][]string{{"app.example.com", "1", "ok"}, {"longer.example.com", "300", "active"}}},
		{name: "unicode ansi multiline", headers: []string{"Field", "Value"}, rows: [][]string{{"Serial Number", "\x1b[31m界é\x1b[0m"}, {"Refresh", "one\ntwo"}}},
		{name: "ragged", headers: []string{"A", "B"}, rows: [][]string{{"x"}, {"y", "z", "extra"}, {}}},
		{name: "styled", headers: []string{"Name", "Value"}, rows: [][]string{{"active", "1"}, {"global", "2"}}, row: map[int]lipgloss.Style{0: active}, cell: map[tableCellPosition]lipgloss.Style{{row: 1, col: 0}: global}},
		{name: "empty rows", headers: []string{"A", "B"}},
		{name: "empty", rows: [][]string{{}}},
		{name: "no headers with data", rows: [][]string{{"one", "2"}, {"three", "4"}}},
		{name: "nil table"},
		{name: "title without columns", title: "Nothing"},
		{name: "multiline headers", headers: []string{"First\nName", "TTL\n(seconds)"}, rows: [][]string{{"a", "2"}}},
		{name: "tabs and carriage returns", headers: []string{"A\tB", "C\rD"}, rows: [][]string{{"x\ty", "z\r"}}},
		{name: "overlapping style inheritance", headers: []string{"Name", "Value"}, rows: [][]string{{"active", "1"}}, row: map[int]lipgloss.Style{0: active}, cell: map[tableCellPosition]lipgloss.Style{{row: 0, col: 0}: global}},
		{name: "wide combining emoji", headers: []string{"Name", "Value"}, rows: [][]string{{"界é👨‍👩‍👧‍👦", "1"}}},
		{name: "whitespace and trailing lines", headers: []string{"Name", "TTL"}, rows: [][]string{{"  spaced  ", " 1 "}, {"start\n\nend\n", "300"}}},
		{name: "inherited layout", headers: []string{"Name", "Value"}, rows: [][]string{{"active", "1"}}, row: map[int]lipgloss.Style{0: active.Padding(1, 2)}},
		{name: "ANSI across lines", headers: []string{"Name", "Value"}, rows: [][]string{{"\x1b[31mone\ntwo\x1b[0m", "value"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := renderTableWithStyles(tc.title, tc.headers, tc.rows, tc.row, tc.cell)
			want := renderTableReference(tc.title, tc.headers, tc.rows, tc.row, tc.cell)
			if got != want {
				t.Fatalf("fast renderer differs from lipgloss reference\ngot:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}

func TestRenderFastTablePreservesInlineRecordColors(t *testing.T) {
	originalProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(originalProfile) })
	headers := []string{"Type", "Name", "TTL"}
	colorType := styledRecordType("host")
	rows := [][]string{{colorType, "界é👨‍👩‍👧‍👦", "\x1b[31m1\x1b[0m"}, {"A", "app.example.com", "300"}}
	got := renderTable("DNS Records", headers, rows)
	want := renderTableReference("DNS Records", headers, rows, nil, nil)
	if ansi.Strip(got) != ansi.Strip(want) {
		t.Fatalf("inline colored layout differs\ngot:\n%s\nwant:\n%s", got, want)
	}
	if !strings.Contains(got, colorType) || !strings.Contains(got, "\x1b[31m1\x1b[0m") {
		t.Fatalf("inline record/TTL colors were lost: %q", got)
	}
}

func TestRenderFastTableMatchesLipglossReferenceWithColor(t *testing.T) {
	originalProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(originalProfile) })
	styles := map[int]lipgloss.Style{0: activeTableRowStyle}
	cells := map[tableCellPosition]lipgloss.Style{{row: 1, col: 0}: globalScopeCellStyle}
	headers := []string{"Name", "TTL"}
	rows := [][]string{{"active\n\x1b[31mcolored\x1b[0m", "1"}, {"global", "300"}}
	got := renderTableWithStyles("Colored", headers, rows, styles, cells)
	want := renderTableReference("Colored", headers, rows, styles, cells)
	if ansi.Strip(got) != ansi.Strip(want) {
		t.Fatalf("colored fast renderer differs from lipgloss reference\ngot:\n%q\nwant:\n%q", got, want)
	}
	if !strings.Contains(got, activeTableRowStyle.Render("active")) || !strings.Contains(got, globalScopeCellStyle.Render("global")) {
		t.Fatalf("colored row/cell styles were not applied:\n%q", got)
	}
	if !strings.Contains(got, "\x1b[31mcolored") {
		t.Fatalf("embedded multiline ANSI style was not preserved:\n%q", got)
	}
}

func BenchmarkTableRenderers10K(b *testing.B) {
	rows := make([][]string, 10_000)
	for i := range rows {
		rows[i] = []string{"host.example.com", "A", "192.0.2.1", "300"}
	}
	headers := []string{"Name", "Type", "Address", "TTL"}
	fast := func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = renderTable("DNS records", headers, rows)
		}
	}
	legacy := func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = renderTableReference("DNS records", headers, rows, nil, nil)
		}
	}
	b.Run("fast/default", fast)
	b.Run("lipgloss/default", legacy)
	originalProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	b.Cleanup(func() { lipgloss.SetColorProfile(originalProfile) })
	b.Run("fast/truecolor", fast)
	b.Run("lipgloss/truecolor", legacy)
	for i := range rows {
		rows[i][1] = styledRecordType("a")
	}
	b.Run("fast/inline-color", fast)
	b.Run("lipgloss/inline-color", legacy)
}
