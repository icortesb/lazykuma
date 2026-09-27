package ui

import (
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/icortesb/lazykuma/internal/kuma"
	"github.com/icortesb/lazykuma/internal/state"
)

// Config is the page's config as the form would save it over the page it
// was built from.
func (s pageSettings) Config() (map[string]any, error) {
	changes, err := s.Changes()
	if err != nil {
		return nil, err
	}
	return withChanges(s.page.Config, changes), nil
}

func TestPageSettingsKeepWhatTheFormDoesNotShow(t *testing.T) {
	p := kuma.StatusPage{ID: 7, Slug: "shop-status", Title: "Shop status", Config: map[string]any{
		"id": float64(7), "slug": "shop-status", "title": "Shop status", "description": nil, "footerText": nil,
		"autoRefreshInterval": float64(300), "domainNameList": []any{}, "theme": "auto",
		"showTags": false, "showCertificateExpiry": false, "showOnlyLastHeartbeat": false,
		"customCSS": "body{}", "icon": "/icon.svg", "analyticsType": nil,
	}}
	f := newPageSettings(p)
	f.fields[1].SetValue("All systems")
	f.fields[3].SetValue("60")
	f.fields[4].SetValue("status.example.com, , www.example.com")
	f.fields[5].SetValue("dark")
	f.toggles[0].on = true
	cfg, err := f.Config()
	if err != nil {
		t.Fatal(err)
	}
	if cfg["description"] != "All systems" || cfg["autoRefreshInterval"] != 60 || cfg["theme"] != "dark" || cfg["showTags"] != true ||
		cfg["customCSS"] != "body{}" || cfg["icon"] != "/icon.svg" {
		t.Fatalf("config = %v", cfg)
	}
	if d, _ := cfg["domainNameList"].([]string); len(d) != 2 || d[0] != "status.example.com" {
		t.Fatalf("domains = %v", cfg["domainNameList"])
	}
	if p.Config["description"] != nil {
		t.Error("Config changed the page it was built from")
	}
	f.fields[3].SetValue("soon")
	if _, err := f.Config(); err == nil {
		t.Error("a bad refresh interval was accepted")
	}
}

