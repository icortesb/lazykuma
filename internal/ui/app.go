// Package ui is the lazykuma terminal interface, built on Bubble Tea.
package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/icortesb/lazykuma/internal/core"
	"github.com/icortesb/lazykuma/internal/kuma"
	"github.com/icortesb/lazykuma/internal/state"
)

// Deps is what the UI needs from main.
type Deps struct {
	// Core owns the instances, their state and the actions on them.
	Core *core.Core
	// Login returns a token for an instance; kuma.Login.
	Login func(ctx context.Context, url, username, password, code string) (string, error)

	Version string
}

// InstanceState is one instance's state, sent in by main with
// tea.Program.Send as the core publishes it.
type InstanceState struct {
	Name  string
	State state.Instance
}

type screen int

const (
	screenMenu screen = iota
	screenInstance
	screenLogin
	screenAdd
	screenHelp
	screenPick         // a type or a service, before its form
	screenMonitor      // the curated monitor form
	screenRaw          // the raw field editor
	screenChannels     // the instance's notification channels
	screenChannel      // one channel's form
	screenSilence      // silence a monitor
	screenSilenced     // what is silenced
	screenIncidents    // state changes
	screenConfirm      // before something irreversible
	screenName         // a group's name
	screenTags         // an instance's tags
	screenTag          // one tag's form
	screenDetail       // one monitor at full size
	screenPages        // an instance's status pages
	screenPageNew      // a new status page's title and slug
	screenPageDelete   // a page's slug, before deleting it
	screenPageSettings // a page's settings
	screenPageSections // a page's sections and their monitors
)

// instance is an instance as the screens see it: the core's handle for
// actions, and the last state it published.
type instance struct {
	inst *core.Instance
	st   state.Instance
}

func (i instance) name() string { return i.inst.Name() }
func (i instance) url() string  { return i.inst.URL() }

// Model is the whole app.
type Model struct {
	deps   Deps
	insts  []instance
	screen screen
	back   screen // where esc from help returns
	cur    int    // the instance open in the instance or login screen

	width, height int

	menu  menuModel
	inst  instanceScreen
	login loginForm
	add   addForm

	pick     picker
	mform    monitorForm
	raw      rawEditor
	chans    channelsScreen
	cform    channelForm
	silence  silenceForm
	silenced maintenanceScreen
	incs     incidentsScreen
	nform    nameForm
	tags     tagsScreen
	tform    tagForm
	detail   detailScreen
	pages    pagesScreen
	pnew     newPageForm
	pdel     slugConfirm
	pset     pageSettings
	secs     sectionsEditor
	moving   state.Monitor // what the move or delete-group picker acts on, fixed when it opened

	tagDefs map[string][]kuma.TagDef // each instance's tags, as last fetched

	// ask is the pending confirmation and what to run when it is accepted.
	ask     confirm
	onYes   tea.Cmd
	backTo  screen // where the current form returns to
	picking string // "monitor", "rawtype", "channel", "move", "delgroup" or "pagemon", for what the picker chose

	flash string
}

const noInstances = "no instances yet: add one"

// New opens on the menu, showing the instances the core holds.
func New(d Deps) Model {
	m := Model{deps: d, inst: newInstanceScreen(), width: 80, height: 24, tagDefs: map[string][]kuma.TagDef{}}
	for _, in := range d.Core.Instances() {
		m.insts = append(m.insts, instance{inst: in, st: in.State()})
	}
	warnings := d.Core.Warnings()
	switch {
	case len(warnings) == 1:
		m.flash = warnings[0]
	case len(warnings) > 1:
		m.flash = fmt.Sprintf("%s (and %d more)", warnings[0], len(warnings)-1)
	case len(m.insts) == 0:
		m.flash = noInstances
	}
	return m
}

func (m Model) Init() tea.Cmd { return nil }

// Messages of the app's own commands.
type (
	actionDone struct {
		name   string
		action string // "paused" or "resumed"
		mon    string
		err    error
		// saved is set when the write landed but a follow-up did not, as a
		// monitor whose tags failed: the form must close all the same, or a
		// retry would create the monitor a second time.
		saved bool
		// monitorID is the monitor a clear was for, so the detail refetches
		// only its own history.
		monitorID int
	}
	loginDone struct {
		name  string
		token string
		err   error
	}
	// monitorLoaded carries the whole monitor Kuma returned, for the form
	// or the field editor.
	monitorLoaded struct {
		instance string // which instance asked: the user can move on
		id       int    // which monitor was asked for
		mon      kuma.RawMonitor
		mode     loadMode
		from     screen // where the fetch was asked from, and where its form returns
		err      error
	}
	flashMsg      struct{ text string }
	clearFlashMsg struct{ text string }
)

