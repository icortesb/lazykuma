package watchlock

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func lockPath(t *testing.T) string {
	return filepath.Join(t.TempDir(), "state", "watch.lock")
}

func TestASecondAcquireIsRefusedWithTheHoldersPID(t *testing.T) {
	path := lockPath(t)
	l, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()

	_, err = Acquire(path)
	var run ErrRunning
	if !errors.As(err, &run) || run.PID != os.Getpid() {
		t.Fatalf("second Acquire = %v, want ErrRunning{%d}", err, os.Getpid())
	}
	pid, running, err := Holder(path)
	if err != nil || !running || pid != os.Getpid() {
		t.Fatalf("Holder = %d, %v, %v", pid, running, err)
	}
}

func TestReleaseFreesTheLock(t *testing.T) {
	path := lockPath(t)
	l, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lock file left behind: %v", err)
	}
	if _, running, err := Holder(path); err != nil || running {
		t.Fatalf("Holder after Release = %v, %v", running, err)
	}
	l, err = Acquire(path)
	if err != nil {
		t.Fatalf("Acquire after Release: %v", err)
	}
	l.Release()
}

func TestHolderOfAnUnlockedFileIsNotRunning(t *testing.T) {
	// A file left by a crashed watch holds a PID but no lock.
	path := lockPath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("4242\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if pid, running, err := Holder(path); err != nil || running || pid != 0 {
		t.Fatalf("Holder = %d, %v, %v", pid, running, err)
	}
}

func TestHolderOfAMissingFileIsNotRunning(t *testing.T) {
	if pid, running, err := Holder(lockPath(t)); err != nil || running || pid != 0 {
		t.Fatalf("Holder = %d, %v, %v", pid, running, err)
	}
}

// TestHelperHold is the child of TestALockDiesWithItsProcess: it takes the
// lock, says so, and holds it until its stdin closes.
func TestHelperHold(t *testing.T) {
	path := os.Getenv("WATCHLOCK_HELPER_PATH")
	if path == "" {
		t.Skip("only runs as a child of TestALockDiesWithItsProcess")
	}
	l, err := Acquire(path)
	if err != nil {
		os.Stdout.WriteString("error: " + err.Error() + "\n")
		os.Exit(1)
	}
	os.Stdout.WriteString("held\n")
	bufio.NewReader(os.Stdin).ReadByte()
	l.Release()
}

func TestALockDiesWithItsProcess(t *testing.T) {
	path := lockPath(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperHold$")
	cmd.Env = append(os.Environ(), "WATCHLOCK_HELPER_PATH="+path)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := false
	defer func() {
		if !exited {
			cmd.Process.Kill()
			cmd.Wait()
		}
	}()

	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || line != "held\n" {
		t.Fatalf("child said %q, %v", line, err)
	}

	_, err = Acquire(path)
	var run ErrRunning
	if !errors.As(err, &run) || run.PID != cmd.Process.Pid {
		t.Fatalf("Acquire = %v, want ErrRunning{%d}", err, cmd.Process.Pid)
	}
	if pid, running, _ := Holder(path); !running || pid != cmd.Process.Pid {
		t.Fatalf("Holder = %d, %v", pid, running)
	}

	stdin.Close()
	cmd.Wait()
	exited = true

	deadline := time.Now().Add(5 * time.Second)
	for {
		l, err := Acquire(path)
		if err == nil {
			l.Release()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("lock not freed after the child exited: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A process that opened the file just before its holder released it locks an
// unlinked file. That lock must not count: a second Acquire must still be
// refused once the first has succeeded on the file at path.
func TestALockOnAnUnlinkedFileDoesNotCount(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows cannot remove a file another handle has open")
	}
	path := lockPath(t)
	old, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	once := false
	afterOpen = func() {
		if !once {
			once = true
			old.Release()
		}
	}
	defer func() { afterOpen = func() {} }()

	l, err := Acquire(path)
	afterOpen = func() {}
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	if _, err := Acquire(path); !errors.As(err, new(ErrRunning)) {
		t.Fatalf("second Acquire = %v, want ErrRunning", err)
	}
	if pid, running, err := Holder(path); err != nil || !running || pid != os.Getpid() {
		t.Fatalf("Holder = %d, %v, %v", pid, running, err)
	}
}

func TestAcquireWaitOutlastsABriefHolder(t *testing.T) {
	path := lockPath(t)
	l, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(200 * time.Millisecond)
		l.Release()
	}()
	l2, err := AcquireWait(path, time.Second)
	if err != nil {
		t.Fatalf("AcquireWait = %v", err)
	}
	l2.Release()
}

func TestAcquireWaitGivesUpOnAHolderThatStays(t *testing.T) {
	path := lockPath(t)
	l, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	start := time.Now()
	_, err = AcquireWait(path, 300*time.Millisecond)
	var run ErrRunning
	if !errors.As(err, &run) || run.PID != os.Getpid() {
		t.Fatalf("AcquireWait = %v, want ErrRunning{%d}", err, os.Getpid())
	}
	if d := time.Since(start); d < 300*time.Millisecond {
		t.Fatalf("gave up after %v", d)
	}
}
