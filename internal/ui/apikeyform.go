package ui

import (
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// apiKeyForm asks for a new API key's name and when it expires.
type apiKeyForm struct {
	form
	// pending is set while Kuma makes the key: a second enter would make a
	// second key, with the same name, whose secret nobody would see.
	pending bool
	now     func() time.Time // the clock an expiry in the past is told by
}

func newAPIKeyForm() apiKeyForm {
	f := form{fields: []textinputModel{
		newField("name     ", "grafana"),
		newField("expires  ", "never, or 2026-12-31, or 2026-12-31 18:00"),
	}, shown: 2}
	f, _ = f.focusOn(0)
	return apiKeyForm{form: f, now: time.Now}
}

func (a apiKeyForm) Update(msg tea.Msg) (apiKeyForm, formAction, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok && a.pending && k.Type == tea.KeyEnter {
		return a, formNone, nil
	}
	f, act, cmd := a.form.update(msg)
	a.form = f
	return a, act, cmd
}

// Values are the name and the expiry as Kuma takes it: empty for never, a
// day as its last minute, or a day and a time. Kuma reads it in its own
// server's time zone, which may not be this machine's, so only a day
// already gone here is refused as past.
func (a apiKeyForm) Values() (name, expires string, err error) {
	name = strings.TrimSpace(a.fields[0].Value())
	if name == "" {
		return "", "", errors.New("the name is empty")
	}
	exp := strings.TrimSpace(a.fields[1].Value())
	if exp == "" {
		return name, "", nil
	}
	// The time is written back as Kuma's layout wants it: an hour typed
	// with one digit parses, but Kuma would not read it.
	t, err := time.Parse("2006-01-02 15:04", exp)
	if err != nil {
		if t, err = time.Parse("2006-01-02", exp); err == nil {
			t = t.Add(23*time.Hour + 59*time.Minute)
		}
	}
	if err != nil {
		return "", "", fmt.Errorf("%q is not an expiry: leave it empty for never, or type YYYY-MM-DD or YYYY-MM-DD HH:MM, in the Kuma server's time", exp)
	}
	if t.Format("2006-01-02") < a.now().Format("2006-01-02") {
		return "", "", fmt.Errorf("%s: that date has passed", t.Format("2006-01-02"))
	}
	return name, t.Format("2006-01-02 15:04"), nil
}

func (a apiKeyForm) View() string {
	out := a.view("New API key", "the key is shown once, right after it is made")
	if a.pending {
		out += "\n" + styleLabel.Render("making the key…")
	}
	return out
}

// apiKeyShown is the one screen that shows a new key's secret. Closing it
// zeroes it: nothing else in lazykuma keeps the key.
type apiKeyShown struct {
	name, key string
	id        int
}

// View draws the key whole, however narrow the terminal: a key cut short
// is a key lost. It wraps what does not fit rather than leave it to the
// cut every screen gets at the terminal's edge.
func (k apiKeyShown) View(width int) string {
	var b strings.Builder
	// Each line is styled on its own: a styled block of several lines
	// would be padded to one width, and trailing spaces copied with the
	// key would break it.
	write := func(style lipgloss.Style, lines string) {
		for _, l := range strings.Split(lines, "\n") {
			b.WriteString(style.Render(l) + "\n")
		}
	}
	wrap := func(s string) string {
		if width <= 0 {
			return s
		}
		return ansi.Wrap(s, width, "")
	}
	write(styleHeading, wrap(fmt.Sprintf("API key %q (id %d)", k.name, k.id)))
	b.WriteString("\n")
	key := k.key
	if width > 0 {
		key = ansi.Hardwrap(key, width, true)
	}
	write(styleValue, key)
	if strings.Contains(key, "\n") {
		write(styleLabel, wrap("the key is one line, wrapped here to fit: join the lines"))
	}
	b.WriteString("\n")
	write(styleWarn, wrap("copy it now: Kuma will not show it again, and lazykuma does not keep it"))
	b.WriteString("\n" + styleFooter.Render(styleKey.Render("esc/enter")+" close"))
	return b.String()
}
