package notify

import (
	"errors"
	"io"
	"slices"
	"strings"
	"sync"
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
	for _, at := range []time.Time{t0, t0.Add(Grace)} {
		if ev := tr.Observe("vps", never, at); len(ev) != 0 {
			t.Fatalf("never-reached instance raised %+v", ev)
		}
	}

	tr.Observe("home", instance(map[int]kuma.Status{1: kuma.StatusUp}), t0)
	lost := state.Apply(instance(map[int]kuma.Status{1: kuma.StatusUp}), kuma.Disconnected{Err: errors.New("connection refused")}, t0)
	if ev := tr.Observe("home", lost, t0); len(ev) != 0 {
		t.Fatalf("reported before the grace period: %+v", ev)
	}
	if w := tr.Waiting(); len(w) != 1 || w[0] != "home" {
		t.Fatalf("Waiting = %v", w)
	}
	if ev := tr.Observe("home", lost, t0.Add(Grace-time.Second)); len(ev) != 0 {
		t.Fatalf("reported before the grace period: %+v", ev)
	}
	ev := tr.Observe("home", lost, t0.Add(Grace))
	if len(ev) != 1 || !ev[0].Down || ev[0].Monitor != "" || !strings.Contains(ev[0].Title(), "home is unreachable") || ev[0].Cause != "connection refused" {
		t.Fatalf("lost instance: %+v", ev)
	}
	if w := tr.Waiting(); len(w) != 0 {
		t.Fatalf("still waiting on a reported instance: %v", w)
	}
	// Still down: said once, not on every reconnect attempt.
	if ev := tr.Observe("home", lost, t0.Add(2*Grace)); len(ev) != 0 {
		t.Fatalf("repeated %+v", ev)
	}
	// While unreachable, the monitors' stale statuses are not compared.
	staleDown := state.Apply(instance(map[int]kuma.Status{1: kuma.StatusDown}), kuma.Disconnected{Err: errors.New("x")}, t0)
	if ev := tr.Observe("home", staleDown, t0.Add(2*Grace)); len(ev) != 0 {
		t.Fatalf("stale monitors raised %+v", ev)
	}

	// Back.
	ev = tr.Observe("home", instance(map[int]kuma.Status{1: kuma.StatusUp}), t0.Add(3*Grace))
	if len(ev) != 1 || ev[0].Down || ev[0].Monitor != "" {
		t.Fatalf("reconnected: %+v", ev)
	}
}

func TestABriefDisconnectIsNotNews(t *testing.T) {
	// A Kuma restart drops the connection for a few seconds.
	tr := NewTracker(config.NotifyChanges)
	tr.Observe("home", instance(map[int]kuma.Status{1: kuma.StatusUp}), t0)
	lost := state.Apply(instance(map[int]kuma.Status{1: kuma.StatusUp}), kuma.Disconnected{Err: io.EOF}, t0)
	tr.Observe("home", lost, t0)
	if ev := tr.Observe("home", instance(map[int]kuma.Status{1: kuma.StatusUp}), t0.Add(5*time.Second)); len(ev) != 0 {
		t.Fatalf("a restart raised %+v", ev)
	}
	// The next drop starts its own grace period.
	tr.Observe("home", lost, t0.Add(Grace))
	if ev := tr.Observe("home", lost, t0.Add(Grace+time.Second)); len(ev) != 0 {
		t.Fatalf("the earlier drop's clock carried over: %+v", ev)
	}
}

func TestUnreachableCauses(t *testing.T) {
	up := instance(map[int]kuma.Status{1: kuma.StatusUp})
	tests := map[string]struct {
		ev   kuma.Event
		want string
	}{
		"dropped":     {kuma.Disconnected{Err: io.EOF}, "connection lost"},
		"bad token":   {kuma.AuthFailed{Msg: "authInvalidToken"}, "Kuma refused the login token; log in again"},
		"unsupported": {kuma.Unsupported{Version: "1.23.16"}, "Kuma 1.23.16 is not supported; lazykuma needs v2"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			tr := NewTracker(config.NotifyDown)
			tr.Observe("home", up, t0)
			gone := state.Apply(up, tt.ev, t0)
			tr.Observe("home", gone, t0)
			ev := tr.Observe("home", gone, t0.Add(Grace))
			if len(ev) != 1 || ev[0].Cause != tt.want {
				t.Fatalf("got %+v, want cause %q", ev, tt.want)
			}
		})
	}
}

type recorder struct {
	mu   sync.Mutex
	got  []string
	gate chan struct{}
	err  error
}

func (r *recorder) Send(title, body string) error {
	if r.gate != nil {
		<-r.gate
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, title+"|"+body)
	return r.err
}

func (r *recorder) sent() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.got)
}

func TestAsyncNeverBlocksTheCaller(t *testing.T) {
	// A notification daemon that hangs.
	r := &recorder{gate: make(chan struct{})}
	a := Async(r, nil)
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			a.Send("t", "b")
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Send blocked on a hung daemon")
	}
	close(r.gate)
	// What fit in the queue (and the one in hand) still goes out; the rest
	// was dropped.
	deadline := time.Now().Add(2 * time.Second)
	for len(r.sent()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	if n := len(r.sent()); n == 0 || n > 17 {
		t.Fatalf("delivered %d", n)
	}
}

func TestAsyncReportsFailures(t *testing.T) {
	errs := make(chan error, 1)
	Async(&recorder{err: errors.New("no daemon")}, func(err error) { errs <- err }).Send("t", "b")
	select {
	case err := <-errs:
		if err.Error() != "no daemon" {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the failure was not reported")
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
