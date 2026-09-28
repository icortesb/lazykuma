package ui

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/icortesb/lazykuma/internal/core"
	"github.com/icortesb/lazykuma/internal/kuma"
)

// proxyRow is a row of the proxy form, in its order down the screen.
type proxyRow int

const (
	prProtocol proxyRow = iota
	prHost
	prPort
	prAuth
	prUser
	prPass
	prDefault
	prApply
	proxyRows
)

// proxyFieldWidth is how much of a text field shows: a long host scrolls
// inside it rather than past the edge of a narrow terminal.
const proxyFieldWidth = 40

// proxyForm makes a proxy, or edits one. Its rows mix text fields, the
// protocol, picked from Kuma's list, and toggles, so it draws them itself.
type proxyForm struct {
	// proxy is the proxy as the form opened it, ID 0 for a new one. Its
	// password is kept only to be sent back when the field is left empty:
	// Kuma sends it in the clear, and it is never drawn.
	proxy                  kuma.Proxy
	protocol               int // in kuma.ProxyProtocols
	host, port, user, pass textinputModel
	auth, def              bool
	// apply sets the proxy on every monitor as it saves. It is never
	// filled from the proxy: Kuma keeps no such setting, it is an act.
	apply bool
	row   proxyRow
	err   string
	// pending is set while Kuma saves: a second enter would make a second
	// proxy, for a new one.
	pending bool
}

func newProxyForm(p kuma.Proxy) proxyForm {
	f := proxyForm{
		proxy: p,
		host:  newField("host       ", "proxy.home.lan"),
		port:  newField("port       ", "3128"),
		user:  newField("username   ", ""),
		pass:  newField("password   ", ""),
		auth:  p.Auth,
		def:   p.Default,
		row:   prHost,
	}
	for i, proto := range kuma.ProxyProtocols {
		if proto == p.Protocol {
			f.protocol = i
		}
	}
	f.host.Width, f.user.Width, f.pass.Width = proxyFieldWidth, proxyFieldWidth, proxyFieldWidth
	f.port.CharLimit = 5
	f.pass.EchoMode, f.pass.EchoCharacter = echoPassword, '•'
	if p.Auth {
		// The field starts empty: the password stays Kuma's unless a new
		// one is typed.
		f.pass.Placeholder = "unchanged"
	}
	f.host.SetValue(p.Host)
	if p.Port != 0 {
		f.port.SetValue(strconv.Itoa(p.Port))
	}
	f.user.SetValue(p.Username)
	f, _ = f.focus()
	return f
}

// field is the text field of a row, nil for the protocol and the toggles.
func (f *proxyForm) field(r proxyRow) *textinputModel {
	switch r {
	case prHost:
		return &f.host
	case prPort:
		return &f.port
	case prUser:
		return &f.user
	case prPass:
		return &f.pass
	}
	return nil
}

// shows says whether a row is on screen: the credentials only with auth on.
func (f proxyForm) shows(r proxyRow) bool {
	return f.auth || (r != prUser && r != prPass)
}

// focus puts the text cursor in the row's field, if it has one.
func (f proxyForm) focus() (proxyForm, tea.Cmd) {
	for r := range proxyRows {
		if fld := f.field(r); fld != nil {
			fld.Blur()
		}
	}
	if fld := f.field(f.row); fld != nil {
		return f, fld.Focus()
	}
	return f, nil
}

// move goes by step to the next row on screen, round the ends.
func (f proxyForm) move(step int) (proxyForm, tea.Cmd) {
	r := f.row
	for {
		r = (r + proxyRow(step) + proxyRows) % proxyRows
		if f.shows(r) {
			break
		}
	}
	f.row = r
	return f.focus()
}

// Update handles a key. Enter saves from any row, as every setting is on
// one screen. Tab, shift+tab and the arrows move; letters typed into a text
// field are always text, so h, j, k and l move nothing there.
func (f proxyForm) Update(msg tea.Msg) (proxyForm, formAction, tea.Cmd) {
	k, isKey := msg.(tea.KeyMsg)
	if !isKey {
		// The cursor's blink.
		var cmd tea.Cmd
		if fld := f.field(f.row); fld != nil {
			*fld, cmd = fld.Update(msg)
		}
		return f, formNone, cmd
	}
	if k.Type != tea.KeyRunes {
		switch {
		case k.Type == tea.KeyEnter:
			if f.pending {
				return f, formNone, nil
			}
			return f, formSubmit, nil
		case key.Matches(k, keys.Back):
			return f, formCancel, nil
		case k.Type == tea.KeyTab, k.Type == tea.KeyDown:
			f, cmd := f.move(1)
			return f, formNone, cmd
		case k.Type == tea.KeyShiftTab, k.Type == tea.KeyUp:
			f, cmd := f.move(-1)
			return f, formNone, cmd
		}
	}
	if fld := f.field(f.row); fld != nil {
		var cmd tea.Cmd
		*fld, cmd = typeInto(*fld, msg)
		return f, formNone, cmd
	}
	n := len(kuma.ProxyProtocols)
	switch s := k.String(); {
	case f.row == prProtocol && (s == "right" || s == "l"):
		f.protocol = (f.protocol + 1) % n
	case f.row == prProtocol && (s == "left" || s == "h"):
		f.protocol = (f.protocol + n - 1) % n
	case s != " ":
	case f.row == prAuth:
		f.auth = !f.auth
	case f.row == prDefault:
		f.def = !f.def
	case f.row == prApply:
		f.apply = !f.apply
	}
	return f, formNone, nil
}

