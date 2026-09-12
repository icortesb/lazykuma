package ui

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/icortesb/lazykuma/internal/config"
	"github.com/icortesb/lazykuma/internal/core"
	"github.com/icortesb/lazykuma/internal/kuma"
	"github.com/icortesb/lazykuma/internal/kumatest"
	"github.com/icortesb/lazykuma/internal/state"
)

// harness drives the model the way Bubble Tea would, over a core wired to
// fake Kuma servers.
type harness struct {
	t       *testing.T
	m       Model
	core    *core.Core
	fakes   map[string]*kumatest.Server
	cfgPath string
	login   func(url, user, pass, code string) (string, error)
}

// newHarness gives each named instance its own fake Kuma. Instances whose
// name is in loggedIn start with a token, so they connect by themselves.
func newHarness(t *testing.T, loggedIn []string, names ...string) *harness {
	t.Helper()
	dir := t.TempDir()
	h := &harness{t: t, fakes: map[string]*kumatest.Server{}, cfgPath: filepath.Join(dir, "config.toml")}
	h.login = func(string, string, string, string) (string, error) { return "jwt", nil }

	var cfg config.Config
	for _, name := range names {
		f := kumatest.New(t, kumaWrites(t))
		h.fakes[name] = f
		if err := cfg.Add(config.Instance{Name: name, URL: f.URL()}); err != nil {
			t.Fatal(err)
		}
	}
	if len(names) > 0 {
		if err := config.Save(h.cfgPath, cfg); err != nil {
			t.Fatal(err)
		}
	}
	tokPath := filepath.Join(dir, "tokens.json")
	tokens, err := config.LoadTokens(tokPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range loggedIn {
		if err := tokens.Set(name, h.fakes[name].URL(), "jwt"); err != nil {
			t.Fatal(err)
		}
	}
	c, err := core.Open(h.cfgPath, tokPath, core.Options{Now: func() time.Time { return tBase }})
	if err != nil {
		t.Fatal(err)
	}
	h.core = c
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c.Run(ctx)

	h.m = New(Deps{
		Core: c, Version: "0.0.0-test", Now: func() time.Time { return tBase },
		Login: func(_ context.Context, url, user, pass, code string) (string, error) {
			return h.login(url, user, pass, code)
		},
	})
	h.send(tea.WindowSizeMsg{Width: 120, Height: 40})
	return h
}

// kumaWrites answers login and the write calls the UI tests make.
func kumaWrites(t *testing.T) func(string, []json.RawMessage) any {
	login := kumatest.Login(false)
	return func(event string, args []json.RawMessage) any {
		switch event {
		case "add":
			return map[string]any{"ok": true, "msg": "successAdded", "monitorID": 9}
		case "getMonitor":
			return map[string]any{"ok": true, "monitor": map[string]any{
				"id": 1, "type": "http", "name": "nextcloud", "url": "https://cloud.home.lan",
				"interval": 60, "retryInterval": 60, "maxretries": 0, "active": true,
				"accepted_statuscodes": []string{"200-299"}, "notificationIDList": map[string]bool{},
			}}
		case "editMonitor", "deleteMonitor", "addNotification", "deleteNotification",
			"addMaintenance", "addMonitorMaintenance", "deleteMaintenance":
			out := map[string]any{"ok": true, "msg": "Saved."}
			if event == "addNotification" {
				out["id"] = 5
			}
			if event == "addMaintenance" {
				out["maintenanceID"] = 6
			}
			return out
		case "testNotification":
			return map[string]any{"ok": false, "msg": "Request failed with status code 401"}
		}
		return login(event, args)
	}
}

// connected waits until the instance's state says it is connected, the way
// the program would see it, and feeds that state to the model.
func (h *harness) connected(name string) {
	h.t.Helper()
	h.waitState(name, "connected", func(s state.Instance) bool { return s.Conn == state.ConnOK })
}

// waitState waits for the core to publish a state matching cond and feeds
// it to the model, as the program's update loop does.
func (h *harness) waitState(name, what string, cond func(state.Instance) bool) {
	h.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		in, ok := h.core.Instance(name)
		if ok && cond(in.State()) {
			h.send(InstanceState{Name: name, State: in.State()})
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.t.Fatalf("%s never reached %s", name, what)
}

// state pushes a state snapshot, as the core's updates do.
func (h *harness) state(name string, st state.Instance) {
	h.send(InstanceState{Name: name, State: st})
}

func (h *harness) send(msg tea.Msg) {
	h.t.Helper()
	next, cmd := h.m.Update(msg)
	h.m = next.(Model)
	h.run(cmd)
}

func (h *harness) run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	out := make(chan tea.Msg, 1)
	go func() { out <- cmd() }()
	select {
	case msg := <-out:
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				h.run(c)
			}
			return
		}
		if msg != nil {
			h.send(msg)
		}
	case <-time.After(50 * time.Millisecond):
	}
}

