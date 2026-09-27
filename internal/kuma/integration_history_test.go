//go:build integration

package kuma

import (
	"context"
	"testing"
	"time"
)

// TestIntegrationHistory checks the calls the detail screen is built on
// against a real Kuma: a monitor's chart fills with its checks, its state
// changes page back newest first, and both clears are accepted.
func TestIntegrationHistory(t *testing.T) {
	url := itEnv("LAZYKUMA_IT_URL", "http://localhost:3902")
	user := itEnv("LAZYKUMA_IT_USER", "admin")
	pass := itEnv("LAZYKUMA_IT_PASS", "lazykuma-it-Passw0rd")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	s0, err := Dial(ctx, url)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	s0.call(ctx, "setup", user, pass) // not ok once a user exists: fine
	s0.Close()
	token, err := Login(ctx, url, user, pass, "")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	s, err := Dial(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() }) // registered first: it runs last
	if err := s.LoginByToken(ctx, token); err != nil {
		t.Fatal(err)
	}

	id, err := s.AddMonitor(ctx, RawMonitor{
		"type": "http", "name": "it-history", "url": "https://this-does-not-exist.invalid", "method": "GET",
		"interval": 20, "retryInterval": 20, "maxretries": 0,
		"accepted_statuscodes": []string{"200-299"}, "notificationIDList": map[string]bool{},
		"conditions": []any{}, "kafkaProducerBrokers": []any{}, "kafkaProducerSaslOptions": map[string]any{},
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer ccancel()
		if err := s.DeleteMonitor(cctx, id); err != nil {
			t.Logf("cleanup: delete monitor %d: %v", id, err)
		}
	})

	// The first check is a state change: wait for it.
	var beats []Beat
	for deadline := time.Now().Add(90 * time.Second); len(beats) == 0; {
		if time.Now().After(deadline) {
			t.Fatal("no state change within 90s")
		}
		time.Sleep(2 * time.Second)
		if beats, err = s.ImportantBeats(ctx, id, 0, 25); err != nil {
			t.Fatalf("ImportantBeats: %v", err)
		}
	}
	if beats[0].MonitorID != id || beats[0].Status != StatusDown || beats[0].Msg == "" {
		t.Fatalf("first state change = %+v", beats[0])
	}

	for _, hours := range []int{1, 24, 168, 720} {
		points, err := s.ChartData(ctx, id, hours)
		if err != nil {
			t.Fatalf("ChartData(%d): %v", hours, err)
		}
		if len(points) == 0 || points[len(points)-1].Down == 0 {
			t.Fatalf("ChartData(%d) = %+v, want a bucket with the down checks", hours, points)
		}
		for i := 1; i < len(points); i++ {
			if !points[i-1].Time.Before(points[i].Time) {
				t.Fatalf("ChartData(%d) not oldest first: %+v", hours, points)
			}
		}
	}

	if err := s.ClearEvents(ctx, id); err != nil {
		t.Fatalf("ClearEvents: %v", err)
	}
	if after, err := s.ImportantBeats(ctx, id, 0, 25); err != nil || len(after) != 0 {
		t.Fatalf("after ClearEvents: %+v, %v; want none", after, err)
	}
	if err := s.ClearHeartbeats(ctx, id); err != nil {
		t.Fatalf("ClearHeartbeats: %v", err)
	}
}
