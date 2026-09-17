package ui

import (
	"fmt"
	"maps"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/icortesb/lazykuma/internal/kuma"
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
	// sel is the monitor under the cursor, by id: the list reorders as
	// states arrive, and a key must act on the monitor that was highlighted,
	// not on whatever reached its row since. cursor is its row, used when
	// sel is not in the list (none chosen yet, or filtered out).
	sel       int
	cursor    int
	filter    textinput.Model
	filtering bool
	show      state.Show
	order     state.Order
	folded    map[int]bool // groups folded by the user, by id
}

func newInstanceScreen() instanceScreen {
	f := textinput.New()
	f.Prompt = "/"
	f.Placeholder = "search names, targets and tags"
	return instanceScreen{filter: f, folded: map[int]bool{}}
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
	instNewGroup  // create a group
	instMove      // move the selected monitor or group into a group
	instClone     // a new monitor from the selected one
	instTags      // the instance's tags
)

// rows is the list as it shows: the tree, searched, filtered, sorted and
// folded.
func (s instanceScreen) rows(st state.Instance) []state.Row {
	return st.Tree(state.View{Query: s.filter.Value(), Show: s.show, Order: s.order, Folded: s.folded})
}

// index is the row under the cursor: the selected monitor's, or the
// cursor's own row, kept inside the list, when that monitor is not shown.
func (s instanceScreen) index(rows []state.Row) int {
	if s.sel != 0 {
		for i, r := range rows {
			if r.ID == s.sel {
				return i
			}
		}
	}
	return min(s.cursor, max(len(rows)-1, 0))
}

// selectedRow is the row under the cursor.
func (s instanceScreen) selectedRow(st state.Instance) (state.Row, bool) {
	rows := s.rows(st)
	if len(rows) == 0 {
		return state.Row{}, false
	}
	return rows[s.index(rows)], true
}

// moveTo puts the cursor on row i and selects its monitor.
func (s instanceScreen) moveTo(rows []state.Row, i int) instanceScreen {
	s.cursor, s.sel = i, 0
	if i >= 0 && i < len(rows) {
		s.sel = rows[i].ID
	}
	return s
}

// pin selects the monitor highlighted in st. The app pins to the state last
// drawn before it takes a new one, so a reorder keeps the cursor on it.
func (s instanceScreen) pin(st state.Instance) instanceScreen {
	rows := s.rows(st)
	if len(rows) == 0 {
		return s
	}
	return s.moveTo(rows, s.index(rows))
}

// selected is the monitor under the cursor, which may be a group.
func (s instanceScreen) selected(st state.Instance) (state.Monitor, bool) {
	r, ok := s.selectedRow(st)
	return r.Monitor, ok
}

