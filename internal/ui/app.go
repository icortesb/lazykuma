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

	"github.com/icortesb/lazykuma/internal/config"
	"github.com/icortesb/lazykuma/internal/kuma"
	"github.com/icortesb/lazykuma/internal/state"
)

// Controller is what the UI asks of an instance's connection;
// *kuma.Supervisor is one.
type Controller interface {
	Pause(ctx context.Context, id int) error
	Resume(ctx context.Context, id int) error
	Retry()
}

// Deps is what the UI needs from main.
type Deps struct {
	Config     config.Config
	ConfigPath string
	Tokens     *config.Tokens
	Warnings   []string // from config.Load, shown on the menu at start

	// Start begins watching an instance and returns its controller. The
	// events it produces come back as InstanceEvent.
	Start func(config.Instance) Controller
	// Login returns a token for an instance; kuma.Login.
	Login func(ctx context.Context, url, username, password, code string) (string, error)

	Version string
	Now     func() time.Time // time.Now when nil
}

// InstanceEvent is an event of one instance, sent in by main with
// tea.Program.Send.
type InstanceEvent struct {
	Name  string
	Event kuma.Event
}

type screen int

const (
	screenMenu screen = iota
	screenInstance
	screenLogin
	screenAdd
	screenHelp
)

type instance struct {
	cfg  config.Instance
	st   state.Instance
	ctrl Controller
}

// Model is the whole app.
type Model struct {
	deps   Deps
	cfg    config.Config
	insts  []instance
	screen screen
	back   screen // where esc from help returns
	cur    int    // the instance open in the instance or login screen

	width, height int

	menu  menuModel
	inst  instanceScreen
	login loginForm
	add   addForm

	flash string
}

const noInstances = "no instances yet: add one"

// New starts every configured instance and opens on the menu.
func New(d Deps) Model {
	if d.Now == nil {
		d.Now = time.Now
	}
	m := Model{deps: d, cfg: d.Config, inst: newInstanceScreen(), width: 80, height: 24}
	for _, in := range d.Config.Instances {
		m.insts = append(m.insts, instance{cfg: in, ctrl: d.Start(in)})
	}
	switch {
	case len(d.Warnings) == 1:
		m.flash = d.Warnings[0]
	case len(d.Warnings) > 1:
		m.flash = fmt.Sprintf("%s (and %d more)", d.Warnings[0], len(d.Warnings)-1)
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
		if in.cfg.Name == name {
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

	case InstanceEvent:
		if i := m.find(msg.Name); i >= 0 {
			m.insts[i].st = state.Apply(m.insts[i].st, msg.Event, m.deps.Now())
		}
		return m, nil

	case actionDone:
		if msg.err != nil {
			return m, flashFor(fmt.Sprintf("%s: %v", msg.mon, msg.err), 5*time.Second)
		}
		return m, flashFor(msg.action+" "+msg.mon, 2*time.Second)

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
	}
	return m, nil
}

func (m Model) menuItems() []menuItem {
	items := make([]menuItem, 0, len(m.insts)+3)
	for i, in := range m.insts {
		items = append(items, menuItem{label: in.cfg.Name, desc: instanceDesc(in.cfg.URL, in.st), inst: i, target: screenInstance})
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
		mon, ok := m.inst.selected(in.st)
		if ok {
			cmd = toggle(in.cfg.Name, in.ctrl, mon)
		}
	}
	return m, cmd
}

// toggle pauses a running monitor or resumes a paused one.
func toggle(name string, ctrl Controller, mon state.Monitor) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if mon.Active {
			return actionDone{name: name, action: "paused", mon: mon.Name, err: ctrl.Pause(ctx, mon.ID)}
		}
		return actionDone{name: name, action: "resumed", mon: mon.Name, err: ctrl.Resume(ctx, mon.ID)}
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
		cmd = func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			tok, err := login(ctx, in.cfg.URL, user, pass, code)
			return loginDone{name: in.cfg.Name, token: tok, err: err}
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
	if err := m.deps.Tokens.Set(msg.name, msg.token); err != nil {
		m.login, _ = m.login.WithResult(fmt.Errorf("logged in, but the token could not be saved: %w", err))
		return m, nil
	}
	// The supervisor is waiting for a token; wake it. Until it connects the
	// instance says so.
	m.insts[i].st = state.Apply(m.insts[i].st, kuma.Connecting{}, m.deps.Now())
	m.insts[i].ctrl.Retry()
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
		in := m.add.Values()
		next := m.cfg
		next.Instances = append([]config.Instance(nil), m.cfg.Instances...)
		if err := next.Add(in); err != nil {
			m.add = m.add.WithError(err)
			return m, nil
		}
		if err := config.Save(m.deps.ConfigPath, next); err != nil {
			m.add = m.add.WithError(fmt.Errorf("could not save %s: %w", m.deps.ConfigPath, err))
			return m, nil
		}
		m.cfg = next
		m.insts = append(m.insts, instance{cfg: in, ctrl: m.deps.Start(in)})
		if m.flash == noInstances {
			m.flash = ""
		}
		m.cur = len(m.insts) - 1
		m.login, m.screen = newLoginForm(), screenLogin
	}
	return m, cmd
}

func (m Model) View() string {
	switch m.screen {
	case screenInstance:
		in := m.insts[m.cur]
		return m.chrome(m.inst.View(in.cfg.Name, in.st, m.width, m.height-2),
			"j/k move   p pause/resume   / filter   ? help   esc menu")
	case screenLogin:
		in := m.insts[m.cur]
		return m.chrome(m.login.View(in.cfg.Name, in.cfg.URL), "")
	case screenAdd:
		return m.chrome(m.add.View(), "")
	case screenHelp:
		return m.chrome(helpText(), "esc back")
	}

	names := make([]string, len(m.insts))
	states := make([]state.Instance, len(m.insts))
	for i, in := range m.insts {
		names[i], states[i] = in.cfg.Name, in.st
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
