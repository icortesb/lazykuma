package ui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/icortesb/lazykuma/internal/core"
	"github.com/icortesb/lazykuma/internal/kuma"
	"github.com/icortesb/lazykuma/internal/state"
)

// chartPeriods are the spans the chart switches between, and the hours
// Kuma is asked for.
var chartPeriods = []struct {
	label string
	hours int
}{{"1h", 1}, {"6h", 6}, {"24h", 24}, {"7d", 168}, {"30d", 720}}

const (
	defaultPeriod = 2  // 24h
	eventPage     = 25 // state changes asked for at a time
)

// detailScreen is one monitor at full size: its uptime, a chart of its
// pings, its beats, and the history of its state changes.
type detailScreen struct {
	id     int
	period int // index into chartPeriods

	chart        []kuma.ChartPoint
	chartErr     string
	chartLoading bool

	// events are the pages of state changes fetched so far, newest first;
	// done means Kuma has no more.
	events        []kuma.Beat
	eventsLoading bool
	eventsDone    bool
	eventsErr     string
	cursor        int

	// since is the newest state change the instance held for the monitor
	// when the screen was built. Those up to it are left to the fetched
	// pages: the instance keeps the ones Kuma sent on connect, and a clear
	// that blanked them in Kuma must not bring them back from memory.
	since time.Time
}

// newDetailScreen opens the detail of monitor id, as of the state st.
func newDetailScreen(id int, st state.Instance) detailScreen {
	d := detailScreen{id: id, period: defaultPeriod, chartLoading: true, eventsLoading: true}
	for _, inc := range st.Incidents {
		if inc.MonitorID == id && inc.Time.After(d.since) {
			d.since = inc.Time
		}
	}
	return d
}

type detailAction int

const (
	detNone detailAction = iota
	detBack
	detPeriod  // the chart period changed: fetch it
	detMore    // near the end of the events: fetch the next page
	detMonitor // a monitor action; which one is in the returned instAction
	detClearEvents
	detClearHistory
)

// shown is the state changes the screen lists: those fetched, and any the
// instance has seen since the screen was built, newest first and without
// repeats.
func (d detailScreen) shown(st state.Instance) []kuma.Beat {
	out := slices.Clone(d.events)
	for _, inc := range st.Incidents {
		if inc.MonitorID != d.id || !inc.Time.After(d.since) {
			continue
		}
		out = append(out, kuma.Beat{MonitorID: inc.MonitorID, Status: inc.Status, Time: inc.Time, Msg: inc.Msg, Important: true})
	}
	slices.SortStableFunc(out, func(a, b kuma.Beat) int { return b.Time.Compare(a.Time) })
	return slices.CompactFunc(out, func(a, b kuma.Beat) bool { return a.Time.Equal(b.Time) })
}

// Update handles a key. A monitor action comes back as detMonitor with the
// instance action to run on this monitor.
func (d detailScreen) Update(msg tea.KeyMsg, st state.Instance) (detailScreen, detailAction, instAction) {
	n := len(d.shown(st))
	switch {
	case key.Matches(msg, keys.Back):
		return d, detBack, instNone
	case msg.Type == tea.KeyRight || msg.String() == "l":
		if d.period < len(chartPeriods)-1 {
			d.period++
			d.chartLoading, d.chart, d.chartErr = true, nil, ""
			return d, detPeriod, instNone
		}
	case msg.Type == tea.KeyLeft || msg.String() == "h":
		if d.period > 0 {
			d.period--
			d.chartLoading, d.chart, d.chartErr = true, nil, ""
			return d, detPeriod, instNone
		}
	case key.Matches(msg, keys.Up):
		if d.cursor > 0 {
			d.cursor--
		}
	case key.Matches(msg, keys.Down):
		if d.cursor < n-1 {
			d.cursor++
		}
		if !d.eventsDone && !d.eventsLoading && d.cursor >= n-2 {
			d.eventsLoading = true
			return d, detMore, instNone
		}
	case key.Matches(msg, keys.Pause):
		return d, detMonitor, instToggle
	case msg.String() == "e":
		return d, detMonitor, instEdit
	case msg.String() == "r":
		return d, detMonitor, instRaw
	case msg.String() == "v":
		return d, detMonitor, instMove
	case msg.String() == "C":
		return d, detMonitor, instClone
	case msg.String() == "d":
		return d, detMonitor, instDelete
	case msg.String() == "m":
		return d, detMonitor, instSilence
	case msg.String() == "x":
		return d, detClearEvents, instNone
	case msg.String() == "X":
		return d, detClearHistory, instNone
	}
	return d, detNone, instNone
}

// withChart takes a chart Kuma returned, if it is the one on screen.
func (d detailScreen) withChart(msg chartLoaded) detailScreen {
	if msg.monitorID != d.id || msg.hours != chartPeriods[d.period].hours {
		return d
	}
	d.chartLoading = false
	d.chart, d.chartErr = msg.points, ""
	if msg.err != nil {
		d.chart, d.chartErr = nil, kuma.Brief(msg.err)
	}
	return d
}

// withEvents takes a page of state changes, if it is the next one for this
// monitor.
func (d detailScreen) withEvents(msg eventsLoaded) detailScreen {
	if msg.monitorID != d.id || msg.offset != len(d.events) {
		return d
	}
	d.eventsLoading = false
	if msg.err != nil {
		d.eventsErr = kuma.Brief(msg.err)
		return d
	}
	d.events = append(slices.Clone(d.events), msg.beats...)
	d.eventsErr = ""
	d.eventsDone = len(msg.beats) < eventPage
	return d
}

