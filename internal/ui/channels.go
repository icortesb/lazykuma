package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/icortesb/lazykuma/internal/kuma"
)

// The notification services with a form of their own. Kuma supports about
// ninety; the rest go through the raw editor.
const (
	svcTelegram = "telegram"
	svcWebhook  = "webhook"
	svcSMTP     = "smtp"
)

var curatedServices = []string{svcTelegram, svcWebhook, svcSMTP}

func serviceLabel(svc string) string {
	switch svc {
	case svcTelegram:
		return "Telegram"
	case svcWebhook:
		return "Webhook"
	case svcSMTP:
		return "Email (SMTP)"
	}
	return svc
}

// fieldsForService is the curated form of a service, in order. The keys are
// Kuma's own, so what is typed lands where Kuma looks for it.
func fieldsForService(svc string) []field {
	name := field{"name", "name      ", "telegram"}
	switch svc {
	case svcTelegram:
		return []field{name,
			{"telegramBotToken", "bot token ", "123456:ABC-DEF"},
			{"telegramChatID", "chat id   ", "7002977645"},
			{"telegramSendSilently", "silent    ", "no"},
			{"isDefault", "default   ", "yes"}}
	case svcWebhook:
		return []field{name,
			{"webhookURL", "url       ", "https://hooks.example.com/kuma"},
			{"webhookContentType", "content   ", "json"},
			{"isDefault", "default   ", "yes"}}
	case svcSMTP:
		return []field{name,
			{"smtpHost", "host      ", "smtp.example.com"},
			{"smtpPort", "port      ", "587"},
			{"smtpSecure", "tls       ", "no"},
			{"smtpUsername", "username  ", "kuma@example.com"},
			{"smtpPassword", "password  ", ""},
			{"smtpFrom", "from      ", "kuma@example.com"},
			{"smtpTo", "to        ", "me@example.com"},
			{"isDefault", "default   ", "yes"}}
	}
	return nil
}

// isSecretField reports whether a field carries a credential, which is
// then typed masked: a bot token is as good as a password.
func isSecretField(key string) bool {
	k := strings.ToLower(key)
	for _, needle := range []string{"password", "token", "secret", "apikey", "api_key"} {
		if strings.Contains(k, needle) {
			return true
		}
	}
	return false
}

// channelForm creates or edits one notification channel.
type channelForm struct {
	form
	svc  string
	id   int            // 0 when creating
	base map[string]any // an edit keeps the fields this form does not show
	defs []field
}

func newChannelForm(svc string) channelForm {
	defs := fieldsForService(svc)
	inputs := make([]textinputModel, 0, len(defs))
	for _, d := range defs {
		in := newField(d.prompt, d.placeholder)
		if isSecretField(d.key) {
			in.EchoMode = echoPassword
			in.EchoCharacter = '•'
		}
		inputs = append(inputs, in)
	}
	f := form{fields: inputs, shown: len(inputs)}
	f, _ = f.focusOn(0)
	c := channelForm{form: f, svc: svc, defs: defs}
	for i, d := range defs {
		switch d.key {
		case "isDefault":
			c.fields[i].SetValue("yes")
		case "telegramSendSilently", "smtpSecure":
			c.fields[i].SetValue("no")
		case "webhookContentType":
			c.fields[i].SetValue("json")
		case "smtpPort":
			c.fields[i].SetValue("587")
		}
	}
	return c
}

