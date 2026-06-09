package ibcli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/mattn/go-isatty"
)

type spinner struct {
	writer  io.Writer
	message string
	done    chan struct{}
	stopped chan struct{}
	once    sync.Once
}

func (a *App) withSpinner(message string, fn func() error) error {
	done := a.debugPhase("phase", df("name", message))
	stop := a.startSpinner(message)
	err := fn()
	stop()
	done(err)
	return err
}

func (a *App) startSpinner(message string) func() {
	message = strings.TrimSpace(message)
	if message == "" || !a.spinnerEnabled() || a.debugEnabled() {
		return func() {}
	}
	s := &spinner{
		writer:  a.Stderr,
		message: message,
		done:    make(chan struct{}),
		stopped: make(chan struct{}),
	}
	go s.run()
	return s.stop
}

func (a *App) spinnerEnabled() bool {
	file, ok := a.Stderr.(*os.File)
	if !ok || file == nil {
		return false
	}
	return isatty.IsTerminal(file.Fd())
}

func (s *spinner) run() {
	frames := []string{"-", "\\", "|", "/"}
	ticker := time.NewTicker(120 * time.Millisecond)
	defer ticker.Stop()

	frame := 0
	s.render(frames[frame])
	for {
		select {
		case <-s.done:
			fmt.Fprint(s.writer, "\r\033[2K")
			close(s.stopped)
			return
		case <-ticker.C:
			frame = (frame + 1) % len(frames)
			s.render(frames[frame])
		}
	}
}

func (s *spinner) render(frame string) {
	fmt.Fprintf(s.writer, "\r%s %s", noteStyle.Render(frame), s.message)
}

func (s *spinner) stop() {
	s.once.Do(func() {
		close(s.done)
		<-s.stopped
	})
}
