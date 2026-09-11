package kuma

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// fakeKuma is a Socket.IO server that answers like Kuma does, enough for the
// client to be tested without a real one.
type fakeKuma struct {
	t   *testing.T
	srv *httptest.Server

	// handle answers a call; returning nil sends no ack.
	handle func(event string, args []json.RawMessage) any
	// slowStart is how long, after the connect, the server takes to set up
	// its handlers, as Kuma does; calls in that window are dropped.
	slowStart time.Duration

	mu     sync.Mutex
	conns  []*websocket.Conn
	calls  []string // event names received, in order
	frames []string // every frame received, in order
}

func newFakeKuma(t *testing.T, handle func(event string, args []json.RawMessage) any) *fakeKuma {
	t.Helper()
	return newSlowFakeKuma(t, 0, handle)
}

func newSlowFakeKuma(t *testing.T, slowStart time.Duration, handle func(event string, args []json.RawMessage) any) *fakeKuma {
	t.Helper()
	f := &fakeKuma{t: t, handle: handle, slowStart: slowStart}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeKuma) URL() string { return f.srv.URL }

func (f *fakeKuma) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/socket.io/" || r.URL.Query().Get("EIO") != "4" {
		http.NotFound(w, r)
		return
	}
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	f.mu.Lock()
	f.conns = append(f.conns, ws)
	f.mu.Unlock()

	ctx := context.Background()
	ws.Write(ctx, websocket.MessageText, []byte(`0{"sid":"s","upgrades":[],"pingInterval":25000,"pingTimeout":20000,"maxPayload":1000000}`))
	var listening time.Time // when the handlers are in place
	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			return
		}
		frame := string(data)
		f.mu.Lock()
		f.frames = append(f.frames, frame)
		f.mu.Unlock()

		p, err := decode(frame)
		if err != nil {
			continue
		}
		if frame == frameConnect {
			ws.Write(ctx, websocket.MessageText, []byte(`40{"sid":"n"}`))
			ws.Write(ctx, websocket.MessageText, []byte(`42["info",{"primaryBaseURL":null}]`))
			listening = time.Now().Add(f.slowStart)
			go func() {
				time.Sleep(f.slowStart)
				ws.Write(ctx, websocket.MessageText, []byte(`42["loginRequired"]`))
			}()
			continue
		}
		if p.kind != kindEvent {
			continue
		}
		f.mu.Lock()
		f.calls = append(f.calls, p.event)
		f.mu.Unlock()

		id, _, _ := splitID(frame[2:])
		if f.handle == nil || time.Now().Before(listening) {
			continue // like Socket.IO with no listener yet: no ack
		}
		if out := f.handle(p.event, p.args); out != nil {
			b, _ := json.Marshal([]any{out})
			ws.Write(ctx, websocket.MessageText, []byte("43"+strconv.Itoa(id)+string(b)))
		}
	}
}

// push sends a raw frame to every client connected so far.
func (f *fakeKuma) push(frame string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, ws := range f.conns {
		ws.Write(context.Background(), websocket.MessageText, []byte(frame))
	}
}

// drop closes every connection, as a server restart would.
func (f *fakeKuma) drop() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, ws := range f.conns {
		ws.CloseNow()
	}
	f.conns = nil
}

func (f *fakeKuma) connCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.conns)
}

func (f *fakeKuma) received(frame string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, fr := range f.frames {
		if fr == frame {
			return true
		}
	}
	return false
}

func (f *fakeKuma) called(event string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c == event {
			return true
		}
	}
	return false
}

// eventually polls cond for up to two seconds.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// kumaLogin answers login and loginByToken like Kuma does for user "admin",
// password "pw", 2FA code "123456" when twoFA is on, token "jwt".
func kumaLogin(twoFA bool) func(string, []json.RawMessage) any {
	return func(event string, args []json.RawMessage) any {
		switch event {
		case "login":
			var d struct{ Username, Password, Token string }
			json.Unmarshal(args[0], &d)
			if d.Username != "admin" || d.Password != "pw" {
				return map[string]any{"ok": false, "msg": "authIncorrectCreds", "msgi18n": true}
			}
			if twoFA && d.Token == "" {
				return map[string]any{"tokenRequired": true}
			}
			if twoFA && d.Token != "123456" {
				return map[string]any{"ok": false, "msg": "authInvalidToken", "msgi18n": true}
			}
			return map[string]any{"ok": true, "token": "jwt"}
		case "loginByToken":
			var tok string
			json.Unmarshal(args[0], &tok)
			if tok != "jwt" {
				return map[string]any{"ok": false, "msg": "authInvalidToken", "msgi18n": true}
			}
			return map[string]any{"ok": true}
		case "pauseMonitor":
			return map[string]any{"ok": true, "msg": "successPaused", "msgi18n": true}
		case "resumeMonitor":
			return map[string]any{"ok": true, "msg": "successResumed", "msgi18n": true}
		}
		return nil
	}
}
