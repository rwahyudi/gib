package ibcli

import (
	"strings"
	"testing"
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
