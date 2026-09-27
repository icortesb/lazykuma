package kuma

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// historyFake answers the history calls with the shapes a real Kuma 2.5.3
// sent when probed.
func historyFake(t *testing.T) (*fakeKuma, *Session) {
	t.Helper()
	login := kumaLogin(false)
	f := newFakeKuma(t, func(event string, args []json.RawMessage) any {
		switch event {
		case "getMonitorChartData":
			var id, hours int
			json.Unmarshal(args[0], &id)
			json.Unmarshal(args[1], &hours)
			if id != 1 {
				return map[string]any{"ok": false, "msg": "Invalid period."}
			}
			return json.RawMessage(`{"ok":true,"data":[
				{"up":0,"down":0,"avgPing":0,"minPing":0,"maxPing":0,"timestamp":1789610400},
				{"up":3,"down":0,"avgPing":68,"minPing":56,"maxPing":83,"timestamp":1789610340},
				{"up":0,"down":2,"avgPing":0,"minPing":0,"maxPing":0,"timestamp":1789610280}]}`)
		case "monitorImportantHeartbeatListPaged":
			return json.RawMessage(`{"ok":true,"data":[
				{"monitorID":1,"status":1,"time":"2026-09-17 02:03:13.036","msg":"200 - OK","ping":97,"important":1,"duration":0,"retries":0,"response":null},
				{"monitorID":1,"status":0,"time":"2026-09-17 01:58:53.049","msg":"getaddrinfo ENOTFOUND","ping":null,"important":1,"duration":0,"retries":1,"response":null}]}`)
		case "clearEvents", "clearHeartbeats":
			return map[string]any{"ok": true}
		}
		return login(event, args)
	})
	s := dial(t, f)
	if err := s.LoginByToken(context.Background(), "jwt"); err != nil {
		t.Fatal(err)
	}
	return f, s
}

func TestChartData(t *testing.T) {
	f, s := historyFake(t)
	points, err := s.ChartData(context.Background(), 1, 24)
	if err != nil {
		t.Fatal(err)
	}
	if !f.Sent(`["getMonitorChartData",1,24]`) {
		t.Errorf("frames: %v", f.Frames())
	}
	// Oldest first, the empty current bucket dropped.
	want := []ChartPoint{
		{Time: time.Unix(1789610280, 0).UTC(), Down: 2},
		{Time: time.Unix(1789610340, 0).UTC(), Up: 3, AvgPing: 68, MinPing: 56, MaxPing: 83},
	}
	if len(points) != len(want) {
		t.Fatalf("points = %+v", points)
	}
	for i := range want {
		if points[i] != want[i] {
			t.Errorf("point %d = %+v, want %+v", i, points[i], want[i])
		}
	}
	if _, err := s.ChartData(context.Background(), 2, 24); err == nil {
		t.Error("a refused chart is not an error")
	}
}

func TestImportantBeats(t *testing.T) {
	f, s := historyFake(t)
	beats, err := s.ImportantBeats(context.Background(), 1, 25, 25)
	if err != nil {
		t.Fatal(err)
	}
	if !f.Sent(`["monitorImportantHeartbeatListPaged",1,25,25]`) {
		t.Errorf("frames: %v", f.Frames())
	}
	if len(beats) != 2 || beats[0].Status != StatusUp || !beats[0].HasPing || beats[0].Ping != 97 ||
		beats[1].Status != StatusDown || beats[1].HasPing || beats[1].MonitorID != 1 || !beats[1].Important {
		t.Fatalf("beats = %+v", beats)
	}
	if want := time.Date(2026, 9, 17, 1, 58, 53, 49e6, time.UTC); !beats[1].Time.Equal(want) {
		t.Errorf("time = %v, want %v", beats[1].Time, want)
	}
}

func TestClearEventsAndHeartbeats(t *testing.T) {
	f, s := historyFake(t)
	ctx := context.Background()
	if err := s.ClearEvents(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearHeartbeats(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if !f.Sent(`["clearEvents",1]`) || !f.Sent(`["clearHeartbeats",1]`) {
		t.Errorf("frames: %v", f.Frames())
	}
}
