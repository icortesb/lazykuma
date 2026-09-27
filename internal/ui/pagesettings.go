package ui

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/icortesb/lazykuma/internal/core"
	"github.com/icortesb/lazykuma/internal/kuma"
)

// pageThemes are the looks Kuma offers a status page.
var pageThemes = []string{"auto", "light", "dark"}

// pageToggle is one yes/no setting of a page, under the text fields.
type pageToggle struct {
	key, name string
	on        bool
	was       bool // as the form filled it
}

// pageSettings edits a page's settings. saveStatusPage replaces the page
// with what it receives, so the form starts from every setting Kuma sent
// and changes only the ones it shows.
type pageSettings struct {
	form
	page    kuma.StatusPage
	toggles []pageToggle
	toggle  int // the toggle the cursor is on; -1 while it is in the fields
	// loaded is each field's text as the form filled it. A field still
	// showing it sends the page's own value back: a one-line field cannot
	// hold Kuma's multi-line markdown, and a null must stay a null.
	loaded []string
	// multiline marks the fields whose value in Kuma has more than one
	// line, which the form shows joined.
	multiline []bool
}

// The config keys the text fields fill, in their order.
var pageFieldKeys = []string{"title", "description", "footerText", "autoRefreshInterval", "domainNameList", "theme"}

// maxRefresh is the longest auto refresh the form takes: a day.
const maxRefresh = 86400

func (s pageSettings) inToggles() bool { return s.toggle >= 0 }

func newPageSettings(p kuma.StatusPage) pageSettings {
	f := form{fields: []textinputModel{
		newField("title        ", "Shop status"),
		newField("description  ", ""),
		newField("footer       ", ""),
		newField("refresh      ", "300"),
		newField("domains      ", "status.example.com"),
		newField("theme        ", "auto, light or dark"),
	}, shown: 6}
	for i := range f.fields {
		// No limit: a long description or domain list loaded cut short
		// would be saved cut short.
		f.fields[i].CharLimit = 0
	}
	c := p.Config
	f.fields[0].SetValue(fieldText(c["title"]))
	if f.fields[0].Value() == "" {
		f.fields[0].SetValue(p.Title)
	}
	f.fields[1].SetValue(fieldText(c["description"]))
	f.fields[2].SetValue(fieldText(c["footerText"]))
	// Kuma's own defaults, for a config that lacks them, so the form does
	// not refuse to save what it was given.
	refresh, theme := fieldText(c["autoRefreshInterval"]), fieldText(c["theme"])
	if refresh == "" {
		refresh = "300"
	}
	if theme == "" {
		theme = "auto"
	}
	f.fields[3].SetValue(refresh)
	f.fields[4].SetValue(fieldText(c["domainNameList"]))
	f.fields[5].SetValue(theme)
	f, _ = f.focusOn(0)
	loaded := make([]string, len(f.fields))
	multiline := make([]bool, len(f.fields))
	for i := range f.fields {
		loaded[i] = f.fields[i].Value()
		v, _ := c[pageFieldKeys[i]].(string)
		multiline[i] = strings.Contains(v, "\n")
	}

	s := pageSettings{form: f, page: p, toggle: -1, loaded: loaded, multiline: multiline, toggles: []pageToggle{
		{key: "showTags", name: "show tags"},
		{key: "showCertificateExpiry", name: "certificate expiry"},
		{key: "showOnlyLastHeartbeat", name: "only last beat"},
	}}
	for i := range s.toggles {
		s.toggles[i].on, _ = c[s.toggles[i].key].(bool)
		s.toggles[i].was = s.toggles[i].on
	}
	return s
}

// Update handles a key. Enter saves from anywhere: every setting is on one
// screen, so there is nothing to walk past. Tab walks from the last field
// onto the toggles and through them; space flips one.
func (s pageSettings) Update(msg tea.Msg) (pageSettings, formAction, tea.Cmd) {
	k, isKey := msg.(tea.KeyMsg)
	if isKey && k.Type == tea.KeyEnter {
		return s, formSubmit, nil
	}
	if isKey && s.inToggles() {
		switch {
		case key.Matches(k, keys.Back):
			return s, formCancel, nil
		case k.Type == tea.KeyShiftTab:
			return s.prevToggle()
		case key.Matches(k, keys.Up):
			return s.prevToggle()
		case k.Type == tea.KeyTab, key.Matches(k, keys.Down):
			if s.toggle < len(s.toggles)-1 {
				s.toggle++
			}
		case k.Type == tea.KeySpace:
			s.toggles[s.toggle].on = !s.toggles[s.toggle].on
		}
		return s, formNone, nil
	}
	if isKey && k.Type != tea.KeyRunes && s.focus == s.shown-1 && key.Matches(k, keys.Next) {
		s.toggle = 0
		for i := range s.fields {
			s.fields[i].Blur()
		}
		return s, formNone, nil
	}
	f, act, cmd := s.form.update(msg)
	s.form = f
	return s, act, cmd
}

