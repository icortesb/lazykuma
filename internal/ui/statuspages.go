package ui

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/icortesb/lazykuma/internal/core"
	"github.com/icortesb/lazykuma/internal/kuma"
)

// keyHintsPages is the footer of the status pages screen, and its section
// of the help.
const keyHintsPages = "n new   e edit   s sections   i incident   u unpin   o open   d delete   esc back"

// openURL shows a page in the user's browser, without waiting for it.
var openURL = func(u string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	case "darwin":
		cmd = exec.Command("open", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait() // reap it; the browser outlives this
	return nil
}

type pageAction int

const (
	pageNone pageAction = iota
	pageBack
	pageNew
	pageEdit     // the selected page's settings
	pageSections // the selected page's sections and their monitors
	pageIncident // pin an incident to the selected page
	pageUnpin    // take the selected page's incident down
	pageOpen     // show the selected page in the browser
	pageDelete
)

// pagesScreen lists an instance's status pages. It holds only the cursor:
// the pages come from the instance's state on every call, so a write's
// result shows as soon as the core applies it.
type pagesScreen struct{ cursor int }

func (s pagesScreen) selected(pages []kuma.StatusPage) (kuma.StatusPage, bool) {
	if len(pages) == 0 {
		return kuma.StatusPage{}, false
	}
	return pages[min(s.cursor, len(pages)-1)], true
}

func (s pagesScreen) Update(msg tea.KeyMsg, pages []kuma.StatusPage) (pagesScreen, pageAction) {
	s.cursor = min(s.cursor, max(len(pages)-1, 0))
	switch {
	case key.Matches(msg, keys.Up):
		if s.cursor > 0 {
			s.cursor--
		}
	case key.Matches(msg, keys.Down):
		if s.cursor < len(pages)-1 {
			s.cursor++
		}
	case key.Matches(msg, keys.Back):
		return s, pageBack
	case msg.String() == "n":
		return s, pageNew
	}
	if len(pages) == 0 {
		return s, pageNone
	}
	switch msg.String() {
	case "e":
		return s, pageEdit
	case "s":
		return s, pageSections
	case "i":
		return s, pageIncident
	case "u":
		return s, pageUnpin
	case "o":
		return s, pageOpen
	case "d":
		return s, pageDelete
	}
	return s, pageNone
}

// View lists the pages, one row each: title, slug, whether it is published,
// and its address, which is cut short first when the terminal is narrow.
func (s pagesScreen) View(name, base string, pages []kuma.StatusPage, width, height int) string {
	var b strings.Builder
	b.WriteString(styleHeading.Render(name+" · status pages") + "\n\n")
	if len(pages) == 0 {
		b.WriteString(styleLabel.Render("no status pages yet: n creates one") + "\n")
	}
	cursor := min(s.cursor, max(len(pages)-1, 0))
	const titleW, slugW, stateW = 24, 20, 9
	urlW := width - 2 - titleW - slugW - stateW - 3
	for i, p := range pages {
		status := "hidden"
		if p.Published {
			status = "published"
		}
		title := padRight(truncate(p.Title, titleW), titleW)
		slug := padRight(truncate(p.Slug, slugW), slugW)
		status = padRight(status, stateW)
		url := truncate(kuma.PageURL(base, p.Slug), urlW)
		if i == cursor {
			line := title + " " + slug + " " + status + " " + url
			b.WriteString(styleRow.Render(" "+truncate(line, width-2)+" ") + "\n")
			continue
		}
		b.WriteString(" " + styleValue.Render(title) + " " + styleLabel.Render(slug) + " " +
			styleValue.Render(status) + " " + styleLabel.Render(url) + "\n")
	}
	return b.String()
}

// padRight fills s with spaces to width columns, counting wide characters
// as the terminal does.
func padRight(s string, width int) string {
	if w := lipgloss.Width(s); w < width {
		return s + strings.Repeat(" ", width-w)
	}
	return s
}

// newPageForm asks for a new page's title and slug. The slug follows the
// title, as Kuma would have it, until the user types in it: from then on it
// is theirs, and a later change to the title leaves it alone.
type newPageForm struct {
	form
	ownSlug bool
}

func newNewPageForm() newPageForm {
	f := form{fields: []textinputModel{
		newField("title  ", "Shop status"),
		newField("slug   ", "shop-status"),
	}, shown: 2}
	f, _ = f.focusOn(0)
	return newPageForm{form: f}
}

func (p newPageForm) Update(msg tea.Msg) (newPageForm, formAction, tea.Cmd) {
	title, slug := p.fields[0].Value(), p.fields[1].Value()
	f, act, cmd := p.form.update(msg)
	p.form = f
	if p.fields[1].Value() != slug {
		p.ownSlug = true
	}
	if !p.ownSlug && p.fields[0].Value() != title {
		p.fields[1].SetValue(kuma.SlugFrom(p.fields[0].Value()))
		// SetValue leaves the cursor where it was; the user who moves to
		// the slug to add to it expects to type at its end.
		p.fields[1].CursorEnd()
	}
	return p, act, cmd
}

// Values are the title and slug typed, checked against Kuma's rule for a
// slug so that a bad one is caught here, with the reason.
func (p newPageForm) Values() (title, slug string, err error) {
	title = strings.TrimSpace(p.fields[0].Value())
	slug = strings.TrimSpace(p.fields[1].Value())
	if title == "" {
		return "", "", errors.New("the title is empty")
	}
	if !kuma.ValidSlug(slug) {
		return "", "", fmt.Errorf("%q can't be a slug: letters, digits and single dashes", slug)
	}
	return title, slug, nil
}

func (p newPageForm) View() string {
	return p.view("New status page", "the slug is its address, /status/<slug>")
}

// typedConfirm asks for a word before something that nothing brings back,
// where a y is too easy to press: a page's slug before deleting it, with
// its sections and incidents, or an instance's name before clearing its
// statistics.
type typedConfirm struct {
	form
	word     string // what must be typed
	title    string // what it is about, for the action to name
	question string
	intro    string
}

func newTypedConfirm(prompt, word, title, question, intro string) typedConfirm {
	f := form{fields: []textinputModel{newField(prompt, "")}, shown: 1}
	f, _ = f.focusOn(0)
	return typedConfirm{form: f, word: word, title: title, question: question, intro: intro}
}

// newSlugConfirm asks for a page's slug before deleting it.
func newSlugConfirm(title, slug string) typedConfirm {
	return newTypedConfirm("slug  ", slug, title, fmt.Sprintf("Delete the status page %q?", title),
		"Kuma deletes it with its sections and incidents. Type "+slug+" to confirm.")
}

func (c typedConfirm) Update(msg tea.Msg) (typedConfirm, formAction, tea.Cmd) {
	f, act, cmd := c.form.update(msg)
	c.form = f
	return c, act, cmd
}

// Confirmed is whether the word typed is the one asked for.
func (c typedConfirm) Confirmed() bool {
	return strings.TrimSpace(c.fields[0].Value()) == c.word
}

func (c typedConfirm) View() string {
	return c.view(c.question, c.intro)
}

func addPage(in *core.Instance, title, slug string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		_, err := in.AddStatusPage(ctx, title, slug)
		return actionDone{name: in.Name(), action: "created", mon: "status page " + title, err: err}
	}
}

func deletePage(in *core.Instance, p kuma.StatusPage) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		return actionDone{name: in.Name(), action: "deleted", mon: "status page " + p.Title, err: in.DeleteStatusPage(ctx, p.Slug)}
	}
}
