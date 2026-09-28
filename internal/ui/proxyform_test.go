package ui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/icortesb/lazykuma/internal/kuma"
	"github.com/icortesb/lazykuma/internal/state"
)

// secret is a password no view may ever carry.
const secret = "s3cret-pass"

// withProxy is an instance whose one proxy is p.
func withProxy(p kuma.Proxy) state.Instance {
	return state.Apply(twoMonitors(), kuma.ProxyList{Proxies: []kuma.Proxy{p}}, tBase)
}

func proxyFrame(h *harness) string {
	var frame string
	for _, f := range h.fakes["home"].Frames() {
		if strings.Contains(f, `"addProxy"`) {
			frame = f
		}
	}
	return frame
}

func TestProxyFormNew(t *testing.T) {
	h := onInstance(t, twoMonitors())
	h.press("A", "2", "n")
	if h.m.screen != screenProxy || !strings.Contains(h.view(), "New proxy") {
		t.Fatalf("n on the proxies tab: %v\n%s", h.m.screen, h.view())
	}
	// h, j, k and l are letters in a text field, never moves.
	h.typeText("hjkl.home.lan")
	h.press("tab")
	h.typeText("3128")
	h.press("tab", " ", "tab")
	h.typeText("kuma")
	h.press("tab")
	h.typeText(secret)
	if v := h.view(); strings.Contains(v, secret) || !strings.Contains(v, "hjkl.home.lan") {
		t.Fatalf("form view:\n%s", v)
	}
	h.press("enter")
	frame := proxyFrame(h)
	for _, want := range []string{`"auth":true`, `"username":"kuma"`, `"password":"` + secret + `"`, `"protocol":"http"`,
		`"host":"hjkl.home.lan"`, `"port":3128`, `"applyExisting":false`} {
		if !strings.Contains(frame, want) {
			t.Errorf("addProxy frame lacks %s: %q", want, frame)
		}
	}
	if !strings.HasSuffix(frame, "null]") {
		t.Errorf("a new proxy is sent with an id: %q", frame)
	}
	if v := h.view(); h.m.screen != screenServer || !strings.Contains(v, "created proxy hjkl.home.lan:3128") || strings.Contains(v, secret) {
		t.Fatalf("after saving: %v\n%s", h.m.screen, v)
	}
}

func TestProxyFormEditKeepsThePassword(t *testing.T) {
	h := onInstance(t, withProxy(kuma.Proxy{ID: 1, Protocol: "socks5", Host: "proxy.home.lan", Port: 1080, Auth: true, Username: "kuma", Password: "p"})) // ggignore: a test's fake password
	h.press("A", "2", "e")
	v := h.view()
	for _, want := range []string{"Edit proxy socks5://proxy.home.lan:1080", "socks5", "proxy.home.lan", "1080", "kuma", "unchanged"} {
		if !strings.Contains(v, want) {
			t.Errorf("edit form lacks %q:\n%s", want, v)
		}
	}
	h.press("enter")
	frame := proxyFrame(h)
	if !strings.Contains(frame, `"password":"p"`) || !strings.Contains(frame, `"protocol":"socks5"`) || !strings.HasSuffix(frame, ",1]") {
		t.Fatalf("addProxy frame = %q", frame)
	}
	if h.m.screen != screenServer || !strings.Contains(h.view(), "saved proxy proxy.home.lan:1080") {
		t.Fatalf("after saving: %v\n%s", h.m.screen, h.view())
	}
}

func TestProxyFormEditSendsANewPassword(t *testing.T) {
	h := onInstance(t, withProxy(kuma.Proxy{ID: 1, Protocol: "http", Host: "proxy.home.lan", Port: 3128, Auth: true, Username: "kuma", Password: "old-pass"})) // ggignore: a test's fake password
	h.press("A", "2", "e")
	// Host, port, auth, username, then the password.
	h.press("tab", "tab", "tab", "tab")
	h.typeText(secret)
	if v := h.view(); strings.Contains(v, secret) || strings.Contains(v, "old-pass") {
		t.Fatalf("the form shows a password:\n%s", v)
	}
	h.press("enter")
	frame := proxyFrame(h)
	if !strings.Contains(frame, `"password":"`+secret+`"`) || strings.Contains(frame, "old-pass") || !strings.HasSuffix(frame, ",1]") {
		t.Fatalf("addProxy frame = %q", frame)
	}
	if v := h.view(); h.m.screen != screenServer || !strings.Contains(v, "saved proxy proxy.home.lan:3128") || strings.Contains(v, secret) || strings.Contains(h.m.flash, secret) {
		t.Fatalf("after saving: %v\n%s", h.m.screen, v)
	}
}

