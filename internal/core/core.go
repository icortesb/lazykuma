// Package core is what the terminal UI, the watch command and the status
// command all stand on: it reads the config, keeps every instance connected,
// folds what they say into state, and hands out the actions that change them.
package core

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/icortesb/lazykuma/internal/config"
	"github.com/icortesb/lazykuma/internal/kuma"
	"github.com/icortesb/lazykuma/internal/state"
)

// Update names an instance whose state changed. It carries no state on
// purpose: the reader asks the instance, so a reader that falls behind sees
// the latest state rather than a queue of stale ones.
type Update struct {
	Instance string
}

// Core owns the instances and their state.
type Core struct {
	cfgPath  string
	tokens   *config.Tokens
	warnings []string
	now      func() time.Time

	mu    sync.Mutex
	cfg   config.Config
	insts map[string]*Instance
	order []string
	// dirty are the instances whose change has not been announced yet, so
	// a burst of events becomes one wake-up per instance.
	dirty map[string]bool

	newSupervisor func(url string, token func() string, out func(kuma.Event)) Supervisor
	updates       chan Update
	wake          chan struct{}
	runCtx        context.Context
}

// Options are Core's seams, all optional.
type Options struct {
	// Now is the clock the state reducer sees; time.Now when nil.
	Now func() time.Time
	// Supervisor builds an instance's connection; the real one when nil.
	Supervisor func(url string, token func() string, out func(kuma.Event)) Supervisor
}

// Supervisor is the part of kuma.Supervisor that Core uses, so tests can
// stand in for it.
type Supervisor interface {
	Run(ctx context.Context)
	Retry()
	Session() (*kuma.Session, error)
}

// Open reads the config and the tokens. Config problems come back as
// warnings rather than errors: lazykuma starts with what it could read.
func Open(configPath, tokenPath string, opt Options) (*Core, error) {
	cfg, warnings, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}
	tokens, err := config.LoadTokens(tokenPath)
	if err != nil {
		return nil, err
	}
	if opt.Now == nil {
		opt.Now = time.Now
	}
	if opt.Supervisor == nil {
		opt.Supervisor = func(url string, token func() string, out func(kuma.Event)) Supervisor {
			return kuma.NewSupervisor(url, token, out)
		}
	}
	c := &Core{
		cfgPath: configPath, tokens: tokens, warnings: warnings, now: opt.Now,
		cfg: cfg, insts: map[string]*Instance{}, dirty: map[string]bool{},
		newSupervisor: opt.Supervisor,
		updates:       make(chan Update), wake: make(chan struct{}, 1),
		runCtx: context.Background(),
	}
	for _, in := range cfg.Instances {
		c.newInstance(in)
	}
	return c, nil
}

// Warnings are the config lines Load skipped.
func (c *Core) Warnings() []string { return c.warnings }

// Updates names an instance each time its state changes. Changes coalesce
// while a reader is busy, so it never falls behind; ask the instance for
// the state itself.
func (c *Core) Updates() <-chan Update { return c.updates }

// Instances are the configured instances, in config order.
func (c *Core) Instances() []*Instance {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*Instance, 0, len(c.order))
	for _, name := range c.order {
		out = append(out, c.insts[name])
	}
	return out
}

// Instance is the one with this name.
func (c *Core) Instance(name string) (*Instance, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	in, ok := c.insts[name]
	return in, ok
}

// Snapshot is every instance's state right now.
func (c *Core) Snapshot() map[string]state.Instance {
	out := map[string]state.Instance{}
	for _, in := range c.Instances() {
		out[in.Name()] = in.State()
	}
	return out
}

// Run connects every instance and keeps them connected until ctx ends. It
// returns immediately; the work happens in goroutines.
func (c *Core) Run(ctx context.Context) {
	c.mu.Lock()
	c.runCtx = ctx
	c.mu.Unlock()
	for _, in := range c.Instances() {
		go in.sup.Run(ctx)
	}
	go c.announce(ctx)
}

