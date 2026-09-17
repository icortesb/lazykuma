package ui

import (
	"context"
	"fmt"
	"strconv"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/icortesb/lazykuma/internal/core"
	"github.com/icortesb/lazykuma/internal/kuma"
	"github.com/icortesb/lazykuma/internal/state"
)

// actionTimeout bounds a write: Kuma answers a call in milliseconds, but a
// login on a large idle instance can take half a minute.
const actionTimeout = 45 * time.Second

// current is the instance the screens are working on.
func (m Model) current() instance { return m.insts[m.cur] }

// formLists is what a monitor form offers on this instance. A new monitor
// starts in the given group and with the default channels ticked, which is
// what Kuma's own form does and what the ★ in the channels list promises; an
// edit has its own ticked by editMonitorForm.
func (m Model) formLists(parent int, forEdit bool) formLists {
	in := m.current()
	var out formLists
	out.groups = []toggle{{id: 0, name: "No group", on: parent == 0}}
	// No monitor has id 0, so this skips no group.
	for _, o := range groupOptions(in.st, state.Monitor{})[1:] {
		id, _ := strconv.Atoi(o.value)
		out.groups = append(out.groups, toggle{id: id, name: o.label, on: id == parent})
	}
	for _, t := range m.tagDefs[in.name()] {
		out.tags = append(out.tags, toggle{id: t.ID, name: t.Name})
	}
	for _, c := range in.st.Channels {
		out.channels = append(out.channels, toggle{id: c.ID, name: c.Name, on: !forEdit && c.IsDefault})
	}
	return out
}

// newParent is the group a new monitor starts in: the one under the
// cursor, or the group of the monitor under it.
func (m Model) newParent() int {
	r, ok := m.inst.selectedRow(m.current().st)
	switch {
	case !ok:
		return 0
	case r.Group:
		return r.ID
	}
	return r.Parent
}

// openMonitorForm shows the curated form for a type, or the field editor
// for a type that has none.
func (m Model) openMonitorForm(kind string) (Model, tea.Cmd) {
	if kind == "other" {
		types := m.current().st.Types
		if len(types) == 0 {
			// The server has not said which types it supports yet; the ones
			// every Kuma 2 implements are still worth offering.
			types = kuma.ClassicTypes
		}
		options := make([]option, 0, len(types))
		for _, t := range types {
			options = append(options, option{t, t})
		}
		m.pick = newPicker("Monitor type", "Kuma does not publish these types' fields, so they are edited directly", options)
		m.picking, m.screen = "rawtype", screenPick
		return m, nil
	}
	m.mform = newMonitorForm(kind, m.formLists(m.newParent(), false))
	m.backTo, m.screen = screenInstance, screenMonitor
	return m, nil
}

func (m Model) updatePick(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var act formAction
	var value string
	m.pick, act, value = m.pick.Update(msg)
	switch act {
	case formCancel:
		m.screen = m.backTo
	case formSubmit:
		switch m.picking {
		case "monitor":
			return m.openMonitorForm(value)
		case "rawtype":
			skeleton := rawSkeleton(value)
			if parent := m.newParent(); parent != 0 {
				skeleton["parent"] = parent
			}
			m.raw = newRawEditor("monitor", 0, skeleton)
			m.backTo, m.screen = screenInstance, screenRaw
		case "channel":
			if value == "other" {
				m.raw = newRawEditor("channel", 0, map[string]any{"name": "", "type": "", "isDefault": false})
				m.backTo, m.screen = screenChannels, screenRaw
				return m, nil
			}
			m.cform = newChannelForm(value)
			m.backTo, m.screen = screenChannels, screenChannel
		case "move":
			// The picker closes as the write goes: a second enter must not
			// send it again, and its result must not close a later screen.
			parent, _ := strconv.Atoi(value)
			m.screen = m.backTo
			return m, moveMonitor(m.current().inst, m.moving, parent)
		case "delgroup":
			g := m.moving
			switch value {
			case "keep":
				m.screen = m.backTo
				return m, deleteGroup(m.current().inst, g, false)
			case "all":
				m.ask = confirm{
					question: fmt.Sprintf("Delete %s and its %s?", g.Name, monitors(countMonitors(m.current().st, g))),
					detail:   "Kuma removes every monitor in the group and all of their history",
				}
				m.onYes, m.screen = deleteGroup(m.current().inst, g, true), screenConfirm
				return m, nil
			}
			m.screen = m.backTo
		}
	}
	return m, nil
}

