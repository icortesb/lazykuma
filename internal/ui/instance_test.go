package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/icortesb/lazykuma/internal/kuma"
	"github.com/icortesb/lazykuma/internal/state"
)

var tBase = time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)

// homelab is an instance with one monitor down, one paused and two up.
func homelab() state.Instance {
	in := state.Apply(state.Instance{}, kuma.Connected{}, tBase)
	in = state.Apply(in, kuma.MonitorList{Monitors: map[int]kuma.Monitor{
		1: {ID: 1, Name: "nextcloud", Type: "http", URL: "https://cloud.home.lan", Active: true},
		2: {ID: 2, Name: "pihole", Type: "ping", Hostname: "10.0.0.2", Active: true},
		3: {ID: 3, Name: "vaultwarden", Type: "http", URL: "https://vault.home.lan", Active: true},
		4: {ID: 4, Name: "backup-s3", Type: "http", URL: "https://s3.home.lan"},
	}}, tBase)
	for i, p := range []float64{40, 44, 42} {
		at := tBase.Add(time.Duration(i) * time.Minute)
		in = state.Apply(in, kuma.Heartbeat{Beat: kuma.Beat{MonitorID: 1, Status: kuma.StatusUp, Ping: p, HasPing: true, Time: at, Msg: "200 - OK"}}, at)
		in = state.Apply(in, kuma.Heartbeat{Beat: kuma.Beat{MonitorID: 2, Status: kuma.StatusUp, Ping: 3, HasPing: true, Time: at, Msg: ""}}, at)
		in = state.Apply(in, kuma.Heartbeat{Beat: kuma.Beat{MonitorID: 3, Status: kuma.StatusDown, Time: at, Msg: "connect ECONNREFUSED"}}, at)
	}
	in = state.Apply(in, kuma.Uptime{MonitorID: 1, Period: "24", Ratio: 0.998}, tBase)
	in = state.Apply(in, kuma.CertInfo{MonitorID: 1, Valid: true, DaysRemaining: 61}, tBase)
	in = state.Apply(in, kuma.AvgPing{MonitorID: 1, Ms: 42, Valid: true}, tBase)
	return in
}

func keyMsg(k string) tea.KeyMsg {
	switch k {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}

func TestInstanceViewWide(t *testing.T) {
	s := newInstanceScreen()
	out := ansi.Strip(s.View("home", homelab(), 120, 30))

	for _, want := range []string{
		"home · 4 monitors · 2 up · 1 down · 1 paused",
		"✖ vaultwarden", "● nextcloud", "‖ backup-s3", "paused",
		"connect ECONNREFUSED", // the down one is selected first
	} {
		if !strings.Contains(out, want) {
			t.Errorf("view lacks %q:\n%s", want, out)
		}
	}
	// Down first: vaultwarden is above nextcloud.
	if strings.Index(out, "vaultwarden") > strings.Index(out, "nextcloud") {
		t.Error("the down monitor is not first")
	}
	assertFits(t, out, 120)
}

func TestInstanceDetail(t *testing.T) {
	s := newInstanceScreen()
	in := homelab()
	s, _, _ = s.Update(keyMsg("j"), in) // vaultwarden → backup-s3 (b < n)
	s, _, _ = s.Update(keyMsg("j"), in) // → nextcloud
	out := ansi.Strip(s.View("home", in, 120, 30))
	for _, want := range []string{"https://cloud.home.lan", "up · 99.8% 24h · cert 61 days", "42ms", "200 - OK"} {
		if !strings.Contains(out, want) {
			t.Errorf("detail lacks %q:\n%s", want, out)
		}
	}
}

func TestInstanceViewNarrowStacks(t *testing.T) {
	out := ansi.Strip(newInstanceScreen().View("home", homelab(), 60, 30))
	assertFits(t, out, 60)
	if !strings.Contains(out, "vaultwarden") || !strings.Contains(out, "ECONNREFUSED") {
		t.Errorf("narrow view:\n%s", out)
	}
}

func TestInstanceFilter(t *testing.T) {
	s := newInstanceScreen()
	in := homelab()
	s, _, _ = s.Update(keyMsg("/"), in)
	for _, r := range "cloud" {
		s, _, _ = s.Update(keyMsg(string(r)), in)
	}
	if got := s.visible(in); len(got) != 1 || got[0].Name != "nextcloud" {
		t.Fatalf("visible = %v", got)
	}
	s, _, _ = s.Update(keyMsg("enter"), in) // keep the filter, back to the list
	if s.filtering {
		t.Fatal("still typing the filter")
	}
	// esc clears the filter before it leaves the screen.
	s, act, _ := s.Update(keyMsg("esc"), in)
	if act != instNone || len(s.visible(in)) != 4 {
		t.Fatalf("esc: action %v, %d visible", act, len(s.visible(in)))
	}
	if _, act, _ = s.Update(keyMsg("esc"), in); act != instBack {
		t.Fatalf("second esc = %v, want back", act)
	}
}

func TestInstanceToggle(t *testing.T) {
	s := newInstanceScreen()
	in := homelab()
	_, act, _ := s.Update(keyMsg("p"), in)
	if act != instToggle {
		t.Fatalf("p = %v", act)
	}
	if m, _ := s.selected(in); m.Name != "vaultwarden" {
		t.Fatalf("selected %q", m.Name)
	}
	if _, act, _ := s.Update(keyMsg("p"), state.Instance{}); act != instNone {
		t.Fatal("toggle with no monitors")
	}
}

func TestInstanceStaleNotice(t *testing.T) {
	in := state.Apply(homelab(), kuma.Disconnected{}, tBase.Add(time.Hour))
	out := ansi.Strip(newInstanceScreen().View("home", in, 120, 30))
	want := "stale since " + in.StaleSince.Local().Format("15:04") + " · down"
	if !strings.Contains(out, want) {
		t.Fatalf("no %q in:\n%s", want, out)
	}
}

// assertFits fails when a line of out is wider than width.
func assertFits(t *testing.T, out string, width int) {
	t.Helper()
	for i, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w > width {
			t.Errorf("line %d is %d wide, over %d: %q", i, w, width, line)
		}
	}
}
