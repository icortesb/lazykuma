package ui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/icortesb/lazykuma/internal/kuma"
)

func typeLogin(l loginForm, s string) loginForm {
	for _, r := range s {
		l, _, _ = l.Update(keyMsg(string(r)))
	}
	return l
}

func TestLoginFormFlow(t *testing.T) {
	l := newLoginForm()
	l = typeLogin(l, "admin")
	l, act, _ := l.Update(keyMsg("enter")) // to the password
	if act != formNone {
		t.Fatalf("enter on the username = %v", act)
	}
	l = typeLogin(l, "pw")
	l, act, _ = l.Update(keyMsg("enter"))
	if act != formSubmit || !l.busy {
		t.Fatalf("enter on the password = %v, busy %v", act, l.busy)
	}
	if u, p, c := l.Values(); u != "admin" || p != "pw" || c != "" {
		t.Fatalf("values = %q %q %q", u, p, c)
	}
	if strings.Contains(ansi.Strip(l.View("home", "http://kuma.lan")), "pw") {
		t.Fatal("the password is shown")
	}

	// Kuma wants the 2FA code: a third field appears and has the focus.
	l, _ = l.WithResult(kuma.ErrTokenRequired)
	if l.busy || l.shown != 3 || l.focus != 2 {
		t.Fatalf("after tokenRequired: busy %v shown %d focus %d", l.busy, l.shown, l.focus)
	}
	l = typeLogin(l, "123456")
	l, act, _ = l.Update(keyMsg("enter"))
	if _, _, c := l.Values(); act != formSubmit || c != "123456" {
		t.Fatalf("code submit = %v, code %q", act, c)
	}

	l, _ = l.WithResult(&kuma.ReplyError{Msg: "authInvalidToken"})
	if !strings.Contains(ansi.Strip(l.View("home", "")), "wrong username, password or code") {
		t.Fatal("no auth error shown")
	}
	if _, _, c := l.Values(); c != "" {
		t.Fatal("the wrong code was kept")
	}

	l, _ = l.WithResult(errors.New("kuma: dial tcp: connection refused"))
	if !strings.Contains(ansi.Strip(l.View("home", "")), "connection refused") {
		t.Fatal("network error not shown")
	}
}

func TestLoginFormIgnoresKeysWhileBusy(t *testing.T) {
	l := newLoginForm()
	l.busy = true
	l = typeLogin(l, "x")
	if u, _, _ := l.Values(); u != "" {
		t.Fatal("typed while busy")
	}
}

func TestFormCancel(t *testing.T) {
	if _, act, _ := newLoginForm().Update(keyMsg("esc")); act != formCancel {
		t.Fatalf("esc = %v", act)
	}
	if _, act, _ := newAddForm().Update(keyMsg("esc")); act != formCancel {
		t.Fatalf("esc = %v", act)
	}
}

func TestAddForm(t *testing.T) {
	a := newAddForm()
	for _, r := range "vps" {
		a, _, _ = a.Update(keyMsg(string(r)))
	}
	a, _, _ = a.Update(keyMsg("tab"))
	for _, r := range " https://status.example.com/ " {
		a, _, _ = a.Update(keyMsg(string(r)))
	}
	a, act, _ := a.Update(keyMsg("enter"))
	if act != formSubmit {
		t.Fatalf("enter = %v", act)
	}
	if v := a.Values(); v.Name != "vps" || v.URL != "https://status.example.com" {
		t.Fatalf("values = %+v", v)
	}
	a = a.WithError(errors.New(`there is already an instance called "vps"`))
	if !strings.Contains(ansi.Strip(a.View()), "already an instance") {
		t.Fatal("error not shown")
	}
}

func TestFieldTakesAWordThatIsAKeyName(t *testing.T) {
	// "home" in one message is also the name of the Home key.
	for _, word := range []string{"home", "end", "down"} {
		a, _, _ := newAddForm().Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(word)})
		if got := a.Values().Name; got != word {
			t.Errorf("typed %q, field has %q", word, got)
		}
	}
}
