package ibcli

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
)

var (
	tableBorderStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#38bdf8"))
	tableHeaderStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#67e8f9"))
	tableCellStyle       = lipgloss.NewStyle().Padding(0, 1)
	tableTitleStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#4ade80"))
	activeTableRowStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#052e16")).Background(lipgloss.Color("#4ade80")).ColorWhitespace(true)
	globalScopeCellStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#fee2e2")).Background(lipgloss.Color("#dc2626")).ColorWhitespace(true)
)

type tableCellPosition struct {
	row int
	col int
}

func renderTable(title string, headers []string, rows [][]string) string {
	return renderTableWithRowStyles(title, headers, rows, nil)
}

func renderTableWithRowStyles(title string, headers []string, rows [][]string, rowStyles map[int]lipgloss.Style) string {
	return renderTableWithStyles(title, headers, rows, rowStyles, nil)
}

func renderTableWithStyles(title string, headers []string, rows [][]string, rowStyles map[int]lipgloss.Style, cellStyles map[tableCellPosition]lipgloss.Style) string {
	headers = lowerTableHeaders(headers)
	numericColumns := numericTableColumns(headers)
	// Config tables use arbitrary inherited styles. Keep their full Lipgloss
	// layout semantics (padding, borders, transforms), while large record tables
	// use the fast path and retain their inline ANSI record-type colors.
	if len(rowStyles) > 0 || len(cellStyles) > 0 || useLipglossTableFallback(headers, rows) {
		return renderLipglossTable(title, headers, rows, numericColumns, rowStyles, cellStyles)
	}
	return renderFastTable(title, headers, rows, numericColumns)
}

func useLipglossTableFallback(headers []string, rows [][]string) bool {
	if len(headers) == 0 {
		return true
	}
	for _, value := range headers {
		if strings.ContainsAny(value, "\n\r\t") {
			return true
		}
	}
	for _, row := range rows {
		for _, value := range row {
			// Multiline ANSI state can span lines; the original renderer handles
			// those sequences as a whole cell rather than styling separate lines.
			if strings.ContainsAny(value, "\r\t") || (strings.Contains(value, "\n") && strings.Contains(value, "\x1b")) {
				return true
			}
		}
	}
	return false
}

