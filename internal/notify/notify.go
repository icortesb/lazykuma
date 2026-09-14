// Package notify decides which changes deserve a notification and raises
// them on the desktop. The decision is plain logic over state snapshots, so
// it is tested without a desktop; only Desktop touches one.
package notify

import (
	"fmt"
	"slices"
	"time"

	"github.com/gen2brain/beeep"

	"github.com/icortesb/lazykuma/internal/config"
	"github.com/icortesb/lazykuma/internal/state"
)

// Sender shows one notification.
type Sender interface {
	Send(title, body string) error
}

// Desktop is the operating system's own notifications: D-Bus on Linux,
// Notification Center on macOS, toasts on Windows.
type Desktop struct{}

func (Desktop) Send(title, body string) error { return beeep.Notify(title, body, "") }

// Event is one change worth telling someone about.
type Event struct {
	Instance string
	Monitor  string // empty when the instance itself is the news
	Down     bool   // it went down; false means it came back
	Cause    string // what Kuma or the connection said
	Time     time.Time
}

// Title is the notification's first line.
func (e Event) Title() string {
	subject := e.Monitor
	if subject == "" {
		subject = e.Instance
	}
	if e.Down {
		if e.Monitor == "" {
			return "✖ " + subject + " is unreachable"
		}
		return "✖ " + subject + " is down"
	}
	return "✔ " + subject + " is back"
}

// Body is the notification's detail.
func (e Event) Body() string {
	if e.Monitor == "" {
		return e.Cause
	}
	if e.Cause == "" {
		return "on " + e.Instance
	}
	return "on " + e.Instance + " · " + e.Cause
}

// Line is the event as one line of the watch command's output.
func (e Event) Line() string {
	what := "down"
	if !e.Down {
		what = "back"
	}
	subject := e.Instance
	if e.Monitor != "" {
		subject += " / " + e.Monitor
	}
	line := fmt.Sprintf("%s  %-4s  %s", e.Time.Local().Format("2006-01-02 15:04:05"), what, subject)
	if e.Cause != "" {
		line += "  " + e.Cause
	}
	return line
}

// Tracker remembers what each monitor and instance last was, and turns a
// new snapshot into the changes since.
type Tracker struct {
	on string

	// down is, per instance, which monitors were last seen down. A monitor
	// absent from it has not been seen with a known status yet.
	down map[string]map[int]bool
	// reached is the instances that have been connected at least once, and
	// lost the ones that are unreachable now.
	reached map[string]bool
	lost    map[string]bool
}

// NewTracker notifies on outages, and on recoveries too when on is
// config.NotifyChanges.
func NewTracker(on string) *Tracker {
	return &Tracker{
		on:      on,
		down:    map[string]map[int]bool{},
		reached: map[string]bool{},
		lost:    map[string]bool{},
	}
}

func (t *Tracker) recoveries() bool { return t.on == config.NotifyChanges }

// Observe folds one instance's snapshot in and returns what changed. The
// first time a monitor shows a status is its starting point, never news:
// Kuma sends everything on connect, and a fresh start must not raise an
// alert per monitor.
func (t *Tracker) Observe(name string, st state.Instance, now time.Time) []Event {
	var events []Event

	switch st.Conn {
	case state.ConnOK:
		if t.lost[name] {
			delete(t.lost, name)
			if t.recoveries() {
				events = append(events, Event{Instance: name, Cause: "connected again", Time: now})
			}
		}
		t.reached[name] = true
	case state.ConnDown:
		// Only an instance that was reachable can become unreachable; one
		// that never answered is a configuration problem, not an outage.
		if t.reached[name] && !t.lost[name] {
			t.lost[name] = true
			events = append(events, Event{Instance: name, Down: true, Cause: st.Detail, Time: now})
		}
		// While the connection is down the monitors' statuses are stale.
		return events
	default:
		return events
	}

	seen := t.down[name]
	if seen == nil {
		seen = map[int]bool{}
		t.down[name] = seen
	}
	for id := range seen {
		if _, still := st.Monitors[id]; !still {
			delete(seen, id) // deleted in Kuma: nothing to report
		}
	}
	for _, id := range sortedIDs(st.Monitors) {
		m := st.Monitors[id]
		var isDown bool
		switch m.Status() {
		case state.StatusDown:
			isDown = true
		case state.StatusUp:
			isDown = false
		default:
			// Paused, in maintenance, pending or without a beat yet: not an
			// outage and not a recovery, so nothing to compare.
			continue
		}
		was, known := seen[id]
		seen[id] = isDown
		if !known || was == isDown {
			continue
		}
		if isDown || t.recoveries() {
			events = append(events, Event{
				Instance: name, Monitor: m.Name, Down: isDown, Cause: lastMessage(m), Time: now,
			})
		}
	}
	return events
}

func lastMessage(m state.Monitor) string {
	if b, ok := m.Last(); ok {
		return b.Msg
	}
	return ""
}

// sortedIDs gives the monitors in a stable order, so a burst of changes is
// reported the same way every time.
func sortedIDs(ms map[int]state.Monitor) []int {
	ids := make([]int, 0, len(ms))
	for id := range ms {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}
