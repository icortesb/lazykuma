package ui

import (
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

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
