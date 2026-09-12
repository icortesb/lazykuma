// Package ui is the lazykuma terminal interface, built on Bubble Tea.
package ui

import (
	"context"
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
	Now     func() time.Time // time.Now when nil
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
	screenPick      // a type or a service, before its form
	screenMonitor   // the curated monitor form
	screenRaw       // the raw field editor
	screenChannels  // the instance's notification channels
	screenChannel   // one channel's form
	screenSilence   // silence a monitor
	screenSilenced  // what is silenced
	screenIncidents // state changes
	screenConfirm   // before something irreversible
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

	// ask is the pending confirmation and what to run when it is accepted.
	ask     confirm
	onYes   tea.Cmd
	backTo  screen // where the current form returns to
	picking string // "monitor" or "channel", for what the picker chose

	flash string
}

const noInstances = "no instances yet: add one"

// New opens on the menu, showing the instances the core holds.
func New(d Deps) Model {
	if d.Now == nil {
		d.Now = time.Now
	}
	m := Model{deps: d, inst: newInstanceScreen(), width: 80, height: 24}
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
	}
	loginDone struct {
		name  string
		token string
		err   error
	}
	// monitorLoaded carries the whole monitor Kuma returned, for the form
	// or the field editor.
	monitorLoaded struct {
		mon   kuma.RawMonitor
		toRaw bool
		err   error
	}
	flashMsg      struct{ text string }
	clearFlashMsg struct{ text string }
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
		if i := m.find(msg.Name); i >= 0 {
			m.insts[i].st = msg.State
		}
		return m, nil

	case actionDone:
		if msg.err != nil {
			return m, flashFor(fmt.Sprintf("%s: %v", msg.mon, kuma.Brief(msg.err)), 8*time.Second)
		}
		// A write lands: leave the form and let the instance's next state
		// show the result.
		switch m.screen {
		case screenMonitor, screenRaw, screenChannel, screenSilence, screenConfirm:
			m.screen = m.backTo
		}
		return m, flashFor(msg.action+" "+msg.mon, 3*time.Second)

	case monitorLoaded:
		if msg.err != nil {
			return m, flashFor(kuma.Brief(msg.err), 8*time.Second)
		}
		if msg.toRaw {
			m.raw = newRawEditor("monitor", msg.mon)
			m.backTo, m.screen = screenInstance, screenRaw
			return m, nil
		}
		kind, _ := msg.mon["type"].(string)
		if !isCuratedKind(kind) {
			// No form knows this type's fields; its own values do.
			m.raw = newRawEditor("monitor", msg.mon)
			m.backTo, m.screen = screenInstance, screenRaw
			return m, nil
		}
		m.mform = editMonitorForm(msg.mon, m.channelToggles())
		m.backTo, m.screen = screenInstance, screenMonitor
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
	return m, nil
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
	switch act {
	case instBack:
		m.screen = screenMenu
	case instToggle:
		if mon, ok := m.inst.selected(in.st); ok {
			cmd = toggle(in.inst, mon)
		}
	case instNew:
		options := make([]option, 0, len(curatedKinds)+1)
		for _, k := range curatedKinds {
			options = append(options, option{kindLabel(k), k})
		}
		options = append(options, option{"Other type…", "other"})
		m.pick = newPicker("New monitor", "what should it watch?", options)
		m.picking, m.backTo, m.screen = "monitor", screenInstance, screenPick
	case instEdit, instRaw:
		if mon, ok := m.inst.selected(in.st); ok {
			cmd = loadMonitor(in.inst, mon.ID, act == instRaw)
		}
	case instDelete:
		if mon, ok := m.inst.selected(in.st); ok {
			m.ask = confirm{
				question: fmt.Sprintf("Delete %q?", mon.Name),
				detail:   "Kuma removes the monitor and all of its history",
			}
			m.onYes, m.backTo, m.screen = deleteMonitor(in.inst, mon), screenInstance, screenConfirm
		}
	case instSilence:
		if mon, ok := m.inst.selected(in.st); ok {
			m.silence = newSilenceForm(mon.Name)
			m.backTo, m.screen = screenInstance, screenSilence
		}
	case instSilenced:
		m.silenced, m.screen = maintenanceScreen{}, screenSilenced
	case instChannels:
		m.chans, m.screen = channelsScreen{}, screenChannels
	case instIncidents:
		m.incs, m.screen = newIncidentsScreen(), screenIncidents
	}
	return m, cmd
}

// toggle pauses a running monitor or resumes a paused one.
func toggle(in *core.Instance, mon state.Monitor) tea.Cmd {
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
	return m, flashFor("logged in to "+msg.name, 2*time.Second)
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
		added, err := m.deps.Core.Add(context.Background(), cfg)
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
	rows := [][2]string{
		{"↑/k ↓/j", "move"},
		{"enter", "open the instance, or log in to it"},
		{"p", "pause or resume the selected monitor"},
		{"/", "filter monitors by name or target; esc clears it"},
		{"esc", "back"},
		{"q", "quit, from the menu"},
		{"ctrl+c", "quit, from anywhere"},
	}
	var b strings.Builder
	b.WriteString(styleHeading.Render("Keys") + "\n\n")
	for _, r := range rows {
		b.WriteString(fmt.Sprintf("  %s  %s\n", styleKey.Render(fmt.Sprintf("%-8s", r[0])), styleValue.Render(r[1])))
	}
	b.WriteString("\n" + styleHeading.Render("What lazykuma stores") + "\n\n")
	b.WriteString(styleValue.Render("  The instances, in ~/.config/lazykuma/config.toml: safe to keep in dotfiles.\n"))
	b.WriteString(styleValue.Render("  The login tokens, in ~/.local/state/lazykuma/tokens.json, readable only by you.\n"))
	b.WriteString(styleValue.Render("  Passwords and 2FA codes are sent to Kuma once and never written anywhere.\n"))
	return b.String()
}
