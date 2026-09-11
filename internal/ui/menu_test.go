package ui

import "testing"

func TestVersionLabel(t *testing.T) {
	for in, want := range map[string]string{"0.1.0": "v0.1.0", "v0.1.0-3-gabc": "v0.1.0-3-gabc", "dev": "dev", "abc123": "abc123"} {
		if got := versionLabel(in); got != want {
			t.Errorf("versionLabel(%q) = %q, want %q", in, got, want)
		}
	}
}
