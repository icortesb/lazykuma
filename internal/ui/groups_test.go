package ui

import (
	"strings"
	"testing"

	"github.com/icortesb/lazykuma/internal/kuma"
	"github.com/icortesb/lazykuma/internal/state"
)

// grouped is an instance with the Shop group (web, api) and a loose monitor.
func grouped() state.Instance {
	in := withMonitors(map[int]kuma.Monitor{
		1: {ID: 1, Name: "Shop", Type: "group", Active: true},
		2: {ID: 2, Name: "web", Type: "http", URL: "https://shop.example.com", Parent: 1, Active: true},
		3: {ID: 3, Name: "api", Type: "http", URL: "https://api.example.com", Parent: 1, Active: true},
		9: {ID: 9, Name: "status", Type: "http", URL: "https://status.example.com", Active: true},
	})
	return in
}

func TestCreateAGroup(t *testing.T) {
	h := onInstance(t, grouped())
	h.press("g")
	if !strings.Contains(h.view(), "New group") {
		t.Fatalf("no group form:\n%s", h.view())
	}
	h.typeText("Blog")
	h.press("enter")
	f := h.fakes["home"]
	if !f.Sent(`"type":"group"`) || !f.Sent(`"name":"Blog"`) {
		t.Fatalf("group not sent: %v", f.Frames())
	}
	if !strings.Contains(h.view(), "created group Blog") {
		t.Errorf("no flash:\n%s", h.view())
	}
}

func TestRenameAGroup(t *testing.T) {
	h := onInstance(t, grouped())
	h.press("e") // Shop is first
	if !strings.Contains(h.view(), "Rename group") {
		t.Fatalf("no rename form:\n%s", h.view())
	}
	h.m.nform.fields[0].SetValue("Store")
	h.press("enter")
	f := h.fakes["home"]
	if !f.Sent(`"editMonitor"`) || !f.Sent(`"name":"Store"`) {
		t.Fatalf("rename not sent: %v", f.Frames())
	}
}

func TestMoveAMonitorIntoAGroup(t *testing.T) {
	h := onInstance(t, grouped())
	h.press("j", "j", "j") // Shop, api, web, status: the loose one
	if m, _ := h.m.inst.selected(h.m.current().st); m.Name != "status" {
		t.Fatalf("selected %q", m.Name)
	}
	h.press("v")
	v := h.view()
	if !strings.Contains(v, "Move status") || !strings.Contains(v, "No group") || !strings.Contains(v, "Shop") {
		t.Fatalf("no group picker:\n%s", v)
	}
	h.press("j", "enter") // No group, Shop
	f := h.fakes["home"]
	if !f.Sent(`"editMonitor"`) || !f.Sent(`"parent":1`) {
		t.Fatalf("move not sent: %v", f.Frames())
	}
	if !strings.Contains(h.view(), "moved status") {
		t.Errorf("no flash:\n%s", h.view())
	}
}

func TestAGroupCannotMoveIntoItself(t *testing.T) {
	st := withMonitors(map[int]kuma.Monitor{
		1: {ID: 1, Name: "Shop", Type: "group", Active: true},
		4: {ID: 4, Name: "Shop EU", Type: "group", Parent: 1, Active: true},
		5: {ID: 5, Name: "Blog", Type: "group", Active: true},
	})
	h := onInstance(t, st)
	h.press("j") // rows are Blog, Shop, Shop EU (empty groups tie, so by name): Shop
	h.press("v")
	v := h.view()
	if strings.Contains(v, "Shop EU") || !strings.Contains(v, "Blog") {
		t.Fatalf("Shop may move into itself or its own subgroup:\n%s", v)
	}
}

func TestDeleteAGroupKeepingItsMonitors(t *testing.T) {
	h := onInstance(t, grouped())
	h.press("d")
	v := h.view()
	if !strings.Contains(v, "Delete group Shop") || !strings.Contains(v, "keep its 2 monitors") {
		t.Fatalf("no choice:\n%s", v)
	}
	h.press("enter") // the first option keeps them
	if !h.fakes["home"].Sent(`["deleteMonitor",1,false]`) {
		t.Fatalf("not deleted keeping: %v", h.fakes["home"].Frames())
	}
}

func TestDeleteAGroupWithItsMonitorsAsksAgain(t *testing.T) {
	h := onInstance(t, grouped())
	h.press("d", "j", "enter")
	if !strings.Contains(h.view(), "Delete Shop and its 2 monitors?") {
		t.Fatalf("no second confirmation:\n%s", h.view())
	}
	h.press("y")
	if !h.fakes["home"].Sent(`["deleteMonitor",1,true]`) {
		t.Fatalf("not deleted with monitors: %v", h.fakes["home"].Frames())
	}
}

