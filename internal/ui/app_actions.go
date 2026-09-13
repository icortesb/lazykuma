package ui

import (
	"context"
	"fmt"
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

// channelToggles is the instance's channels. A new monitor starts with the
// default ones ticked, which is what Kuma's own form does and what the ★ in
// the channels list promises; an edit has its own ticked by the caller.
func (m Model) newChannelToggles() []channelToggle {
	return m.channelToggles(true)
}

func (m Model) channelToggles(tickDefaults bool) []channelToggle {
	st := m.current().st
	out := make([]channelToggle, 0, len(st.Channels))
	for _, c := range st.Channels {
		out = append(out, channelToggle{id: c.ID, name: c.Name, on: tickDefaults && c.IsDefault})
	}
	return out
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
	m.mform = newMonitorForm(kind, m.newChannelToggles())
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
			m.raw = newRawEditor("monitor", 0, rawSkeleton(value))
			m.backTo, m.screen = screenInstance, screenRaw
		case "channel":
			if value == "other" {
				m.raw = newRawEditor("channel", 0, map[string]any{"name": "", "type": "", "isDefault": false})
				m.backTo, m.screen = screenChannels, screenRaw
				return m, nil
			}
			m.cform = newChannelForm(value)
			m.backTo, m.screen = screenChannels, screenChannel
		}
	}
	return m, nil
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
		return m, saveMonitor(m.current().inst, mon, m.mform.id)
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
		return m, saveMonitor(m.current().inst, kuma.RawMonitor(values), m.raw.id)
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
		return m, silenceMonitor(m.current().inst, title, m.silence.monitor, start, end)
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
func loadMonitor(in *core.Instance, id int, toRaw bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		mon, err := in.GetMonitor(ctx, id)
		return monitorLoaded{instance: in.Name(), mon: mon, toRaw: toRaw, err: err}
	}
}

func saveMonitor(in *core.Instance, mon kuma.RawMonitor, id int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		name := fieldText(mon["name"])
		if id == 0 {
			_, err := in.AddMonitor(ctx, mon)
			return actionDone{name: in.Name(), action: "created", mon: name, err: err}
		}
		mon["id"] = float64(id)
		return actionDone{name: in.Name(), action: "saved", mon: name, err: in.EditMonitor(ctx, mon)}
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

func silenceMonitor(in *core.Instance, title string, mon state.Monitor, start, end time.Time) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		_, err := in.Silence(ctx, title, []int{mon.ID}, start, end)
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
