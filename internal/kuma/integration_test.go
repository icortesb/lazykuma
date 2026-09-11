//go:build integration

package kuma

import (
	"context"
	"os"
	"testing"
	"time"
)

// Runs against a real Kuma v2: `make integration` starts one with
// scripts/kuma-up.sh. LAZYKUMA_IT_URL, _USER and _PASS point it elsewhere.
func itEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func TestIntegrationRealKuma(t *testing.T) {
	url := itEnv("LAZYKUMA_IT_URL", "http://localhost:3902")
	user := itEnv("LAZYKUMA_IT_USER", "admin")
	pass := itEnv("LAZYKUMA_IT_PASS", "lazykuma-it-Passw0rd")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// A fresh Kuma has no user yet; setup creates it. Once one exists setup
	// answers not ok, which is fine.
	s0, err := Dial(ctx, url)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	r0, err := s0.call(ctx, "setup", user, pass)
	s0.Close()
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	t.Logf("setup: ok=%v %s", r0.OK, r0.Msg)

	token, err := Login(ctx, url, user, pass, "")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	// Add a monitor on a session of its own, so there is something to watch.
	s, err := Dial(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.LoginByToken(ctx, token); err != nil {
		t.Fatal(err)
	}
	r, err := s.call(ctx, "add", map[string]any{
		"type": "http", "name": "lazykuma-it", "url": "https://example.com",
		"interval": 20, "retryInterval": 20, "maxretries": 0, "method": "GET",
		"accepted_statuscodes": []string{"200-299"}, "notificationIDList": map[string]bool{},
		"conditions": []any{}, "kafkaProducerBrokers": []any{}, "kafkaProducerSaslOptions": map[string]any{},
	})
	if err != nil || !r.OK {
		t.Fatalf("add monitor: %+v %v", r, err)
	}
	s.Close()

	// Now what lazykuma does: a Supervisor on the stored token.
	events := make(chan Event, 1024)
	sup := NewSupervisor(url, func() string { return token }, func(ev Event) { events <- ev })
	go sup.Run(ctx)

	var gotList, gotBeat bool
	monitorID := 0
	for !(gotList && gotBeat) {
		select {
		case ev := <-events:
			switch ev := ev.(type) {
			case MonitorList:
				for id, m := range ev.Monitors {
					if m.Name == "lazykuma-it" {
						gotList, monitorID = true, id
					}
				}
			case Heartbeat:
				gotBeat = true
			case HeartbeatList:
				if len(ev.Beats) > 0 {
					gotBeat = true
				}
			case AuthFailed, Unsupported:
				t.Fatalf("supervisor: %+v", ev)
			}
		case <-ctx.Done():
			t.Fatalf("list %v, beat %v before the deadline", gotList, gotBeat)
		}
	}

	if err := sup.Pause(ctx, monitorID); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if err := sup.Resume(ctx, monitorID); err != nil {
		t.Fatalf("resume: %v", err)
	}
}