func (m Model) updateName(msg tea.Msg) (tea.Model, tea.Cmd) {
	var act formAction
	var cmd tea.Cmd
	m.nform, act, cmd = m.nform.Update(msg)
	switch act {
	case formCancel:
		m.screen = m.backTo
	case formSubmit:
		name, err := m.nform.Value()
		if err != nil {
			m.nform.err = err.Error()
			return m, nil
		}
		if m.nform.id == 0 {
			return m, addGroup(m.current().inst, name)
		}
		return m, renameGroup(m.current().inst, m.nform.id, name)
	}
	return m, cmd
}

func (m Model) updateTags(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	in := m.current()
	tags := m.tagDefs[in.name()]
	var act tagAction
	m.tags, act = m.tags.Update(msg, tags)
	switch act {
	case tagBack:
		m.screen = screenInstance
	case tagNew:
		m.tform = newTagForm(kuma.TagDef{Color: kuma.TagColors[0].Hex})
		m.backTo, m.screen = screenTags, screenTag
	case tagEdit:
		if t, ok := m.tags.selected(tags); ok {
			m.tform = newTagForm(t)
			m.backTo, m.screen = screenTags, screenTag
		}
	case tagDelete:
		if t, ok := m.tags.selected(tags); ok {
			m.ask = confirm{
				question: fmt.Sprintf("Delete the tag %q?", t.Name),
				detail:   "Kuma takes it off every monitor that carries it",
			}
			m.onYes, m.backTo, m.screen = deleteTag(in.inst, t), screenTags, screenConfirm
		}
	}
	return m, nil
}

func (m Model) updateTagForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	var act formAction
	var cmd tea.Cmd
	m.tform, act, cmd = m.tform.Update(msg)
	switch act {
	case formCancel:
		m.screen = m.backTo
	case formSubmit:
		t, err := m.tform.Values()
		if err != nil {
			m.tform.err = err.Error()
			return m, nil
		}
		return m, saveTag(m.current().inst, t)
	}
	return m, cmd
}

func (m Model) updateMonitorForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	var act formAction
	var cmd tea.Cmd
	m.mform, act, cmd = m.mform.Update(msg)
	switch act {
	case formCancel:
		m.screen = m.backTo
	case formSubmit:
		mon, err := m.mform.Values()
		if err != nil {
			m.mform.err = err.Error()
			return m, nil
		}
		add, remove := m.mform.TagChanges()
		return m, saveMonitor(m.current().inst, mon, m.mform.id, add, remove)
	}
	return m, cmd
}

func (m Model) updateRaw(msg tea.Msg) (tea.Model, tea.Cmd) {
	var act formAction
	var cmd tea.Cmd
	m.raw, act, cmd = m.raw.Update(msg)
	switch act {
	case formCancel:
		m.screen = m.backTo
	case formSubmit:
		values, err := m.raw.Values()
		if err != nil {
			m.raw.err = err.Error()
			return m, nil
		}
		if m.raw.what == "channel" {
			return m, saveChannel(m.current().inst, values, m.raw.id)
		}
		var addTags []kuma.Tag
		if m.raw.id == 0 {
			addTags = m.raw.cloneTags
		}
		return m, saveMonitor(m.current().inst, kuma.RawMonitor(values), m.raw.id, addTags, nil)
	}
	return m, cmd
}

