package ui

import (
	"net/http"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/icortesb/lazykuma/internal/kuma"
)

func TestIncidentPostOnAPageWithoutOne(t *testing.T) {
	h := onInstance(t, withPages(twoMonitors()))
	h.fakes["home"].Handle("/api/status-page/shop-status", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"incidents":[],"publicGroupList":[]}`))
	})
	h.press("S", "i")
	if h.m.screen != screenPageIncident || !strings.Contains(h.view(), "Post an incident on Shop status") {
		t.Fatalf("i did not open an empty incident:\n%s", h.view())
	}
	h.typeText("Down")
	h.press("tab")
	h.typeText("We are on it")
	h.press("tab", "l", "l", "ctrl+s")
	f := h.fakes["home"]
	if !f.Sent(`["postIncident","shop-status",{"content":"We are on it","style":"danger","title":"Down"}]`) {
		t.Fatalf("post: %v", f.Frames())
	}
	if h.m.screen != screenPages {
		t.Errorf("after the post: %v", h.m.screen)
	}
}

func TestIncidentEditThePinnedOne(t *testing.T) {
	h := onInstance(t, withPages(twoMonitors()))
	h.fakes["home"].Handle("/api/status-page/shop-status", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"incidents":[{"id":3,"style":"warning","title":"Slow checkout","content":"Payments lag\nWe are looking","pin":true}],"publicGroupList":[]}`))
	})
	h.press("S", "i")
	v := h.view()
	if h.m.screen != screenPageIncident || !strings.Contains(v, "Edit the incident on Shop status") || !strings.Contains(v, "Slow checkout") {
		t.Fatalf("i did not open the pinned incident:\n%s", v)
	}
	h.press("ctrl+s")
	f := h.fakes["home"]
	if !f.Sent(`"id":3`) || !f.Sent(`"content":"Payments lag\nWe are looking"`) || !f.Sent(`"style":"warning"`) {
		t.Fatalf("edit: %v", f.Frames())
	}
}

func TestIncidentUnpinAsks(t *testing.T) {
	h := onInstance(t, withPages(twoMonitors()))
	h.press("S", "u")
	v := h.view()
	if !strings.Contains(v, `Take down the incident on "Shop status"?`) || !strings.Contains(v, "Kuma keeps it in its history") {
		t.Fatalf("no confirmation:\n%s", v)
	}
	h.press("n")
	if h.fakes["home"].Called("unpinIncident") || h.m.screen != screenPages {
		t.Fatalf("n unpinned: %v %v", h.m.screen, h.fakes["home"].Frames())
	}
	h.press("u", "y")
	if !h.fakes["home"].Sent(`["unpinIncident","shop-status"]`) || h.m.screen != screenPages {
		t.Fatalf("y: %v %v", h.m.screen, h.fakes["home"].Frames())
	}
}

func TestIncidentValuesRejectEmpty(t *testing.T) {
	f := newIncidentForm("shop-status", "Shop status", nil)
	f.title.SetValue(" ")
	f.content.SetValue("We are on it")
	if _, err := f.Values(); err == nil {
		t.Error("an empty title was accepted")
	}
	f.title.SetValue("Down")
	f.content.SetValue(" \n ")
	if _, err := f.Values(); err == nil {
		t.Error("an empty content was accepted")
	}
	f.content.SetValue("We are on it")
	inc, err := f.Values()
	if err != nil || inc != (kuma.PageIncident{Title: "Down", Content: "We are on it", Style: "info"}) {
		t.Errorf("values = %+v, %v", inc, err)
	}
}

func TestIncidentFormKeys(t *testing.T) {
	f := newIncidentForm("shop-status", "Shop status", &kuma.PageIncident{ID: 3, Title: "Down", Content: "x", Style: "dark"})
	if f.style != len(kuma.IncidentStyles)-1 {
		t.Fatalf("style %d", f.style)
	}
	// h and l are letters in the title and the content.
	f, _, _ = f.Update(keyMsg("l"))
	f, _, _ = f.Update(keyMsg("tab"))
	f, _, _ = f.Update(keyMsg("h"))
	f, act, _ := f.Update(keyMsg("enter"))
	f, _, _ = f.Update(keyMsg("k"))
	if act != formNone || f.title.Value() != "Downl" || f.content.Value() != "xh\nk" || f.style != len(kuma.IncidentStyles)-1 {
		t.Fatalf("typing: %q %q style %d act %v", f.title.Value(), f.content.Value(), f.style, act)
	}
	f, _, _ = f.Update(keyMsg("tab"))
	f, _, _ = f.Update(keyMsg("right")) // dark wraps to info
	if kuma.IncidentStyles[f.style] != "info" {
		t.Errorf("right from dark: %s", kuma.IncidentStyles[f.style])
	}
	f, _, _ = f.Update(keyMsg("left"))
	f, _, _ = f.Update(keyMsg("h"))
	if kuma.IncidentStyles[f.style] != "light" {
		t.Errorf("left, h from info: %s", kuma.IncidentStyles[f.style])
	}
	f, _, _ = f.Update(keyMsg("shift+tab"))
	if f.row != 1 {
		t.Errorf("shift+tab from the style: row %d", f.row)
	}
	if _, act, _ := f.Update(keyMsg("ctrl+s")); act != formSubmit {
		t.Error("ctrl+s does not save from the content")
	}
	if _, act, _ := f.Update(keyMsg("esc")); act != formCancel {
		t.Error("esc does not cancel")
	}
	assertFits(t, ansi.Strip(f.withWidth(60).View()), 60)
}

func TestIncidentIgnoreALateLoad(t *testing.T) {
	h := onInstance(t, withPages(twoMonitors()))
	h.press("S", "esc")
	h.send(incidentLoaded{instance: "home", slug: "shop-status", title: "Shop status"})
	if h.m.screen != screenInstance {
		t.Fatalf("a load that came after the user left opened the form: %v", h.m.screen)
	}
}
