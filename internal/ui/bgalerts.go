package ui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/icortesb/lazykuma/internal/autostart"
	"github.com/icortesb/lazykuma/internal/kuma"
)

// Autostart is the switch behind "Background alerts": whether `lazykuma
// watch` is registered to start at login. autostart.Manager satisfies it.
type Autostart interface {
	Status() (autostart.Status, error)
	Enable() error
	Disable() error
}

// bgAlerts is what the menu knows about the background watch. Status runs
// systemctl and friends, so it is only ever asked for in a command, and the
// menu says "…" until the answer comes.
type bgAlerts struct {
	st   autostart.Status
	err  error // the last status read failed
	busy bool  // a status read or a toggle is on its way: enter does nothing
	seen bool  // st holds an answer
}

// Messages of the background alerts commands.
type (
	bgStatus struct {
		st  autostart.Status
		err error
	}
	// bgToggled is the answer of a toggle: what it did, then the status read
	// after it.
	bgToggled struct {
		enabled bool // the switch was turned on
		err     error
		st      autostart.Status
		stErr   error
	}
)

func readBgStatus(a Autostart) tea.Cmd {
	return func() tea.Msg {
		st, err := a.Status()
		return bgStatus{st: st, err: err}
	}
}

func toggleBg(a Autostart, enable bool) tea.Cmd {
	return func() tea.Msg {
		var err error
		if enable {
			err = a.Enable()
		} else {
			err = a.Disable()
		}
		// The status is read even after a failure: the registration may
		// have changed half way.
		st, stErr := a.Status()
		return bgToggled{enabled: enable, err: err, st: st, stErr: stErr}
	}
}

// item is the menu entry for the switch.
func (b bgAlerts) item() menuItem {
	it := menuItem{inst: -1, bg: true}
	switch {
	case b.busy:
		it.label = "Background alerts: …"
		it.desc = "Checking…"
	case !b.seen:
		it.label = "Background alerts: ?"
		it.desc = "Could not tell whether they run · enter tries to keep them running at login"
	case b.st.On:
		it.label = "Background alerts: on"
		it.desc = "Alerts keep coming with lazykuma closed · enter turns them off"
	default:
		it.label = "Background alerts: off"
		it.desc = "Only while lazykuma is open · enter keeps them running at login"
	}
	return it
}

// toggleBgAlerts runs the switch, unless one is already on its way.
func (m Model) toggleBgAlerts() (tea.Model, tea.Cmd) {
	if m.deps.Autostart == nil || m.bg.busy {
		return m, nil
	}
	m.bg.busy = true
	return m, toggleBg(m.deps.Autostart, !m.bg.st.On)
}

func (m Model) bgStatusRead(msg bgStatus) (tea.Model, tea.Cmd) {
	m.bg.busy = false
	m.bg.st, m.bg.err, m.bg.seen = msg.st, msg.err, msg.err == nil
	if msg.err != nil {
		return m, flashFor("background alerts: "+kuma.Brief(msg.err), 8*time.Second)
	}
	return m, nil
}

func (m Model) bgToggled(msg bgToggled) (tea.Model, tea.Cmd) {
	m.bg.busy = false
	m.bg.st, m.bg.err, m.bg.seen = msg.st, msg.stErr, msg.stErr == nil
	switch {
	case msg.err != nil:
		return m, flashFor("background alerts: "+kuma.Brief(msg.err), 8*time.Second)
	case msg.stErr != nil:
		return m, flashFor("background alerts: "+kuma.Brief(msg.stErr), 8*time.Second)
	case !msg.st.On:
		return m, flashFor("Background alerts off", 3*time.Second)
	case msg.st.Running:
		return m, flashFor(fmt.Sprintf("Background alerts on: watch running (pid %d)", msg.st.PID), 5*time.Second)
	}
	return m, flashFor("Background alerts on: watch not running yet", 5*time.Second)
}
