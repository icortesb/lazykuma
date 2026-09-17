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

// toggle is one entry of a list under the form's fields: a group, a tag or
// a channel, and whether this monitor has it.
type toggle struct {
	id   int
	name string
	on   bool
}

// channelToggle keeps the name the channel code knows toggles by.
type channelToggle = toggle

// The lists under the fields, by title, in the order they show.
const (
	listGroup    = "group"
	listTags     = "tags"
	listChannels = "notify through"
)

// toggleList is a list under the fields. A radio list has exactly one entry
// on: a monitor is in one group or none.
type toggleList struct {
	title string
	items []toggle
	radio bool
}

// formLists is what a monitor can be put in or given: the instance's
// groups ("No group" first, id 0), tags and channels.
type formLists struct {
	groups, tags, channels []toggle
}

// monitorForm creates or edits one monitor.
type monitorForm struct {
	form
	kind string
	id   int // 0 when creating
	// base is the whole monitor an edit must send back: Kuma replaces the
	// monitor with what it receives and refuses a partial object.
	base kuma.RawMonitor
	defs []field
	// lists are the group, tags and channels, the empty ones left out.
	lists []toggleList
	list  int // the list the cursor is in; -1 while it is in the fields
	item  int
	// tagsBefore are the monitor's tags when the form opened: a clone's
	// count as not yet added, so they are applied to the copy.
	tagsBefore []kuma.Tag
	clone      bool
	cloneOf    string // the name of the monitor a clone copies, for the title
}

func (m monitorForm) inLists() bool { return m.list >= 0 }

// channelItems is the channel list's entries, for the tests and the view.
func (m monitorForm) channelItems() []toggle {
	for _, l := range m.lists {
		if l.title == listChannels {
			return l.items
		}
	}
	return nil
}

