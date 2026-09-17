package ui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/icortesb/lazykuma/internal/core"
	"github.com/icortesb/lazykuma/internal/kuma"
)

// tagsLoaded is an instance's tag list, which Kuma only gives when asked.
type tagsLoaded struct {
	instance string
	tags     []kuma.TagDef
	err      error
}

func loadTags(in *core.Instance) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		tags, err := in.Tags(ctx)
		return tagsLoaded{instance: in.Name(), tags: tags, err: err}
	}
}

type tagAction int

const (
	tagNone tagAction = iota
	tagBack
	tagNew
	tagEdit
	tagDelete
)

// tagsScreen lists an instance's tags.
type tagsScreen struct{ cursor int }

func (s tagsScreen) selected(tags []kuma.TagDef) (kuma.TagDef, bool) {
	if len(tags) == 0 {
		return kuma.TagDef{}, false
	}
	return tags[min(s.cursor, len(tags)-1)], true
}

func (s tagsScreen) Update(msg tea.KeyMsg, tags []kuma.TagDef) (tagsScreen, tagAction) {
	switch {
	case key.Matches(msg, keys.Up):
		if s.cursor > 0 {
			s.cursor--
		}
	case key.Matches(msg, keys.Down):
		if s.cursor < len(tags)-1 {
			s.cursor++
		}
	case key.Matches(msg, keys.Back):
		return s, tagBack
	case msg.String() == "n":
		return s, tagNew
	case len(tags) > 0 && msg.String() == "e":
		return s, tagEdit
	case len(tags) > 0 && msg.String() == "d":
		return s, tagDelete
	}
	return s, tagNone
}

func (s tagsScreen) View(name string, tags []kuma.TagDef, width, height int) string {
	var b strings.Builder
	b.WriteString(styleHeading.Render(name+" · tags") + "\n")
	b.WriteString(styleLabel.Render("put them on a monitor from its form (e); search them with /") + "\n\n")
	if len(tags) == 0 {
		b.WriteString(styleLabel.Render("no tags yet") + "\n")
	}
	for i, t := range tags {
		chip := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF")).Background(lipgloss.Color(t.Color)).Render(" " + truncate(t.Name, width-8) + " ")
		if i == min(s.cursor, len(tags)-1) {
			b.WriteString(styleRow.Render("›") + " " + chip + "\n")
			continue
		}
		b.WriteString("  " + chip + "\n")
	}
	b.WriteString("\n" + styleFooter.Render(
		styleKey.Render("n")+" new   "+styleKey.Render("e")+" edit   "+styleKey.Render("d")+" delete   "+styleKey.Render("esc")+" back"))
	return b.String()
}

// tagForm names a tag and picks its colour from Kuma's palette.
type tagForm struct {
	form
	id      int
	color   int  // index into kuma.TagColors
	onColor bool // the cursor is on the colour row
	// custom is the tag's own colour when it is not one of the palette's,
	// as Kuma's web UI allows: it is kept until the user picks another, so
	// renaming a tag does not repaint it.
	custom string
}

func newTagForm(t kuma.TagDef) tagForm {
	f := form{fields: []textinputModel{newField("name    ", "prod")}, shown: 1}
	f.fields[0].SetValue(t.Name)
	f, _ = f.focusOn(0)
	tf := tagForm{form: f, id: t.ID, custom: t.Color}
	for i, c := range kuma.TagColors {
		if strings.EqualFold(c.Hex, t.Color) {
			tf.color, tf.custom = i, ""
		}
	}
	return tf
}

// Update: tab moves between the name and the colour; on the colour, ←/→
// (or h/l) choose; enter saves from either.
func (t tagForm) Update(msg tea.Msg) (tagForm, formAction, tea.Cmd) {
	k, isKey := msg.(tea.KeyMsg)
	if isKey {
		switch {
		case key.Matches(k, keys.Back):
			return t, formCancel, nil
		case k.Type == tea.KeyEnter:
			return t, formSubmit, nil
		case k.Type == tea.KeyTab || k.Type == tea.KeyShiftTab:
			t.onColor = !t.onColor
			if t.onColor {
				t.fields[0].Blur()
				return t, formNone, nil
			}
			return t, formNone, t.fields[0].Focus()
		}
		if t.onColor {
			switch {
			case k.Type == tea.KeyRight || k.String() == "l":
				t.color = (t.color + 1) % len(kuma.TagColors)
				t.custom = ""
			case k.Type == tea.KeyLeft || k.String() == "h":
				t.color = (t.color + len(kuma.TagColors) - 1) % len(kuma.TagColors)
				t.custom = ""
			}
			return t, formNone, nil
		}
	}
	var cmd tea.Cmd
	t.fields[0], cmd = typeInto(t.fields[0], msg)
	return t, formNone, cmd
}

func (t tagForm) Values() (kuma.TagDef, error) {
	name := strings.TrimSpace(t.fields[0].Value())
	if name == "" {
		return kuma.TagDef{}, fmt.Errorf("the name is empty")
	}
	color := kuma.TagColors[t.color].Hex
	if t.custom != "" {
		color = t.custom
	}
	return kuma.TagDef{ID: t.id, Name: name, Color: color}, nil
}

func (t tagForm) View() string {
	title := "New tag"
	if t.id != 0 {
		title = "Edit tag"
	}
	var b strings.Builder
	b.WriteString(styleHeading.Render(title) + "\n\n")
	b.WriteString(t.fields[0].View() + "\n")
	b.WriteString(styleLabel.Render("colour  "))
	for i, c := range kuma.TagColors {
		swatch := lipgloss.NewStyle().Background(lipgloss.Color(c.Hex)).Render("  ")
		if i == t.color && t.custom == "" {
			swatch = "[" + swatch + "]"
		} else {
			swatch = " " + swatch + " "
		}
		b.WriteString(swatch)
	}
	if t.custom != "" {
		swatch := lipgloss.NewStyle().Background(lipgloss.Color(t.custom)).Render("  ")
		b.WriteString("  [" + swatch + "] " + styleValue.Render("custom "+t.custom))
	} else {
		b.WriteString("  " + styleValue.Render(kuma.TagColors[t.color].Name))
	}
	if t.onColor {
		b.WriteString(styleLabel.Render("  ←/→"))
	}
	b.WriteString("\n")
	if t.err != "" {
		b.WriteString("\n" + styleErr.Render(t.err) + "\n")
	}
	b.WriteString("\n" + styleFooter.Render(
		styleKey.Render("tab")+" name/colour   "+styleKey.Render("enter")+" save   "+styleKey.Render("esc")+" cancel"))
	return b.String()
}

func saveTag(in *core.Instance, t kuma.TagDef) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		if t.ID == 0 {
			_, err := in.AddTag(ctx, t.Name, t.Color)
			return actionDone{name: in.Name(), action: "created", mon: "tag " + t.Name, err: err}
		}
		return actionDone{name: in.Name(), action: "saved", mon: "tag " + t.Name, err: in.EditTag(ctx, t)}
	}
}

func deleteTag(in *core.Instance, t kuma.TagDef) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		return actionDone{name: in.Name(), action: "deleted", mon: "tag " + t.Name, err: in.DeleteTag(ctx, t.ID)}
	}
}
