package kuma

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fixture decodes line n (1-based) of testdata/kuma-v2/<name>.jsonl, which
// holds events captured from a real Kuma 2.5.3, one ["event", args…] per line.
func fixture(t *testing.T, name string, n int) Event {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "testdata", "kuma-v2", name+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 4<<20)
	for i := 1; sc.Scan(); i++ {
		if i != n {
			continue
		}
		var arr []json.RawMessage
		if err := json.Unmarshal(sc.Bytes(), &arr); err != nil {
			t.Fatal(err)
		}
		var ev string
		json.Unmarshal(arr[0], &ev)
		got, ok, err := DecodeEvent(ev, arr[1:])
		if err != nil || !ok {
			t.Fatalf("DecodeEvent(%s line %d) = ok %v, err %v", name, n, ok, err)
		}
		return got
	}
	t.Fatalf("%s has no line %d", name, n)
	return nil
}

func TestDecodeMonitorList(t *testing.T) {
	ml := fixture(t, "monitorList", 1).(MonitorList)
	if len(ml.Monitors) != 2 {
		t.Fatalf("got %d monitors, want 2", len(ml.Monitors))
	}
	m := ml.Monitors[1]
	want := Monitor{ID: 1, Name: "https://example.com", Type: "http", URL: "https://example.com", Active: true, Interval: 20}
	if m != want {
		t.Fatalf("monitor 1 = %+v, want %+v", m, want)
	}
	if m.Target() != "https://example.com" {
		t.Errorf("Target() = %q", m.Target())
	}
}

func TestDecodeMonitorUpdate(t *testing.T) {
	up := fixture(t, "updateMonitorIntoList", 1).(MonitorUpdate)
	if m, ok := up.Monitors[1]; !ok || m.Active {
		t.Fatalf("monitor 1 = %+v, want it paused", m)
	}
}

func TestDecodeHeartbeat(t *testing.T) {
	down := fixture(t, "heartbeat", 1).(Heartbeat).Beat
	if down.MonitorID != 2 || down.Status != StatusDown || down.HasPing {
		t.Errorf("down beat = %+v", down)
	}
	if want := time.Date(2026, 9, 11, 0, 12, 42, 90e6, time.UTC); !down.Time.Equal(want) {
		t.Errorf("time = %v, want %v", down.Time, want)
	}

	up := fixture(t, "heartbeat", 2).(Heartbeat).Beat
	if up.MonitorID != 1 || up.Status != StatusUp || !up.HasPing || up.Ping != 62 || up.Msg != "200 - OK" {
		t.Errorf("up beat = %+v", up)
	}
}

func TestDecodeHeartbeatList(t *testing.T) {
	hl := fixture(t, "heartbeatList", 1).(HeartbeatList)
	if hl.MonitorID != 1 || len(hl.Beats) != 5 || hl.Overwrite {
		t.Fatalf("list = monitor %d, %d beats, overwrite %v", hl.MonitorID, len(hl.Beats), hl.Overwrite)
	}
	first := hl.Beats[0]
	if first.MonitorID != 1 || first.Ping != 72 || !first.Important {
		t.Errorf("first beat = %+v", first)
	}
	if !hl.Beats[0].Time.Before(hl.Beats[4].Time) {
		t.Errorf("beats not oldest first")
	}
}

func TestDecodeStats(t *testing.T) {
	if ap := fixture(t, "avgPing", 1).(AvgPing); ap != (AvgPing{MonitorID: 1, Ms: 62, Valid: true}) {
		t.Errorf("avgPing = %+v", ap)
	}
	if ap := fixture(t, "avgPing", 2).(AvgPing); ap != (AvgPing{MonitorID: 2}) {
		t.Errorf("null avgPing = %+v", ap)
	}
	if up := fixture(t, "uptime", 1).(Uptime); up != (Uptime{MonitorID: 1, Period: "24", Ratio: 1}) {
		t.Errorf("uptime = %+v", up)
	}
	// Kuma sends the id as a string on login and as a number afterwards.
	if ap := fixture(t, "avgPing", 3).(AvgPing); ap.MonitorID != 2 {
		t.Errorf("numeric id = %+v", ap)
	}
	if ci := fixture(t, "certInfo", 1).(CertInfo); ci != (CertInfo{MonitorID: 1, Valid: true, DaysRemaining: 46}) {
		t.Errorf("certInfo = %+v", ci)
	}
}

func TestDecodeInfo(t *testing.T) {
	if in := fixture(t, "info", 1).(Info); in.Version != "" {
		t.Errorf("before login version = %q, want empty", in.Version)
	}
	if in := fixture(t, "info", 2).(Info); in.Version != "2.5.3" {
		t.Errorf("version = %q", in.Version)
	}
}

func TestDecodeIgnoresUnusedEvents(t *testing.T) {
	ev, ok, err := DecodeEvent("proxyList", []json.RawMessage{json.RawMessage(`[]`)})
	if ev != nil || ok || err != nil {
		t.Fatalf("got %v %v %v, want nothing", ev, ok, err)
	}
}

func TestDecodeDeleted(t *testing.T) {
	ev, ok, err := DecodeEvent("deleteMonitorFromList", []json.RawMessage{json.RawMessage(`7`)})
	if !ok || err != nil || ev.(MonitorDeleted).ID != 7 {
		t.Fatalf("got %v %v %v", ev, ok, err)
	}
}

func TestMonitorTarget(t *testing.T) {
	tests := []struct {
		m    Monitor
		want string
	}{
		{Monitor{Type: "http", URL: "https://a.lan"}, "https://a.lan"},
		{Monitor{Type: "port", Hostname: "db.lan", Port: 5432}, "db.lan:5432"},
		{Monitor{Type: "ping", Hostname: "10.0.0.1"}, "10.0.0.1"},
		{Monitor{Type: "group"}, "group"},
	}
	for _, tt := range tests {
		if got := tt.m.Target(); got != tt.want {
			t.Errorf("%+v.Target() = %q, want %q", tt.m, got, tt.want)
		}
	}
}
