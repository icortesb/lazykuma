package ui

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/icortesb/lazykuma/internal/core"
	"github.com/icortesb/lazykuma/internal/kuma"
	"github.com/icortesb/lazykuma/internal/state"
)

// actionTimeout bounds a write: Kuma answers a call in milliseconds, but a
// login on a large idle instance can take half a minute.
const actionTimeout = 45 * time.Second

// sentFrom stamps the form a write is sent from on the write's answer, so
// that the answer closes that form and no other: a form opened while the
// write was on its way stays open.
func sentFrom(from screen, cmd tea.Cmd) tea.Cmd {
	return func() tea.Msg {
		msg := cmd()
		if done, ok := msg.(actionDone); ok {
			done.from = from
			return done
		}
		return msg
	}
}

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
		case "pagemon":
			id, _ := strconv.Atoi(value)
			m.screen = m.backTo
			// A monitor deleted while the picker was open would be saved
			// onto the page as an id Kuma no longer has.
			if mon, ok := m.current().st.Monitors[id]; ok {
				m.secs = m.secs.addMonitor(kuma.PageMonitor{ID: id, Name: mon.Name})
			}
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
		switch m.nform.section {
		case "add":
			m.secs, m.screen = m.secs.addSection(name), m.backTo
			return m, nil
		case "rename":
			m.secs, m.screen = m.secs.renameSection(name), m.backTo
			return m, nil
		}
		if m.nform.id == 0 {
			return m, sentFrom(screenName, addGroup(m.current().inst, name))
		}
		return m, sentFrom(screenName, renameGroup(m.current().inst, m.nform.id, name))
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
		return m, sentFrom(screenTag, saveTag(m.current().inst, t))
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
		return m, sentFrom(screenMonitor, saveMonitor(m.current().inst, mon, m.mform.id, add, remove))
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
			return m, sentFrom(screenRaw, saveChannel(m.current().inst, values, m.raw.id))
		}
		var addTags []kuma.Tag
		if m.raw.id == 0 {
			addTags = m.raw.cloneTags
		}
		return m, sentFrom(screenRaw, saveMonitor(m.current().inst, kuma.RawMonitor(values), m.raw.id, addTags, nil))
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
		return m, sentFrom(screenChannel, saveChannel(m.current().inst, cfg, m.cform.id))
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
		return m, sentFrom(screenSilence, silenceMonitor(m.current().inst, title, m.silence.monitor, m.silence.covers, start, end))
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
		return monitorLoaded{instance: in.Name(), id: id, mon: mon, mode: mode, from: from, err: err}
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

// onDetail is whether the user is on the monitor's detail: on it, or on a
// form, picker, question or help opened from it, which return there.
func (m Model) onDetail() bool {
	switch m.screen {
	case screenDetail:
		return true
	case screenHelp:
		return m.back == screenDetail
	case screenPick, screenMonitor, screenRaw, screenChannel, screenSilence, screenConfirm, screenName, screenTag:
		return m.backTo == screenDetail
	}
	return false
}

// refreshDetail builds the detail afresh, on the period it shows, and
// fetches its chart and its first page of events again.
func (m Model) refreshDetail() (detailScreen, tea.Cmd) {
	in, id, period := m.current(), m.detail.id, m.detail.period
	d := newDetailScreen(id, in.st)
	d.period = period
	return d, tea.Batch(loadChart(in.inst, id, chartPeriods[period].hours), loadEvents(in.inst, id, 0))
}

func (m Model) updateDetail(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	in := m.current()
	mon, ok := in.st.Monitors[m.detail.id]
	if !ok {
		m.screen = screenInstance
		return m, nil
	}
	if key.Matches(msg, keys.Help) {
		m.back, m.screen = screenDetail, screenHelp
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
			detail:   "Kuma removes its past state changes from the history; its beats and uptime stay",
		}
		m.onYes, m.backTo, m.screen = clearEvents(in.inst, mon), screenDetail, screenConfirm
	case detClearHistory:
		m.ask = confirm{
			question: fmt.Sprintf("Clear the history of %s?", mon.Name),
			detail:   "Kuma deletes all of its beats, state changes and uptime statistics; the chart starts again from now",
		}
		m.onYes, m.backTo, m.screen = clearHistory(in.inst, mon), screenDetail, screenConfirm
	}
	return m, nil
}

