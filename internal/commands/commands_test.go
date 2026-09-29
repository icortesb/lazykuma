package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/icortesb/lazykuma/internal/config"
	"github.com/icortesb/lazykuma/internal/core"
	"github.com/icortesb/lazykuma/internal/kumatest"
	"github.com/icortesb/lazykuma/internal/notify"
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

// statusJSON is the report Status prints with --json.
type statusJSON struct {
	Text, Tooltip, Class string
	Up, Down, Pending    int
}

func runStatusJSON(t *testing.T, c *core.Core) statusJSON {
	t.Helper()
	var out bytes.Buffer
	// A status bar hides a module whose command fails: always 0.
	if code := Status(context.Background(), c, 3*time.Second, true, &out); code != ExitUp {
		t.Fatalf("exit %d with --json, output %q", code, out.String())
	}
	var got statusJSON
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %q", out.String())
	}
	return got
}

func TestStatusSomethingDown(t *testing.T) {
	c, fakes := running(t, "", "home")
	waitConnected(t, c, "home")
	monitors(fakes["home"], map[int]string{1: "web", 2: "shop.example.com"})
	beat(fakes["home"], 1, 1, 1, "200 - OK")
	beat(fakes["home"], 2, 0, 2, "connect: connection refused")

	var out bytes.Buffer
	if code := Status(context.Background(), c, 3*time.Second, false, &out); code != ExitDown {
		t.Fatalf("exit %d, output %q", code, out.String())
	}
	if want := "1 down: shop.example.com\n  shop.example.com: connect: connection refused\n"; out.String() != want {
		t.Fatalf("output = %q, want %q", out.String(), want)
	}

	got := runStatusJSON(t, c)
	if got.Text != "1 down: shop.example.com" || got.Class != "down" || got.Up != 1 || got.Down != 1 {
		t.Fatalf("report = %+v", got)
	}
	if !strings.Contains(got.Tooltip, "connection refused") {
		t.Errorf("tooltip = %q", got.Tooltip)
	}
}

