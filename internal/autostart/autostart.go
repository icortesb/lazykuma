// Package autostart registers "lazykuma watch" to start at login, in the
// user's own session and without admin rights: a systemd user service or an
// XDG autostart entry on Linux, a launch agent on macOS, a Run value on
// Windows. Every operating system action goes through a seam of Manager, so
// the logic is tested without touching the real session.
//
// On XDG autostart and Windows nothing supervises the watch, so Enable starts
// it itself: it stops whatever watch holds the lock, one started by hand in a
// terminal included, and starts a fresh detached one with the registered
// command.
package autostart

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/icortesb/lazykuma/internal/config"
	"github.com/icortesb/lazykuma/internal/watchlock"
)

// Status is what is registered and whether a watch runs.
type Status struct {
	On bool
	// Kind is "systemd user service", "XDG autostart", "launch agent" or
	// "Run key": the mechanism used, or the one Enable would use when off.
	Kind string
	// Path is the registered binary; "" when off.
	Path string
	// Running is whether any watch holds the lock, registered or not.
	Running bool
	PID     int
}

// The values of Status.Kind.
const (
	KindSystemd = "systemd user service"
	KindXDG     = "XDG autostart"
	KindLaunchd = "launch agent"
	KindRunKey  = "Run key"
)

// Manager registers and unregisters the watch. Default fills it for the
// running system; tests fill it with fakes and temporary directories.
type Manager struct {
	GOOS string
	// Home is the user's home directory.
	Home string
	// Config is XDG_CONFIG_HOME or Home/.config; only Linux uses it.
	Config string
	// Exe is the binary to register, symlinks resolved.
	Exe string
	// LockPath is the watch lock: whoever holds it is the running watch.
	LockPath string
	// LogPath is where a watch started with --log writes (XDG and Windows).
	LogPath string
	// Run runs a command and returns its combined output.
	Run func(name string, args ...string) ([]byte, error)
	// Start starts argv detached from this process, with no console.
	Start func(argv []string) error
	// Stop ends the process pid and returns once it no longer holds the lock.
	Stop func(pid int) error
	// Registry is the Run key; only Windows uses it.
	Registry Registry
	// UID names the launchd GUI domain, gui/<uid>; only macOS uses it.
	UID int
	// Env is the environment the systemd unit and the launch agent set for
	// the watch: XDG_CONFIG_HOME and XDG_STATE_HOME when the caller has them.
	Env map[string]string
}

// Default is a Manager for the running system. It runs no command: which
// Linux mechanism to use is decided by each call, because it depends on
// whether a systemd user session answers.
func Default() (*Manager, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return nil, err
	}
	lock, logPath, err := StatePaths()
	if err != nil {
		return nil, err
	}
	cfg := os.Getenv("XDG_CONFIG_HOME")
	if cfg == "" {
		cfg = filepath.Join(home, ".config")
	}
	// The service manager does not start the watch with this environment,
	// so it is written into the unit and the plist: the watch must read the
	// config and hold the lock the CLI does.
	env := map[string]string{}
	for _, k := range []string{"XDG_CONFIG_HOME", "XDG_STATE_HOME"} {
		if v := os.Getenv(k); v != "" {
			env[k] = v
		}
	}
	return &Manager{
		GOOS:     runtime.GOOS,
		Home:     home,
		Config:   cfg,
		Exe:      exe,
		LockPath: lock,
		LogPath:  logPath,
		Run: func(name string, args ...string) ([]byte, error) {
			return exec.Command(name, args...).CombinedOutput()
		},
		Start:    func(argv []string) error { return startDetached(home, argv) },
		Stop:     func(pid int) error { return stopProcess(pid, lock) },
		Registry: systemRegistry,
		UID:      os.Getuid(),
		Env:      env,
	}, nil
}

// StatePaths are the watch lock and the watch log, next to tokens.json. The
// watch command takes them from here, so it and the manager cannot disagree
// on where "running" is looked up.
func StatePaths() (lock, log string, err error) {
	_, tokenFile, err := config.Paths()
	if err != nil {
		return "", "", err
	}
	state := filepath.Dir(tokenFile)
	return filepath.Join(state, "watch.lock"), filepath.Join(state, "watch.log"), nil
}

// backend is one registration mechanism.
type backend interface {
	kind() string
	// registered reports whether the watch is registered, and with which
	// binary; path is "" when the registration cannot be read back.
	registered() (on bool, path string, err error)
	enable() error
	disable() error
}

