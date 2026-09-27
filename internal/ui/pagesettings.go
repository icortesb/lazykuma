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
}

// pageSettings edits a page's settings. saveStatusPage replaces the page
// with what it receives, so the form starts from every setting Kuma sent
// and changes only the ones it shows.
type pageSettings struct {
	form
	page    kuma.StatusPage
	toggles []pageToggle
	toggle  int // the toggle the cursor is on; -1 while it is in the fields
}

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

	s := pageSettings{form: f, page: p, toggle: -1, toggles: []pageToggle{
		{key: "showTags", name: "show tags"},
		{key: "showCertificateExpiry", name: "certificate expiry"},
		{key: "showOnlyLastHeartbeat", name: "only last beat"},
	}}
	for i := range s.toggles {
		s.toggles[i].on, _ = c[s.toggles[i].key].(bool)
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
	if isKey && s.focus == s.shown-1 && key.Matches(k, keys.Next) {
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

// Config is the page's config with the form's settings in it: a copy, so
// the page the form was built from is left as Kuma sent it.
func (s pageSettings) Config() (map[string]any, error) {
	out := make(map[string]any, len(s.page.Config)+9)
	for k, v := range s.page.Config {
		out[k] = v
	}
	title := strings.TrimSpace(s.fields[0].Value())
	if title == "" {
		return nil, errors.New("the title is empty")
	}
	refresh := strings.TrimSpace(s.fields[3].Value())
	n, err := strconv.Atoi(refresh)
	if err != nil || n < 0 {
		return nil, fmt.Errorf("the refresh is %q; it is seconds, 0 or more", refresh)
	}
	theme := strings.ToLower(strings.TrimSpace(s.fields[5].Value()))
	known := false
	for _, t := range pageThemes {
		known = known || t == theme
	}
	if !known {
		return nil, fmt.Errorf("the theme is %q; it is %s", theme, strings.Join(pageThemes, ", "))
	}
	out["title"] = title
	out["description"] = strings.TrimSpace(s.fields[1].Value())
	out["footerText"] = strings.TrimSpace(s.fields[2].Value())
	out["autoRefreshInterval"] = n
	out["domainNameList"] = splitList(s.fields[4].Value())
	out["theme"] = theme
	for _, t := range s.toggles {
		out[t.key] = t.on
	}
	return out, nil
}

func (s pageSettings) View() string {
	var b strings.Builder
	b.WriteString(s.view("Settings of "+s.page.Title, "refresh in seconds · domains comma separated · theme auto, light or dark"))
	b.WriteString("\n\n")
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
		styleKey.Render("tab") + " to the toggles   " + styleKey.Render("enter") + " save from anywhere"))
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

// savePageSettings saves a page's settings with the sections it has right
// now: saveStatusPage replaces the sections too, and only the public page
// gives them. If they cannot be read, nothing is saved, for saving without
// them would wipe them.
func savePageSettings(in *core.Instance, slug string, config map[string]any) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		name := "status page " + fieldText(config["title"])
		pub, err := in.PublicPage(ctx, slug)
		if err != nil {
			return actionDone{name: in.Name(), action: "saved", mon: name, err: fmt.Errorf("nothing saved, its sections could not be read: %s", kuma.Brief(err))}
		}
		err = in.SaveStatusPage(ctx, slug, config, pub.Sections)
		return actionDone{name: in.Name(), action: "saved", mon: name, err: err}
	}
}
