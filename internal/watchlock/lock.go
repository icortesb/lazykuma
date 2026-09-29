// Package watchlock lets one "lazykuma watch" run per user. Running means
// holding an operating system lock on a file: the OS drops the lock when the
// process dies, so a crash never leaves a stale "running" behind, which a PID
// file alone could not promise.
package watchlock

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ErrRunning is returned by Acquire when another process holds the lock.
type ErrRunning struct{ PID int }

func (e ErrRunning) Error() string { return fmt.Sprintf("watch already running (pid %d)", e.PID) }

// Lock is a held lock.
type Lock struct {
	f    *os.File
	path string
}

// Acquire creates the directory if needed, opens path, takes the lock
// without blocking, truncates and writes the PID + "\n".
func Acquire(path string) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, held, err := openLocked(path, true)
	if err != nil {
		return nil, err
	}
	if !held {
		pid := readPID(f)
		f.Close()
		return nil, ErrRunning{PID: pid}
	}
	// The lock is ours, so the file is ours to rewrite.
	if err := f.Truncate(0); err == nil {
		_, err = f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	}
	if err != nil {
		unlock(f)
		f.Close()
		return nil, err
	}
	return &Lock{f: f, path: path}, nil
}

// AcquireWait is Acquire that keeps trying for up to wait while the lock is
// held. Holder probes by taking the lock for an instant, so a watch starting
// while someone asks for its status can find it held; waiting absorbs that,
// and a real holder still yields ErrRunning once wait has passed.
func AcquireWait(path string, wait time.Duration) (*Lock, error) {
	deadline := time.Now().Add(wait)
	for {
		l, err := Acquire(path)
		if !errors.As(err, new(ErrRunning)) || time.Now().After(deadline) {
			return l, err
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Release unlocks, closes and removes the file. The file is closed before it
// is removed because Windows refuses to remove an open file.
func (l *Lock) Release() error {
	uerr := unlock(l.f)
	cerr := l.f.Close()
	rerr := os.Remove(l.path)
	if errors.Is(rerr, os.ErrNotExist) {
		rerr = nil
	}
	return errors.Join(uerr, cerr, rerr)
}

// Holder reports the PID of the process holding the lock, if any. It
// never takes the lock for longer than the probe: it tries to lock; on
// success it unlocks and reports (0, false); on contention it reads the PID.
func Holder(path string) (pid int, running bool, err error) {
	f, held, err := openLocked(path, false)
	if errors.Is(err, os.ErrNotExist) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	defer f.Close()
	if held {
		return 0, false, unlock(f)
	}
	return readPID(f), true, nil
}

// afterOpen is a test seam: it runs between opening the file and locking it,
// the window in which a releasing holder can unlink the file.
var afterOpen = func() {}

// openLocked opens path and tries to lock it. A lock taken on a file that was
// unlinked meanwhile (Release removes the file) guards nothing: a third
// process would create a new file at path and lock that too. So a successful
// lock only counts when the open file is still the one at path; otherwise it
// starts over. On contention it returns the open, unlocked file so the caller
// can read the holder's PID.
func openLocked(path string, create bool) (*os.File, bool, error) {
	flag := os.O_RDWR
	if create {
		flag |= os.O_CREATE
	}
	for {
		f, err := os.OpenFile(path, flag, 0o644)
		if err != nil {
			return nil, false, err
		}
		afterOpen()
		held, err := tryLock(f)
		if err != nil {
			f.Close()
			return nil, false, err
		}
		if !held {
			return f, false, nil
		}
		if sameFile(f, path) {
			return f, true, nil
		}
		unlock(f)
		f.Close()
	}
}

func sameFile(f *os.File, path string) bool {
	a, err := f.Stat()
	if err != nil {
		return false
	}
	b, err := os.Stat(path)
	return err == nil && os.SameFile(a, b)
}

// readPID returns 0 when the holder has locked the file but not yet written
// its PID, or when the content is not a number.
func readPID(f *os.File) int {
	b, err := io.ReadAll(io.NewSectionReader(f, 0, 32))
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return pid
}
