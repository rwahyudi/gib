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
	tableBorderStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#38bdf8"))
	tableHeaderStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#67e8f9"))
	tableCellStyle      = lipgloss.NewStyle().Padding(0, 1)
	tableTitleStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#4ade80"))
	activeTableRowStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#052e16")).Background(lipgloss.Color("#4ade80")).ColorWhitespace(true)
)

func renderTable(title string, headers []string, rows [][]string) string {
	return renderTableWithRowStyles(title, headers, rows, nil)
}

func renderTableWithRowStyles(title string, headers []string, rows [][]string, rowStyles map[int]lipgloss.Style) string {
	headers = lowerTableHeaders(headers)
	numericColumns := numericTableColumns(headers)
	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(tableBorderStyle).
		Headers(headers...).
		Rows(rows...).
		StyleFunc(func(row, col int) lipgloss.Style {
			rightAlign := numericColumns[col] || numericFieldValueCell(headers, rows, row, col)
			if row == table.HeaderRow {
				style := tableHeaderStyle.Padding(0, 1)
				if rightAlign {
					return style.Align(lipgloss.Right)
				}
				return style
			}
			if rightAlign {
				style := tableCellStyle.Align(lipgloss.Right)
				if rowStyle, ok := rowStyles[row]; ok {
					return style.Inherit(rowStyle)
				}
				return style
			}
			if rowStyle, ok := rowStyles[row]; ok {
				return tableCellStyle.Inherit(rowStyle)
			}
			return tableCellStyle
		})

	if title == "" {
		return t.String()
	}
	return tableTitleStyle.Render(title) + "\n" + t.String()
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
