package kuma

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/coder/websocket"
)

// ackTimeout is how long a call waits for Kuma's answer. A login on a large
// instance that has been idle can take a while: Kuma works out every
// monitor's uptime before it answers.
const ackTimeout = 30 * time.Second

var (
	// ErrTokenRequired is Kuma asking for the 2FA code: log in again with it.
	ErrTokenRequired = errors.New("kuma: 2FA code required")
	// ErrNotConnected is an action tried while the instance is offline.
	ErrNotConnected = errors.New("kuma: not connected")
)

// ReplyError is Kuma answering a call with {ok:false}.
type ReplyError struct{ Msg string }

func (e *ReplyError) Error() string { return "kuma: " + e.Msg }

// wsaeConnRefused is Windows' WSAECONNREFUSED, which syscall.ECONNREFUSED
// does not match there; the wording check alone would miss a non-English
// Windows.
const wsaeConnRefused = syscall.Errno(10061)

// Brief is a short, human cause for err, meant to fit a menu line even after
// a wide terminal is accounted for: a real dial error can run to 180
// characters ("kuma: failed to WebSocket dial: failed to send handshake
// request: Get ...: dial tcp ...: connect: connection refused"), most of it
// noise once the reader just wants to know why. Never "" for a non-nil err.
func Brief(err error) string {
	// A refused connection is worded differently on every system, and
	// Windows puts the word that matters last ("...the target machine
	// actively refused it"), where a narrow terminal cuts it off.
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, wsaeConnRefused) ||
		strings.Contains(err.Error(), "actively refused") {
		return "connection refused"
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Err != nil {
		return opErr.Err.Error()
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return dnsErr.Err
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timed out"
	}
	var replyErr *ReplyError
	if errors.As(err, &replyErr) {
		return replyErr.Msg
	}
	// Otherwise, the innermost error: whatever %w wrapping added on the way
	// up is our own context, not the reason.
	for {
		next := errors.Unwrap(err)
		if next == nil {
			return err.Error()
		}
		err = next
	}
}

// IsAuth reports whether err means Kuma refused the credentials or the token.
func IsAuth(err error) bool {
	var r *ReplyError
	if !errors.As(err, &r) {
		return false
	}
	switch r.Msg {
	case "authIncorrectCreds", "authInvalidToken", "authUserInactiveOrDeleted":
		return true
	}
	return false
}

// Session is one live connection to one Kuma server.
type Session struct {
	ws     *websocket.Conn
	queue  chan Event    // readLoop to pump
	events chan Event    // pump to Events
	stop   chan struct{} // closed by Close
	once   sync.Once
	ready  chan struct{} // closed when Kuma can take calls
	rOnce  sync.Once
	done   chan struct{} // closed when readLoop has ended
	err    error         // why the session ended; set before done is closed

	mu     sync.Mutex
	nextID int
	acks   map[int]chan []json.RawMessage
}

// socketURL is where Kuma's Socket.IO endpoint lives for a base URL such as
// https://kuma.example.com.
func socketURL(base string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "http", "ws":
		u.Scheme = "ws"
	case "https", "wss":
		u.Scheme = "wss"
	default:
		return "", fmt.Errorf("kuma: %q is not an http(s) URL", base)
	}
	if u.Host == "" {
		return "", fmt.Errorf("kuma: %q has no host", base)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/socket.io/"
	u.RawQuery = "EIO=4&transport=websocket"
	u.Fragment = ""
	return u.String(), nil
}

