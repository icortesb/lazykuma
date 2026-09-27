package ui

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/icortesb/lazykuma/internal/kuma"
)

// names lists an editor's sections with their monitors, "A: 1 2", so a test
// can say what it expects in one line.
func names(e sectionsEditor) []string {
	var out []string
	for _, s := range e.sections {
		line := s.Name + ":"
		for _, m := range s.Monitors {
			line += " " + m.Name
		}
		out = append(out, line)
	}
	return out
}

func twoSections() sectionsEditor {
	return sectionsEditor{slug: "shop-status", title: "Shop status", sections: []kuma.PageSection{
		{ID: 4, Name: "A", Monitors: []kuma.PageMonitor{{ID: 1, Name: "m1", SendURL: true, URL: "https://cloud.home.lan"}, {ID: 2, Name: "m2"}}},
		{ID: 5, Name: "B", Monitors: []kuma.PageMonitor{{ID: 3, Name: "m3"}}},
	}}
}

func TestSectionsEditorAddAndRename(t *testing.T) {
	var e sectionsEditor
	e = e.addSection("Services")
	if !reflect.DeepEqual(names(e), []string{"Services:"}) || e.cursor != 0 || !e.dirty {
		t.Fatalf("into an empty editor: %v cursor %d dirty %v", names(e), e.cursor, e.dirty)
	}
	withMon := e.addMonitor(kuma.PageMonitor{ID: 1, Name: "nextcloud"})
	if !reflect.DeepEqual(names(withMon), []string{"Services: nextcloud"}) || withMon.cursor != 1 {
		t.Fatalf("add a monitor: %v cursor %d", names(withMon), withMon.cursor)
	}
	if len(e.sections[0].Monitors) != 0 {
		t.Error("addMonitor changed the editor it was called on")
	}
	withMon.cursor = 0
	two := withMon.addSection("Internal")
	if !reflect.DeepEqual(names(two), []string{"Services: nextcloud", "Internal:"}) || two.cursor != 2 {
		t.Fatalf("after a section: %v cursor %d", names(two), two.cursor)
	}
	two.cursor = 1 // on nextcloud: the new section goes after the section it is in
	three := two.addSection("Edge")
	if !reflect.DeepEqual(names(three), []string{"Services: nextcloud", "Edge:", "Internal:"}) || three.cursor != 2 {
		t.Fatalf("after the current monitor's section: %v cursor %d", names(three), three.cursor)
	}
	if len(two.sections) != 2 {
		t.Error("addSection changed the editor it was called on")
	}
	renamed := three.renameSection("Public")
	if !reflect.DeepEqual(names(renamed), []string{"Services: nextcloud", "Public:", "Internal:"}) || renamed.cursor != 2 {
		t.Fatalf("rename: %v cursor %d", names(renamed), renamed.cursor)
	}
	if three.sections[1].Name != "Edge" {
		t.Error("renameSection changed the editor it was called on")
	}
}

func TestSectionsEditorRows(t *testing.T) {
	e := twoSections()
	want := []secRow{{0, -1}, {0, 0}, {0, 1}, {1, -1}, {1, 0}}
	if got := e.rows(); !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %v", got)
	}
}

func TestSectionsEditorMoveMonitor(t *testing.T) {
	e := twoSections()
	e.cursor = 2 // m2, the last of A
	if got := e.moveDown(); !reflect.DeepEqual(names(got), names(e)) || got.cursor != 2 || got.dirty {
		t.Fatalf("moveDown at the end of its section: %v cursor %d dirty %v", names(got), got.cursor, got.dirty)
	}
	up := e.moveUp()
	if !reflect.DeepEqual(names(up), []string{"A: m2 m1", "B: m3"}) || up.cursor != 1 || !up.dirty {
		t.Fatalf("moveUp: %v cursor %d", names(up), up.cursor)
	}
	if up.sections[0].Monitors[1] != (kuma.PageMonitor{ID: 1, Name: "m1", SendURL: true, URL: "https://cloud.home.lan"}) {
		t.Errorf("a moved monitor lost its link: %+v", up.sections[0].Monitors[1])
	}
	if e.sections[0].Monitors[0].ID != 1 {
		t.Error("moveUp changed the editor it was called on")
	}
	if got := up.moveUp(); got.cursor != 1 || got.dirty != up.dirty || !reflect.DeepEqual(names(got), names(up)) {
		t.Errorf("moveUp at the top of its section: %v cursor %d", names(got), got.cursor)
	}
}

