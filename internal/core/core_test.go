package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/icortesb/lazykuma/internal/config"
	"github.com/icortesb/lazykuma/internal/kumatest"
	"github.com/icortesb/lazykuma/internal/state"
)

var t0 = time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)

// kumaFake answers login plus the writes these tests exercise.
func kumaFake(t *testing.T) *kumatest.Server {
	t.Helper()
	login := kumatest.Login(false)
	return kumatest.New(t, func(event string, args []json.RawMessage) any {
		switch event {
		case "add":
			return map[string]any{"ok": true, "msg": "successAdded", "monitorID": 9}
		case "addMaintenance":
			return map[string]any{"ok": true, "msg": "successAdded", "maintenanceID": 4}
		case "addMonitorMaintenance", "deleteMaintenance", "deleteMonitor":
			return map[string]any{"ok": true, "msg": "successDeleted"}
		}
		return login(event, args)
	})
}

// open writes a config and tokens for the given instances and opens a Core.
func open(t *testing.T, tokenFor func(name string) string, instances ...config.Instance) (*Core, string) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	tokPath := filepath.Join(dir, "tokens.json")

	var cfg config.Config
	for _, in := range instances {
		if err := cfg.Add(in); err != nil {
			t.Fatal(err)
		}
	}
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	tokens, err := config.LoadTokens(tokPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range instances {
		if tok := tokenFor(in.Name); tok != "" {
			if err := tokens.Set(in.Name, in.URL, tok); err != nil {
				t.Fatal(err)
			}
		}
	}
	c, err := Open(cfgPath, tokPath, Options{Now: func() time.Time { return t0 }})
	if err != nil {
		t.Fatal(err)
	}
	return c, cfgPath
}

func runCore(t *testing.T, c *Core) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c.Run(ctx)
}

// waitFor polls an instance's state until cond holds.
func waitFor(t *testing.T, c *Core, name, what string, cond func(state.Instance) bool) state.Instance {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		in, ok := c.Instance(name)
		if ok && cond(in.State()) {
			return in.State()
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s on %s", what, name)
	return state.Instance{}
}

func TestCoreConnectsAndPublishes(t *testing.T) {
	f := kumaFake(t)
	c, _ := open(t, func(string) string { return "jwt" }, config.Instance{Name: "home", URL: f.URL()})
	runCore(t, c)

	// The update channel carries full snapshots.
	var got Update
	deadline := time.After(3 * time.Second)
	for {
		select {
		case u := <-c.Updates():
			if u.Instance == "home" && u.State.Conn == state.ConnOK {
				got = u
			}
		case <-deadline:
			t.Fatal("no connected update")
		}
		if got.Instance != "" {
			break
		}
	}
	if c.Snapshot()["home"].Conn != state.ConnOK {
		t.Fatalf("snapshot = %+v", c.Snapshot()["home"])
	}
	f.Push(`42["avgPing","1",62]`)
	waitFor(t, c, "home", "the ping", func(s state.Instance) bool { return s.Monitors[1].HasAvgPing })
}

func TestCoreWithoutTokenWaitsForOne(t *testing.T) {
	f := kumaFake(t)
	c, _ := open(t, func(string) string { return "" }, config.Instance{Name: "home", URL: f.URL()})
	runCore(t, c)
	waitFor(t, c, "home", "no cred", func(s state.Instance) bool { return s.Conn == state.ConnNoCred })

	// Storing a token wakes the connection.
	if err := c.SetToken("home", f.URL(), "jwt"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, c, "home", "connected", func(s state.Instance) bool { return s.Conn == state.ConnOK })
}

func TestCoreActionsReachKuma(t *testing.T) {
	f := kumaFake(t)
	c, _ := open(t, func(string) string { return "jwt" }, config.Instance{Name: "home", URL: f.URL()})
	runCore(t, c)
	waitFor(t, c, "home", "connected", func(s state.Instance) bool { return s.Conn == state.ConnOK })

	in, _ := c.Instance("home")
	ctx := context.Background()
	if id, err := in.AddMonitor(ctx, map[string]any{"type": "http", "name": "web"}); err != nil || id != 9 {
		t.Fatalf("AddMonitor = %d, %v", id, err)
	}
	if err := in.Pause(ctx, 9); err != nil {
		t.Fatal(err)
	}
	if err := in.DeleteMonitor(ctx, 9); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`["add",`, `["pauseMonitor",9]`, `["deleteMonitor",9]`} {
		if !f.Sent(want) {
			t.Errorf("never sent %s", want)
		}
	}
}

