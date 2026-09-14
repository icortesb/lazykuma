// Package state is what lazykuma knows about each instance, built by folding
// the events of package kuma into it. Apply is pure: the UI keeps the value it
// returns and nothing else changes it.
package state

import (
	"maps"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/icortesb/lazykuma/internal/kuma"
)

// beatsKept is how many heartbeats each monitor keeps: enough for the
// sparkline and the bar on the widest terminal.
const beatsKept = 100

// Conn is the state of the link to an instance.
type Conn int

const (
	ConnConnecting Conn = iota
	ConnOK
	ConnDown
	ConnNoCred
	ConnBadCred
	ConnUnsupported
)

// String is the word the menu footer uses.
func (c Conn) String() string {
	switch c {
	case ConnOK:
		return "ok"
	case ConnDown:
		return "down"
	case ConnNoCred:
		return "no cred"
	case ConnBadCred:
		return "bad cred"
	case ConnUnsupported:
		return "unsupported"
	}
	return "connecting"
}

// Status is a monitor's state as the list shows it.
type Status int

const (
	StatusUnknown Status = iota // no heartbeat yet
	StatusDown
	StatusPending
	StatusUp
	StatusMaintenance
	StatusPaused
)

// Monitor is a Kuma monitor with what the server has said about it.
type Monitor struct {
	kuma.Monitor
	Beats      []kuma.Beat // oldest first, at most beatsKept
	Uptime24   float64     // 0 to 1
	HasUptime  bool
	AvgPing    float64
	HasAvgPing bool
	CertDays   int
	HasCert    bool
}

// Last is the latest heartbeat.
func (m Monitor) Last() (kuma.Beat, bool) {
	if len(m.Beats) == 0 {
		return kuma.Beat{}, false
	}
	return m.Beats[len(m.Beats)-1], true
}

// Status is Paused for a monitor switched off, otherwise the last beat's.
func (m Monitor) Status() Status {
	if !m.Active {
		return StatusPaused
	}
	b, ok := m.Last()
	if !ok {
		return StatusUnknown
	}
	switch b.Status {
	case kuma.StatusUp:
		return StatusUp
	case kuma.StatusDown:
		return StatusDown
	case kuma.StatusPending:
		return StatusPending
	case kuma.StatusMaintenance:
		return StatusMaintenance
	}
	return StatusUnknown
}

// incidentsKept is how many state changes an instance remembers: enough to
// fill the incidents screen without growing without bound.
const incidentsKept = 200

// Incident is one monitor changing state, the material of the incidents
// screen.
type Incident struct {
	MonitorID int
	Time      time.Time
	Status    kuma.Status
	Msg       string
}

// Instance is everything known about one Kuma.
type Instance struct {
	Conn      Conn
	Detail    string // why Conn is not ok: the error or Kuma's message
	Version   string
	Monitors  map[int]Monitor
	LastEvent time.Time // the last thing the server said
	// Listed is whether the monitor list has arrived: Kuma sends it after
	// every login, even when empty, so until then "no monitors" means
	// "not told yet".
	Listed     bool
	StaleSince time.Time // when the data stopped being live; zero while connected

	// Channels are the notification channels this Kuma sends alerts through.
	Channels []kuma.Notification
	// Maintenances are its maintenance windows, by id.
	Maintenances map[int]kuma.Maintenance
	// Types are the monitor types it supports, for the type picker.
	Types []string
	// Incidents are the state changes it reported, oldest first.
	Incidents []Incident
}

// RecentIncidents is the newest n state changes, newest first.
func (in Instance) RecentIncidents(n int) []Incident {
	out := make([]Incident, 0, min(n, len(in.Incidents)))
	for i := len(in.Incidents) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, in.Incidents[i])
	}
	return out
}

