package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/icortesb/lazykuma/internal/kuma"
	"github.com/icortesb/lazykuma/internal/state"
)

func typeInSilence(s silenceForm, text string) silenceForm {
	for _, r := range text {
		s, _, _ = s.Update(keyMsg(string(r)))
	}
	return s
}

func TestSilenceNowUntilEnded(t *testing.T) {
	s := newSilenceForm(state.Monitor{Monitor: kuma.Monitor{ID: 1, Name: "nextcloud"}})
	title, start, end, err := s.Values()
	if err != nil {
		t.Fatal(err)
	}
	// Both times empty: Kuma's manual strategy, which core.Silence sends.
	if title != "maintenance: nextcloud" || !start.IsZero() || !end.IsZero() {
		t.Fatalf("values = %q %v %v", title, start, end)
	}
	if !strings.Contains(ansi.Strip(s.View()), "until you end it") {
		t.Error("the form does not explain the empty times")
	}
}

func TestSilenceWindow(t *testing.T) {
	s := newSilenceForm(state.Monitor{Monitor: kuma.Monitor{ID: 1, Name: "nextcloud"}})
	s, _, _ = s.Update(keyMsg("tab"))
	s = typeInSilence(s, "2026-09-12 15:00")
	s, _, _ = s.Update(keyMsg("tab"))
	s = typeInSilence(s, "2026-09-12 17:30")

	_, start, end, err := s.Values()
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 9, 12, 15, 0, 0, 0, time.Local)
	if !start.Equal(want) || !end.Equal(want.Add(150*time.Minute)) {
		t.Fatalf("window = %v → %v", start, end)
	}
	// The times are read in the machine's own timezone, which is what the
	// core then tells Kuma.
	if start.Location() != time.Local {
		t.Errorf("location = %v", start.Location())
	}
}

func TestSilenceFormRejectsBadWindows(t *testing.T) {
	tests := []struct{ from, to, want string }{
		{"2026-09-12 15:00", "", "both ends"},
		{"", "2026-09-12 15:00", "both ends"},
		{"yesterday", "2026-09-12 15:00", "not a time"},
		{"2026-09-12 17:00", "2026-09-12 15:00", "ends before it starts"},
	}
	for _, tt := range tests {
		s := newSilenceForm(state.Monitor{Monitor: kuma.Monitor{ID: 1, Name: "web"}})
		s, _, _ = s.Update(keyMsg("tab"))
		s = typeInSilence(s, tt.from)
		s, _, _ = s.Update(keyMsg("tab"))
		s = typeInSilence(s, tt.to)
		if _, _, _, err := s.Values(); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("from %q to %q: err = %v, want one about %q", tt.from, tt.to, err, tt.want)
		}
	}

	s := newSilenceForm(state.Monitor{Monitor: kuma.Monitor{ID: 1, Name: "web"}})
	s.fields[0].SetValue("")
	if _, _, _, err := s.Values(); err == nil || !strings.Contains(err.Error(), "title is empty") {
		t.Errorf("empty title = %v", err)
	}
}

func TestMaintenanceScreen(t *testing.T) {
	windows := map[int]kuma.Maintenance{
		1: {ID: 1, Title: "later", Strategy: "single", Status: "scheduled", Start: "2026-09-13 02:00", End: "2026-09-13 04:00"},
		2: {ID: 2, Title: "deploy", Strategy: "manual", Status: "under-maintenance"},
	}
	sorted := sortedMaintenances(windows)
	// What is silencing something right now comes first.
	if sorted[0].ID != 2 {
		t.Fatalf("order = %+v", sorted)
	}

	s := maintenanceScreen{}
	out := ansi.Strip(s.View("home", sorted, 80, 24))
	for _, want := range []string{"home · silenced", "deploy", "under-maintenance", "until ended", "2026-09-13 02:00 → 2026-09-13 04:00", "d delete it"} {
		if !strings.Contains(out, want) {
			t.Errorf("view lacks %q:\n%s", want, out)
		}
	}

	if _, act := s.Update(keyMsg("esc"), sorted); act != mtBack {
		t.Error("esc did not go back")
	}
	s2, act := s.Update(keyMsg("d"), sorted)
	if act != mtEnd {
		t.Fatalf("d = %v", act)
	}
	if got, _ := s2.selected(sorted); got.ID != 2 {
		t.Errorf("selected = %+v", got)
	}
	if _, act := (maintenanceScreen{}).Update(keyMsg("d"), nil); act != mtNone {
		t.Error("d on an empty list did something")
	}
	if !strings.Contains(ansi.Strip(s.View("home", nil, 80, 24)), "nothing is silenced") {
		t.Error("the empty list does not say what to do")
	}
}

func TestSilencedWindowShowsLocalTime(t *testing.T) {
	// Kuma returns a window in its own timezone; the list shows it in the
	// machine's, the way the silence form asked for it.
	utc := time.Date(2026, 9, 12, 18, 0, 0, 0, time.UTC)
	want := utc.Local().Format(silenceLayout)
	if got := localTime("2026-09-12 18:00:00", "UTC"); got != want {
		t.Fatalf("localTime = %q, want %q", got, want)
	}
	// A zone Go does not know, or none: show what Kuma said rather than guess.
	if got := localTime("2026-09-12 18:00:00", "Mars/Olympus"); got != "2026-09-12 18:00:00" {
		t.Errorf("unknown zone = %q", got)
	}
	if got := localTime("2026-09-12 18:00:00", ""); got != "2026-09-12 18:00:00" {
		t.Errorf("no zone = %q", got)
	}
}
