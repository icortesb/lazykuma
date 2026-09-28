package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/icortesb/lazykuma/internal/core"
	"github.com/icortesb/lazykuma/internal/kuma"
	"github.com/icortesb/lazykuma/internal/state"
)

// keyHintsServer is the server screen's section of the help: every key of
// every tab. The footer shows only the open tab's, from hints.
const keyHintsServer = "tab next tab   1-4 tabs   n new   e edit   space enable/disable   t test   d delete   s shrink   X clear statistics   r refresh   ? help   esc back"

// hints is the footer of the open tab: its own keys, then the ones every
// tab takes. Two spaces between the keys keep each under 80 columns.
func (s serverScreen) hints() string {
	own := "n new  space enable/disable  d delete"
	switch s.tab {
	case tabProxies:
		own = "n new  e edit  d delete"
	case tabDocker:
		own = "n new  e edit  t test  d delete"
	case tabDatabase:
		own = "s shrink  X clear stats  r refresh"
	}
	return own + "  tab next tab  1-4 tabs  ? help  esc back"
}

type serverTab int

const (
	tabKeys serverTab = iota
	tabProxies
	tabDocker
	tabDatabase
)

const serverTabs = 4

func (t serverTab) String() string {
	switch t {
	case tabKeys:
		return "API keys"
	case tabProxies:
		return "proxies"
	case tabDocker:
		return "Docker hosts"
	}
	return "database"
}

type serverAction int

const (
	srvNone serverAction = iota
	srvBack
	srvNew     // a new key, proxy or Docker host, by the tab
	srvEdit    // the selected proxy or Docker host
	srvToggle  // enable or disable the selected API key
	srvDelete  // the selected key, proxy or Docker host
	srvTest    // try the selected Docker host
	srvShrink  // compact the database
	srvClear   // clear every monitor's statistics
	srvRefresh // ask the database's size again
)

// serverScreen is an instance's administration: its API keys, proxies,
// Docker hosts and database, one tab each. The lists come from the
// instance's state on every call, as Kuma pushes them again after each
// write; the screen holds only a cursor per tab and the database's size,
// which Kuma gives only when asked.
type serverScreen struct {
	tab     serverTab
	cursor  [serverTabs]int
	dbSize  int64
	dbKnown bool
	dbErr   string
	// test is the Docker host test on its way, set by the model as it
	// draws: the screen is made afresh when it opens, the test is not.
	test dockerTest
}

// dbSized is the database's size, as Kuma answered it for an instance.
type dbSized struct {
	instance string
	size     int64
	err      error
}

func loadDBSize(in *core.Instance) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		size, err := in.DatabaseSize(ctx)
		return dbSized{instance: in.Name(), size: size, err: err}
	}
}

// withSize takes Kuma's answer about the database's size.
func (s serverScreen) withSize(msg dbSized) serverScreen {
	s.dbSize, s.dbKnown, s.dbErr = msg.size, msg.err == nil, ""
	if msg.err != nil {
		s.dbErr = kuma.Brief(msg.err)
	}
	return s
}

// rowCount is how many rows the tab lists; the database tab has none to
// move over.
func (s serverScreen) rowCount(t serverTab, st state.Instance) int {
	switch t {
	case tabKeys:
		return len(st.APIKeys)
	case tabProxies:
		return len(st.Proxies)
	case tabDocker:
		return len(st.DockerHosts)
	}
	return 0
}

// row is the cursor of the tab, kept inside its list: Kuma's pushes can
// shorten it under the cursor.
func (s serverScreen) row(t serverTab, st state.Instance) int {
	return min(s.cursor[t], max(s.rowCount(t, st)-1, 0))
}

func (s serverScreen) selectedKey(st state.Instance) (kuma.APIKey, bool) {
	if len(st.APIKeys) == 0 {
		return kuma.APIKey{}, false
	}
	return st.APIKeys[s.row(tabKeys, st)], true
}

func (s serverScreen) selectedProxy(st state.Instance) (kuma.Proxy, bool) {
	if len(st.Proxies) == 0 {
		return kuma.Proxy{}, false
	}
	return st.Proxies[s.row(tabProxies, st)], true
}

func (s serverScreen) selectedDocker(st state.Instance) (kuma.DockerHost, bool) {
	if len(st.DockerHosts) == 0 {
		return kuma.DockerHost{}, false
	}
	return st.DockerHosts[s.row(tabDocker, st)], true
}

