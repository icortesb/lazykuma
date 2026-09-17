package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/icortesb/lazykuma/internal/kuma"
	"github.com/icortesb/lazykuma/internal/state"
)

// detailed is one monitor with uptimes, a cert, pings and a tag.
func detailed() state.Instance {
	in := withMonitors(map[int]kuma.Monitor{
		1: {ID: 1, Name: "Shop", Type: "group", Active: true},
		2: {ID: 2, Name: "web", Type: "http", URL: "https://shop.example.com", Parent: 1, Active: true, Interval: 60,
			Tags: []kuma.Tag{{ID: 4, Name: "prod", Color: "#DC2626"}}},
	})
	for i, p := range []float64{40, 120, 80} {
		at := tBase.Add(time.Duration(i) * time.Minute)
		in = state.Apply(in, kuma.Heartbeat{Beat: kuma.Beat{MonitorID: 2, Status: kuma.StatusUp, Ping: p, HasPing: true, Time: at, Msg: "200 - OK"}}, at)
	}
	for period, r := range map[string]float64{"24": 1, "720": 0.998, "1y": 0.9991} {
		in = state.Apply(in, kuma.Uptime{MonitorID: 2, Period: period, Ratio: r}, tBase)
	}
	in = state.Apply(in, kuma.CertInfo{MonitorID: 2, Valid: true, DaysRemaining: 62}, tBase)
	in = state.Apply(in, kuma.AvgPing{MonitorID: 2, Ms: 128, Valid: true}, tBase)
	return in
}

// toDetail opens the detail of web, the only monitor under Shop.
func toDetail(t *testing.T) *harness {
	t.Helper()
	h := onInstance(t, detailed())
	h.press("j", "enter") // Shop, web
	if h.m.screen != screenDetail {
		t.Fatalf("enter did not open the detail:\n%s", h.view())
	}
	return h
}

func TestDetailHeaderChartAndEvents(t *testing.T) {
	h := toDetail(t)
	v := h.view()
	for _, want := range []string{
		"web", "https://shop.example.com", "http", "every 60s", "Shop", "prod",
		"24h 100%", "30d 99.8%", "1y 99.9%", "cert 62 days", "avg 128ms",
		"ping · 24h", "max 410ms",
		"events", "connect ETIMEDOUT",
	} {
		if !strings.Contains(v, want) {
			t.Errorf("detail lacks %q:\n%s", want, v)
		}
	}
	f := h.fakes["home"]
	if !f.Sent(`["getMonitorChartData",2,24]`) || !f.Sent(`["monitorImportantHeartbeatListPaged",2,0,25]`) {
		t.Errorf("fetches: %v", f.Frames())
	}
	assertFits(t, v, 120)
}

func TestDetailPeriodSwitch(t *testing.T) {
	h := toDetail(t)
	h.send(tea.KeyMsg{Type: tea.KeyRight})
	if !h.fakes["home"].Sent(`["getMonitorChartData",2,168]`) || !strings.Contains(h.view(), "ping · 7d") {
		t.Fatalf("right did not switch to 7d:\n%s", h.view())
	}
	h.press("h", "h") // back to 24h, then 6h
	if !h.fakes["home"].Sent(`["getMonitorChartData",2,6]`) || !strings.Contains(h.view(), "ping · 6h") {
		t.Fatalf("h did not step back:\n%s", h.view())
	}
}

func TestDetailPagesEventsAsYouScroll(t *testing.T) {
	h := toDetail(t)
	if h.fakes["home"].Sent(`["monitorImportantHeartbeatListPaged",2,25,25]`) {
		t.Fatal("second page asked before scrolling")
	}
	for i := 0; i < 24; i++ {
		h.press("j")
	}
	if !h.fakes["home"].Sent(`["monitorImportantHeartbeatListPaged",2,25,25]`) {
		t.Fatalf("second page not asked near the end: %v", h.fakes["home"].Frames())
	}
	// 30 in all: a short second page means no third.
	for i := 0; i < 10; i++ {
		h.press("j")
	}
	if h.fakes["home"].Sent(`["monitorImportantHeartbeatListPaged",2,50,25]`) {
		t.Error("asked past the last page")
	}
}

