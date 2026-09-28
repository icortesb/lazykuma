package ui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/icortesb/lazykuma/internal/kuma"
	"github.com/icortesb/lazykuma/internal/state"
)

// withServer is an instance with two API keys, the second expired, a proxy
// and a Docker host.
func withServer(st state.Instance) state.Instance {
	st = state.Apply(st, kuma.APIKeyList{Keys: []kuma.APIKey{
		{ID: 1, Name: "prometheus", Active: true, Created: "2026-09-01 10:00:00", Status: "active"},
		{ID: 2, Name: "old-grafana", Active: true, Expires: "2026-09-10 23:59:00", Created: "2026-08-01 10:00:00", Status: "expired"},
	}}, tBase)
	st = state.Apply(st, kuma.ProxyList{Proxies: []kuma.Proxy{
		{ID: 4, Protocol: "http", Host: "proxy.home.lan", Port: 3128, Auth: true, Username: "kuma", Password: "hunter2", Default: true}, // ggignore: a test's fake password
	}}, tBase)
	return state.Apply(st, kuma.DockerHostList{Hosts: []kuma.DockerHost{
		{ID: 2, Name: "nas", Type: "socket", Daemon: "/var/run/docker.sock"},
	}}, tBase)
}

func TestServerTabsAndKeys(t *testing.T) {
	h := onInstance(t, withServer(twoMonitors()))
	h.press("A")
	v := h.view()
	for _, want := range []string{"home · server", "API keys", "prometheus (id 1)", "active", "never expires", "made 2026-09-01",
		"old-grafana", "expired", "expires 2026-09-10 23:59"} {
		if !strings.Contains(v, want) {
			t.Errorf("keys tab lacks %q:\n%s", want, v)
		}
	}
	h.press("tab")
	if v := h.view(); !strings.Contains(v, "http://proxy.home.lan:3128") || !strings.Contains(v, "auth kuma") || strings.Contains(v, "hunter2") {
		t.Errorf("proxies tab:\n%s", v)
	}
	h.press("4")
	if !h.fakes["home"].Called("getDatabaseSize") {
		t.Fatalf("the database tab did not ask its size: %v", h.fakes["home"].Frames())
	}
	if v := h.view(); !strings.Contains(v, "60.0 KiB") {
		t.Errorf("database tab:\n%s", v)
	}
	h.press("esc")
	if h.m.screen != screenInstance {
		t.Fatalf("esc: %v", h.m.screen)
	}
}

func TestServerMakesAnAPIKeyAndShowsItOnce(t *testing.T) {
	h := onInstance(t, twoMonitors())
	h.press("A")
	if !strings.Contains(h.view(), "no API keys: n makes one for Prometheus or Grafana") {
		t.Fatalf("empty keys tab:\n%s", h.view())
	}
	h.press("n")
	if !strings.Contains(h.view(), "the key is shown once, right after it is made") {
		t.Fatalf("form:\n%s", h.view())
	}
	h.typeText("grafana")
	h.press("enter", "enter")
	var frame string
	for _, f := range h.fakes["home"].Frames() {
		if strings.Contains(f, `"addAPIKey"`) {
			frame = f
		}
	}
	if !strings.Contains(frame, `"name":"grafana"`) || !strings.Contains(frame, `"expires":null`) {
		t.Fatalf("addAPIKey frame = %q", frame)
	}
	v := h.view()
	for _, want := range []string{`API key "grafana" (id 1)`, "uk1_secret", "copy it now"} {
		if !strings.Contains(v, want) {
			t.Errorf("shown key lacks %q:\n%s", want, v)
		}
	}
	h.press("esc")
	if h.m.screen != screenServer {
		t.Fatalf("esc: %v", h.m.screen)
	}
	if strings.Contains(h.view(), "uk1_secret") || strings.Contains(h.m.flash, "uk1_secret") {
		t.Fatalf("the key is still on screen:\n%s", h.view())
	}
	if strings.Contains(fmt.Sprintf("%+v", h.m), "uk1_secret") {
		t.Fatal("the model still holds the key")
	}
}

