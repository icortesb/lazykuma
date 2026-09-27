package ui

import (
	"context"
	"errors"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/icortesb/lazykuma/internal/core"
	"github.com/icortesb/lazykuma/internal/kuma"
)

// The rows of the incident form, in their order.
const (
	incTitle = iota
	incContent
	incStyle
	incRows
)

// incidentForm posts a page's incident, or edits the one pinned to it. The
// content is markdown of several lines, so enter in it is a new line and
// ctrl+s saves from any row.
type incidentForm struct {
	slug, page string
	id         int // the incident edited; 0 for a new one
	title      textinput.Model
	content    textarea.Model
	style      int // into kuma.IncidentStyles
	row        int
	err        string
}

func newIncidentForm(slug, pageTitle string, current *kuma.PageIncident) incidentForm {
	title := newField("title    ", "Checkout is down")
	// Kuma keeps an incident's title in a column of 255 characters.
	title.CharLimit = 255
	content := textarea.New()
	content.ShowLineNumbers = false
	content.Placeholder = "What happened, and what is being done"
	content.Prompt = "  "
	content.SetHeight(5)
	f := incidentForm{slug: slug, page: pageTitle, title: title, content: content}
	if current != nil {
		f.id = current.ID
		f.title.SetValue(current.Title)
		f.content.SetValue(current.Content)
		for i, s := range kuma.IncidentStyles {
			if s == current.Style {
				f.style = i
			}
		}
	}
	f, _ = f.focusOn(incTitle)
	return f.withWidth(80)
}

// withWidth fits the fields to a terminal this wide.
func (f incidentForm) withWidth(width int) incidentForm {
	w := max(width-2, 20)
	f.title.Width = w - lipgloss.Width(f.title.Prompt) - 1
	f.content.SetWidth(w)
	return f
}

func (f incidentForm) focusOn(row int) (incidentForm, tea.Cmd) {
	f.row = row
	f.title.Blur()
	f.content.Blur()
	switch row {
	case incTitle:
		return f, f.title.Focus()
	case incContent:
		return f, f.content.Focus()
	}
	return f, nil
}

// Update handles a key, or passes the cursor's blink to the focused field.
func (f incidentForm) Update(msg tea.Msg) (incidentForm, formAction, tea.Cmd) {
	k, isKey := msg.(tea.KeyMsg)
	if isKey && k.Type != tea.KeyRunes {
		switch {
		case key.Matches(k, keys.Back):
			return f, formCancel, nil
		case k.Type == tea.KeyCtrlS:
			return f, formSubmit, nil
		case k.Type == tea.KeyTab:
			f, cmd := f.focusOn((f.row + 1) % incRows)
			return f, formNone, cmd
		case k.Type == tea.KeyShiftTab:
			f, cmd := f.focusOn((f.row + incRows - 1) % incRows)
			return f, formNone, cmd
		case k.Type == tea.KeyEnter && f.row == incTitle:
			f, cmd := f.focusOn(incContent)
			return f, formNone, cmd
		case k.Type == tea.KeyEnter && f.row == incStyle:
			return f, formSubmit, nil
		}
	}
	switch f.row {
	case incTitle:
		var cmd tea.Cmd
		f.title, cmd = typeInto(f.title, msg)
		return f, formNone, cmd
	case incContent:
		var cmd tea.Cmd
		f.content, cmd = typeIntoArea(f.content, msg)
		return f, formNone, cmd
	}
	if isKey {
		n := len(kuma.IncidentStyles)
		switch {
		case k.Type == tea.KeyLeft, k.String() == "h":
			f.style = (f.style + n - 1) % n
		case k.Type == tea.KeyRight, k.String() == "l":
			f.style = (f.style + 1) % n
		}
	}
	return f, formNone, nil
}

// typeIntoArea is typeInto for the content: typed fast, "home" would be
// taken for the Home key there too.
func typeIntoArea(ta textarea.Model, msg tea.Msg) (textarea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok || k.Type != tea.KeyRunes || len(k.Runes) < 2 || k.Paste {
		return ta.Update(msg)
	}
	var cmds []tea.Cmd
	for _, r := range k.Runes {
		var cmd tea.Cmd
		ta, cmd = ta.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		cmds = append(cmds, cmd)
	}
	return ta, tea.Batch(cmds...)
}

// Values is the incident as typed, with its id when it is an edit.
func (f incidentForm) Values() (kuma.PageIncident, error) {
	title := strings.TrimSpace(f.title.Value())
	content := f.content.Value()
	if title == "" {
		return kuma.PageIncident{}, errors.New("the title is empty")
	}
	if strings.TrimSpace(content) == "" {
		return kuma.PageIncident{}, errors.New("the content is empty")
	}
	return kuma.PageIncident{ID: f.id, Title: title, Content: content, Style: kuma.IncidentStyles[f.style]}, nil
}

func (f incidentForm) View() string {
	heading := "Post an incident on " + f.page
	if f.id != 0 {
		heading = "Edit the incident on " + f.page
	}
	var b strings.Builder
	b.WriteString(styleHeading.Render(heading) + "\n")
	b.WriteString(styleLabel.Render("pinned at the top of the page · the content is markdown") + "\n\n")
	b.WriteString(f.title.View() + "\n")
	label := styleLabel
	if f.row == incContent {
		label = styleValue
	}
	b.WriteString(label.Render("content") + "\n")
	b.WriteString(f.content.View() + "\n")

	name := kuma.IncidentStyles[f.style]
	shown := styleValue
	switch name {
	case "danger":
		shown = styleErr
	case "warning":
		shown = styleWarn
	}
	line := "style    " + shown.Render(name)
	if f.row == incStyle {
		line = "style    " + styleKey.Render("‹ ") + shown.Render(name) + styleKey.Render(" ›")
	}
	b.WriteString(line + "\n")
	if f.err != "" {
		b.WriteString("\n" + styleErr.Render(f.err) + "\n")
	}
	b.WriteString("\n" + styleFooter.Render(
		styleKey.Render("tab")+" next   "+styleKey.Render("←/→")+" style   "+
			styleKey.Render("ctrl+s")+" save   "+styleKey.Render("esc")+" cancel"))
	return b.String()
}

// incidentLoaded carries the incident pinned to a page now, if any, for
// the form.
type incidentLoaded struct {
	instance, slug, title string
	current               *kuma.PageIncident
	err                   error
}

// loadIncident reads a page's incident afresh from its public page, the
// only place Kuma gives it.
func loadIncident(in *core.Instance, slug, title string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		p, err := in.PublicPage(ctx, slug)
		msg := incidentLoaded{instance: in.Name(), slug: slug, title: title, err: err}
		for _, inc := range p.Incidents {
			if inc.Pinned {
				msg.current = &inc
				break
			}
		}
		return msg
	}
}

func postIncident(in *core.Instance, slug, title string, inc kuma.PageIncident) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		_, err := in.PostIncident(ctx, slug, inc)
		return actionDone{name: in.Name(), action: "posted the incident on", mon: "status page " + title, err: err}
	}
}

func unpinIncident(in *core.Instance, slug, title string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		return actionDone{name: in.Name(), action: "unpinned the incident on", mon: "status page " + title, err: in.UnpinIncident(ctx, slug)}
	}
}
