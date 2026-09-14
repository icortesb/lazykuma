package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestCommandLine(t *testing.T) {
	tests := []struct {
		args   []string
		code   int
		stdout string
		stderr string
	}{
		{[]string{"--version"}, 0, "dev", ""},
		{[]string{"version"}, 0, "dev", ""},
		{[]string{"help"}, 0, "lazykuma status [--json]", ""},
		{[]string{"frobnicate"}, 2, "", `unknown command "frobnicate"`},
		{[]string{"status", "--nope"}, 2, "", "flag provided but not defined"},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			var out, errOut bytes.Buffer
			if code := run(tt.args, &out, &errOut); code != tt.code {
				t.Fatalf("exit %d, want %d (stderr %q)", code, tt.code, errOut.String())
			}
			if !strings.Contains(out.String(), tt.stdout) {
				t.Errorf("stdout = %q, want it to contain %q", out.String(), tt.stdout)
			}
			if !strings.Contains(errOut.String(), tt.stderr) {
				t.Errorf("stderr = %q, want it to contain %q", errOut.String(), tt.stderr)
			}
		})
	}
}
