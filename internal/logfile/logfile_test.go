package logfile

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRotatesAtTheCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "watch.log")
	w, err := Open(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("123456"))
	w.Write([]byte("abcd")) // exactly at the cap: no rotation
	if got := read(t, path); got != "123456abcd" {
		t.Fatalf("log = %q", got)
	}
	w.Write([]byte("Z"))
	if got := read(t, path); got != "Z" {
		t.Fatalf("log = %q, want the fresh file", got)
	}
	if got := read(t, path+".1"); got != "123456abcd" {
		t.Fatalf(".1 = %q", got)
	}
	// Close before the test's temp dir is removed: Windows cannot delete open files.
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOnlyOneRotationIsKept(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watch.log")
	w, err := Open(path, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	for _, s := range []string{"aaaa", "bbbb", "cccc"} {
		w.Write([]byte(s))
	}
	if got := read(t, path); got != "cccc" {
		t.Fatalf("log = %q", got)
	}
	if got := read(t, path+".1"); got != "bbbb" {
		t.Fatalf(".1 = %q, want only the latest rotation", got)
	}
	if _, err := os.Stat(path + ".2"); err == nil {
		t.Fatal("a second rotation file exists")
	}
}

func TestAppendsToAnExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watch.log")
	os.WriteFile(path, []byte("old\n"), 0o644)
	w, err := Open(path, 100)
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("new\n"))
	w.Close()
	if got := read(t, path); got != "old\nnew\n" {
		t.Fatalf("log = %q", got)
	}
}

func TestConcurrentWritersKeepWholeLinesAndTheCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watch.log")
	w, err := Open(path, 64)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				w.Write([]byte("line\n"))
			}
		}()
	}
	wg.Wait()
	w.Close()
	// Every rotation keeps whole lines, and neither file passes the cap.
	for _, p := range []string{path, path + ".1"} {
		got := read(t, p)
		if len(got) > 64 || strings.Trim(got, "line\n") != "" || len(got)%5 != 0 {
			t.Fatalf("%s = %q", filepath.Base(p), got)
		}
	}
}

func TestAFailedRotationDropsNothing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "watch.log")
	// A non-empty directory at path.1 makes the rename fail on every OS.
	if err := os.MkdirAll(filepath.Join(path+".1", "keep"), 0o755); err != nil {
		t.Fatal(err)
	}
	w, err := Open(path, 4)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"aaaa", "bbbb", "cccc"} {
		if n, err := w.Write([]byte(s)); err != nil || n != 4 {
			t.Fatalf("Write(%q) = %d, %v", s, n, err)
		}
	}
	w.Close()
	if got := read(t, path); got != "aaaabbbbcccc" {
		t.Fatalf("log = %q, want every write kept", got)
	}
}

func TestAFailedReopenIsRetriedOnTheNextWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watch.log")
	w, err := Open(path, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	w.Write([]byte("aaaa"))

	defer func(orig func(string, int, os.FileMode) (*os.File, error)) { openFile = orig }(openFile)
	openFile = func(string, int, os.FileMode) (*os.File, error) { return nil, os.ErrPermission }
	if _, err := w.Write([]byte("bbbb")); err == nil {
		t.Fatal("Write succeeded with the reopen failing")
	}

	openFile = os.OpenFile
	if n, err := w.Write([]byte("cccc")); err != nil || n != 4 {
		t.Fatalf("Write after the failure = %d, %v, want the file reopened", n, err)
	}
	if got := read(t, path); got != "cccc" {
		t.Fatalf("log = %q", got)
	}
}

func TestTheLogIsReadableByItsOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no permission bits")
	}
	path := filepath.Join(t.TempDir(), "watch.log")
	w, err := Open(path, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := st.Mode().Perm(); mode != 0o600 {
		t.Fatalf("mode = %o, want 600", mode)
	}
}