// newMonitorForm starts a new monitor of the given type.
func newMonitorForm(kind string, lists formLists) monitorForm {
	defs := fieldsFor(kind)
	inputs := make([]textinputModel, 0, len(defs))
	for _, d := range defs {
		inputs = append(inputs, newField(d.prompt, d.placeholder))
	}
	f := form{fields: inputs, shown: len(inputs)}
	f, _ = f.focusOn(0)
	m := monitorForm{form: f, kind: kind, defs: defs, list: -1}
	// A form only offers a group when there is one to be in.
	if len(lists.groups) > 1 {
		m.lists = append(m.lists, toggleList{title: listGroup, items: lists.groups, radio: true})
	}
	if len(lists.tags) > 0 {
		m.lists = append(m.lists, toggleList{title: listTags, items: lists.tags})
	}
	if len(lists.channels) > 0 {
		m.lists = append(m.lists, toggleList{title: listChannels, items: lists.channels})
	}
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
func editMonitorForm(mon kuma.RawMonitor, lists formLists) monitorForm {
	kind, _ := mon["type"].(string)
	m := newMonitorForm(kind, lists)
	m.base = mon
	if id, ok := numberOf(mon["id"]); ok {
		m.id = id
	}
	for i, d := range m.defs {
		m.fields[i].SetValue(fieldText(mon[d.key]))
	}
	parent, _ := numberOf(mon["parent"])
	on := map[string]bool{}
	if raw, ok := mon["notificationIDList"].(map[string]any); ok {
		for id, v := range raw {
			if b, _ := v.(bool); b {
				on[id] = true
			}
		}
	}
	m.tagsBefore = rawTags(mon["tags"])
	has := map[int]bool{}
	for _, t := range m.tagsBefore {
		has[t.ID] = true
	}
	for li := range m.lists {
		l := &m.lists[li]
		for i := range l.items {
			switch l.title {
			case listGroup:
				l.items[i].on = l.items[i].id == parent
			case listTags:
				l.items[i].on = has[l.items[i].id]
			case listChannels:
				l.items[i].on = on[strconv.Itoa(l.items[i].id)]
			}
		}
	}
	return m
}

// cloneMonitorForm is a new monitor made from a whole one: its settings,
// group, channels and tags, under "copy of" its name.
func cloneMonitorForm(mon kuma.RawMonitor, lists formLists) monitorForm {
	m := editMonitorForm(mon, lists)
	m.base = kuma.ForClone(mon)
	m.id, m.clone, m.cloneOf = 0, true, fieldText(mon["name"])
	if i := m.index("name"); i >= 0 {
		m.fields[i].SetValue(fieldText(m.base["name"]))
	}
	return m
}

// rawTags reads the tags getMonitor returns on a monitor.
func rawTags(v any) []kuma.Tag {
	list, _ := v.([]any)
	out := make([]kuma.Tag, 0, len(list))
	for _, e := range list {
		t, _ := e.(map[string]any)
		id, ok := numberOf(t["tag_id"])
		if !ok {
			continue
		}
		name, _ := t["name"].(string)
		color, _ := t["color"].(string)
		value, _ := t["value"].(string)
		out = append(out, kuma.Tag{ID: id, Name: name, Color: color, Value: value})
	}
	return out
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

// Update handles a key. The group, tag and channel lists sit after the
// fields: tab walks into them, and on through them a list at a time; space
// chooses a group or toggles a tag or a channel.
func (m monitorForm) Update(msg tea.Msg) (monitorForm, formAction, tea.Cmd) {
	k, isKey := msg.(tea.KeyMsg)
	if isKey && m.inLists() {
		l := &m.lists[m.list]
		switch {
		case key.Matches(k, keys.Back):
			return m, formCancel, nil
		case k.Type == tea.KeyShiftTab:
			return m.prevList()
		case k.Type == tea.KeyTab:
			if m.list < len(m.lists)-1 {
				m.list, m.item = m.list+1, 0
			}
		case key.Matches(k, keys.Up):
			if m.item > 0 {
				m.item--
			} else {
				return m.prevList()
			}
		case key.Matches(k, keys.Down):
			if m.item < len(l.items)-1 {
				m.item++
			} else if m.list < len(m.lists)-1 {
				m.list, m.item = m.list+1, 0
			}
		case k.Type == tea.KeySpace:
			if l.radio {
				for i := range l.items {
					l.items[i].on = i == m.item
				}
			} else {
				l.items[m.item].on = !l.items[m.item].on
			}
		case k.Type == tea.KeyEnter:
			return m, formSubmit, nil
		}
		return m, formNone, nil
	}

	// Leaving the last field walks into the lists rather than submitting,
	// so they are never skipped by accident.
	if isKey && len(m.lists) > 0 && m.focus == m.shown-1 &&
		(k.Type == tea.KeyEnter || key.Matches(k, keys.Next)) {
		m.list, m.item = 0, 0
		for i := range m.fields {
			m.fields[i].Blur()
		}
		return m, formNone, nil
	}

	f, act, cmd := m.form.update(msg)
	m.form = f
	return m, act, cmd
}

// prevList goes up out of a list: to the end of the previous one, or back
// to the last field.
func (m monitorForm) prevList() (monitorForm, formAction, tea.Cmd) {
	if m.list > 0 {
		m.list--
		m.item = len(m.lists[m.list].items) - 1
		return m, formNone, nil
	}
	m.list = -1
	f, cmd := m.form.focusOn(m.shown - 1)
	m.form = f
	return m, formNone, cmd
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

	for _, l := range m.lists {
		if l.title != listGroup {
			continue
		}
		for _, g := range l.items {
			if !g.on {
				continue
			}
			if g.id == 0 {
				out["parent"] = nil
			} else {
				out["parent"] = g.id
			}
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
	for _, c := range m.channelItems() {
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

// TagChanges is what to do to the monitor's tags after saving it: Kuma's
// add and editMonitor ignore tags. A new tag goes on with no value; a
// removed one names the value it had, which Kuma matches on, and a tag the
// monitor carries more than once, with different values, goes with all of
// them. A clone starts with none, so its source's tags, values kept, are all
// added: the ticked ones, and those the form did not list, which the user
// could not untick (the instance's tags were not fetched yet, or a tag was
// made in the web UI since).
func (m monitorForm) TagChanges() (add, remove []kuma.Tag) {
	before := map[int][]kuma.Tag{}
	for _, t := range m.tagsBefore {
		before[t.ID] = append(before[t.ID], t)
	}
	listed := map[int]bool{}
	for _, l := range m.lists {
		if l.title != listTags {
			continue
		}
		for _, it := range l.items {
			listed[it.id] = true
			had, was := before[it.id]
			switch {
			case m.clone && it.on && was:
				add = append(add, had...)
			case it.on && !was:
				add = append(add, kuma.Tag{ID: it.id, Name: it.name})
			case !it.on && was && !m.clone:
				remove = append(remove, had...)
			}
		}
	}
	if m.clone {
		for _, t := range m.tagsBefore {
			if !listed[t.ID] {
				add = append(add, t)
			}
		}
	}
	return add, remove
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
	switch {
	case m.clone:
		title = "Clone of " + m.cloneOf
	case m.id != 0:
		title = "Edit " + fieldText(m.base["name"])
	}
	var b strings.Builder
	b.WriteString(m.view(title, "tab moves; enter on the last field goes to the lists below"))
	if len(m.lists) == 0 {
		return b.String()
	}
	for li, l := range m.lists {
		b.WriteString("\n\n" + styleLabel.Render(l.title) + "\n")
		for i, it := range l.items {
			mark := "[ ]"
			switch {
			case l.radio && it.on:
				mark = "(x)"
			case l.radio:
				mark = "( )"
			case it.on:
				mark = "[x]"
			}
			line := fmt.Sprintf("%s %s", mark, it.name)
			if m.list == li && i == m.item {
				line = styleRow.Render(" " + line + " ")
			} else {
				line = styleValue.Render(" " + line)
			}
			b.WriteString(line + "\n")
		}
	}
	b.WriteString(styleFooter.Render(styleKey.Render("space") + " choose/toggle   " +
		styleKey.Render("tab") + " next list   " + styleKey.Render("enter") + " save"))
	return b.String()
}