// Dial connects to the Kuma at baseURL and completes the Socket.IO handshake.
// The session is not logged in yet.
func Dial(ctx context.Context, baseURL string) (*Session, error) {
	u, err := socketURL(baseURL)
	if err != nil {
		return nil, err
	}
	ws, _, err := websocket.Dial(ctx, u, nil)
	if err != nil {
		return nil, fmt.Errorf("kuma: %w", err)
	}
	// The monitor list and the certificates are large; Kuma allows 100 MB.
	ws.SetReadLimit(100 << 20)

	hs, err := handshakeWith(ctx, ws)
	if err != nil {
		ws.Close(websocket.StatusProtocolError, "")
		return nil, err
	}

	s := &Session{
		ws:     ws,
		queue:  make(chan Event),
		events: make(chan Event),
		stop:   make(chan struct{}),
		ready:  make(chan struct{}),
		done:   make(chan struct{}),
		acks:   map[int]chan []json.RawMessage{},
	}
	go s.readLoop(time.Duration(hs.PingInterval+hs.PingTimeout) * time.Millisecond)
	go s.pump()

	// Kuma registers its handlers only after an await on the database, and
	// drops a call that arrives before them, unanswered. It says it is done
	// with loginRequired (or autoLogin when auth is off).
	select {
	case <-s.ready:
		return s, nil
	case <-s.done:
		err := s.Err()
		s.Close()
		return nil, fmt.Errorf("kuma: connection ended before the server was ready: %w", err)
	case <-ctx.Done():
		s.Close()
		return nil, fmt.Errorf("kuma: waiting for the server: %w", ctx.Err())
	}
}

func handshakeWith(ctx context.Context, ws *websocket.Conn) (handshake, error) {
	var hs handshake
	p, err := readPacket(ctx, ws)
	if err != nil {
		return hs, err
	}
	if p.kind != kindOpen {
		return hs, fmt.Errorf("kuma: expected the open packet, got %v", p.kind)
	}
	if err := json.Unmarshal([]byte(p.data), &hs); err != nil {
		return hs, fmt.Errorf("kuma: open packet: %w", err)
	}
	if err := ws.Write(ctx, websocket.MessageText, []byte(frameConnect)); err != nil {
		return hs, fmt.Errorf("kuma: %w", err)
	}
	for {
		p, err := readPacket(ctx, ws)
		if err != nil {
			return hs, err
		}
		switch p.kind {
		case kindConnect:
			return hs, nil
		case kindConnectError:
			return hs, fmt.Errorf("kuma: connect refused: %s", p.data)
		case kindPing:
			ws.Write(ctx, websocket.MessageText, []byte(framePong))
		}
	}
}

func readPacket(ctx context.Context, ws *websocket.Conn) (packet, error) {
	_, data, err := ws.Read(ctx)
	if err != nil {
		return packet{}, fmt.Errorf("kuma: %w", err)
	}
	return decode(string(data))
}

// readLoop answers pings, hands acks to their callers and events to Events,
// until the connection ends. Silence longer than deadline ends it too: the
// server pings every pingInterval and gives up after pingTimeout.
func (s *Session) readLoop(deadline time.Duration) {
	var err error
	defer func() {
		s.mu.Lock()
		s.err = err
		for id, ch := range s.acks {
			close(ch)
			delete(s.acks, id)
		}
		s.mu.Unlock()
		close(s.done)
		close(s.queue)
	}()

	for {
		ctx, cancel := context.WithTimeout(context.Background(), deadline)
		var p packet
		p, err = readPacket(ctx, s.ws)
		cancel()
		if err != nil {
			return
		}
		switch p.kind {
		case kindPing:
			s.ws.Write(context.Background(), websocket.MessageText, []byte(framePong))
		case kindClose:
			err = errors.New("kuma: server closed the connection")
			return
		case kindAck:
			s.mu.Lock()
			ch := s.acks[p.ackID]
			delete(s.acks, p.ackID)
			s.mu.Unlock()
			if ch != nil {
				ch <- p.args
			}
		case kindEvent:
			if p.event == "loginRequired" || p.event == "autoLogin" {
				s.rOnce.Do(func() { close(s.ready) })
			}
			ev, ok, derr := DecodeEvent(p.event, p.args)
			if derr != nil || !ok {
				// A payload we cannot read is dropped: one odd monitor must
				// not take the whole instance down.
				continue
			}
			select {
			case s.queue <- ev:
			case <-s.stop:
				err = errors.New("kuma: session closed")
				return
			}
		}
	}
}

// pump holds the events nobody has taken yet, however many. Kuma pushes the
// whole state of every monitor right after a login and before it answers
// it: were the reader to wait for Events to be read, a login from someone
// not reading them would never see its answer.
func (s *Session) pump() {
	defer close(s.events)
	var pending []Event
	in := s.queue
	for in != nil || len(pending) > 0 {
		var out chan Event
		var next Event
		if len(pending) > 0 {
			out, next = s.events, pending[0]
		}
		select {
		case ev, ok := <-in:
			if !ok {
				in = nil // the reader is done; hand over what is left
				continue
			}
			pending = append(pending, ev)
		case out <- next:
			pending = pending[1:]
		case <-s.stop:
			return
		}
	}
}

