package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/icortesb/lazykuma/internal/kuma"
	"github.com/icortesb/lazykuma/internal/state"
)

// silenceLayout is how a window's ends are typed: local time, to the minute.
const silenceLayout = "2006-01-02 15:04"

// silenceForm quiets one monitor: now until it is resumed, or between two
// times.
type silenceForm struct {
	form
	// monitor is fixed when the form opens: the list under it can reorder
	// while the window is being typed.
	monitor state.Monitor
}

func newSilenceForm(mon state.Monitor) silenceForm {
	f := form{fields: []textinputModel{
		newField("title  ", "deploy"),
		newField("from   ", "now"),
		newField("to     ", "until I end it"),
	}, shown: 3}
	f, _ = f.focusOn(0)
	f.fields[0].SetValue("maintenance: " + mon.Name)
	return silenceForm{form: f, monitor: mon}
}

func (s silenceForm) Update(msg tea.Msg) (silenceForm, formAction, tea.Cmd) {
	f, act, cmd := s.form.update(msg)
	s.form = f
	return s, act, cmd
}

// Values is the title and the window. Both times zero means "now, until it
// is ended", which is Kuma's manual strategy.
func (s silenceForm) Values() (title string, start, end time.Time, err error) {
	title = strings.TrimSpace(s.fields[0].Value())
	if title == "" {
		return "", time.Time{}, time.Time{}, fmt.Errorf("the title is empty")
	}
	from := strings.TrimSpace(s.fields[1].Value())
	to := strings.TrimSpace(s.fields[2].Value())
	if from == "" && to == "" {
		return title, time.Time{}, time.Time{}, nil
	}
	if from == "" || to == "" {
		return "", time.Time{}, time.Time{}, fmt.Errorf("a window needs both ends, or neither")
	}
	start, err = time.ParseInLocation(silenceLayout, from, time.Local)
	if err != nil {
		return "", time.Time{}, time.Time{}, fmt.Errorf("%q is not a time like 2026-09-12 15:04", from)
	}
	end, err = time.ParseInLocation(silenceLayout, to, time.Local)
	if err != nil {
		return "", time.Time{}, time.Time{}, fmt.Errorf("%q is not a time like 2026-09-12 15:04", to)
	}
	if !end.After(start) {
		return "", time.Time{}, time.Time{}, fmt.Errorf("the window ends before it starts")
	}
	return title, start, end, nil
}

func (s silenceForm) View() string {
	return s.view("Silence "+s.monitor.Name,
		"leave both times empty to silence it now, until you end it; otherwise 2026-09-12 15:04")
}

// maintenanceScreen lists an instance's maintenance windows.
type maintenanceScreen struct {
	cursor int
}

type mtAction int

const (
	mtNone mtAction = iota
	mtBack
	mtEnd
)

// sortedMaintenances is the windows in a stable order: the ones silencing
// something now first, then by title.
func sortedMaintenances(ms map[int]kuma.Maintenance) []kuma.Maintenance {
	out := make([]kuma.Maintenance, 0, len(ms))
	for _, m := range ms {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		ai, aj := out[i].Status == "under-maintenance", out[j].Status == "under-maintenance"
		if ai != aj {
			return ai
		}
		if out[i].Title != out[j].Title {
			return out[i].Title < out[j].Title
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func (s maintenanceScreen) Update(msg tea.KeyMsg, windows []kuma.Maintenance) (maintenanceScreen, mtAction) {
	s.cursor = min(s.cursor, max(len(windows)-1, 0))
	switch {
	case key.Matches(msg, keys.Up):
		if s.cursor > 0 {
			s.cursor--
		}
	case key.Matches(msg, keys.Down):
		if s.cursor < len(windows)-1 {
			s.cursor++
		}
	case key.Matches(msg, keys.Back):
		return s, mtBack
	case len(windows) == 0:
	case msg.String() == "d", msg.String() == "e":
		return s, mtEnd
	}
	return s, mtNone
}

func (s maintenanceScreen) selected(windows []kuma.Maintenance) (kuma.Maintenance, bool) {
	if len(windows) == 0 {
		return kuma.Maintenance{}, false
	}
	return windows[min(s.cursor, len(windows)-1)], true
}

func (s maintenanceScreen) View(name string, windows []kuma.Maintenance, width, height int) string {
	var b strings.Builder
	b.WriteString(styleHeading.Render(name+" · silenced") + "\n\n")
	if len(windows) == 0 {
		b.WriteString(styleLabel.Render("nothing is silenced; m on a monitor silences it") + "\n")
	}
	cursor := min(s.cursor, max(len(windows)-1, 0))
	for i, m := range windows {
		when := "until ended"
		if m.Strategy == "single" {
			when = m.Start + " → " + m.End
		}
		line := fmt.Sprintf("%-24s %-13s %s", truncate(m.Title, 24), m.Status, when)
		if i == cursor {
			b.WriteString(styleRow.Render(" "+truncate(line, width-2)+" ") + "\n")
			continue
		}
		style := styleValue
		if m.Status == "under-maintenance" {
			style = styleMaint
		}
		b.WriteString(" " + style.Render(truncate(line, width-2)) + "\n")
	}
	b.WriteString("\n" + styleFooter.Render(
		styleKey.Render("d")+" delete it   "+styleKey.Render("esc")+" back"))
	return b.String()
}
