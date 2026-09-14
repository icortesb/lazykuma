package notify

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/icortesb/lazykuma/internal/config"
	"github.com/icortesb/lazykuma/internal/kuma"
	"github.com/icortesb/lazykuma/internal/state"
)

var t0 = time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)

// instance is a connected instance whose monitors have the given statuses.
func instance(statuses map[int]kuma.Status) state.Instance {
	in := state.Apply(state.Instance{}, kuma.Connected{}, t0)
	mons := map[int]kuma.Monitor{}
	for id := range statuses {
		mons[id] = kuma.Monitor{ID: id, Name: map[int]string{1: "web", 2: "db", 3: "mail"}[id], Active: true}
	}
	in = state.Apply(in, kuma.MonitorList{Monitors: mons}, t0)
	for id, st := range statuses {
		in = state.Apply(in, kuma.Heartbeat{Beat: kuma.Beat{
			MonitorID: id, Status: st, Msg: map[kuma.Status]string{kuma.StatusDown: "connect ECONNREFUSED", kuma.StatusUp: "200 - OK"}[st],
			Time: t0.Add(time.Duration(id) * time.Second),
		}}, t0)
	}
	return in
}

func TestTheFirstSnapshotIsNeverNews(t *testing.T) {
	tr := NewTracker(config.NotifyChanges)
	// Kuma sends everything on connect, including what is already down.
	if ev := tr.Observe("home", instance(map[int]kuma.Status{1: kuma.StatusDown, 2: kuma.StatusUp}), t0); len(ev) != 0 {
		t.Fatalf("the starting point raised %+v", ev)
	}
	// The same state again is not news either.
	if ev := tr.Observe("home", instance(map[int]kuma.Status{1: kuma.StatusDown, 2: kuma.StatusUp}), t0); len(ev) != 0 {
		t.Fatalf("an unchanged state raised %+v", ev)
	}
}

func TestOutagesAndRecoveries(t *testing.T) {
	down := NewTracker(config.NotifyDown)
	changes := NewTracker(config.NotifyChanges)
	for _, tr := range []*Tracker{down, changes} {
		tr.Observe("home", instance(map[int]kuma.Status{1: kuma.StatusUp, 2: kuma.StatusUp}), t0)
	}

	// web goes down: both trackers report it.
	outage := instance(map[int]kuma.Status{1: kuma.StatusDown, 2: kuma.StatusUp})
	for name, tr := range map[string]*Tracker{"down": down, "changes": changes} {
		ev := tr.Observe("home", outage, t0)
		if len(ev) != 1 || !ev[0].Down || ev[0].Monitor != "web" || ev[0].Cause != "connect ECONNREFUSED" {
			t.Fatalf("%s tracker: %+v", name, ev)
		}
	}

	// web comes back: only the "changes" tracker says so.
	back := instance(map[int]kuma.Status{1: kuma.StatusUp, 2: kuma.StatusUp})
	if ev := down.Observe("home", back, t0); len(ev) != 0 {
		t.Errorf(`on = "down" reported a recovery: %+v`, ev)
	}
	ev := changes.Observe("home", back, t0)
	if len(ev) != 1 || ev[0].Down || ev[0].Monitor != "web" {
		t.Fatalf(`on = "changes": %+v`, ev)
	}
}

func TestPausedAndUnknownAreNotOutages(t *testing.T) {
	tr := NewTracker(config.NotifyChanges)
	tr.Observe("home", instance(map[int]kuma.Status{1: kuma.StatusDown}), t0)

	// Pausing a down monitor is not a recovery.
	paused := state.Apply(instance(map[int]kuma.Status{1: kuma.StatusDown}), kuma.MonitorUpdate{
		Monitors: map[int]kuma.Monitor{1: {ID: 1, Name: "web", Active: false}},
	}, t0)
	if ev := tr.Observe("home", paused, t0); len(ev) != 0 {
		t.Fatalf("pausing raised %+v", ev)
	}
	// A monitor with no beat yet has nothing to compare.
	fresh := state.Apply(state.Apply(state.Instance{}, kuma.Connected{}, t0),
		kuma.MonitorList{Monitors: map[int]kuma.Monitor{7: {ID: 7, Name: "new", Active: true}}}, t0)
	if ev := tr.Observe("home", fresh, t0); len(ev) != 0 {
		t.Fatalf("a monitor without beats raised %+v", ev)
	}
}