func TestSectionsEditorMoveSectionCarriesItsMonitors(t *testing.T) {
	e := twoSections()
	e.cursor = 3 // B
	up := e.moveUp()
	if !reflect.DeepEqual(names(up), []string{"B: m3", "A: m1 m2"}) || up.cursor != 0 || !up.dirty {
		t.Fatalf("moveUp of B: %v cursor %d", names(up), up.cursor)
	}
	if up.sections[0].ID != 5 || up.sections[1].ID != 4 {
		t.Errorf("sections lost their ids: %+v", up.sections)
	}
	if e.sections[0].Name != "A" {
		t.Error("moveUp changed the editor it was called on")
	}
	down := up.moveDown()
	if !reflect.DeepEqual(names(down), []string{"A: m1 m2", "B: m3"}) || down.cursor != 3 {
		t.Fatalf("moveDown of B back: %v cursor %d", names(down), down.cursor)
	}
	if got := down.moveDown(); got.cursor != 3 || !reflect.DeepEqual(names(got), names(down)) {
		t.Errorf("moveDown of the last section: %v cursor %d", names(got), got.cursor)
	}
	e.cursor = 0
	if got := e.moveUp(); got.cursor != 0 || got.dirty || !reflect.DeepEqual(names(got), names(e)) {
		t.Errorf("moveUp of the first section: %v cursor %d", names(got), got.cursor)
	}
}

func TestSectionsEditorRemoveRow(t *testing.T) {
	e := twoSections()
	e.cursor = 1 // m1
	mon := e.removeRow()
	if !reflect.DeepEqual(names(mon), []string{"A: m2", "B: m3"}) || mon.cursor != 1 || !mon.dirty {
		t.Fatalf("remove a monitor: %v cursor %d", names(mon), mon.cursor)
	}
	if len(e.sections[0].Monitors) != 2 {
		t.Error("removeRow changed the editor it was called on")
	}
	e.cursor = 0 // A
	sec := e.removeRow()
	if !reflect.DeepEqual(names(sec), []string{"B: m3"}) || sec.cursor != 0 {
		t.Fatalf("remove a section: %v cursor %d", names(sec), sec.cursor)
	}
	e.cursor = 4 // m3, the last row
	last := e.removeRow()
	if !reflect.DeepEqual(names(last), []string{"A: m1 m2", "B:"}) || last.cursor != 3 {
		t.Fatalf("remove the last row: %v cursor %d", names(last), last.cursor)
	}
	only := sectionsEditor{sections: []kuma.PageSection{{Name: "A"}}}.removeRow()
	if len(only.sections) != 0 || only.cursor != 0 {
		t.Fatalf("remove the only row: %v cursor %d", names(only), only.cursor)
	}
}

