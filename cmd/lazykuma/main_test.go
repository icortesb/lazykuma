package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/icortesb/lazykuma/internal/autostart"
	"github.com/icortesb/lazykuma/internal/watchlock"
)

// isolate points every path lazykuma derives at a temporary directory, so no
// test reads or writes the real user's config, state or autostart entries.
func isolate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	t.Setenv("AppData", filepath.Join(dir, "appdata"))
	t.Setenv("LocalAppData", filepath.Join(dir, "localappdata"))
	return filepath.Join(dir, "state", "lazykuma")
}

func TestCommandLine(t *testing.T) {
	tests := []struct {
		args   []string
		code   int
		stdout string
		stderr string
	}{
		{[]string{"--version"}, 0, "dev", ""},
		{[]string{"version"}, 0, "dev", ""},
		{[]string{"help"}, 0, "lazykuma status [--json]", ""},
		{[]string{"help"}, 0, "lazykuma autostart [on|off]", ""},
		{[]string{"autostart", "maybe"}, 2, "", `unexpected "maybe"`},
		{[]string{"autostart", "on", "off"}, 2, "", `unexpected "off"`},
		{[]string{"autostart", "-h"}, 0, "", "usage: lazykuma autostart"},
		{[]string{"frobnicate"}, 2, "", `unknown command "frobnicate"`},
		{[]string{"status", "--nope"}, 2, "", "flag provided but not defined"},
		{[]string{"status", "-h"}, 0, "", "-timeout"},
		{[]string{"status", "extra"}, 2, "", `unexpected "extra"`},
		{[]string{"watch", "-h"}, 0, "", "usage: lazykuma watch"},
		{[]string{"watch", "--json"}, 2, "", "flag provided but not defined"},
		{[]string{"watch", "now"}, 2, "", `unexpected "now"`},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			isolate(t)
			var out, errOut bytes.Buffer
			if code := run(tt.args, &out, &errOut); code != tt.code {
				t.Fatalf("exit %d, want %d (stderr %q)", code, tt.code, errOut.String())
			}
			if !strings.Contains(out.String(), tt.stdout) {
				t.Errorf("stdout = %q, want it to contain %q", out.String(), tt.stdout)
			}
			if !strings.Contains(errOut.String(), tt.stderr) {
				t.Errorf("stderr = %q, want it to contain %q", errOut.String(), tt.stderr)
			}
		})
	}
}

func TestWatchRefusesWhileAnotherRuns(t *testing.T) {
	state := isolate(t)
	held, err := watchlock.Acquire(filepath.Join(state, "watch.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()

	for _, args := range [][]string{{"watch"}, {"watch", "--log"}} {
		var out, errOut bytes.Buffer
		if code := run(args, &out, &errOut); code != 1 {
			t.Fatalf("%v: exit %d, want 1 (stderr %q)", args, code, errOut.String())
		}
		want := "lazykuma: watch already running (pid " + strconv.Itoa(os.Getpid()) + ")"
		if !strings.Contains(errOut.String(), want) {
			t.Errorf("%v: stderr = %q, want it to contain %q", args, errOut.String(), want)
		}
	}
	// With --log the refusal is in the file too: nobody sees stderr there.
	log, err := os.ReadFile(filepath.Join(state, "watch.log"))
	if err != nil || !strings.Contains(string(log), "watch already running") {
		t.Errorf("watch.log = %q, %v; want the refusal", log, err)
	}
}

func TestAutostartText(t *testing.T) {
	tests := []struct {
		st   autostart.Status
		want string
	}{
		{autostart.Status{On: true, Kind: autostart.KindSystemd, Path: "/bin/lazykuma", Running: true, PID: 4242},
			"background alerts: on (systemd user service)\nruns: /bin/lazykuma\nwatch: running (pid 4242)\n"},
		{autostart.Status{On: true, Kind: autostart.KindXDG, Path: "/bin/lazykuma"},
			"background alerts: on (XDG autostart)\nruns: /bin/lazykuma\nwatch: not running\n"},
		{autostart.Status{},
			"background alerts: off\nwatch: not running\n"},
		{autostart.Status{Running: true, PID: 7},
			"background alerts: off\nwatch: running (pid 7), started by hand\n"},
	}
	for _, tt := range tests {
		if got := autostartText(tt.st); got != tt.want {
			t.Errorf("autostartText(%+v) = %q, want %q", tt.st, got, tt.want)
		}
	}
}

func TestBinaryWarning(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "lazykuma")
	if err := os.WriteFile(real, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "shim")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("no symlinks:", err)
	}
	other := filepath.Join(dir, "other")

	if w := binaryWarning(link, real); w != "" {
		t.Errorf("a symlink to the same binary warned: %q", w)
	}
	if w := binaryWarning(real, real); w != "" {
		t.Errorf("the same path warned: %q", w)
	}
	want := "lazykuma: background alerts run " + other + ", not this binary (" + real + `); run "lazykuma autostart on" to switch`
	if w := binaryWarning(other, real); w != want {
		t.Errorf("warning = %q, want %q", w, want)
	}
	if runtime.GOOS == "windows" {
		if w := binaryWarning(strings.ToUpper(real), real); w != "" {
			t.Errorf("a path differing by case warned: %q", w)
		}
	}
}

func TestWarnBinary(t *testing.T) {
	home := t.TempDir()
	m := &autostart.Manager{GOOS: "linux", Home: home, Config: filepath.Join(home, ".config"), Exe: "/new/lazykuma"}
	unit := filepath.Join(m.Config, "systemd", "user", "lazykuma-watch.service")
	warn := func() string {
		var b bytes.Buffer
		warnBinary(&b, m)
		return b.String()
	}

	if w := warn(); w != "" {
		t.Errorf("nothing registered warned: %q", w)
	}
	if err := os.MkdirAll(filepath.Dir(unit), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unit, []byte(autostart.SystemdUnit("/old/lazykuma", nil)), 0o644); err != nil {
		t.Fatal(err)
	}
	if w := warn(); !strings.Contains(w, "run /old/lazykuma, not this binary (/new/lazykuma)") {
		t.Errorf("warning = %q", w)
	}
	m.Exe = "/old/lazykuma"
	if w := warn(); w != "" {
		t.Errorf("the registered binary warned: %q", w)
	}
}