func TestAPIKeyFormExpiry(t *testing.T) {
	form := func(typed string) apiKeyForm {
		f := newAPIKeyForm()
		f.now = func() time.Time { return tBase } // 2026-09-11
		f.fields[0].SetValue("grafana")
		f.fields[1].SetValue(typed)
		return f
	}
	for _, tc := range []struct{ typed, want string }{
		{"", ""},
		{"2026-12-31", "2026-12-31 23:59"},
		{"2026-12-31 08:30", "2026-12-31 08:30"},
		{"2026-12-31 8:30", "2026-12-31 08:30"},
		{"  2026-12-31  ", "2026-12-31 23:59"},
		{"2026-09-11", "2026-09-11 23:59"}, // today still counts
	} {
		name, expires, err := form(tc.typed).Values()
		if err != nil || name != "grafana" || expires != tc.want {
			t.Errorf("%q: %q %q %v", tc.typed, name, expires, err)
		}
	}
	for _, typed := range []string{"tomorrow", "2026-02-30"} {
		if _, _, err := form(typed).Values(); err == nil || !strings.Contains(err.Error(), "YYYY-MM-DD") || !strings.Contains(err.Error(), "server") {
			t.Errorf("%q: %v", typed, err)
		}
	}
	if _, _, err := form("2026-09-10 12:00").Values(); err == nil || !strings.Contains(err.Error(), "that date has passed") {
		t.Errorf("past date: %v", err)
	}
}

func TestAPIKeyFormSendsOnce(t *testing.T) {
	h := onInstance(t, twoMonitors())
	h.press("A", "n")
	h.typeText("grafana")
	h.press("enter")
	held := h.hold("enter")
	if !strings.Contains(h.view(), "making the key…") {
		t.Errorf("no word while making:\n%s", h.view())
	}
	h.press("enter") // impatient: must not make a second key
	h.run(held)
	n := 0
	for _, f := range h.fakes["home"].Frames() {
		if strings.Contains(f, `"addAPIKey"`) {
			n++
		}
	}
	if n != 1 || h.m.screen != screenAPIKeyShown {
		t.Fatalf("addAPIKey sent %d times, screen %v", n, h.m.screen)
	}
}

func TestServerTogglesAndDeletesAKey(t *testing.T) {
	h := onInstance(t, withServer(twoMonitors()))
	h.press("A")
	h.send(tea.KeyMsg{Type: tea.KeySpace})
	if !h.fakes["home"].Sent(`["disableAPIKey",1]`) || !strings.Contains(h.view(), "disabled API key prometheus") {
		t.Fatalf("toggle: %v\n%s", h.fakes["home"].Frames(), h.view())
	}
	h.press("d")
	v := h.view()
	if !strings.Contains(v, `Delete the API key "prometheus"?`) || !strings.Contains(v, "anything using it stops working") {
		t.Fatalf("no question:\n%s", v)
	}
	h.press("y")
	if !h.fakes["home"].Sent(`["deleteAPIKey",1]`) || h.m.screen != screenServer {
		t.Fatalf("delete: %v %v", h.m.screen, h.fakes["home"].Frames())
	}
}

func TestServerClearStatisticsNeedsTheName(t *testing.T) {
	h := onInstance(t, twoMonitors())
	h.press("A", "4", "X")
	v := h.view()
	if !strings.Contains(v, "Clear all statistics of home?") || !strings.Contains(v, "Type home to confirm") {
		t.Fatalf("no typed question:\n%s", v)
	}
	h.typeText("hom")
	h.press("enter")
	if h.fakes["home"].Called("clearStatistics") || h.m.screen != screenServerConfirm || !strings.Contains(h.view(), "type home to clear them") {
		t.Fatalf("cleared on a wrong word: %v\n%s", h.m.screen, h.view())
	}
	h.typeText("e")
	h.press("enter")
	if !h.fakes["home"].Called("clearStatistics") || h.m.screen != screenServer || !strings.Contains(h.m.flash, "cleared all statistics of home; the charts fill again as monitors check") {
		t.Fatalf("clear: %v %v\n%s", h.m.screen, h.fakes["home"].Frames(), h.view())
	}
}

func TestServerShrinksAndReloadsTheSize(t *testing.T) {
	h := onInstance(t, twoMonitors())
	h.press("A", "4", "s")
	if v := h.view(); !strings.Contains(v, "Shrink the database?") || !strings.Contains(v, "compacts its SQLite file") {
		t.Fatalf("no question:\n%s", v)
	}
	h.m.srv.dbKnown = false
	h.press("y")
	if !h.fakes["home"].Called("shrinkDatabase") || !h.m.srv.dbKnown || h.m.screen != screenServer {
		t.Fatalf("shrink: %v %+v %v", h.m.screen, h.m.srv, h.fakes["home"].Frames())
	}
}

