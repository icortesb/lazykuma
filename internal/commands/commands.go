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

	// Every change of connection state is printed, even the ones that are
	// not worth a notification: a watch that silently lost an instance to
	// a refused token must still say so somewhere.
	conn := map[string]string{}
	observe := func(name string) {
		in, ok := c.Instance(name)
		if !ok {
			return
		}
		st := in.State()
		if line := connLine(st); line != conn[name] {
			conn[name] = line
			fmt.Fprintf(out, "%s  %s: %s\n", now().Local().Format("2006-01-02 15:04:05"), name, line)
		}
		for _, ev := range tracker.Observe(name, st, now()) {
			fmt.Fprintln(out, ev.Line())
			if settings.Watch {
				_ = sender.Send(ev.Title(), ev.Body())
			}
		}
	}
	tick := time.NewTicker(notify.Recheck)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case u := <-c.Updates():
			observe(u.Instance)
		case <-tick.C:
			for _, name := range tracker.Waiting() {
				observe(name)
			}
		}
	}
}

// connLine is an instance's connection state in words.
func connLine(st state.Instance) string {
	line := st.Conn.String()
	if st.Detail != "" && st.Conn != state.ConnOK {
		line += " (" + st.Detail + ")"
	}
	return line
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
	Pending     int      `json:"pending"`
	Paused      int      `json:"paused"`
	Maintenance int      `json:"maintenance"`
	DownNames   []string `json:"-"`
	Causes      []string `json:"-"`
	// Unreachable are the instances that could not be monitored, and
	// UnreachableWhy the reason for each, in the same order.
	Unreachable    []string `json:"-"`
	UnreachableWhy []string `json:"-"`
	// NotLoggedIn are instances the user has not logged in to yet: worth a
	// mention, not an alarm.
	NotLoggedIn []string `json:"-"`
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
		return exit(asJSON, ExitUnreachable)
	}

	if !waitSettled(ctx, c, timeout) && ctx.Err() != nil {
		return exit(asJSON, ExitUnreachable)
	}
	r := summarize(c.Snapshot())
	tooltip := append(append([]string{}, r.Causes...), unreachableLines(r)...)
	for _, name := range r.NotLoggedIn {
		tooltip = append(tooltip, name+": not logged in")
	}
	switch {
	case r.Reachable == 0:
		write(out, asJSON, "unreachable", strings.Join(tooltip, "\n"), "unreachable", r)
		return exit(asJSON, ExitUnreachable)
	case r.Down > 0 || len(r.Unreachable) > 0:
		write(out, asJSON, downText(r), strings.Join(tooltip, "\n"), "down", r)
		return exit(asJSON, ExitDown)
	}
	text := fmt.Sprintf("%d up", r.Up)
	if r.Pending > 0 {
		text += fmt.Sprintf(", %d pending", r.Pending)
	}
	if len(tooltip) == 0 {
		tooltip = []string{fmt.Sprintf("%d monitors up", r.Up)}
	}
	write(out, asJSON, text, strings.Join(tooltip, "\n"), "up", r)
	return exit(asJSON, ExitUp)
}

// exit is the code Status returns. With --json it is always 0: waybar and
// its relatives hide a module whose command fails, which would make it
// vanish exactly when something is down; the class carries the state.
func exit(asJSON bool, code int) int {
	if asJSON {
		return ExitUp
	}
	return code
}

func unreachableLines(r Report) []string {
	out := make([]string, len(r.Unreachable))
	for i, name := range r.Unreachable {
		out[i] = name + ": " + r.UnreachableWhy[i]
	}
	return out
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
		switch st.Conn {
		case state.ConnOK:
		case state.ConnNoCred:
			r.NotLoggedIn = append(r.NotLoggedIn, name)
			continue
		default:
			why := st.Conn.String()
			if st.Detail != "" {
				why += ": " + st.Detail
			}
			r.Unreachable = append(r.Unreachable, name)
			r.UnreachableWhy = append(r.UnreachableWhy, why)
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
			case state.StatusPending, state.StatusUnknown:
				r.Pending++
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
	for _, name := range r.Unreachable {
		parts = append(parts, name+" unreachable")
	}
	return strings.Join(parts, "; ")
}

// write prints the report: a plain line, or the JSON waybar and its
// relatives read ({"text", "tooltip", "class"}).
func write(out io.Writer, asJSON bool, text, tooltip, class string, r Report) {
	if !asJSON {
		// The summary first, for a script that reads one line; the details
		// under it, for the person who ran it.
		fmt.Fprintln(out, text)
		if class != "up" && tooltip != "" {
			for _, line := range strings.Split(tooltip, "\n") {
				fmt.Fprintln(out, "  "+line)
			}
		}
		return
	}
	// Waybar renders text and tooltip as Pango markup, and Kuma's messages
	// can carry a page's HTML or a name with an ampersand.
	json.NewEncoder(out).Encode(struct {
		Text    string `json:"text"`
		Tooltip string `json:"tooltip"`
		Class   string `json:"class"`
		Report
	}{markup.Replace(text), markup.Replace(tooltip), class, r})
}

var markup = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