func TestDetailShowsLiveStateChanges(t *testing.T) {
	h := toDetail(t)
	later := time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC) // newer than every fetched one
	st := state.Apply(h.m.current().st, kuma.Heartbeat{Beat: kuma.Beat{
		MonitorID: 2, Status: kuma.StatusDown, Important: true, Msg: "certificate has expired", Time: later,
	}}, later)
	h.state("home", st)
	v := h.view()
	if !strings.Contains(v, "certificate has expired") {
		t.Fatalf("live state change missing:\n%s", v)
	}
	if strings.Index(v, "certificate has expired") > strings.Index(v, "connect ETIMEDOUT") {
		t.Error("the live change is not first")
	}
}

func TestDetailActions(t *testing.T) {
	h := toDetail(t)
	h.press("p")
	if !h.fakes["home"].Sent(`["pauseMonitor",2]`) {
		t.Fatalf("p did not pause: %v", h.fakes["home"].Frames())
	}
	h.press("e")
	if h.m.screen != screenMonitor {
		t.Fatalf("e did not open the form:\n%s", h.view())
	}
	h.press("esc")
	if h.m.screen != screenDetail {
		t.Fatalf("esc from the form did not return to the detail: %v", h.m.screen)
	}
	h.press("x")
	if v := h.view(); !strings.Contains(v, "Clear the events of web?") || !strings.Contains(v, "removes its past state changes") {
		t.Fatalf("no clear-events question:\n%s", v)
	}
	h.press("y")
	if !h.fakes["home"].Sent(`["clearEvents",2]`) || h.m.screen != screenDetail {
		t.Fatalf("clear events: screen %v, frames %v", h.m.screen, h.fakes["home"].Frames())
	}
	h.press("X")
	if v := h.view(); !strings.Contains(v, "Clear the history of web?") || !strings.Contains(v, "deletes all of its beats, state changes and uptime") {
		t.Fatalf("no clear-history question:\n%s", v)
	}
	h.press("y")
	if !h.fakes["home"].Sent(`["clearHeartbeats",2]`) {
		t.Fatalf("clear history not sent: %v", h.fakes["home"].Frames())
	}
	h.press("esc")
	if h.m.screen != screenInstance {
		t.Fatalf("esc did not return to the list: %v", h.m.screen)
	}
}

func TestDetailLeavesAMonitorThatIsGone(t *testing.T) {
	h := toDetail(t)
	st := state.Apply(h.m.current().st, kuma.MonitorDeleted{ID: 2}, tBase)
	h.state("home", st)
	if h.m.screen != screenInstance {
		t.Fatalf("still on the detail of a deleted monitor: %v\n%s", h.m.screen, h.view())
	}
}

func TestDetailIgnoresAnswersForAnotherMonitor(t *testing.T) {
	h := toDetail(t)
	h.send(chartLoaded{instance: "home", monitorID: 99, hours: 24, points: []kuma.ChartPoint{{Time: tBase, Up: 1, AvgPing: 999, MaxPing: 999}}})
	if strings.Contains(h.view(), "999ms") {
		t.Fatal("a chart for another monitor was shown")
	}
}

func TestEnterOnAGroupStillFolds(t *testing.T) {
	h := onInstance(t, detailed())
	h.press("enter") // Shop
	if h.m.screen != screenInstance || !strings.Contains(h.view(), "▸ Shop") {
		t.Fatalf("enter on a group:\n%s", h.view())
	}
}