func (m Model) updatePages(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	in := m.current()
	pages := in.st.StatusPages
	var act pageAction
	m.pages, act = m.pages.Update(msg, pages)
	p, ok := m.pages.selected(pages)
	switch act {
	case pageBack:
		m.screen = screenInstance
	case pageEdit:
		if ok {
			return m, loadPage(in.inst, p.Slug)
		}
	case pageSections:
		if ok {
			return m, loadSections(in.inst, p.Slug, p.Title)
		}
	case pageIncident:
		if ok {
			return m, loadIncident(in.inst, p.Slug, p.Title)
		}
	case pageUnpin:
		if ok {
			m.ask = confirm{
				question: fmt.Sprintf("Take down the incident on %q?", p.Title),
				detail:   "the page stops showing it; Kuma keeps it in its history",
			}
			m.onYes, m.backTo, m.screen = unpinIncident(in.inst, p.Slug, p.Title), screenPages, screenConfirm
		}
	case pageNew:
		m.pnew = newNewPageForm()
		m.backTo, m.screen = screenPages, screenPageNew
	case pageOpen:
		if !ok {
			break
		}
		// openURL starts the browser and does not wait for it, so it can
		// run here, in the update.
		u := kuma.PageURL(in.url(), p.Slug)
		if err := openURL(u); err != nil {
			return m, flashFor(fmt.Sprintf("open %s yourself: %v", u, err), 8*time.Second)
		}
		return m, flashFor("opened "+u, 3*time.Second)
	case pageDelete:
		if ok {
			m.pdel = newSlugConfirm(p.Title, p.Slug)
			m.backTo, m.screen = screenPages, screenPageDelete
		}
	}
	return m, nil
}

func (m Model) updatePageForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	var act formAction
	var cmd tea.Cmd
	m.pnew, act, cmd = m.pnew.Update(msg)
	switch act {
	case formCancel:
		m.screen = m.backTo
	case formSubmit:
		title, slug, err := m.pnew.Values()
		if err != nil {
			m.pnew.err = err.Error()
			return m, nil
		}
		m.pnew.err = ""
		return m, sentFrom(screenPageNew, addPage(m.current().inst, title, slug))
	}
	return m, cmd
}

func (m Model) updatePageSettings(msg tea.Msg) (tea.Model, tea.Cmd) {
	var act formAction
	var cmd tea.Cmd
	m.pset, act, cmd = m.pset.Update(msg)
	switch act {
	case formCancel:
		m.screen = m.backTo
	case formSubmit:
		changes, err := m.pset.Changes()
		if err != nil {
			m.pset.err = err.Error()
			return m, nil
		}
		m.pset.err = ""
		return m, sentFrom(screenPageSettings, savePageSettings(m.current().inst, m.pset.page.Slug, m.pset.page.Title, changes))
	}
	return m, cmd
}

func (m Model) updatePageIncident(msg tea.Msg) (tea.Model, tea.Cmd) {
	var act formAction
	var cmd tea.Cmd
	m.pinc, act, cmd = m.pinc.Update(msg)
	switch act {
	case formCancel:
		m.screen = m.backTo
	case formSubmit:
		inc, err := m.pinc.Values()
		if err != nil {
			m.pinc.err = err.Error()
			return m, nil
		}
		m.pinc.err = ""
		return m, sentFrom(screenPageIncident, postIncident(m.current().inst, m.pinc.slug, m.pinc.page, inc))
	}
	return m, cmd
}

func (m Model) updateSlugConfirm(msg tea.Msg) (tea.Model, tea.Cmd) {
	var act formAction
	var cmd tea.Cmd
	m.pdel, act, cmd = m.pdel.Update(msg)
	switch act {
	case formCancel:
		m.screen = m.backTo
	case formSubmit:
		if !m.pdel.Confirmed() {
			m.pdel.err = "type " + m.pdel.word + " to delete it"
			return m, nil
		}
		m.pdel.err = ""
		in := m.current()
		p, ok := in.st.StatusPage(m.pdel.word)
		if !ok {
			p = kuma.StatusPage{Slug: m.pdel.word, Title: m.pdel.title}
		}
		return m, sentFrom(screenPageDelete, deletePage(in.inst, p))
	}
	return m, cmd
}

