// lazykuma is a terminal UI for Uptime Kuma v2.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/icortesb/lazykuma/internal/config"
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
	cfg, warnings, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	tokens, err := config.LoadTokens(tokPath)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Supervisors start inside ui.New, before the program exists: their
	// events wait for it.
	var p *tea.Program
	ready := make(chan struct{})
	start := func(in config.Instance) ui.Controller {
		sup := kuma.NewSupervisor(in.URL, func() string { return tokens.Get(in.Name, in.URL) }, func(ev kuma.Event) {
			select {
			case <-ready:
				p.Send(ui.InstanceEvent{Name: in.Name, Event: ev})
			case <-ctx.Done():
			}
		})
		go sup.Run(ctx)
		return sup
	}

	m := ui.New(ui.Deps{
		Config:     cfg,
		ConfigPath: cfgPath,
		Tokens:     tokens,
		Warnings:   warnings,
		Start:      start,
		Login:      kuma.Login,
		Version:    version,
	})
	p = tea.NewProgram(m, tea.WithAltScreen())
	close(ready)
	_, err = p.Run()
	return err
}