// Values are the proxy as the form holds it, and whether to set it on every
// monitor. A password left empty on a proxy that had auth is the one it
// has, so an edit keeps it.
func (f proxyForm) Values() (kuma.Proxy, bool, error) {
	p := kuma.Proxy{ID: f.proxy.ID, Protocol: kuma.ProxyProtocols[f.protocol], Auth: f.auth, Default: f.def}
	p.Host = strings.TrimSpace(f.host.Value())
	if p.Host == "" {
		return kuma.Proxy{}, false, errors.New("the host is empty")
	}
	port := strings.TrimSpace(f.port.Value())
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return kuma.Proxy{}, false, fmt.Errorf("the port is %q; it is a number from 1 to 65535", port)
	}
	p.Port = n
	if !f.auth {
		return p, f.apply, nil
	}
	p.Username, p.Password = strings.TrimSpace(f.user.Value()), f.pass.Value()
	if p.Username == "" {
		return kuma.Proxy{}, false, errors.New("the username is empty")
	}
	if p.Password == "" {
		if !f.proxy.Auth {
			return kuma.Proxy{}, false, errors.New("the password is empty")
		}
		p.Password = f.proxy.Password
	}
	return p, f.apply, nil
}

// proxyAddr is how a proxy is named on screen; never with its credentials.
func proxyAddr(p kuma.Proxy) string {
	return fmt.Sprintf("%s://%s:%d", p.Protocol, p.Host, p.Port)
}

func (f proxyForm) View() string {
	var b strings.Builder
	title := "New proxy"
	if f.proxy.ID != 0 {
		title = "Edit proxy " + truncate(proxyAddr(f.proxy), 44)
	}
	b.WriteString(styleHeading.Render(title) + "\n")
	b.WriteString(styleLabel.Render("the password is never shown; left empty, it stays") + "\n\n")
	line := func(r proxyRow, s string) {
		if r == f.row {
			b.WriteString(styleRow.Render(" "+s+" ") + "\n")
		} else {
			b.WriteString(styleValue.Render(" "+s) + "\n")
		}
	}
	toggle := func(r proxyRow, on bool, name string) {
		mark := "[ ]"
		if on {
			mark = "[x]"
		}
		line(r, mark+" "+name)
	}
	line(prProtocol, "protocol   ‹ "+kuma.ProxyProtocols[f.protocol]+" ›")
	for r := range proxyRows {
		if !f.shows(r) {
			continue
		}
		switch r {
		case prHost, prPort, prUser, prPass:
			b.WriteString(" " + f.field(r).View() + "\n")
		case prAuth:
			toggle(r, f.auth, "authentication")
		case prDefault:
			toggle(r, f.def, "default for new monitors")
		case prApply:
			toggle(r, f.apply, "use it on every existing monitor now")
		}
	}
	if f.pending {
		b.WriteString("\n" + styleLabel.Render("saving…") + "\n")
	}
	if f.err != "" {
		b.WriteString("\n" + styleErr.Render(f.err) + "\n")
	}
	// Two spaces between the keys, so the line fits 60 columns.
	b.WriteString("\n" + styleFooter.Render(styleKey.Render("tab")+" next  "+styleKey.Render("←/→")+" protocol  "+
		styleKey.Render("space")+" toggle  "+styleKey.Render("enter")+" save  "+styleKey.Render("esc")+" cancel"))
	return b.String()
}

func saveProxy(in *core.Instance, p kuma.Proxy, applyExisting bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		action := "saved"
		if p.ID == 0 {
			action = "created"
		}
		_, err := in.SaveProxy(ctx, p, applyExisting)
		return actionDone{name: in.Name(), action: action, mon: fmt.Sprintf("proxy %s:%d", p.Host, p.Port), err: err}
	}
}

func deleteProxy(in *core.Instance, p kuma.Proxy) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		return actionDone{name: in.Name(), action: "deleted", mon: fmt.Sprintf("proxy %s:%d", p.Host, p.Port), err: in.DeleteProxy(ctx, p.ID)}
	}
}
