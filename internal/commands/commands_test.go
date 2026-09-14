package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/icortesb/lazykuma/internal/config"
	"github.com/icortesb/lazykuma/internal/core"
	"github.com/icortesb/lazykuma/internal/kumatest"
	"github.com/icortesb/lazykuma/internal/state"
)

var t0 = time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)

// running opens a core whose instances each point at their own fake Kuma,
// all logged in, and runs it.
func running(t *testing.T, notifyTOML string, names ...string) (*core.Core, map[string]*kumatest.Server) {
	t.Helper()
	dir := t.TempDir()
	cfgPath, tokPath := filepath.Join(dir, "config.toml"), filepath.Join(dir, "tokens.json")
	if notifyTOML != "" {
		if err := os.WriteFile(cfgPath, []byte(notifyTOML), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tokens, err := config.LoadTokens(tokPath)
	if err != nil {
		t.Fatal(err)
	}
	fakes := map[string]*kumatest.Server{}
	for _, name := range names {
		f := kumatest.New(t, kumatest.Login(false))
		fakes[name] = f
		if err := config.AppendInstance(cfgPath, config.Instance{Name: name, URL: f.URL()}); err != nil {
			t.Fatal(err)
		}
		if err := tokens.Set(name, f.URL(), "jwt"); err != nil {
			t.Fatal(err)
		}
	}
	c, err := core.Open(cfgPath, tokPath, core.Options{Now: func() time.Time { return t0 }})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c.Run(ctx)
	return c, fakes
}

func waitConnected(t *testing.T, c *core.Core, name string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if in, ok := c.Instance(name); ok && in.State().Conn == state.ConnOK {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s never connected", name)
}

// monitors pushes a monitor list, id → name.
func monitors(f *kumatest.Server, names map[int]string) {
	parts := []string{}
	for id, name := range names {
		parts = append(parts, fmt.Sprintf(`"%d":{"id":%d,"name":%q,"type":"http","active":true}`, id, id, name))
	}
	f.Push(`42["monitorList",{` + strings.Join(parts, ",") + `}]`)
}

// beat pushes one heartbeat: status 1 up, 0 down.
func beat(f *kumatest.Server, id, status int, second int, msg string) {
	f.Push(fmt.Sprintf(`42["heartbeat",{"monitorID":%d,"status":%d,"time":"2026-09-14 12:00:%02d.000","msg":%q,"ping":5,"important":true}]`,
		id, status, second, msg))
}

func TestStatusAllUp(t *testing.T) {
	c, fakes := running(t, "", "home")
	waitConnected(t, c, "home")
	monitors(fakes["home"], map[int]string{1: "web", 2: "db"})
	beat(fakes["home"], 1, 1, 1, "200 - OK")
	beat(fakes["home"], 2, 1, 2, "200 - OK")

	var out bytes.Buffer
	if code := Status(context.Background(), c, 3*time.Second, false, &out); code != ExitUp {
		t.Fatalf("exit %d, output %q", code, out.String())
	}
	if strings.TrimSpace(out.String()) != "2 up" {
		t.Fatalf("output = %q", out.String())
	}
}

func TestStatusSomethingDownAsJSON(t *testing.T) {
	c, fakes := running(t, "", "home")
	waitConnected(t, c, "home")
	monitors(fakes["home"], map[int]string{1: "web", 2: "apcbrokers.com.ar"})
	beat(fakes["home"], 1, 1, 1, "200 - OK")
	beat(fakes["home"], 2, 0, 2, "connect: connection refused")

	var out bytes.Buffer
	if code := Status(context.Background(), c, 3*time.Second, true, &out); code != ExitDown {
		t.Fatalf("exit %d, output %q", code, out.String())
	}
	var got struct {
		Text, Tooltip, Class string
		Up, Down             int
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %q", out.String())
	}
	if got.Text != "1 down: apcbrokers.com.ar" || got.Class != "down" || got.Up != 1 || got.Down != 1 {
		t.Fatalf("report = %+v", got)
	}
	if !strings.Contains(got.Tooltip, "connection refused") {
		t.Errorf("tooltip = %q", got.Tooltip)
	}
}

func TestStatusWaitsForTheFirstBeats(t *testing.T) {
	c, fakes := running(t, "", "home")
	waitConnected(t, c, "home")
	monitors(fakes["home"], map[int]string{1: "web"})
	// The beat arrives later: status must wait for it rather than answer
	// "0 up" for a monitor it has no status for yet.
	go func() {
		time.Sleep(300 * time.Millisecond)
		beat(fakes["home"], 1, 0, 1, "timeout")
	}()
	var out bytes.Buffer
	if code := Status(context.Background(), c, 3*time.Second, false, &out); code != ExitDown {
		t.Fatalf("exit %d, output %q", code, out.String())
	}
}

func TestStatusUnreachable(t *testing.T) {
	c, fakes := running(t, "", "home")
	fakes["home"].Close()

	var out bytes.Buffer
	if code := Status(context.Background(), c, 2*time.Second, false, &out); code != ExitUnreachable {
		t.Fatalf("exit %d, output %q", code, out.String())
	}
	if !strings.Contains(out.String(), "unreachable") {
		t.Errorf("output = %q", out.String())
	}
}

func TestStatusWithoutInstances(t *testing.T) {
	c, _ := running(t, "")
	var out bytes.Buffer
	if code := Status(context.Background(), c, time.Second, false, &out); code != ExitUnreachable {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), "no instances") {
		t.Errorf("output = %q", out.String())
	}
}

// recorder is a Sender that remembers what it was asked to show.
type recorder struct {
	mu   sync.Mutex
	sent []string
}

func (r *recorder) Send(title, body string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, title+" | "+body)
	return nil
}

func (r *recorder) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string{}, r.sent...)
}

