package autostart

import (
	"path/filepath"
	"strings"
)

// DesktopEntry is the XDG autostart entry that runs the watch. Nothing
// collects the output of a program started this way, so it passes --log.
func DesktopEntry(exe string) string {
	return `[Desktop Entry]
Type=Application
Name=lazykuma watch
Comment=Uptime Kuma alerts
Exec=` + desktopQuote(exe) + ` watch --log
NoDisplay=true
X-GNOME-Autostart-enabled=true
Terminal=false
`
}

// desktopQuote quotes one word of an Exec key. The Desktop Entry spec
// applies three rules in turn, so they are written in reverse: % starts a
// field code; inside double quotes ", `, $ and \ take a backslash; and the
// value is a string, whose own escaping doubles every backslash once more.
func desktopQuote(s string) string {
	s = strings.ReplaceAll(s, "%", "%%")
	s = `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`", `$`, `\$`).Replace(s) + `"`
	return strings.ReplaceAll(s, `\`, `\\`)
}

// parseDesktop returns the binary of the entry's Exec key, or "".
func parseDesktop(entry string) string {
	for line := range strings.Lines(entry) {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "Exec="); ok {
			v = strings.NewReplacer(`\\`, `\`, `\s`, " ", `\n`, "\n", `\t`, "\t", `\r`, "\r").Replace(v)
			return strings.ReplaceAll(firstWord(v), "%%", "%")
		}
	}
	return ""
}

type xdg struct{ m *Manager }

func (m *Manager) desktopPath() string {
	return filepath.Join(m.Config, "autostart", "lazykuma-watch.desktop")
}

func (xdg) kind() string { return KindXDG }

func (x xdg) registered() (bool, string, error) {
	entry, ok, err := readFile(x.m.desktopPath())
	if err != nil || !ok {
		return false, "", err
	}
	return true, parseDesktop(entry), nil
}

// enable starts the watch now as well: the entry only takes effect at the
// next login.
func (x xdg) enable() error {
	if err := writeFile(x.m.desktopPath(), DesktopEntry(x.m.Exe)); err != nil {
		return err
	}
	if err := x.m.stopHolder(); err != nil {
		return err
	}
	return x.m.startWatch([]string{x.m.Exe, "watch", "--log"})
}

func (x xdg) disable() error {
	if err := removeFile(x.m.desktopPath()); err != nil {
		return err
	}
	return x.m.stopHolder()
}