func renderLipglossTable(title string, headers []string, rows [][]string, numericColumns map[int]bool, rowStyles map[int]lipgloss.Style, cellStyles map[tableCellPosition]lipgloss.Style) string {
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

// renderFastTable keeps the table's layout work in one pass. In particular, the
// lipgloss table's StyleFunc is expensive on large tables because it repeatedly
// normalizes rows and resolves styles while rendering each cell.
func renderFastTable(title string, headers []string, rows [][]string, numericColumns map[int]bool) string {
	columns := len(headers)
	for _, row := range rows {
		if len(row) > columns {
			columns = len(row)
		}
	}
	widths := make([]int, columns)
	for col := 0; col < columns; col++ {
		if col < len(headers) {
			widths[col] = tableCellWidth(headers[col])
		}
	}
	for _, row := range rows {
		for col, value := range row {
			if width := tableCellWidth(value); width > widths[col] {
				widths[col] = width
			}
		}
	}
	for col := range widths {
		widths[col] += 2 // one horizontal padding cell on each side
	}

	borderTop := tableBorderStyle.Render(tableBorderLine("╭", "┬", "╮", widths))
	borderMiddle := tableBorderStyle.Render(tableBorderLine("├", "┼", "┤", widths))
	borderBottom := tableBorderStyle.Render(tableBorderLine("╰", "┴", "╯", widths))
	vertical := tableBorderStyle.Render("│")
	headerStyles := make([]lipgloss.Style, columns)
	bodyStyles := make([]lipgloss.Style, columns)
	for col, width := range widths {
		headerStyles[col] = tableHeaderStyle.Padding(0, 1).Width(width)
		bodyStyles[col] = tableCellStyle.Width(width)
		if numericColumns[col] {
			bodyStyles[col] = bodyStyles[col].Align(lipgloss.Right)
		}
	}
	fieldValueRows := make([]bool, len(rows))
	fieldValueTable := len(headers) == 2 && strings.EqualFold(strings.TrimSpace(headers[0]), "Field") && strings.EqualFold(strings.TrimSpace(headers[1]), "Value")
	if fieldValueTable {
		for rowIndex, row := range rows {
			fieldValueRows[rowIndex] = len(row) > 0 && numericTableField(row[0])
		}
	}

	var b strings.Builder
	if title != "" {
		b.WriteString(tableTitleStyle.Render(title))
		b.WriteByte('\n')
	}
	b.WriteString(borderTop)
	b.WriteByte('\n')
	b.WriteString(renderFastRow(headers, -1, widths, headerStyles, bodyStyles, vertical, numericColumns, fieldValueRows))
	b.WriteByte('\n')
	b.WriteString(borderMiddle)
	for rowIndex, row := range rows {
		b.WriteByte('\n')
		b.WriteString(renderFastRow(row, rowIndex, widths, headerStyles, bodyStyles, vertical, numericColumns, fieldValueRows))
	}
	b.WriteByte('\n')
	b.WriteString(borderBottom)
	return b.String()
}

func tableCellWidth(value string) int {
	width := 0
	for len(value) > 0 {
		line := value
		if index := strings.IndexByte(value, '\n'); index >= 0 {
			line = value[:index]
			value = value[index+1:]
		} else {
			value = ""
		}
		lineWidth := 0
		if printableASCII(line) {
			lineWidth = len(line)
		} else {
			lineWidth = lipgloss.Width(line)
		}
		if lineWidth > width {
			width = lineWidth
		}
	}
	return width
}

func printableASCII(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] < 0x20 || value[i] > 0x7e {
			return false
		}
	}
	return true
}

func tableBorderLine(left, join, right string, widths []int) string {
	var b strings.Builder
	b.WriteString(left)
	for col, width := range widths {
		if col > 0 {
			b.WriteString(join)
		}
		b.WriteString(strings.Repeat("─", width))
	}
	b.WriteString(right)
	return b.String()
}

func renderFastRow(values []string, rowIndex int, widths []int, headerStyles, bodyStyles []lipgloss.Style, vertical string, numericColumns map[int]bool, fieldValueRows []bool) string {
	height := 1
	for _, value := range values {
		lines := strings.Count(value, "\n") + 1
		if lines > height {
			height = lines
		}
	}
	// Split only multiline rows, once per cell. Scanning from the first newline
	// for every visual line would make long wrapped TXT values quadratic.
	var cellLines [][]string
	if height > 1 {
		cellLines = make([][]string, len(values))
		for col, value := range values {
			cellLines[col] = strings.Split(value, "\n")
		}
	}

	var b strings.Builder
	for line := 0; line < height; line++ {
		if line > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(vertical)
		for col, width := range widths {
			if col > 0 {
				b.WriteString(vertical)
			}
			text := ""
			if col < len(values) {
				if height == 1 {
					text = values[col]
				} else if line < len(cellLines[col]) {
					text = cellLines[col][line]
				}
			}
			rightAlign := numericColumns[col] || (col == 1 && rowIndex >= 0 && rowIndex < len(fieldValueRows) && fieldValueRows[rowIndex])
			if rowIndex >= 0 && printableASCII(text) {
				padding := width - len(text) - 2
				if padding < 0 {
					padding = 0
				}
				b.WriteByte(' ')
				if rightAlign {
					b.WriteString(strings.Repeat(" ", padding))
					b.WriteString(text)
				} else {
					b.WriteString(text)
					b.WriteString(strings.Repeat(" ", padding))
				}
				b.WriteByte(' ')
				continue
			}
			style := bodyStyles[col]
			if rowIndex < 0 {
				style = headerStyles[col]
			}
			if rightAlign {
				style = style.Align(lipgloss.Right)
			}
			b.WriteString(style.Render(text))
		}
		b.WriteString(vertical)
	}
	return b.String()
}