func (m Model) updateSections(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	e := m.secs
	r, hasRow := e.current()
	switch {
	case key.Matches(msg, keys.Up):
		m.secs = e.up()
	case key.Matches(msg, keys.Down):
		m.secs = e.down()
	case key.Matches(msg, keys.Back):
		if !e.dirty {
			m.screen = screenPages
			return m, nil
		}
		m.ask = confirm{question: "Discard the changes to the sections?", detail: "none of them has been saved to Kuma"}
		m.onYes = func() tea.Msg { return sectionsAnswered{discard: true} }
		m.backTo, m.screen = screenPageSections, screenConfirm
	case msg.Type == tea.KeyCtrlS:
		return m, sentFrom(screenPageSections, saveSections(m.current().inst, e.slug, e.title, e.sections))
	case msg.String() == "a":
		m.nform = newNameForm("New section", "a heading on the page, with monitors under it", "", 0)
		m.nform.section = "add"
		m.backTo, m.screen = screenPageSections, screenName
	case msg.String() == "r" && hasRow && r.mon < 0:
		m.nform = newNameForm("Rename section", "", e.sections[r.sec].Name, 0)
		m.nform.section = "rename"
		m.backTo, m.screen = screenPageSections, screenName
	case msg.String() == "m":
		if !hasRow {
			return m, flashFor("add a section first (a)", 3*time.Second)
		}
		sec := e.sections[r.sec]
		options := monitorOptions(m.current().st, e.sections)
		if len(options) == 0 {
			return m, flashFor("every monitor is on the page already", 3*time.Second)
		}
		m.pick = newPicker(fmt.Sprintf("Add to %q", sec.Name), "a monitor, or a group to show as one", options)
		m.picking, m.backTo, m.screen = "pagemon", screenPageSections, screenPick
	case msg.String() == "d" && hasRow:
		sec := e.sections[r.sec]
		if r.mon >= 0 || len(sec.Monitors) == 0 {
			m.secs = e.removeRow()
			return m, nil
		}
		m.ask = confirm{
			question: fmt.Sprintf("Remove the section %q and its %s from the page?", sec.Name, monitors(len(sec.Monitors))),
			detail:   "the monitors stay in Kuma; nothing changes there until ctrl+s saves",
		}
		m.onYes = func() tea.Msg { return sectionsAnswered{} }
		m.backTo, m.screen = screenPageSections, screenConfirm
	case msg.String() == "K":
		m.secs = e.moveUp()
	case msg.String() == "J":
		m.secs = e.moveDown()
	}
	return m, nil
}