// The app cuts every line to the terminal, which would hide a detail that
// draws too wide; the screen is measured on its own.
func TestDetailFitsWithOrWithoutHistory(t *testing.T) {
	st := detailed()
	st = state.Apply(st, kuma.Heartbeat{Beat: kuma.Beat{
		MonitorID: 2, Status: kuma.StatusDown, Important: true, Time: tBase.Add(time.Hour),
		Msg: strings.Repeat("a long reason from the far side ", 8),
	}}, tBase.Add(time.Hour))
	loading := newDetailScreen(2, detailed()) // opened before the long reason came in
	loaded := loading.withChart(chartLoaded{monitorID: 2, hours: 24, points: []kuma.ChartPoint{
		{Time: tBase, Up: 3, AvgPing: 40, MinPing: 30, MaxPing: 50},
		{Time: tBase.Add(time.Minute), Down: 2},
	}})
	loaded = loaded.withEvents(eventsLoaded{monitorID: 2, beats: []kuma.Beat{{MonitorID: 2, Status: kuma.StatusUp, Time: tBase, Msg: "200 - OK"}}})
	empty := loading.withChart(chartLoaded{monitorID: 2, hours: 24})
	empty = empty.withEvents(eventsLoaded{monitorID: 2})
	for name, d := range map[string]detailScreen{"loading": loading, "loaded": loaded, "empty": empty} {
		for _, width := range []int{120, 60, 30, 12} {
			assertFits(t, d.View(st, width, 30), width)
			assertFits(t, d.View(st, width, 3), width)
		}
		// Keys on a screen with nothing fetched must not trip over the
		// empty lists.
		for _, k := range []string{"j", "k", "l", "h"} {
			d, _, _ = d.Update(keyMsg(k), withMonitors(nil))
		}
		if v := d.View(withMonitors(nil), 80, 20); !strings.Contains(v, "gone") {
			t.Errorf("%s: a deleted monitor's detail:\n%s", name, v)
		}
	}
	if v := empty.View(withMonitors(map[int]kuma.Monitor{2: {ID: 2, Name: "web", Active: true}}), 80, 20); !strings.Contains(v, "no checks in this period") || !strings.Contains(v, "no state changes yet") {
		t.Errorf("empty detail:\n%s", v)
	}
}

// An answer for a period left behind, or for a page not next, is late: it
// must not take the place of what is on screen.
func TestDetailIgnoresLateAnswers(t *testing.T) {
	h := toDetail(t)
	h.send(chartLoaded{instance: "home", monitorID: 2, hours: 168, points: []kuma.ChartPoint{{Time: tBase, Up: 1, AvgPing: 999, MaxPing: 999}}})
	h.send(eventsLoaded{instance: "home", monitorID: 2, offset: 5, beats: []kuma.Beat{{MonitorID: 2, Time: tBase.Add(48 * time.Hour), Msg: "a stale page"}}})
	h.send(eventsLoaded{instance: "vps", monitorID: 2, offset: 25, beats: []kuma.Beat{{MonitorID: 2, Time: tBase.Add(48 * time.Hour), Msg: "another instance"}}})
	v := h.view()
	for _, late := range []string{"999ms", "a stale page", "another instance"} {
		if strings.Contains(v, late) {
			t.Errorf("late answer %q shown:\n%s", late, v)
		}
	}
}

// A clear changes what Kuma holds: the chart and the events on screen are
// fetched again, for the period the user was looking at.
func TestDetailRefetchesWhatWasCleared(t *testing.T) {
	h := toDetail(t)
	h.press("l", "x", "y") // 7d, then clear its events
	count := func(want string) int {
		n := 0
		for _, fr := range h.fakes["home"].Frames() {
			if strings.Contains(fr, want) {
				n++
			}
		}
		return n
	}
	if got := count(`["monitorImportantHeartbeatListPaged",2,0,25]`); got != 2 {
		t.Errorf("first page asked %d times, want 2", got)
	}
	if got := count(`["getMonitorChartData",2,168]`); got != 2 {
		t.Errorf("7d chart asked %d times, want 2", got)
	}
	if v := h.view(); !strings.Contains(v, "ping · 7d") || !strings.Contains(v, "connect ETIMEDOUT") {
		t.Errorf("after the clear:\n%s", v)
	}
}

// Answers that land while a question opened from the detail is on screen
// still reach it: back on the detail it is not left loading, and paging
// goes on.
func TestDetailTakesAnswersThatLandUnderAQuestion(t *testing.T) {
	h := onInstance(t, detailed())
	h.press("j")
	next, fetch := h.m.Update(keyMsg("enter")) // the fetches are held back
	h.m = next.(Model)
	h.press("x")
	if h.m.screen != screenConfirm {
		t.Fatalf("x did not ask:\n%s", h.view())
	}
	h.run(fetch) // they land under the question
	h.press("n")
	if h.m.screen != screenDetail || h.m.detail.chartLoading || h.m.detail.eventsLoading {
		t.Fatalf("screen %v, chart loading %v, events loading %v", h.m.screen, h.m.detail.chartLoading, h.m.detail.eventsLoading)
	}
	if v := h.view(); !strings.Contains(v, "max 410ms") || !strings.Contains(v, "connect ETIMEDOUT") {
		t.Fatalf("answers lost:\n%s", v)
	}
	for i := 0; i < 24; i++ {
		h.press("j")
	}
	if !h.fakes["home"].Sent(`["monitorImportantHeartbeatListPaged",2,25,25]`) {
		t.Errorf("paging stopped: %v", h.fakes["home"].Frames())
	}
}

