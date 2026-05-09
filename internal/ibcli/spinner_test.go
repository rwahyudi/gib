package ibcli

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestSpinnerDisabledForBufferedStderr(t *testing.T) {
	var stderr bytes.Buffer
	app := testApp(t)
	app.Stderr = &stderr

	stop := app.startSpinner("Loading DNS records...")
	stop()
	stop()

	if stderr.String() != "" {
		t.Fatalf("spinner wrote to non-TTY stderr: %q", stderr.String())
	}
}

func TestWithSpinnerReturnsFunctionErrorAndKeepsBufferedStderrClean(t *testing.T) {
	var stderr bytes.Buffer
	app := testApp(t)
	app.Stderr = &stderr
	want := errors.New("search failed")

	err := app.withSpinner("Searching DNS records...", func() error {
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("withSpinner error = %v, want %v", err, want)
	}
	if stderr.String() != "" {
		t.Fatalf("spinner wrote to non-TTY stderr: %q", stderr.String())
	}
}

func TestSpinnerDisabledForBlankMessage(t *testing.T) {
	var stderr bytes.Buffer
	app := testApp(t)
	app.Stderr = &stderr

	stop := app.startSpinner(strings.Repeat(" ", 3))
	stop()

	if stderr.String() != "" {
		t.Fatalf("blank spinner wrote output: %q", stderr.String())
	}
}