func (m Model) updateChannels(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	in := m.current()
	var act chanAction
	m.chans, act = m.chans.Update(msg, in.st.Channels)
	switch act {
	case chanBack:
		m.screen = screenInstance
	case chanNew:
		options := make([]option, 0, len(curatedServices)+1)
		for _, svc := range curatedServices {
			options = append(options, option{serviceLabel(svc), svc})
		}
		options = append(options, option{"Other service…", "other"})
		m.pick = newPicker("New channel", "Kuma supports about ninety; these three have a form", options)
		m.picking, m.backTo, m.screen = "channel", screenChannels, screenPick
	case chanEdit:
		if c, ok := m.chans.selected(in.st.Channels); ok {
			if isCurated(c.Type) {
				m.cform = editChannelForm(c)
				m.backTo, m.screen = screenChannels, screenChannel
			} else {
				cfg := map[string]any{}
				for k, v := range c.Config {
					cfg[k] = v
				}
				m.raw = newRawEditor("channel", c.ID, cfg)
				m.backTo, m.screen = screenChannels, screenRaw
			}
		}
	case chanDelete:
		if c, ok := m.chans.selected(in.st.Channels); ok {
			m.ask = confirm{
				question: fmt.Sprintf("Delete the channel %q?", c.Name),
				detail:   "monitors using it stop notifying through it",
			}
			m.onYes, m.backTo, m.screen = deleteChannel(in.inst, c), screenChannels, screenConfirm
		}
	case chanTest:
		if c, ok := m.chans.selected(in.st.Channels); ok {
			return m, testChannel(in.inst, c)
		}
	}
	return m, nil
}

func isCurated(svc string) bool {
	for _, s := range curatedServices {
		if s == svc {
			return true
		}
	}
	return false
}

func (m Model) updateChannelForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	var act formAction
	var cmd tea.Cmd
	m.cform, act, cmd = m.cform.Update(msg)
	switch act {
	case formCancel:
		m.screen = m.backTo
	case formSubmit:
		cfg, err := m.cform.Values()
		if err != nil {
			m.cform.err = err.Error()
			return m, nil
		}
		return m, saveChannel(m.current().inst, cfg, m.cform.id)
	}
	return m, cmd
}

func (m Model) updateSilence(msg tea.Msg) (tea.Model, tea.Cmd) {
	var act formAction
	var cmd tea.Cmd
	m.silence, act, cmd = m.silence.Update(msg)
	switch act {
	case formCancel:
		m.screen = m.backTo
	case formSubmit:
		title, start, end, err := m.silence.Values()
		if err != nil {
			m.silence.err = err.Error()
			return m, nil
		}
		// The monitor was chosen when the form opened: the list can reorder
		// underneath while the times are typed.
		return m, silenceMonitor(m.current().inst, title, m.silence.monitor, m.silence.covers, start, end)
	}
	return m, cmd
}

func (m Model) updateSilenced(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	in := m.current()
	windows := sortedMaintenances(in.st.Maintenances)
	var act mtAction
	m.silenced, act = m.silenced.Update(msg, windows)
	switch act {
	case mtBack:
		m.screen = screenInstance
	case mtEnd:
		if w, ok := m.silenced.selected(windows); ok {
			m.ask = confirm{
				question: fmt.Sprintf("Delete the maintenance %q?", w.Title),
				detail:   "every monitor it covers speaks again, and the window is gone for good",
			}
			m.onYes, m.backTo, m.screen = endSilence(in.inst, w), screenSilenced, screenConfirm
		}
	}
	return m, nil
}

func (m Model) updateIncidents(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var act incAction
	var cmd tea.Cmd
	m.incs, act, cmd = m.incs.Update(msg, m.current().st)
	if act == incBack {
		m.screen = screenInstance
	}
	return m, cmd
}

// loadMonitor fetches the whole monitor: Kuma replaces a monitor with what
// an edit sends, so an edit must start from everything it holds.
func loadMonitor(in *core.Instance, id int, mode loadMode, from screen) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		mon, err := in.GetMonitor(ctx, id)
		return monitorLoaded{instance: in.Name(), mon: mon, mode: mode, from: from, err: err}
	}
}