func TestProxyFormSavesOnce(t *testing.T) {
	h := onInstance(t, twoMonitors())
	h.press("A", "2", "n")
	h.typeText("proxy.home.lan")
	h.press("tab")
	h.typeText("3128")
	held := h.hold("enter")
	if !strings.Contains(h.view(), "saving…") {
		t.Errorf("no word while saving:\n%s", h.view())
	}
	h.press("enter") // impatient: must not make a second proxy
	h.run(held)
	if n := len(framesOf(h, "addProxy")); n != 1 || h.m.screen != screenServer {
		t.Fatalf("addProxy sent %d times, screen %v", n, h.m.screen)
	}

	// Through the question before setting it on every monitor, too; and a
	// failed save frees the form for another try.
	h.press("n")
	h.typeText("proxy.home.lan")
	h.press("tab")
	h.typeText("3128")
	h.press("tab", "tab", "tab", " ", "enter")
	h.hold("y") // Kuma's answer is kept back
	if !h.m.pform.pending || h.m.screen != screenProxy {
		t.Fatalf("y: %v pending %v", h.m.screen, h.m.pform.pending)
	}
	h.press("enter")
	if n := len(framesOf(h, "addProxy")); n != 1 {
		t.Fatalf("enter while saving sent addProxy: %d frames", n)
	}
	h.send(actionDone{name: "home", action: "created", mon: "proxy proxy.home.lan:3128", err: errors.New("Kuma said no"), from: screenProxy})
	if h.m.pform.pending || h.m.screen != screenProxy || strings.Contains(h.view(), "saving…") {
		t.Fatalf("after a failed save: %v pending %v\n%s", h.m.screen, h.m.pform.pending, h.view())
	}
	h.press("enter", "y")
	if n := len(framesOf(h, "addProxy")); n != 2 {
		t.Fatalf("the retry was not sent: %d frames", n)
	}
}

func TestProxyPasswordNeverShown(t *testing.T) {
	p := kuma.Proxy{ID: 4, Protocol: "http", Host: "proxy.home.lan", Port: 3128, Auth: true, Username: "kuma", Password: secret, Default: true}
	f := newProxyForm(p)
	if v := ansi.Strip(f.View()); strings.Contains(v, secret) {
		t.Fatalf("edit form shows the password:\n%s", v)
	}
	// Typed again, and with an error on screen, it stays hidden.
	f.pass.SetValue(secret)
	f.host.SetValue("")
	if _, _, err := f.Values(); err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("empty host: %v", err)
	}
	f.err = "the host is empty"
	if v := ansi.Strip(f.View()); strings.Contains(v, secret) {
		t.Fatalf("form shows the typed password:\n%s", v)
	}

	h := onInstance(t, withProxy(p))
	h.press("A", "2")
	if v := h.view(); strings.Contains(v, secret) || !strings.Contains(v, "http://proxy.home.lan:3128") {
		t.Fatalf("list:\n%s", v)
	}
	h.press("e")
	if v := h.view(); strings.Contains(v, secret) {
		t.Fatalf("form on screen:\n%s", v)
	}
	h.press("esc", "d")
	if v := h.view(); strings.Contains(v, secret) {
		t.Fatalf("delete question:\n%s", v)
	}
}

func TestProxyFormRejects(t *testing.T) {
	form := func(host, port string) proxyForm {
		f := newProxyForm(kuma.Proxy{})
		f.host.SetValue(host)
		f.port.SetValue(port)
		return f
	}
	if _, _, err := form("", "3128").Values(); err == nil || !strings.Contains(err.Error(), "the host is empty") {
		t.Errorf("empty host: %v", err)
	}
	for _, port := range []string{"", "0", "65536", "80a", "-1"} {
		if _, _, err := form("proxy.home.lan", port).Values(); err == nil || !strings.Contains(err.Error(), "1 to 65535") {
			t.Errorf("port %q: %v", port, err)
		}
	}
	p, apply, err := form(" proxy.home.lan ", "65535").Values()
	if err != nil || p.Host != "proxy.home.lan" || p.Port != 65535 || p.Protocol != "http" || p.ID != 0 || p.Auth || apply {
		t.Errorf("good values: %+v %v %v", p, apply, err)
	}

	// Auth turned on for a proxy that had none needs a password, and a
	// username.
	f := newProxyForm(kuma.Proxy{ID: 3, Protocol: "https", Host: "proxy.home.lan", Port: 443})
	f.auth = true
	f.user.SetValue("kuma")
	if _, _, err := f.Values(); err == nil || !strings.Contains(err.Error(), "password") {
		t.Errorf("auth on without a password: %v", err)
	}
	f.user.SetValue("")
	f.pass.SetValue("x")
	if _, _, err := f.Values(); err == nil || !strings.Contains(err.Error(), "username") {
		t.Errorf("auth on without a username: %v", err)
	}

	// Auth turned off sends no credentials, whatever the fields hold.
	f = newProxyForm(kuma.Proxy{ID: 3, Protocol: "https", Host: "proxy.home.lan", Port: 443, Auth: true, Username: "kuma", Password: "p"}) // ggignore: a test's fake password
	f.auth = false
	if p, _, err := f.Values(); err != nil || p.Auth || p.Password != "" || p.Username != "" || p.ID != 3 {
		t.Errorf("auth off: %+v %v", p, err)
	}
}

