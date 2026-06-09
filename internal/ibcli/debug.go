package ibcli

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type debugField struct {
	Name  string
	Value any
}

func df(name string, value any) debugField {
	return debugField{Name: strings.TrimSpace(name), Value: value}
}

func (a *App) debugEnabled() bool {
	return a != nil && a.Debug
}

func (a *App) debugEnsureStart() {
	if !a.debugEnabled() {
		return
	}
	if a.debugStartedAt.IsZero() {
		a.debugStartedAt = time.Now()
	}
}

func (a *App) debugEvent(event string, fields ...debugField) {
	if !a.debugEnabled() || a.Stderr == nil {
		return
	}
	now := time.Now()
	a.debugEnsureStart()
	elapsed := now.Sub(a.debugStartedAt)
	fmt.Fprintf(a.Stderr, "DEBUG %s +%s %s", now.Format("2006-01-02T15:04:05.000Z07:00"), debugDuration(elapsed), strings.TrimSpace(event))
	for _, field := range fields {
		if field.Name == "" {
			continue
		}
		fmt.Fprintf(a.Stderr, " %s=%s", field.Name, debugValue(field.Value))
	}
	fmt.Fprintln(a.Stderr)
}

func (a *App) debugPhase(name string, fields ...debugField) func(error) {
	if !a.debugEnabled() {
		return func(error) {}
	}
	started := time.Now()
	a.debugEvent(name+" start", fields...)
	return func(err error) {
		doneFields := append([]debugField{}, fields...)
		doneFields = append(doneFields, df("duration", time.Since(started)))
		if err != nil {
			doneFields = append(doneFields, df("error", err.Error()))
			a.debugEvent(name+" error", doneFields...)
			return
		}
		a.debugEvent(name+" done", doneFields...)
	}
}

func debugDuration(duration time.Duration) string {
	if duration < 0 {
		duration = 0
	}
	if duration < time.Millisecond {
		return "0ms"
	}
	return duration.Round(time.Millisecond).String()
}

func debugValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return `""`
	case string:
		return strconv.Quote(typed)
	case time.Duration:
		return strconv.Quote(debugDuration(typed))
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return fmt.Sprint(typed)
	case float32, float64:
		return fmt.Sprint(typed)
	default:
		return strconv.Quote(fmt.Sprint(typed))
	}
}

func argsContainDebug(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if arg == "--debug" || arg == "--debug=true" || arg == "--debug=1" {
			return true
		}
	}
	return false
}