// announce turns the instances marked dirty into updates, one per instance,
// however many changes piled up while the reader was busy.
func (c *Core) announce(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.wake:
		}
		for _, name := range c.takeDirty() {
			select {
			case c.updates <- Update{Instance: name}:
			case <-ctx.Done():
				return
			}
		}
	}
}

// takeDirty is the instances that changed since the last call.
func (c *Core) takeDirty() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.dirty))
	for name := range c.dirty {
		out = append(out, name)
		delete(c.dirty, name)
	}
	return out
}

// changed marks an instance as worth announcing.
func (c *Core) changed(name string) {
	c.mu.Lock()
	c.dirty[name] = true
	c.mu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default: // a wake-up is already pending; it will take this one too
	}
}

// Add appends an instance to the config file and starts watching it. The
// config file keeps its comments and any entries Load skipped.
func (c *Core) Add(ctx context.Context, in config.Instance) (*Instance, error) {
	c.mu.Lock()
	next := c.cfg
	next.Instances = append([]config.Instance(nil), c.cfg.Instances...)
	err := next.Add(in)
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if err := config.AppendInstance(c.cfgPath, in); err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.cfg = next
	c.mu.Unlock()

	inst := c.newInstance(in)
	go inst.sup.Run(c.context(ctx))
	return inst, nil
}

// context is the context the instances run under: the one Run was given, so
// an instance added later stops with the rest.
func (c *Core) context(fallback context.Context) context.Context {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.runCtx != nil {
		return c.runCtx
	}
	return fallback
}

// SetToken stores an instance's login token and wakes its connection.
func (c *Core) SetToken(name, url, token string) error {
	if err := c.tokens.Set(name, url, token); err != nil {
		return err
	}
	if in, ok := c.Instance(name); ok {
		in.apply(kuma.Connecting{})
		in.sup.Retry()
	}
	return nil
}

func (c *Core) newInstance(cfg config.Instance) *Instance {
	in := &Instance{cfg: cfg, core: c}
	in.sup = c.newSupervisor(cfg.URL, func() string { return c.tokens.Get(cfg.Name, cfg.URL) }, in.apply)
	c.mu.Lock()
	c.insts[cfg.Name] = in
	c.order = append(c.order, cfg.Name)
	c.mu.Unlock()
	return in
}

// Instance is one Kuma: its state, and the actions that change it.
type Instance struct {
	cfg  config.Instance
	core *Core
	sup  Supervisor

	mu sync.Mutex
	st state.Instance
}

// Name and URL are how the instance is configured.
func (i *Instance) Name() string { return i.cfg.Name }
func (i *Instance) URL() string  { return i.cfg.URL }

// Config is the instance as the config file has it.
func (i *Instance) Config() config.Instance { return i.cfg }

// State is what is known about it right now.
func (i *Instance) State() state.Instance {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.st
}

// Retry wakes the connection: after a login stored a new token, or to cut a
// backoff short.
func (i *Instance) Retry() { i.sup.Retry() }

// apply folds one event into the instance's state. It is the only writer,
// so a fold can never be built on a snapshot another goroutine has already
// replaced.
func (i *Instance) apply(ev kuma.Event) {
	i.mu.Lock()
	i.st = state.Apply(i.st, ev, i.core.now())
	i.mu.Unlock()
	i.core.changed(i.cfg.Name)
}

// session is the live connection, or ErrNotConnected.
func (i *Instance) session() (*kuma.Session, error) { return i.sup.Session() }

// Pause stops a monitor; Resume starts it again.
func (i *Instance) Pause(ctx context.Context, id int) error {
	s, err := i.session()
	if err != nil {
		return err
	}
	return s.Pause(ctx, id)
}

func (i *Instance) Resume(ctx context.Context, id int) error {
	s, err := i.session()
	if err != nil {
		return err
	}
	return s.Resume(ctx, id)
}