func (h *harness) press(keys ...string) {
	h.t.Helper()
	for _, k := range keys {
		h.send(keyMsg(k))
	}
}

func (h *harness) typeText(s string) {
	for _, r := range s {
		h.send(keyMsg(string(r)))
	}
}

func (h *harness) view() string { return ansi.Strip(h.m.View()) }

func TestMenuListsInstancesAndTheirState(t *testing.T) {
	h := newHarness(t, []string{"home"}, "home", "vps")
	h.connected("home")
	h.state("vps", state.Apply(state.Instance{}, kuma.Disconnected{Err: errors.New("dial tcp: connection refused")}, tBase))

	v := h.view()
	for _, want := range []string{"home", "vps", "Add instance", "Help", "Quit", "home ok", "vps down", "v0.0.0-test"} {
		if !strings.Contains(v, want) {
			t.Errorf("menu lacks %q:\n%s", want, v)
		}
	}
	h.press("down")
	if !strings.Contains(h.view(), "down: dial tcp: connection refused") {
		t.Errorf("down instance does not say why:\n%s", h.view())
	}
}

func TestEmptyMenuSaysToAddOne(t *testing.T) {
	h := newHarness(t, nil)
	if !strings.Contains(h.view(), "no instances yet: add one") {
		t.Fatalf("menu:\n%s", h.view())
	}
}

// withMonitors is an instance state carrying the given monitors.
func withMonitors(mons map[int]kuma.Monitor) state.Instance {
	in := state.Apply(state.Instance{}, kuma.Connected{}, tBase)
	return state.Apply(in, kuma.MonitorList{Monitors: mons}, tBase)
}

func TestOpenInstanceAndPause(t *testing.T) {
	h := newHarness(t, []string{"home"}, "home")
	h.connected("home")
	h.state("home", withMonitors(map[int]kuma.Monitor{
		7: {ID: 7, Name: "nextcloud", URL: "https://cloud.lan", Active: true},
		8: {ID: 8, Name: "backup", URL: "https://s3.lan"},
	}))

	h.press("enter")
	if !strings.Contains(h.view(), "home · 2 monitors") {
		t.Fatalf("not on the instance:\n%s", h.view())
	}
	h.press("p")      // backup is first (b < n) and paused: resume it
	h.press("j", "p") // nextcloud: pause it
	f := h.fakes["home"]
	if !f.Sent(`["resumeMonitor",8]`) || !f.Sent(`["pauseMonitor",7]`) {
		t.Fatalf("frames: %v", f.Frames())
	}
	if !strings.Contains(h.view(), "paused nextcloud") {
		t.Errorf("no flash:\n%s", h.view())
	}
	h.press("esc")
	if !strings.Contains(h.view(), "Add instance") {
		t.Fatal("esc did not go back to the menu")
	}
}

