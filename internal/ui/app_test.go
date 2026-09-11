package ui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/icortesb/lazykuma/internal/config"
	"github.com/icortesb/lazykuma/internal/kuma"
)

// fakeCtrl stands in for a kuma.Supervisor.
type fakeCtrl struct {
	mu              sync.Mutex
	paused, resumed []int
	retries         int
	err             error
}

func (f *fakeCtrl) Pause(_ context.Context, id int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paused = append(f.paused, id)
	return f.err
}

func (f *fakeCtrl) Resume(_ context.Context, id int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resumed = append(f.resumed, id)
	return f.err
}

func (f *fakeCtrl) Retry() { f.mu.Lock(); f.retries++; f.mu.Unlock() }

// harness is the app with fake connections, a temporary config and tokens,
// and a login that answers what the test says.
type harness struct {
	t       *testing.T
	m       Model
	ctrls   map[string]*fakeCtrl
	cfgPath string
	tokens  *config.Tokens
	login   func(url, user, pass, code string) (string, error)
}

func newHarness(t *testing.T, instances ...config.Instance) *harness {
	t.Helper()
	dir := t.TempDir()
	tokens, err := config.LoadTokens(filepath.Join(dir, "tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, ctrls: map[string]*fakeCtrl{}, cfgPath: filepath.Join(dir, "config.toml"), tokens: tokens}
	h.login = func(string, string, string, string) (string, error) { return "jwt", nil }

	var cfg config.Config
	for _, in := range instances {
		if err := cfg.Add(in); err != nil {
			t.Fatal(err)
		}
	}
	h.m = New(Deps{
		Config: cfg, ConfigPath: h.cfgPath, Tokens: tokens, Version: "0.0.0-test",
		Start: func(in config.Instance) Controller {
			c := &fakeCtrl{}
			h.ctrls[in.Name] = c
			return c
		},
		Login: func(_ context.Context, url, user, pass, code string) (string, error) {
			return h.login(url, user, pass, code)
		},
		Now: func() time.Time { return tBase },
	})
	h.send(tea.WindowSizeMsg{Width: 120, Height: 40})
	return h
}

// send delivers msg and then whatever its commands return within a moment,
// so timers such as the flash's are left out.
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
	case <-time.After(20 * time.Millisecond):
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

func (h *harness) event(name string, ev kuma.Event) { h.send(InstanceEvent{Name: name, Event: ev}) }

func (h *harness) view() string { return ansi.Strip(h.m.View()) }

var (
	home = config.Instance{Name: "home", URL: "http://kuma.lan"}
	vps  = config.Instance{Name: "vps", URL: "https://status.example.com"}
)

func TestMenuListsInstancesAndTheirState(t *testing.T) {
	h := newHarness(t, home, vps)
	h.event("home", kuma.Connected{})
	h.event("vps", kuma.Disconnected{Err: errors.New("dial tcp: connection refused")})

	v := h.view()
	for _, want := range []string{"home", "vps", "Add instance", "Help", "Quit", "home ok", "vps down", "v0.0.0-test"} {
		if !strings.Contains(v, want) {
			t.Errorf("menu lacks %q:\n%s", want, v)
		}
	}
	if !strings.Contains(v, "http://kuma.lan · 0 monitors") {
		t.Errorf("selected instance has no description:\n%s", v)
	}
	h.press("down")
	if !strings.Contains(h.view(), "down: dial tcp: connection refused") {
		t.Errorf("down instance does not say why:\n%s", h.view())
	}
}

func TestEmptyMenuSaysToAddOne(t *testing.T) {
	h := newHarness(t)
	if !strings.Contains(h.view(), "no instances yet: add one") {
		t.Fatalf("menu:\n%s", h.view())
	}
}

func TestOpenInstanceAndPause(t *testing.T) {
	h := newHarness(t, home)
	for _, ev := range []kuma.Event{kuma.Connected{}, kuma.MonitorList{Monitors: map[int]kuma.Monitor{
		7: {ID: 7, Name: "nextcloud", URL: "https://cloud.lan", Active: true},
		8: {ID: 8, Name: "backup", URL: "https://s3.lan"},
	}}} {
		h.event("home", ev)
	}
	h.press("enter")
	if !strings.Contains(h.view(), "home · 2 monitors") {
		t.Fatalf("not on the instance:\n%s", h.view())
	}

	h.press("p") // backup is first (b < n) and paused: resume it
	h.press("j", "p")
	c := h.ctrls["home"]
	if len(c.resumed) != 1 || c.resumed[0] != 8 || len(c.paused) != 1 || c.paused[0] != 7 {
		t.Fatalf("resumed %v paused %v", c.resumed, c.paused)
	}
	if !strings.Contains(h.view(), "paused nextcloud") {
		t.Errorf("no flash:\n%s", h.view())
	}

	h.press("esc")
	if !strings.Contains(h.view(), "Add instance") {
		t.Fatal("esc did not go back to the menu")
	}
}

func TestPauseFailureIsShown(t *testing.T) {
	h := newHarness(t, home)
	h.event("home", kuma.Connected{})
	h.event("home", kuma.MonitorList{Monitors: map[int]kuma.Monitor{1: {ID: 1, Name: "web", Active: true}}})
	h.ctrls["home"].err = kuma.ErrNotConnected
	h.press("enter", "p")
	if !strings.Contains(h.view(), "web: kuma: not connected") {
		t.Fatalf("view:\n%s", h.view())
	}
}

func TestLoginFromTheMenu(t *testing.T) {
	h := newHarness(t, home)
	h.event("home", kuma.AuthFailed{NoToken: true})

	var tried []string
	h.login = func(url, user, pass, code string) (string, error) {
		tried = append(tried, url+" "+user+" "+pass+" "+code)
		if code == "" {
			return "", kuma.ErrTokenRequired
		}
		return "jwt-new", nil
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

	if want := []string{"http://kuma.lan admin pw ", "http://kuma.lan admin pw 123456"}; strings.Join(tried, "|") != strings.Join(want, "|") {
		t.Fatalf("logins = %q", tried)
	}
	if h.tokens.Get("home") != "jwt-new" {
		t.Fatal("token not stored")
	}
	if h.ctrls["home"].retries != 1 {
		t.Fatal("supervisor not woken")
	}
	if !strings.Contains(h.view(), "home · 0 monitors") {
		t.Fatalf("not on the instance after login:\n%s", h.view())
	}
}

func TestAddInstance(t *testing.T) {
	h := newHarness(t)
	h.press("enter") // "Add instance", the only entry but Help and Quit
	h.typeText("vps")
	h.press("tab")
	h.typeText("https://status.example.com")
	h.press("enter")

	if _, ok := h.ctrls["vps"]; !ok {
		t.Fatal("vps not started")
	}
	if h.m.flash == noInstances {
		t.Fatal("still says there are no instances")
	}
	if !strings.Contains(h.view(), "Log in to vps") {
		t.Fatalf("not on the login:\n%s", h.view())
	}
	saved, _, err := config.Load(h.cfgPath)
	if err != nil || len(saved.Instances) != 1 || saved.Instances[0] != vps {
		t.Fatalf("saved config = %+v, %v", saved, err)
	}
	h.press("esc")
	if !strings.Contains(h.view(), "vps") {
		t.Fatal("vps not on the menu")
	}
}

func TestAddInstanceRejectsDuplicate(t *testing.T) {
	h := newHarness(t, home)
	h.press("down", "enter")
	h.typeText("HOME")
	h.press("tab")
	h.typeText("http://other.lan")
	h.press("enter")
	if !strings.Contains(h.view(), `already an instance called "home"`) {
		t.Fatalf("view:\n%s", h.view())
	}
	if _, err := os.Stat(h.cfgPath); err == nil {
		t.Fatal("config written for a rejected instance")
	}
}

func TestHelpAndQuit(t *testing.T) {
	h := newHarness(t, home)
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
	h := newHarness(t, home)
	h.press("down", "enter") // the add form
	_, cmd := h.m.Update(keyMsg("q"))
	if cmd != nil {
		if _, ok := cmd().(tea.QuitMsg); ok {
			t.Fatal("q quit from a form")
		}
	}
}

func TestMenuFitsNarrowTerminal(t *testing.T) {
	h := newHarness(t, home, vps)
	h.send(tea.WindowSizeMsg{Width: 60, Height: 24})
	assertFits(t, h.view(), 60)
}