func lowerTableHeaders(headers []string) []string {
	lowered := make([]string, 0, len(headers))
	for _, header := range headers {
		lowered = append(lowered, strings.ToLower(header))
	}
	return lowered
}

func numericTableColumns(headers []string) map[int]bool {
	columns := make(map[int]bool)
	for index, header := range headers {
		if numericTableField(header) {
			columns[index] = true
		}
	}
	return columns
}

func numericFieldValueCell(headers []string, rows [][]string, row, col int) bool {
	if row < 0 || row >= len(rows) || col != 1 || len(headers) != 2 {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(headers[0]), "Field") || !strings.EqualFold(strings.TrimSpace(headers[1]), "Value") {
		return false
	}
	if len(rows[row]) == 0 {
		return false
	}
	return numericTableField(rows[row][0])
}

func numericTableField(value string) bool {
	name := strings.ToLower(strings.TrimSpace(value))
	name = strings.ReplaceAll(name, "_", " ")
	name = strings.ReplaceAll(name, "-", " ")
	name = strings.Join(strings.Fields(name), " ")

	switch name {
	case "ttl",
		"serial",
		"serial number",
		"items",
		"count",
		"preference",
		"priority",
		"weight",
		"port",
		"refresh",
		"retry",
		"expiry",
		"negative caching ttl":
		return true
	default:
		return false
	}
}

func (a *App) emitRows(title string, fields []string, rows []map[string]any) error {
	switch a.Output {
	case "", tableOutput:
		displayRows := make([][]string, 0, len(rows))
		for _, row := range rows {
			display := make([]string, 0, len(fields))
			for _, field := range fields {
				display = append(display, stringify(row[field]))
			}
			displayRows = append(displayRows, display)
		}
		fmt.Fprintln(a.Stdout, renderTable(title, titleCaseFields(fields), displayRows))
	case jsonOutput:
		encoder := json.NewEncoder(a.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(rows)
	case csvOutput:
		writer := csv.NewWriter(a.Stdout)
		if err := writer.Write(fields); err != nil {
			return err
		}
		for _, row := range rows {
			values := make([]string, 0, len(fields))
			for _, field := range fields {
				values = append(values, stringify(row[field]))
			}
			if err := writer.Write(values); err != nil {
				return err
			}
		}
		writer.Flush()
		return writer.Error()
	default:
		return fmt.Errorf("unsupported output format %q", a.Output)
	}
	return nil
}

func (a *App) emitObject(title string, fields []string, row map[string]any) error {
	if a.Output == jsonOutput {
		encoder := json.NewEncoder(a.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(row)
	}
	return a.emitRows(title, fields, []map[string]any{row})
}

func titleCaseFields(fields []string) []string {
	headers := make([]string, 0, len(fields))
	for _, field := range fields {
		parts := strings.FieldsFunc(field, func(r rune) bool {
			return r == '_' || r == '-'
		})
		for i := range parts {
			if parts[i] == "" {
				continue
			}
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
		headers = append(headers, strings.Join(parts, " "))
	}
	return headers
}

func stringify(value any) string {
	if value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	case []string:
		return strings.Join(typed, ", ")
	case []any, map[string]any:
		raw, err := json.Marshal(typed)
		if err == nil {
			return string(raw)
		}
	}
	return fmt.Sprint(value)
}

func sortedKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func actionRow(action, objectType, name, zone, view, message string) map[string]any {
	return map[string]any{
		"status":  "success",
		"action":  action,
		"type":    objectType,
		"name":    name,
		"zone":    zone,
		"view":    view,
		"message": message,
	}
}