// Apply folds one event into the instance.
func Apply(in Instance, ev kuma.Event, now time.Time) Instance {
	switch ev := ev.(type) {
	case kuma.Connecting:
		in.Conn, in.Detail = ConnConnecting, ""
		return in
	case kuma.Connected:
		// Kuma sends the monitor list again after this login.
		in.Conn, in.Detail, in.StaleSince, in.Listed = ConnOK, "", time.Time{}, false
		return in
	case kuma.Disconnected:
		in.Conn = ConnDown
		if ev.Err != nil {
			in.Detail = kuma.Brief(ev.Err)
		}
		return goStale(in, now)
	case kuma.AuthFailed:
		in.Conn, in.Detail = ConnBadCred, ev.Msg
		if ev.NoToken {
			in.Conn, in.Detail = ConnNoCred, ""
		}
		return goStale(in, now)
	case kuma.Unsupported:
		in.Conn, in.Version = ConnUnsupported, ev.Version
		in.Detail = "Kuma " + ev.Version + " is not supported; lazykuma needs v2"
		return goStale(in, now)
	}

	// From here on, the server talking.
	in.LastEvent = now
	switch ev := ev.(type) {
	case kuma.Info:
		if ev.Version != "" {
			in.Version = ev.Version
		}
	case kuma.MonitorList:
		in.Listed = true
		next := make(map[int]Monitor, len(ev.Monitors))
		for id, km := range ev.Monitors {
			m := in.Monitors[id] // keeps the beats and stats already held
			m.Monitor = km
			next[id] = m
		}
		in.Monitors = next
	case kuma.MonitorUpdate:
		in.Monitors = clone(in.Monitors)
		for id, km := range ev.Monitors {
			m := in.Monitors[id]
			m.Monitor = km
			in.Monitors[id] = m
		}
	case kuma.MonitorDeleted:
		in.Monitors = clone(in.Monitors)
		delete(in.Monitors, ev.ID)
	case kuma.Heartbeat:
		in = update(in, ev.Beat.MonitorID, func(m *Monitor) {
			m.Beats = mergeBeats(m.Beats, []kuma.Beat{ev.Beat})
		})
		in = addIncidents(in, []kuma.Beat{ev.Beat})
	case kuma.HeartbeatList:
		in = update(in, ev.MonitorID, func(m *Monitor) {
			if ev.Overwrite {
				m.Beats = mergeBeats(nil, ev.Beats)
			} else {
				m.Beats = mergeBeats(m.Beats, ev.Beats)
			}
		})
		in = addIncidents(in, ev.Beats)
	case kuma.AvgPing:
		in = update(in, ev.MonitorID, func(m *Monitor) { m.AvgPing, m.HasAvgPing = ev.Ms, ev.Valid })
	case kuma.Uptime:
		if ev.Period == "24" {
			in = update(in, ev.MonitorID, func(m *Monitor) { m.Uptime24, m.HasUptime = ev.Ratio, true })
		}
	case kuma.NotificationList:
		in.Channels = ev.Notifications
	case kuma.MaintenanceList:
		in.Maintenances = ev.Maintenances
	case kuma.MonitorTypes:
		in.Types = ev.All()
	case kuma.CertInfo:
		in = update(in, ev.MonitorID, func(m *Monitor) {
			m.CertDays, m.HasCert = ev.DaysRemaining, ev.Valid
		})
	}
	return in
}

// addIncidents records the important beats among these: Kuma marks a beat
// important when the monitor changed state, which is exactly an incident.
// Repeats are dropped, so a reconnect's replayed history adds nothing.
func addIncidents(in Instance, beats []kuma.Beat) Instance {
	fresh := make([]Incident, 0, len(beats))
	for _, b := range beats {
		if !b.Important {
			continue
		}
		inc := Incident{MonitorID: b.MonitorID, Time: b.Time, Status: b.Status, Msg: b.Msg}
		if !slices.Contains(in.Incidents, inc) {
			fresh = append(fresh, inc)
		}
	}
	if len(fresh) == 0 {
		return in
	}
	all := make([]Incident, 0, len(in.Incidents)+len(fresh))
	all = append(append(all, in.Incidents...), fresh...)
	sort.SliceStable(all, func(i, j int) bool { return all[i].Time.Before(all[j].Time) })
	if len(all) > incidentsKept {
		all = all[len(all)-incidentsKept:]
	}
	in.Incidents = all
	return in
}

// goStale marks the data as no longer live, from the last time the server
// spoke; it keeps the first such time across repeated failures.
func goStale(in Instance, now time.Time) Instance {
	if !in.StaleSince.IsZero() || len(in.Monitors) == 0 {
		return in
	}
	in.StaleSince = in.LastEvent
	if in.StaleSince.IsZero() {
		in.StaleSince = now
	}
	return in
}

// update changes one monitor on a copy of the map. A monitor not seen yet is
// created: a heartbeat can arrive before the list that names it.
func update(in Instance, id int, f func(*Monitor)) Instance {
	in.Monitors = clone(in.Monitors)
	m, ok := in.Monitors[id]
	if !ok {
		m.ID, m.Active = id, true
	}
	f(&m)
	in.Monitors[id] = m
	return in
}

func clone(m map[int]Monitor) map[int]Monitor {
	if m == nil {
		return map[int]Monitor{}
	}
	return maps.Clone(m)
}

// mergeBeats puts both lists in time order, drops the repeats a reconnect
// sends again, and keeps the newest beatsKept. It never changes old.
func mergeBeats(old, add []kuma.Beat) []kuma.Beat {
	all := make([]kuma.Beat, 0, len(old)+len(add))
	all = append(append(all, old...), add...)
	sort.SliceStable(all, func(i, j int) bool { return all[i].Time.Before(all[j].Time) })
	all = slices.CompactFunc(all, func(a, b kuma.Beat) bool { return a.Time.Equal(b.Time) })
	if len(all) > beatsKept {
		all = all[len(all)-beatsKept:]
	}
	return all
}

// Counts is how many monitors are in each status.
type Counts struct {
	Total, Up, Down, Pending, Maintenance, Paused int
}

func (in Instance) Counts() Counts {
	c := Counts{Total: len(in.Monitors)}
	for _, m := range in.Monitors {
		switch m.Status() {
		case StatusUp:
			c.Up++
		case StatusDown:
			c.Down++
		case StatusPending:
			c.Pending++
		case StatusMaintenance:
			c.Maintenance++
		case StatusPaused:
			c.Paused++
		}
	}
	return c
}

// Sorted is the monitors the way the list shows them: down first, then by
// name.
func (in Instance) Sorted() []Monitor {
	out := make([]Monitor, 0, len(in.Monitors))
	for _, m := range in.Monitors {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		di, dj := out[i].Status() == StatusDown, out[j].Status() == StatusDown
		if di != dj {
			return di
		}
		ni, nj := strings.ToLower(out[i].Name), strings.ToLower(out[j].Name)
		if ni != nj {
			return ni < nj
		}
		return out[i].ID < out[j].ID
	})
	return out
}
