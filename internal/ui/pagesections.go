package ui

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/icortesb/lazykuma/internal/core"
	"github.com/icortesb/lazykuma/internal/kuma"
	"github.com/icortesb/lazykuma/internal/state"
)

// keyHintsSections is the footer of a page's sections, and its section of
// the help.
const keyHintsSections = "a add section   r rename   m add monitor   d remove   K/J move up/down   ctrl+s save   esc back"

// secRow is one line of the sections editor: a section, or one of its
// monitors. mon is -1 on a section's own line.
type secRow struct{ sec, mon int }

// sectionsEditor arranges a page's sections and the monitors in each. It
// changes nothing in Kuma until the whole page is saved: saveStatusPage
// replaces every section at once. Its edits return a new editor and leave
// the one they were called on as it was, so a slice shared between the two
// is never written to.
type sectionsEditor struct {
	slug, title string
	sections    []kuma.PageSection
	cursor      int
	dirty       bool
}

// rows are the editor's lines in order: each section, then its monitors.
func (e sectionsEditor) rows() []secRow {
	var out []secRow
	for i, s := range e.sections {
		out = append(out, secRow{i, -1})
		for j := range s.Monitors {
			out = append(out, secRow{i, j})
		}
	}
	return out
}

// current is the row under the cursor; false when there are no rows.
func (e sectionsEditor) current() (secRow, bool) {
	rows := e.rows()
	if len(rows) == 0 {
		return secRow{}, false
	}
	return rows[min(max(e.cursor, 0), len(rows)-1)], true
}

// rowOf is the index of a row, for the cursor to follow what moved.
func (e sectionsEditor) rowOf(r secRow) int {
	for i, row := range e.rows() {
		if row == r {
			return i
		}
	}
	return 0
}

// edited is a copy to change: the sections and their monitor lists are new
// slices, and it is dirty.
func (e sectionsEditor) edited() sectionsEditor {
	secs := make([]kuma.PageSection, len(e.sections))
	for i, s := range e.sections {
		s.Monitors = append([]kuma.PageMonitor(nil), s.Monitors...)
		secs[i] = s
	}
	e.sections, e.dirty = secs, true
	return e
}

// addSection puts a new, empty section after the current one, or first on
// a page with none, and moves the cursor onto it.
func (e sectionsEditor) addSection(name string) sectionsEditor {
	at := 0
	if r, ok := e.current(); ok {
		at = r.sec + 1
	}
	e = e.edited()
	e.sections = append(e.sections[:at], append([]kuma.PageSection{{Name: name}}, e.sections[at:]...)...)
	e.cursor = e.rowOf(secRow{at, -1})
	return e
}

// renameSection names the section the cursor is in. Its id stays, so Kuma
// updates the section rather than making a new one.
func (e sectionsEditor) renameSection(name string) sectionsEditor {
	r, ok := e.current()
	if !ok {
		return e
	}
	e = e.edited()
	e.sections[r.sec].Name = name
	return e
}

// addMonitor puts a monitor at the end of the section the cursor is in and
// moves the cursor onto it.
func (e sectionsEditor) addMonitor(m kuma.PageMonitor) sectionsEditor {
	r, ok := e.current()
	if !ok {
		return e
	}
	e = e.edited()
	s := &e.sections[r.sec]
	s.Monitors = append(s.Monitors, m)
	e.cursor = e.rowOf(secRow{r.sec, len(s.Monitors) - 1})
	return e
}

// removeRow takes the monitor under the cursor off the page, or the section
// with all its monitors. The cursor stays on the line where it was, which
// is now the next one, or the last line when there is no next one.
func (e sectionsEditor) removeRow() sectionsEditor {
	r, ok := e.current()
	if !ok {
		return e
	}
	e = e.edited()
	if r.mon < 0 {
		e.sections = append(e.sections[:r.sec], e.sections[r.sec+1:]...)
	} else {
		s := &e.sections[r.sec]
		s.Monitors = append(s.Monitors[:r.mon], s.Monitors[r.mon+1:]...)
	}
	e.cursor = min(e.cursor, max(len(e.rows())-1, 0))
	return e
}

func (e sectionsEditor) moveUp() sectionsEditor   { return e.move(-1) }
func (e sectionsEditor) moveDown() sectionsEditor { return e.move(1) }

// move swaps the row under the cursor with its neighbour: a monitor within
// its section, a section with the one before or after it, carrying its
// monitors. At either end nothing changes, and the editor is not dirtied.
func (e sectionsEditor) move(by int) sectionsEditor {
	r, ok := e.current()
	if !ok {
		return e
	}
	if r.mon < 0 {
		to := r.sec + by
		if to < 0 || to >= len(e.sections) {
			return e
		}
		e = e.edited()
		e.sections[r.sec], e.sections[to] = e.sections[to], e.sections[r.sec]
		e.cursor = e.rowOf(secRow{to, -1})
		return e
	}
	to := r.mon + by
	if to < 0 || to >= len(e.sections[r.sec].Monitors) {
		return e
	}
	e = e.edited()
	mons := e.sections[r.sec].Monitors
	mons[r.mon], mons[to] = mons[to], mons[r.mon]
	e.cursor = e.rowOf(secRow{r.sec, to})
	return e
}