func (m Model) updateServer(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	in := m.current()
	if key.Matches(msg, keys.Help) {
		m.back, m.screen = screenServer, screenHelp
		return m, nil
	}
	var act serverAction
	m.srv, act = m.srv.Update(msg, in.st)
	switch act {
	case srvBack:
		m.screen = screenInstance
	case srvRefresh:
		m.srv.dbKnown, m.srv.dbErr = false, ""
		return m, loadDBSize(in.inst)
	case srvNew:
		switch m.srv.tab {
		case tabKeys:
			m.akform = newAPIKeyForm()
			m.backTo, m.screen = screenServer, screenAPIKey
		case tabProxies:
			m.pform, m.screen = newProxyForm(kuma.Proxy{}), screenProxy
		case tabDocker:
			m.dform, m.screen = newDockerForm(kuma.DockerHost{}), screenDockerHost
		}
	case srvEdit:
		if p, ok := m.srv.selectedProxy(in.st); ok && m.srv.tab == tabProxies {
			m.pform, m.screen = newProxyForm(p), screenProxy
		}
		if d, ok := m.srv.selectedDocker(in.st); ok && m.srv.tab == tabDocker {
			m.dform, m.screen = newDockerForm(d), screenDockerHost
		}
	case srvTest:
		if d, ok := m.srv.selectedDocker(in.st); ok {
			return m.testDockerHost(d, d.ID)
		}
	case srvToggle:
		if k, ok := m.srv.selectedKey(in.st); ok {
			return m, setAPIKeyActive(in.inst, k, !k.Active)
		}
	case srvDelete:
		if k, ok := m.srv.selectedKey(in.st); ok && m.srv.tab == tabKeys {
			m.ask = confirm{
				question: fmt.Sprintf("Delete the API key %q?", k.Name),
				detail:   "anything using it stops working",
			}
			m.onYes, m.backTo, m.screen = deleteAPIKey(in.inst, k), screenServer, screenConfirm
		}
		if p, ok := m.srv.selectedProxy(in.st); ok && m.srv.tab == tabProxies {
			m.ask = confirm{
				question: fmt.Sprintf("Delete the proxy %s?", proxyAddr(p)),
				detail:   "monitors using it go without a proxy",
			}
			m.onYes, m.backTo, m.screen = deleteProxy(in.inst, p), screenServer, screenConfirm
		}
		if d, ok := m.srv.selectedDocker(in.st); ok && m.srv.tab == tabDocker {
			m.ask = confirm{
				question: fmt.Sprintf("Delete the Docker host %q?", d.Name),
				detail:   "docker monitors on it lose their host",
			}
			m.onYes, m.backTo, m.screen = deleteDockerHost(in.inst, d), screenServer, screenConfirm
		}
	case srvShrink:
		if m.srv.dbKnown && m.srv.dbSize == 0 {
			// Kuma reports no size on MariaDB, where its shrink is a no-op:
			// asking would promise something it does not do.
			return m, flashFor("Kuma's database is MariaDB: there is nothing to shrink", 5*time.Second)
		}
		m.ask = confirm{
			question: "Shrink the database?",
			detail:   "Kuma compacts its SQLite file; it can take a while on a large one",
		}
		m.onYes, m.backTo, m.screen = shrinkDatabase(in.inst), screenServer, screenConfirm
	case srvClear:
		m.sconf = newTypedConfirm("name  ", in.name(), "",
			fmt.Sprintf("Clear all statistics of %s?", in.name()),
			fmt.Sprintf("Kuma deletes the uptime history of every monitor. Type %s to confirm.", in.name()))
		m.backTo, m.screen = screenServer, screenServerConfirm
	}
	return m, nil
}

func (m Model) updateAPIKeyForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	var act formAction
	var cmd tea.Cmd
	m.akform, act, cmd = m.akform.Update(msg)
	switch act {
	case formCancel:
		m.screen = m.backTo
	case formSubmit:
		name, expires, err := m.akform.Values()
		if err != nil {
			m.akform.err = err.Error()
			return m, nil
		}
		m.akform.err, m.akform.pending = "", true
		return m, addAPIKey(m.current().inst, name, expires)
	}
	return m, cmd
}

// apiKeyMade shows a new key's secret, on the form that asked for it or on
// the server screen it returns to. The secret is never flashed: a key made
// while another screen is up is only said to exist, by its id, since Kuma
// will not show it again.
func (m Model) apiKeyMade(msg apiKeyMade) (tea.Model, tea.Cmd) {
	if msg.instance != m.current().name() {
		// The user moved to another instance while Kuma made the key: its
		// screen is not the place for the secret, but the key exists and
		// must not go unmentioned.
		if msg.err != nil {
			return m, flashFor(fmt.Sprintf("API key %q on %s: %s", msg.name, msg.instance, kuma.Brief(msg.err)), 8*time.Second)
		}
		return m, flashFor(fmt.Sprintf("API key %q (id %d) on %s was made after its form closed: its secret can't be shown again; delete it (id %d) and make another if you need it",
			msg.name, msg.id, msg.instance, msg.id), 8*time.Second)
	}
	if m.screen == screenAPIKey {
		m.akform.pending = false
		if msg.err != nil {
			m.akform.err = kuma.Brief(msg.err)
			return m, nil
		}
	}
	if msg.err != nil {
		return m, flashFor(fmt.Sprintf("API key %q: %s", msg.name, kuma.Brief(msg.err)), 8*time.Second)
	}
	if m.screen != screenAPIKey && m.screen != screenServer {
		return m, flashFor(fmt.Sprintf("API key %q (id %d) was made after its form closed: its secret can't be shown again; delete it (id %d) and make another if you need it",
			msg.name, msg.id, msg.id), 8*time.Second)
	}
	m.akshown, m.screen = apiKeyShown{name: msg.name, key: msg.key, id: msg.id}, screenAPIKeyShown
	return m, nil
}