func TestActionOnAnOfflineInstanceSaysSo(t *testing.T) {
	h := newHarness(t, nil, "home")
	h.state("home", withMonitors(map[int]kuma.Monitor{1: {ID: 1, Name: "web", Active: true}}))
	h.press("enter", "p")
	if !strings.Contains(h.view(), "web: kuma: not connected") {
		t.Fatalf("view:\n%s", h.view())
	}
}

func TestLoginFromTheMenu(t *testing.T) {
	h := newHarness(t, nil, "home")
	// With no token the core says so, and the menu entry opens the login.
	h.waitState("home", "no cred", func(s state.Instance) bool { return s.Conn == state.ConnNoCred })
	var tried []string
	h.login = func(url, user, pass, code string) (string, error) {
		tried = append(tried, user+" "+pass+" "+code)
		if code == "" {
			return "", kuma.ErrTokenRequired
		}
		return "jwt", nil
	}

	h.press("enter")
	if !strings.Contains(h.view(), "Log in to home") {
		t.Fatalf("not on the login:\n%s", h.view())
	}
	h.typeText("admin")
	h.press("enter")
	h.typeText("pw")
	h.press("enter")
	if !strings.Contains(h.view(), "2FA code") {
		t.Fatalf("no code field after tokenRequired:\n%s", h.view())
	}
	h.typeText("123456")
	h.press("enter")

	if want := "admin pw |admin pw 123456"; strings.Join(tried, "|") != want {
		t.Fatalf("logins = %q", tried)
	}
	// The token reached the core, which woke the connection.
	h.connected("home")
	if !strings.Contains(h.view(), "home · 0 monitors") {
		t.Fatalf("not on the instance after login:\n%s", h.view())
	}
}

func TestAddInstance(t *testing.T) {
	h := newHarness(t, nil)
	f := kumatest.New(t, kumaWrites(t))
	h.press("enter") // "Add instance"
	h.typeText("vps")
	h.press("tab")
	h.typeText(f.URL())
	h.press("enter")

	if !strings.Contains(h.view(), "Log in to vps") {
		t.Fatalf("not on the login:\n%s", h.view())
	}
	if _, ok := h.core.Instance("vps"); !ok {
		t.Fatal("the core did not start vps")
	}
	saved, _ := os.ReadFile(h.cfgPath)
	if !strings.Contains(string(saved), `name = "vps"`) {
		t.Fatalf("config file = %s", saved)
	}
	if h.m.flash == noInstances {
		t.Fatal("still says there are no instances")
	}
}

func TestAddInstanceRejectsDuplicate(t *testing.T) {
	h := newHarness(t, nil, "home")
	h.press("down", "enter")
	h.typeText("HOME")
	h.press("tab")
	h.typeText("http://other.lan")
	h.press("enter")
	if !strings.Contains(h.view(), `already an instance called "home"`) {
		t.Fatalf("view:\n%s", h.view())
	}
}

func TestHelpAndQuit(t *testing.T) {
	h := newHarness(t, nil, "home")
	h.press("?")
	if !strings.Contains(h.view(), "What lazykuma stores") {
		t.Fatalf("help:\n%s", h.view())
	}
	h.press("esc")
	if !strings.Contains(h.view(), "Add instance") {
		t.Fatal("esc did not leave help")
	}
	_, cmd := h.m.Update(keyMsg("q"))
	if cmd == nil {
		t.Fatal("q did nothing")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("q did not quit")
	}
}

func TestTypingQInAFormDoesNotQuit(t *testing.T) {
	h := newHarness(t, nil, "home")
	h.press("down", "enter") // the add form
	_, cmd := h.m.Update(keyMsg("q"))
	if cmd != nil {
		if _, ok := cmd().(tea.QuitMsg); ok {
			t.Fatal("q quit from a form")
		}
	}
}

func TestMenuFitsNarrowTerminal(t *testing.T) {
	h := newHarness(t, []string{"home"}, "home", "vps")
	h.send(tea.WindowSizeMsg{Width: 60, Height: 24})
	assertFits(t, h.view(), 60)
}
