package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/icortesb/lazykuma/internal/state"
)

// The list keeps this width beside the detail; under wideAt columns the
// detail goes below the list instead.
const (
	listWidth = 34
	wideAt    = 90
)

// instanceScreen is the monitors of one instance: a list, and the detail of
// the one under the cursor.
type instanceScreen struct {
	cursor    int
	filter    textinput.Model
	filtering bool
}

func newInstanceScreen() instanceScreen {
	f := textinput.New()
	f.Prompt = "/"
	f.Placeholder = "filter by name or target"
	return instanceScreen{filter: f}
}

type instAction int

const (
	instNone instAction = iota
	instBack
	instToggle    // pause or resume the selected monitor
	instNew       // create a monitor
	instEdit      // edit the selected monitor
	instDelete    // delete the selected monitor
	instRaw       // edit the selected monitor's fields directly
	instSilence   // silence the selected monitor
	instSilenced  // what is silenced right now
	instChannels  // the instance's notification channels
	instIncidents // the instance's state changes
)

// visible is the monitors the list shows: sorted, and matching the filter.
func (s instanceScreen) visible(st state.Instance) []state.Monitor {
	all := st.Sorted()
	q := strings.ToLower(strings.TrimSpace(s.filter.Value()))
	if q == "" {
		return all
	}
	out := all[:0:0]
	for _, m := range all {
		if strings.Contains(strings.ToLower(m.Name), q) || strings.Contains(strings.ToLower(m.Target()), q) {
			out = append(out, m)
		}
	}
	return out
}

// selected is the monitor under the cursor.
func (s instanceScreen) selected(st state.Instance) (state.Monitor, bool) {
	mons := s.visible(st)
	if len(mons) == 0 {
		return state.Monitor{}, false
	}
	return mons[min(s.cursor, len(mons)-1)], true
}

// Update handles a key and says what the app should do about it.
func (s instanceScreen) Update(msg tea.KeyMsg, st state.Instance) (instanceScreen, instAction, tea.Cmd) {
	if s.filtering {
		switch msg.Type {
		case tea.KeyEsc:
			s.filtering = false
			s.filter.SetValue("")
			s.filter.Blur()
			s.cursor = 0
			return s, instNone, nil
		case tea.KeyEnter:
			s.filtering = false
			s.filter.Blur()
			return s, instNone, nil
		}
		var cmd tea.Cmd
		s.filter, cmd = typeInto(s.filter, msg)
		s.cursor = 0
		return s, instNone, cmd
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
		return s, instNone, s.filter.Focus()
	case key.Matches(msg, keys.Pause):
		if n > 0 {
			return s, instToggle, nil
		}
	case msg.String() == "n":
		return s, instNew, nil
	case msg.String() == "c":
		return s, instChannels, nil
	case msg.String() == "i":
		return s, instIncidents, nil
	case n > 0 && msg.String() == "e":
		return s, instEdit, nil
	case n > 0 && msg.String() == "d":
		return s, instDelete, nil
	case n > 0 && msg.String() == "r":
		return s, instRaw, nil
	case n > 0 && msg.String() == "m":
		return s, instSilence, nil
	case msg.String() == "M":
		return s, instSilenced, nil
	case key.Matches(msg, keys.Back):
		if s.filter.Value() != "" {
			s.filter.SetValue("")
			s.cursor = 0
			return s, instNone, nil
		}
		return s, instBack, nil
	}
	return s, instNone, nil
}

// View draws the screen in width × height cells.
func (s instanceScreen) View(name string, st state.Instance, width, height int) string {
	head := s.header(name, st)
	mons := s.visible(st)
	cursor := min(s.cursor, max(len(mons)-1, 0))

	bodyHeight := height - lipgloss.Height(head) - 1
	if bodyHeight < 6 {
		bodyHeight = 6
	}

	var body string
	if width >= wideAt {
		lw := listWidth
		list := s.list(mons, cursor, lw-4, bodyHeight-2)
		detail := s.detail(mons, cursor, width-lw-4, bodyHeight-2)
		body = lipgloss.JoinHorizontal(lipgloss.Top,
			stylePanel.Width(lw-2).Height(bodyHeight-2).Render(list),
			stylePanel.Width(width-lw-2).Height(bodyHeight-2).Render(detail),
		)
	} else {
		listH := max(bodyHeight/2-2, 3)
		detailH := max(bodyHeight-listH-4, 3)
		list := s.list(mons, cursor, width-4, listH)
		detail := s.detail(mons, cursor, width-4, detailH)
		body = lipgloss.JoinVertical(lipgloss.Left,
			stylePanel.Width(width-2).Height(listH).Render(list),
			stylePanel.Width(width-2).Height(detailH).Render(detail),
		)
	}
	return head + "\n" + body
}

