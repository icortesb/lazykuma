// lazykuma is a terminal UI for Uptime Kuma v2, plus two commands that need
// no terminal: watch, which reports outages until stopped, and status, which
// answers once for a status bar or a script.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/icortesb/lazykuma/internal/commands"
	"github.com/icortesb/lazykuma/internal/config"
	"github.com/icortesb/lazykuma/internal/core"
	"github.com/icortesb/lazykuma/internal/kuma"
	"github.com/icortesb/lazykuma/internal/notify"
	"github.com/icortesb/lazykuma/internal/ui"
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

	p := tea.NewProgram(ui.New(ui.Deps{Core: c, Login: kuma.Login, Version: version}), tea.WithAltScreen())

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
	if code, done := parse(fs, args); done {
		return code
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	c, err := open(ctx)
	if err != nil {
		return fail(stderr, err)
	}
	warn(stderr, c)
	desktop := notify.Async(notify.Desktop{}, func(err error) {
		fmt.Fprintln(stderr, "lazykuma: desktop notification:", err)
	})
	return fail(stderr, commands.Watch(ctx, c, desktop, stdout, time.Now))
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
		return commands.ExitUnreachable
	}
	warn(stderr, c)
	return commands.Status(ctx, c, *timeout, *asJSON, stdout)
}