// loadMode is what a fetched monitor is for.
type loadMode int

const (
	loadEdit loadMode = iota
	loadRaw
	loadClone
)

// flashFor shows text for a while; a newer flash is not cut short by the
// timer of an older one.
func flashFor(text string, d time.Duration) tea.Cmd {
	return tea.Batch(
		func() tea.Msg { return flashMsg{text} },
		tea.Tick(d, func(time.Time) tea.Msg { return clearFlashMsg{text} }),
	)
}

func (m Model) find(name string) int {
	for i, in := range m.insts {
		if in.name() == name {
			return i
		}
	}
	return -1
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case InstanceState:
		i := m.find(msg.Name)
		if i < 0 {
			return m, nil
		}
		prev := m.insts[i].st
		if i == m.cur {
			// Pin the cursor to the monitor drawn under it before the new
			// state reorders the list.
			m.inst = m.inst.pin(prev)
		}
		m.insts[i].st = msg.State
		// The detail's monitor was deleted, here or in the web UI: there is
		// nothing left to show.
		if i == m.cur && m.screen == screenDetail {
			if _, ok := msg.State.Monitors[m.detail.id]; !ok {
				m.screen = screenInstance
			}
		}
		// Kuma only gives the tags when asked, and a session that was not
		// up yet when the instance opened could not answer: ask again once
		// it is, or a clone made now would find no tags to copy.
		if i == m.cur && m.screen != screenMenu && msg.State.Conn == state.ConnOK && prev.Conn != state.ConnOK {
			tags := loadTags(m.insts[i].inst)
			// A detail open through the outage could fetch nothing while it
			// lasted, and missed the state changes of its duration: fetch its
			// chart and its events again, for the period on screen.
			if _, ok := msg.State.Monitors[m.detail.id]; ok && m.onDetail() {
				var fetch tea.Cmd
				m.detail, fetch = m.refreshDetail()
				return m, tea.Batch(tags, fetch)
			}
			return m, tags
		}
		return m, nil

	case actionDone:
		if msg.err != nil {
			if msg.saved && (m.screen == screenMonitor || m.screen == screenRaw) {
				m.screen = m.backTo
			}
			return m, flashFor(fmt.Sprintf("%s: %v", msg.mon, kuma.Brief(msg.err)), 8*time.Second)
		}
		// A write lands: leave the form and let the instance's next state
		// show the result. The pickers that write (move, delete group) close
		// as they send, like the confirmation: a picker open now was opened
		// since, for something else.
		switch m.screen {
		case screenMonitor, screenRaw, screenChannel, screenSilence, screenConfirm, screenName, screenTag,
			screenPageNew, screenPageDelete, screenPageSettings:
			m.screen = m.backTo
		case screenPageSections:
			// Its backTo is where its own forms return, the editor itself.
			m.screen = screenPages
		}
		done := flashFor(msg.action+" "+msg.mon, 3*time.Second)
		if m.screen == screenDetail {
			if _, ok := m.current().st.Monitors[m.detail.id]; !ok {
				m.screen = screenInstance
			}
		}
		if strings.HasPrefix(msg.action, "cleared") && msg.name == m.current().name() && msg.monitorID == m.detail.id && m.onDetail() {
			// What was cleared is fetched again, for the period on screen,
			// while the user is still on that detail: a form opened from it
			// meanwhile returns to it. One left behind is built afresh when
			// opened again.
			var fetch tea.Cmd
			m.detail, fetch = m.refreshDetail()
			return m, tea.Batch(done, fetch)
		}
		if m.screen == screenTags || strings.HasPrefix(msg.mon, "tag ") {
			return m, tea.Batch(done, loadTags(m.current().inst))
		}
		return m, done

	case tagsLoaded:
		if errors.Is(msg.err, kuma.ErrNotConnected) {
			// Asked before the session was up; they are asked for again
			// when it is.
			return m, nil
		}
		if msg.err != nil {
			return m, flashFor("tags: "+kuma.Brief(msg.err), 8*time.Second)
		}
		m.tagDefs[msg.instance] = msg.tags
		return m, nil

	case monitorLoaded:
		if msg.err != nil {
			return m, flashFor(kuma.Brief(msg.err), 8*time.Second)
		}
		if m.screen != msg.from || m.current().name() != msg.instance || (msg.from == screenDetail && m.detail.id != msg.id) {
			// The user moved on while Kuma was answering; an edit opened now
			// would save one instance's monitor into another, or open over
			// the detail of another monitor.
			return m, nil
		}
		if msg.mode == loadRaw {
			m.raw = newRawEditor("monitor", 0, msg.mon)
			m.backTo, m.screen = msg.from, screenRaw
			return m, nil
		}
		kind, _ := msg.mon["type"].(string)
		if !isCuratedKind(kind) {
			// No form knows this type's fields; its own values do. A clone
			// is a new monitor there too, so it is saved with no id.
			mon := msg.mon
			if msg.mode == loadClone {
				mon = kuma.ForClone(msg.mon)
			}
			m.raw = newRawEditor("monitor", 0, mon)
			if msg.mode == loadClone {
				m.raw.cloneTags = rawTags(msg.mon["tags"])
			}
			m.backTo, m.screen = msg.from, screenRaw
			return m, nil
		}
		lists := m.formLists(0, true)
		if msg.mode == loadClone {
			m.mform = cloneMonitorForm(msg.mon, lists)
		} else {
			m.mform = editMonitorForm(msg.mon, lists)
		}
		m.backTo, m.screen = msg.from, screenMonitor
		return m, nil

	case pageLoaded:
		if m.screen != screenPages || m.current().name() != msg.instance {
			// The user moved on while Kuma was answering; a form opened now
			// would save one instance's page into another.
			return m, nil
		}
		if msg.err != nil {
			return m, flashFor("status page "+msg.slug+": "+kuma.Brief(msg.err), 8*time.Second)
		}
		m.pset = newPageSettings(msg.page)
		m.backTo, m.screen = screenPages, screenPageSettings
		return m, nil

	case sectionsLoaded:
		if m.screen != screenPages || m.current().name() != msg.instance {
			// The user moved on while Kuma was answering; an editor opened
			// now would save one instance's sections into another's page.
			return m, nil
		}
		if msg.err != nil {
			return m, flashFor("sections of "+msg.title+": "+kuma.Brief(msg.err), 8*time.Second)
		}
		m.secs, m.screen = sectionsEditor{slug: msg.slug, title: msg.title, sections: msg.sections}, screenPageSections
		return m, nil

	case sectionsAnswered:
		if m.screen != screenPageSections {
			return m, nil
		}
		if msg.discard {
			m.screen = screenPages
			return m, nil
		}
		m.secs = m.secs.removeRow()
		return m, nil

	// The detail takes its answers whatever is on screen: a question or a
	// form opened from it before they landed returns to it, and it must not
	// be left loading. It drops those for another monitor, period or page.
	case chartLoaded:
		if m.current().name() == msg.instance {
			m.detail = m.detail.withChart(msg)
		}
		return m, nil
	case eventsLoaded:
		if m.current().name() == msg.instance {
			m.detail = m.detail.withEvents(msg)
		}
		return m, nil

	case loginDone:
		return m.loginFinished(msg)

	case flashMsg:
		m.flash = msg.text
		return m, nil
	case clearFlashMsg:
		if m.flash == msg.text {
			m.flash = ""
		}
		return m, nil

	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
			return m, tea.Quit
		}
		switch m.screen {
		case screenMenu:
			return m.updateMenu(msg)
		case screenInstance:
			return m.updateInstance(msg)
		case screenPick:
			return m.updatePick(msg)
		case screenChannels:
			return m.updateChannels(msg)
		case screenSilenced:
			return m.updateSilenced(msg)
		case screenIncidents:
			return m.updateIncidents(msg)
		case screenTags:
			return m.updateTags(msg)
		case screenDetail:
			return m.updateDetail(msg)
		case screenPages:
			return m.updatePages(msg)
		case screenPageSections:
			return m.updateSections(msg)
		case screenConfirm:
			answered, yes := m.ask.Update(msg)
			if !answered {
				return m, nil
			}
			cmd := m.onYes
			m.screen, m.onYes = m.backTo, nil
			if !yes {
				return m, nil
			}
			return m, cmd
		case screenHelp:
			if key.Matches(msg, keys.Back, keys.Help, keys.Quit) {
				m.screen = m.back
			}
			return m, nil
		}
	}

	// Everything else (keys, the cursor blink) belongs to the open form.
	switch m.screen {
	case screenLogin:
		return m.updateLogin(msg)
	case screenAdd:
		return m.updateAdd(msg)
	case screenMonitor:
		return m.updateMonitorForm(msg)
	case screenRaw:
		return m.updateRaw(msg)
	case screenChannel:
		return m.updateChannelForm(msg)
	case screenSilence:
		return m.updateSilence(msg)
	case screenName:
		return m.updateName(msg)
	case screenTag:
		return m.updateTagForm(msg)
	case screenPageNew:
		return m.updatePageForm(msg)
	case screenPageSettings:
		return m.updatePageSettings(msg)
	case screenPageDelete:
		return m.updateSlugConfirm(msg)
	}
	return m, nil
}

