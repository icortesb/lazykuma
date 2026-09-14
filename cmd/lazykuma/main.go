// lazykuma is a terminal UI for Uptime Kuma v2.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/icortesb/lazykuma/internal/config"
	"github.com/icortesb/lazykuma/internal/core"
	"github.com/icortesb/lazykuma/internal/kuma"
	"github.com/icortesb/lazykuma/internal/ui"
)

// version is stamped by the Makefile from git describe.
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "lazykuma:", err)
		os.Exit(1)
	}
}

func run() error {
	cfgPath, tokPath, err := config.Paths()
	if err != nil {
		return err
	}
	c, err := core.Open(cfgPath, tokPath, core.Options{})
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.Run(ctx)

	p := tea.NewProgram(ui.New(ui.Deps{Core: c, Login: kuma.Login, Version: version}), tea.WithAltScreen())

	// Every state the core publishes becomes a message for the program.
	go func() {
		for {
			select {
			case u := <-c.Updates():
				if in, ok := c.Instance(u.Instance); ok {
					p.Send(ui.InstanceState{Name: u.Instance, State: in.State()})
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	_, err = p.Run()
	return err
}
