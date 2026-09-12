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

	// The writes, on a session of its own.
	w, err := Dial(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := w.LoginByToken(ctx, token); err != nil {
		t.Fatal(err)
	}
	go func() {
		for range w.Events() {
		}
	}()

	id, err := w.AddMonitor(ctx, RawMonitor{
		"type": "http", "name": "lazykuma-writes", "url": "https://example.com", "method": "GET",
		"interval": 60, "retryInterval": 60, "maxretries": 0, "timeout": 48, "maxredirects": 10,
		"accepted_statuscodes": []string{"200-299"}, "expiryNotification": true,
		"notificationIDList": map[string]bool{}, "conditions": []any{},
		"kafkaProducerBrokers": []any{}, "kafkaProducerSaslOptions": map[string]any{},
	})
	if err != nil {
		t.Fatalf("AddMonitor: %v", err)
	}

	full, err := w.GetMonitor(ctx, id)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if len(full) < 50 {
		t.Fatalf("GetMonitor returned %d fields, expected the whole monitor", len(full))
	}
	full["name"] = "lazykuma-writes-renamed"
	if err := w.EditMonitor(ctx, full); err != nil {
		t.Fatalf("EditMonitor: %v", err)
	}
	// Kuma refuses a partial monitor rather than merging it, which is why
	// an edit starts from GetMonitor.
	if err := w.EditMonitor(ctx, RawMonitor{"id": full["id"], "name": "partial", "type": "http"}); err == nil {
		t.Fatal("a partial editMonitor was accepted; the client may stop sending whole monitors")
	}

	cfg := map[string]any{
		"name": "lazykuma-telegram", "type": "telegram", "isDefault": false, "applyExisting": false,
		"telegramBotToken": "123:abc", "telegramChatID": "42", "telegramSendSilently": false,
	}
	nid, err := w.SaveNotification(ctx, cfg, 0)
	if err != nil || nid == 0 {
		t.Fatalf("SaveNotification: %d, %v", nid, err)
	}
	cfg["name"] = "lazykuma-telegram-renamed"
	if _, err := w.SaveNotification(ctx, cfg, nid); err != nil {
		t.Fatalf("SaveNotification(edit): %v", err)
	}
	// A test send reaches Telegram, which rejects the made-up token: the
	// provider's own complaint is what the UI shows.
	if err := w.TestNotification(ctx, cfg); err == nil {
		t.Fatal("TestNotification with a bogus token succeeded")
	}
	if err := w.DeleteNotification(ctx, nid); err != nil {
		t.Fatalf("DeleteNotification: %v", err)
	}

	mid, err := w.AddMaintenance(ctx, map[string]any{
		"title": "lazykuma", "description": "", "strategy": "manual", "active": true,
		"intervalDay": 1, "dateRange": []any{nil},
		"timeRange": []any{map[string]any{"hours": 0, "minutes": 0}, map[string]any{"hours": 0, "minutes": 0}},
		"weekdays":  []any{}, "daysOfMonth": []any{}, "timezoneOption": "SAME_AS_SERVER",
	})
	if err != nil {
		t.Fatalf("AddMaintenance: %v", err)
	}
	if err := w.SetMaintenanceMonitors(ctx, mid, []int{id}); err != nil {
		t.Fatalf("SetMaintenanceMonitors: %v", err)
	}
	if err := w.DeleteMaintenance(ctx, mid); err != nil {
		t.Fatalf("DeleteMaintenance: %v", err)
	}
	if err := w.DeleteMonitor(ctx, id); err != nil {
		t.Fatalf("DeleteMonitor: %v", err)
	}
	if _, err := w.GetMonitor(ctx, id); err == nil {
		t.Fatal("GetMonitor still answers for a deleted monitor")
	}
}