// Update handles a key and says what the app should do about it.
func (s instanceScreen) Update(msg tea.KeyMsg, st state.Instance) (instanceScreen, instAction, tea.Cmd) {
	// Every key starts from the monitor highlighted now, so a search, a
	// show or a sort that still shows it keeps the cursor on it, and one
	// that hides it puts the cursor on the first row.
	s = s.pin(st)
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

	rows := s.rows(st)
	n := len(rows)
	row, hasRow := s.selectedRow(st)
	switch {
	case key.Matches(msg, keys.Up):
		if i := s.index(rows); i > 0 {
			s = s.moveTo(rows, i-1)
		}
	case key.Matches(msg, keys.Down):
		if i := s.index(rows); i < n-1 {
			s = s.moveTo(rows, i+1)
		}
	case key.Matches(msg, keys.Filter):
		s.filtering = true
		return s, instNone, s.filter.Focus()
	case key.Matches(msg, keys.Pause):
		if n > 0 {
			return s, instToggle, nil
		}
	case hasRow && row.Group && (msg.Type == tea.KeySpace || msg.Type == tea.KeyEnter):
		f := maps.Clone(s.folded)
		f[row.ID] = !f[row.ID]
		s.folded = f
	case msg.String() == "f":
		s.show, s.cursor = s.show.Next(), 0
	case msg.String() == "s":
		s.order = s.order.Next()
	case msg.String() == "g":
		return s, instNewGroup, nil
	case msg.String() == "t":
		return s, instTags, nil
	case hasRow && msg.String() == "v":
		return s, instMove, nil
	case hasRow && !row.Group && msg.String() == "C":
		return s, instClone, nil
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
	rows := s.rows(st)
	cursor := s.index(rows)

	bodyHeight := height - lipgloss.Height(head) - 1
	if bodyHeight < 6 {
		bodyHeight = 6
	}

	var body string
	if width >= wideAt {
		lw := listWidth
		list := s.list(rows, cursor, lw-4, bodyHeight-2)
		detail := s.detail(st, rows, cursor, width-lw-4, bodyHeight-2)
		body = lipgloss.JoinHorizontal(lipgloss.Top,
			stylePanel.Width(lw-2).Height(bodyHeight-2).Render(list),
			stylePanel.Width(width-lw-2).Height(bodyHeight-2).Render(detail),
		)
	} else {
		listH := max(bodyHeight/2-2, 3)
		detailH := max(bodyHeight-listH-4, 3)
		list := s.list(rows, cursor, width-4, listH)
		detail := s.detail(st, rows, cursor, width-4, detailH)
		body = lipgloss.JoinVertical(lipgloss.Left,
			stylePanel.Width(width-2).Height(listH).Render(list),
			stylePanel.Width(width-2).Height(detailH).Render(detail),
		)
	}
	return head + "\n" + body
}

// keyHints is the footer of the instance screen, which now does rather more
// than watch.
const keyHints = "n new   g group   e edit   d delete   v move   C clone   r fields   p pause   space fold   m silence   M silenced   t tags   c channels   i incidents   / search   f show   s sort   esc menu"

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
	if s.show != state.ShowAll {
		parts = append(parts, styleWarn.Render("show "+s.show.String()))
	}
	if s.order != state.OrderStatus {
		parts = append(parts, styleLabel.Render("sort "+s.order.String()))
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

func (s instanceScreen) list(rows []state.Row, cursor, width, height int) string {
	if len(rows) == 0 {
		if s.filter.Value() != "" || s.show != state.ShowAll {
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
	for i := start; i < len(rows) && i < start+height; i++ {
		r := rows[i]
		indent := strings.Repeat("  ", r.Depth)
		mark := icon(r.Rollup)
		right := "—"
		switch {
		case r.Group && r.Folded:
			mark = "▸"
			right = fmt.Sprintf("%s (%d)", icon(r.Rollup), r.Children)
		case r.Group:
			mark = "▾"
			right = icon(r.Rollup)
		case r.Rollup == state.StatusPaused:
			right = "paused"
		default:
			if b, ok := r.Last(); ok && b.HasPing {
				right = ms(b.Ping)
			}
		}
		prefix := indent + mark + " "
		nameW := width - lipgloss.Width(prefix) - 1 - lipgloss.Width(right)
		label := truncate(r.Name, max(nameW, 1))
		pad := strings.Repeat(" ", max(nameW-lipgloss.Width(label), 0))
		if i == cursor {
			lines = append(lines, prefix+styleRow.Render(label+pad+" "+right))
			continue
		}
		name := styleValue.Render(label)
		if r.Group {
			name = styleHeading.Render(label)
		}
		lines = append(lines, prefix+name+pad+" "+styleLabel.Render(right))
	}
	return strings.Join(lines, "\n")
}

func (s instanceScreen) detail(st state.Instance, rows []state.Row, cursor, width, height int) string {
	if len(rows) == 0 {
		return ""
	}
	r := rows[cursor]
	if r.Group {
		counts := map[state.Status]int{}
		for _, m := range st.Descendants(r.ID) {
			if !m.IsGroup() {
				counts[m.Status()]++
			}
		}
		var parts []string
		for _, sc := range []struct {
			st   state.Status
			word string
		}{{state.StatusUp, "up"}, {state.StatusDown, "down"}, {state.StatusPending, "pending"}, {state.StatusMaintenance, "maintenance"}, {state.StatusPaused, "paused"}} {
			if counts[sc.st] > 0 {
				parts = append(parts, fmt.Sprintf("%d %s", counts[sc.st], sc.word))
			}
		}
		lines := []string{
			styleHeading.Render(truncate(r.Name, width)),
			styleLabel.Render(fmt.Sprintf("group · %s", monitors(r.Children))),
			strings.Join(parts, styleLabel.Render(" · ")),
		}
		return strings.Join(lines, "\n")
	}
	m := r.Monitor
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
	}
	if chips := tagChips(m.Tags, width); chips != "" {
		lines = append(lines, chips)
	}
	lines = append(lines, strings.Join(facts, styleLabel.Render(" · ")), "")

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

// tagChips draws a monitor's tags in their own colours, "name" or
// "name:value", as many as fit.
func tagChips(tags []kuma.Tag, width int) string {
	var chips []string
	used := 0
	for _, t := range tags {
		text := t.Name
		if t.Value != "" {
			text += ":" + t.Value
		}
		chip := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF")).Background(lipgloss.Color(t.Color)).Render(" " + text + " ")
		w := lipgloss.Width(chip) + 1
		if used+w > width {
			break
		}
		chips = append(chips, chip)
		used += w
	}
	return strings.Join(chips, " ")
}