// editChannelForm fills the form from a channel Kuma reported.
func editChannelForm(n kuma.Notification) channelForm {
	c := newChannelForm(n.Type)
	c.id, c.base = n.ID, n.Config
	for i, d := range c.defs {
		if d.key == "isDefault" {
			c.fields[i].SetValue(yesNo(n.IsDefault))
			continue
		}
		if v, ok := n.Config[d.key]; ok {
			c.fields[i].SetValue(fieldText(v))
		}
	}
	return c
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func (c channelForm) Update(msg tea.Msg) (channelForm, formAction, tea.Cmd) {
	f, act, cmd := c.form.update(msg)
	c.form = f
	return c, act, cmd
}

// Values is the channel config Kuma stores, which is also what an edit and
// a test send.
func (c channelForm) Values() (map[string]any, error) {
	out := map[string]any{}
	for k, v := range c.base {
		out[k] = v
	}
	out["type"] = c.svc
	out["applyExisting"] = false
	for i, d := range c.defs {
		v := strings.TrimSpace(c.fields[i].Value())
		switch d.key {
		case "name":
			if v == "" {
				return nil, fmt.Errorf("the name is empty")
			}
			out["name"] = v
		case "isDefault", "telegramSendSilently", "smtpSecure":
			out[d.key] = isYes(v)
		case "smtpPort":
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > 65535 {
				return nil, fmt.Errorf("%q is not a port between 1 and 65535", v)
			}
			out[d.key] = n
		case "smtpPassword", "telegramBotToken":
			// Typed masked, sent to Kuma, never written to our own files.
			if c.fields[i].Value() == "" {
				return nil, fmt.Errorf("%s is empty", strings.TrimSpace(d.prompt))
			}
			out[d.key] = c.fields[i].Value()
		default:
			if v == "" {
				return nil, fmt.Errorf("%s is empty", strings.TrimSpace(d.prompt))
			}
			out[d.key] = v
		}
	}
	return out, nil
}

func (c channelForm) View() string {
	title := "New " + serviceLabel(c.svc) + " channel"
	if c.id != 0 {
		title = "Edit " + fieldText(c.base["name"])
	}
	return c.view(title, "secrets go to Kuma and are never stored here")
}

// channelsScreen lists an instance's notification channels.
type channelsScreen struct {
	cursor int
}

type chanAction int

const (
	chanNone chanAction = iota
	chanBack
	chanNew
	chanEdit
	chanDelete
	chanTest
)

func (s channelsScreen) Update(msg tea.KeyMsg, channels []kuma.Notification) (channelsScreen, chanAction) {
	s.cursor = min(s.cursor, max(len(channels)-1, 0))
	switch {
	case key.Matches(msg, keys.Up):
		if s.cursor > 0 {
			s.cursor--
		}
	case key.Matches(msg, keys.Down):
		if s.cursor < len(channels)-1 {
			s.cursor++
		}
	case key.Matches(msg, keys.Back):
		return s, chanBack
	case msg.String() == "n":
		return s, chanNew
	case len(channels) == 0:
		// Nothing to act on yet.
	case key.Matches(msg, keys.Select), msg.String() == "e":
		return s, chanEdit
	case msg.String() == "d":
		return s, chanDelete
	case msg.String() == "t":
		return s, chanTest
	}
	return s, chanNone
}

// selected is the channel under the cursor.
func (s channelsScreen) selected(channels []kuma.Notification) (kuma.Notification, bool) {
	if len(channels) == 0 {
		return kuma.Notification{}, false
	}
	return channels[min(s.cursor, len(channels)-1)], true
}

func (s channelsScreen) View(name string, channels []kuma.Notification, width, height int) string {
	var b strings.Builder
	b.WriteString(styleHeading.Render(name+" · channels") + "\n\n")
	if len(channels) == 0 {
		b.WriteString(styleLabel.Render("no channels yet: n adds one") + "\n")
	}
	cursor := min(s.cursor, max(len(channels)-1, 0))
	for i, c := range channels {
		mark := "  "
		if c.IsDefault {
			mark = "★ " // Kuma applies a default channel to new monitors
		}
		line := fmt.Sprintf("%s%-24s %s", mark, truncate(c.Name, 24), serviceLabel(c.Type))
		if i == cursor {
			b.WriteString(styleRow.Render(" "+truncate(line, width-2)+" ") + "\n")
			continue
		}
		b.WriteString(" " + styleValue.Render(truncate(line, width-2)) + "\n")
	}
	b.WriteString("\n" + styleFooter.Render(
		styleKey.Render("n")+" new   "+styleKey.Render("e")+" edit   "+
			styleKey.Render("d")+" delete   "+styleKey.Render("t")+" test   "+
			styleKey.Render("esc")+" back"))
	return b.String()
}
