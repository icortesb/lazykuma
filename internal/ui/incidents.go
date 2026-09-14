package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/icortesb/lazykuma/internal/state"
)

// incidentsScreen is an instance's state changes, newest first: when a
// monitor went down, when it came back, and what it said.
type incidentsScreen struct {
	cursor    int
	filter    textinputModel
	filtering bool
}

func newIncidentsScreen() incidentsScreen {
	f := newField("/", "filter by monitor")
	f.Prompt = "/"
	return incidentsScreen{filter: f}
}

type incAction int

const (
	incNone incAction = iota
	incBack
)

// visible is the incidents the screen shows: newest first, and only those
// of monitors matching the filter.
func (s incidentsScreen) visible(st state.Instance) []state.Incident {
	all := st.RecentIncidents(len(st.Incidents))
	q := strings.ToLower(strings.TrimSpace(s.filter.Value()))
	if q == "" {
		return all
	}
	out := all[:0:0]
	for _, inc := range all {
		if strings.Contains(strings.ToLower(monitorName(st, inc.MonitorID)), q) {
			out = append(out, inc)
		}
	}
	return out
}

// monitorName is the monitor's name, or its id when it is gone.
func monitorName(st state.Instance, id int) string {
	if m, ok := st.Monitors[id]; ok && m.Name != "" {
		return m.Name
	}
	return fmt.Sprintf("monitor %d", id)
}

func (s incidentsScreen) Update(msg tea.KeyMsg, st state.Instance) (incidentsScreen, incAction, tea.Cmd) {
	if s.filtering {
		switch msg.Type {
		case tea.KeyEsc:
			s.filtering = false
			s.filter.SetValue("")
			s.filter.Blur()
			s.cursor = 0
			return s, incNone, nil
		case tea.KeyEnter:
			s.filtering = false
			s.filter.Blur()
			return s, incNone, nil
		}
		var cmd tea.Cmd
		s.filter, cmd = typeInto(s.filter, msg)
		s.cursor = 0
		return s, incNone, cmd
	}

	n := len(s.visible(st))
	switch {
	case key.Matches(msg, keys.Up):
		if s.cursor > 0 {
			s.cursor--
		}
	case key.Matches(msg, keys.Down):
		if s.cursor < n-1 {
			s.cursor++
		}
	case key.Matches(msg, keys.Filter):
		s.filtering = true
		return s, incNone, s.filter.Focus()
	case key.Matches(msg, keys.Back):
		if s.filter.Value() != "" {
			s.filter.SetValue("")
			s.cursor = 0
			return s, incNone, nil
		}
		return s, incBack, nil
	}
	return s, incNone, nil
}

func (s incidentsScreen) View(name string, st state.Instance, width, height int) string {
	var b strings.Builder
	head := styleHeading.Render(name + " · incidents")
	if s.filtering || s.filter.Value() != "" {
		head += "\n" + s.filter.View()
	}
	b.WriteString(head + "\n\n")

	incidents := s.visible(st)
	if len(incidents) == 0 {
		b.WriteString(styleLabel.Render("no state changes recorded yet") + "\n")
	}
	rows := max(height-6, 3)
	start := 0
	if s.cursor >= rows {
		start = s.cursor - rows + 1
	}
	for i := start; i < len(incidents) && i < start+rows; i++ {
		inc := incidents[i]
		mark, style := "✔ up  ", styleOK
		if inc.Status != 1 { // kuma.StatusUp
			mark, style = "✖ down", styleErr
		}
		line := fmt.Sprintf("%s  %s  %-20s %s",
			inc.Time.Local().Format("01-02 15:04"), mark,
			truncate(monitorName(st, inc.MonitorID), 20), inc.Msg)
		if i == s.cursor {
			b.WriteString(styleRow.Render(" "+truncate(line, width-2)+" ") + "\n")
			continue
		}
		b.WriteString(" " + style.Render(truncate(line, width-2)) + "\n")
	}
	b.WriteString("\n" + styleFooter.Render(
		styleKey.Render("j/k")+" move   "+styleKey.Render("/")+" filter   "+styleKey.Render("esc")+" back"))
	return b.String()
}
