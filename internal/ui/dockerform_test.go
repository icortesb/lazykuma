package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/icortesb/lazykuma/internal/kuma"
)

// framesOf are the frames sent to the home instance for a call.
func framesOf(h *harness, call string) []string {
	var out []string
	for _, f := range h.fakes["home"].Frames() {
		if strings.Contains(f, `"`+call+`"`) {
			out = append(out, f)
		}
	}
	return out
}

func TestDockerFormNew(t *testing.T) {
	h := onInstance(t, twoMonitors())
	h.press("A", "3", "n")
	v := h.view()
	if h.m.screen != screenDockerHost || !strings.Contains(v, "New Docker host") || !strings.Contains(v, "/var/run/docker.sock") {
		t.Fatalf("n on the Docker tab: %v\n%s", h.m.screen, v)
	}
	// h, j, k, l and t are letters in a text field, never moves or a test.
	h.typeText("hjklt")
	h.press("tab", "l")
	if v := h.view(); !strings.Contains(v, "tcp://10.0.0.7:2375") {
		t.Fatalf("the daemon's placeholder did not follow the type:\n%s", v)
	}
	h.press("tab")
	h.typeText("tcp://10.0.0.7:2375")
	h.press("enter")
	frames := framesOf(h, "addDockerHost")
	if len(frames) != 1 {
		t.Fatalf("addDockerHost frames: %q", frames)
	}
	for _, want := range []string{`"name":"hjklt"`, `"dockerType":"tcp"`, `"dockerDaemon":"tcp://10.0.0.7:2375"`} {
		if !strings.Contains(frames[0], want) {
			t.Errorf("addDockerHost frame lacks %s: %q", want, frames[0])
		}
	}
	if !strings.HasSuffix(frames[0], ",null]") {
		t.Errorf("a new host is sent with an id: %q", frames[0])
	}
	if h.m.screen != screenServer || !strings.Contains(h.view(), "created Docker host hjklt") {
		t.Fatalf("after saving: %v\n%s", h.m.screen, h.view())
	}
	if h.fakes["home"].Called("testDockerHost") {
		t.Fatal("t typed into the name tested the host")
	}
}

func TestDockerFormEdit(t *testing.T) {
	h := onInstance(t, withServer(twoMonitors()))
	h.press("A", "3", "e")
	v := h.view()
	for _, want := range []string{"Edit Docker host nas", "nas", "‹ socket ›", "/var/run/docker.sock"} {
		if !strings.Contains(v, want) {
			t.Errorf("edit form lacks %q:\n%s", want, v)
		}
	}
	h.press("enter")
	frames := framesOf(h, "addDockerHost")
	if len(frames) != 1 || !strings.HasSuffix(frames[0], ",2]") || !strings.Contains(frames[0], `"dockerType":"socket"`) {
		t.Fatalf("addDockerHost frames: %q", frames)
	}
	if h.m.screen != screenServer || !strings.Contains(h.view(), "saved Docker host nas") {
		t.Fatalf("after saving: %v\n%s", h.m.screen, h.view())
	}
}

func TestDockerFormSendsOnce(t *testing.T) {
	h := onInstance(t, withServer(twoMonitors()))
	h.press("A", "3", "e")
	held := h.hold("enter")
	// A second enter while Kuma saves would save the host twice: a second
	// host, for a new one.
	h.press("enter")
	h.run(held)
	if frames := framesOf(h, "addDockerHost"); len(frames) != 1 {
		t.Fatalf("addDockerHost frames: %q", frames)
	}
}

func TestDockerFormTestsWithoutSaving(t *testing.T) {
	h := onInstance(t, withServer(twoMonitors()))
	h.press("A", "3", "e", "tab", "l", "tab")
	for range len("/var/run/docker.sock") {
		h.press("backspace")
	}
	h.typeText("tcp://10.0.0.7:2375")
	h.press("shift+tab")
	held := h.hold("t")
	if v := h.view(); !strings.Contains(v, "testing…") {
		t.Fatalf("no testing… while Kuma tries:\n%s", v)
	}
	// A second t while Kuma tries sends nothing more.
	next, cmd := h.m.Update(keyMsg("t"))
	h.m = next.(Model)
	h.run(cmd)
	h.run(held)
	frames := framesOf(h, "testDockerHost")
	if len(frames) != 1 || !strings.Contains(frames[0], `"dockerType":"tcp"`) || !strings.Contains(frames[0], `"dockerDaemon":"tcp://10.0.0.7:2375"`) ||
		!strings.Contains(frames[0], `"name":"nas"`) {
		t.Fatalf("testDockerHost frames: %q", frames)
	}
	v := h.view()
	if h.m.screen != screenDockerHost || !strings.Contains(v, "Docker nas: Connected Successfully. Amount of containers: 3") || strings.Contains(v, "testing…") {
		t.Fatalf("after the test: %v\n%s", h.m.screen, v)
	}
	if h.fakes["home"].Called("addDockerHost") {
		t.Fatal("the test saved the host")
	}
	// ctrl+t tests from a text row too.
	h.press("tab")
	h.send(tea.KeyMsg{Type: tea.KeyCtrlT})
	if frames := framesOf(h, "testDockerHost"); len(frames) != 2 {
		t.Fatalf("ctrl+t: %q", frames)
	}
}