func (m Model) menuItems() []menuItem {
	items := make([]menuItem, 0, len(m.insts)+3)
	for i, in := range m.insts {
		items = append(items, menuItem{label: in.name(), desc: instanceDesc(in.url(), in.st), inst: i, target: screenInstance})
	}
	return append(items,
		menuItem{label: "Add instance", desc: "Watch another Uptime Kuma", inst: -1, target: screenAdd},
		menuItem{label: "Help", desc: "Keys, and what lazykuma stores", inst: -1, target: screenHelp},
		menuItem{label: "Quit", inst: -1, target: screenMenu},
	)
}

func (m Model) updateMenu(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Quit):
		return m, tea.Quit
	case key.Matches(msg, keys.Help):
		m.back, m.screen = screenMenu, screenHelp
		return m, nil
	}
	var chosen *menuItem
	m.menu, chosen = m.menu.Update(msg, m.menuItems())
	if chosen == nil {
		return m, nil
	}
	switch {
	case chosen.label == "Quit" && chosen.inst < 0:
		return m, tea.Quit
	case chosen.inst >= 0:
		return m.openInstance(chosen.inst)
	case chosen.target == screenAdd:
		m.add, m.screen = newAddForm(), screenAdd
		return m, nil
	case chosen.target == screenHelp:
		m.back, m.screen = screenMenu, screenHelp
	}
	return m, nil
}

