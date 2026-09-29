// lazykuma is a terminal UI for Uptime Kuma v2, plus commands that need no
// terminal: watch, which reports outages until stopped, status, which answers
// once for a status bar or a script, and autostart, which runs watch at login.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/icortesb/lazykuma/internal/autostart"
	"github.com/icortesb/lazykuma/internal/commands"
	"github.com/icortesb/lazykuma/internal/config"
	"github.com/icortesb/lazykuma/internal/core"
	"github.com/icortesb/lazykuma/internal/kuma"
	"github.com/icortesb/lazykuma/internal/logfile"
	"github.com/icortesb/lazykuma/internal/notify"
	"github.com/icortesb/lazykuma/internal/ui"
	"github.com/icortesb/lazykuma/internal/watchlock"
)

// version is stamped by the Makefile from git describe.
var version = "dev"

const usage = `lazykuma — Uptime Kuma, in the terminal

usage:
  lazykuma                       open the terminal UI
  lazykuma watch                 report every outage until stopped
  lazykuma status [--json] [--timeout 10s]
                                 print the state once; exit 0 up, 1 down, 2 unreachable
                                 (always 0 with --json: the class carries the state)
  lazykuma autostart [on|off]    run watch in the background at login
  lazykuma --version             print the version
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	cmd := ""
	if len(args) > 0 {
		cmd = args[0]
	}
	switch cmd {
	case "":
		return fail(stderr, tui())
	case "watch":
		return watch(args[1:], stdout, stderr)
	case "autostart":
		return autostartCmd(args[1:], stdout, stderr)
	case "status":
		return status(args[1:], stdout, stderr)
	case "-version", "--version", "version":
		fmt.Fprintln(stdout, version)
		return 0
	case "-h", "--help", "help":
		fmt.Fprint(stdout, usage)
		return 0
	}
	fmt.Fprintf(stderr, "lazykuma: unknown command %q\n\n%s", cmd, usage)
	return 2
}

func fail(stderr io.Writer, err error) int {
	if err != nil {
		fmt.Fprintln(stderr, "lazykuma:", err)
		return 1
	}
	return 0
}

// open reads the config and starts every instance under ctx.
func open(ctx context.Context) (*core.Core, error) {
	cfgPath, tokPath, err := config.Paths()
	if err != nil {
		return nil, err
	}
	return openAt(ctx, cfgPath, tokPath)
}

// openAt reads the config and tokens at these paths and starts every
// instance under ctx.
func openAt(ctx context.Context, cfgPath, tokPath string) (*core.Core, error) {
	c, err := core.Open(cfgPath, tokPath, core.Options{})
	if err != nil {
		return nil, err
	}
	c.Run(ctx)
	return c, nil
}

func tui() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, err := open(ctx)
	if err != nil {
		return err
	}

	deps := ui.Deps{Core: c, Login: kuma.Login, Version: version}
	// Without a manager (no home directory, say) the menu hides the switch.
	if a, err := autostart.Default(); err == nil {
		deps.Autostart = a
	}
	p := tea.NewProgram(ui.New(deps), tea.WithAltScreen())

	// Every change the core announces becomes a message for the program,
	// and a desktop notification when the config asks for one.
	go func() {
		tracker := notify.NewTracker(c.Notify().On)
		// A notification daemon that is slow to answer must not hold up
		// the screen; a failure has nowhere to go under the full-screen UI.
		desktop := notify.Async(notify.Desktop{}, nil)
		observe := func(name string) {
			in, ok := c.Instance(name)
			if !ok {
				tracker.Forget(name)
				return
			}
			st := in.State()
			p.Send(ui.InstanceState{Name: name, State: st})
			for _, ev := range tracker.Observe(name, st, time.Now()) {
				if c.Notify().Desktop {
					_ = desktop.Send(ev.Title(), ev.Body())
				}
			}
		}
		tick := time.NewTicker(notify.Recheck)
		defer tick.Stop()
		for {
			select {
			case u := <-c.Updates():
				observe(u.Instance)
			case <-tick.C:
				for _, name := range tracker.Waiting() {
					observe(name)
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	_, err = p.Run()
	return err
}

func watch(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: lazykuma watch\n\nPrints every outage and recovery until stopped, and raises a desktop\nnotification unless [notify] watch = false.")
	}
	// Hidden: the entries that start watch at login have no terminal, so
	// its output goes to a file instead.
	toLog := fs.Bool("log", false, "write output to the watch log")
	if code, done := parse(fs, args); done {
		return code
	}

	cfgPath, tokPath, err := config.Paths()
	if err != nil {
		return fail(stderr, err)
	}
	lockPath, logPath, err := autostart.StatePaths()
	if err != nil {
		return fail(stderr, err)
	}

	// Under the Run key or XDG autostart nobody sees stderr, so with --log
	// every message goes to the file as well, startup errors included. The
	// log comes first so a broken stderr cannot keep lines out of it.
	// Errors before the log is open (paths, opening it) can only reach stderr.
	out, msgs := stdout, stderr
	if *toLog {
		l, err := logfile.Open(logPath, 1<<20)
		if err != nil {
			return fail(stderr, err)
		}
		defer l.Close()
		out, msgs = l, &logMsgs{log: l, stderr: stderr}
	}

	// A status probe holds the lock for an instant; waiting a second keeps
	// it from making a starting watch give up.
	lock, err := watchlock.AcquireWait(lockPath, time.Second)
	if err != nil {
		return fail(msgs, err)
	}
	defer lock.Release()

	src := commands.WatchSource{
		Open:  func(ctx context.Context) (*core.Core, error) { return openAt(ctx, cfgPath, tokPath) },
		Stamp: func() string { return config.Stamp(cfgPath, tokPath) },
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	desktop := notify.Async(notify.Desktop{}, func(err error) {
		fmt.Fprintln(msgs, "lazykuma: desktop notification:", err)
	})
	return fail(msgs, commands.Watch(ctx, src, desktop, out, time.Now))
}

// logMsgs sends a message to the watch log, stamped like the lines Watch
// writes there, and to stderr. The log goes first and a stderr that fails
// (no console under the Run key) is ignored: the log is the record.
type logMsgs struct{ log, stderr io.Writer }

func (m *logMsgs) Write(p []byte) (int, error) {
	stamp := time.Now().Local().Format("2006-01-02 15:04:05")
	if _, err := fmt.Fprintf(m.log, "%s  %s", stamp, p); err != nil {
		return 0, err
	}
	_, _ = m.stderr.Write(p)
	return len(p), nil
}

// autostartCmd shows, enables or disables the watch at login.
func autostartCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("autostart", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: lazykuma autostart [on|off]\n\nWith no argument, shows whether watch starts at login. on registers this\nbinary and restarts the background watch with it; off removes it and stops it.")
	}
	arg := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		arg, args = args[0], args[1:]
	}
	if code, done := parse(fs, args); done {
		return code
	}
	if arg != "" && arg != "on" && arg != "off" {
		fmt.Fprintf(stderr, "lazykuma autostart: unexpected %q\n", arg)
		fs.Usage()
		return 2
	}

	m, err := autostart.Default()
	if err != nil {
		return autostartFail(stderr, err)
	}
	return runAutostart(m, arg, stdout, stderr)
}

func runAutostart(m *autostart.Manager, arg string, stdout, stderr io.Writer) int {
	switch arg {
	case "on":
		if err := m.Enable(); err != nil {
			return autostartFail(stderr, err)
		}
	case "off":
		if err := m.Disable(); err != nil {
			return autostartFail(stderr, err)
		}
		fmt.Fprintln(stdout, "background alerts: off")
		return 0
	}
	st, err := m.Status()
	if err != nil {
		return autostartFail(stderr, err)
	}
	fmt.Fprint(stdout, autostartText(st))
	return 0
}

func autostartFail(stderr io.Writer, err error) int {
	fmt.Fprintln(stderr, "lazykuma: autostart:", err)
	return 1
}

// autostartText is what "lazykuma autostart" prints. A watch that runs while
// autostart is off was started by hand.
func autostartText(st autostart.Status) string {
	var b strings.Builder
	if st.On {
		fmt.Fprintf(&b, "background alerts: on (%s)\nruns: %s\n", st.Kind, st.Path)
	} else {
		b.WriteString("background alerts: off\n")
	}
	switch {
	case !st.Running:
		b.WriteString("watch: not running\n")
	case st.On:
		fmt.Fprintf(&b, "watch: running (pid %d)\n", st.PID)
	default:
		fmt.Fprintf(&b, "watch: running (pid %d), started by hand\n", st.PID)
	}
	return b.String()
}

// binaryWarning is the message for a registered watch that is not this
// binary, or "" when they match. Both paths are resolved first, since the
// registered one may go through a symlink such as a Homebrew shim, and
// Windows paths differ by case only.
func binaryWarning(registered, current string) string {
	same := func(a, b string) bool {
		a, b = resolve(a), resolve(b)
		if runtime.GOOS == "windows" {
			return strings.EqualFold(a, b)
		}
		return a == b
	}
	if registered == "" || same(registered, current) {
		return ""
	}
	return fmt.Sprintf("lazykuma: background alerts run %s, not this binary (%s); run \"lazykuma autostart on\" to switch", registered, current)
}

func resolve(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// warnBinary tells a status caller that the background watch is another
// binary. It only reads the registration, no command and no lock probe, so
// a status bar polling every few seconds pays nothing; its errors are
// ignored because status must never fail because of autostart.
func warnBinary(stderr io.Writer, m *autostart.Manager) {
	on, path, err := m.Registered()
	if err != nil || !on {
		return
	}
	if w := binaryWarning(path, m.Exe); w != "" {
		fmt.Fprintln(stderr, w)
	}
}

// parse reads a subcommand's flags. done means the command must stop here
// with code: 0 for -h, 2 for a flag it does not know.
func parse(fs *flag.FlagSet, args []string) (code int, done bool) {
	err := fs.Parse(args)
	switch {
	case errors.Is(err, flag.ErrHelp):
		return 0, true
	case err != nil:
		return 2, true
	case fs.NArg() > 0:
		fmt.Fprintf(fs.Output(), "lazykuma %s: unexpected %q\n", fs.Name(), fs.Arg(0))
		fs.Usage()
		return 2, true
	}
	return 0, false
}

// warn prints what was wrong with the config, where a status bar reading
// stdout will not mistake it for the answer.
func warn(stderr io.Writer, c *core.Core) {
	for _, w := range c.Warnings() {
		fmt.Fprintln(stderr, "lazykuma: config:", w)
	}
}

func status(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, `print {"text","tooltip","class"} for a status bar`)
	timeout := fs.Duration("timeout", 10*time.Second, "how long to wait for every instance to answer")
	if code, done := parse(fs, args); done {
		return code
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	c, err := open(ctx)
	if err != nil {
		fmt.Fprintln(stderr, "lazykuma:", err)
		return commands.Failed(*asJSON, err, stdout)
	}
	warn(stderr, c)
	if m, err := autostart.Default(); err == nil {
		warnBinary(stderr, m)
	}
	return commands.Status(ctx, c, *timeout, *asJSON, stdout)
}