func TestDockerListTest(t *testing.T) {
	h := onInstance(t, withServer(twoMonitors()))
	h.press("A", "3")
	held := h.hold("t")
	if v := h.view(); !strings.Contains(v, "testing…") {
		t.Fatalf("no testing… on the row:\n%s", v)
	}
	// An answer for another test, from before, is not this one's.
	h.send(dockerTested{instance: "lab", name: "nas", msg: "Connected Successfully. Amount of containers: 1"})
	if v := h.view(); !strings.Contains(v, "testing…") || strings.Contains(v, "Amount of containers: 1") {
		t.Fatalf("a stale answer was taken:\n%s", v)
	}
	h.run(held)
	v := h.view()
	if !strings.Contains(v, "Docker nas: connect ENOENT /var/run/docker.sock") || strings.Contains(v, "testing…") {
		t.Fatalf("after the test:\n%s", v)
	}
	if !h.fakes["home"].Sent(`["testDockerHost",{"dockerDaemon":"/var/run/docker.sock","dockerType":"socket","name":"nas"}`) {
		t.Fatalf("frames: %v", h.fakes["home"].Frames())
	}
}

func TestDockerDelete(t *testing.T) {
	h := onInstance(t, withServer(twoMonitors()))
	h.press("A", "3", "d")
	v := h.view()
	if !strings.Contains(v, `Delete the Docker host "nas"?`) || !strings.Contains(v, "docker monitors on it lose their host") {
		t.Fatalf("no question:\n%s", v)
	}
	h.press("y")
	if !h.fakes["home"].Sent(`["deleteDockerHost",2]`) || h.m.screen != screenServer || !strings.Contains(h.view(), "deleted Docker host nas") {
		t.Fatalf("delete: %v %v\n%s", h.m.screen, h.fakes["home"].Frames(), h.view())
	}
}

func TestDockerFormRejects(t *testing.T) {
	form := func(name, daemon string) dockerForm {
		f := newDockerForm(kuma.DockerHost{})
		f.name.SetValue(name)
		f.daemon.SetValue(daemon)
		return f
	}
	if _, err := form(" ", "/var/run/docker.sock").Values(); err == nil || !strings.Contains(err.Error(), "the name is empty") {
		t.Errorf("empty name: %v", err)
	}
	if _, err := form("nas", " ").Values(); err == nil || !strings.Contains(err.Error(), "the daemon is empty") {
		t.Errorf("empty daemon: %v", err)
	}
	d, err := form(" nas ", " /var/run/docker.sock ").Values()
	if err != nil || d != (kuma.DockerHost{Name: "nas", Type: "socket", Daemon: "/var/run/docker.sock"}) {
		t.Errorf("good values: %+v %v", d, err)
	}

	h := onInstance(t, twoMonitors())
	h.press("A", "3", "n", "enter")
	if h.m.screen != screenDockerHost || !strings.Contains(h.view(), "the name is empty") || h.fakes["home"].Called("addDockerHost") {
		t.Fatalf("empty form saved: %v\n%s", h.m.screen, h.view())
	}
	h.press("esc")
	if h.m.screen != screenServer {
		t.Fatalf("esc: %v", h.m.screen)
	}
}

func TestDockerFormType(t *testing.T) {
	f := newDockerForm(kuma.DockerHost{ID: 2, Name: "nas", Type: "tcp", Daemon: "tcp://10.0.0.7:2375"})
	f, _, _ = f.Update(keyMsg("tab"))
	for _, k := range []string{"right", "h", "left"} {
		f, _, _ = f.Update(keyMsg(k))
	}
	if d, _ := f.Values(); d.Type != "socket" || d.ID != 2 {
		t.Fatalf("three steps from tcp: %+v", d)
	}
	// On a text row the arrows move the text cursor, never the type: right
	// on the daemon row leaves it socket.
	f, _, _ = f.Update(keyMsg("tab"))
	f, _, _ = f.Update(keyMsg("right"))
	if d, _ := f.Values(); d.Type != "socket" {
		t.Fatalf("right on the daemon row: %+v", d)
	}
	if nf := newDockerForm(kuma.DockerHost{}); dockerTypes[nf.typ] != "socket" {
		t.Fatalf("new form starts on %q", dockerTypes[nf.typ])
	}
}

func TestDockerEmptyTab(t *testing.T) {
	h := onInstance(t, twoMonitors())
	h.send(tea.WindowSizeMsg{Width: 60, Height: 24})
	h.press("A", "3")
	// The tab's own lines, before the app cuts any to the terminal.
	v := ansi.Strip(h.m.srv.View("home", h.m.current().st, 60, 22))
	assertFits(t, v, 60)
	if !strings.Contains(v, "no Docker hosts") || !strings.Contains(v, "docker monitors take a host's id in the field editor (r)") {
		t.Fatalf("empty tab:\n%s", v)
	}
}

func TestDockerSaveClosesOnlyItsForm(t *testing.T) {
	h := onInstance(t, withServer(twoMonitors()))
	h.press("A", "3", "e")
	held := h.hold("enter")
	h.press("esc", "1", "n")
	h.run(held)
	if h.m.screen != screenAPIKey {
		t.Fatalf("the Docker host's answer closed another form: %v", h.m.screen)
	}
}

func TestDockerFormFitsNarrowTerminal(t *testing.T) {
	h := onInstance(t, withServer(twoMonitors()))
	h.send(tea.WindowSizeMsg{Width: 60, Height: 24})
	h.press("A", "3", "e")
	h.typeText(strings.Repeat("x", 80))
	h.press("tab", "tab")
	h.typeText(strings.Repeat("y", 80))
	h.m.dform.err = "the daemon is empty"
	h.m.dform.testing = true
	assertFits(t, ansi.Strip(h.m.dform.View()), 60)
}