func (m Model) updateAPIKeyShown(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, keys.Back) || msg.Type == tea.KeyEnter {
		m.akshown, m.screen = apiKeyShown{}, screenServer
	}
	return m, nil
}

func (m Model) updateServerConfirm(msg tea.Msg) (tea.Model, tea.Cmd) {
	var act formAction
	var cmd tea.Cmd
	m.sconf, act, cmd = m.sconf.Update(msg)
	switch act {
	case formCancel:
		m.screen = m.backTo
	case formSubmit:
		if !m.sconf.Confirmed() {
			m.sconf.err = "type " + m.sconf.word + " to clear them"
			return m, nil
		}
		m.sconf.err = ""
		return m, sentFrom(screenServerConfirm, clearStatistics(m.current().inst))
	}
	return m, cmd
}

// updateProxyForm saves the proxy on enter, once, asking first when it is
// to be set on every monitor; the question's yes marks the form as saving.
// The form returns to the server screen, not to backTo, which the question
// borrows to come back to the form.
func (m Model) updateProxyForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	var act formAction
	var cmd tea.Cmd
	m.pform, act, cmd = m.pform.Update(msg)
	switch act {
	case formCancel:
		m.screen = screenServer
	case formSubmit:
		p, apply, err := m.pform.Values()
		if err != nil {
			m.pform.err = err.Error()
			return m, nil
		}
		m.pform.err = ""
		save := sentFrom(screenProxy, saveProxy(m.current().inst, p, apply))
		if !apply {
			m.pform.pending = true
			return m, save
		}
		m.ask = confirm{
			question: "Set this proxy on every monitor?",
			detail:   "every monitor will check through it",
		}
		m.onYes, m.backTo, m.screen = save, screenProxy, screenConfirm
	}
	return m, cmd
}

// updateDockerForm saves the Docker host on enter, once, and tests the
// form's values on t without saving them.
func (m Model) updateDockerForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	var act formAction
	var cmd tea.Cmd
	m.dform, act, cmd = m.dform.Update(msg)
	switch act {
	case formCancel:
		m.screen = screenServer
	case formSubmit, formTest:
		h, err := m.dform.Values()
		if err != nil {
			m.dform.err = err.Error()
			return m, nil
		}
		m.dform.err = ""
		if act == formTest {
			return m.testDockerHost(h, 0)
		}
		m.dform.pending = true
		return m, sentFrom(screenDockerHost, saveDockerHost(m.current().inst, h))
	}
	return m, cmd
}

// testDockerHost asks Kuma to reach a host, from the list (listID is its
// id) or from the form (0). One test runs at a time: Kuma can take six
// seconds to give up, and a second t meanwhile would only ask again.
func (m Model) testDockerHost(h kuma.DockerHost, listID int) (tea.Model, tea.Cmd) {
	if m.dtest.running() {
		return m, flashFor("still testing Docker "+m.dtest.name, 3*time.Second)
	}
	in := m.current()
	m.dtest = dockerTest{instance: in.name(), name: h.Name, listID: listID}
	if listID == 0 {
		m.dform.testing = true
	}
	return m, testDocker(in.inst, h)
}

// dockerTested shows Kuma's answer to the test on its way, and drops any
// other: only the test it is for may clear what says it is running.
func (m Model) dockerTested(msg dockerTested) (tea.Model, tea.Cmd) {
	if !m.dtest.running() || msg.instance != m.dtest.instance || msg.name != m.dtest.name {
		return m, nil
	}
	if m.dtest.listID == 0 {
		m.dform.testing = false
	}
	m.dtest = dockerTest{}
	return m, msg.flash()
}