// saveMonitor creates a monitor, or edits the one with id, and then puts
// on and takes off its tags.
func saveMonitor(in *core.Instance, mon kuma.RawMonitor, id int, addTags, removeTags []kuma.Tag) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		name := fieldText(mon["name"])
		action := "saved"
		if id == 0 {
			action = "created"
			newID, err := in.AddMonitor(ctx, mon)
			if err != nil {
				return actionDone{name: in.Name(), action: action, mon: name, err: err}
			}
			id = newID
		} else {
			mon["id"] = float64(id)
			if err := in.EditMonitor(ctx, mon); err != nil {
				return actionDone{name: in.Name(), action: action, mon: name, err: err}
			}
		}
		// The monitor is saved; its tags are separate calls. A failure here
		// says so rather than pretending the save failed. The reason is
		// shortened here, not wrapped: the flash shortens an error to its
		// innermost cause, which would drop that the monitor was saved.
		tagFailed := func(t kuma.Tag, err error) actionDone {
			return actionDone{name: in.Name(), action: action, mon: name, saved: true,
				err: fmt.Errorf("%s, but tag %s: %s", action, t.Name, kuma.Brief(err))}
		}
		for _, t := range addTags {
			if err := in.AddMonitorTag(ctx, t.ID, id, t.Value); err != nil {
				return tagFailed(t, err)
			}
		}
		for _, t := range removeTags {
			if err := in.DeleteMonitorTag(ctx, t.ID, id, t.Value); err != nil {
				return tagFailed(t, err)
			}
		}
		return actionDone{name: in.Name(), action: action, mon: name}
	}
}

func deleteMonitor(in *core.Instance, mon state.Monitor) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		return actionDone{name: in.Name(), action: "deleted", mon: mon.Name, err: in.DeleteMonitor(ctx, mon.ID)}
	}
}

func saveChannel(in *core.Instance, cfg map[string]any, id int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		name := fieldText(cfg["name"])
		action := "saved"
		if id == 0 {
			action = "created"
		}
		_, err := in.SaveNotification(ctx, cfg, id)
		return actionDone{name: in.Name(), action: action, mon: "channel " + name, err: err}
	}
}

func deleteChannel(in *core.Instance, c kuma.Notification) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		return actionDone{name: in.Name(), action: "deleted", mon: "channel " + c.Name, err: in.DeleteNotification(ctx, c.ID)}
	}
}

func testChannel(in *core.Instance, c kuma.Notification) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		cfg := map[string]any{}
		for k, v := range c.Config {
			cfg[k] = v
		}
		err := in.TestNotification(ctx, cfg)
		return actionDone{name: in.Name(), action: "tested", mon: "channel " + c.Name, err: err}
	}
}

func silenceMonitor(in *core.Instance, title string, mon state.Monitor, ids []int, start, end time.Time) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		_, err := in.Silence(ctx, title, ids, start, end)
		return actionDone{name: in.Name(), action: "silenced", mon: mon.Name, err: err}
	}
}

func endSilence(in *core.Instance, w kuma.Maintenance) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		return actionDone{name: in.Name(), action: "ended", mon: w.Title, err: in.DeleteMaintenance(ctx, w.ID)}
	}
}

func (m Model) updateDetail(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	in := m.current()
	mon, ok := in.st.Monitors[m.detail.id]
	if !ok {
		m.screen = screenInstance
		return m, nil
	}
	var act detailAction
	var mact instAction
	m.detail, act, mact = m.detail.Update(msg, in.st)
	switch act {
	case detBack:
		m.screen = screenInstance
	case detPeriod:
		return m, loadChart(in.inst, mon.ID, chartPeriods[m.detail.period].hours)
	case detMore:
		return m, loadEvents(in.inst, mon.ID, len(m.detail.events))
	case detMonitor:
		// The detail's monitor as the list would give it: a monitor, never
		// a group, so the actions take the monitor path.
		return m.actOn(mact, state.Row{Monitor: mon, Rollup: mon.Status()}, screenDetail)
	case detClearEvents:
		m.ask = confirm{
			question: fmt.Sprintf("Clear the events of %s?", mon.Name),
			detail:   "Kuma blanks the messages of its past state changes; its uptime stays",
		}
		m.onYes, m.backTo, m.screen = clearEvents(in.inst, mon), screenDetail, screenConfirm
	case detClearHistory:
		m.ask = confirm{
			question: fmt.Sprintf("Clear the history of %s?", mon.Name),
			detail:   "Kuma deletes its uptime statistics and starts it again; the chart and uptime begin from now",
		}
		m.onYes, m.backTo, m.screen = clearHistory(in.inst, mon), screenDetail, screenConfirm
	}
	return m, nil
}
