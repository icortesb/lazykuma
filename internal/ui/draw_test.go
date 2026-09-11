package ui

import (
	"testing"
	"time"

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