func TestProxyFormProtocolAndToggles(t *testing.T) {
	f := newProxyForm(kuma.Proxy{ID: 2, Protocol: "socks5", Host: "proxy.home.lan", Port: 1080, Default: true})
	f, _, _ = f.Update(keyMsg("shift+tab")) // from the host up to the protocol
	for _, k := range []string{"right", "l"} {
		f, _, _ = f.Update(keyMsg(k))
	}
	if p, _, _ := f.Values(); p.Protocol != "socks4" {
		t.Fatalf("two right of socks5: %q", p.Protocol)
	}
	f, _, _ = f.Update(keyMsg("right")) // wraps
	if p, _, _ := f.Values(); p.Protocol != "http" {
		t.Fatalf("right of socks4: %q", p.Protocol)
	}
	f, _, _ = f.Update(keyMsg("h"))
	if p, _, _ := f.Values(); p.Protocol != "socks4" {
		t.Fatalf("left of http: %q", p.Protocol)
	}
	// Down to the default toggle: host, port, auth, then default, since the
	// credentials hide while auth is off.
	for range 4 {
		f, _, _ = f.Update(keyMsg("tab"))
	}
	f, _, _ = f.Update(keyMsg(" "))
	f, _, _ = f.Update(keyMsg("tab"))
	f, _, _ = f.Update(keyMsg(" "))
	p, apply, err := f.Values()
	if err != nil || p.Default || !apply {
		t.Fatalf("toggles: %+v %v %v", p, apply, err)
	}
	if v := ansi.Strip(f.View()); strings.Contains(v, "username") || !strings.Contains(v, "[x] use it on every existing monitor now") {
		t.Fatalf("view:\n%s", v)
	}
	// A new proxy starts on http, with the apply toggle off.
	if nf := newProxyForm(kuma.Proxy{}); kuma.ProxyProtocols[nf.protocol] != "http" || nf.apply {
		t.Fatalf("new form: %+v", nf)
	}
}

func TestProxyApplyToEveryMonitorAsksFirst(t *testing.T) {
	h := onInstance(t, twoMonitors())
	h.press("A", "2", "n")
	h.typeText("proxy.home.lan")
	h.press("tab")
	h.typeText("3128")
	h.press("tab", "tab", "tab", " ", "enter")
	v := h.view()
	if !strings.Contains(v, "Set this proxy on every monitor?") || !strings.Contains(v, "every monitor will check through it") {
		t.Fatalf("no question:\n%s", v)
	}
	h.press("n")
	if h.fakes["home"].Called("addProxy") || h.m.screen != screenProxy {
		t.Fatalf("n: %v %v", h.m.screen, h.fakes["home"].Frames())
	}
	h.press("enter", "y")
	if frame := proxyFrame(h); !strings.Contains(frame, `"applyExisting":true`) || h.m.screen != screenServer {
		t.Fatalf("y: %v %q", h.m.screen, frame)
	}
}

func TestProxyFormCancels(t *testing.T) {
	h := onInstance(t, twoMonitors())
	h.press("A", "2", "n", "esc")
	if h.m.screen != screenServer || h.fakes["home"].Called("addProxy") {
		t.Fatalf("esc: %v", h.m.screen)
	}
}

func TestProxyDelete(t *testing.T) {
	h := onInstance(t, withServer(twoMonitors()))
	h.press("A", "2", "d")
	v := h.view()
	if !strings.Contains(v, "Delete the proxy http://proxy.home.lan:3128?") || !strings.Contains(v, "monitors using it go without a proxy") {
		t.Fatalf("no question:\n%s", v)
	}
	h.press("y")
	if !h.fakes["home"].Sent(`["deleteProxy",4]`) || h.m.screen != screenServer || !strings.Contains(h.view(), "deleted proxy proxy.home.lan:3128") {
		t.Fatalf("delete: %v %v\n%s", h.m.screen, h.fakes["home"].Frames(), h.view())
	}
}

func TestProxySaveClosesOnlyItsForm(t *testing.T) {
	h := onInstance(t, withServer(twoMonitors()))
	h.press("A", "2", "e")
	held := h.hold("enter")
	// The user leaves and opens the API key form before Kuma answers.
	h.press("esc", "1", "n")
	h.run(held)
	if h.m.screen != screenAPIKey {
		t.Fatalf("the proxy's answer closed another form: %v", h.m.screen)
	}
}

func TestProxyFormFitsNarrowTerminal(t *testing.T) {
	h := onInstance(t, withServer(twoMonitors()))
	h.send(tea.WindowSizeMsg{Width: 60, Height: 24})
	h.press("A", "2", "e")
	h.typeText(strings.Repeat("x", 80))
	h.press("up")
	// The form's own lines, before the app cuts any to the terminal.
	assertFits(t, ansi.Strip(h.m.pform.View()), 60)
}
