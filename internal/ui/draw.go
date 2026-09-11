package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/icortesb/lazykuma/internal/kuma"
	"github.com/icortesb/lazykuma/internal/state"
)

// icon is the mark in front of a monitor in the list.
func icon(s state.Status) string {
	switch s {
	case state.StatusUp:
		return styleOK.Render("●")
	case state.StatusDown:
		return styleErr.Render("✖")
	case state.StatusPending:
		return styleWarn.Render("◐")
	case state.StatusMaintenance:
		return styleMaint.Render("◆")
	case state.StatusPaused:
		return styleLabel.Render("‖")
	}
	return styleLabel.Render("○")
}

// statusWord is the monitor's status in words, for the detail panel.
func statusWord(s state.Status) string {
	switch s {
	case state.StatusUp:
		return styleOK.Render("up")
	case state.StatusDown:
		return styleErr.Render("down")
	case state.StatusPending:
		return styleWarn.Render("pending")
	case state.StatusMaintenance:
		return styleMaint.Render("maintenance")
	case state.StatusPaused:
		return styleLabel.Render("paused")
	}
	return styleLabel.Render("no data yet")
}

var sparks = []rune("▁▂▃▄▅▆▇█")

// sparkline draws the pings of the newest beats that fit in width, low to
// high between their own minimum and maximum. Beats without a ping (down)
// are a space.
func sparkline(beats []kuma.Beat, width int) string {
	if width <= 0 || len(beats) == 0 {
		return ""
	}
	if len(beats) > width {
		beats = beats[len(beats)-width:]
	}
	lo, hi, seen := 0.0, 0.0, false
	for _, b := range beats {
		if !b.HasPing {
			continue
		}
		if !seen || b.Ping < lo {
			lo = b.Ping
		}
		if !seen || b.Ping > hi {
			hi = b.Ping
		}
		seen = true
	}
	var sb strings.Builder
	for _, b := range beats {
		if !b.HasPing {
			sb.WriteRune(' ')
			continue
		}
		i := 0
		if hi > lo {
			i = int((b.Ping - lo) / (hi - lo) * float64(len(sparks)-1))
		}
		sb.WriteRune(sparks[i])
	}
	return styleHeading.Render(sb.String())
}

// beatBar is one block per beat, newest on the right, coloured by status
// like the bar in Kuma's web UI.
func beatBar(beats []kuma.Beat, width int) string {
	if width <= 0 {
		return ""
	}
	if len(beats) > width {
		beats = beats[len(beats)-width:]
	}
	var sb strings.Builder
	for _, b := range beats {
		st := styleOK
		switch b.Status {
		case kuma.StatusDown:
			st = styleErr
		case kuma.StatusPending:
			st = styleWarn
		case kuma.StatusMaintenance:
			st = styleMaint
		}
		sb.WriteString(st.Render("█"))
	}
	return sb.String()
}

// ms is a ping for the list and the detail.
func ms(v float64) string { return fmt.Sprintf("%.0fms", v) }

// truncate cuts s to width cells, with an ellipsis when it had to.
func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r))+1 > width {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

// monitors is "1 monitor" or "n monitors".
func monitors(n int) string {
	if n == 1 {
		return "1 monitor"
	}
	return fmt.Sprintf("%d monitors", n)
}
