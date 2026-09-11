package ui

import (
	"errors"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/icortesb/lazykuma/internal/config"
	"github.com/icortesb/lazykuma/internal/kuma"
)

type formAction int

const (
	formNone formAction = iota
	formCancel
	formSubmit
)

// form is a column of text fields: tab and the arrows move between them,
// enter on the last one submits, esc cancels.
type form struct {
	fields []textinput.Model
	shown  int // how many of fields are in use, from the top
	focus  int
	err    string
}

func newField(prompt, placeholder string) textinput.Model {
	f := textinput.New()
	f.Prompt = prompt
	f.Placeholder = placeholder
	f.CharLimit = 512
	return f
}

func (f form) focusOn(i int) (form, tea.Cmd) {
	for j := range f.fields {
		f.fields[j].Blur()
	}
	f.focus = i
	return f, f.fields[i].Focus()
}

func (f form) update(msg tea.Msg) (form, formAction, tea.Cmd) {
	// Typed characters are always text here, even a word like "down".
	if k, ok := msg.(tea.KeyMsg); ok && k.Type != tea.KeyRunes {
		switch {
		case key.Matches(k, keys.Back):
			return f, formCancel, nil
		case k.Type == tea.KeyEnter:
			if f.focus == f.shown-1 {
				return f, formSubmit, nil
			}
			f, cmd := f.focusOn(f.focus + 1)
			return f, formNone, cmd
		case key.Matches(k, keys.Next):
			f, cmd := f.focusOn((f.focus + 1) % f.shown)
			return f, formNone, cmd
		case key.Matches(k, keys.Prev):
			f, cmd := f.focusOn((f.focus + f.shown - 1) % f.shown)
			return f, formNone, cmd
		}
	}
	var cmd tea.Cmd
	f.fields[f.focus], cmd = typeInto(f.fields[f.focus], msg)
	return f, formNone, cmd
}

// typeInto passes msg to a text field. Characters typed fast or pasted
// arrive as one message whose text can read like a key name, and the field
// takes "home" for the Home key instead of typing it; so they go in one at a
// time.
func typeInto(ti textinput.Model, msg tea.Msg) (textinput.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok || k.Type != tea.KeyRunes || len(k.Runes) < 2 || k.Paste {
		return ti.Update(msg)
	}
	var cmds []tea.Cmd
	for _, r := range k.Runes {
		var cmd tea.Cmd
		ti, cmd = ti.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		cmds = append(cmds, cmd)
	}
	return ti, tea.Batch(cmds...)
}

func (f form) view(title, intro string) string {
	var b strings.Builder
	b.WriteString(styleHeading.Render(title) + "\n")
	if intro != "" {
		b.WriteString(styleLabel.Render(intro) + "\n")
	}
	b.WriteString("\n")
	for i := 0; i < f.shown; i++ {
		b.WriteString(f.fields[i].View() + "\n")
	}
	if f.err != "" {
		b.WriteString("\n" + styleErr.Render(f.err) + "\n")
	}
	b.WriteString("\n" + styleFooter.Render(
		styleKey.Render("tab")+" next   "+styleKey.Render("enter")+" submit   "+styleKey.Render("esc")+" cancel"))
	return b.String()
}

// loginForm asks for the username and password of an instance, and the 2FA
// code once Kuma has said it wants one.
type loginForm struct {
	form
	busy bool
}

func newLoginForm() loginForm {
	pass := newField("password  ", "")
	pass.EchoMode = textinput.EchoPassword
	pass.EchoCharacter = '•'
	f := form{fields: []textinput.Model{
		newField("username  ", "admin"),
		pass,
		newField("2FA code  ", "123456"),
	}, shown: 2}
	f, _ = f.focusOn(0)
	return loginForm{form: f}
}

func (l loginForm) Update(msg tea.Msg) (loginForm, formAction, tea.Cmd) {
	if l.busy {
		return l, formNone, nil
	}
	f, act, cmd := l.form.update(msg)
	l.form = f
	if act == formSubmit {
		l.busy, l.err = true, ""
	}
	return l, act, cmd
}

// Values are what was typed; code is empty until Kuma asks for it.
func (l loginForm) Values() (username, password, code string) {
	return strings.TrimSpace(l.fields[0].Value()), l.fields[1].Value(), strings.TrimSpace(l.fields[2].Value())
}

// WithResult takes the answer to a login that did not succeed.
func (l loginForm) WithResult(err error) (loginForm, tea.Cmd) {
	l.busy = false
	var cmd tea.Cmd
	switch {
	case errors.Is(err, kuma.ErrTokenRequired):
		l.shown, l.err = 3, ""
		l.form, cmd = l.focusOn(2)
	case kuma.IsAuth(err):
		l.err = "wrong username, password or code"
		l.fields[2].SetValue("")
	default:
		l.err = err.Error()
	}
	return l, cmd
}

func (l loginForm) View(name, url string) string {
	out := l.view("Log in to "+name, url+" · the password is used once and never stored")
	if l.busy {
		out += "\n" + styleLabel.Render("logging in…")
	}
	return out
}

// addForm asks for a new instance's name and URL.
type addForm struct{ form }

func newAddForm() addForm {
	f := form{fields: []textinput.Model{
		newField("name  ", "home"),
		newField("url   ", "https://kuma.example.com"),
	}, shown: 2}
	f, _ = f.focusOn(0)
	return addForm{form: f}
}

func (a addForm) Update(msg tea.Msg) (addForm, formAction, tea.Cmd) {
	f, act, cmd := a.form.update(msg)
	a.form = f
	return a, act, cmd
}

// Values is the instance as typed.
func (a addForm) Values() config.Instance {
	return config.Instance{
		Name: strings.TrimSpace(a.fields[0].Value()),
		URL:  strings.TrimRight(strings.TrimSpace(a.fields[1].Value()), "/"),
	}
}

func (a addForm) WithError(err error) addForm {
	a.err = err.Error()
	return a
}

func (a addForm) View() string {
	return a.view("Add an instance", "the address you open Uptime Kuma at")
}