// important is a state change the instance saw, as Kuma reports it.
func important(st state.Instance, at time.Time, status kuma.Status, msg string) state.Instance {
	return state.Apply(st, kuma.Heartbeat{Beat: kuma.Beat{MonitorID: 2, Status: status, Important: true, Msg: msg, Time: at}}, at)
}

// The state changes the instance already held when the detail opened are
// Kuma's to list: they show once, from the fetched page, or not at all.
func TestDetailLeavesOlderStateChangesToThePages(t *testing.T) {
	st := detailed()
	st = important(st, time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC), kuma.StatusUp, "200 - OK") // also on the page
	st = important(st, time.Date(2026, 9, 16, 13, 0, 0, 0, time.UTC), kuma.StatusDown, "an outage held in memory")
	h := onInstance(t, st)
	h.press("j", "enter")
	if v := h.view(); strings.Contains(v, "an outage held in memory") {
		t.Errorf("an in-memory change shown:\n%s", v)
	}
	if got := len(h.m.detail.shown(h.m.current().st)); got != eventPage {
		t.Errorf("shown %d, want the %d fetched", got, eventPage)
	}
}

// Clearing the events must not leave the ones the instance keeps in memory
// on screen, as if nothing happened.
func TestDetailClearEventsDropsTheInMemoryOnes(t *testing.T) {
	h := toDetail(t)
	h.state("home", important(h.m.current().st, time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC), kuma.StatusDown, "certificate has expired"))
	if !strings.Contains(h.view(), "certificate has expired") {
		t.Fatalf("live change missing:\n%s", h.view())
	}
	h.press("x", "y")
	if v := h.view(); strings.Contains(v, "certificate has expired") {
		t.Errorf("still shown after the clear:\n%s", v)
	}
}

// A clear of another monitor, finished while this detail is open, leaves
// this one's history alone.
func TestDetailRefetchesOnlyItsOwnClear(t *testing.T) {
	h := toDetail(t)
	before := len(h.fakes["home"].Frames())
	h.send(actionDone{name: "home", action: "cleared the events of", mon: "other", monitorID: 99})
	if after := len(h.fakes["home"].Frames()); after != before {
		t.Errorf("fetched for another monitor's clear: %v", h.fakes["home"].Frames()[before:])
	}
}

func TestDetailHelpReturnsToTheDetail(t *testing.T) {
	h := toDetail(t)
	h.press("?")
	if h.m.screen != screenHelp || !strings.Contains(h.view(), "On a monitor's detail") {
		t.Fatalf("? on the detail:\n%s", h.view())
	}
	h.press("esc")
	if h.m.screen != screenDetail {
		t.Fatalf("esc from help went to %v", h.m.screen)
	}
}

// A page that failed says so, even under the ones that loaded, until a
// later one comes in.
func TestDetailShowsAFailedPage(t *testing.T) {
	st := detailed()
	d := newDetailScreen(2, st).withEvents(eventsLoaded{monitorID: 2, beats: []kuma.Beat{{MonitorID: 2, Time: tBase, Msg: "200 - OK"}}})
	d.eventsDone = false
	d = d.withEvents(eventsLoaded{monitorID: 2, offset: 1, err: errors.New("timeout")})
	if v := d.View(st, 80, 30); !strings.Contains(v, "timeout") || !strings.Contains(v, "200 - OK") {
		t.Errorf("failed page:\n%s", v)
	}
	d = d.withEvents(eventsLoaded{monitorID: 2, offset: 1, beats: []kuma.Beat{{MonitorID: 2, Time: tBase.Add(-time.Hour), Msg: "connect ETIMEDOUT"}}})
	if v := d.View(st, 80, 30); strings.Contains(v, "timeout") {
		t.Errorf("error kept after a page loaded:\n%s", v)
	}
}