func TestSilence(t *testing.T) {
	f := kumaFake(t)
	c, _ := open(t, func(string) string { return "jwt" }, config.Instance{Name: "home", URL: f.URL()})
	runCore(t, c)
	waitFor(t, c, "home", "connected", func(s state.Instance) bool { return s.Conn == state.ConnOK })
	in, _ := c.Instance("home")
	ctx := context.Background()

	// Manual: no dates, silent until it is ended.
	id, err := in.Silence(ctx, "deploy", []int{3}, time.Time{}, time.Time{})
	if err != nil || id != 4 {
		t.Fatalf("Silence(manual) = %d, %v", id, err)
	}
	if !f.Sent(`"strategy":"manual"`) || !f.Sent(`["addMonitorMaintenance",4,[{"id":3}]]`) {
		t.Fatalf("manual maintenance frames: %v", f.Frames())
	}

	// A window: the dates Kuma expects, in the machine's own timezone.
	start := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	if _, err := in.Silence(ctx, "migration", []int{3}, start, start.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if !f.Sent(`"dateRange":["2026-09-12 10:00:00","2026-09-12 12:00:00"]`) {
		t.Fatalf("window frames: %v", f.Frames())
	}

	// A window that ends before it starts is refused before Kuma sees it.
	if _, err := in.Silence(ctx, "bad", []int{3}, start, start.Add(-time.Hour)); err == nil {
		t.Fatal("backwards window accepted")
	}
	if _, err := in.Silence(ctx, "none", nil, time.Time{}, time.Time{}); err == nil {
		t.Fatal("maintenance without monitors accepted")
	}
	if err := in.EndMaintenance(ctx, 4); err != nil {
		t.Fatal(err)
	}
}

func TestAddInstanceKeepsTheConfigAndStarts(t *testing.T) {
	f, f2 := kumaFake(t), kumaFake(t)
	c, cfgPath := open(t, func(string) string { return "jwt" }, config.Instance{Name: "home", URL: f.URL()})
	// A comment and a hand-written entry Load skips: both must survive.
	extra := "\n# hand written\n[[instance]]\nname = \"\"\nurl = \"http://broken.lan\"\n"
	appendFile(t, cfgPath, extra)
	runCore(t, c)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	vps := config.Instance{Name: "vps", URL: f2.URL()}
	if _, err := c.Add(ctx, vps); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Add(ctx, vps); err == nil {
		t.Fatal("the same name was accepted twice")
	}

	saved, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(saved), "# hand written") || !strings.Contains(string(saved), `name = "vps"`) {
		t.Fatalf("config file = %s", saved)
	}
	// A new instance has no token yet: it waits for the login, and the
	// token that login stores wakes it.
	waitFor(t, c, "vps", "no cred", func(s state.Instance) bool { return s.Conn == state.ConnNoCred })
	if err := c.SetToken("vps", vps.URL, "jwt"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, c, "vps", "connected", func(s state.Instance) bool { return s.Conn == state.ConnOK })
	if names := len(c.Instances()); names != 2 {
		t.Fatalf("instances = %d", names)
	}
}

func TestOpenReportsConfigWarnings(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	os.WriteFile(cfgPath, []byte("[[instance]]\nname = \"\"\nurl = \"http://x.lan\"\n"), 0o644)
	c, err := Open(cfgPath, filepath.Join(dir, "tokens.json"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Warnings()) != 1 || len(c.Instances()) != 0 {
		t.Fatalf("warnings = %q, instances = %d", c.Warnings(), len(c.Instances()))
	}
}

func appendFile(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
}
