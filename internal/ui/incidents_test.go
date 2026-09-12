package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/icortesb/lazykuma/internal/kuma"
	"github.com/icortesb/lazykuma/internal/state"
)

// withIncidents is an instance with two monitors and a few state changes.
func withIncidents() state.Instance {
	in := state.Apply(state.Instance{}, kuma.MonitorList{Monitors: map[int]kuma.Monitor{
		1: {ID: 1, Name: "nextcloud", Active: true},
		2: {ID: 2, Name: "vaultwarden", Active: true},
	}}, tBase)
	beats := []kuma.Beat{
		{MonitorID: 1, Status: kuma.StatusUp, Important: true, Msg: "200 - OK", Time: tBase},
		{MonitorID: 2, Status: kuma.StatusDown, Important: true, Msg: "connect ECONNREFUSED", Time: tBase.Add(time.Minute)},
		{MonitorID: 2, Status: kuma.StatusUp, Important: true, Msg: "200 - OK", Time: tBase.Add(2 * time.Minute)},
	}
	for _, b := range beats {
		in = state.Apply(in, kuma.Heartbeat{Beat: b}, tBase)
	}
	return in
}

func TestIncidentsScreenNewestFirst(t *testing.T) {
	s := newIncidentsScreen()
	in := withIncidents()
	out := ansi.Strip(s.View("home", in, 100, 24))

	for _, want := range []string{"home · incidents", "vaultwarden", "connect ECONNREFUSED", "✖ down", "✔ up", "/ filter"} {
		if !strings.Contains(out, want) {
			t.Errorf("view lacks %q:\n%s", want, out)
		}
	}
	// The newest change is on top: vaultwarden recovering, then its outage.
	up := strings.Index(out, "✔ up")
	down := strings.Index(out, "✖ down")
	if up > down {
		t.Errorf("not newest first:\n%s", out)
	}
	assertFits(t, out, 100)
}

func TestIncidentsFilterByMonitor(t *testing.T) {
	s := newIncidentsScreen()
	in := withIncidents()
	s, _, _ = s.Update(keyMsg("/"), in)
	for _, r := range "vault" {
		s, _, _ = s.Update(keyMsg(string(r)), in)
	}
	got := s.visible(in)
	if len(got) != 2 {
		t.Fatalf("filtered = %d incidents", len(got))
	}
	for _, inc := range got {
		if inc.MonitorID != 2 {
			t.Errorf("filter let through %+v", inc)
		}
	}
	// esc clears the filter first, and only then leaves.
	s, act, _ := s.Update(keyMsg("esc"), in)
	if act != incNone || len(s.visible(in)) != 3 {
		t.Fatalf("esc: act %v, %d visible", act, len(s.visible(in)))
	}
	if _, act, _ = s.Update(keyMsg("esc"), in); act != incBack {
		t.Fatalf("second esc = %v", act)
	}
}

func TestIncidentsNamesAMonitorThatIsGone(t *testing.T) {
	in := state.Apply(state.Instance{}, kuma.Heartbeat{Beat: kuma.Beat{
		MonitorID: 9, Status: kuma.StatusDown, Important: true, Msg: "gone", Time: tBase,
	}}, tBase)
	out := ansi.Strip(newIncidentsScreen().View("home", in, 80, 24))
	if !strings.Contains(out, "monitor 9") {
		t.Errorf("view:\n%s", out)
	}
}

func TestIncidentsEmpty(t *testing.T) {
	out := ansi.Strip(newIncidentsScreen().View("home", state.Instance{}, 80, 24))
	if !strings.Contains(out, "no state changes recorded yet") {
		t.Errorf("view:\n%s", out)
	}
}
