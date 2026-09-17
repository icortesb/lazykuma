package ui

import (
	"fmt"
	"strconv"
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

// chartLine draws a monitor's history in width cells, newest on the right.
// A cell's height is its average ping between the lowest and highest in the
// line; a cell with only down checks sits at the bottom in red, and one with
// some down checks is drawn in the warning colour. Neighbouring points merge
// when there are more than cells. ok is false when no check was up, so
// there is no ping range to speak of.
func chartLine(points []kuma.ChartPoint, width int) (line string, lo, hi float64, ok bool) {
	if width <= 0 || len(points) == 0 {
		return "", 0, 0, false
	}
	type cell struct {
		up, down int
		ping     float64 // sum of avgPing × up, divided by up once merged
	}
	n := min(len(points), width)
	cells := make([]cell, n)
	for i, p := range points {
		c := &cells[i*n/len(points)]
		c.up += p.Up
		c.down += p.Down
		c.ping += p.AvgPing * float64(p.Up)
	}
	for i := range cells {
		if cells[i].up == 0 {
			continue
		}
		cells[i].ping /= float64(cells[i].up)
		if !ok || cells[i].ping < lo {
			lo = cells[i].ping
		}
		if !ok || cells[i].ping > hi {
			hi = cells[i].ping
		}
		ok = true
	}
	var sb strings.Builder
	for _, c := range cells {
		if c.up == 0 {
			sb.WriteString(styleErr.Render(string(sparks[0])))
			continue
		}
		i := 0
		if hi > lo {
			i = int((c.ping - lo) / (hi - lo) * float64(len(sparks)-1))
		}
		style := styleHeading
		if c.down > 0 {
			style = styleWarn
		}
		sb.WriteString(style.Render(string(sparks[i])))
	}
	return sb.String(), lo, hi, ok
}

// pct is an uptime ratio as the detail shows it, or a dash when Kuma has
// not said.
func pct(ratio float64, has bool) string {
	if !has {
		return "—"
	}
	s := strconv.FormatFloat(ratio*100, 'f', 1, 64)
	return strings.TrimSuffix(s, ".0") + "%"
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
