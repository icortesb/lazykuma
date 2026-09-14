// Package commands holds lazykuma's non-interactive commands: watch, which
// runs until stopped and reports every outage, and status, which answers
// once for a status bar or a script.
package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/icortesb/lazykuma/internal/config"
	"github.com/icortesb/lazykuma/internal/core"
	"github.com/icortesb/lazykuma/internal/notify"
	"github.com/icortesb/lazykuma/internal/state"
)

// Watch reports every change the tracker finds, one line each on out, and
// as a desktop notification when the config's notify.watch says so. It runs
// until ctx ends. c must already be running.
func Watch(ctx context.Context, c *core.Core, sender notify.Sender, out io.Writer, now func() time.Time) error {
	settings := c.Notify()
	tracker := notify.NewTracker(settings.On)

	names := make([]string, 0)
	for _, in := range c.Instances() {
		names = append(names, in.Name())
	}
	if len(names) == 0 {
		return fmt.Errorf("no instances configured: add one with lazykuma first")
	}
	fmt.Fprintf(out, "watching %s; notifying on %s\n", strings.Join(names, ", "), describe(settings))

	for {
		select {
		case <-ctx.Done():
			return nil
		case u := <-c.Updates():
			in, ok := c.Instance(u.Instance)
			if !ok {
				continue
			}
			for _, ev := range tracker.Observe(u.Instance, in.State(), now()) {
				fmt.Fprintln(out, ev.Line())
				if !settings.Watch {
					continue
				}
				if err := sender.Send(ev.Title(), ev.Body()); err != nil {
					fmt.Fprintf(out, "could not show the notification: %v\n", err)
				}
			}
		}
	}
}

// describe says in words what the watch will notify about.
func describe(n config.Notify) string {
	what := "outages"
	if n.On == config.NotifyChanges {
		what = "outages and recoveries"
	}
	if !n.Watch {
		return what + " (printed only: notify.watch is off)"
	}
	return what
}

// Exit codes of Status, so a bar or a script can tell "down" from "cannot
// tell".
const (
	ExitUp          = 0
	ExitDown        = 1
	ExitUnreachable = 2
)

// Report is what Status found.
type Report struct {
	Up          int      `json:"up"`
	Down        int      `json:"down"`
	Paused      int      `json:"paused"`
	Maintenance int      `json:"maintenance"`
	DownNames   []string `json:"-"`
	Causes      []string `json:"-"`
	Unreachable []string `json:"-"`
	Reachable   int      `json:"-"`
}

// Status waits for every instance to settle — connected with a status for
// each active monitor, or failed — or for timeout, then prints one line (or
// JSON for a status bar) and returns the exit code. c must already be
// running.
func Status(ctx context.Context, c *core.Core, timeout time.Duration, asJSON bool, out io.Writer) int {
	insts := c.Instances()
	if len(insts) == 0 {
		write(out, asJSON, "no instances", "no instances configured: add one with lazykuma first", "unreachable", Report{})
		return ExitUnreachable
	}

	if !waitSettled(ctx, c, timeout) && ctx.Err() != nil {
		return ExitUnreachable
	}
	r := summarize(c.Snapshot())
	switch {
	case r.Reachable == 0:
		write(out, asJSON, "unreachable", strings.Join(r.Unreachable, "\n"), "unreachable", r)
		return ExitUnreachable
	case r.Down > 0 || len(r.Unreachable) > 0:
		write(out, asJSON, downText(r), strings.Join(append(r.Causes, r.Unreachable...), "\n"), "down", r)
		return ExitDown
	}
	write(out, asJSON, fmt.Sprintf("%d up", r.Up), fmt.Sprintf("%d monitors up", r.Up), "up", r)
	return ExitUp
}

// waitSettled polls until every instance settles, the timeout passes, or ctx
// ends. It reports whether the instances settled; on a timeout Status still
// reports what it knows.
func waitSettled(ctx context.Context, c *core.Core, timeout time.Duration) bool {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for !settled(c.Snapshot()) {
		select {
		case <-ctx.Done():
			return false
		case <-deadline.C:
			return false
		case <-tick.C:
		}
	}
	return true
}

// settled reports whether every instance has said what it is going to say:
// failed for good, or connected with a status for every active monitor.
func settled(snap map[string]state.Instance) bool {
	for _, st := range snap {
		switch st.Conn {
		case state.ConnOK:
			if !st.Listed {
				return false // connected, but the monitor list has not arrived
			}
			for _, m := range st.Monitors {
				if m.Status() == state.StatusUnknown {
					return false
				}
			}
		case state.ConnConnecting:
			return false
		}
	}
	return true
}

func summarize(snap map[string]state.Instance) Report {
	var r Report
	names := make([]string, 0, len(snap))
	for name := range snap {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		st := snap[name]
		if st.Conn != state.ConnOK {
			detail := st.Conn.String()
			if st.Detail != "" {
				detail += ": " + st.Detail
			}
			r.Unreachable = append(r.Unreachable, name+" "+detail)
			continue
		}
		r.Reachable++
		for _, m := range st.Sorted() {
			switch m.Status() {
			case state.StatusUp:
				r.Up++
			case state.StatusDown:
				r.Down++
				r.DownNames = append(r.DownNames, m.Name)
				cause := m.Name
				if b, ok := m.Last(); ok && b.Msg != "" {
					cause += ": " + b.Msg
				}
				r.Causes = append(r.Causes, cause)
			case state.StatusPaused:
				r.Paused++
			case state.StatusMaintenance:
				r.Maintenance++
			}
		}
	}
	return r
}

func downText(r Report) string {
	parts := []string{}
	if r.Down > 0 {
		parts = append(parts, fmt.Sprintf("%d down: %s", r.Down, strings.Join(r.DownNames, ", ")))
	}
	for _, u := range r.Unreachable {
		parts = append(parts, strings.SplitN(u, " ", 2)[0]+" unreachable")
	}
	return strings.Join(parts, "; ")
}

// write prints the report: a plain line, or the JSON waybar and its
// relatives read ({"text", "tooltip", "class"}).
func write(out io.Writer, asJSON bool, text, tooltip, class string, r Report) {
	if !asJSON {
		fmt.Fprintln(out, text)
		return
	}
	json.NewEncoder(out).Encode(struct {
		Text    string `json:"text"`
		Tooltip string `json:"tooltip"`
		Class   string `json:"class"`
		Report
	}{text, tooltip, class, r})
}