func TestServerShrinkOnMariaDB(t *testing.T) {
	h := onInstance(t, twoMonitors())
	h.press("A", "4")
	h.send(dbSized{instance: "home", size: 0})
	h.press("s")
	if h.m.screen != screenServer || h.fakes["home"].Called("shrinkDatabase") ||
		!strings.Contains(h.m.flash, "Kuma's database is MariaDB: there is nothing to shrink") {
		t.Fatalf("shrink on MariaDB: %v %q %v", h.m.screen, h.m.flash, h.fakes["home"].Frames())
	}
	// A size not known yet still asks: only Kuma's 0 means MariaDB.
	h.m.srv.dbKnown = false
	h.press("s")
	if h.m.screen != screenConfirm {
		t.Fatalf("shrink with the size unknown: %v", h.m.screen)
	}
}

func TestServerIgnoresStaleAnswers(t *testing.T) {
	h := onInstance(t, twoMonitors())
	h.press("A", "n")
	// A key made for another instance must not open here, nor its size
	// land on this one's database tab.
	h.send(apiKeyMade{instance: "vps", name: "grafana", key: "uk1_other", id: 7})
	h.send(dbSized{instance: "vps", size: 1 << 30})
	if h.m.screen != screenAPIKey || h.m.srv.dbKnown || strings.Contains(fmt.Sprintf("%+v", h.m), "uk1_other") {
		t.Fatalf("stale answers landed: %v %+v", h.m.screen, h.m.srv)
	}
	// It is not dropped silently: the user hears where it was made, and
	// how to be rid of it, but never its secret.
	if !strings.Contains(h.m.flash, `API key "grafana" (id 7) on vps was made after its form closed`) || !strings.Contains(h.m.flash, "delete it (id 7)") ||
		strings.Contains(h.view(), "uk1_other") {
		t.Fatalf("key on another instance: %q\n%s", h.m.flash, h.view())
	}
	h.send(apiKeyMade{instance: "vps", name: "grafana", key: "uk1_other", err: errors.New("the name is taken")})
	if !strings.Contains(h.m.flash, `API key "grafana" on vps: the name is taken`) || strings.Contains(fmt.Sprintf("%+v", h.m), "uk1_other") {
		t.Fatalf("error on another instance: %q", h.m.flash)
	}
	// A key made after the form was left shows on the server screen the
	// form returns to...
	h.press("esc")
	h.send(apiKeyMade{instance: "home", name: "grafana", key: "uk1_late", id: 3})
	if h.m.screen != screenAPIKeyShown || !strings.Contains(h.view(), "uk1_late") || !strings.Contains(h.view(), "(id 3)") {
		t.Fatalf("late key on the server screen: %v\n%s", h.m.screen, h.view())
	}
	h.press("enter")
	// ...but not over another screen: the user hears it was made, by id,
	// and the secret is dropped.
	h.press("esc")
	h.send(apiKeyMade{instance: "home", name: "grafana", key: "uk1_later", id: 4})
	if h.m.screen != screenInstance || strings.Contains(fmt.Sprintf("%+v", h.m), "uk1_l") || !strings.Contains(h.view(), `API key "grafana" (id 4) was made`) {
		t.Fatalf("late key: %v\n%s", h.m.screen, h.view())
	}
}

func TestServerHelp(t *testing.T) {
	h := onInstance(t, twoMonitors())
	h.press("A", "?")
	if h.m.screen != screenHelp || !strings.Contains(h.view(), "On the server screen") {
		t.Fatalf("help: %v\n%s", h.m.screen, h.view())
	}
	h.press("esc")
	if h.m.screen != screenServer {
		t.Fatalf("esc from help: %v", h.m.screen)
	}
}

func TestServerCursorFollowsTheList(t *testing.T) {
	h := onInstance(t, withServer(twoMonitors()))
	h.press("A", "j")
	// The second key goes: the cursor stays on the list.
	h.state("home", state.Apply(withServer(twoMonitors()), kuma.APIKeyList{Keys: []kuma.APIKey{{ID: 1, Name: "prometheus", Active: true, Status: "active"}}}, tBase))
	h.send(tea.KeyMsg{Type: tea.KeySpace})
	if !h.fakes["home"].Sent(`["disableAPIKey",1]`) {
		t.Fatalf("toggle after the list shrank: %v", h.fakes["home"].Frames())
	}
}

