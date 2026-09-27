package ui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/icortesb/lazykuma/internal/state"
)

// actOn runs one of the actions that work on a single monitor or group —
// pause, edit, fields, clone, delete, silence, move — for row, from the
// screen the user is on. The forms, pickers and questions it opens return
// there: the instance list, or a monitor's detail.
func (m Model) actOn(act instAction, row state.Row, from screen) (Model, tea.Cmd) {
	in := m.current()
	switch act {
	case instToggle:
		if row.Group {
			verb, detail := "Pause", "Kuma stops checking every monitor in the group until it is resumed"
			if !row.Active {
				verb, detail = "Resume", "every monitor in the group is resumed, including any paused on its own"
			}
			m.ask = confirm{
				question: fmt.Sprintf("%s %s and its %s?", verb, row.Name, monitors(row.Children)),
				detail:   detail,
			}
			// The ids are taken now: the group can change while the question
			// is on screen, and the answer is to what was asked.
			ids := coveredBy(in.st, row.Monitor)
			m.onYes, m.backTo, m.screen = toggleGroup(in.inst, row.Monitor, ids), from, screenConfirm
			return m, nil
		}
		return m, togglePause(in.inst, row.Monitor)
	case instEdit:
		if row.Group {
			m.nform = newNameForm("Rename group", "", row.Name, row.ID)
			m.backTo, m.screen = from, screenName
			return m, nil
		}
		return m, loadMonitor(in.inst, row.ID, loadEdit, from)
	case instRaw:
		return m, loadMonitor(in.inst, row.ID, loadRaw, from)
	case instClone:
		return m, loadMonitor(in.inst, row.ID, loadClone, from)
	case instDelete:
		if row.Group {
			m.moving = row.Monitor
			m.pick = newPicker("Delete group "+row.Name, "", []option{
				{fmt.Sprintf("Delete the group, keep its %s", monitors(row.Children)), "keep"},
				{fmt.Sprintf("Delete the group and its %s", monitors(row.Children)), "all"},
				{"Cancel", "cancel"},
			})
			m.picking, m.backTo, m.screen = "delgroup", from, screenPick
			return m, nil
		}
		m.ask = confirm{
			question: fmt.Sprintf("Delete %q?", row.Name),
			detail:   "Kuma removes the monitor and all of its history",
		}
		m.onYes, m.backTo, m.screen = deleteMonitor(in.inst, row.Monitor), from, screenConfirm
	case instSilence:
		m.silence = newSilenceForm(row.Monitor, coveredBy(in.st, row.Monitor))
		m.backTo, m.screen = from, screenSilence
	case instMove:
		m.moving = row.Monitor
		m.pick = newPicker("Move "+row.Name, "into which group?", groupOptions(in.st, row.Monitor))
		m.picking, m.backTo, m.screen = "move", from, screenPick
	}
	return m, nil
}