// Update moves between the tabs and the rows, and says what a key asks for
// on the tab it was pressed on. Opening the database tab asks for its size.
func (s serverScreen) Update(msg tea.KeyMsg, st state.Instance) (serverScreen, serverAction) {
	for t := range serverTab(serverTabs) {
		s.cursor[t] = s.row(t, st)
	}
	to := s.tab
	switch {
	case msg.Type == tea.KeyTab:
		to = (s.tab + 1) % serverTabs
	case msg.Type == tea.KeyShiftTab:
		to = (s.tab + serverTabs - 1) % serverTabs
	case len(msg.Runes) == 1 && msg.Runes[0] >= '1' && msg.Runes[0] <= '4':
		to = serverTab(msg.Runes[0] - '1')
	}
	if to != s.tab {
		s.tab = to
		if to == tabDatabase {
			return s, srvRefresh
		}
		return s, srvNone
	}
	n := s.rowCount(s.tab, st)
	switch {
	case key.Matches(msg, keys.Back):
		return s, srvBack
	case key.Matches(msg, keys.Up):
		if s.cursor[s.tab] > 0 {
			s.cursor[s.tab]--
		}
		return s, srvNone
	case key.Matches(msg, keys.Down):
		if s.cursor[s.tab] < n-1 {
			s.cursor[s.tab]++
		}
		return s, srvNone
	}
	k := msg.String()
	switch s.tab {
	case tabKeys, tabProxies, tabDocker:
		switch {
		case k == "n":
			return s, srvNew
		case n == 0:
		case k == "d":
			return s, srvDelete
		case s.tab == tabKeys && msg.Type == tea.KeySpace:
			return s, srvToggle
		case s.tab != tabKeys && k == "e":
			return s, srvEdit
		case s.tab == tabDocker && k == "t":
			return s, srvTest
		}
	case tabDatabase:
		switch k {
		case "s":
			return s, srvShrink
		case "X":
			return s, srvClear
		case "r":
			return s, srvRefresh
		}
	}
	return s, srvNone
}

// View draws the heading, the tab bar and the open tab.
func (s serverScreen) View(name string, st state.Instance, width, height int) string {
	var b strings.Builder
	b.WriteString(styleHeading.Render(name+" · server") + "\n\n")
	for t := range serverTab(serverTabs) {
		label := " " + strconv.Itoa(int(t)+1) + " " + t.String() + " "
		if t == s.tab {
			b.WriteString(styleRow.Render(label))
		} else {
			b.WriteString(styleLabel.Render(label))
		}
		b.WriteString(" ")
	}
	b.WriteString("\n\n")
	var lines []string
	switch s.tab {
	case tabKeys:
		lines = s.keysView(st, width)
	case tabProxies:
		lines = s.proxiesView(st, width)
	case tabDocker:
		lines = s.dockerView(name, st, width)
	default:
		lines = s.databaseView()
	}
	// The rows the terminal holds, keeping the cursor's in sight; the
	// heading and the tab bar take four lines.
	if rows := height - 4; rows > 0 && len(lines) > rows {
		start := max(s.row(s.tab, st)-rows+1, 0)
		lines = lines[start:min(start+rows, len(lines))]
	}
	for _, l := range lines {
		b.WriteString(l + "\n")
	}
	return b.String()
}

// listRow draws one row of a tab: highlighted under the cursor, else in its
// own colours.
func listRow(plain, styled string, selected bool, width int) string {
	if selected {
		return styleRow.Render(" " + truncate(plain, width-2) + " ")
	}
	return " " + styled
}

func (s serverScreen) keysView(st state.Instance, width int) []string {
	if len(st.APIKeys) == 0 {
		return []string{styleLabel.Render("no API keys: n makes one for Prometheus or Grafana")}
	}
	// The id tells apart keys of one name, and the one-time screen and
	// its flashes name keys by it.
	const nameW, statusW, expW = 32, 9, 26
	cursor := s.row(tabKeys, st)
	out := make([]string, 0, len(st.APIKeys))
	for i, k := range st.APIKeys {
		status, style := "inactive", styleLabel
		switch {
		case k.Status == "expired":
			status, style = "expired", styleWarn
		case k.Active:
			status, style = "active", styleOK
		}
		exp := "never expires"
		if k.Expires != "" {
			exp = "expires " + kumaTime(k.Expires, 16)
		}
		made := "made " + kumaTime(k.Created, 10)
		id := fmt.Sprintf(" (id %d)", k.ID)
		n := padRight(truncate(k.Name, nameW-len(id))+id, nameW)
		status = padRight(status, statusW)
		exp = padRight(exp, expW)
		plain := n + " " + status + " " + exp + " " + made
		styled := styleValue.Render(n) + " " + style.Render(status) + " " + styleLabel.Render(exp) + " " + styleLabel.Render(made)
		out = append(out, listRow(plain, styled, i == cursor, width))
	}
	return out
}