func TestUnreachableInstance(t *testing.T) {
	tr := NewTracker(config.NotifyChanges)

	// An instance that never connected is a setup problem, not an outage.
	never := state.Apply(state.Instance{}, kuma.Disconnected{Err: errors.New("connection refused")}, t0)
	if ev := tr.Observe("vps", never, t0); len(ev) != 0 {
		t.Fatalf("never-reached instance raised %+v", ev)
	}

	tr.Observe("home", instance(map[int]kuma.Status{1: kuma.StatusUp}), t0)
	lost := state.Apply(instance(map[int]kuma.Status{1: kuma.StatusUp}), kuma.Disconnected{Err: errors.New("connection refused")}, t0)
	ev := tr.Observe("home", lost, t0)
	if len(ev) != 1 || !ev[0].Down || ev[0].Monitor != "" || !strings.Contains(ev[0].Title(), "home is unreachable") {
		t.Fatalf("lost instance: %+v", ev)
	}
	// Still down: said once, not on every reconnect attempt.
	if ev := tr.Observe("home", lost, t0); len(ev) != 0 {
		t.Fatalf("repeated %+v", ev)
	}
	// While unreachable, the monitors' stale statuses are not compared.
	staleDown := state.Apply(instance(map[int]kuma.Status{1: kuma.StatusDown}), kuma.Disconnected{Err: errors.New("x")}, t0)
	if ev := tr.Observe("home", staleDown, t0); len(ev) != 0 {
		t.Fatalf("stale monitors raised %+v", ev)
	}

	// Back.
	ev = tr.Observe("home", instance(map[int]kuma.Status{1: kuma.StatusUp}), t0)
	if len(ev) != 1 || ev[0].Down || ev[0].Monitor != "" {
		t.Fatalf("reconnected: %+v", ev)
	}
}

func TestDeletedMonitorIsForgotten(t *testing.T) {
	tr := NewTracker(config.NotifyChanges)
	tr.Observe("home", instance(map[int]kuma.Status{1: kuma.StatusDown, 2: kuma.StatusUp}), t0)
	tr.Observe("home", instance(map[int]kuma.Status{2: kuma.StatusUp}), t0) // 1 deleted
	// A new monitor reusing id 1 starts fresh: its first status is not news.
	if ev := tr.Observe("home", instance(map[int]kuma.Status{1: kuma.StatusUp, 2: kuma.StatusUp}), t0); len(ev) != 0 {
		t.Fatalf("a reused id raised %+v", ev)
	}
}

func TestEventText(t *testing.T) {
	down := Event{Instance: "home", Monitor: "web", Down: true, Cause: "connect ECONNREFUSED", Time: t0}
	if down.Title() != "✖ web is down" || down.Body() != "on home · connect ECONNREFUSED" {
		t.Errorf("down = %q / %q", down.Title(), down.Body())
	}
	if line := down.Line(); !strings.Contains(line, "down  home / web  connect ECONNREFUSED") {
		t.Errorf("line = %q", line)
	}
	back := Event{Instance: "home", Monitor: "web", Time: t0}
	if back.Title() != "✔ web is back" || back.Body() != "on home" {
		t.Errorf("back = %q / %q", back.Title(), back.Body())
	}
	lost := Event{Instance: "vps", Down: true, Cause: "connection refused", Time: t0}
	if lost.Title() != "✖ vps is unreachable" || lost.Body() != "connection refused" {
		t.Errorf("lost = %q / %q", lost.Title(), lost.Body())
	}
}
