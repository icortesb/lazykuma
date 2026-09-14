package ui

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/icortesb/lazykuma/internal/kuma"
)

// The monitor types with a form of their own. Everything else Kuma offers
// goes through the raw editor, because Kuma does not publish each type's
// fields: its own web UI hardcodes them, one type at a time.
const (
	kindHTTP    = "http"
	kindKeyword = "keyword"
	kindPing    = "ping"
	kindPort    = "port"
)

var curatedKinds = []string{kindHTTP, kindKeyword, kindPing, kindPort}

// isCuratedKind reports whether this type has a form of its own.
func isCuratedKind(kind string) bool {
	for _, k := range curatedKinds {
		if k == kind {
			return true
		}
	}
	return false
}

// kindLabel is how the type picker names a type.
func kindLabel(kind string) string {
	switch kind {
	case kindHTTP:
		return "HTTP — a URL answers"
	case kindKeyword:
		return "Keyword — a URL answers and contains a word"
	case kindPing:
		return "Ping — a host answers"
	case kindPort:
		return "Port — a TCP port accepts connections"
	}
	return kind
}

// field is one line of a monitor form: which monitor key it fills, and how
// it is shown.
type field struct {
	key, prompt, placeholder string
}

// fieldsFor is the curated form of a type, in order.
func fieldsFor(kind string) []field {
	name := field{"name", "name      ", "nextcloud"}
	interval := field{"interval", "interval  ", "60"}
	retries := field{"maxretries", "retries   ", "0"}
	accepted := field{"accepted_statuscodes", "accept    ", "200-299"}
	switch kind {
	case kindHTTP:
		return []field{name, {"url", "url       ", "https://cloud.home.lan"}, interval, retries, accepted}
	case kindKeyword:
		return []field{name, {"url", "url       ", "https://cloud.home.lan"},
			{"keyword", "keyword   ", "Login"}, {"invertKeyword", "inverted  ", "no"}, interval, retries, accepted}
	case kindPing:
		return []field{name, {"hostname", "host      ", "10.0.0.2"}, interval, retries}
	case kindPort:
		return []field{name, {"hostname", "host      ", "db.home.lan"},
			{"port", "port      ", "5432"}, interval, retries}
	}
	return nil
}

// channelToggle is a notification channel and whether this monitor uses it.
type channelToggle struct {
	id   int
	name string
	on   bool
}

// monitorForm creates or edits one monitor.
type monitorForm struct {
	form
	kind string
	id   int // 0 when creating
	// base is the whole monitor an edit must send back: Kuma replaces the
	// monitor with what it receives and refuses a partial object.
	base     kuma.RawMonitor
	defs     []field
	channels []channelToggle
	onChans  bool // the cursor is in the channel list
	chanCur  int
}

// newMonitorForm starts a new monitor of the given type.
func newMonitorForm(kind string, channels []channelToggle) monitorForm {
	defs := fieldsFor(kind)
	inputs := make([]textinputModel, 0, len(defs))
	for _, d := range defs {
		inputs = append(inputs, newField(d.prompt, d.placeholder))
	}
	f := form{fields: inputs, shown: len(inputs)}
	f, _ = f.focusOn(0)
	m := monitorForm{form: f, kind: kind, defs: defs, channels: channels}
	// Kuma's own defaults, so an empty field means what the web UI means.
	for key, value := range map[string]string{
		"interval": "60", "maxretries": "0", "accepted_statuscodes": "200-299", "invertKeyword": "no",
	} {
		if i := m.index(key); i >= 0 {
			m.fields[i].SetValue(value)
		}
	}
	return m
}

// editMonitorForm fills the form from a monitor Kuma returned whole.
func editMonitorForm(mon kuma.RawMonitor, channels []channelToggle) monitorForm {
	kind, _ := mon["type"].(string)
	m := newMonitorForm(kind, channels)
	m.base = mon
	if id, ok := numberOf(mon["id"]); ok {
		m.id = id
	}
	for i, d := range m.defs {
		m.fields[i].SetValue(fieldText(mon[d.key]))
	}
	on := map[string]bool{}
	if raw, ok := mon["notificationIDList"].(map[string]any); ok {
		for id, v := range raw {
			if b, _ := v.(bool); b {
				on[id] = true
			}
		}
	}
	for i := range m.channels {
		m.channels[i].on = on[strconv.Itoa(m.channels[i].id)]
	}
	return m
}

// index is the field filling a monitor key, or -1.
func (m monitorForm) index(key string) int {
	for i, d := range m.defs {
		if d.key == key {
			return i
		}
	}
	return -1
}

// fieldText is how a monitor's value is shown in a text field.
func fieldText(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		if t {
			return "yes"
		}
		return "no"
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case []any:
		parts := make([]string, 0, len(t))
		for _, e := range t {
			parts = append(parts, fieldText(e))
		}
		return strings.Join(parts, ", ")
	}
	return fmt.Sprint(v)
}

func numberOf(v any) (int, bool) {
	switch t := v.(type) {
	case float64:
		return int(t), true
	case int:
		return t, true
	}
	return 0, false
}

