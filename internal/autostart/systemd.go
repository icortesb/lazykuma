package autostart

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const unitName = "lazykuma-watch.service"

// SystemdUnit is the user service that runs the watch. Its output goes to
// the journal, so it does not pass --log.
func SystemdUnit(exe string) string {
	return `[Unit]
Description=lazykuma watch: Uptime Kuma alerts
After=network-online.target

[Service]
ExecStart=` + systemdQuote(exe) + ` watch
Restart=on-failure
RestartSec=30

[Install]
WantedBy=default.target
`
}

// systemdQuote quotes one word of a command line in a unit file. Besides the
// C-style escapes inside double quotes, systemd expands % specifiers and $
// variables, so both are doubled.
func systemdQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, `%`, `%%`, `$`, `$$`).Replace(s) + `"`
}

// parseSystemd returns the binary of the unit's ExecStart, or "".
func parseSystemd(unit string) string {
	for line := range strings.Lines(unit) {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "ExecStart="); ok {
			return strings.NewReplacer("%%", "%", "$$", "$").Replace(firstWord(v))
		}
	}
	return ""
}

// firstWord is the first word of a command line: up to the closing quote,
// backslash escapes undone, when it is double-quoted; up to the first space
// otherwise.
func firstWord(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, `"`) {
		w, _, _ := strings.Cut(s, " ")
		return w
	}
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		switch c := s[i]; c {
		case '\\':
			if i+1 < len(s) {
				i++
				b.WriteByte(s[i])
			}
		case '"':
			return b.String()
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

type systemd struct{ m *Manager }

func (m *Manager) unitPath() string {
	return filepath.Join(m.Config, "systemd", "user", unitName)
}

func (systemd) kind() string { return KindSystemd }

func (s systemd) registered() (bool, string, error) {
	unit, ok, err := readFile(s.m.unitPath())
	if err != nil || !ok {
		return false, "", err
	}
	return true, parseSystemd(unit), nil
}

func (s systemd) enable() error {
	if err := writeFile(s.m.unitPath(), SystemdUnit(s.m.Exe)); err != nil {
		return err
	}
	for _, verb := range [][]string{{"daemon-reload"}, {"enable", unitName}, {"restart", unitName}} {
		if err := s.m.run("systemctl", append([]string{"--user"}, verb...)...); err != nil {
			return err
		}
	}
	return nil
}

func (s systemd) disable() error {
	path := s.m.unitPath()
	if err := s.m.run("systemctl", "--user", "disable", "--now", unitName); err != nil {
		// With the unit file already gone systemd does not know the unit,
		// and there is nothing left to disable.
		if _, serr := os.Stat(path); !errors.Is(serr, os.ErrNotExist) {
			return err
		}
	}
	if err := removeFile(path); err != nil {
		return err
	}
	return s.m.run("systemctl", "--user", "daemon-reload")
}
