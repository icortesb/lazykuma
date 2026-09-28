package autostart

import (
	"errors"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/icortesb/lazykuma/internal/watchlock"
)

// exitError is a failed command's exit status, as *exec.ExitError reports it.
type exitError int

func (e exitError) Error() string { return "exit status " + strconv.Itoa(int(e)) }
func (e exitError) ExitCode() int { return int(e) }

type fakeRegistry map[string]string

func (r fakeRegistry) Get(name string) (string, bool, error) {
	v, ok := r[name]
	return v, ok, nil
}

func (r fakeRegistry) Set(name, value string) error {
	r[name] = value
	return nil
}

func (r fakeRegistry) Delete(name string) error {
	delete(r, name)
	return nil
}

// env is a Manager wired to fakes: commands are recorded and answered from
// fail and out, Start and Stop are recorded, and a watch "runs" by holding a
// real lock in a temporary directory.
type env struct {
	t     *testing.T
	m     *Manager
	calls []string
	fail  map[string]error
	// seq answers a command from its head first, one error per call.
	seq map[string][]error
	// startDies makes a started watch exit before taking the lock.
	startDies bool
	out       map[string]string
	starts    [][]string
	stops     []int
	// events is the order of commands, starts and stops.
	events []string
	lock   *watchlock.Lock
	reg    fakeRegistry
}

func newEnv(t *testing.T, goos string) *env {
	dir := t.TempDir()
	e := &env{t: t, fail: map[string]error{}, seq: map[string][]error{}, out: map[string]string{}, reg: fakeRegistry{}}
	e.m = &Manager{
		GOOS: goos,
		Home: filepath.Join(dir, "home"),
		// XDG_CONFIG_HOME elsewhere, as the systemd unit must ignore it.
		Config:   filepath.Join(dir, "xdg-config"),
		Exe:      "/opt/lazy kuma/lazykuma",
		LockPath: filepath.Join(dir, "state", "watch.lock"),
		LogPath:  filepath.Join(dir, "state", "watch.log"),
		Run: func(name string, args ...string) ([]byte, error) {
			c := strings.Join(append([]string{name}, args...), " ")
			e.calls = append(e.calls, c)
			e.events = append(e.events, c)
			if q := e.seq[c]; len(q) > 0 {
				e.seq[c] = q[1:]
				return []byte(e.out[c]), q[0]
			}
			return []byte(e.out[c]), e.fail[c]
		},
		Start: func(argv []string) error {
			e.starts = append(e.starts, argv)
			e.events = append(e.events, "start")
			if !e.startDies && e.lock == nil {
				e.hold()
			}
			return nil
		},
		Stop: func(pid int) error {
			e.stops = append(e.stops, pid)
			e.events = append(e.events, "stop")
			if e.lock != nil {
				e.lock.Release()
				e.lock = nil
			}
			return nil
		},
		Registry: e.reg,
		UID:      501,
	}
	t.Cleanup(func() {
		if e.lock != nil {
			e.lock.Release()
		}
	})
	return e
}

// hold makes a watch run: this process holds the lock.
func (e *env) hold() {
	l, err := watchlock.Acquire(e.m.LockPath)
	if err != nil {
		e.t.Fatal(err)
	}
	e.lock = l
}

// noSystemd makes the systemd user session not answer.
func (e *env) noSystemd() {
	e.fail["systemctl --user show-environment"] = exitError(1)
}

func (e *env) wantCalls(want ...string) {
	e.t.Helper()
	if !slices.Equal(e.calls, want) {
		e.t.Fatalf("commands:\n  %s\nwant:\n  %s", strings.Join(e.calls, "\n  "), strings.Join(want, "\n  "))
	}
}

func (e *env) wantEvents(want ...string) {
	e.t.Helper()
	if !slices.Equal(e.events, want) {
		e.t.Fatalf("events = %q, want %q", e.events, want)
	}
}

func readString(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func wantGone(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s still there: %v", path, err)
	}
}

