package ui

import (
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/icortesb/lazykuma/internal/kuma"
)

func beat(status kuma.Status, ping float64) kuma.Beat {
	return kuma.Beat{Status: status, Ping: ping, HasPing: status == kuma.StatusUp, Time: time.Now()}
}

func TestSparkline(t *testing.T) {
	beats := []kuma.Beat{beat(kuma.StatusUp, 10), beat(kuma.StatusUp, 20), beat(kuma.StatusDown, 0), beat(kuma.StatusUp, 90)}
	if got := ansi.Strip(sparkline(beats, 10)); got != "▁▁ █" {
		t.Fatalf("sparkline = %q", got)
	}
	// Only the newest that fit; one ping alone has no range, so it is low.
	if got := ansi.Strip(sparkline(beats, 2)); got != " ▁" {
		t.Fatalf("narrow sparkline = %q", got)
	}
	if got := ansi.Strip(sparkline([]kuma.Beat{beat(kuma.StatusUp, 5), beat(kuma.StatusUp, 5)}, 5)); got != "▁▁" {
		t.Fatalf("flat sparkline = %q", got)
	}
}

func TestBeatBar(t *testing.T) {
	beats := []kuma.Beat{beat(kuma.StatusUp, 1), beat(kuma.StatusDown, 0), beat(kuma.StatusUp, 1)}
	if got := ansi.Strip(beatBar(beats, 2)); got != "██" {
		t.Fatalf("bar = %q", got)
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("vaultwarden", 6); got != "vault…" {
		t.Fatalf("got %q", got)
	}
	if got := truncate("pihole", 6); got != "pihole" {
		t.Fatalf("got %q", got)
	}
}

func TestMonitorsPlural(t *testing.T) {
	if monitors(1) != "1 monitor" || monitors(0) != "0 monitors" || monitors(12) != "12 monitors" {
		t.Fatal(monitors(1), monitors(0), monitors(12))
	}
}

func TestChartLine(t *testing.T) {
	at := func(min int) time.Time { return time.Date(2026, 9, 16, 12, min, 0, 0, time.UTC) }
	points := []kuma.ChartPoint{
		{Time: at(0), Up: 3, AvgPing: 40},
		{Time: at(1), Up: 3, AvgPing: 120},
		{Time: at(2), Down: 3},
		{Time: at(3), Up: 2, Down: 1, AvgPing: 80},
	}
	line, lo, hi, ok := chartLine(points, 10)
	if !ok || lo != 40 || hi != 120 {
		t.Fatalf("ok %v lo %v hi %v", ok, lo, hi)
	}
	// One cell per point while they fit: lowest, highest, a down-only
	// bucket at the bottom, a mixed one in between.
	if got := ansi.Strip(line); got != "▁█▁▄" {
		t.Errorf("line = %q", got)
	}

	// More points than cells: neighbours are merged, newest on the right.
	many := make([]kuma.ChartPoint, 0, 60)
	for i := 0; i < 60; i++ {
		many = append(many, kuma.ChartPoint{Time: at(i), Up: 1, AvgPing: float64(10 + i)})
	}
	line, lo, hi, _ = chartLine(many, 20)
	if w := lipgloss.Width(line); w != 20 {
		t.Errorf("width = %d, want 20", w)
	}
	if got := []rune(ansi.Strip(line)); got[0] != '▁' || got[len(got)-1] != '█' {
		t.Errorf("merged line = %q", string(got))
	}
	if lo >= hi {
		t.Errorf("lo %v hi %v", lo, hi)
	}

	if _, _, _, ok := chartLine([]kuma.ChartPoint{{Time: at(0), Down: 2}}, 10); ok {
		t.Error("a chart with no up check has a ping range")
	}
	if line, _, _, _ := chartLine(nil, 10); line != "" {
		t.Errorf("empty chart = %q", line)
	}
}

func TestPct(t *testing.T) {
	if got := pct(0.99812, true); got != "99.8%" {
		t.Errorf("pct = %q", got)
	}
	if got := pct(1, true); got != "100%" {
		t.Errorf("pct(1) = %q", got)
	}
	// Short of every check up is never shown as all of them.
	if got := pct(0.99996, true); got != "99.9%" {
		t.Errorf("pct(0.99996) = %q", got)
	}
	if got := pct(0, false); got != "—" {
		t.Errorf("pct unknown = %q", got)
	}
}