// openInstance shows an instance, or its login when there is no usable
// token.
func (m Model) openInstance(i int) (tea.Model, tea.Cmd) {
	m.cur = i
	switch m.insts[i].st.Conn {
	case state.ConnNoCred, state.ConnBadCred:
		m.login, m.screen = newLoginForm(), screenLogin
		return m, nil
	}
	m.inst, m.screen = newInstanceScreen(), screenInstance
	return m, loadTags(m.insts[i].inst)
}

func (m Model) updateInstance(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	in := m.insts[m.cur]
	if !m.inst.filtering && key.Matches(msg, keys.Help) {
		m.back, m.screen = screenInstance, screenHelp
		return m, nil
	}
	var act instAction
	var cmd tea.Cmd
	m.inst, act, cmd = m.inst.Update(msg, in.st)
	row, hasRow := m.inst.selectedRow(in.st)
	switch act {
	case instBack:
		m.screen = screenMenu
	case instToggle, instEdit, instRaw, instClone, instDelete, instSilence, instMove:
		if hasRow {
			return m.actOn(act, row, screenInstance)
		}
	case instNew:
		options := make([]option, 0, len(curatedKinds)+1)
		for _, k := range curatedKinds {
			options = append(options, option{kindLabel(k), k})
		}
		options = append(options, option{"Other type…", "other"})
		m.pick = newPicker("New monitor", "what should it watch?", options)
		m.picking, m.backTo, m.screen = "monitor", screenInstance, screenPick
	case instNewGroup:
		m.nform = newNameForm("New group", "monitors move into it with v", "", 0)
		m.backTo, m.screen = screenInstance, screenName
	case instSilenced:
		m.silenced, m.screen = maintenanceScreen{}, screenSilenced
	case instChannels:
		m.chans, m.screen = channelsScreen{}, screenChannels
	case instIncidents:
		m.incs, m.screen = newIncidentsScreen(), screenIncidents
	case instTags:
		m.tags, m.screen = tagsScreen{}, screenTags
		cmd = loadTags(in.inst)
	case instPages:
		m.pages, m.screen = pagesScreen{}, screenPages
	case instDetail:
		if hasRow {
			m.detail, m.screen = newDetailScreen(row.ID, in.st), screenDetail
			hours := chartPeriods[m.detail.period].hours
			return m, tea.Batch(loadChart(in.inst, row.ID, hours), loadEvents(in.inst, row.ID, 0))
		}
	}
	return m, cmd
}