// backend picks the mechanism. On Linux what is already registered wins, so
// Status and Disable see it even when the choice would differ today: a unit
// file stays systemd with the user session gone, and an XDG entry written
// when systemd did not answer stays XDG once it does. Enable is the one
// call that moves an XDG registration to systemd (forEnable).
func (m *Manager) backend(forEnable bool) backend {
	switch m.GOOS {
	case "darwin":
		return launchd{m}
	case "windows":
		return winRun{m}
	}
	if exists(m.unitPath()) {
		return systemd{m}
	}
	if !forEnable && exists(m.desktopPath()) {
		return xdg{m}
	}
	if _, err := m.Run("systemctl", "--user", "show-environment"); err == nil {
		return systemd{m}
	}
	return xdg{m}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Status reports the registration and the running watch.
func (m *Manager) Status() (Status, error) {
	b := m.backend(false)
	on, path, err := b.registered()
	if err != nil {
		return Status{}, err
	}
	pid, running, err := m.holder()
	if err != nil {
		return Status{}, err
	}
	st := Status{On: on, Kind: b.kind(), Running: running, PID: pid}
	if on {
		st.Path = path
	}
	return st, nil
}

// Registered reports whether the watch is registered and with which binary.
// Unlike Status it runs no command and probes no lock, so a status bar can
// call it on every poll: on Linux it does not ask systemd whether a user
// session answers, it only looks for the unit or the desktop entry.
func (m *Manager) Registered() (on bool, path string, err error) {
	var b backend
	switch m.GOOS {
	case "darwin":
		b = launchd{m}
	case "windows":
		b = winRun{m}
	default:
		switch {
		case exists(m.unitPath()):
			b = systemd{m}
		case exists(m.desktopPath()):
			b = xdg{m}
		default:
			return false, "", nil
		}
	}
	return b.registered()
}

// Enable registers the current binary and (re)starts the watch with it. It
// is idempotent: run again, it refreshes the registered path.
func (m *Manager) Enable() error {
	if m.Exe == "" {
		return errors.New("the path of this binary is unknown")
	}
	return m.backend(true).enable()
}

// Disable unregisters the watch and stops it.
func (m *Manager) Disable() error {
	return m.backend(false).disable()
}

// pidWait is how long to wait for a watch that holds the lock but has not
// written its PID yet.
var pidWait = time.Second

// holder is watchlock.Holder that waits out the moment between a starting
// watch taking the lock and writing its PID.
func (m *Manager) holder() (pid int, running bool, err error) {
	deadline := time.Now().Add(pidWait)
	for {
		pid, running, err = watchlock.Holder(m.LockPath)
		if err != nil || !running || pid > 0 || time.Now().After(deadline) {
			return pid, running, err
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// stopHolder stops whatever watch holds the lock. A PID of 0 is never
// signalled: on Unix that would reach the whole process group.
func (m *Manager) stopHolder() error {
	pid, running, err := m.holder()
	if err != nil || !running {
		return err
	}
	if pid <= 0 {
		return errors.New("a watch is starting and has not written its pid yet; try again")
	}
	if err := m.Stop(pid); err != nil {
		return fmt.Errorf("stop the running watch (pid %d): %w", pid, err)
	}
	return nil
}

// startWait is how long a watch started by Enable gets to take the lock.
var startWait = 2 * time.Second

// startWatch starts argv detached and waits for it to take the lock, so a
// watch that exits at once is reported rather than taken for running.
func (m *Manager) startWatch(argv []string) error {
	if err := m.Start(argv); err != nil {
		return err
	}
	deadline := time.Now().Add(startWait)
	for {
		_, running, err := watchlock.Holder(m.LockPath)
		if err != nil {
			return err
		}
		if running {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("watch exited right after starting; see %s", m.LogPath)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// cmdError is a failed command, told the way the user would type it, with
// what it printed.
type cmdError struct {
	cmd string
	out string
	err error
}

func (e *cmdError) Error() string {
	if e.out != "" {
		return e.cmd + ": " + e.out
	}
	return e.cmd + ": " + e.err.Error()
}

func (e *cmdError) Unwrap() error { return e.err }

func (m *Manager) run(name string, args ...string) error {
	out, err := m.Run(name, args...)
	if err != nil {
		return &cmdError{
			cmd: strings.Join(append([]string{name}, args...), " "),
			out: strings.TrimSpace(string(out)),
			err: err,
		}
	}
	return nil
}

// exitCode is the exit status of a failed command, or -1 when it did not
// exit.
func exitCode(err error) int {
	var ec interface{ ExitCode() int }
	if errors.As(err, &ec) {
		return ec.ExitCode()
	}
	return -1
}

// writeFile writes a registration file, creating its directory.
func writeFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return err
	}
	// WriteFile's mode applies only to a new file, and through the umask.
	return os.Chmod(path, 0o644)
}

// readFile reads a registration file; a missing one is not an error.
func readFile(path string) (content string, exists bool, err error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return string(b), true, nil
}

func removeFile(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
