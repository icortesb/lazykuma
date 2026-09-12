package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/icortesb/lazykuma/internal/kuma"
)

func typeInChannel(c channelForm, s string) channelForm {
	for _, r := range s {
		c, _, _ = c.Update(keyMsg(string(r)))
	}
	return c
}

func TestTelegramChannelForm(t *testing.T) {
	c := newChannelForm(svcTelegram)
	c = typeInChannel(c, "telegram")
	c, _, _ = c.Update(keyMsg("tab"))
	c = typeInChannel(c, "123456:ABC")
	c, _, _ = c.Update(keyMsg("tab"))
	c = typeInChannel(c, "7002977645")

	cfg, err := c.Values()
	if err != nil {
		t.Fatal(err)
	}
	// Kuma's own field names, so what is typed lands where it looks.
	want := map[string]any{
		"type": "telegram", "name": "telegram", "telegramBotToken": "123456:ABC",
		"telegramChatID": "7002977645", "telegramSendSilently": false,
		"isDefault": true, "applyExisting": false,
	}
	for k, v := range want {
		if cfg[k] != v {
			t.Errorf("%s = %v, want %v", k, cfg[k], v)
		}
	}
}

func TestSMTPChannelHidesThePassword(t *testing.T) {
	c := newChannelForm(svcSMTP)
	values := []string{"mail", "smtp.example.com", "", "", "kuma@example.com", "hunter2", "kuma@example.com", "me@example.com"}
	for i, v := range values {
		if i > 0 {
			c, _, _ = c.Update(keyMsg("tab"))
		}
		if v != "" {
			c = typeInChannel(c, v)
		}
	}
	cfg, err := c.Values()
	if err != nil {
		t.Fatal(err)
	}
	if cfg["smtpPassword"] != "hunter2" || cfg["smtpPort"] != 587 || cfg["smtpHost"] != "smtp.example.com" {
		t.Fatalf("config = %v", cfg)
	}
	if strings.Contains(ansi.Strip(c.View()), "hunter2") {
		t.Fatal("the password is on screen")
	}
	if !strings.Contains(ansi.Strip(c.View()), "never stored here") {
		t.Error("the form does not say where secrets go")
	}
}

func TestChannelFormRejectsEmptyFields(t *testing.T) {
	c := newChannelForm(svcWebhook) // nothing typed
	if _, err := c.Values(); err == nil || !strings.Contains(err.Error(), "name is empty") {
		t.Fatalf("err = %v", err)
	}
	c = typeInChannel(c, "hook")
	if _, err := c.Values(); err == nil || !strings.Contains(err.Error(), "url is empty") {
		t.Fatalf("err = %v", err)
	}
}

func TestEditChannelKeepsUnknownFields(t *testing.T) {
	n := kuma.Notification{
		ID: 4, Name: "tg", Type: "telegram", IsDefault: true,
		Config: map[string]any{
			"name": "tg", "type": "telegram", "telegramBotToken": "123:abc",
			"telegramChatID": "42", "telegramServerUrl": "https://api.telegram.org",
			"telegramProtectContent": true,
		},
	}
	c := editChannelForm(n)
	if c.id != 4 || c.fields[0].Value() != "tg" {
		t.Fatalf("form = id %d name %q", c.id, c.fields[0].Value())
	}
	cfg, err := c.Values()
	if err != nil {
		t.Fatal(err)
	}
	if cfg["telegramServerUrl"] != "https://api.telegram.org" || cfg["telegramProtectContent"] != true {
		t.Fatalf("edit dropped fields: %v", cfg)
	}
	if !strings.Contains(ansi.Strip(c.View()), "Edit tg") {
		t.Error("the title does not name the channel")
	}
}

func sampleChannels() []kuma.Notification {
	return []kuma.Notification{
		{ID: 1, Name: "telegram", Type: "telegram", IsDefault: true},
		{ID: 2, Name: "ops mail", Type: "smtp"},
	}
}

func TestChannelsScreen(t *testing.T) {
	s := channelsScreen{}
	chans := sampleChannels()
	out := ansi.Strip(s.View("home", chans, 80, 24))
	for _, want := range []string{"home · channels", "★ telegram", "ops mail", "Email (SMTP)", "n new"} {
		if !strings.Contains(out, want) {
			t.Errorf("view lacks %q:\n%s", want, out)
		}
	}

	if _, act := s.Update(keyMsg("n"), chans); act != chanNew {
		t.Errorf("n = %v", act)
	}
	s, act := s.Update(keyMsg("j"), chans)
	if act != chanNone || s.cursor != 1 {
		t.Fatalf("j = %v, cursor %d", act, s.cursor)
	}
	if got, _ := s.selected(chans); got.ID != 2 {
		t.Errorf("selected = %+v", got)
	}
	for _, tt := range []struct {
		key  string
		want chanAction
	}{{"e", chanEdit}, {"d", chanDelete}, {"t", chanTest}, {"esc", chanBack}} {
		if _, act := s.Update(keyMsg(tt.key), chans); act != tt.want {
			t.Errorf("%s = %v, want %v", tt.key, act, tt.want)
		}
	}

	// With no channels, only new and back do anything.
	empty := channelsScreen{}
	if _, act := empty.Update(keyMsg("d"), nil); act != chanNone {
		t.Errorf("d on an empty list = %v", act)
	}
	if _, ok := empty.selected(nil); ok {
		t.Error("selected on an empty list")
	}
	if !strings.Contains(ansi.Strip(empty.View("home", nil, 80, 24)), "no channels yet") {
		t.Error("the empty list does not say what to do")
	}
}
