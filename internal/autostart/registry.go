package autostart

// Registry is the values under the user's Run key,
// HKCU\Software\Microsoft\Windows\CurrentVersion\Run.
type Registry interface {
	// Get reports ok false, not an error, when the value does not exist.
	Get(name string) (value string, ok bool, err error)
	Set(name, value string) error
	// Delete succeeds when the value does not exist.
	Delete(name string) error
}

// systemRegistry is the real Run key on Windows, set by registry_windows.go,
// and nil elsewhere.
var systemRegistry Registry