// Update handles a key. The channel list sits after the fields: tab walks
// into it and space toggles a channel.
func (m monitorForm) Update(msg tea.Msg) (monitorForm, formAction, tea.Cmd) {
	k, isKey := msg.(tea.KeyMsg)
	if isKey && m.onChans {
		switch {
		case key.Matches(k, keys.Back):
			return m, formCancel, nil
		case key.Matches(k, keys.Up):
			if m.chanCur > 0 {
				m.chanCur--
			} else {
				m.onChans = false
				f, cmd := m.form.focusOn(m.shown - 1)
				m.form = f
				return m, formNone, cmd
			}
		case key.Matches(k, keys.Down):
			if m.chanCur < len(m.channels)-1 {
				m.chanCur++
			}
		case k.Type == tea.KeySpace:
			m.channels[m.chanCur].on = !m.channels[m.chanCur].on
		case k.Type == tea.KeyEnter:
			return m, formSubmit, nil
		}
		return m, formNone, nil
	}

	// Leaving the last field walks into the channel list rather than
	// submitting, so the channels are never skipped by accident.
	if isKey && len(m.channels) > 0 && m.focus == m.shown-1 &&
		(k.Type == tea.KeyEnter || key.Matches(k, keys.Next)) {
		m.onChans, m.chanCur = true, 0
		for i := range m.fields {
			m.fields[i].Blur()
		}
		return m, formNone, nil
	}

	f, act, cmd := m.form.update(msg)
	m.form = f
	return m, act, cmd
}

// Values is the monitor to send. An edit keeps every field Kuma knows and
// this form does not.
func (m monitorForm) Values() (kuma.RawMonitor, error) {
	out := kuma.RawMonitor{}
	if m.base != nil {
		for k, v := range m.base {
			out[k] = v
		}
	} else {
		// The fields Kuma's own form always sends for a new monitor.
		out["type"] = m.kind
		out["method"] = "GET"
		out["retryInterval"] = 60
		out["timeout"] = 48
		out["maxredirects"] = 10
		out["expiryNotification"] = true
		out["accepted_statuscodes"] = []string{"200-299"}
		out["conditions"] = []any{}
		out["kafkaProducerBrokers"] = []any{}
		out["kafkaProducerSaslOptions"] = map[string]any{}
	}

	for i, d := range m.defs {
		v := strings.TrimSpace(m.fields[i].Value())
		switch d.key {
		case "name":
			if v == "" {
				return nil, fmt.Errorf("the name is empty")
			}
			out["name"] = v
		case "url":
			u, err := url.Parse(v)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				return nil, fmt.Errorf("%q is not an http(s) URL", v)
			}
			out["url"] = v
		case "hostname":
			if v == "" {
				return nil, fmt.Errorf("the host is empty")
			}
			out["hostname"] = v
		case "keyword":
			if v == "" {
				return nil, fmt.Errorf("the keyword is empty")
			}
			out["keyword"] = v
		case "invertKeyword":
			out["invertKeyword"] = isYes(v)
		case "port":
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > 65535 {
				return nil, fmt.Errorf("%q is not a port between 1 and 65535", v)
			}
			out["port"] = n
		case "interval":
			n, err := strconv.Atoi(v)
			if err != nil || n < 20 {
				return nil, fmt.Errorf("the interval is %q; Kuma's minimum is 20 seconds", v)
			}
			out["interval"] = n
			if _, ok := out["retryInterval"]; !ok || m.base == nil {
				out["retryInterval"] = n
			}
		case "maxretries":
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return nil, fmt.Errorf("%q is not a number of retries", v)
			}
			out["maxretries"] = n
		case "accepted_statuscodes":
			codes := splitList(v)
			if len(codes) == 0 {
				return nil, fmt.Errorf("no accepted status codes")
			}
			out["accepted_statuscodes"] = codes
		}
	}

	// Start from the monitor's own channels and only flip the ones this
	// form showed: editMonitor replaces the monitor, so a channel the form
	// never knew about must not be dropped from it.
	ids := map[string]bool{}
	if base, ok := m.base["notificationIDList"].(map[string]any); ok {
		for id, v := range base {
			if b, _ := v.(bool); b {
				ids[id] = true
			}
		}
	}
	for _, c := range m.channels {
		key := strconv.Itoa(c.id)
		if c.on {
			ids[key] = true
			continue
		}
		delete(ids, key)
	}
	out["notificationIDList"] = ids
	return out, nil
}

func isYes(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "yes", "y", "true", "1":
		return true
	}
	return false
}

// splitList reads a comma separated field into its parts.
func splitList(v string) []string {
	out := []string{}
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (m monitorForm) View(width int) string {
	title := "New " + m.kind + " monitor"
	if m.id != 0 {
		title = "Edit " + fieldText(m.base["name"])
	}
	var b strings.Builder
	b.WriteString(m.view(title, "tab moves; enter on the last field goes to the channels"))
	if len(m.channels) > 0 {
		b.WriteString("\n\n" + styleLabel.Render("notify through") + "\n")
		for i, c := range m.channels {
			mark := "[ ]"
			if c.on {
				mark = "[x]"
			}
			line := fmt.Sprintf("%s %s", mark, c.name)
			if m.onChans && i == m.chanCur {
				line = styleRow.Render(" " + line + " ")
			} else {
				line = styleValue.Render(" " + line)
			}
			b.WriteString(line + "\n")
		}
		b.WriteString(styleFooter.Render(styleKey.Render("space") + " toggle   " + styleKey.Render("enter") + " save"))
	}
	return b.String()
}
