//go:build !windows

package autostart

import (
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/icortesb/lazykuma/internal/watchlock"
)

// TestHelperWatch is the process the stop tests start detached: it holds the
// lock like a watch, and gives up by itself so a failed test leaves nothing
// behind for long.
func TestHelperWatch(t *testing.T) {
	path := os.Getenv("AUTOSTART_HELPER_LOCK")
	if path == "" {
		t.Skip("only runs as a child of the stop tests")
	}
	if os.Getenv("AUTOSTART_HELPER_IGNORE_TERM") != "" {
		signal.Ignore(syscall.SIGTERM)
	}
	l, err := watchlock.Acquire(path)
	if err != nil {
		os.Exit(1)
	}
	time.Sleep(30 * time.Second)
	l.Release()
}

// startHelper starts TestHelperWatch detached and returns its PID once it
// holds the lock.
func startHelper(t *testing.T, ignoreTerm bool) (pid int, lock string) {
	lock = filepath.Join(t.TempDir(), "watch.lock")
	t.Setenv("AUTOSTART_HELPER_LOCK", lock)
	if ignoreTerm {
		t.Setenv("AUTOSTART_HELPER_IGNORE_TERM", "1")
	}
	if err := startDetached([]string{os.Args[0], "-test.run=^TestHelperWatch$"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		pid, running, err := watchlock.Holder(lock)
		if err == nil && running && pid > 0 {
			t.Cleanup(func() {
				// Only while it still holds the lock: once it has ended,
				// its PID may name some other process.
				if p, running, _ := watchlock.Holder(lock); running && p == pid {
					syscall.Kill(pid, syscall.SIGKILL)
				}
			})
			return pid, lock
		}
		if time.Now().After(deadline) {
			t.Fatalf("the helper never took the lock: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestStopProcessEndsAWatch(t *testing.T) {
	pid, lock := startHelper(t, false)
	if err := stopProcess(pid, lock); err != nil {
		t.Fatal(err)
	}
	if _, running, _ := watchlock.Holder(lock); running {
		t.Fatal("the lock is still held")
	}
}

func TestStopProcessForcesAWatchThatIgnoresTerm(t *testing.T) {
	stopWait = 300 * time.Millisecond
	defer func() { stopWait = 5 * time.Second }()
	pid, lock := startHelper(t, true)
	if err := stopProcess(pid, lock); err != nil {
		t.Fatal(err)
	}
	if _, running, _ := watchlock.Holder(lock); running {
		t.Fatal("the lock is still held")
	}
}

func TestStopProcessRefusesPIDsThatNameGroups(t *testing.T) {
	for _, pid := range []int{0, -1} {
		if err := stopProcess(pid, filepath.Join(t.TempDir(), "watch.lock")); err == nil {
			t.Fatalf("stopProcess(%d) succeeded", pid)
		}
	}
}
