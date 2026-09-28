package ui

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/icortesb/lazykuma/internal/autostart"
)

// fakeAutostart stands in for the manager: no systemd, no files.
type fakeAutostart struct {
	mu       sync.Mutex
	st       autostart.Status
	err      error // returned by Enable and Disable
	enables  int
	disables int
}

func (f *fakeAutostart) Status() (autostart.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.st, nil
}

func (f *fakeAutostart) Enable() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.enables++
	if f.err == nil {
		f.st = autostart.Status{On: true, Running: true, PID: 42}
	}
	return f.err
}

func (f *fakeAutostart) Disable() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.disables++
	if f.err == nil {
		f.st = autostart.Status{}
	}
	return f.err
}

// withAutostart gives the harness a switch, as New and Init would.
func withAutostart(h *harness, a Autostart) {
	h.m.deps.Autostart = a
	h.m.bg = bgAlerts{busy: true}
	h.run(h.m.Init())
}

// selectBg moves the cursor onto the switch.
func selectBg(h *harness) {
	h.press("down") // past Add instance; these tests have no instances
}

func TestBgAlertsHiddenWithoutAutostart(t *testing.T) {
	h := newHarness(t, nil)
	if v := h.view(); strings.Contains(v, "Background alerts") {
		t.Errorf("menu shows the switch without a manager:\n%s", v)
	}
	if h.m.Init() != nil {
		t.Error("Init has work without a manager")
	}
}

func TestBgAlertsLabelFollowsStatus(t *testing.T) {
	h := newHarness(t, nil)
	h.m.deps.Autostart = &fakeAutostart{}
	h.m.bg = bgAlerts{busy: true}
	if v := h.view(); !strings.Contains(v, "Background alerts: …") {
		t.Errorf("no loading label:\n%s", v)
	}
	h.run(h.m.Init())
	if v := h.view(); !strings.Contains(v, "Background alerts: off") {
		t.Errorf("no off label:\n%s", v)
	}

	h2 := newHarness(t, nil)
	withAutostart(h2, &fakeAutostart{st: autostart.Status{On: true}})
	selectBg(h2)
	v := h2.view()
	for _, want := range []string{"Background alerts: on", "Alerts keep coming with lazykuma closed"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q:\n%s", want, v)
		}
	}
}

func TestBgAlertsEnterTogglesBothWays(t *testing.T) {
	h := newHarness(t, nil)
	f := &fakeAutostart{}
	withAutostart(h, f)
	selectBg(h)

	h.press("enter")
	if f.enables != 1 || f.disables != 0 {
		t.Fatalf("enables=%d disables=%d after the first enter", f.enables, f.disables)
	}
	v := h.view()
	for _, want := range []string{"Background alerts: on", "Background alerts on: watch running (pid 42)"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q:\n%s", want, v)
		}
	}

	h.press("enter")
	if f.enables != 1 || f.disables != 1 {
		t.Fatalf("enables=%d disables=%d after the second enter", f.enables, f.disables)
	}
	if v := h.view(); !strings.Contains(v, "Background alerts: off") {
		t.Errorf("not off again:\n%s", v)
	}
}

func TestBgAlertsDoubleEnterRunsOnce(t *testing.T) {
	h := newHarness(t, nil)
	f := &fakeAutostart{}
	withAutostart(h, f)
	selectBg(h)

	cmd := h.hold("enter")
	h.press("enter") // the first is still on its way
	if f.enables != 0 || f.disables != 0 {
		t.Fatalf("second enter ran a toggle: enables=%d disables=%d", f.enables, f.disables)
	}
	h.run(cmd)
	if f.enables != 1 || f.disables != 0 {
		t.Errorf("enables=%d disables=%d, want one enable", f.enables, f.disables)
	}
}

func TestBgAlertsErrorGoesToFlash(t *testing.T) {
	h := newHarness(t, nil)
	f := &fakeAutostart{err: errors.New("systemctl: no bus")}
	withAutostart(h, f)
	selectBg(h)
	h.press("enter")
	v := h.view()
	for _, want := range []string{"systemctl: no bus", "Background alerts: off"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q:\n%s", want, v)
		}
	}
}
