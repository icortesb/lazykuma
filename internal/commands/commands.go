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
	// a refused token must still say so somewhere. Only a change of state
	// counts: each reconnect attempt passes through "connecting" and may
	// word its error differently, and a service left running for days must
	// not fill the journal with them.
	conn := map[string]state.Conn{}
	observe := func(name string) {
		in, ok := c.Instance(name)
		if !ok {
			tracker.Forget(name)
			return
		}
		st := in.State()
		if was, seen := conn[name]; st.Conn != state.ConnConnecting && (!seen || was != st.Conn) {
			conn[name] = st.Conn
			fmt.Fprintf(out, "%s  %s: %s\n", now().Local().Format("2006-01-02 15:04:05"), name, connLine(st))
		}
		for _, ev := range tracker.Observe(name, st, now()) {
			fmt.Fprintln(out, ev.Line())
			if settings.Watch {
				_ = sender.Send(ev.Title(), ev.Body())
			}
		}
	}
	tick := time.NewTicker(recheck)
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

// recheck is how often Watch observes the instances waiting out the grace
// period; a variable so a test need not wait seconds.
var recheck = notify.Recheck

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
		// A config that failed to parse also ends here; say why, since a
		// status bar never shows the warning on stderr.
		details := []string{"no instances configured: add one with lazykuma first"}
		for _, w := range c.Warnings() {
			details = append(details, "config: "+w)
		}
		write(out, asJSON, "no instances", "unreachable", details, Report{})
		return exit(asJSON, ExitUnreachable)
	}

	if !waitSettled(ctx, c, timeout) && ctx.Err() != nil {
		return exit(asJSON, ExitUnreachable)
	}
	r := summarize(c.Snapshot())
	details := append(append([]string{}, r.Causes...), unreachableLines(r)...)
	for _, name := range r.NotLoggedIn {
		details = append(details, name+": not logged in")
	}
	switch {
	case r.Reachable == 0 && len(r.Unreachable) == 0:
		// Every instance is only waiting for a login: nothing can be told,
		// but nothing is wrong either.
		write(out, asJSON, "not logged in", "unreachable", details, r)
		return exit(asJSON, ExitUnreachable)
	case r.Reachable == 0:
		write(out, asJSON, "unreachable", "unreachable", details, r)
		return exit(asJSON, ExitUnreachable)
	case r.Down > 0 || len(r.Unreachable) > 0:
		write(out, asJSON, downText(r), "down", details, r)
		return exit(asJSON, ExitDown)
	}
	text := fmt.Sprintf("%d up", r.Up)
	if r.Pending > 0 {
		text += fmt.Sprintf(", %d pending", r.Pending)
	}
	write(out, asJSON, text, "up", details, r)
	return exit(asJSON, ExitUp)
}

// Failed reports that lazykuma could not even start checking, such as a
// config it cannot read, and returns the exit code. A status bar still gets
// its JSON, or the module would vanish with the error.
func Failed(asJSON bool, err error, out io.Writer) int {
	if asJSON {
		write(out, true, "lazykuma error", "unreachable", []string{err.Error()}, Report{})
	}
	return exit(asJSON, ExitUnreachable)
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

// write prints the report: the text, then each detail indented under it;
// or the JSON waybar and its relatives read ({"text", "tooltip", "class"}),
// with the details as the tooltip.
func write(out io.Writer, asJSON bool, text, class string, details []string, r Report) {
	if !asJSON {
		// The summary first, for a script that reads one line; the details
		// under it, for the person who ran it.
		fmt.Fprintln(out, text)
		for _, line := range details {
			fmt.Fprintln(out, "  "+line)
		}
		return
	}
	tooltip := strings.Join(details, "\n")
	if tooltip == "" {
		tooltip = fmt.Sprintf("%d monitors up", r.Up)
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