// keyHints is the footer of the instance screen, which now does rather more
// than watch.
const keyHints = "n new   e edit   d delete   r fields   p pause   m silence   M silenced   c channels   i incidents   / filter   esc menu"

func (s instanceScreen) header(name string, st state.Instance) string {
	c := st.Counts()
	parts := []string{
		styleHeading.Render(name),
		styleValue.Render(monitors(c.Total)),
		styleOK.Render(fmt.Sprintf("%d up", c.Up)),
	}
	if c.Down > 0 {
		parts = append(parts, styleErr.Render(fmt.Sprintf("%d down", c.Down)))
	}
	if c.Paused > 0 {
		parts = append(parts, styleLabel.Render(fmt.Sprintf("%d paused", c.Paused)))
	}
	line := strings.Join(parts, styleLabel.Render(" · "))

	if st.Conn != state.ConnOK {
		note := st.Conn.String()
		if !st.StaleSince.IsZero() {
			note = fmt.Sprintf("stale since %s · %s", st.StaleSince.Local().Format("15:04"), note)
		}
		line += "   " + styleWarn.Render(note)
	}
	if s.filtering || s.filter.Value() != "" {
		line += "\n" + s.filter.View()
	}
	return line
}

func (s instanceScreen) list(mons []state.Monitor, cursor, width, height int) string {
	if len(mons) == 0 {
		if s.filter.Value() != "" {
			return styleLabel.Render("nothing matches")
		}
		return styleLabel.Render("no monitors yet")
	}
	// Scroll so the cursor stays in view.
	start := 0
	if cursor >= height {
		start = cursor - height + 1
	}
	var lines []string
	for i := start; i < len(mons) && i < start+height; i++ {
		m := mons[i]
		right := "—"
		switch {
		case m.Status() == state.StatusPaused:
			right = "paused"
		default:
			if b, ok := m.Last(); ok && b.HasPing {
				right = ms(b.Ping)
			}
		}
		nameW := width - 3 - lipgloss.Width(right)
		label := truncate(m.Name, nameW)
		pad := strings.Repeat(" ", max(nameW-lipgloss.Width(label), 0))
		if i == cursor {
			lines = append(lines, icon(m.Status())+" "+styleRow.Render(label+pad+" "+right))
			continue
		}
		lines = append(lines, icon(m.Status())+" "+styleValue.Render(label)+pad+" "+styleLabel.Render(right))
	}
	return strings.Join(lines, "\n")
}

func (s instanceScreen) detail(mons []state.Monitor, cursor, width, height int) string {
	if len(mons) == 0 {
		return ""
	}
	m := mons[cursor]
	facts := []string{statusWord(m.Status())}
	if m.HasUptime {
		facts = append(facts, fmt.Sprintf("%.1f%% 24h", m.Uptime24*100))
	}
	if m.HasCert {
		facts = append(facts, fmt.Sprintf("cert %d days", m.CertDays))
	}
	lines := []string{
		styleHeading.Render(truncate(m.Name, width)),
		styleLabel.Render(truncate(m.Target(), width)),
		strings.Join(facts, styleLabel.Render(" · ")),
		"",
	}

	graphW := width - 12
	pingLabel := "—"
	if m.HasAvgPing {
		pingLabel = ms(m.AvgPing)
	}
	lines = append(lines,
		styleLabel.Render("ping  ")+sparkline(m.Beats, graphW)+" "+styleValue.Render(pingLabel),
		styleLabel.Render("beats ")+beatBar(m.Beats, graphW),
		"",
	)

	// The newest messages, as many as fit.
	for i := len(m.Beats) - 1; i >= 0 && len(lines) < height; i-- {
		b := m.Beats[i]
		lines = append(lines, styleLabel.Render(b.Time.Local().Format("15:04"))+"  "+styleValue.Render(truncate(b.Msg, width-7)))
	}
	return strings.Join(lines, "\n")
}
