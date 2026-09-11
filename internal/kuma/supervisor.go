package kuma

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

// Supervisor keeps one instance connected: it logs in with the stored token,
// passes on what the server says, and reconnects when the link drops.
type Supervisor struct {
	url   string
	token func() string
	out   func(Event)

	// backoff is the wait before reconnect attempt n (0-based).
	backoff func(attempt int) time.Duration
	// dialTimeout bounds Dial: a server that accepts the websocket but never
	// sends loginRequired must not leave the instance "connecting" forever.
	dialTimeout time.Duration

	kick chan struct{}

	mu   sync.Mutex
	sess *Session
}

// NewSupervisor watches the Kuma at url. token returns the stored token, or
// "" when there is none; it is asked again on every attempt, so a token
// saved after a login is picked up by Retry. out receives every event, from
// the goroutine running Run.
func NewSupervisor(url string, token func() string, out func(Event)) *Supervisor {
	return &Supervisor{
		url:         url,
		token:       token,
		out:         out,
		backoff:     defaultBackoff,
		dialTimeout: 30 * time.Second,
		kick:        make(chan struct{}, 1),
	}
}

// defaultBackoff is 1, 2, 4, 8, 16 seconds, then 30.
func defaultBackoff(attempt int) time.Duration {
	if attempt >= 5 {
		return 30 * time.Second
	}
	return time.Second << attempt
}

// Retry wakes the Supervisor now: after a login has stored a new token, or
// to cut a backoff short.
func (s *Supervisor) Retry() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// Run connects and reconnects until ctx ends.
func (s *Supervisor) Run(ctx context.Context) {
	attempt := 0
	for ctx.Err() == nil {
		tok := s.token()
		if tok == "" {
			s.out(AuthFailed{NoToken: true})
			if !s.wait(ctx, 0) {
				return
			}
			continue
		}

		s.out(Connecting{})
		dctx, cancel := context.WithTimeout(ctx, s.dialTimeout)
		sess, err := Dial(dctx, s.url)
		cancel()
		if err == nil {
			if err = sess.LoginByToken(ctx, tok); err != nil {
				sess.Close()
			}
		}
		if IsAuth(err) {
			var r *ReplyError
			errors.As(err, &r)
			s.out(AuthFailed{Msg: r.Msg})
			if !s.wait(ctx, 0) {
				return
			}
			continue
		}
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			s.out(Disconnected{Err: err})
			if !s.wait(ctx, s.backoff(attempt)) {
				return
			}
			attempt++
			continue
		}

		attempt = 0
		// The session is set before Connected goes out, so a pause right
		// after it does not race a caller into ErrNotConnected.
		s.setSession(sess)
		s.out(Connected{})
		unsupported := s.forward(ctx, sess)
		sess.Close() // idempotent: released whether the server or ctx ended it
		s.setSession(nil)
		if ctx.Err() != nil {
			return
		}
		if unsupported {
			// Nothing to retry until the server is upgraded.
			if !s.wait(ctx, 0) {
				return
			}
			continue
		}
		s.out(Disconnected{Err: sess.Err()})
		if !s.wait(ctx, s.backoff(attempt)) {
			return
		}
		attempt++
	}
}

// forward passes the session's events on until it ends. It reports true when
// it closed the session because the server is not Kuma v2.
func (s *Supervisor) forward(ctx context.Context, sess *Session) (unsupported bool) {
	for {
		select {
		case <-ctx.Done():
			sess.Close()
			return false
		case ev, ok := <-sess.Events():
			if !ok {
				return false
			}
			if info, isInfo := ev.(Info); isInfo && info.Version != "" && !strings.HasPrefix(info.Version, "2.") {
				sess.Close()
				s.out(Unsupported{Version: info.Version})
				return true
			}
			s.out(ev)
		}
	}
}

// wait sleeps for d, or until Retry when d is 0. It is false when ctx ended.
func (s *Supervisor) wait(ctx context.Context, d time.Duration) bool {
	var timer <-chan time.Time
	if d > 0 {
		t := time.NewTimer(d)
		defer t.Stop()
		timer = t.C
	}
	select {
	case <-ctx.Done():
		return false
	case <-s.kick:
		return true
	case <-timer:
		return true
	}
}

func (s *Supervisor) setSession(sess *Session) {
	s.mu.Lock()
	s.sess = sess
	s.mu.Unlock()
}

func (s *Supervisor) session() *Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sess
}

// Pause stops a monitor on the instance; ErrNotConnected while it is offline.
func (s *Supervisor) Pause(ctx context.Context, id int) error {
	sess := s.session()
	if sess == nil {
		return ErrNotConnected
	}
	return sess.Pause(ctx, id)
}

// Resume starts a paused monitor again; ErrNotConnected while offline.
func (s *Supervisor) Resume(ctx context.Context, id int) error {
	sess := s.session()
	if sess == nil {
		return ErrNotConnected
	}
	return sess.Resume(ctx, id)
}
