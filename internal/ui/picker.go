package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
)

// option is one line of a picker: what is shown, and what it means.
type option struct {
	label string
	value string
}

// picker asks for one of a short list of things — a monitor type, a
// notification service — before a form knows which fields to show.
type picker struct {
	title   string
	intro   string
	options []option
	cursor  int
}

func newPicker(title, intro string, options []option) picker {
	return picker{title: title, intro: intro, options: options}
}

// Update moves the cursor and reports the chosen value.
func (p picker) Update(msg tea.KeyMsg) (picker, formAction, string) {
	switch {
	case key.Matches(msg, keys.Up):
		if p.cursor > 0 {
			p.cursor--
		}
	case key.Matches(msg, keys.Down):
		if p.cursor < len(p.options)-1 {
			p.cursor++
		}
	case key.Matches(msg, keys.Back):
		return p, formCancel, ""
	case key.Matches(msg, keys.Select):
		if len(p.options) == 0 {
			return p, formCancel, ""
		}
		return p, formSubmit, p.options[p.cursor].value
	}
	return p, formNone, ""
}

func (p picker) View(width, height int) string {
	var b strings.Builder
	b.WriteString(styleHeading.Render(p.title) + "\n")
	if p.intro != "" {
		b.WriteString(styleLabel.Render(p.intro) + "\n")
	}
	b.WriteString("\n")

	rows := max(height-8, 5)
	start := 0
	if p.cursor >= rows {
		start = p.cursor - rows + 1
	}
	for i := start; i < len(p.options) && i < start+rows; i++ {
		line := truncate(p.options[i].label, width-4)
		if i == p.cursor {
			b.WriteString(styleRow.Render(" "+line+" ") + "\n")
			continue
		}
		b.WriteString(" " + styleValue.Render(line) + "\n")
	}
	b.WriteString("\n" + styleFooter.Render(
		styleKey.Render("enter")+" choose   "+styleKey.Render("esc")+" back"))
	return b.String()
}

// confirm asks before something irreversible.
type confirm struct {
	question string
	detail   string
}

// Update answers: yes on y, no on n or esc, nothing otherwise.
func (c confirm) Update(msg tea.KeyMsg) (answered, yes bool) {
	switch strings.ToLower(msg.String()) {
	case "y":
		return true, true
	case "n", "esc":
		return true, false
	}
	return false, false
}

func (c confirm) View() string {
	return styleWarn.Render(c.question) + "\n" +
		styleLabel.Render(c.detail) + "\n\n" +
		styleFooter.Render(styleKey.Render("y")+" yes   "+styleKey.Render("n")+" no")
}
