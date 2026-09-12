package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/icortesb/lazykuma/internal/kuma"
	"github.com/icortesb/lazykuma/internal/state"
)

// onInstance opens a connected instance carrying the given state, ready for
// the keys the instance screen offers.
func onInstance(t *testing.T, st state.Instance) *harness {
	t.Helper()
	h := newHarness(t, []string{"home"}, "home")
	h.connected("home")
	h.state("home", st)
	h.press("enter")
	return h
}

// toChannels tabs through the rest of the monitor form, to the channel
// list that sits after the fields.
func (h *harness) toChannels() {
	h.t.Helper()
	for i := 0; i < 12 && !h.m.mform.onChans; i++ {
		h.press("tab")
	}
	if !h.m.mform.onChans {
		h.t.Fatal("never reached the channel list")
	}
}

// twoMonitors is an instance with a channel and two monitors.
func twoMonitors() state.Instance {
	in := withMonitors(map[int]kuma.Monitor{
		1: {ID: 1, Name: "nextcloud", Type: "http", URL: "https://cloud.lan", Active: true},
		2: {ID: 2, Name: "vaultwarden", Type: "http", URL: "https://vault.lan", Active: true},
	})
	in = state.Apply(in, kuma.NotificationList{Notifications: []kuma.Notification{
		{ID: 5, Name: "telegram", Type: "telegram", IsDefault: true, Config: map[string]any{"type": "telegram", "name": "telegram"}},
	}}, tBase)
	in = state.Apply(in, kuma.MonitorTypes{Types: []string{"dns", "port"}}, tBase)
	return in
}

func TestCreateAMonitorThroughTheUI(t *testing.T) {
	h := onInstance(t, twoMonitors())
	h.press("n")
	if !strings.Contains(h.view(), "what should it watch?") {
		t.Fatalf("no type picker:\n%s", h.view())
	}
	h.press("enter") // HTTP, the first type
	h.typeText("pihole")
	h.press("tab")
	h.typeText("https://pi.home.lan")
	h.toChannels()                         // past interval, retries and accept
	h.send(tea.KeyMsg{Type: tea.KeySpace}) // tick telegram
	h.press("enter")                       // save

	f := h.fakes["home"]
	if !f.Sent(`"name":"pihole"`) || !f.Sent(`"url":"https://pi.home.lan"`) {
		t.Fatalf("monitor not sent: %v", f.Frames())
	}
	if !f.Sent(`"notificationIDList":{"5":true}`) {
		t.Errorf("channel not ticked: %v", f.Frames())
	}
	if !strings.Contains(h.view(), "created pihole") {
		t.Errorf("no flash:\n%s", h.view())
	}
	// The form closes and the instance is back on screen.
	if !strings.Contains(h.view(), "home · 2 monitors") {
		t.Errorf("did not return to the instance:\n%s", h.view())
	}
}

func TestEditAMonitorKeepsWhatTheFormDoesNotShow(t *testing.T) {
	h := onInstance(t, twoMonitors())
	h.press("e") // nextcloud is first
	if !strings.Contains(h.view(), "Edit nextcloud") {
		t.Fatalf("not editing:\n%s", h.view())
	}
	// The fake returns a monitor with fields the form never shows.
	h.m.mform.fields[0].SetValue("nextcloud-2")
	h.toChannels()
	h.press("enter")

	f := h.fakes["home"]
	if !f.Sent(`"editMonitor"`) {
		t.Fatalf("no edit sent: %v", f.Frames())
	}
	if !f.Sent(`"id":1`) {
		t.Errorf("edit without the id: %v", f.Frames())
	}
}

func TestDeleteAMonitorAsks(t *testing.T) {
	h := onInstance(t, twoMonitors())
	h.press("d")
	if !strings.Contains(h.view(), `Delete "nextcloud"?`) {
		t.Fatalf("no confirmation:\n%s", h.view())
	}
	h.press("n") // no
	if h.fakes["home"].Sent(`"deleteMonitor"`) {
		t.Fatal("deleted anyway")
	}
	h.press("d", "y")
	if !h.fakes["home"].Sent(`["deleteMonitor",1]`) {
		t.Fatalf("not deleted: %v", h.fakes["home"].Frames())
	}
	if !strings.Contains(h.view(), "deleted nextcloud") {
		t.Errorf("no flash:\n%s", h.view())
	}
}