func TestPauseAGroupAsks(t *testing.T) {
	h := onInstance(t, grouped())
	h.press("p")
	if !strings.Contains(h.view(), "Pause Shop and its 2 monitors?") {
		t.Fatalf("no confirmation:\n%s", h.view())
	}
	h.press("y")
	f := h.fakes["home"]
	for _, frame := range []string{`["pauseMonitor",1]`, `["pauseMonitor",2]`, `["pauseMonitor",3]`} {
		if !f.Sent(frame) {
			t.Fatalf("%s not sent: %v", frame, f.Frames())
		}
	}
	if f.Sent(`["pauseMonitor",9]`) {
		t.Errorf("a monitor outside the group was paused: %v", f.Frames())
	}
}

func TestResumeAGroupAsks(t *testing.T) {
	h := onInstance(t, withMonitors(map[int]kuma.Monitor{
		1: {ID: 1, Name: "Shop", Type: "group"},
		2: {ID: 2, Name: "web", Type: "http", URL: "https://shop.example.com", Parent: 1},
		3: {ID: 3, Name: "api", Type: "http", URL: "https://api.example.com", Parent: 1},
	}))
	h.press("p")
	if !strings.Contains(h.view(), "Resume Shop and its 2 monitors?") {
		t.Fatalf("no confirmation:\n%s", h.view())
	}
	h.press("y")
	f := h.fakes["home"]
	for _, frame := range []string{`["resumeMonitor",1]`, `["resumeMonitor",2]`, `["resumeMonitor",3]`} {
		if !f.Sent(frame) {
			t.Fatalf("%s not sent: %v", frame, f.Frames())
		}
	}
	if !strings.Contains(h.view(), "resumed group Shop") {
		t.Errorf("no flash:\n%s", h.view())
	}
}

func TestSilenceAGroupCoversItsMonitors(t *testing.T) {
	h := onInstance(t, grouped())
	h.press("m")
	if !strings.Contains(h.view(), "Silence Shop") {
		t.Fatalf("no silence form:\n%s", h.view())
	}
	h.press("enter", "enter", "enter")
	if !h.fakes["home"].Sent(`["addMonitorMaintenance",6,[{"id":1},{"id":2},{"id":3}]]`) {
		t.Fatalf("maintenance does not cover the group: %v", h.fakes["home"].Frames())
	}
}

func TestANewMonitorStartsInTheGroupUnderTheCursor(t *testing.T) {
	h := onInstance(t, grouped())
	row, _ := h.m.inst.selectedRow(h.m.current().st)
	if !row.Group || row.ID != 1 {
		t.Fatalf("the cursor is not on Shop: %+v", row)
	}
	h.press("n", "enter") // HTTP
	h.typeText("cart")
	h.press("tab")
	h.typeText("https://cart.example.com")
	if !strings.Contains(h.view(), "(x) Shop") {
		t.Errorf("Shop is not chosen:\n%s", h.view())
	}
	for i := 0; i < 8 && !h.m.mform.inLists(); i++ {
		h.press("tab")
	}
	h.press("enter")
	if !h.fakes["home"].Sent(`"parent":1`) {
		t.Fatalf("the new monitor is not in Shop: %v", h.fakes["home"].Frames())
	}

	// On a monitor inside the group, a new one goes next to it.
	h.press("j")
	if row, _ := h.m.inst.selectedRow(h.m.current().st); row.Group || row.Parent != 1 {
		t.Fatalf("the cursor is not on a monitor in Shop: %+v", row)
	}
	h.press("n", "enter")
	if g := h.m.mform.lists[0]; g.title != listGroup || !g.items[1].on || g.items[0].on {
		t.Errorf("group list = %+v", g)
	}
}

func TestTheMovePickerClosesAsItSends(t *testing.T) {
	h := onInstance(t, grouped())
	h.press("j", "j", "j", "v") // status
	// Something else finishing meanwhile leaves the picker alone.
	h.send(actionDone{name: "home", action: "paused", mon: "web"})
	if h.m.screen != screenPick {
		t.Fatalf("an unrelated result closed the picker: screen %v", h.m.screen)
	}
	h.press("j") // Shop
	next, cmd := h.m.Update(keyMsg("enter"))
	h.m = next.(Model)
	if h.m.screen != screenInstance || cmd == nil {
		t.Fatalf("the picker did not close as it sent: screen %v", h.m.screen)
	}
	h.run(cmd)
	h.press("enter") // on the instance now: nothing to send again
	moves := 0
	for _, fr := range h.fakes["home"].Frames() {
		if strings.Contains(fr, `"editMonitor"`) {
			moves++
		}
	}
	if moves != 1 {
		t.Fatalf("%d moves sent: %v", moves, h.fakes["home"].Frames())
	}
}

func TestANewRawMonitorStartsInTheGroupUnderTheCursor(t *testing.T) {
	h := onInstance(t, grouped())             // on Shop
	h.press("n", "j", "j", "j", "j", "enter") // Other type…
	h.press("enter")                          // the first type
	if h.m.screen != screenRaw || h.m.raw.vals["parent"] != "1" {
		t.Fatalf("raw editor parent = %q on screen %v", h.m.raw.vals["parent"], h.m.screen)
	}
}
