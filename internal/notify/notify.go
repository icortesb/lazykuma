// Package notify decides which changes deserve a notification and raises
// them on the desktop. The decision is plain logic over state snapshots, so
// it is tested without a desktop; only Desktop touches one.
package notify

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/gen2brain/beeep"

	"github.com/icortesb/lazykuma/internal/config"
	"github.com/icortesb/lazykuma/internal/state"
)

// Sender shows one notification.
type Sender interface {
	Send(title, body string) error
}

func init() {
	// Otherwise every notification is labelled "DefaultAppName", and on
	// Windows that is also the toast's header.
	beeep.AppName = "lazykuma"
}

// Desktop is the operating system's own notifications: D-Bus on Linux,
// Notification Center on macOS, toasts on Windows.
type Desktop struct{}

func (Desktop) Send(title, body string) error { return beeep.Notify(title, body, "") }

// Async sends through s on a goroutine of its own, so a slow notification
// system — an exec on macOS, COM and a sleep on Windows — never holds up the
// caller. When notifications pile up faster than they can be shown, the
// newest are dropped rather than the caller blocked; failures go to errs.
func Async(s Sender, errs func(error)) Sender {
	a := &async{queue: make(chan [2]string, 16)}
	go func() {
		for n := range a.queue {
			if err := s.Send(n[0], n[1]); err != nil && errs != nil {
				errs(err)
			}
		}
	}()
	return a
}

type async struct{ queue chan [2]string }

func (a *async) Send(title, body string) error {
	select {
	case a.queue <- [2]string{title, body}:
	default:
	}
	return nil
}

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

// Grace is how long an instance must stay unreachable before it is news. A
// Kuma restart, a container update or a laptop waking up drops the
// connection for seconds; that is not an outage worth an alert.
const Grace = 30 * time.Second

// Tracker remembers what each monitor and instance last was, and turns a
// new snapshot into the changes since.
type Tracker struct {
	on string

	// down is, per instance, which monitors were last seen down. A monitor
	// absent from it has not been seen with a known status yet.
	down map[string]map[int]bool
	// reached is the instances that have been connected at least once;
	// since is when each unreachable one was first seen so; lost is the
	// ones already reported unreachable.
	reached map[string]bool
	since   map[string]time.Time
	lost    map[string]bool
}

// NewTracker notifies on outages, and on recoveries too when on is
// config.NotifyChanges.
func NewTracker(on string) *Tracker {
	return &Tracker{
		on:      on,
		down:    map[string]map[int]bool{},
		reached: map[string]bool{},
		since:   map[string]time.Time{},
		lost:    map[string]bool{},
	}
}

// Waiting is the instances seen unreachable but not reported yet. A refused
// token stops the reconnect attempts, so no further update may come to
// carry one past Grace: whoever feeds the tracker must observe these again.
func (t *Tracker) Waiting() []string {
	names := make([]string, 0, len(t.since))
	for name := range t.since {
		if !t.lost[name] {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// Recheck is how often to observe the Waiting instances again.
const Recheck = Grace / 6

func (t *Tracker) recoveries() bool { return t.on == config.NotifyChanges }

// Observe folds one instance's snapshot in and returns what changed. The
// first time a monitor shows a status is its starting point, never news:
// Kuma sends everything on connect, and a fresh start must not raise an
// alert per monitor.
func (t *Tracker) Observe(name string, st state.Instance, now time.Time) []Event {
	var events []Event

	switch st.Conn {
	case state.ConnOK:
		delete(t.since, name)
		if t.lost[name] {
			delete(t.lost, name)
			if t.recoveries() {
				events = append(events, Event{Instance: name, Cause: "connected again", Time: now})
			}
		}
		t.reached[name] = true
	case state.ConnDown, state.ConnBadCred, state.ConnUnsupported:
		// Only an instance that was reachable can become unreachable; one
		// that never answered is a configuration problem, not an outage.
		// A refused token or an unsupported server stops monitoring just as
		// surely as a dead connection, so it is reported the same way.
		if !t.reached[name] || t.lost[name] {
			return events
		}
		first, seen := t.since[name]
		if !seen {
			t.since[name] = now
			return events
		}
		if now.Sub(first) >= Grace {
			t.lost[name] = true
			events = append(events, Event{Instance: name, Down: true, Cause: unreachableCause(st), Time: now})
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

// unreachableCause says why an instance cannot be monitored, in words: a
// dropped websocket's own error is often just "EOF".
func unreachableCause(st state.Instance) string {
	switch st.Conn {
	case state.ConnBadCred:
		return "Kuma refused the login token; log in again"
	case state.ConnUnsupported:
		return st.Detail
	}
	if st.Detail == "" || st.Detail == "EOF" || strings.Contains(st.Detail, "EOF") {
		return "connection lost"
	}
	return st.Detail
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