func TestStatusDoesNotCountGroups(t *testing.T) {
	c, fakes := running(t, "", "home")
	waitConnected(t, c, "home")
	fakes["home"].Push(`42["monitorList",{"1":{"id":1,"name":"Shop","type":"group","active":true},"2":{"id":2,"name":"web","type":"http","parent":1,"active":true}}]`)
	beat(fakes["home"], 1, 0, 1, "Child inaccessible")
	beat(fakes["home"], 2, 0, 2, "connect ECONNREFUSED")

	var out bytes.Buffer
	if code := Status(context.Background(), c, 3*time.Second, false, &out); code != ExitDown {
		t.Fatalf("exit %d, output %q", code, out.String())
	}
	if !strings.HasPrefix(out.String(), "1 down: web\n") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestStatusEscapesMarkup(t *testing.T) {
	c, fakes := running(t, "", "home")
	waitConnected(t, c, "home")
	monitors(fakes["home"], map[int]string{1: "R&D <api>"})
	beat(fakes["home"], 1, 0, 1, "<html>502 Bad Gateway</html>")

	got := runStatusJSON(t, c)
	if got.Text != "1 down: R&amp;D &lt;api&gt;" {
		t.Errorf("text = %q", got.Text)
	}
	if got.Tooltip != "R&amp;D &lt;api&gt;: &lt;html&gt;502 Bad Gateway&lt;/html&gt;" {
		t.Errorf("tooltip = %q", got.Tooltip)
	}
}

func TestStatusCountsPending(t *testing.T) {
	c, fakes := running(t, "", "home")
	waitConnected(t, c, "home")
	monitors(fakes["home"], map[int]string{1: "web", 2: "db"})
	beat(fakes["home"], 1, 1, 1, "200 - OK")
	beat(fakes["home"], 2, 2, 2, "retrying") // 2 is pending

	got := runStatusJSON(t, c)
	if got.Text != "1 up, 1 pending" || got.Class != "up" || got.Pending != 1 {
		t.Fatalf("report = %+v", got)
	}
}

func TestStatusNotLoggedInIsNotAnAlarm(t *testing.T) {
	c, fakes := running(t, "", "home")
	// An instance added but never logged in to.
	if _, err := c.Add(config.Instance{Name: "lab", URL: kumatest.New(t, nil).URL()}); err != nil {
		t.Fatal(err)
	}
	waitConnected(t, c, "home")
	monitors(fakes["home"], map[int]string{1: "web"})
	beat(fakes["home"], 1, 1, 1, "200 - OK")

	got := runStatusJSON(t, c)
	if got.Text != "1 up" || got.Class != "up" || !strings.Contains(got.Tooltip, "lab: not logged in") {
		t.Fatalf("report = %+v", got)
	}
	var out bytes.Buffer
	if code := Status(context.Background(), c, 3*time.Second, false, &out); code != ExitUp {
		t.Fatalf("exit %d, output %q", code, out.String())
	}
	if out.String() != "1 up\n  lab: not logged in\n" {
		t.Errorf("output = %q", out.String())
	}
}

func TestStatusNobodyLoggedIn(t *testing.T) {
	c, _ := running(t, "")
	if _, err := c.Add(config.Instance{Name: "lab", URL: kumatest.New(t, nil).URL()}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code := Status(context.Background(), c, 3*time.Second, false, &out); code != ExitUnreachable {
		t.Fatalf("exit %d, output %q", code, out.String())
	}
	if out.String() != "not logged in\n  lab: not logged in\n" {
		t.Errorf("output = %q", out.String())
	}
}

func TestFailedStillFeedsTheBar(t *testing.T) {
	var out bytes.Buffer
	if code := Failed(true, fmt.Errorf("config.toml: line 3: expected '='"), &out); code != 0 {
		t.Fatalf("exit %d with --json", code)
	}
	var got statusJSON
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || got.Class != "unreachable" || !strings.Contains(got.Tooltip, "line 3") {
		t.Fatalf("output %q, %v", out.String(), err)
	}
	out.Reset()
	if code := Failed(false, fmt.Errorf("x"), &out); code != ExitUnreachable || out.Len() != 0 {
		t.Fatalf("plain: exit %d, output %q (the error goes to stderr)", code, out.String())
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
	// Close alone waits for open connections to end; drop the one the
	// core may already hold, so the instance really is unreachable.
	fakes["home"].Drop()
	fakes["home"].Close()

	var out bytes.Buffer
	if code := Status(context.Background(), c, 2*time.Second, false, &out); code != ExitUnreachable {
		t.Fatalf("exit %d, output %q", code, out.String())
	}
	if !strings.Contains(out.String(), "unreachable") {
		t.Errorf("output = %q", out.String())
	}
}

func TestStatusExplainsABrokenConfig(t *testing.T) {
	c, _ := running(t, "[[instance\n")
	got := runStatusJSON(t, c)
	if got.Text != "no instances" || !strings.Contains(got.Tooltip, "config: ") {
		t.Fatalf("report = %+v", got)
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

// seen is what Watch has finished with, so a test waits for Watch itself
// rather than for a while: once it has seen a state, whatever that state
// was going to send has been sent.
type seen struct {
	mu    sync.Mutex
	last  map[string]state.Instance
	ticks int
}

// source is src reporting to s.
func (s *seen) source(src WatchSource) WatchSource {
	src.observed = func(name string, st state.Instance) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.last == nil {
			s.last = map[string]state.Instance{}
		}
		s.last[name] = st
	}
	src.ticked = func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.ticks++
	}
	return src
}

// until waits for Watch to have seen name in a state that satisfies cond.
func (s *seen) until(t *testing.T, what, name string, cond func(state.Instance) bool) {
	t.Helper()
	eventually(t, what, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		st, ok := s.last[name]
		return ok && cond(st)
	})
}

// ticked waits for n more recheck ticks.
func (s *seen) ticked(t *testing.T, n int) {
	t.Helper()
	s.mu.Lock()
	want := s.ticks + n
	s.mu.Unlock()
	eventually(t, fmt.Sprint(n, " ticks"), func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.ticks >= want
	})
}

// beatAt is whether monitor id's last beat is the one beat pushed at second.
func beatAt(id, second int) func(state.Instance) bool {
	return func(st state.Instance) bool {
		b, ok := st.Monitors[id].Last()
		return ok && b.Time.Second() == second
	}
}

func listed(st state.Instance) bool { return st.Listed }

func TestWatchReportsAnOutageOnce(t *testing.T) {
	c, fakes := running(t, "", "home")
	var out syncBuffer
	sender := &recorder{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	w := &seen{}
	go func() { done <- Watch(ctx, w.source(fixed(c)), sender, &out, func() time.Time { return t0 }) }()

	waitConnected(t, c, "home")
	monitors(fakes["home"], map[int]string{1: "web"})
	beat(fakes["home"], 1, 1, 1, "200 - OK") // the starting point: no alert
	w.until(t, "the first beat", "home", beatAt(1, 1))
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
	w.until(t, "the recovery", "home", beatAt(1, 3))
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
	w := &seen{}
	go Watch(ctx, w.source(fixed(c)), sender, &out, func() time.Time { return t0 })

	waitConnected(t, c, "home")
	monitors(fakes["home"], map[int]string{1: "web"})
	beat(fakes["home"], 1, 0, 1, "down")
	w.until(t, "the baseline", "home", beatAt(1, 1))
	beat(fakes["home"], 1, 1, 2, "200 - OK")
	eventually(t, "the recovery line", func() bool { return strings.Contains(out.String(), "back  home / web") })
	if len(sender.all()) != 0 {
		t.Errorf("sent with notify.watch = false: %v", sender.all())
	}
	if !strings.Contains(out.String(), "printed only") {
		t.Errorf("the header does not say notifications are off: %q", out.String())
	}
}

func TestWatchReportsARefusedTokenAfterTheGracePeriod(t *testing.T) {
	// Once Kuma refuses the token the supervisor stops trying, so no update
	// will come to carry the instance past the grace period: the recheck
	// must.
	defer func(d time.Duration) { recheck = d }(recheck)
	recheck = 20 * time.Millisecond

	var refuse atomic.Bool
	login := kumatest.Login(false)
	f := kumatest.New(t, func(event string, args []json.RawMessage) any {
		if event == "loginByToken" && refuse.Load() {
			return map[string]any{"ok": false, "msg": "authInvalidToken", "msgi18n": true}
		}
		return login(event, args)
	})
	c, _ := running(t, "", "spare") // a core with an instance, to add ours to
	if _, err := c.Add(config.Instance{Name: "home", URL: f.URL()}); err != nil {
		t.Fatal(err)
	}
	if err := c.SetToken("home", f.URL(), "jwt"); err != nil {
		t.Fatal(err)
	}

	var clock atomic.Int64
	clock.Store(t0.UnixNano())
	now := func() time.Time { return time.Unix(0, clock.Load()) }
	var out syncBuffer
	sender := &recorder{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &seen{}
	go Watch(ctx, w.source(fixed(c)), sender, &out, now)

	waitConnected(t, c, "home")
	monitors(f, map[int]string{1: "web"})
	w.until(t, "the monitor list", "home", listed)

	refuse.Store(true)
	f.Drop()
	// Once Watch has seen the refusal, the clock starts.
	w.until(t, "the refusal", "home", func(st state.Instance) bool { return st.Conn == state.ConnBadCred })
	if len(sender.all()) != 0 {
		t.Fatalf("reported before the grace period: %v", sender.all())
	}
	clock.Add(int64(notify.Grace))
	eventually(t, "the notification", func() bool { return len(sender.all()) == 1 })
	if got := sender.all()[0]; got != "✖ home is unreachable | Kuma refused the login token; log in again" {
		t.Fatalf("sent %q", got)
	}
	if strings.Count(out.String(), "home: connecting") != 0 {
		t.Errorf("connecting printed: %q", out.String())
	}
	if !strings.Contains(out.String(), "home: bad cred") {
		t.Errorf("the refusal was not printed: %q", out.String())
	}
}

// fixed is a source for a core the test already runs, whose config never
// changes.
func fixed(c *core.Core) WatchSource {
	return WatchSource{
		Open:  func(context.Context) (*core.Core, error) { return c, nil },
		Stamp: func() string { return "" },
	}
}

// files is a source over a real config and token file, as main builds it,
// that also keeps the core it opened last so a test can wait on it.
type files struct {
	cfgPath, tokPath string
	mu               sync.Mutex
	c                *core.Core
}

func newFiles(t *testing.T) *files {
	dir := t.TempDir()
	return &files{cfgPath: filepath.Join(dir, "config.toml"), tokPath: filepath.Join(dir, "tokens.json")}
}

func (f *files) source() WatchSource {
	return WatchSource{
		Open: func(ctx context.Context) (*core.Core, error) {
			c, err := core.Open(f.cfgPath, f.tokPath, core.Options{Now: func() time.Time { return t0 }})
			if err != nil {
				return nil, err
			}
			c.Run(ctx)
			f.mu.Lock()
			f.c = c
			f.mu.Unlock()
			return c, nil
		},
		Stamp: func() string { return config.Stamp(f.cfgPath, f.tokPath) },
		Poll:  20 * time.Millisecond,
	}
}

func (f *files) core() *core.Core {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.c
}

// add logs in to a new fake Kuma and appends it to the config. The token
// goes first, so a reload between the two writes never sees the instance
// logged out.
func (f *files) add(t *testing.T, name string) *kumatest.Server {
	t.Helper()
	fake := kumatest.New(t, kumatest.Login(false))
	tokens, err := config.LoadTokens(f.tokPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := tokens.Set(name, fake.URL(), "jwt"); err != nil {
		t.Fatal(err)
	}
	if err := config.AppendInstance(f.cfgPath, config.Instance{Name: name, URL: fake.URL()}); err != nil {
		t.Fatal(err)
	}
	return fake
}

// remove drops an instance from the config.
func (f *files) remove(t *testing.T, name string) {
	t.Helper()
	cfg, _, err := config.Load(f.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Instances = slices.DeleteFunc(cfg.Instances, func(in config.Instance) bool { return in.Name == name })
	if err := config.Save(f.cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
}

// connected waits for a core other than old to have name connected, and
// returns it.
func (f *files) connected(t *testing.T, old *core.Core, name string) *core.Core {
	t.Helper()
	var c *core.Core
	eventually(t, name+" connected in a new core", func() bool {
		c = f.core()
		if c == nil || c == old {
			return false
		}
		in, ok := c.Instance(name)
		return ok && in.State().Conn == state.ConnOK
	})
	return c
}

// reloaded waits for Watch to print header, and returns the core it opened
// last. Adding an instance writes the tokens and then the config, and a
// reload may land between the two: only the header shows which core is the
// final one.
func (f *files) reloaded(t *testing.T, out *syncBuffer, header string) *core.Core {
	t.Helper()
	eventually(t, "the header "+header, func() bool { return strings.Contains(out.String(), "  "+header) })
	return f.core()
}

// watching runs Watch on src until the test ends.
func watching(t *testing.T, src WatchSource, sender notify.Sender, out io.Writer) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Watch(ctx, src, sender, out, func() time.Time { return t0 }) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
}

func TestWatchReloadPicksUpAnAddedInstance(t *testing.T) {
	f := newFiles(t)
	f.add(t, "home")
	var out syncBuffer
	sender := &recorder{}
	w := &seen{}
	watching(t, w.source(f.source()), sender, &out)
	f.connected(t, nil, "home")
	eventually(t, "the header", func() bool {
		return strings.Contains(out.String(), t0.Local().Format("2006-01-02 15:04:05")+"  watching home; notifying on outages\n")
	})

	away := f.add(t, "away")
	c := f.reloaded(t, &out, "watching home, away; notifying on outages\n")
	if !strings.Contains(out.String(), t0.Local().Format("2006-01-02 15:04:05")+"  config changed; reloading\n") {
		t.Errorf("the reload was not printed: %q", out.String())
	}
	waitConnected(t, c, "away")

	// The new instance is watched: its outage is reported.
	monitors(away, map[int]string{1: "db"})
	beat(away, 1, 1, 1, "OK")
	w.until(t, "the first beat", "away", beatAt(1, 1))
	beat(away, 1, 0, 2, "timeout")
	eventually(t, "the notification", func() bool { return len(sender.all()) == 1 })
	if got := sender.all()[0]; got != "✖ db is down | on away · timeout" {
		t.Fatalf("sent %q", got)
	}
}

func TestWatchReloadForgetsARemovedInstance(t *testing.T) {
	f := newFiles(t)
	f.add(t, "home")
	f.add(t, "away")
	var out syncBuffer
	watching(t, f.source(), &recorder{}, &out)
	first := f.connected(t, nil, "away")
	eventually(t, "away connected", func() bool { return strings.Count(out.String(), "away: ok") == 1 })

	f.remove(t, "away")
	eventually(t, "the header without away", func() bool { return strings.Contains(out.String(), "watching home; notifying") })
	second := f.core()

	// Added back, it is news again: what was known about it went with it.
	f.add(t, "away")
	f.connected(t, second, "away")
	eventually(t, "away connected again", func() bool { return strings.Count(out.String(), "away: ok") == 2 })
	if first == second {
		t.Fatal("the removal did not reload")
	}
}

func TestWatchReloadDoesNotReportAnOngoingOutageAgain(t *testing.T) {
	f := newFiles(t)
	home := f.add(t, "home")
	var out syncBuffer
	sender := &recorder{}
	w := &seen{}
	watching(t, w.source(f.source()), sender, &out)
	f.connected(t, nil, "home")

	monitors(home, map[int]string{1: "web", 2: "api"})
	beat(home, 1, 1, 1, "OK")
	w.until(t, "the first beat", "home", beatAt(1, 1))
	beat(home, 1, 0, 2, "timeout")
	eventually(t, "the notification", func() bool { return len(sender.all()) == 1 })

	// Any write reloads; nothing about home changed.
	f.add(t, "spare")
	c := f.reloaded(t, &out, "watching home, spare;")
	waitConnected(t, c, "home")
	monitors(home, map[int]string{1: "web", 2: "api"})
	beat(home, 1, 0, 3, "timeout")
	beat(home, 2, 1, 3, "OK")
	w.until(t, "the beats after the reload", "home", func(st state.Instance) bool {
		return beatAt(1, 3)(st) && beatAt(2, 3)(st)
	})

	// A new outage is still reported, and by then web's state after the
	// reload has been seen: it must not have been reported again.
	beat(home, 2, 0, 4, "refused")
	eventually(t, "the api outage", func() bool { return len(sender.all()) == 2 })
	if got := sender.all(); !strings.HasPrefix(got[1], "✖ api is down") {
		t.Fatalf("sent %v", got)
	}
	if n := strings.Count(out.String(), "home: ok"); n != 1 {
		t.Errorf("home's connection printed %d times across the reload: %q", n, out.String())
	}
}

func TestWatchReloadFollowsNotifyOn(t *testing.T) {
	f := newFiles(t)
	home := f.add(t, "home")
	var out syncBuffer
	sender := &recorder{}
	w := &seen{}
	watching(t, w.source(f.source()), sender, &out)
	first := f.connected(t, nil, "home")

	monitors(home, map[int]string{1: "web"})
	beat(home, 1, 1, 1, "OK")
	w.until(t, "the first beat", "home", beatAt(1, 1))
	beat(home, 1, 0, 2, "timeout")
	eventually(t, "the outage", func() bool { return len(sender.all()) == 1 })

	cfg, _, err := config.Load(f.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Notify.On = config.NotifyChanges
	if err := config.Save(f.cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	c := f.reloaded(t, &out, "watching home; notifying on outages and recoveries\n")
	if c == first {
		t.Fatal("the change did not reload")
	}
	waitConnected(t, c, "home")

	// The recovery is news under the new setting.
	monitors(home, map[int]string{1: "web"})
	beat(home, 1, 0, 3, "timeout")
	w.until(t, "the outage after the reload", "home", beatAt(1, 3))
	beat(home, 1, 1, 4, "OK")
	eventually(t, "the recovery", func() bool { return len(sender.all()) == 2 })
	if got := sender.all()[1]; !strings.HasPrefix(got, "✔ web is back") {
		t.Fatalf("sent %v", sender.all())
	}
}

func TestWatchWithoutInstancesWaits(t *testing.T) {
	f := newFiles(t)
	var out syncBuffer
	watching(t, f.source(), &recorder{}, &out)
	eventually(t, "the waiting line", func() bool {
		return strings.Contains(out.String(), "no instances configured; waiting for one to be added\n")
	})

	// A change that still leaves no instances does not repeat it.
	if err := os.WriteFile(f.cfgPath, []byte("[notify]\nwatch = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the reload", func() bool { return strings.Contains(out.String(), "config changed; reloading\n") })
	f.add(t, "home")
	f.connected(t, nil, "home")
	eventually(t, "the header", func() bool { return strings.Contains(out.String(), "watching home; notifying") })
	if n := strings.Count(out.String(), "no instances configured"); n != 1 {
		t.Errorf("the waiting line printed %d times: %q", n, out.String())
	}
}

func TestWatchPrintsConfigWarnings(t *testing.T) {
	f := newFiles(t)
	if err := os.WriteFile(f.cfgPath, []byte("[[instance]]\nname = \"bad\"\nurl = \"ftp://x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out syncBuffer
	watching(t, f.source(), &recorder{}, &out)
	eventually(t, "the warning", func() bool { return strings.Contains(out.String(), "lazykuma: config: ") })
}

func TestWatchFirstLoadErrorIsReturned(t *testing.T) {
	src := WatchSource{
		Open:  func(context.Context) (*core.Core, error) { return nil, errors.New("broken") },
		Stamp: func() string { return "" },
	}
	err := Watch(context.Background(), src, &recorder{}, &bytes.Buffer{}, time.Now)
	if err == nil || err.Error() != "broken" {
		t.Fatalf("err = %v", err)
	}
}

func TestWatchReloadErrorIsRetriedOnTheNextChange(t *testing.T) {
	c, _ := running(t, "", "home")
	var stamp, opens, checks atomic.Int64
	src := WatchSource{
		Open: func(context.Context) (*core.Core, error) {
			if opens.Add(1) == 2 {
				return nil, errors.New("tokens unreadable")
			}
			return c, nil
		},
		Stamp: func() string {
			checks.Add(1)
			return fmt.Sprint(stamp.Load())
		},
		Poll: 20 * time.Millisecond,
	}
	var out syncBuffer
	watching(t, src, &recorder{}, &out)
	eventually(t, "the first header", func() bool { return strings.Count(out.String(), "watching home;") == 1 })

	stamp.Add(1)
	eventually(t, "the error", func() bool { return strings.Contains(out.String(), "lazykuma: tokens unreadable\n") })
	after := checks.Load()
	eventually(t, "more checks", func() bool { return checks.Load() >= after+3 })
	if n := opens.Load(); n != 2 {
		t.Fatalf("opened %d times with no change after the error", n)
	}

	stamp.Add(1)
	eventually(t, "the header again", func() bool { return strings.Count(out.String(), "watching home;") == 2 })
}

func TestWatchFailedReloadKeepsTheGracePeriodWaiting(t *testing.T) {
	// An instance inside its grace period is rechecked on every tick; a
	// reload that fails leaves no core to recheck it against, and that
	// must neither crash nor lose it.
	defer func(d time.Duration) { recheck = d }(recheck)
	recheck = 20 * time.Millisecond

	var refuse atomic.Bool
	login := kumatest.Login(false)
	f := kumatest.New(t, func(event string, args []json.RawMessage) any {
		if event == "loginByToken" && refuse.Load() {
			return map[string]any{"ok": false, "msg": "authInvalidToken", "msgi18n": true}
		}
		return login(event, args)
	})
	c, _ := running(t, "", "spare")
	if _, err := c.Add(config.Instance{Name: "home", URL: f.URL()}); err != nil {
		t.Fatal(err)
	}
	if err := c.SetToken("home", f.URL(), "jwt"); err != nil {
		t.Fatal(err)
	}

	var stamp, opens atomic.Int64
	src := WatchSource{
		Open: func(context.Context) (*core.Core, error) {
			if opens.Add(1) == 2 {
				return nil, errors.New("config unreadable")
			}
			return c, nil
		},
		Stamp: func() string { return fmt.Sprint(stamp.Load()) },
		Poll:  20 * time.Millisecond,
	}
	var clock atomic.Int64
	clock.Store(t0.UnixNano())
	now := func() time.Time { return time.Unix(0, clock.Load()) }
	var out syncBuffer
	sender := &recorder{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	w := &seen{}
	go func() { done <- Watch(ctx, w.source(src), sender, &out, now) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()

	waitConnected(t, c, "home")
	monitors(f, map[int]string{1: "web"})
	w.until(t, "the monitor list", "home", listed)
	refuse.Store(true)
	f.Drop()
	eventually(t, "the refusal printed", func() bool { return strings.Contains(out.String(), "home: bad cred") })

	stamp.Add(1)
	eventually(t, "the reload error", func() bool { return strings.Contains(out.String(), "lazykuma: config unreadable\n") })
	w.ticked(t, 3) // several ticks with no core
	clock.Add(int64(notify.Grace))
	w.ticked(t, 3)
	if len(sender.all()) != 0 {
		t.Fatalf("reported with no core loaded: %v", sender.all())
	}

	stamp.Add(1)
	eventually(t, "the notification after the good reload", func() bool { return len(sender.all()) == 1 })
	if got := sender.all()[0]; got != "✖ home is unreachable | Kuma refused the login token; log in again" {
		t.Fatalf("sent %q", got)
	}
}
