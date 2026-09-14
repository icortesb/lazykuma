// lazykuma is a terminal UI for Uptime Kuma v2, plus two commands that need
// no terminal: watch, which reports outages until stopped, and status, which
// answers once for a status bar or a script.
package main

import (
	"context"
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
		return fail(stderr, watch(stdout))
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
		var desktop notify.Desktop
		for {
			select {
			case u := <-c.Updates():
				in, ok := c.Instance(u.Instance)
				if !ok {
					continue
				}
				st := in.State()
				p.Send(ui.InstanceState{Name: u.Instance, State: st})
				for _, ev := range tracker.Observe(u.Instance, st, time.Now()) {
					if c.Notify().Desktop {
						_ = desktop.Send(ev.Title(), ev.Body())
					}
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	_, err = p.Run()
	return err
}

func watch(stdout io.Writer) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	c, err := open(ctx)
	if err != nil {
		return err
	}
	for _, w := range c.Warnings() {
		fmt.Fprintln(stdout, "config:", w)
	}
	return commands.Watch(ctx, c, notify.Desktop{}, stdout, time.Now)
}

func status(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, `print {"text","tooltip","class"} for a status bar`)
	timeout := fs.Duration("timeout", 10*time.Second, "how long to wait for every instance to answer")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	c, err := open(ctx)
	if err != nil {
		fmt.Fprintln(stderr, "lazykuma:", err)
		return commands.ExitUnreachable
	}
	return commands.Status(ctx, c, *timeout, *asJSON, stdout)
}
