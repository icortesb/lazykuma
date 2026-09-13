package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/icortesb/lazykuma/internal/kuma"
)

func typeInForm(m monitorForm, s string) monitorForm {
	for _, r := range s {
		m, _, _ = m.Update(keyMsg(string(r)))
	}
	return m
}

func TestNewHTTPMonitor(t *testing.T) {
	m := newMonitorForm(kindHTTP, []channelToggle{{id: 5, name: "telegram"}})
	m = typeInForm(m, "nextcloud")
	m, _, _ = m.Update(keyMsg("tab"))
	m = typeInForm(m, "https://cloud.home.lan")

	// The rest keeps Kuma's defaults, which the form starts filled with.
	mon, err := m.Values()
	if err != nil {
		t.Fatal(err)
	}
	if mon["type"] != "http" || mon["name"] != "nextcloud" || mon["url"] != "https://cloud.home.lan" {
		t.Fatalf("monitor = %v", mon)
	}
	if mon["interval"] != 60 || mon["maxretries"] != 0 || mon["retryInterval"] != 60 {
		t.Errorf("defaults = %v %v %v", mon["interval"], mon["maxretries"], mon["retryInterval"])
	}
	codes, _ := mon["accepted_statuscodes"].([]string)
	if len(codes) != 1 || codes[0] != "200-299" {
		t.Errorf("accepted = %v", mon["accepted_statuscodes"])
	}
	// No channel was ticked, so none is set.
	if ids := mon["notificationIDList"].(map[string]bool); len(ids) != 0 {
		t.Errorf("channels = %v", ids)
	}
}

func TestMonitorFormChannels(t *testing.T) {
	m := newMonitorForm(kindPing, []channelToggle{{id: 5, name: "telegram"}, {id: 6, name: "mail"}})
	m = typeInForm(m, "pihole")
	m, _, _ = m.Update(keyMsg("tab"))
	m = typeInForm(m, "10.0.0.2")

	// Walking past the last field lands in the channel list, not a submit.
	var act formAction
	for i := 0; i < 3; i++ {
		m, act, _ = m.Update(keyMsg("tab"))
		if act == formSubmit {
			t.Fatal("tab submitted instead of reaching the channels")
		}
		if m.onChans {
			break
		}
	}
	if !m.onChans {
		t.Fatal("never reached the channel list")
	}
	m, _, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace}) // telegram on
	m, _, _ = m.Update(keyMsg("j"))
	m, _, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace}) // mail on
	m, _, _ = m.Update(keyMsg("j"))
	m, _, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace}) // mail off again
	m, act, _ = m.Update(keyMsg("enter"))
	if act != formSubmit {
		t.Fatalf("enter in the channel list = %v", act)
	}

	mon, err := m.Values()
	if err != nil {
		t.Fatal(err)
	}
	ids := mon["notificationIDList"].(map[string]bool)
	if len(ids) != 1 || !ids["5"] {
		t.Fatalf("channels = %v", ids)
	}
	if mon["type"] != "ping" || mon["hostname"] != "10.0.0.2" {
		t.Fatalf("monitor = %v", mon)
	}
	if !strings.Contains(ansi.Strip(m.View(80)), "[x] telegram") {
		t.Errorf("view:\n%s", ansi.Strip(m.View(80)))
	}
}

func TestMonitorFormRejectsBadValues(t *testing.T) {
	tests := []struct {
		name   string
		kind   string
		values []string
		want   string
	}{
		{"no name", kindHTTP, []string{"", "https://x.lan"}, "name is empty"},
		{"bad url", kindHTTP, []string{"web", "cloud.home.lan"}, "not an http(s) URL"},
		{"no host", kindPing, []string{"ping", ""}, "host is empty"},
		{"bad port", kindPort, []string{"db", "db.lan", "70000"}, "not a port"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newMonitorForm(tt.kind, nil)
			for i, v := range tt.values {
				if i > 0 {
					m, _, _ = m.Update(keyMsg("tab"))
				}
				m = typeInForm(m, v)
			}
			_, err := m.Values()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want one about %q", err, tt.want)
			}
		})
	}

	// Kuma refuses anything under 20 seconds, so the form does too.
	m := newMonitorForm(kindHTTP, nil)
	m = typeInForm(m, "web")
	m, _, _ = m.Update(keyMsg("tab"))
	m = typeInForm(m, "https://x.lan")
	m, _, _ = m.Update(keyMsg("tab"))
	m.fields[m.index("interval")].SetValue("5")
	if _, err := m.Values(); err == nil || !strings.Contains(err.Error(), "20 seconds") {
		t.Fatalf("interval 5 = %v", err)
	}
}

