package autostart

import (
	"os"
	"strconv"
	"testing"
)

// TestRunKey writes the real Run key, under a name of its own that it always
// removes. It runs on the CI Windows runner.
func TestRunKey(t *testing.T) {
	r := hkcuRun{}
	name := "lazykuma-watch-test-" + strconv.Itoa(os.Getpid())
	t.Cleanup(func() { r.Delete(name) })

	if _, ok, err := r.Get(name); err != nil || ok {
		t.Fatalf("Get before Set = %v, %v", ok, err)
	}
	value := RunValue(`C:\Program Files\lazy kuma\lazykuma.exe`)
	if err := r.Set(name, value); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := r.Get(name); err != nil || !ok || got != value {
		t.Fatalf("Get = %q, %v, %v", got, ok, err)
	}
	if err := r.Delete(name); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := r.Get(name); err != nil || ok {
		t.Fatalf("Get after Delete = %v, %v", ok, err)
	}
	if err := r.Delete(name); err != nil {
		t.Fatalf("Delete of a missing value = %v", err)
	}
}