// onSections opens the sections of the test page, whose public copy has one
// section, Services, with nextcloud in it.
func onSections(t *testing.T) *harness {
	t.Helper()
	h := onInstance(t, withPages(twoMonitors()))
	h.fakes["home"].Handle("/api/status-page/shop-status", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"incidents":[],"publicGroupList":[{"id":4,"name":"Services","monitorList":[{"id":1,"name":"nextcloud","sendUrl":0}]}]}`))
	})
	h.press("S", "s")
	if h.m.screen != screenPageSections {
		t.Fatalf("s did not open the sections: %v\n%s", h.m.screen, h.view())
	}
	return h
}

func TestPageSectionsArrangeAndSave(t *testing.T) {
	h := onSections(t)
	v := h.view()
	for _, want := range []string{"Shop status", "▾ Services", "nextcloud", "ctrl+s save"} {
		if !strings.Contains(v, want) {
			t.Errorf("editor lacks %q:\n%s", want, v)
		}
	}
	h.press("m")
	if h.m.screen != screenPick || strings.Contains(h.view(), "nextcloud") || !strings.Contains(h.view(), "vaultwarden") {
		t.Fatalf("the picker offers what is in the section, or not the rest:\n%s", h.view())
	}
	h.press("enter", "J")
	if h.m.screen != screenPageSections || !reflect.DeepEqual(names(h.m.secs), []string{"Services: nextcloud vaultwarden"}) {
		t.Fatalf("after pick and J: %v %v", h.m.screen, names(h.m.secs))
	}
	h.press("a")
	if h.m.screen != screenName || !strings.Contains(h.view(), "New section") {
		t.Fatalf("a did not ask for a name:\n%s", h.view())
	}
	h.typeText("Internal")
	h.press("enter")
	if h.m.screen != screenPageSections || !strings.Contains(h.view(), "(no monitors)") {
		t.Fatalf("after naming the section: %v\n%s", h.m.screen, h.view())
	}
	assertFits(t, h.view(), 120)
	h.press("ctrl+s")
	f := h.fakes["home"]
	for _, want := range []string{`"saveStatusPage","shop-status"`, `"customCSS":"body{}"`,
		`[{"id":4,"monitorList":[{"id":1},{"id":2}],"name":"Services"},{"monitorList":[],"name":"Internal"}]`} {
		if !f.Sent(want) {
			t.Errorf("save lacks %s: %v", want, f.Frames())
		}
	}
	if h.m.screen != screenPages || !strings.Contains(h.view(), "saved the sections of status page Shop status") {
		t.Errorf("after save: %v\n%s", h.m.screen, h.view())
	}
}

func TestPageSectionsEscAsksWhenDirty(t *testing.T) {
	h := onSections(t)
	h.press("a")
	h.typeText("Internal")
	h.press("enter", "esc")
	if h.m.screen != screenConfirm || !strings.Contains(h.view(), "Discard the changes to the sections?") {
		t.Fatalf("esc on a dirty editor did not ask:\n%s", h.view())
	}
	h.press("n")
	if h.m.screen != screenPageSections || len(h.m.secs.sections) != 2 {
		t.Fatalf("n left the editor: %v %v", h.m.screen, names(h.m.secs))
	}
	h.press("esc", "y")
	if h.m.screen != screenPages || h.fakes["home"].Called("saveStatusPage") {
		t.Fatalf("y did not discard: %v", h.m.screen)
	}
}

func TestPageSectionsRemoveASectionAsks(t *testing.T) {
	h := onSections(t)
	h.press("d")
	if h.m.screen != screenConfirm || !strings.Contains(h.view(), `Remove the section "Services" and its 1 monitor from the page?`) {
		t.Fatalf("no question:\n%s", h.view())
	}
	h.press("y")
	if h.m.screen != screenPageSections || len(h.m.secs.sections) != 0 {
		t.Fatalf("y did not remove it: %v %v", h.m.screen, names(h.m.secs))
	}
	h.press("m")
	if h.m.screen != screenPageSections || !strings.Contains(h.view(), "add a section first (a)") {
		t.Errorf("m with no sections:\n%s", h.view())
	}
	h.press("esc")
	if h.m.screen != screenConfirm {
		t.Errorf("esc after a removal did not ask: %v", h.m.screen)
	}
}

func TestPageSectionsRenameAndGroupFlowsStayApart(t *testing.T) {
	h := onSections(t)
	h.press("r")
	if h.m.screen != screenName || !strings.Contains(h.view(), "Rename section") {
		t.Fatalf("r did not ask:\n%s", h.view())
	}
	h.m.nform.fields[0].SetValue("Apps")
	h.press("enter")
	if h.m.screen != screenPageSections || h.m.secs.sections[0].Name != "Apps" || h.m.secs.sections[0].ID != 4 {
		t.Fatalf("rename: %v %+v", h.m.screen, h.m.secs.sections)
	}
	if h.fakes["home"].Called("add") || h.fakes["home"].Called("editMonitor") {
		t.Error("naming a section wrote a group")
	}
	h.press("esc", "y", "esc", "g")
	if h.m.screen != screenName || !strings.Contains(h.view(), "New group") {
		t.Fatalf("g after the sections: %v\n%s", h.m.screen, h.view())
	}
	h.typeText("Shop")
	h.press("enter")
	if !h.fakes["home"].Sent(`["add",`) {
		t.Errorf("the group form no longer creates a group: %v", h.fakes["home"].Frames())
	}
}

func TestPageSectionsIgnoreALateLoad(t *testing.T) {
	h := onInstance(t, withPages(twoMonitors()))
	h.press("S", "esc")
	h.send(sectionsLoaded{instance: "home", slug: "shop-status", title: "Shop status"})
	if h.m.screen != screenInstance {
		t.Fatalf("a load that came after the user left opened the editor: %v", h.m.screen)
	}
}

func TestPageSectionsStayOpenWhenAnotherWriteLands(t *testing.T) {
	h := onInstance(t, withPages(twoMonitors()))
	h.fakes["home"].Handle("/api/status-page/shop-status", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"incidents":[],"publicGroupList":[{"id":4,"name":"Services","monitorList":[{"id":1,"name":"nextcloud","sendUrl":0}]}]}`))
	})
	h.press("S", "u")
	unpin := h.hold("y")
	h.press("s", "a")
	h.typeText("Internal")
	h.press("enter")
	if h.m.screen != screenPageSections || !h.m.secs.dirty {
		t.Fatalf("the edit did not take: %v", h.m.screen)
	}
	h.run(unpin)
	if !h.fakes["home"].Sent(`["unpinIncident","shop-status"]`) {
		t.Fatalf("the unpin was not sent: %v", h.fakes["home"].Frames())
	}
	if h.m.screen != screenPageSections || !h.m.secs.dirty || len(h.m.secs.sections) != 2 {
		t.Fatalf("the unpin's answer closed the editor and its edits: %v %v", h.m.screen, names(h.m.secs))
	}
	if !strings.Contains(h.view(), "unpinned the incident on status page Shop status") {
		t.Errorf("the unpin's answer is not shown:\n%s", h.view())
	}
}