func (s serverScreen) proxiesView(st state.Instance, width int) []string {
	if len(st.Proxies) == 0 {
		return []string{styleLabel.Render("no proxies")}
	}
	cursor := s.row(tabProxies, st)
	out := make([]string, 0, len(st.Proxies))
	for i, p := range st.Proxies {
		// The password is Kuma's to keep: it is never drawn.
		addr := fmt.Sprintf("%s://%s:%d", p.Protocol, p.Host, p.Port)
		var extra []string
		if p.Default {
			extra = append(extra, "default")
		}
		if p.Auth {
			extra = append(extra, "auth "+p.Username)
		}
		note := strings.Join(extra, "  ")
		plain := addr + "  " + note
		styled := styleValue.Render(addr) + "  " + styleLabel.Render(note)
		out = append(out, listRow(plain, styled, i == cursor, width))
	}
	return out
}

func (s serverScreen) dockerView(name string, st state.Instance, width int) []string {
	if len(st.DockerHosts) == 0 {
		return []string{
			styleLabel.Render("no Docker hosts"),
			styleLabel.Render("docker monitors take a host's id in the field editor (r)"),
		}
	}
	cursor := s.row(tabDocker, st)
	out := make([]string, 0, len(st.DockerHosts))
	for i, d := range st.DockerHosts {
		// The id is shown because a docker monitor names its host by id in
		// the field editor.
		id := fmt.Sprintf("(id %d)", d.ID)
		plain := d.Name + "  " + id + "  " + d.Type + "  " + d.Daemon
		styled := styleValue.Render(d.Name) + "  " + styleLabel.Render(id) + "  " + styleValue.Render(d.Type) + "  " + styleLabel.Render(d.Daemon)
		if s.test.running() && s.test.instance == name && s.test.listID == d.ID {
			// Kuma takes up to six seconds to give up on a daemon.
			plain = "testing…  " + plain
			styled = styleWarn.Render("testing…") + "  " + styled
		}
		out = append(out, listRow(plain, styled, i == cursor, width))
	}
	return out
}

func (s serverScreen) databaseView() []string {
	size := styleLabel.Render("asking Kuma…")
	switch {
	case s.dbErr != "":
		size = styleErr.Render(s.dbErr)
	case s.dbKnown && s.dbSize == 0:
		size = styleValue.Render("not reported (MariaDB)")
	case s.dbKnown:
		size = styleValue.Render(humanBytes(s.dbSize))
	}
	return []string{
		" " + styleLabel.Render("size  ") + size,
		"",
		// One key a line, so none is cut on a narrow terminal.
		" " + styleLabel.Render("s shrinks the SQLite file"),
		" " + styleLabel.Render("X clears every monitor's uptime statistics"),
		" " + styleLabel.Render("r asks the size again"),
	}
}

// kumaTime cuts a time Kuma stores, "2026-09-10 23:59:00", to its first n
// characters: the minute, or the day.
func kumaTime(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// humanBytes is a size in powers of 1024, with one decimal from KiB up.
func humanBytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	v := float64(n)
	unit := ""
	for _, u := range []string{"KiB", "MiB", "GiB"} {
		v, unit = v/1024, u
		if v < 1024 {
			break
		}
	}
	return fmt.Sprintf("%.1f %s", v, unit)
}

// apiKeyMade is Kuma's answer to a new API key. It carries the key's secret,
// which Kuma never shows again: the message is the only place it lives until
// the screen that shows it closes.
type apiKeyMade struct {
	instance, name, key string
	id                  int
	err                 error
}

func addAPIKey(in *core.Instance, name, expires string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		k, id, err := in.AddAPIKey(ctx, name, expires)
		return apiKeyMade{instance: in.Name(), name: name, key: k, id: id, err: err}
	}
}

func setAPIKeyActive(in *core.Instance, k kuma.APIKey, active bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		action := "disabled"
		if active {
			action = "enabled"
		}
		return actionDone{name: in.Name(), action: action, mon: "API key " + k.Name, err: in.SetAPIKeyActive(ctx, k.ID, active)}
	}
}

func deleteAPIKey(in *core.Instance, k kuma.APIKey) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		return actionDone{name: in.Name(), action: "deleted", mon: "API key " + k.Name, err: in.DeleteAPIKey(ctx, k.ID)}
	}
}

func shrinkDatabase(in *core.Instance) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		return actionDone{name: in.Name(), action: "shrank", mon: "the database", err: in.ShrinkDatabase(ctx)}
	}
}

// clearStatistics asks Kuma to delete every monitor's statistics. Kuma
// then restarts the active monitors but sends nothing to replace what a
// client already holds (its own web page reloads itself after a clear): the
// history lazykuma has stays on screen, and only new checks come in on top
// of it. Its success says so.
func clearStatistics(in *core.Instance) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		return actionDone{name: in.Name(), action: "cleared", mon: "all statistics of " + in.Name(), err: in.ClearStatistics(ctx)}
	}
}
