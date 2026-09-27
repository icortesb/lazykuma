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
	for i := 0; i < 16 && !(h.m.mform.inLists() && h.m.mform.lists[h.m.mform.list].title == listChannels); i++ {
		h.press("tab")
	}
	if !(h.m.mform.inLists() && h.m.mform.lists[h.m.mform.list].title == listChannels) {
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
	h.toChannels() // past interval, retries and accept
	// The default channel comes ticked, as Kuma's own form does.
	if !h.m.mform.channelItems()[0].on {
		t.Fatal("the default channel is not ticked")
	}
	h.press("enter") // save

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

func TestUntickingAChannelSticks(t *testing.T) {
	h := onInstance(t, twoMonitors())
	h.press("n")
	h.press("enter") // HTTP
	h.typeText("no-alerts")
	h.press("tab")
	h.typeText("https://x.home.lan")
	h.toChannels()
	h.send(tea.KeyMsg{Type: tea.KeySpace}) // untick the default
	h.press("enter")

	if !h.fakes["home"].Sent(`"notificationIDList":{}`) {
		t.Fatalf("channel not unticked: %v", h.fakes["home"].Frames())
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
	if !strings.Contains(h.view(), "Delete the maintenance") {
		t.Fatalf("no confirmation:\n%s", h.view())
	}
	h.press("y")
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

func TestALateMonitorFromAnotherInstanceIsIgnored(t *testing.T) {
	h := onInstance(t, twoMonitors())
	// The answer to a fetch the user started on another instance arrives
	// after they moved here: opening it would edit the wrong instance.
	h.send(monitorLoaded{instance: "elsewhere", from: screenInstance, mon: kuma.RawMonitor{"id": float64(1), "type": "http", "name": "x"}})
	if h.m.screen != screenInstance {
		t.Fatalf("a foreign monitor opened screen %v", h.m.screen)
	}
}

func TestCloneAMonitorThroughTheUI(t *testing.T) {
	h := onInstance(t, twoMonitors())
	h.press("C") // nextcloud
	if v := h.view(); !strings.Contains(v, "Clone of nextcloud") || !strings.Contains(v, "copy of nextcloud") {
		t.Fatalf("no clone form:\n%s", v)
	}
	h.toChannels()
	h.press("enter")
	f := h.fakes["home"]
	if !f.Sent(`["add",`) || !f.Sent(`"name":"copy of nextcloud"`) {
		t.Fatalf("clone not sent: %v", f.Frames())
	}
	for _, fr := range f.Frames() {
		if strings.Contains(fr, `["add",`) && strings.Contains(fr, `"id":`) {
			t.Errorf("clone sent an id: %s", fr)
		}
	}
}

func TestCreateAMonitorWithATag(t *testing.T) {
	h := onInstance(t, twoMonitors())
	h.press("n", "enter") // HTTP
	h.typeText("tagged")
	h.press("tab")
	h.typeText("https://tagged.example.com")
	// Walk to the tag list and tick region.
	for i := 0; i < 16 && !(h.m.mform.inLists() && h.m.mform.lists[h.m.mform.list].title == listTags); i++ {
		h.press("tab")
	}
	h.send(tea.KeyMsg{Type: tea.KeySpace})
	h.toChannels()
	h.press("enter")
	if !h.fakes["home"].Sent(`["addMonitorTag",4,9,""]`) {
		t.Fatalf("tag not applied to the new monitor 9: %v", h.fakes["home"].Frames())
	}
}

func TestATagFailureAfterCreateClosesTheForm(t *testing.T) {
	h := onInstance(t, twoMonitors())
	h.m.tagDefs["home"] = []kuma.TagDef{{ID: 13, Name: "broken", Color: "#DC2626"}}
	h.press("n", "enter") // HTTP
	h.typeText("half")
	h.press("tab")
	h.typeText("https://half.example.com")
	for i := 0; i < 16 && !(h.m.mform.inLists() && h.m.mform.lists[h.m.mform.list].title == listTags); i++ {
		h.press("tab")
	}
	h.send(tea.KeyMsg{Type: tea.KeySpace})
	h.toChannels()
	h.press("enter")

	if !h.fakes["home"].Sent(`["add",`) {
		t.Fatalf("monitor not sent: %v", h.fakes["home"].Frames())
	}
	// The monitor exists: staying on the form would invite a second one.
	if h.m.screen != screenInstance {
		t.Errorf("the form stayed open after the monitor was created: screen %v", h.m.screen)
	}
	if v := h.view(); !strings.Contains(v, "created, but tag broken") || !strings.Contains(v, "tag gone") {
		t.Errorf("the flash does not say the monitor was created:\n%s", v)
	}
}

func TestCloneAnUncuratedMonitorKeepsItsTags(t *testing.T) {
	h := onInstance(t, withMonitors(map[int]kuma.Monitor{
		21: {ID: 21, Name: "resolver", Type: "dns", Hostname: "home.lan", Active: true},
	}))
	h.press("C")
	if h.m.screen != screenRaw || !strings.Contains(h.view(), "copy of resolver") {
		t.Fatalf("no field editor for the clone:\n%s", h.view())
	}
	h.send(tea.KeyMsg{Type: tea.KeyCtrlS})
	f := h.fakes["home"]
	added, tagged := -1, map[string]int{}
	for i, fr := range f.Frames() {
		switch {
		case strings.Contains(fr, `["add",`):
			added = i
		case strings.Contains(fr, `["addMonitorTag",4,9,"eu"]`):
			tagged["region"] = i
		case strings.Contains(fr, `["addMonitorTag",6,9,""]`):
			tagged["dns"] = i
		}
	}
	if added < 0 || len(tagged) != 2 || tagged["region"] < added || tagged["dns"] < added {
		t.Fatalf("the clone's tags did not follow its add: %v", f.Frames())
	}

	// A new monitor typed in the field editor afterwards carries none of them.
	h.press("n", "j", "j", "j", "j", "enter", "enter")
	if h.m.screen != screenRaw || len(h.m.raw.cloneTags) != 0 {
		t.Fatalf("a later field editor kept the clone's tags: %+v", h.m.raw.cloneTags)
	}
}

func TestAReorderKeepsKeysOnTheHighlightedMonitor(t *testing.T) {
	st := func(down int, pings map[int]float64) state.Instance {
		in := withMonitors(map[int]kuma.Monitor{
			1: {ID: 1, Name: "alpha", Type: "http", URL: "https://alpha.example.com", Active: true},
			2: {ID: 2, Name: "beta", Type: "http", URL: "https://beta.example.com", Active: true},
		})
		for id, p := range pings {
			status := kuma.StatusUp
			if id == down {
				status = kuma.StatusDown
			}
			in = state.Apply(in, kuma.Heartbeat{Beat: kuma.Beat{MonitorID: id, Status: status, Ping: p, HasPing: true, Time: tBase}}, tBase)
		}
		return in
	}
	h := onInstance(t, st(0, map[int]float64{1: 50, 2: 100}))
	// Nothing chosen yet: alpha is on top by name. beta goes down and
	// takes the top row between the frame and the key.
	h.state("home", st(2, map[int]float64{1: 50, 2: 100}))
	h.press("p")
	f := h.fakes["home"]
	if !f.Sent(`["pauseMonitor",1]`) || f.Sent(`["pauseMonitor",2]`) {
		t.Fatalf("p did not act on alpha: %v", f.Frames())
	}

	// By ping, with beta chosen: alpha slows down past it.
	h.state("home", st(0, map[int]float64{1: 50, 2: 100}))
	h.press("s", "s") // status → name → ping
	h.press("k")      // beta, the slowest
	if row, _ := h.m.inst.selectedRow(h.m.current().st); row.Name != "beta" {
		t.Fatalf("highlighted %q", row.Name)
	}
	h.state("home", st(0, map[int]float64{1: 200, 2: 100}))
	if row, _ := h.m.inst.selectedRow(h.m.current().st); row.Name != "beta" {
		t.Fatalf("after the reorder the cursor is on %q", row.Name)
	}
	h.press("p")
	if !f.Sent(`["pauseMonitor",2]`) {
		t.Fatalf("p did not act on beta: %v", f.Frames())
	}
}

func TestATagFailureAfterARawCloneClosesTheEditor(t *testing.T) {
	h := onInstance(t, withMonitors(map[int]kuma.Monitor{
		21: {ID: 21, Name: "resolver", Type: "dns", Hostname: "home.lan", Active: true},
	}))
	h.press("C")
	h.m.raw.cloneTags = []kuma.Tag{{ID: 13, Name: "broken"}}
	h.send(tea.KeyMsg{Type: tea.KeyCtrlS})
	// The clone exists: staying in the editor would invite a second one.
	if h.m.screen != screenInstance {
		t.Fatalf("the editor stayed open after the clone was created: screen %v", h.m.screen)
	}
	if v := h.view(); !strings.Contains(v, "created, but tag broken") {
		t.Errorf("the flash does not say the clone was created:\n%s", v)
	}
}

func TestAFormStaysOpenWhenAnotherWriteLands(t *testing.T) {
	h := onInstance(t, twoMonitors())
	h.press("n", "enter")
	if h.m.screen != screenMonitor {
		t.Fatalf("no monitor form: %v", h.m.screen)
	}
	h.typeText("shop")
	// A pause sent from the list before the form opened, and a create that
	// saved the monitor but not its tags: neither is this form's.
	h.send(actionDone{name: "home", action: "paused", mon: "web"})
	h.send(actionDone{name: "home", action: "created", mon: "api", saved: true, err: errors.New("tag gone")})
	if h.m.screen != screenMonitor || !strings.Contains(h.view(), "shop") {
		t.Fatalf("another write's answer closed the form being filled: %v\n%s", h.m.screen, h.view())
	}
	h.send(actionDone{name: "home", action: "created", mon: "shop", saved: true, err: errors.New("tag gone"), from: screenMonitor})
	if h.m.screen != screenInstance {
		t.Errorf("the form's own half-saved write left it open: %v", h.m.screen)
	}
}