// prevToggle goes up a toggle, or back out of them to the last field.
func (s pageSettings) prevToggle() (pageSettings, formAction, tea.Cmd) {
	if s.toggle > 0 {
		s.toggle--
		return s, formNone, nil
	}
	s.toggle = -1
	f, cmd := s.form.focusOn(s.shown - 1)
	s.form = f
	return s, formNone, cmd
}

// withChanges is a copy of a page's config with changes laid over it.
func withChanges(config, changes map[string]any) map[string]any {
	out := make(map[string]any, len(config)+len(changes))
	for k, v := range config {
		out[k] = v
	}
	for k, v := range changes {
		out[k] = v
	}
	return out
}

// Changes are the settings the user changed in the form, and the ones Kuma
// did not send, which the form filled with Kuma's defaults. Only these are
// laid over the page as Kuma has it when it is saved: the web UI may have
// changed the others since the form opened.
func (s pageSettings) Changes() (map[string]any, error) {
	out := map[string]any{}
	for i, k := range pageFieldKeys {
		raw := s.fields[i].Value()
		v := strings.TrimSpace(raw)
		if k == "title" && v == "" {
			return nil, errors.New("the title is empty")
		}
		// An untouched field keeps what the page has, as Kuma sent it. A
		// key Kuma did not send was filled with its default, which is sent.
		if _, had := s.page.Config[k]; had && raw == s.loaded[i] {
			continue
		}
		switch k {
		case "autoRefreshInterval":
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 || n > maxRefresh {
				return nil, fmt.Errorf("the refresh is %q; it is seconds, from 0 to %d", v, maxRefresh)
			}
			out[k] = n
		case "domainNameList":
			out[k] = splitList(v)
		case "theme":
			theme := strings.ToLower(v)
			known := false
			for _, t := range pageThemes {
				known = known || t == theme
			}
			if !known {
				return nil, fmt.Errorf("the theme is %q; it is %s", v, strings.Join(pageThemes, ", "))
			}
			out[k] = theme
		default:
			out[k] = v
		}
	}
	for _, t := range s.toggles {
		if _, had := s.page.Config[t.key]; !had || t.on != t.was {
			out[t.key] = t.on
		}
	}
	return out, nil
}

func (s pageSettings) View() string {
	var b strings.Builder
	b.WriteString(styleHeading.Render("Settings of "+s.page.Title) + "\n")
	b.WriteString(styleLabel.Render("refresh in seconds · domains comma separated · theme auto, light or dark") + "\n\n")
	for i := 0; i < s.shown; i++ {
		b.WriteString(s.fields[i].View() + "\n")
		if s.multiline[i] {
			b.WriteString(styleLabel.Render("             multi-line in Kuma; editing here makes it one line") + "\n")
		}
	}
	if s.err != "" {
		b.WriteString("\n" + styleErr.Render(s.err) + "\n")
	}
	b.WriteString("\n")
	for i, t := range s.toggles {
		mark := "[ ]"
		if t.on {
			mark = "[x]"
		}
		line := mark + " " + t.name
		if i == s.toggle {
			line = styleRow.Render(" " + line + " ")
		} else {
			line = styleValue.Render(" " + line)
		}
		b.WriteString(line + "\n")
	}
	b.WriteString(styleFooter.Render(styleKey.Render("space") + " toggle   " +
		styleKey.Render("tab") + " next   " + styleKey.Render("enter") + " save   " + styleKey.Render("esc") + " cancel"))
	return b.String()
}

// pageLoaded carries a page's settings as Kuma has them now, for the form.
type pageLoaded struct {
	instance, slug string
	page           kuma.StatusPage
	err            error
}

// loadPage reads a page's settings afresh: the list's copy is the one Kuma
// sent at login, or after lazykuma's own last write, and the web UI may
// have changed the page since.
func loadPage(in *core.Instance, slug string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		p, err := in.GetStatusPage(ctx, slug)
		return pageLoaded{instance: in.Name(), slug: slug, page: p, err: err}
	}
}

// savePageSettings saves the form's changes over the page's settings and
// sections as they are right now: saveStatusPage replaces all of them, and
// the web UI may have changed any since the form opened, the custom CSS or
// the analytics the form does not show among them. Only the public page
// gives the sections. If either cannot be read, nothing is saved, for
// saving without them would reset them.
func savePageSettings(in *core.Instance, slug, title string, changes map[string]any) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		if t, ok := changes["title"]; ok {
			title = fieldText(t)
		}
		name := "status page " + title
		p, err := in.GetStatusPage(ctx, slug)
		if err != nil {
			return actionDone{name: in.Name(), action: "saved", mon: name, err: fmt.Errorf("nothing saved, its settings could not be read: %s", kuma.Brief(err))}
		}
		pub, err := in.PublicPage(ctx, slug)
		if err != nil {
			return actionDone{name: in.Name(), action: "saved", mon: name, err: fmt.Errorf("nothing saved, its sections could not be read: %s", kuma.Brief(err))}
		}
		err = in.SaveStatusPage(ctx, slug, withChanges(p.Config, changes), pub.Sections)
		return actionDone{name: in.Name(), action: "saved", mon: name, err: err}
	}
}