// Events yields what the server pushes, decoded, until the session ends;
// then it is closed and Err says why.
func (s *Session) Events() <-chan Event { return s.events }

// Done is closed when the session has ended.
func (s *Session) Done() <-chan struct{} { return s.done }

// Err is why the session ended, or nil while it is alive.
func (s *Session) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Close ends the session and waits for its reader to stop.
func (s *Session) Close() error {
	s.once.Do(func() { close(s.stop) })
	err := s.ws.Close(websocket.StatusNormalClosure, "")
	<-s.done
	return err
}

// emit sends an event and waits for Kuma's answer to it.
func (s *Session) emit(ctx context.Context, event string, args ...any) ([]json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, ackTimeout)
	defer cancel()

	ch := make(chan []json.RawMessage, 1)
	s.mu.Lock()
	if s.err != nil {
		s.mu.Unlock()
		return nil, ErrNotConnected
	}
	s.nextID++
	id := s.nextID
	s.acks[id] = ch
	s.mu.Unlock()

	frame, err := encodeEmit(id, event, args...)
	if err == nil {
		err = s.ws.Write(ctx, websocket.MessageText, []byte(frame))
	}
	if err != nil {
		s.mu.Lock()
		delete(s.acks, id)
		s.mu.Unlock()
		return nil, fmt.Errorf("kuma: %s: %w", event, err)
	}

	select {
	case reply, ok := <-ch:
		if !ok {
			return nil, ErrNotConnected
		}
		return reply, nil
	case <-ctx.Done():
		s.mu.Lock()
		delete(s.acks, id)
		s.mu.Unlock()
		return nil, fmt.Errorf("kuma: %s: %w", event, ctx.Err())
	}
}

// reply is the object Kuma passes to a callback.
type reply struct {
	OK            bool   `json:"ok"`
	Msg           string `json:"msg"`
	Token         string `json:"token"`
	TokenRequired bool   `json:"tokenRequired"`
}

func (s *Session) call(ctx context.Context, event string, args ...any) (reply, error) {
	raw, err := s.emit(ctx, event, args...)
	if err != nil {
		return reply{}, err
	}
	var r reply
	if len(raw) > 0 {
		if err := json.Unmarshal(raw[0], &r); err != nil {
			return reply{}, fmt.Errorf("kuma: %s reply: %w", event, err)
		}
	}
	return r, nil
}

func (r reply) err() error {
	if r.OK {
		return nil
	}
	return &ReplyError{Msg: r.Msg}
}

// Login logs in with a password, and the 2FA code when the user has 2FA on
// (empty otherwise). It returns the token for LoginByToken. A user with 2FA
// and no code gets ErrTokenRequired.
func (s *Session) Login(ctx context.Context, username, password, code string) (string, error) {
	r, err := s.call(ctx, "login", map[string]string{"username": username, "password": password, "token": code})
	if err != nil {
		return "", err
	}
	if r.TokenRequired {
		return "", ErrTokenRequired
	}
	if err := r.err(); err != nil {
		return "", err
	}
	return r.Token, nil
}

// LoginByToken logs in with a token a previous Login returned.
func (s *Session) LoginByToken(ctx context.Context, token string) error {
	r, err := s.call(ctx, "loginByToken", token)
	if err != nil {
		return err
	}
	return r.err()
}

// Pause stops a monitor.
func (s *Session) Pause(ctx context.Context, id int) error {
	r, err := s.call(ctx, "pauseMonitor", id)
	if err != nil {
		return err
	}
	return r.err()
}

// Resume starts a paused monitor again.
func (s *Session) Resume(ctx context.Context, id int) error {
	r, err := s.call(ctx, "resumeMonitor", id)
	if err != nil {
		return err
	}
	return r.err()
}

// Login dials baseURL just to log in, and returns the token.
func Login(ctx context.Context, baseURL, username, password, code string) (string, error) {
	s, err := Dial(ctx, baseURL)
	if err != nil {
		return "", err
	}
	defer s.Close()
	return s.Login(ctx, username, password, code)
}