// togglePause pauses a running monitor or resumes a paused one.
func togglePause(in *core.Instance, mon state.Monitor) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if mon.Active {
			return actionDone{name: in.Name(), action: "paused", mon: mon.Name, err: in.Pause(ctx, mon.ID)}
		}
		return actionDone{name: in.Name(), action: "resumed", mon: mon.Name, err: in.Resume(ctx, mon.ID)}
	}
}

func (m Model) updateLogin(msg tea.Msg) (tea.Model, tea.Cmd) {
	var act formAction
	var cmd tea.Cmd
	m.login, act, cmd = m.login.Update(msg)
	switch act {
	case formCancel:
		m.screen = screenMenu
	case formSubmit:
		in := m.insts[m.cur]
		user, pass, code := m.login.Values()
		login := m.deps.Login
		name, url := in.name(), in.url()
		cmd = func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			tok, err := login(ctx, url, user, pass, code)
			return loginDone{name: name, token: tok, err: err}
		}
	}
	return m, cmd
}

func (m Model) loginFinished(msg loginDone) (tea.Model, tea.Cmd) {
	i := m.find(msg.name)
	if i < 0 {
		return m, nil
	}
	if msg.err != nil {
		var cmd tea.Cmd
		m.login, cmd = m.login.WithResult(msg.err)
		return m, cmd
	}
	if err := m.deps.Core.SetToken(msg.name, m.insts[i].url(), msg.token); err != nil {
		m.login, _ = m.login.WithResult(fmt.Errorf("logged in, but the token could not be saved: %w", err))
		return m, nil
	}
	m.inst, m.screen = newInstanceScreen(), screenInstance
	return m, tea.Batch(flashFor("logged in to "+msg.name, 2*time.Second), loadTags(m.insts[i].inst))
}

func (m Model) updateAdd(msg tea.Msg) (tea.Model, tea.Cmd) {
	var act formAction
	var cmd tea.Cmd
	m.add, act, cmd = m.add.Update(msg)
	switch act {
	case formCancel:
		m.screen = screenMenu
	case formSubmit:
		cfg := m.add.Values()
		added, err := m.deps.Core.Add(cfg)
		if err != nil {
			m.add = m.add.WithError(err)
			return m, nil
		}
		m.insts = append(m.insts, instance{inst: added, st: added.State()})
		if m.flash == noInstances {
			m.flash = ""
		}
		m.cur = len(m.insts) - 1
		m.login, m.screen = newLoginForm(), screenLogin
	}
	return m, cmd
}

// View draws the current screen, then makes sure no line runs past the
// terminal: a real Kuma error can be ~180 characters, and a menu that only
// centres it lets the terminal cut off exactly the useful end.
func (m Model) View() string {
	return truncateLines(m.render(), m.width)
}

func truncateLines(s string, width int) string {
	if width <= 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = ansi.Truncate(line, width, "…")
	}
	return strings.Join(lines, "\n")
}