// GetMonitor is the whole monitor, the shape EditMonitor wants back.
func (i *Instance) GetMonitor(ctx context.Context, id int) (kuma.RawMonitor, error) {
	s, err := i.session()
	if err != nil {
		return nil, err
	}
	return s.GetMonitor(ctx, id)
}

// AddMonitor creates a monitor and returns its id.
func (i *Instance) AddMonitor(ctx context.Context, m kuma.RawMonitor) (int, error) {
	s, err := i.session()
	if err != nil {
		return 0, err
	}
	return s.AddMonitor(ctx, m)
}

// EditMonitor saves a whole monitor.
func (i *Instance) EditMonitor(ctx context.Context, m kuma.RawMonitor) error {
	s, err := i.session()
	if err != nil {
		return err
	}
	return s.EditMonitor(ctx, m)
}

// DeleteMonitor removes a monitor and its history.
func (i *Instance) DeleteMonitor(ctx context.Context, id int) error {
	s, err := i.session()
	if err != nil {
		return err
	}
	return s.DeleteMonitor(ctx, id)
}

// SaveNotification creates a channel, or edits the one with that id.
func (i *Instance) SaveNotification(ctx context.Context, cfg map[string]any, id int) (int, error) {
	s, err := i.session()
	if err != nil {
		return 0, err
	}
	return s.SaveNotification(ctx, cfg, id)
}

// DeleteNotification removes a channel.
func (i *Instance) DeleteNotification(ctx context.Context, id int) error {
	s, err := i.session()
	if err != nil {
		return err
	}
	return s.DeleteNotification(ctx, id)
}

// TestNotification asks Kuma to send a test message through a channel.
func (i *Instance) TestNotification(ctx context.Context, cfg map[string]any) error {
	s, err := i.session()
	if err != nil {
		return err
	}
	return s.TestNotification(ctx, cfg)
}

// Silence quiets the given monitors: until EndMaintenance when start and end
// are zero, or between those two times.
func (i *Instance) Silence(ctx context.Context, title string, monitorIDs []int, start, end time.Time) (int, error) {
	s, err := i.session()
	if err != nil {
		return 0, err
	}
	if len(monitorIDs) == 0 {
		return 0, fmt.Errorf("lazykuma: a maintenance needs at least one monitor")
	}
	m := map[string]any{
		"title": title, "description": "", "active": true, "intervalDay": 1,
		"timeRange":   []any{map[string]any{"hours": 0, "minutes": 0}, map[string]any{"hours": 0, "minutes": 0}},
		"weekdays":    []any{},
		"daysOfMonth": []any{},
	}
	if start.IsZero() && end.IsZero() {
		m["strategy"] = "manual"
		m["dateRange"] = []any{nil}
		m["timezoneOption"] = "SAME_AS_SERVER"
	} else {
		if !end.After(start) {
			return 0, fmt.Errorf("lazykuma: the window ends before it starts")
		}
		// In UTC on purpose: Go names an unset TZ "Local", which Kuma feeds
		// to dayjs and rejects. The times themselves are absolute either way.
		m["strategy"] = "single"
		m["dateRange"] = []any{kumaTime(start.UTC()), kumaTime(end.UTC())}
		m["timezoneOption"] = "UTC"
	}
	id, err := s.AddMaintenance(ctx, m)
	if err != nil {
		return 0, err
	}
	if err := s.SetMaintenanceMonitors(ctx, id, monitorIDs); err != nil {
		// A window covering nothing would sit in the list forever.
		_ = s.DeleteMaintenance(ctx, id)
		return 0, err
	}
	return id, nil
}

// DeleteMaintenance removes a maintenance window for good, so its monitors
// speak again.
func (i *Instance) DeleteMaintenance(ctx context.Context, id int) error {
	s, err := i.session()
	if err != nil {
		return err
	}
	return s.DeleteMaintenance(ctx, id)
}

// kumaTime is how Kuma writes the ends of a maintenance window.
func kumaTime(t time.Time) string { return t.Format("2006-01-02 15:04:05") }
