package kuma

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// recorder collects what a Supervisor sends out.
type recorder struct {
	mu     sync.Mutex
	events []Event
}

func (r *recorder) out(ev Event) {
	r.mu.Lock()
	r.events = append(r.events, ev)
	r.mu.Unlock()
}

// kinds is the events so far as their type names, for easy comparison.
func (r *recorder) kinds() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.events))
	for i, ev := range r.events {
		out[i] = fmt.Sprintf("%T", ev)
	}
	return out
}

func (r *recorder) has(kind string) bool {
	for _, k := range r.kinds() {
		if k == kind {
			return true
		}
	}
	return false
}

func (r *recorder) count(kind string) int {
	n := 0
	for _, k := range r.kinds() {
		if k == kind {
			n++
		}
	}
	return n
}

func (r *recorder) last() Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.events) == 0 {
		return nil
	}
	return r.events[len(r.events)-1]
}

// tokenBox is a token that a test can change while the Supervisor runs.
type tokenBox struct {
	mu  sync.Mutex
	tok string
}

func (b *tokenBox) get() string    { b.mu.Lock(); defer b.mu.Unlock(); return b.tok }
func (b *tokenBox) set(tok string) { b.mu.Lock(); b.tok = tok; b.mu.Unlock() }

func runSupervisor(t *testing.T, url string, tok *tokenBox) (*Supervisor, *recorder) {
	t.Helper()
	rec := &recorder{}
	s := NewSupervisor(url, tok.get, rec.out)
	s.backoff = func(int) time.Duration { return 10 * time.Millisecond }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return s, rec
}

func TestSupervisorWithoutTokenWaitsForRetry(t *testing.T) {
	f := newFakeKuma(t, kumaLogin(false))
	tok := &tokenBox{}
	s, rec := runSupervisor(t, f.URL(), tok)

	eventually(t, "AuthFailed", func() bool { return rec.has("kuma.AuthFailed") })
	if af := rec.last().(AuthFailed); !af.NoToken {
		t.Fatalf("got %+v, want NoToken", af)
	}
	if f.connCount() != 0 {
		t.Fatal("dialed without a token")
	}

	tok.set("jwt")
	s.Retry()
	eventually(t, "Connected", func() bool { return rec.has("kuma.Connected") })
}

func TestSupervisorBadToken(t *testing.T) {
	f := newFakeKuma(t, kumaLogin(false))
	_, rec := runSupervisor(t, f.URL(), &tokenBox{tok: "expired"})

	eventually(t, "AuthFailed", func() bool { return rec.has("kuma.AuthFailed") })
	af := rec.last().(AuthFailed)
	if af.NoToken || af.Msg != "authInvalidToken" {
		t.Fatalf("got %+v", af)
	}
	// It must not hammer the server with a token that was refused.
	time.Sleep(100 * time.Millisecond)
	if n := rec.count("kuma.Connecting"); n != 1 {
		t.Fatalf("connected %d times, want 1", n)
	}
}

func TestSupervisorForwardsEventsAndActions(t *testing.T) {
	f := newFakeKuma(t, kumaLogin(false))
	s, rec := runSupervisor(t, f.URL(), &tokenBox{tok: "jwt"})

	eventually(t, "Connected", func() bool { return rec.has("kuma.Connected") })
	f.push(`42["avgPing","1",62]`)
	eventually(t, "AvgPing", func() bool { return rec.has("kuma.AvgPing") })

	if err := s.Pause(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if !f.called("pauseMonitor") {
		t.Fatal("pause not sent")
	}
}

func TestSupervisorReconnects(t *testing.T) {
	f := newFakeKuma(t, kumaLogin(false))
	_, rec := runSupervisor(t, f.URL(), &tokenBox{tok: "jwt"})

	eventually(t, "Connected", func() bool { return rec.has("kuma.Connected") })
	f.drop()
	eventually(t, "Disconnected", func() bool { return rec.has("kuma.Disconnected") })
	eventually(t, "a second Connected", func() bool { return rec.count("kuma.Connected") == 2 })
}

func TestSupervisorServerDown(t *testing.T) {
	f := newFakeKuma(t, nil)
	url := f.URL()
	f.srv.Close() // nothing listens there any more
	_, rec := runSupervisor(t, url, &tokenBox{tok: "jwt"})

	eventually(t, "two attempts", func() bool { return rec.count("kuma.Disconnected") >= 2 })
}

func TestSupervisorRefusesKumaV1(t *testing.T) {
	f := newFakeKuma(t, kumaLogin(false))
	_, rec := runSupervisor(t, f.URL(), &tokenBox{tok: "jwt"})

	eventually(t, "Connected", func() bool { return rec.has("kuma.Connected") })
	f.push(`42["info",{"version":"1.23.16"}]`)
	eventually(t, "Unsupported", func() bool { return rec.has("kuma.Unsupported") })
	if u := rec.last().(Unsupported); u.Version != "1.23.16" {
		t.Fatalf("got %+v", u)
	}
	time.Sleep(100 * time.Millisecond)
	if rec.count("kuma.Connecting") != 1 {
		t.Fatal("reconnected to a v1 server")
	}
}

func TestSupervisorActionsOffline(t *testing.T) {
	s := NewSupervisor("http://127.0.0.1:1", func() string { return "" }, func(Event) {})
	if err := s.Pause(context.Background(), 1); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("err = %v", err)
	}
}

// TestSupervisorDialTimeout is the fix for F4: a server that accepts the
// websocket but never sends loginRequired (or never finishes the handshake)
// must not leave the instance "connecting" forever.
func TestSupervisorDialTimeout(t *testing.T) {
	f := newSlowFakeKuma(t, time.Hour, kumaLogin(false))
	rec := &recorder{}
	s := NewSupervisor(f.URL(), func() string { return "jwt" }, rec.out)
	s.backoff = func(int) time.Duration { return 10 * time.Millisecond }
	s.dialTimeout = 100 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	eventually(t, "Disconnected", func() bool { return rec.has("kuma.Disconnected") })
	eventually(t, "a second Connecting", func() bool { return rec.count("kuma.Connecting") >= 2 })
}

func TestDefaultBackoff(t *testing.T) {
	want := []time.Duration{1, 2, 4, 8, 16, 30, 30}
	for i, w := range want {
		if got := defaultBackoff(i); got != w*time.Second {
			t.Errorf("attempt %d: %v, want %v", i, got, w*time.Second)
		}
	}
}