func TestSystemdUnit(t *testing.T) {
	want := `[Unit]
Description=lazykuma watch: Uptime Kuma alerts
After=network-online.target

[Service]
ExecStart="/usr/local/bin/lazykuma" watch
Restart=on-failure
RestartSec=30

[Install]
WantedBy=default.target
`
	if got := SystemdUnit("/usr/local/bin/lazykuma", nil); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestSystemdUnitQuotesThePath(t *testing.T) {
	got := SystemdUnit(`/home/a b/"x"/100%/$HOME/c\d`, nil)
	want := `ExecStart="/home/a b/\"x\"/100%%/$$HOME/c\\d" watch` + "\n"
	if !strings.Contains(got, want) {
		t.Fatalf("got:\n%s\nwant a line %q", got, want)
	}
}

func TestDesktopEntry(t *testing.T) {
	want := `[Desktop Entry]
Type=Application
Name=lazykuma watch
Comment=Uptime Kuma alerts
Exec="/usr/local/bin/lazykuma" watch --log
NoDisplay=true
X-GNOME-Autostart-enabled=true
Terminal=false
`
	if got := DesktopEntry("/usr/local/bin/lazykuma"); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestDesktopEntryQuotesThePath(t *testing.T) {
	// Inside quotes " ` $ \ take a backslash, and then the string escaping
	// doubles every backslash.
	got := DesktopEntry("/a b/\"q\"/`c`/$v/100%/s\\t")
	want := "Exec=\"/a b/\\\\\"q\\\\\"/\\\\`c\\\\`/\\\\$v/100%%/s\\\\\\\\t\" watch --log\n"
	if !strings.Contains(got, want) {
		t.Fatalf("got:\n%s\nwant a line %q", got, want)
	}
}

func TestLaunchdPlist(t *testing.T) {
	want := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>io.github.icortesb.lazykuma.watch</string>
	<key>ProgramArguments</key>
	<array>
		<string>/Users/me/bin/lazy&amp;kuma</string>
		<string>watch</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>ThrottleInterval</key>
	<integer>30</integer>
	<key>StandardOutPath</key>
	<string>/Users/me/Library/Logs/lazykuma/watch.log</string>
	<key>StandardErrorPath</key>
	<string>/Users/me/Library/Logs/lazykuma/watch.log</string>
	<key>ProcessType</key>
	<string>Background</string>
</dict>
</plist>
`
	if got := LaunchdPlist("/Users/me/bin/lazy&kuma", "/Users/me/Library/Logs/lazykuma/watch.log", nil); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRunValue(t *testing.T) {
	got := RunValue(`C:\Users\me\bin\lazykuma.exe`)
	want := `conhost.exe --headless "C:\Users\me\bin\lazykuma.exe" watch --log`
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestTheRegisteredPathReadsBack(t *testing.T) {
	for _, exe := range []string{
		"/usr/local/bin/lazykuma",
		"/home/me/my tools/lazykuma",
		`/home/me/"quoted"/lazykuma`,
		"/home/me/100%/%h/lazykuma",
		"/home/me/a&b<c>/lazykuma",
		"/home/josé/ツール/lazykuma",
		"/home/me/$HOME/`x`/lazykuma",
		`/home/me/back\slash\\/lazykuma`,
		`/home/me/\s\n/lazykuma`,
	} {
		if got := parseSystemd(SystemdUnit(exe, nil)); got != exe {
			t.Errorf("systemd: %q read back as %q", exe, got)
		}
		if got := parseDesktop(DesktopEntry(exe)); got != exe {
			t.Errorf("desktop: %q read back as %q", exe, got)
		}
		if got := parsePlist(LaunchdPlist(exe, "/tmp/log", nil)); got != exe {
			t.Errorf("plist: %q read back as %q", exe, got)
		}
	}
	for _, exe := range []string{
		`C:\Users\me\bin\lazykuma.exe`,
		`C:\Program Files\lazy kuma\lazykuma.exe`,
		`C:\Users\José\100% & more\ツール\lazykuma.exe`,
	} {
		if got := parseRunValue(RunValue(exe)); got != exe {
			t.Errorf("run value: %q read back as %q", exe, got)
		}
	}
}

func TestLinuxUsesSystemdWhenTheUserSessionAnswers(t *testing.T) {
	e := newEnv(t, "linux")
	st, err := e.m.Status()
	if err != nil {
		t.Fatal(err)
	}
	if st.Kind != KindSystemd || st.On || st.Path != "" || st.Running {
		t.Fatalf("Status = %+v", st)
	}
	e.wantCalls("systemctl --user show-environment")
}

func TestLinuxUsesXDGAutostartWithoutSystemd(t *testing.T) {
	e := newEnv(t, "linux")
	e.noSystemd()
	st, err := e.m.Status()
	if err != nil {
		t.Fatal(err)
	}
	if st.Kind != KindXDG || st.On {
		t.Fatalf("Status = %+v", st)
	}
}

func TestAUnitFileLeftBehindKeepsSystemd(t *testing.T) {
	// The session no longer answers, but off must still clean up the unit.
	e := newEnv(t, "linux")
	e.noSystemd()
	if err := writeFile(e.m.unitPath(), SystemdUnit(e.m.Exe, nil)); err != nil {
		t.Fatal(err)
	}
	st, err := e.m.Status()
	if err != nil {
		t.Fatal(err)
	}
	if st.Kind != KindSystemd || !st.On || st.Path != e.m.Exe {
		t.Fatalf("Status = %+v", st)
	}
	e.wantCalls()
}

func TestSystemdEnable(t *testing.T) {
	e := newEnv(t, "linux")
	if err := e.m.Enable(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(e.m.Home, ".config", "systemd", "user", "lazykuma-watch.service")
	if got := readString(t, path); got != SystemdUnit(e.m.Exe, nil) {
		t.Fatalf("unit:\n%s", got)
	}
	if fi, err := os.Stat(path); runtime.GOOS != "windows" && (err != nil || fi.Mode().Perm() != 0o644) {
		t.Fatalf("unit mode = %v, %v", fi.Mode(), err)
	}
	e.wantCalls(
		"systemctl --user show-environment",
		"systemctl --user daemon-reload",
		"systemctl --user enable lazykuma-watch.service",
		"systemctl --user restart lazykuma-watch.service",
	)
	if len(e.starts) != 0 || len(e.stops) != 0 {
		t.Fatalf("systemd starts the watch itself: starts %v, stops %v", e.starts, e.stops)
	}
}

func TestSystemdStatusWhenOnAndRunning(t *testing.T) {
	e := newEnv(t, "linux")
	if err := e.m.Enable(); err != nil {
		t.Fatal(err)
	}
	e.hold()
	st, err := e.m.Status()
	if err != nil {
		t.Fatal(err)
	}
	want := Status{On: true, Kind: KindSystemd, Path: e.m.Exe, Running: true, PID: os.Getpid()}
	if st != want {
		t.Fatalf("Status = %+v, want %+v", st, want)
	}
}

func TestAFailedCommandSaysWhichAndWhatItPrinted(t *testing.T) {
	e := newEnv(t, "linux")
	e.fail["systemctl --user enable lazykuma-watch.service"] = exitError(1)
	e.out["systemctl --user enable lazykuma-watch.service"] = "Failed to enable unit: Access denied\n"
	err := e.m.Enable()
	want := "systemctl --user enable lazykuma-watch.service: Failed to enable unit: Access denied"
	if err == nil || err.Error() != want {
		t.Fatalf("Enable = %v, want %q", err, want)
	}
	if exitCode(err) != 1 {
		t.Fatalf("exit code lost: %v", err)
	}
}

func TestSystemdDisable(t *testing.T) {
	e := newEnv(t, "linux")
	if err := e.m.Enable(); err != nil {
		t.Fatal(err)
	}
	e.calls = nil
	if err := e.m.Disable(); err != nil {
		t.Fatal(err)
	}
	wantGone(t, e.m.unitPath())
	e.wantCalls(
		"systemctl --user disable --now lazykuma-watch.service",
		"systemctl --user daemon-reload",
	)
}

func TestSystemdDisableWhenAlreadyOff(t *testing.T) {
	e := newEnv(t, "linux")
	e.fail["systemctl --user disable --now lazykuma-watch.service"] = exitError(1)
	e.out["systemctl --user disable --now lazykuma-watch.service"] = "Failed to disable unit: Unit file lazykuma-watch.service does not exist."
	if err := e.m.Disable(); err != nil {
		t.Fatal(err)
	}
}

func TestSystemdDisableFailsWhileTheUnitIsThere(t *testing.T) {
	e := newEnv(t, "linux")
	if err := e.m.Enable(); err != nil {
		t.Fatal(err)
	}
	e.fail["systemctl --user disable --now lazykuma-watch.service"] = exitError(1)
	e.out["systemctl --user disable --now lazykuma-watch.service"] = "Failed to connect to bus"
	err := e.m.Disable()
	if err == nil || err.Error() != "systemctl --user disable --now lazykuma-watch.service: Failed to connect to bus" {
		t.Fatalf("Disable = %v", err)
	}
	if _, err := os.Stat(e.m.unitPath()); err != nil {
		t.Fatalf("unit removed although disabling failed: %v", err)
	}
}

func TestXDGEnableReplacesTheRunningWatch(t *testing.T) {
	e := newEnv(t, "linux")
	e.noSystemd()
	e.hold()
	if err := e.m.Enable(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(e.m.Config, "autostart", "lazykuma-watch.desktop")
	if got := readString(t, path); got != DesktopEntry(e.m.Exe) {
		t.Fatalf("entry:\n%s", got)
	}
	if !slices.Equal(e.stops, []int{os.Getpid()}) {
		t.Fatalf("stops = %v", e.stops)
	}
	if len(e.starts) != 1 || !slices.Equal(e.starts[0], []string{e.m.Exe, "watch", "--log"}) {
		t.Fatalf("starts = %q", e.starts)
	}
	e.wantEvents("systemctl --user show-environment", "stop", "start")

	st, err := e.m.Status()
	if err != nil {
		t.Fatal(err)
	}
	if st.Kind != KindXDG || !st.On || st.Path != e.m.Exe {
		t.Fatalf("Status = %+v", st)
	}
}

func TestXDGEnableWithNothingRunningOnlyStarts(t *testing.T) {
	e := newEnv(t, "linux")
	e.noSystemd()
	if err := e.m.Enable(); err != nil {
		t.Fatal(err)
	}
	if len(e.stops) != 0 || len(e.starts) != 1 {
		t.Fatalf("stops %v, starts %q", e.stops, e.starts)
	}
}

func TestXDGDisable(t *testing.T) {
	e := newEnv(t, "linux")
	e.noSystemd()
	if err := e.m.Enable(); err != nil {
		t.Fatal(err)
	}
	if err := e.m.Disable(); err != nil {
		t.Fatal(err)
	}
	wantGone(t, e.m.desktopPath())
	if !slices.Equal(e.stops, []int{os.Getpid()}) {
		t.Fatalf("stops = %v", e.stops)
	}
	st, err := e.m.Status()
	if err != nil {
		t.Fatal(err)
	}
	if st.On || st.Running || st.Path != "" {
		t.Fatalf("Status = %+v", st)
	}
}

func TestAWatchWithoutAPIDIsNeverSignalled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the lock file cannot be emptied under a held range on Windows")
	}
	pidWait = 100 * time.Millisecond
	defer func() { pidWait = time.Second }()
	e := newEnv(t, "linux")
	e.noSystemd()
	e.hold()
	// A watch that has taken the lock but not yet written its PID.
	if err := os.Truncate(e.m.LockPath, 0); err != nil {
		t.Fatal(err)
	}
	if err := e.m.Enable(); err == nil {
		t.Fatal("Enable succeeded")
	}
	if len(e.stops) != 0 || len(e.starts) != 0 {
		t.Fatalf("stops %v, starts %q", e.stops, e.starts)
	}
}

func TestLaunchdEnable(t *testing.T) {
	e := newEnv(t, "darwin")
	// Not loaded yet: bootout fails, and that is fine.
	e.fail["launchctl bootout gui/501/io.github.icortesb.lazykuma.watch"] = exitError(113)
	if err := e.m.Enable(); err != nil {
		t.Fatal(err)
	}
	plist := filepath.Join(e.m.Home, "Library", "LaunchAgents", "io.github.icortesb.lazykuma.watch.plist")
	logPath := filepath.Join(e.m.Home, "Library", "Logs", "lazykuma", "watch.log")
	if got := readString(t, plist); got != LaunchdPlist(e.m.Exe, logPath, nil) {
		t.Fatalf("plist:\n%s", got)
	}
	if fi, err := os.Stat(filepath.Dir(logPath)); err != nil || !fi.IsDir() {
		t.Fatalf("log directory: %v", err)
	}
	e.wantCalls(
		"launchctl bootout gui/501/io.github.icortesb.lazykuma.watch",
		"launchctl bootstrap gui/501 "+plist,
	)
	st, err := e.m.Status()
	if err != nil {
		t.Fatal(err)
	}
	if st.Kind != KindLaunchd || !st.On || st.Path != e.m.Exe {
		t.Fatalf("Status = %+v", st)
	}
}

// fastBootstrap makes the bootstrap retries quick.
func fastBootstrap(t *testing.T) {
	bootstrapWait, bootstrapRetry = 200*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() { bootstrapWait, bootstrapRetry = 5*time.Second, 250*time.Millisecond })
}

func TestLaunchdEnableRetriesWhileTheOldJobGoes(t *testing.T) {
	fastBootstrap(t)
	for _, code := range []int{5, 37} {
		e := newEnv(t, "darwin")
		bootstrap := "launchctl bootstrap gui/501 " + e.m.plistPath()
		e.seq[bootstrap] = []error{exitError(code), exitError(code)}
		if err := e.m.Enable(); err != nil {
			t.Fatalf("exit %d: Enable = %v", code, err)
		}
		e.wantCalls("launchctl bootout gui/501/io.github.icortesb.lazykuma.watch", bootstrap, bootstrap, bootstrap)
	}
}

func TestLaunchdEnableDoesNotRetryOtherFailures(t *testing.T) {
	e := newEnv(t, "darwin")
	bootstrap := "launchctl bootstrap gui/501 " + e.m.plistPath()
	e.fail[bootstrap] = exitError(1)
	if err := e.m.Enable(); err == nil {
		t.Fatal("Enable succeeded")
	}
	e.wantCalls("launchctl bootout gui/501/io.github.icortesb.lazykuma.watch", bootstrap)
}

func TestLaunchdEnableFails(t *testing.T) {
	fastBootstrap(t)
	e := newEnv(t, "darwin")
	e.fail["launchctl bootstrap gui/501 "+e.m.plistPath()] = exitError(5)
	e.out["launchctl bootstrap gui/501 "+e.m.plistPath()] = "Bootstrap failed: 5: Input/output error\n"
	err := e.m.Enable()
	if err == nil || !strings.HasSuffix(err.Error(), ": Bootstrap failed: 5: Input/output error") {
		t.Fatalf("Enable = %v", err)
	}
}

func TestLaunchdDisable(t *testing.T) {
	for _, code := range []int{0, 3, 113} {
		e := newEnv(t, "darwin")
		if err := e.m.Enable(); err != nil {
			t.Fatal(err)
		}
		if code != 0 {
			e.fail["launchctl bootout gui/501/io.github.icortesb.lazykuma.watch"] = exitError(code)
		}
		if err := e.m.Disable(); err != nil {
			t.Fatalf("bootout exit %d: Disable = %v", code, err)
		}
		wantGone(t, e.m.plistPath())
	}
}

func TestLaunchdDisableFails(t *testing.T) {
	e := newEnv(t, "darwin")
	if err := e.m.Enable(); err != nil {
		t.Fatal(err)
	}
	e.fail["launchctl bootout gui/501/io.github.icortesb.lazykuma.watch"] = exitError(5)
	if err := e.m.Disable(); err == nil {
		t.Fatal("Disable succeeded")
	}
	if _, err := os.Stat(e.m.plistPath()); err != nil {
		t.Fatalf("plist removed although bootout failed: %v", err)
	}
}

func TestWindowsEnableReplacesTheRunningWatch(t *testing.T) {
	e := newEnv(t, "windows")
	e.m.Exe = `C:\Users\me\bin\lazykuma.exe`
	e.hold()
	if err := e.m.Enable(); err != nil {
		t.Fatal(err)
	}
	if got := e.reg["lazykuma-watch"]; got != RunValue(e.m.Exe) {
		t.Fatalf("Run value = %q", got)
	}
	e.wantEvents("stop", "start")
	if !slices.Equal(e.starts[0], []string{"conhost.exe", "--headless", e.m.Exe, "watch", "--log"}) {
		t.Fatalf("starts = %q", e.starts)
	}
	st, err := e.m.Status()
	if err != nil {
		t.Fatal(err)
	}
	want := Status{On: true, Kind: KindRunKey, Path: e.m.Exe, Running: true, PID: os.Getpid()}
	if st != want {
		t.Fatalf("Status = %+v, want %+v", st, want)
	}
}

func TestWindowsDisable(t *testing.T) {
	e := newEnv(t, "windows")
	if err := e.m.Enable(); err != nil {
		t.Fatal(err)
	}
	if err := e.m.Disable(); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.reg["lazykuma-watch"]; ok {
		t.Fatal("Run value left behind")
	}
	if !slices.Equal(e.stops, []int{os.Getpid()}) {
		t.Fatalf("stops = %v", e.stops)
	}
}

func TestWindowsWithoutARegistry(t *testing.T) {
	e := newEnv(t, "windows")
	e.m.Registry = nil
	if _, err := e.m.Status(); err == nil {
		t.Fatal("Status succeeded")
	}
}

func TestDefault(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	m, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if m.GOOS != runtime.GOOS || m.Home == "" || m.Exe == "" || !filepath.IsAbs(m.Exe) {
		t.Fatalf("Default = %+v", m)
	}
	if m.Config != filepath.Join(dir, "config") {
		t.Fatalf("Config = %s", m.Config)
	}
	if m.LockPath != filepath.Join(dir, "state", "lazykuma", "watch.lock") ||
		m.LogPath != filepath.Join(dir, "state", "lazykuma", "watch.log") {
		t.Fatalf("LockPath = %s, LogPath = %s", m.LockPath, m.LogPath)
	}
	if m.Run == nil || m.Start == nil || m.Stop == nil {
		t.Fatal("a seam is missing")
	}
	want := map[string]string{"XDG_CONFIG_HOME": filepath.Join(dir, "config"), "XDG_STATE_HOME": filepath.Join(dir, "state")}
	if !maps.Equal(m.Env, want) {
		t.Fatalf("Env = %v, want %v", m.Env, want)
	}
	if (m.Registry != nil) != (runtime.GOOS == "windows") {
		t.Fatalf("Registry = %v on %s", m.Registry, runtime.GOOS)
	}
}

func TestSystemdUnitSetsTheCallersXDGDirectories(t *testing.T) {
	got := SystemdUnit("/usr/bin/lazykuma", map[string]string{
		"XDG_STATE_HOME":  "/home/me/state",
		"XDG_CONFIG_HOME": `/home/me/my "cfg"/100%/$x`,
	})
	want := `[Service]
Environment="XDG_CONFIG_HOME=/home/me/my \"cfg\"/100%%/$x"
Environment="XDG_STATE_HOME=/home/me/state"
ExecStart="/usr/bin/lazykuma" watch
`
	if !strings.Contains(got, want) {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if parseSystemd(got) != "/usr/bin/lazykuma" {
		t.Fatalf("ExecStart read back as %q", parseSystemd(got))
	}
}

func TestLaunchdPlistSetsTheCallersXDGDirectories(t *testing.T) {
	got := LaunchdPlist("/usr/bin/lazykuma", "/tmp/log", map[string]string{
		"XDG_STATE_HOME":  "/Users/me/state",
		"XDG_CONFIG_HOME": "/Users/me/a&b",
	})
	want := `		<string>watch</string>
	</array>
	<key>EnvironmentVariables</key>
	<dict>
		<key>XDG_CONFIG_HOME</key>
		<string>/Users/me/a&amp;b</string>
		<key>XDG_STATE_HOME</key>
		<string>/Users/me/state</string>
	</dict>
	<key>RunAtLoad</key>
`
	if !strings.Contains(got, want) {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if parsePlist(got) != "/usr/bin/lazykuma" {
		t.Fatalf("ProgramArguments read back as %q", parsePlist(got))
	}
}

func TestSystemdEnableWritesTheManagersEnvironment(t *testing.T) {
	e := newEnv(t, "linux")
	e.m.Env = map[string]string{"XDG_CONFIG_HOME": e.m.Config}
	if err := e.m.Enable(); err != nil {
		t.Fatal(err)
	}
	if got := readString(t, e.m.unitPath()); got != SystemdUnit(e.m.Exe, e.m.Env) {
		t.Fatalf("unit:\n%s", got)
	}
	if strings.HasPrefix(e.m.unitPath(), e.m.Config) {
		t.Fatalf("unit under XDG_CONFIG_HOME: %s", e.m.unitPath())
	}
}

func TestLaunchdEnableWritesTheManagersEnvironment(t *testing.T) {
	e := newEnv(t, "darwin")
	e.m.Env = map[string]string{"XDG_STATE_HOME": "/Users/me/state"}
	if err := e.m.Enable(); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(e.m.Home, "Library", "Logs", "lazykuma", "watch.log")
	if got := readString(t, e.m.plistPath()); got != LaunchdPlist(e.m.Exe, logPath, e.m.Env) {
		t.Fatalf("plist:\n%s", got)
	}
}

// xdgThenSystemd registers with XDG autostart while systemd does not answer,
// then lets it answer.
func xdgThenSystemd(t *testing.T) *env {
	e := newEnv(t, "linux")
	e.noSystemd()
	if err := e.m.Enable(); err != nil {
		t.Fatal(err)
	}
	delete(e.fail, "systemctl --user show-environment")
	e.calls, e.events, e.starts, e.stops = nil, nil, nil, nil
	return e
}

func TestAnXDGEntryStaysOnOnceSystemdAnswers(t *testing.T) {
	e := xdgThenSystemd(t)
	st, err := e.m.Status()
	if err != nil {
		t.Fatal(err)
	}
	if st.Kind != KindXDG || !st.On || st.Path != e.m.Exe {
		t.Fatalf("Status = %+v", st)
	}
}

func TestSystemdEnableRemovesAnXDGEntry(t *testing.T) {
	e := xdgThenSystemd(t)
	if err := e.m.Enable(); err != nil {
		t.Fatal(err)
	}
	wantGone(t, e.m.desktopPath())
	if len(e.stops) != 0 || len(e.starts) != 0 {
		t.Fatalf("stops %v, starts %q", e.stops, e.starts)
	}
	st, err := e.m.Status()
	if err != nil {
		t.Fatal(err)
	}
	if st.Kind != KindSystemd || !st.On {
		t.Fatalf("Status = %+v", st)
	}
}

func TestSystemdDisableRemovesAnXDGEntry(t *testing.T) {
	e := newEnv(t, "linux")
	if err := e.m.Enable(); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(e.m.desktopPath(), DesktopEntry(e.m.Exe)); err != nil {
		t.Fatal(err)
	}
	if err := e.m.Disable(); err != nil {
		t.Fatal(err)
	}
	wantGone(t, e.m.unitPath())
	wantGone(t, e.m.desktopPath())
}

func TestAWatchThatExitsAtOnceIsReported(t *testing.T) {
	startWait = 100 * time.Millisecond
	defer func() { startWait = 2 * time.Second }()
	for _, goos := range []string{"linux", "windows"} {
		e := newEnv(t, goos)
		e.noSystemd()
		e.startDies = true
		err := e.m.Enable()
		want := "watch exited right after starting; see " + e.m.LogPath
		if err == nil || err.Error() != want {
			t.Fatalf("%s: Enable = %v, want %q", goos, err, want)
		}
	}
}