func TestOtherMonitorTypeGoesToTheFieldEditor(t *testing.T) {
	h := onInstance(t, twoMonitors())
	h.press("n")
	h.press("j", "j", "j", "j", "enter") // past the four curated types
	if !strings.Contains(h.view(), "Monitor type") {
		t.Fatalf("no type list:\n%s", h.view())
	}
	h.press("enter") // dns, the instance's first reported type
	v := h.view()
	if !strings.Contains(v, "Fields of the new monitor") || !strings.Contains(v, "values are JSON") {
		t.Fatalf("not in the field editor:\n%s", v)
	}
	if !strings.Contains(v, "dns") {
		t.Errorf("the type is not filled in:\n%s", v)
	}
}

func TestChannelsFromTheInstance(t *testing.T) {
	h := onInstance(t, twoMonitors())
	h.press("c")
	if !strings.Contains(h.view(), "home · channels") {
		t.Fatalf("not on the channels:\n%s", h.view())
	}
	// Test the channel Kuma has: the provider's complaint comes back.
	h.press("t")
	if !strings.Contains(h.view(), "401") {
		t.Errorf("the test result is not shown:\n%s", h.view())
	}

	// A new Telegram channel.
	h.press("n")
	h.press("enter") // Telegram
	h.typeText("alerts")
	h.press("tab")
	h.typeText("123:abc")
	h.press("tab")
	h.typeText("42")
	h.press("enter", "enter", "enter")
	f := h.fakes["home"]
	if !f.Sent(`"telegramBotToken":"123:abc"`) || !f.Sent(`"name":"alerts"`) {
		t.Fatalf("channel not sent: %v", f.Frames())
	}
	if !strings.Contains(h.view(), "created channel alerts") {
		t.Errorf("no flash:\n%s", h.view())
	}
}

func TestSilenceAMonitor(t *testing.T) {
	h := onInstance(t, twoMonitors())
	h.press("m")
	if !strings.Contains(h.view(), "Silence nextcloud") {
		t.Fatalf("no silence form:\n%s", h.view())
	}
	h.press("enter", "enter", "enter") // title as offered, both times empty

	f := h.fakes["home"]
	if !f.Sent(`"strategy":"manual"`) || !f.Sent(`["addMonitorMaintenance",6,[{"id":1}]]`) {
		t.Fatalf("maintenance not sent: %v", f.Frames())
	}
	if !strings.Contains(h.view(), "silenced nextcloud") {
		t.Errorf("no flash:\n%s", h.view())
	}
}

func TestSilencedListEndsAWindow(t *testing.T) {
	st := state.Apply(twoMonitors(), kuma.MaintenanceList{Maintenances: map[int]kuma.Maintenance{
		6: {ID: 6, Title: "deploy", Strategy: "manual", Status: "under-maintenance", Active: true},
	}}, tBase)
	h := onInstance(t, st)
	h.press("M")
	if !strings.Contains(h.view(), "home · silenced") {
		t.Fatalf("no silenced list:\n%s", h.view())
	}
	h.press("d")
	if !h.fakes["home"].Sent(`["deleteMaintenance",6]`) {
		t.Fatalf("not ended: %v", h.fakes["home"].Frames())
	}
	if !strings.Contains(h.view(), "ended deploy") {
		t.Errorf("no flash:\n%s", h.view())
	}
}

func TestIncidentsFromTheInstance(t *testing.T) {
	st := state.Apply(twoMonitors(), kuma.Heartbeat{Beat: kuma.Beat{
		MonitorID: 2, Status: kuma.StatusDown, Important: true, Msg: "connect ECONNREFUSED",
		Time: tBase.Add(time.Minute),
	}}, tBase)
	h := onInstance(t, st)
	h.press("i")
	v := h.view()
	if !strings.Contains(v, "home · incidents") || !strings.Contains(v, "connect ECONNREFUSED") {
		t.Fatalf("incidents:\n%s", v)
	}
	h.press("esc")
	if !strings.Contains(h.view(), "home · 2 monitors") {
		t.Errorf("esc did not return to the instance:\n%s", h.view())
	}
}

func TestAFailedWriteKeepsTheFormOpen(t *testing.T) {
	h := onInstance(t, twoMonitors())
	h.press("n", "enter") // HTTP
	h.typeText("bad")
	h.press("tab")
	h.typeText("not-a-url")
	h.toChannels()
	h.press("enter")

	v := h.view()
	if !strings.Contains(v, "not an http(s) URL") {
		t.Fatalf("no error shown:\n%s", v)
	}
	if h.fakes["home"].Sent(`"name":"bad"`) {
		t.Error("a monitor with a bad URL reached Kuma")
	}
}
