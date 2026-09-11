package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/icortesb/lazykuma/internal/state"
)

// menuItem is an entry of the menu: an instance, or one of the fixed
// entries under them.
type menuItem struct {
	label  string
	desc   string
	inst   int // index into the app's instances; -1 for a fixed entry
	target screen
}

// menuModel is the menu of mdg-tui: the logo, the entries centred, the
// selected one with its description under it, and a footer saying which
// instances answer.
type menuModel struct {
	cursor int
}

// Update moves the cursor over items and reports the one chosen.
func (m menuModel) Update(msg tea.KeyMsg, items []menuItem) (menuModel, *menuItem) {
	m.cursor = min(m.cursor, len(items)-1)
	switch {
	case key.Matches(msg, keys.Up):
		if m.cursor > 0 {
			m.cursor--
		}
	case key.Matches(msg, keys.Down):
		if m.cursor < len(items)-1 {
			m.cursor++
		}
	case key.Matches(msg, keys.Select):
		it := items[m.cursor]
		return m, &it
	}
	return m, nil
}

func (m menuModel) View(width, height int, items []menuItem, footerLine, version, notice string) string {
	var b strings.Builder

	b.WriteString("\n")
	for _, line := range logoFor(width) {
		b.WriteString(center(width, styleLogo.Render(line)) + "\n")
	}
	b.WriteString("\n")
	b.WriteString(center(width, styleSubtitle.Render("Uptime Kuma, in the terminal")) + "\n\n")

	// The block logo takes six rows, so the airy spacing between entries is
	// only affordable on a tall terminal.
	gap := "\n"
	if height < 32 {
		gap = ""
	}

	cursor := min(m.cursor, len(items)-1)
	for n, it := range items {
		b.WriteString(gap)
		if n == cursor {
			b.WriteString(center(width, styleItemActive.Render(" ➤ "+it.label+" ")) + "\n")
			b.WriteString(center(width, styleItemDesc.Render(it.desc)) + "\n")
			continue
		}
		b.WriteString(center(width, styleItem.Render(it.label)) + "\n")
	}

	footer := menuFooter(width, footerLine, version, notice)
	body := strings.TrimRight(b.String(), "\n")

	// The logo and the entries sit in the middle of what the footer leaves,
	// rather than piling up at the top with a hole underneath.
	space := height - lipgloss.Height(footer)
	if space > lipgloss.Height(body) {
		body = lipgloss.PlaceVertical(space, lipgloss.Center, body)
	}
	return body + "\n" + footer
}

func menuFooter(width int, line, version, notice string) string {
	var b strings.Builder
	if notice != "" {
		b.WriteString(center(width, styleWarn.Render(notice)) + "\n")
	} else {
		b.WriteString("\n")
	}
	b.WriteString(center(width, line) + "\n")
	b.WriteString(center(width, styleFooter.Render(
		styleKey.Render("↑/k ↓/j")+" move   "+
			styleKey.Render("enter")+" select   "+
			styleKey.Render("?")+" help   "+
			styleKey.Render("q")+" quit")) + "\n")
	b.WriteString(center(width, styleFooter.Render(versionLabel(version))))
	return b.String()
}

// versionLabel puts the v in front of a bare version number and leaves the
// rest alone: git describe already answers v0.1.0, and a build outside git
// says dev.
func versionLabel(version string) string {
	if version != "" && version[0] >= '0' && version[0] <= '9' {
		return "v" + version
	}
	return version
}

// semaphore is the one-line answer to "which instances answer right now".
func semaphore(names []string, states []state.Instance) string {
	parts := make([]string, 0, len(names))
	for i, name := range names {
		parts = append(parts, styleLabel.Render(name)+" "+connMark(states[i].Conn))
	}
	return strings.Join(parts, styleFooter.Render("   "))
}

func connMark(c state.Conn) string {
	switch c {
	case state.ConnOK:
		return styleOK.Render(c.String())
	case state.ConnNoCred, state.ConnBadCred:
		return styleWarn.Render(c.String())
	case state.ConnDown, state.ConnUnsupported:
		return styleErr.Render(c.String())
	}
	return styleLabel.Render(c.String())
}

// instanceDesc is what the menu says under a selected instance.
func instanceDesc(url string, st state.Instance) string {
	switch st.Conn {
	case state.ConnOK:
		c := st.Counts()
		desc := url + " · " + monitors(c.Total)
		if c.Down > 0 {
			desc += fmt.Sprintf(" · %d down", c.Down)
		}
		return desc
	case state.ConnNoCred:
		return "not logged in yet · enter to log in"
	case state.ConnBadCred:
		return "Kuma refused the token · enter to log in again"
	case state.ConnDown:
		return "down: " + st.Detail
	case state.ConnUnsupported:
		return st.Detail
	}
	return "connecting to " + url + "…"
}