func TestServerFitsNarrowTerminal(t *testing.T) {
	h := onInstance(t, withServer(twoMonitors()))
	h.send(tea.WindowSizeMsg{Width: 60, Height: 24})
	h.press("A")
	for _, tab := range []string{"1", "2", "3", "4"} {
		h.press(tab)
		assertFits(t, h.view(), 60)
	}
	// The words of the empty tabs and of the database tab are the screen's
	// own lines, not rows the app may cut at the edge: they must fit
	// before it does.
	h = onInstance(t, twoMonitors())
	h.press("A")
	for _, tab := range []string{"1", "2", "3", "4"} {
		h.press(tab)
		if tab == "4" {
			h.send(dbSized{instance: "home", size: 61440})
		}
		assertFits(t, ansi.Strip(h.m.srv.View("home", h.m.current().st, 60, 22)), 60)
	}
}

func TestServerFooterFollowsTheTab(t *testing.T) {
	for tab, want := range map[serverTab][]string{
		tabKeys:     {"n new", "space enable/disable", "d delete"},
		tabProxies:  {"n new", "e edit", "d delete"},
		tabDocker:   {"n new", "e edit", "t test", "d delete"},
		tabDatabase: {"s shrink", "X clear stats", "r refresh"},
	} {
		hints := serverScreen{tab: tab}.hints()
		if w := lipgloss.Width(hints); w >= 80 {
			t.Errorf("%v footer is %d wide: %q", tab, w, hints)
		}
		for _, k := range append(want, "tab next tab", "1-4 tabs", "? help", "esc back") {
			if !strings.Contains(hints, k) {
				t.Errorf("%v footer lacks %q: %q", tab, k, hints)
			}
		}
		// Only the keys that work there.
		if tab != tabKeys && strings.Contains(hints, "space") || tab != tabDocker && strings.Contains(hints, "t test") ||
			tab == tabDatabase && strings.Contains(hints, "n new") || tab != tabDatabase && strings.Contains(hints, "s shrink") {
			t.Errorf("%v footer has another tab's keys: %q", tab, hints)
		}
	}
	// The help still lists them all.
	for _, k := range []string{"1-4 tabs", "space enable/disable", "t test", "s shrink", "X clear statistics", "r refresh"} {
		if !strings.Contains(keyHintsServer, k) {
			t.Errorf("help lacks %q", k)
		}
	}
	h := onInstance(t, twoMonitors())
	h.press("A", "4")
	if v := h.view(); !strings.Contains(v, "s shrink") || strings.Contains(v, "space enable/disable") {
		t.Fatalf("database tab footer:\n%s", v)
	}
}

func TestAPIKeyShownFitsNarrowTerminal(t *testing.T) {
	key := "uk1_" + strings.Repeat("0123456789", 9)
	v := ansi.Strip(apiKeyShown{name: "grafana", key: key, id: 3}.View(60))
	assertFits(t, v, 60)
	lines := strings.Split(v, "\n")
	// The key, whole across its lines, with nothing padded onto them.
	var got string
	for _, l := range lines {
		if strings.HasPrefix(l, "uk1_") || got != "" && len(got) < len(key) {
			got += l
		}
	}
	if got != key {
		t.Fatalf("the key is not whole: %q\n%s", got, v)
	}
	if !strings.Contains(v, "wrapped here to fit") || !strings.Contains(strings.Join(strings.Fields(v), " "), "copy it now: Kuma will not show it again, and lazykuma does not keep it") {
		t.Fatalf("warning:\n%s", v)
	}
	// A key that fits is one line, with no word about wrapping.
	if v := ansi.Strip(apiKeyShown{name: "grafana", key: key, id: 3}.View(120)); !strings.Contains(v, key) || strings.Contains(v, "wrapped") {
		t.Fatalf("wide:\n%s", v)
	}
}

func TestHumanBytes(t *testing.T) {
	for n, want := range map[int64]string{0: "0 B", 512: "512 B", 61440: "60.0 KiB", 5 << 20: "5.0 MiB", 3 << 30: "3.0 GiB"} {
		if got := humanBytes(n); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", n, got, want)
		}
	}
}
