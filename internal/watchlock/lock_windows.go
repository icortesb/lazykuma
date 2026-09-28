package watchlock

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// lockOffset is far past the PID: Windows byte-range locks also block reads
// of the locked range, and other processes must still be able to read the PID.
const lockOffset = 1 << 20

// tryLock reports false, not an error, when another handle holds the lock.
func tryLock(f *os.File) (bool, error) {
	ol := &windows.Overlapped{Offset: lockOffset}
	err := windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, windows.ERROR_LOCK_VIOLATION):
		return false, nil
	default:
		return false, err
	}
}

func unlock(f *os.File) error {
	ol := &windows.Overlapped{Offset: lockOffset}
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, ol)
}
