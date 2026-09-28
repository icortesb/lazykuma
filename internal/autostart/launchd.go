package autostart

import (
	"encoding/xml"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

const launchdLabel = "io.github.icortesb.lazykuma.watch"

// LaunchdPlist is the launch agent that runs the watch, with env set for it.
// launchd writes its output to logPath itself, so it does not pass --log.
// KeepAlive restarts it when it fails, a watch that found another one running
// included: that one takes over when the other stops.
func LaunchdPlist(exe, logPath string, env map[string]string) string {
	var envDict strings.Builder
	if len(env) > 0 {
		envDict.WriteString("\t<key>EnvironmentVariables</key>\n\t<dict>\n")
		for _, k := range slices.Sorted(maps.Keys(env)) {
			envDict.WriteString("\t\t<key>" + xmlEscape(k) + "</key>\n\t\t<string>" + xmlEscape(env[k]) + "</string>\n")
		}
		envDict.WriteString("\t</dict>\n")
	}
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + launchdLabel + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + xmlEscape(exe) + `</string>
		<string>watch</string>
	</array>
` + envDict.String() + `	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>ThrottleInterval</key>
	<integer>30</integer>
	<key>StandardOutPath</key>
	<string>` + xmlEscape(logPath) + `</string>
	<key>StandardErrorPath</key>
	<string>` + xmlEscape(logPath) + `</string>
	<key>ProcessType</key>
	<string>Background</string>
</dict>
</plist>
`
}

func xmlEscape(s string) string {
	var b strings.Builder
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

// parsePlist returns the first string of ProgramArguments, or "".
func parsePlist(plist string) string {
	d := xml.NewDecoder(strings.NewReader(plist))
	d.Strict = false
	var key, text string
	inArgs := false
	for {
		tok, err := d.Token()
		if err != nil {
			return ""
		}
		switch t := tok.(type) {
		case xml.StartElement:
			text = ""
			if t.Name.Local == "array" && key == "ProgramArguments" {
				inArgs = true
			}
		case xml.CharData:
			text += string(t)
		case xml.EndElement:
			switch {
			case t.Name.Local == "key":
				key = text
			case t.Name.Local == "string" && inArgs:
				return text
			case t.Name.Local == "array":
				inArgs = false
			}
		}
	}
}

type launchd struct{ m *Manager }

func (m *Manager) plistPath() string {
	return filepath.Join(m.Home, "Library", "LaunchAgents", launchdLabel+".plist")
}

func (m *Manager) launchdLog() string {
	return filepath.Join(m.Home, "Library", "Logs", "lazykuma", "watch.log")
}

func (l launchd) domain() string { return "gui/" + strconv.Itoa(l.m.UID) }

func (launchd) kind() string { return KindLaunchd }

func (l launchd) registered() (bool, string, error) {
	plist, ok, err := readFile(l.m.plistPath())
	if err != nil || !ok {
		return false, "", err
	}
	return true, parsePlist(plist), nil
}

// enable boots the agent out first: bootstrap refuses a label that is
// already loaded, and booting out is also what restarts the watch.
func (l launchd) enable() error {
	logPath := l.m.launchdLog()
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return err
	}
	if err := writeFile(l.m.plistPath(), LaunchdPlist(l.m.Exe, logPath, l.m.Env)); err != nil {
		return err
	}
	_ = l.m.run("launchctl", "bootout", l.domain()+"/"+launchdLabel)
	// bootout returns before launchd has torn the job down, and until then
	// bootstrap fails with 5 (input/output error) or 37 (operation already
	// in progress).
	deadline := time.Now().Add(bootstrapWait)
	for {
		err := l.m.run("launchctl", "bootstrap", l.domain(), l.m.plistPath())
		if c := exitCode(err); err == nil || (c != 5 && c != 37) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(bootstrapRetry)
	}
}

// bootstrapWait is how long enable retries bootstrap while the old job is
// torn down, bootstrapRetry how long it waits between tries.
var bootstrapWait, bootstrapRetry = 5 * time.Second, 250 * time.Millisecond

func (l launchd) disable() error {
	if err := l.m.run("launchctl", "bootout", l.domain()+"/"+launchdLabel); err != nil && !notLoaded(err) {
		return err
	}
	return removeFile(l.m.plistPath())
}

// notLoaded reports whether bootout failed only because the agent was not
// loaded: exit 3 (no such process) or 113 (could not find service).
func notLoaded(err error) bool {
	if c := exitCode(err); c == 3 || c == 113 {
		return true
	}
	var ce *cmdError
	return errors.As(err, &ce) && strings.Contains(ce.out, "No such process")
}
