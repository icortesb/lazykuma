package ui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/icortesb/lazykuma/internal/kuma"
)

// rawEditor edits an object as the key/value pairs Kuma stores. It is how
// lazykuma reaches the monitor types and notification services it has no
// form for — Kuma does not publish their fields — and any field a curated
// form leaves out.
type rawEditor struct {
	what   string // "monitor" or "channel", for the title
	id     int    // 0 when creating
	keys   []string
	vals   map[string]string // each value as JSON text
	cursor int

	editing   bool
	addingKey bool
	input     textinputModel
	err       string
}

// rawSkeleton is what a new monitor of an uncurated type starts from: the
// fields Kuma expects of every monitor, with its own defaults.
func rawSkeleton(kind string) kuma.RawMonitor {
	return kuma.RawMonitor{
		"type": kind, "name": "", "interval": 60, "retryInterval": 60, "maxretries": 0,
		"timeout": 48, "accepted_statuscodes": []string{"200-299"},
		"notificationIDList": map[string]bool{}, "conditions": []any{},
	}
}

func newRawEditor(what string, obj map[string]any) rawEditor {
	e := rawEditor{what: what, vals: map[string]string{}, input: newField("value  ", "")}
	if id, ok := numberOf(obj["id"]); ok {
		e.id = id
	}
	for k, v := range obj {
		b, err := json.Marshal(v)
		if err != nil {
			b = []byte(`""`)
		}
		e.keys = append(e.keys, k)
		e.vals[k] = string(b)
	}
	sort.Strings(e.keys)
	return e
}

// Update handles a key: enter edits the value under the cursor, a adds a
// field, d removes one, ctrl+s saves, esc goes back.
func (e rawEditor) Update(msg tea.Msg) (rawEditor, formAction, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return e, formNone, nil
	}
	if e.editing {
		switch k.Type {
		case tea.KeyEsc:
			e.editing, e.addingKey = false, false
			e.input.Blur()
			return e, formNone, nil
		case tea.KeyEnter:
			return e.commit(), formNone, nil
		}
		var cmd tea.Cmd
		e.input, cmd = typeInto(e.input, msg)
		return e, formNone, cmd
	}

	switch {
	case k.Type == tea.KeyCtrlS:
		return e, formSubmit, nil
	case key.Matches(k, keys.Back):
		return e, formCancel, nil
	case key.Matches(k, keys.Up):
		if e.cursor > 0 {
			e.cursor--
		}
	case key.Matches(k, keys.Down):
		if e.cursor < len(e.keys)-1 {
			e.cursor++
		}
	case k.Type == tea.KeyEnter:
		if len(e.keys) == 0 {
			return e, formNone, nil
		}
		e.editing, e.err = true, ""
		e.input.Prompt = e.keys[e.cursor] + "  "
		e.input.SetValue(e.vals[e.keys[e.cursor]])
		return e, formNone, e.input.Focus()
	case k.String() == "a":
		e.editing, e.addingKey, e.err = true, true, ""
		e.input.Prompt = "new field  "
		e.input.SetValue("")
		return e, formNone, e.input.Focus()
	case k.String() == "d":
		if len(e.keys) > 0 {
			delete(e.vals, e.keys[e.cursor])
			e.keys = append(e.keys[:e.cursor], e.keys[e.cursor+1:]...)
			if e.cursor >= len(e.keys) && e.cursor > 0 {
				e.cursor--
			}
		}
	}
	return e, formNone, nil
}

// commit takes what was typed: a new field's name, or a value that must be
// JSON, so a string keeps its quotes and a number stays a number.
func (e rawEditor) commit() rawEditor {
	v := strings.TrimSpace(e.input.Value())
	if e.addingKey {
		if v == "" {
			e.err = "the field name is empty"
			return e
		}
		if _, exists := e.vals[v]; !exists {
			e.keys = append(e.keys, v)
			sort.Strings(e.keys)
			e.vals[v] = `""`
		}
		for i, k := range e.keys {
			if k == v {
				e.cursor = i
			}
		}
		e.editing, e.addingKey, e.err = false, false, ""
		e.input.Blur()
		return e
	}
	if !json.Valid([]byte(v)) {
		e.err = fmt.Sprintf("%s is not JSON: a text needs quotes, as in %q", v, "example")
		return e
	}
	e.vals[e.keys[e.cursor]] = v
	e.editing, e.err = false, ""
	e.input.Blur()
	return e
}

// Values is the object as typed. A name and a type are required: Kuma
// refuses anything without them, later and less clearly.
func (e rawEditor) Values() (map[string]any, error) {
	out := map[string]any{}
	for _, k := range e.keys {
		var v any
		if err := json.Unmarshal([]byte(e.vals[k]), &v); err != nil {
			return nil, fmt.Errorf("%s: %s is not JSON", k, e.vals[k])
		}
		out[k] = v
	}
	if name, _ := out["name"].(string); strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("the name is empty")
	}
	if kind, _ := out["type"].(string); kind == "" {
		return nil, fmt.Errorf("the type is empty")
	}
	return out, nil
}

func (e rawEditor) View(width, height int) string {
	title := "Fields of the new " + e.what
	if e.id != 0 {
		title = fmt.Sprintf("Fields of %s %d", e.what, e.id)
	}
	var b strings.Builder
	b.WriteString(styleHeading.Render(title) + "\n")
	b.WriteString(styleLabel.Render("values are JSON: \"text\", 20, true, [\"200-299\"]") + "\n\n")

	rows := max(height-8, 5)
	start := 0
	if e.cursor >= rows {
		start = e.cursor - rows + 1
	}
	for i := start; i < len(e.keys) && i < start+rows; i++ {
		k := e.keys[i]
		line := fmt.Sprintf("%-28s %s", truncate(k, 28), truncate(e.vals[k], max(width-32, 10)))
		if i == e.cursor && !e.editing {
			b.WriteString(styleRow.Render(" "+line+" ") + "\n")
			continue
		}
		b.WriteString(" " + styleValue.Render(line) + "\n")
	}
	if e.editing {
		b.WriteString("\n" + e.input.View() + "\n")
	}
	if e.err != "" {
		b.WriteString("\n" + styleErr.Render(e.err) + "\n")
	}
	b.WriteString("\n" + styleFooter.Render(
		styleKey.Render("enter")+" edit   "+styleKey.Render("a")+" add field   "+
			styleKey.Render("d")+" delete   "+styleKey.Render("ctrl+s")+" save   "+styleKey.Render("esc")+" back"))
	return lipgloss.NewStyle().Width(width).Render(b.String())
}
