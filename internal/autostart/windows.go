package autostart

import (
	"errors"
	"strings"
)

const runName = "lazykuma-watch"

// RunValue is the Run key command that runs the watch. A console program
// started at login gets a console window; conhost --headless gives it one
// nobody sees. Nothing collects its output, so it passes --log. Windows
// paths cannot contain a double quote, so quoting needs no escapes.
func RunValue(exe string) string {
	return `conhost.exe --headless "` + exe + `" watch --log`
}

// parseRunValue returns the binary of a Run value, or "". Backslashes are
// path separators here, not escapes.
func parseRunValue(v string) string {
	v = strings.TrimSpace(v)
	if rest, ok := strings.CutPrefix(v, "conhost.exe --headless "); ok {
		v = strings.TrimSpace(rest)
	}
	if rest, ok := strings.CutPrefix(v, `"`); ok {
		exe, _, _ := strings.Cut(rest, `"`)
		return exe
	}
	exe, _, _ := strings.Cut(v, " ")
	return exe
}

type winRun struct{ m *Manager }

func (winRun) kind() string { return KindRunKey }

func (w winRun) registry() (Registry, error) {
	if w.m.Registry == nil {
		return nil, errors.New("no Windows registry")
	}
	return w.m.Registry, nil
}

func (w winRun) registered() (bool, string, error) {
	r, err := w.registry()
	if err != nil {
		return false, "", err
	}
	v, ok, err := r.Get(runName)
	if err != nil || !ok {
		return false, "", err
	}
	return true, parseRunValue(v), nil
}

// enable starts the watch now as well: the Run value only takes effect at
// the next login.
func (w winRun) enable() error {
	r, err := w.registry()
	if err != nil {
		return err
	}
	if err := r.Set(runName, RunValue(w.m.Exe)); err != nil {
		return err
	}
	if err := w.m.stopHolder(); err != nil {
		return err
	}
	return w.m.Start([]string{"conhost.exe", "--headless", w.m.Exe, "watch", "--log"})
}

func (w winRun) disable() error {
	r, err := w.registry()
	if err != nil {
		return err
	}
	if err := r.Delete(runName); err != nil {
		return err
	}
	return w.m.stopHolder()
}