func (m Model) render() string {
	switch m.screen {
	case screenInstance:
		in := m.insts[m.cur]
		return m.chrome(m.inst.View(in.name(), in.st, m.width, m.height-2), keyHints)
	case screenLogin:
		in := m.insts[m.cur]
		return m.chrome(m.login.View(in.name(), in.url()), "")
	case screenAdd:
		return m.chrome(m.add.View(), "")
	case screenHelp:
		return m.chrome(helpText(), "esc back")
	case screenPick:
		return m.chrome(m.pick.View(m.width, m.height-2), "")
	case screenMonitor:
		return m.chrome(m.mform.View(m.width), "")
	case screenRaw:
		return m.chrome(m.raw.View(m.width, m.height-2), "")
	case screenChannels:
		in := m.current()
		return m.chrome(m.chans.View(in.name(), in.st.Channels, m.width, m.height-2), "")
	case screenChannel:
		return m.chrome(m.cform.View(), "")
	case screenSilence:
		return m.chrome(m.silence.View(), "")
	case screenSilenced:
		in := m.current()
		return m.chrome(m.silenced.View(in.name(), sortedMaintenances(in.st.Maintenances), m.width, m.height-2), "")
	case screenIncidents:
		in := m.current()
		return m.chrome(m.incs.View(in.name(), in.st, m.width, m.height-2), "")
	case screenConfirm:
		return m.chrome(m.ask.View(), "")
	case screenName:
		return m.chrome(m.nform.View(), "")
	case screenTags:
		in := m.current()
		return m.chrome(m.tags.View(in.name(), m.tagDefs[in.name()], m.width, m.height-2), "")
	case screenTag:
		return m.chrome(m.tform.View(), "")
	case screenDetail:
		return m.chrome(m.detail.View(m.current().st, m.width, m.height-2), keyHintsDetail)
	case screenPages:
		in := m.current()
		return m.chrome(m.pages.View(in.name(), in.url(), in.st.StatusPages, m.width, m.height-2), keyHintsPages)
	case screenPageNew:
		return m.chrome(m.pnew.View(), "")
	case screenPageDelete:
		return m.chrome(m.pdel.View(), "")
	case screenPageSettings:
		return m.chrome(m.pset.View(), "")
	case screenPageSections:
		return m.chrome(m.secs.View(m.width, m.height-2), keyHintsSections)
	}

	names := make([]string, len(m.insts))
	states := make([]state.Instance, len(m.insts))
	for i, in := range m.insts {
		names[i], states[i] = in.name(), in.st
	}
	return m.menu.View(m.width, m.height, m.menuItems(), semaphore(names, states), m.deps.Version, m.flash)
}

// chrome puts the flash line and the keys under a screen, so every screen
// says how to get out.
func (m Model) chrome(body, hint string) string {
	footer := ""
	if hint != "" {
		footer = styleFooter.Render(hint)
	}
	if m.flash != "" {
		footer = styleOK.Render(m.flash) + styleFooter.Render("   ") + footer
	}
	out := body
	pad := m.height - lipgloss.Height(out) - 1
	if pad > 0 {
		out += strings.Repeat("\n", pad)
	}
	return out + "\n" + footer
}

func helpText() string {
	// The instance and detail keys come from the same lines those screens
	// show, so the two cannot drift apart.
	var b strings.Builder
	b.WriteString(styleHeading.Render("Keys") + "\n\n")
	for _, r := range [][2]string{
		{"↑/k ↓/j", "move"},
		{"enter", "open an instance, or log in to it"},
		{"esc", "back"},
		{"?", "help"},
		{"q", "quit, from the menu"},
		{"ctrl+c", "quit, from anywhere"},
	} {
		b.WriteString(fmt.Sprintf("  %s  %s\n", styleKey.Render(fmt.Sprintf("%-8s", r[0])), styleValue.Render(r[1])))
	}
	for _, sec := range []struct{ heading, hints string }{
		{"On an instance", keyHints},
		{"On a monitor's detail", keyHintsDetail},
		{"On status pages", keyHintsPages},
		{"On a page's sections", keyHintsSections},
	} {
		b.WriteString("\n" + styleHeading.Render(sec.heading) + "\n\n")
		for _, part := range strings.Split(sec.hints, "   ") {
			if part = strings.TrimSpace(part); part != "" {
				b.WriteString("  " + styleValue.Render(part) + "\n")
			}
		}
	}
	b.WriteString("\n" + styleHeading.Render("In the field editor") + "\n\n")
	b.WriteString("  " + styleValue.Render("enter edit   a add field   d delete   ctrl+s save") + "\n")
	b.WriteString("\n" + styleHeading.Render("What lazykuma stores") + "\n\n")
	// Each line is styled on its own: a newline inside a styled block makes
	// lipgloss pad the lines to one width and push the next ones sideways.
	for _, line := range []string{
		"The instances, in ~/.config/lazykuma/config.toml: safe to keep in dotfiles.",
		"The login tokens, in ~/.local/state/lazykuma/tokens.json, readable only by you.",
		"Passwords, 2FA codes and channel secrets go to Kuma and are never written here.",
	} {
		b.WriteString("  " + styleValue.Render(line) + "\n")
	}
	return b.String()
}
