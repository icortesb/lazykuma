package state

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/icortesb/lazykuma/internal/kuma"
)

var t0 = time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)

// replay folds every line of the named fixtures (testdata/kuma-v2, captured
// from a real Kuma 2.5.3) into an instance, in the order given.
func replay(t *testing.T, in Instance, names ...string) Instance {
	t.Helper()
	for _, name := range names {
		f, err := os.Open(filepath.Join("..", "..", "testdata", "kuma-v2", name+".jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(nil, 4<<20)
		for sc.Scan() {
			var arr []json.RawMessage
			if err := json.Unmarshal(sc.Bytes(), &arr); err != nil {
				t.Fatal(err)
			}
			var ev string
			json.Unmarshal(arr[0], &ev)
			e, ok, err := kuma.DecodeEvent(ev, arr[1:])
			if err != nil || !ok {
				t.Fatalf("%s: ok %v err %v", name, ok, err)
			}
			in = Apply(in, e, t0)
		}
		f.Close()
	}
	return in
}

func TestReplayRealSession(t *testing.T) {
	in := replay(t, Instance{}, "info", "monitorList", "heartbeatList", "heartbeat", "avgPing", "uptime", "certInfo")

	if in.Version != "2.5.3" {
		t.Errorf("version = %q", in.Version)
	}
	up, down := in.Monitors[1], in.Monitors[2]
	if up.Status() != StatusUp || down.Status() != StatusDown {
		t.Fatalf("statuses = %v, %v", up.Status(), down.Status())
	}
	if !up.HasAvgPing || up.AvgPing != 59.55 || down.HasAvgPing { // the last avgPing for 1 in the fixture
		t.Errorf("avg ping = %v/%v, %v", up.AvgPing, up.HasAvgPing, down.HasAvgPing)
	}
	if !up.HasUptime || up.Uptime24 != 1 || down.Uptime24 != 0 {
		t.Errorf("uptime = %v, %v", up.Uptime24, down.Uptime24)
	}
	if !up.HasCert || up.CertDays != 46 {
		t.Errorf("cert = %v/%v", up.CertDays, up.HasCert)
	}
	// The fixture has several overlapping lists: repeats must be gone.
	for i := 1; i < len(up.Beats); i++ {
		if !up.Beats[i-1].Time.Before(up.Beats[i].Time) {
			t.Fatalf("beats out of order or repeated at %d", i)
		}
	}
	if got := in.Sorted(); got[0].ID != 2 {
		t.Errorf("first in list = %d, want the down monitor 2", got[0].ID)
	}
	if c := in.Counts(); c != (Counts{Total: 2, Up: 1, Down: 1}) {
		t.Errorf("counts = %+v", c)
	}
}

func TestPauseShowsThroughUpdate(t *testing.T) {
	in := replay(t, Instance{}, "monitorList", "heartbeatList")
	beats := len(in.Monitors[1].Beats)

	paused := Apply(in, kuma.MonitorUpdate{Monitors: map[int]kuma.Monitor{1: {ID: 1, Name: "https://example.com"}}}, t0)
	if paused.Monitors[1].Status() != StatusPaused {
		t.Fatalf("status = %v, want paused", paused.Monitors[1].Status())
	}
	if len(paused.Monitors[1].Beats) != beats {
		t.Fatal("the update dropped the beats")
	}
	if in.Monitors[1].Status() == StatusPaused {
		t.Fatal("Apply changed its input")
	}
}

func TestMonitorListDropsDeletedAndKeepsBeats(t *testing.T) {
	in := replay(t, Instance{}, "monitorList", "heartbeatList")
	only1 := kuma.MonitorList{Monitors: map[int]kuma.Monitor{1: {ID: 1, Name: "renamed", Active: true}}}
	out := Apply(in, only1, t0)
	if _, ok := out.Monitors[2]; ok {
		t.Fatal("monitor 2 still there")
	}
	if out.Monitors[1].Name != "renamed" || len(out.Monitors[1].Beats) == 0 {
		t.Fatalf("monitor 1 = %+v", out.Monitors[1])
	}

	out = Apply(out, kuma.MonitorDeleted{ID: 1}, t0)
	if len(out.Monitors) != 0 {
		t.Fatal("delete ignored")
	}
}

func TestBeatsCapped(t *testing.T) {
	in := Instance{}
	for i := 0; i < beatsKept+20; i++ {
		b := kuma.Beat{MonitorID: 1, Status: kuma.StatusUp, Time: t0.Add(time.Duration(i) * time.Second)}
		in = Apply(in, kuma.Heartbeat{Beat: b}, t0)
	}
	beats := in.Monitors[1].Beats
	if len(beats) != beatsKept {
		t.Fatalf("kept %d beats, want %d", len(beats), beatsKept)
	}
	if !beats[len(beats)-1].Time.Equal(t0.Add(time.Duration(beatsKept+19) * time.Second)) {
		t.Fatal("did not keep the newest")
	}
}

func TestHeartbeatListOverwrite(t *testing.T) {
	old := kuma.Beat{MonitorID: 1, Time: t0}
	in := Apply(Instance{}, kuma.Heartbeat{Beat: old}, t0)
	fresh := kuma.Beat{MonitorID: 1, Time: t0.Add(time.Minute)}
	in = Apply(in, kuma.HeartbeatList{MonitorID: 1, Beats: []kuma.Beat{fresh}, Overwrite: true}, t0)
	if got := in.Monitors[1].Beats; len(got) != 1 || !got[0].Time.Equal(fresh.Time) {
		t.Fatalf("beats = %+v", got)
	}
}

func TestConnectionStates(t *testing.T) {
	in := replay(t, Instance{}, "monitorList")
	last := t0

	in = Apply(in, kuma.Connecting{}, t0.Add(time.Minute))
	if in.Conn != ConnConnecting {
		t.Fatalf("conn = %v", in.Conn)
	}
	in = Apply(in, kuma.Connected{}, t0.Add(time.Minute))
	if in.Conn != ConnOK || !in.StaleSince.IsZero() {
		t.Fatalf("connected: %+v", in)
	}

	in = Apply(in, kuma.Disconnected{Err: errors.New("connection reset")}, t0.Add(5*time.Minute))
	if in.Conn != ConnDown || in.Detail != "connection reset" || !in.StaleSince.Equal(last) {
		t.Fatalf("down: conn %v detail %q stale %v", in.Conn, in.Detail, in.StaleSince)
	}
	// A second failure does not move the time the data went stale.
	in = Apply(in, kuma.Disconnected{Err: errors.New("refused")}, t0.Add(9*time.Minute))
	if !in.StaleSince.Equal(last) {
		t.Fatalf("stale moved to %v", in.StaleSince)
	}
	if len(in.Monitors) != 2 {
		t.Fatal("the monitors were dropped while down")
	}

	in = Apply(in, kuma.Connected{}, t0.Add(10*time.Minute))
	if !in.StaleSince.IsZero() {
		t.Fatal("still stale after reconnecting")
	}

	if got := Apply(in, kuma.AuthFailed{NoToken: true}, t0); got.Conn != ConnNoCred {
		t.Errorf("no token = %v", got.Conn)
	}
	if got := Apply(in, kuma.AuthFailed{Msg: "authInvalidToken"}, t0); got.Conn != ConnBadCred || got.Detail != "authInvalidToken" {
		t.Errorf("bad token = %v %q", got.Conn, got.Detail)
	}
	if got := Apply(in, kuma.Unsupported{Version: "1.23.16"}, t0); got.Conn != ConnUnsupported || got.Version != "1.23.16" {
		t.Errorf("v1 = %v %q", got.Conn, got.Version)
	}
}

func TestConnString(t *testing.T) {
	want := map[Conn]string{
		ConnConnecting: "connecting", ConnOK: "ok", ConnDown: "down",
		ConnNoCred: "no cred", ConnBadCred: "bad cred", ConnUnsupported: "unsupported",
	}
	for c, w := range want {
		if c.String() != w {
			t.Errorf("%d.String() = %q, want %q", c, c.String(), w)
		}
	}
}

func TestSortedByNameAfterDown(t *testing.T) {
	up := func(id int, name string) kuma.Monitor { return kuma.Monitor{ID: id, Name: name, Active: true} }
	in := Apply(Instance{}, kuma.MonitorList{Monitors: map[int]kuma.Monitor{
		1: up(1, "beta"), 2: up(2, "Alpha"), 3: up(3, "zeta"), 4: {ID: 4, Name: "paused"},
	}}, t0)
	in = Apply(in, kuma.Heartbeat{Beat: kuma.Beat{MonitorID: 3, Status: kuma.StatusDown, Time: t0}}, t0)

	var names []string
	for _, m := range in.Sorted() {
		names = append(names, m.Name)
	}
	want := []string{"zeta", "Alpha", "beta", "paused"}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("order = %v, want %v", names, want)
		}
	}
}
