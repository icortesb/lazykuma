//go:build !windows

package autostart

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

// startDetached starts argv in a session of its own, so closing the terminal
// lazykuma runs in does not hang it up, and in dir, the home directory, so
// it does not keep the directory lazykuma was started in busy. Its standard
// streams are the null device. It is waited for on a goroutine rather than
// released, so that a TUI left open does not collect zombies.
func startDetached(dir string, argv []string) error {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}

// stopProcess asks pid to end, forces it after stopWait, and returns once
// the lock at lockPath is free.
func stopProcess(pid int, lockPath string) error {
	if pid <= 0 {
		// kill(0) and kill(-1) reach whole groups of processes.
		return fmt.Errorf("refusing to signal pid %d", pid)
	}
	for _, sig := range []unix.Signal{unix.SIGTERM, unix.SIGKILL} {
		if err := unix.Kill(pid, sig); err != nil && !errors.Is(err, unix.ESRCH) {
			return err
		}
		if waitFree(lockPath, stopWait) {
			return nil
		}
	}
	return fmt.Errorf("pid %d still holds %s", pid, lockPath)
}
