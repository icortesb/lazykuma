package autostart

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// startDetached starts argv with no console window and out of this
// console's process group, so closing the terminal lazykuma runs in does
// not end it.
func startDetached(argv []string) error {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW,
		HideWindow:    true,
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}

// stopProcess ends pid and returns once the lock at lockPath is free.
// Windows has no polite signal for a process without a window.
func stopProcess(pid int, lockPath string) error {
	if pid <= 0 {
		return fmt.Errorf("refusing to stop pid %d", pid)
	}
	p, err := os.FindProcess(pid)
	if err == nil {
		err = p.Kill()
		p.Release()
	}
	if waitFree(lockPath, stopWait) {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("pid %d still holds %s", pid, lockPath)
}
