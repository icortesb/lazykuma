//go:build !windows

package watchlock

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// tryLock reports false, not an error, when another open file description
// holds the lock. flock covers the whole file, which is fine here: it does
// not stop other processes from reading the PID.
func tryLock(f *os.File) (bool, error) {
	for {
		err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		switch {
		case err == nil:
			return true, nil
		case errors.Is(err, unix.EINTR):
			continue
		case errors.Is(err, unix.EWOULDBLOCK):
			return false, nil
		default:
			return false, err
		}
	}
}

func unlock(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