func TestPageSettingsSaveKeepsTheSections(t *testing.T) {
	h := onInstance(t, withPages(twoMonitors()))
	h.fakes["home"].Handle("/api/status-page/shop-status", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"incidents":[],"publicGroupList":[{"id":4,"name":"Services","monitorList":[{"id":1,"name":"nextcloud","sendUrl":0}]}]}`))
	})
	h.press("S", "e")
	if h.m.screen != screenPageSettings {
		t.Fatalf("e did not open the settings:\n%s", h.view())
	}
	h.m.pset.fields[1].SetValue("All systems")
	h.press("enter")
	f := h.fakes["home"]
	for _, want := range []string{`"saveStatusPage","shop-status"`, `"description":"All systems"`, `"customCSS":"body{}"`, `[{"id":4,"monitorList":[{"id":1}],"name":"Services"}]`} {
		if !f.Sent(want) {
			t.Errorf("save lacks %s: %v", want, f.Frames())
		}
	}
	if h.m.screen != screenPages {
		t.Errorf("after save: %v", h.m.screen)
	}
}

func TestPageSettingsDoNotSaveWithoutTheSections(t *testing.T) {
	h := onInstance(t, withPages(twoMonitors()))
	h.fakes["home"].Handle("/api/status-page/shop-status", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	h.press("S", "e", "enter")
	if h.fakes["home"].Called("saveStatusPage") {
		t.Fatal("saved without the sections, which would wipe them")
	}
	if h.m.screen != screenPageSettings || !strings.Contains(h.view(), "sections") {
		t.Errorf("a failed read does not say so:\n%s", h.view())
	}
}

func TestPageSettingsIgnoreALateLoad(t *testing.T) {
	h := onInstance(t, withPages(twoMonitors()))
	h.press("S", "esc")
	h.send(pageLoaded{instance: "home", slug: "shop-status", page: kuma.StatusPage{Slug: "shop-status"}})
	if h.m.screen != screenInstance {
		t.Fatalf("a load that came after the user left opened the form: %v", h.m.screen)
	}
}

func TestPageSettingsToggles(t *testing.T) {
	f := newPageSettings(kuma.StatusPage{Slug: "shop-status", Title: "Shop status", Config: map[string]any{"showTags": true}})
	if !f.toggles[0].on || f.fields[3].Value() != "300" || f.fields[5].Value() != "auto" {
		t.Fatalf("not filled from the config: %v %q %q", f.toggles, f.fields[3].Value(), f.fields[5].Value())
	}
	for _, k := range []string{"tab", "tab", "tab", "tab", "tab", "tab", "tab"} {
		f, _, _ = f.Update(keyMsg(k))
	}
	if !f.inToggles() || f.toggle != 1 {
		t.Fatalf("tab from the last field: toggle %d", f.toggle)
	}
	f, _, _ = f.Update(tea.KeyMsg{Type: tea.KeySpace})
	if !f.toggles[1].on {
		t.Error("space did not flip the toggle")
	}
	f, _, _ = f.Update(keyMsg("shift+tab"))
	f, _, _ = f.Update(keyMsg("shift+tab"))
	if f.inToggles() || f.focus != 5 {
		t.Errorf("shift+tab out of the toggles: toggle %d focus %d", f.toggle, f.focus)
	}
	if _, act, _ := f.Update(keyMsg("enter")); act != formSubmit {
		t.Error("enter does not save")
	}
	f.fields[5].SetValue("blue")
	if _, err := f.Config(); err == nil || !strings.Contains(err.Error(), "auto, light, dark") {
		t.Errorf("a bad theme: %v", err)
	}
	f.fields[5].SetValue("dark")
	f.fields[0].SetValue(" ")
	if _, err := f.Config(); err == nil {
		t.Error("an empty title was accepted")
	}
}

func TestPageSettingsSendUntouchedFieldsAsLoaded(t *testing.T) {
	var domains []any
	for i := 0; len(fieldText(domains)) < 600; i++ {
		domains = append(domains, fmt.Sprintf("status%d.example.com", i))
	}
	p := kuma.StatusPage{Slug: "shop-status", Title: "Shop status", Config: map[string]any{
		"title": "Shop status", "description": nil, "footerText": "line one\nline two",
		"autoRefreshInterval": float64(300), "domainNameList": domains, "theme": "auto", "showTags": false,
	}}
	f := newPageSettings(p)
	if !strings.Contains(ansi.Strip(f.View()), "multi-line in Kuma") {
		t.Errorf("the multi-line footer is not pointed out:\n%s", f.View())
	}
	f.toggles[0].on = true
	cfg, err := f.Config()
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := cfg["description"]; !ok || v != nil {
		t.Errorf("description = %#v, want nil kept", v)
	}
	if cfg["footerText"] != "line one\nline two" {
		t.Errorf("footer = %q", cfg["footerText"])
	}
	if !reflect.DeepEqual(cfg["domainNameList"], domains) {
		t.Errorf("domains cut or changed: %v", cfg["domainNameList"])
	}
	if cfg["autoRefreshInterval"] != float64(300) || cfg["showTags"] != true {
		t.Errorf("config = %v", cfg)
	}

	f.fields[1].SetValue("All systems")
	cfg, _ = f.Config()
	if cfg["description"] != "All systems" || cfg["footerText"] != "line one\nline two" {
		t.Errorf("an edited description is not sent alone: %v", cfg)
	}
}

func TestPageSettingsDownTypedAsTextStaysInTheField(t *testing.T) {
	f := newPageSettings(kuma.StatusPage{Slug: "shop-status", Title: "Shop status"})
	for i := 0; i < 5; i++ {
		f, _, _ = f.Update(keyMsg("tab"))
	}
	f.fields[5].SetValue("")
	f, _, _ = f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("down")})
	if f.inToggles() || f.fields[5].Value() != "down" {
		t.Errorf("typed %q, in toggles %v", f.fields[5].Value(), f.inToggles())
	}
}

func TestPageSettingsRefreshRange(t *testing.T) {
	f := newPageSettings(kuma.StatusPage{Slug: "shop-status", Title: "Shop status"})
	for _, v := range []string{"-1", "86401", "999999999", "soon"} {
		f.fields[3].SetValue(v)
		if _, err := f.Config(); err == nil || !strings.Contains(err.Error(), "0 to 86400") {
			t.Errorf("refresh %q: %v", v, err)
		}
	}
	f.fields[3].SetValue("86400")
	if cfg, err := f.Config(); err != nil || cfg["autoRefreshInterval"] != 86400 {
		t.Errorf("a day: %v %v", cfg, err)
	}
}

func TestPageSettingsSaveOverTheSettingsAsTheyAreNow(t *testing.T) {
	h := onInstance(t, state.Apply(twoMonitors(), kuma.StatusPageList{Pages: []kuma.StatusPage{
		{ID: 8, Slug: "restyled", Title: "Restyled", Config: map[string]any{"slug": "restyled", "title": "Restyled"}},
	}}, tBase))
	h.fakes["home"].Handle("/api/status-page/restyled", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"incidents":[],"publicGroupList":[]}`))
	})
	h.press("S", "e")
	if h.m.screen != screenPageSettings {
		t.Fatalf("e did not open the settings:\n%s", h.view())
	}
	// The web UI changes the CSS, the footer and a toggle while the form is
	// open; the form changes the description.
	h.m.pset.fields[1].SetValue("All systems")
	h.press("enter")
	f := h.fakes["home"]
	for _, want := range []string{`"customCSS":"body{color:red}"`, `"footerText":"From the web"`, `"description":"All systems"`, `"showTags":true`} {
		if !f.Sent(want) {
			t.Errorf("save lacks %s: %v", want, f.Frames())
		}
	}
	if h.m.screen != screenPages || !strings.Contains(h.view(), "saved status page Shop status") {
		t.Errorf("after save: %v\n%s", h.m.screen, h.view())
	}
}
