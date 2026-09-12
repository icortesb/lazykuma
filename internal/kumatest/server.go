// Package kumatest is a fake Uptime Kuma server: enough of its Socket.IO
// API, and enough of its quirks, to test a client without a container.
package kumatest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// fakeKuma is a Socket.IO server that answers like Kuma does, enough for the
// client to be tested without a real one.
type Server struct {
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

func New(t *testing.T, handle func(event string, args []json.RawMessage) any) *Server {
	t.Helper()
	return NewSlow(t, 0, handle)
}

func NewSlow(t *testing.T, slowStart time.Duration, handle func(event string, args []json.RawMessage) any) *Server {
	t.Helper()
	f := &Server{t: t, handle: handle, slowStart: slowStart}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *Server) URL() string { return f.srv.URL }

func (f *Server) serve(w http.ResponseWriter, r *http.Request) {
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

		event, ackID, args, isEvent := parse(frame)
		if frame == "40" {
			ws.Write(ctx, websocket.MessageText, []byte(`40{"sid":"n"}`))
			ws.Write(ctx, websocket.MessageText, []byte(`42["info",{"primaryBaseURL":null}]`))
			listening = time.Now().Add(f.slowStart)
			go func() {
				time.Sleep(f.slowStart)
				ws.Write(ctx, websocket.MessageText, []byte(`42["loginRequired"]`))
			}()
			continue
		}
		if !isEvent {
			continue
		}
		f.mu.Lock()
		f.calls = append(f.calls, event)
		f.mu.Unlock()

		if f.handle == nil || time.Now().Before(listening) {
			continue // like Socket.IO with no listener yet: no ack
		}
		if out := f.handle(event, args); out != nil {
			b, _ := json.Marshal([]any{out})
			ws.Write(ctx, websocket.MessageText, []byte("43"+strconv.Itoa(ackID)+string(b)))
		}
	}
}

// push sends a raw frame to every client connected so far.
func (f *Server) Push(frame string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, ws := range f.conns {
		ws.Write(context.Background(), websocket.MessageText, []byte(frame))
	}
}

// drop closes every connection, as a server restart would.
func (f *Server) Drop() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, ws := range f.conns {
		ws.CloseNow()
	}
	f.conns = nil
}

func (f *Server) ConnCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.conns)
}

func (f *Server) Received(frame string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, fr := range f.frames {
		if fr == frame {
			return true
		}
	}
	return false
}

func (f *Server) Called(event string) bool {
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
func Eventually(t *testing.T, what string, cond func() bool) {
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
func Login(twoFA bool) func(string, []json.RawMessage) any {
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

// parse reads a client frame: 42<ackID>["event",args…].
func parse(frame string) (event string, ackID int, args []json.RawMessage, ok bool) {
	if len(frame) < 3 || frame[:2] != "42" {
		return "", 0, nil, false
	}
	body := frame[2:]
	i := strings.IndexByte(body, '[')
	if i < 0 {
		return "", 0, nil, false
	}
	if i > 0 {
		ackID, _ = strconv.Atoi(body[:i])
	}
	var arr []json.RawMessage
	if err := json.Unmarshal([]byte(body[i:]), &arr); err != nil || len(arr) == 0 {
		return "", 0, nil, false
	}
	if err := json.Unmarshal(arr[0], &event); err != nil {
		return "", 0, nil, false
	}
	return event, ackID, arr[1:], true
}

// Frames is every frame the server received, in order.
func (f *Server) Frames() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.frames...)
}

// Sent reports whether any frame received contains want, so a test can pin a
// payload without pinning its ack id.
func (f *Server) Sent(want string) bool {
	for _, fr := range f.Frames() {
		if strings.Contains(fr, want) {
			return true
		}
	}
	return false
}

// Close stops the server, as an outage would.
func (f *Server) Close() { f.srv.Close() }