func TestEditKeepsEveryFieldKumaKnows(t *testing.T) {
	// A monitor as Kuma returns it: fields the form never shows must come
	// back untouched, or editMonitor would wipe them.
	mon := kuma.RawMonitor{
		"id": float64(3), "type": "keyword", "name": "vault", "url": "https://vault.lan",
		"keyword": "Login", "invertKeyword": false, "interval": float64(120),
		"maxretries": float64(2), "accepted_statuscodes": []any{"200-299"},
		"notificationIDList": map[string]any{"5": true},
		"resendInterval":     float64(10), "httpBodyEncoding": "json", "weight": float64(2000),
	}
	m := editMonitorForm(mon, []channelToggle{{id: 5, name: "telegram"}, {id: 6, name: "mail"}})

	if m.id != 3 || m.kind != "keyword" {
		t.Fatalf("form = id %d kind %q", m.id, m.kind)
	}
	if got := m.fields[m.index("name")].Value(); got != "vault" {
		t.Errorf("name field = %q", got)
	}
	if got := m.fields[m.index("interval")].Value(); got != "120" {
		t.Errorf("interval field = %q", got)
	}
	if got := m.fields[m.index("accepted_statuscodes")].Value(); got != "200-299" {
		t.Errorf("accepted field = %q", got)
	}
	if !m.channels[0].on || m.channels[1].on {
		t.Errorf("channels = %+v", m.channels)
	}

	out, err := m.Values()
	if err != nil {
		t.Fatal(err)
	}
	if out["id"] != float64(3) || out["resendInterval"] != float64(10) || out["httpBodyEncoding"] != "json" || out["weight"] != float64(2000) {
		t.Fatalf("edit dropped fields: %v", out)
	}
	if ids := out["notificationIDList"].(map[string]bool); !ids["5"] || len(ids) != 1 {
		t.Errorf("channels = %v", ids)
	}
	if !strings.Contains(ansi.Strip(m.View(80)), "Edit vault") {
		t.Errorf("view:\n%s", ansi.Strip(m.View(80)))
	}
}

func TestKindLabels(t *testing.T) {
	for _, k := range curatedKinds {
		if !strings.Contains(kindLabel(k), "—") {
			t.Errorf("%s has no description", k)
		}
	}
	if kindLabel("dns") != "dns" {
		t.Errorf("an uncurated type keeps its name")
	}
}

func TestEditNeverDropsChannelsTheFormCannotSee(t *testing.T) {
	// The channel list can be empty — it arrives after the monitors, and a
	// channel whose config Kuma cannot read is skipped — but editMonitor
	// replaces the monitor, so an empty form must not unlink anything.
	mon := kuma.RawMonitor{
		"id": float64(3), "type": "http", "name": "vault", "url": "https://vault.lan",
		"interval": float64(60), "maxretries": float64(0), "accepted_statuscodes": []any{"200-299"},
		"notificationIDList": map[string]any{"5": true, "9": true},
	}

	// No channels known at all: both survive untouched.
	out, err := editMonitorForm(mon, nil).Values()
	if err != nil {
		t.Fatal(err)
	}
	ids := out["notificationIDList"].(map[string]bool)
	if !ids["5"] || !ids["9"] || len(ids) != 2 {
		t.Fatalf("channels lost with an empty list: %v", ids)
	}

	// One of the two known: the other is still not this form's to remove.
	m := editMonitorForm(mon, []channelToggle{{id: 5, name: "telegram"}})
	if !m.channels[0].on {
		t.Fatal("the monitor's own channel is not ticked")
	}
	out, err = m.Values()
	if err != nil {
		t.Fatal(err)
	}
	ids = out["notificationIDList"].(map[string]bool)
	if !ids["5"] || !ids["9"] {
		t.Fatalf("channels = %v", ids)
	}

	// Unticking the one it shows removes that one, and only that one.
	m.channels[0].on = false
	out, _ = m.Values()
	ids = out["notificationIDList"].(map[string]bool)
	if ids["5"] || !ids["9"] {
		t.Fatalf("unticking removed the wrong channel: %v", ids)
	}
}