// syncBuffer is a bytes.Buffer safe to read while Watch writes to it.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestWatchReportsAnOutageOnce(t *testing.T) {
	c, fakes := running(t, "", "home")
	var out syncBuffer
	sender := &recorder{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Watch(ctx, c, sender, &out, func() time.Time { return t0 }) }()

	waitConnected(t, c, "home")
	monitors(fakes["home"], map[int]string{1: "web"})
	beat(fakes["home"], 1, 1, 1, "200 - OK") // the starting point: no alert
	eventually(t, "the first beat", func() bool {
		in, _ := c.Instance("home")
		return in.State().Monitors[1].Status() == state.StatusUp
	})
	time.Sleep(100 * time.Millisecond)
	if len(sender.all()) != 0 {
		t.Fatalf("the starting point raised %v", sender.all())
	}

	beat(fakes["home"], 1, 0, 2, "connect ECONNREFUSED")
	eventually(t, "the notification", func() bool { return len(sender.all()) == 1 })
	if got := sender.all()[0]; got != "✖ web is down | on home · connect ECONNREFUSED" {
		t.Fatalf("sent %q", got)
	}
	if !strings.Contains(out.String(), "down  home / web  connect ECONNREFUSED") {
		t.Errorf("output = %q", out.String())
	}

	// Recoveries are off by default.
	beat(fakes["home"], 1, 1, 3, "200 - OK")
	time.Sleep(200 * time.Millisecond)
	if len(sender.all()) != 1 {
		t.Errorf("a recovery was sent with notify.on = down: %v", sender.all())
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestWatchPrintsOnlyWhenNotificationsAreOff(t *testing.T) {
	c, fakes := running(t, "[notify]\nwatch = false\non = \"changes\"\n", "home")
	var out syncBuffer
	sender := &recorder{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Watch(ctx, c, sender, &out, func() time.Time { return t0 })

	waitConnected(t, c, "home")
	monitors(fakes["home"], map[int]string{1: "web"})
	beat(fakes["home"], 1, 0, 1, "down")
	eventually(t, "the baseline", func() bool {
		in, _ := c.Instance("home")
		return in.State().Monitors[1].Status() == state.StatusDown
	})
	time.Sleep(100 * time.Millisecond)
	beat(fakes["home"], 1, 1, 2, "200 - OK")
	eventually(t, "the recovery line", func() bool { return strings.Contains(out.String(), "back  home / web") })
	if len(sender.all()) != 0 {
		t.Errorf("sent with notify.watch = false: %v", sender.all())
	}
	if !strings.Contains(out.String(), "printed only") {
		t.Errorf("the header does not say notifications are off: %q", out.String())
	}
}

func TestWatchWithoutInstances(t *testing.T) {
	c, _ := running(t, "")
	if err := Watch(context.Background(), c, &recorder{}, &bytes.Buffer{}, time.Now); err == nil {
		t.Fatal("watch with no instances did not complain")
	}
}
