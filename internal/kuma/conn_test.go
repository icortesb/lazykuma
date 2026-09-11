package kuma

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestSocketURL(t *testing.T) {
	tests := []struct{ in, want string }{
		{"http://localhost:3001", "ws://localhost:3001/socket.io/?EIO=4&transport=websocket"},
		{"https://kuma.example.com/", "wss://kuma.example.com/socket.io/?EIO=4&transport=websocket"},
		{" https://example.com/kuma ", "wss://example.com/kuma/socket.io/?EIO=4&transport=websocket"},
	}
	for _, tt := range tests {
		got, err := socketURL(tt.in)
		if err != nil || got != tt.want {
			t.Errorf("socketURL(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
		}
	}
	for _, bad := range []string{"kuma.lan", "ftp://kuma.lan", "http://"} {
		if _, err := socketURL(bad); err == nil {
			t.Errorf("socketURL(%q) = nil error", bad)
		}
	}
}

func dial(t *testing.T, f *fakeKuma) *Session {
	t.Helper()
	s, err := Dial(context.Background(), f.URL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSessionReceivesEvents(t *testing.T) {
	f := newFakeKuma(t, nil)
	s := dial(t, f)

	f.push(`42["proxyList",[]]`) // not used by the TUI: dropped
	f.push(`42["heartbeat",{"monitorID":3,"status":1,"time":"2026-09-11 00:12:49.103","msg":"200 - OK","ping":5,"important":false}]`)

	for {
		select {
		case ev := <-s.Events():
			if _, isInfo := ev.(Info); isInfo {
				continue // sent on every connect
			}
			hb, ok := ev.(Heartbeat)
			if !ok || hb.Beat.MonitorID != 3 || hb.Beat.Ping != 5 {
				t.Fatalf("got %#v", ev)
			}
			return
		case <-time.After(2 * time.Second):
			t.Fatal("no event")
		}
	}
}

func TestSessionAnswersPing(t *testing.T) {
	f := newFakeKuma(t, nil)
	dial(t, f)
	f.push("2")
	eventually(t, "a pong", func() bool { return f.received("3") })
}

func TestSessionLogin(t *testing.T) {
	ctx := context.Background()

	t.Run("password", func(t *testing.T) {
		s := dial(t, newFakeKuma(t, kumaLogin(false)))
		tok, err := s.Login(ctx, "admin", "pw", "")
		if err != nil || tok != "jwt" {
			t.Fatalf("Login = %q, %v", tok, err)
		}
	})

	t.Run("wrong password", func(t *testing.T) {
		s := dial(t, newFakeKuma(t, kumaLogin(false)))
		_, err := s.Login(ctx, "admin", "nope", "")
		if !IsAuth(err) {
			t.Fatalf("err = %v, want an auth error", err)
		}
	})

	t.Run("2FA", func(t *testing.T) {
		s := dial(t, newFakeKuma(t, kumaLogin(true)))
		if _, err := s.Login(ctx, "admin", "pw", ""); !errors.Is(err, ErrTokenRequired) {
			t.Fatalf("no code: err = %v, want ErrTokenRequired", err)
		}
		if _, err := s.Login(ctx, "admin", "pw", "000000"); !IsAuth(err) {
			t.Fatalf("wrong code: err = %v, want an auth error", err)
		}
		if tok, err := s.Login(ctx, "admin", "pw", "123456"); err != nil || tok != "jwt" {
			t.Fatalf("right code: %q, %v", tok, err)
		}
	})

	t.Run("token", func(t *testing.T) {
		s := dial(t, newFakeKuma(t, kumaLogin(false)))
		if err := s.LoginByToken(ctx, "old"); !IsAuth(err) {
			t.Fatalf("bad token: err = %v, want an auth error", err)
		}
		if err := s.LoginByToken(ctx, "jwt"); err != nil {
			t.Fatalf("good token: %v", err)
		}
	})
}

func TestLoginHelper(t *testing.T) {
	f := newFakeKuma(t, kumaLogin(false))
	tok, err := Login(context.Background(), f.URL(), "admin", "pw", "")
	if err != nil || tok != "jwt" {
		t.Fatalf("Login = %q, %v", tok, err)
	}
}

func TestDialWaitsUntilKumaListens(t *testing.T) {
	// Kuma drops calls made before its handlers are registered; Dial must
	// not return before loginRequired says they are.
	f := newSlowFakeKuma(t, 200*time.Millisecond, kumaLogin(false))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := Login(ctx, f.URL(), "admin", "pw", ""); err != nil {
		t.Fatalf("Login: %v", err)
	}
}

func TestDialGivesUpWhenKumaNeverListens(t *testing.T) {
	f := newSlowFakeKuma(t, time.Hour, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := Dial(ctx, f.URL()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want a deadline", err)
	}
}

func TestDialFailsWhenServerDropsBeforeReady(t *testing.T) {
	f := newSlowFakeKuma(t, time.Hour, nil)
	go func() {
		eventually(t, "the connect packet", func() bool { return f.received(frameConnect) })
		time.Sleep(50 * time.Millisecond) // the fake answers 40 and info, then Dial waits
		f.drop()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s, err := Dial(ctx, f.URL())
	if err == nil || s != nil {
		t.Fatalf("Dial = %v, %v; want an error", s, err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v: Dial waited for the deadline instead of noticing the drop", err)
	}
}

func TestSessionPauseResume(t *testing.T) {
	f := newFakeKuma(t, kumaLogin(false))
	s := dial(t, f)
	ctx := context.Background()
	if err := s.Pause(ctx, 4); err != nil {
		t.Fatal(err)
	}
	if err := s.Resume(ctx, 4); err != nil {
		t.Fatal(err)
	}
	if !f.received(`421["pauseMonitor",4]`) || !f.received(`422["resumeMonitor",4]`) {
		t.Fatalf("frames = %v", f.frames)
	}
}

func TestSessionRefusedAction(t *testing.T) {
	f := newFakeKuma(t, func(string, []json.RawMessage) any {
		return map[string]any{"ok": false, "msg": "You are not logged in."}
	})
	s := dial(t, f)
	err := s.Pause(context.Background(), 1)
	var r *ReplyError
	if !errors.As(err, &r) || r.Msg != "You are not logged in." {
		t.Fatalf("err = %v", err)
	}
	if IsAuth(err) {
		t.Fatal("a refused action is not an auth error")
	}
}

func TestSessionCallTimesOut(t *testing.T) {
	s := dial(t, newFakeKuma(t, nil)) // never acks
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := s.Pause(ctx, 1); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want a deadline", err)
	}
}

func TestSessionEndsWhenServerDrops(t *testing.T) {
	f := newFakeKuma(t, nil)
	s := dial(t, f)
	f.drop()

	select {
	case <-s.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("session still alive")
	}
	// What arrived before the drop is still handed over, then Events closes.
	drained := make(chan struct{})
	go func() {
		for range s.Events() {
		}
		close(drained)
	}()
	select {
	case <-drained:
	case <-time.After(2 * time.Second):
		t.Fatal("Events not closed")
	}
	if s.Err() == nil {
		t.Fatal("Err() = nil after the drop")
	}
	if err := s.Pause(context.Background(), 1); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("Pause after the drop = %v, want ErrNotConnected", err)
	}
}

func TestLoginWhileKumaFloodsEvents(t *testing.T) {
	// Kuma sends every monitor's state before it answers the login; an
	// instance with a few hundred monitors sends thousands of events.
	var f *fakeKuma
	login := kumaLogin(false)
	f = newFakeKuma(t, func(event string, args []json.RawMessage) any {
		if event == "login" || event == "loginByToken" {
			for i := 0; i < 1000; i++ {
				f.push(`42["avgPing","1",62]`)
			}
		}
		return login(event, args)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := Login(ctx, f.URL(), "admin", "pw", ""); err != nil {
		t.Fatalf("Login: %v", err)
	}

	s := dial(t, f)
	if err := s.LoginByToken(ctx, "jwt"); err != nil {
		t.Fatalf("LoginByToken: %v", err)
	}
	n := 0
	for n < 1000 {
		select {
		case <-s.Events():
			n++
		case <-ctx.Done():
			t.Fatalf("only %d of 1000 events arrived", n)
		}
	}
}

func TestCloseDoesNotHangOnUnreadEvents(t *testing.T) {
	f := newFakeKuma(t, nil)
	s, err := Dial(context.Background(), f.URL())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 300; i++ { // more than the Events buffer
		f.push(`42["avgPing","1",62]`)
	}
	done := make(chan struct{})
	go func() { s.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close hung")
	}
}