// up and down move the cursor over the lines.
func (e sectionsEditor) up() sectionsEditor {
	if e.cursor > 0 {
		e.cursor--
	}
	return e
}

func (e sectionsEditor) down() sectionsEditor {
	if e.cursor < len(e.rows())-1 {
		e.cursor++
	}
	return e
}

func (e sectionsEditor) View(width, height int) string {
	var b strings.Builder
	heading := "Sections of " + e.title
	if e.dirty {
		heading += " · not saved"
	}
	b.WriteString(styleHeading.Render(truncate(heading, width-2)) + "\n")
	b.WriteString(styleLabel.Render(truncate("nothing changes in Kuma until ctrl+s saves the page", width-2)) + "\n\n")
	if len(e.sections) == 0 {
		b.WriteString(styleLabel.Render("no sections yet: a adds one") + "\n")
		return b.String()
	}

	// Each line drawn, with the row it is when it is one: an empty
	// section's note is a line the cursor skips.
	type line struct {
		text string
		row  int
	}
	var lines []line
	cursorLine, row := 0, 0
	for _, s := range e.sections {
		if row == e.cursor {
			cursorLine = len(lines)
		}
		lines = append(lines, line{"▾ " + s.Name, row})
		row++
		for _, m := range s.Monitors {
			if row == e.cursor {
				cursorLine = len(lines)
			}
			lines = append(lines, line{"    " + m.Name, row})
			row++
		}
		if len(s.Monitors) == 0 {
			lines = append(lines, line{"    (no monitors)", -1})
		}
	}
	shown := max(height-4, 5)
	start := 0
	if cursorLine >= shown {
		start = cursorLine - shown + 1
	}
	for i := start; i < len(lines) && i < start+shown; i++ {
		l := lines[i]
		text := truncate(l.text, width-4)
		switch {
		case l.row == e.cursor:
			b.WriteString(styleRow.Render(" "+text+" ") + "\n")
		case l.row < 0:
			b.WriteString(" " + styleLabel.Render(text) + "\n")
		default:
			b.WriteString(" " + styleValue.Render(text) + "\n")
		}
	}
	return b.String()
}

// monitorOptions are the monitors and groups a section can take: every one
// not in it already, named with the groups it is in.
func monitorOptions(st state.Instance, sec kuma.PageSection) []option {
	in := map[int]bool{}
	for _, m := range sec.Monitors {
		in[m.ID] = true
	}
	var options []option
	for _, m := range st.Monitors {
		if !in[m.ID] {
			options = append(options, option{groupPath(st, m), strconv.Itoa(m.ID)})
		}
	}
	sort.Slice(options, func(i, j int) bool { return options[i].label < options[j].label })
	return options
}

// sectionsSaved is the editor taking its own save's success. It closes only
// when it still shows what was saved: edited again since, it stays open and
// not saved. A form, picker or question opened from it stays too, and the
// editor it returns to is no longer marked as not saved if nothing changed.
func (m Model) sectionsSaved(saved []kuma.PageSection) Model {
	if !m.onSections() || !reflect.DeepEqual(m.secs.sections, saved) {
		return m
	}
	m.secs.dirty = false
	if m.screen == screenPageSections {
		m.screen = screenPages
	}
	return m
}

// onSections is whether the user is on the sections editor, or on a form,
// picker or question opened from it.
func (m Model) onSections() bool {
	switch m.screen {
	case screenPageSections:
		return true
	case screenPick, screenName, screenConfirm:
		return m.backTo == screenPageSections
	}
	return false
}

// sectionsLoaded carries a page's sections as a visitor sees them now.
type sectionsLoaded struct {
	instance, slug, title string
	sections              []kuma.PageSection
	err                   error
}

// sectionsAnswered is a yes to a question the sections editor asked: to
// discard its changes, or to remove the section under its cursor.
type sectionsAnswered struct{ discard bool }

// loadSections reads a page's sections from its public page, the only
// place Kuma gives them.
func loadSections(in *core.Instance, slug, title string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		p, err := in.PublicPage(ctx, slug)
		return sectionsLoaded{instance: in.Name(), slug: slug, title: title, sections: p.Sections, err: err}
	}
}

// saveSections saves the page with these sections and the settings it has
// right now: saveStatusPage replaces the settings too, and the web UI may
// have changed them since the editor opened. If they cannot be read,
// nothing is saved, for saving without them would reset them.
func saveSections(in *core.Instance, slug, title string, sections []kuma.PageSection) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		const action = "saved the sections of"
		name := "status page " + title
		p, err := in.GetStatusPage(ctx, slug)
		if err != nil {
			return actionDone{name: in.Name(), action: action, mon: name, err: fmt.Errorf("nothing saved, its settings could not be read: %s", kuma.Brief(err))}
		}
		return actionDone{name: in.Name(), action: action, mon: name, sections: sections, err: in.SaveStatusPage(ctx, slug, p.Config, sections)}
	}
}
