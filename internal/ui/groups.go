package ui

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/icortesb/lazykuma/internal/core"
	"github.com/icortesb/lazykuma/internal/kuma"
	"github.com/icortesb/lazykuma/internal/state"
)

// nameForm asks for one name: a new group's, or a group's new one; or the
// same for a section of a status page.
type nameForm struct {
	form
	title, intro string
	id           int // the group renamed; 0 for a new one
	// section is "add" or "rename" when the name is a page section's, which
	// the sections editor takes, and empty when it is a group's, which is
	// written to Kuma.
	section string
}

func newNameForm(title, intro, value string, id int) nameForm {
	f := form{fields: []textinputModel{newField("name  ", "Shop")}, shown: 1}
	f.fields[0].SetValue(value)
	f, _ = f.focusOn(0)
	return nameForm{form: f, title: title, intro: intro, id: id}
}

func (n nameForm) Update(msg tea.Msg) (nameForm, formAction, tea.Cmd) {
	f, act, cmd := n.form.update(msg)
	n.form = f
	return n, act, cmd
}

// Value is the name typed, which must not be empty.
func (n nameForm) Value() (string, error) {
	v := strings.TrimSpace(n.fields[0].Value())
	if v == "" {
		return "", fmt.Errorf("the name is empty")
	}
	return v, nil
}

func (n nameForm) View() string { return n.view(n.title, n.intro) }

// groupOptions is where a monitor can move: out of any group, or into a
// group that is not itself nor inside it.
func groupOptions(st state.Instance, mon state.Monitor) []option {
	skip := map[int]bool{mon.ID: true}
	if mon.IsGroup() {
		for _, d := range st.Descendants(mon.ID) {
			skip[d.ID] = true
		}
	}
	var groups []state.Monitor
	for _, m := range st.Monitors {
		if m.IsGroup() && !skip[m.ID] {
			groups = append(groups, m)
		}
	}
	sort.Slice(groups, func(i, j int) bool { return groupPath(st, groups[i]) < groupPath(st, groups[j]) })
	options := []option{{"No group", "0"}}
	for _, g := range groups {
		options = append(options, option{groupPath(st, g), strconv.Itoa(g.ID)})
	}
	return options
}

// groupPath names a group with the groups it is in, "Shop / EU".
func groupPath(st state.Instance, g state.Monitor) string {
	parts := []string{g.Name}
	seen := map[int]bool{g.ID: true}
	for p := g.Parent; p != 0 && !seen[p]; {
		parent, ok := st.Monitors[p]
		if !ok {
			break
		}
		seen[p] = true
		parts = append([]string{parent.Name}, parts...)
		p = parent.Parent
	}
	return strings.Join(parts, " / ")
}

// coveredBy is a group and everything in it, by id, for a maintenance: Kuma
// silences exactly the monitors a maintenance lists.
func coveredBy(st state.Instance, mon state.Monitor) []int {
	ids := []int{mon.ID}
	for _, d := range st.Descendants(mon.ID) {
		ids = append(ids, d.ID)
	}
	sort.Ints(ids)
	return ids
}

// countMonitors is how many monitors, groups not counted, are in a group.
func countMonitors(st state.Instance, g state.Monitor) int {
	n := 0
	for _, d := range st.Descendants(g.ID) {
		if !d.IsGroup() {
			n++
		}
	}
	return n
}

func addGroup(in *core.Instance, name string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		_, err := in.AddMonitor(ctx, kuma.NewGroup(name))
		return actionDone{name: in.Name(), action: "created", mon: "group " + name, err: err}
	}
}

// renameGroup and moveMonitor change one field, and so fetch the whole
// monitor first: editMonitor replaces it with what it receives.
func renameGroup(in *core.Instance, id int, name string) tea.Cmd {
	return editField(in, id, "renamed", "group "+name, func(m kuma.RawMonitor) { m["name"] = name })
}

func moveMonitor(in *core.Instance, mon state.Monitor, parent int) tea.Cmd {
	return editField(in, mon.ID, "moved", mon.Name, func(m kuma.RawMonitor) {
		if parent == 0 {
			m["parent"] = nil
			return
		}
		m["parent"] = float64(parent)
	})
}

func editField(in *core.Instance, id int, action, what string, change func(kuma.RawMonitor)) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		m, err := in.GetMonitor(ctx, id)
		if err != nil {
			return actionDone{name: in.Name(), action: action, mon: what, err: err}
		}
		change(m)
		return actionDone{name: in.Name(), action: action, mon: what, err: in.EditMonitor(ctx, m)}
	}
}

// toggleGroup pauses a running group, or resumes a paused one, together with
// everything in it. Kuma pauses only the monitor it is told to: the children
// of a paused group are listed as paused, but keep being checked.
func toggleGroup(in *core.Instance, g state.Monitor, ids []int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		action, call := "paused", in.Pause
		if !g.Active {
			action, call = "resumed", in.Resume
		}
		for _, id := range ids {
			if err := call(ctx, id); err != nil {
				return actionDone{name: in.Name(), action: action, mon: "group " + g.Name, err: err}
			}
		}
		return actionDone{name: in.Name(), action: action, mon: "group " + g.Name}
	}
}

func deleteGroup(in *core.Instance, g state.Monitor, withMonitors bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		return actionDone{name: in.Name(), action: "deleted", mon: "group " + g.Name, err: in.DeleteGroup(ctx, g.ID, withMonitors)}
	}
}