func TestPageSectionsEditedDuringTheSaveStayOpen(t *testing.T) {
	h := onSections(t)
	h.press("a")
	h.typeText("Internal")
	h.press("enter")
	save := h.hold("ctrl+s")
	h.press("a")
	h.typeText("Edge")
	h.press("enter")
	h.run(save)
	if h.m.screen != screenPageSections || !h.m.secs.dirty {
		t.Fatalf("the save closed the editor over a later edit: %v dirty %v", h.m.screen, h.m.secs.dirty)
	}
	if !reflect.DeepEqual(names(h.m.secs), []string{"Services: nextcloud", "Internal:", "Edge:"}) {
		t.Errorf("the later edit was lost: %v", names(h.m.secs))
	}

	// Saved again, and not touched while Kuma saves: now it closes.
	h.press("ctrl+s")
	if h.m.screen != screenPages {
		t.Errorf("a save of what is on screen did not close the editor: %v", h.m.screen)
	}
}

func TestPageSectionsSavedUnderAFormOpenedFromThem(t *testing.T) {
	h := onSections(t)
	h.press("a")
	h.typeText("Internal")
	h.press("enter")
	save := h.hold("ctrl+s")
	h.press("r")
	h.typeText("x")
	h.run(save)
	if h.m.screen != screenName || !strings.Contains(h.view(), "Rename section") {
		t.Fatalf("the save closed the rename being typed: %v\n%s", h.m.screen, h.view())
	}
	h.press("esc")
	if h.m.screen != screenPageSections || h.m.secs.dirty {
		t.Fatalf("back on the editor: %v dirty %v", h.m.screen, h.m.secs.dirty)
	}
	h.press("esc")
	if h.m.screen != screenPages {
		t.Errorf("esc on a saved editor asked or stayed: %v", h.m.screen)
	}
}

func TestPageSectionsOfferOnlyMonitorsNotOnThePage(t *testing.T) {
	h := onSections(t)
	h.press("a")
	h.typeText("Internal")
	h.press("enter", "m")
	if h.m.screen != screenPick || strings.Contains(h.view(), "nextcloud") || !strings.Contains(h.view(), "vaultwarden") {
		t.Fatalf("a new section is offered a monitor another section has, or not the rest:\n%s", h.view())
	}
	h.press("enter", "a")
	h.typeText("Edge")
	h.press("enter", "m")
	if h.m.screen != screenPageSections || !strings.Contains(h.view(), "every monitor is on the page already") {
		t.Errorf("with every monitor on the page: %v\n%s", h.m.screen, h.view())
	}
	if !reflect.DeepEqual(names(h.m.secs), []string{"Services: nextcloud", "Internal: vaultwarden", "Edge:"}) {
		t.Errorf("sections = %v", names(h.m.secs))
	}
}
