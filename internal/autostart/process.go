package autostart

import (
	"time"

	"github.com/icortesb/lazykuma/internal/watchlock"
)

// stopWait is how long a stopped watch gets to let go of the lock, once
// asked to end and again once forced to.
var stopWait = 5 * time.Second

// waitFree waits up to d for nobody to hold the lock at path.
func waitFree(path string, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for {
		if _, running, err := watchlock.Holder(path); err == nil && !running {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}