// keyHintsDetail is the footer of the detail screen.
const keyHintsDetail = "←/→ period   j/k events   x clear events   X clear history   e edit   r fields   p pause   v move   C clone   m silence   d delete   ? help   esc back"

func (d detailScreen) View(st state.Instance, width, height int) string {
	m, ok := st.Monitors[d.id]
	if !ok {
		return styleLabel.Render(truncate("this monitor is gone", width))
	}
	var lines []string

	lines = append(lines, styleHeading.Render(truncate(m.Name, width-16))+"   "+statusWord(m.Status()))
	facts := []string{m.Target(), m.Type}
	if m.Interval > 0 {
		facts = append(facts, fmt.Sprintf("every %ds", m.Interval))
	}
	if g, ok := st.Monitors[m.Parent]; ok && m.Parent != 0 {
		facts = append(facts, groupPath(st, g))
	}
	lines = append(lines, styleLabel.Render(truncate(strings.Join(facts, " · "), width)))
	if chips := tagChips(m.Tags, width); chips != "" {
		lines = append(lines, chips)
	}
	lines = append(lines, "")

	uptime := fmt.Sprintf("uptime  24h %s   30d %s   1y %s",
		pct(m.Uptime24, m.HasUptime), pct(m.Uptime30d, m.HasUptime30d), pct(m.Uptime1y, m.HasUptime1y))
	if m.HasCert {
		uptime += fmt.Sprintf("     cert %d days", m.CertDays)
	}
	if m.HasAvgPing {
		uptime += "     avg " + ms(m.AvgPing)
	}
	lines = append(lines, styleValue.Render(truncate(uptime, width)), "")

	head := "ping · " + chartPeriods[d.period].label
	chart := ""
	switch {
	case d.chartLoading:
		chart = styleLabel.Render(truncate("loading…", width))
	case d.chartErr != "":
		chart = styleErr.Render(truncate(d.chartErr, width))
	case len(d.chart) == 0:
		chart = styleLabel.Render(truncate("no checks in this period", width))
	default:
		line, _, _, ok := chartLine(d.chart, width)
		chart = line
		if ok {
			top := 0.0
			for _, p := range d.chart {
				top = max(top, p.MaxPing)
			}
			head += fmt.Sprintf("   min %s · max %s", ms(minPing(d.chart)), ms(top))
		}
	}
	lines = append(lines, styleLabel.Render(truncate(head, width)), chart)
	lines = append(lines, styleLabel.Render("beats  ")+beatBar(m.Beats, width-7), "")

	lines = append(lines, styleHeading.Render("events"))
	events := d.shown(st)
	switch {
	case len(events) == 0 && d.eventsLoading:
		lines = append(lines, styleLabel.Render(truncate("loading…", width)))
	case len(events) == 0 && d.eventsErr == "":
		lines = append(lines, styleLabel.Render(truncate("no state changes yet", width)))
	}
	// A page that failed says so even under the ones that loaded: j asks
	// for it again.
	if d.eventsErr != "" {
		lines = append(lines, styleErr.Render(truncate(d.eventsErr, width)))
	}
	rows := max(height-len(lines)-1, 3)
	cursor := min(d.cursor, max(len(events)-1, 0))
	start := 0
	if cursor >= rows {
		start = cursor - rows + 1
	}
	for i := start; i < len(events) && i < start+rows; i++ {
		b := events[i]
		mark, style := "● up  ", styleOK
		switch b.Status {
		case kuma.StatusDown:
			mark, style = "✖ down", styleErr
		case kuma.StatusPending:
			mark, style = "◐ pend", styleWarn
		case kuma.StatusMaintenance:
			mark, style = "◆ mnt ", styleMaint
		}
		text := truncate(fmt.Sprintf("%s  %s  %s", b.Time.Local().Format("2006-01-02 15:04"), mark, b.Msg), width-2)
		if i == cursor {
			lines = append(lines, styleRow.Render(" "+text+" "))
			continue
		}
		lines = append(lines, " "+style.Render(text))
	}
	return strings.Join(lines, "\n")
}

// minPing is the lowest single ping in a chart's up buckets.
func minPing(points []kuma.ChartPoint) float64 {
	lo, seen := 0.0, false
	for _, p := range points {
		if p.Up > 0 && (!seen || p.MinPing < lo) {
			lo, seen = p.MinPing, true
		}
	}
	return lo
}

// Messages and commands of the detail.
type (
	chartLoaded struct {
		instance         string
		monitorID, hours int
		points           []kuma.ChartPoint
		err              error
	}
	eventsLoaded struct {
		instance          string
		monitorID, offset int
		beats             []kuma.Beat
		err               error
	}
)

func loadChart(in *core.Instance, id, hours int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		points, err := in.ChartData(ctx, id, hours)
		return chartLoaded{instance: in.Name(), monitorID: id, hours: hours, points: points, err: err}
	}
}

func loadEvents(in *core.Instance, id, offset int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		beats, err := in.ImportantBeats(ctx, id, offset, eventPage)
		return eventsLoaded{instance: in.Name(), monitorID: id, offset: offset, beats: beats, err: err}
	}
}

func clearEvents(in *core.Instance, mon state.Monitor) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		return actionDone{name: in.Name(), action: "cleared the events of", mon: mon.Name, monitorID: mon.ID, err: in.ClearEvents(ctx, mon.ID)}
	}
}

func clearHistory(in *core.Instance, mon state.Monitor) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		return actionDone{name: in.Name(), action: "cleared the history of", mon: mon.Name, monitorID: mon.ID, err: in.ClearHeartbeats(ctx, mon.ID)}
	}
}
