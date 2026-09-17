package state

import (
	"sort"
	"strings"
)

// Show narrows the list to monitors in one state.
type Show int

const (
	ShowAll Show = iota
	ShowDown
	ShowUp
	ShowPaused
	ShowMaintenance
)

func (s Show) String() string {
	return [...]string{"all", "down", "up", "paused", "maintenance"}[s]
}

// Next is the filter after s, back to all after the last.
func (s Show) Next() Show { return (s + 1) % 5 }

func (s Show) keeps(st Status) bool {
	switch s {
	case ShowDown:
		return st == StatusDown
	case ShowUp:
		return st == StatusUp
	case ShowPaused:
		return st == StatusPaused
	case ShowMaintenance:
		return st == StatusMaintenance
	}
	return true
}

// Order is how siblings are sorted.
type Order int

const (
	OrderStatus Order = iota // down first, then by name
	OrderName
	OrderPing   // slowest first
	OrderUptime // worst 24 h uptime first
)

func (o Order) String() string {
	return [...]string{"status", "name", "ping", "uptime"}[o]
}

// Next is the order after o, back to status after the last.
func (o Order) Next() Order { return (o + 1) % 4 }

// View is what the list is asked to show.
type View struct {
	Query  string       // matched against name, target, tag names and values
	Show   Show         // which states to keep
	Order  Order        // how siblings are sorted
	Folded map[int]bool // groups whose children are hidden, by id
}

// Filtering reports whether the view hides monitors by query or state.
// While it does, groups are shown open, so a match is never hidden in a
// folded group.
func (v View) Filtering() bool { return strings.TrimSpace(v.Query) != "" || v.Show != ShowAll }

// Row is one line of the monitor list.
type Row struct {
	Monitor
	Depth    int  // 0 at the top level
	Group    bool // a group: its children follow, unless Folded
	Folded   bool
	Children int    // for a group, the monitors under it at any depth, groups not counted
	Rollup   Status // for a group, its worst monitor's status; otherwise the monitor's
}

// severity ranks statuses for the rollup and the status order: lower is
// worse.
func severity(s Status) int {
	switch s {
	case StatusDown:
		return 0
	case StatusPending:
		return 1
	case StatusMaintenance:
		return 2
	case StatusUp:
		return 3
	case StatusUnknown:
		return 4
	}
	return 5 // paused
}

// tree is the parent/child structure of an instance's monitors.
type tree struct {
	in       Instance
	children map[int][]int // group id → child ids
	top      []int
}

// buildTree places every monitor exactly once. A monitor goes to the top
// level when it has no parent, or its own parent is missing or not a group,
// or its parent chain loops back to it; Kuma should never send the last two,
// but a list that hides a monitor would be worse than one that misplaces it.
// Only the direct parent counts: a subgroup whose group is gone goes to the
// top level itself, and what is in it stays in it.
func (in Instance) buildTree() tree {
	t := tree{in: in, children: map[int][]int{}}
	for id, m := range in.Monitors {
		if t.underGroup(m) {
			t.children[m.Parent] = append(t.children[m.Parent], id)
			continue
		}
		t.top = append(t.top, id)
	}
	return t
}

// underGroup reports whether m shows under its parent. In a loop every
// monitor on it goes to the top level, which breaks it; a monitor whose
// chain only runs into a loop stays under its parent, which is shown.
func (t tree) underGroup(m Monitor) bool {
	if parent, ok := t.in.Monitors[m.Parent]; m.Parent == 0 || !ok || !parent.IsGroup() {
		return false
	}
	seen := map[int]bool{}
	for p := m.Parent; p != 0 && !seen[p]; {
		if p == m.ID {
			return false
		}
		parent, ok := t.in.Monitors[p]
		if !ok || !parent.IsGroup() {
			break
		}
		seen[p] = true
		p = parent.Parent
	}
	return true
}

// monitorsUnder is every non-group monitor below id.
func (t tree) monitorsUnder(id int) []Monitor {
	var out []Monitor
	for _, c := range t.children[id] {
		m := t.in.Monitors[c]
		if m.IsGroup() {
			out = append(out, t.monitorsUnder(c)...)
			continue
		}
		out = append(out, m)
	}
	return out
}

// Descendants is every monitor below a group, groups included, at any depth.
func (in Instance) Descendants(id int) []Monitor {
	t := in.buildTree()
	var out []Monitor
	var walk func(int)
	walk = func(g int) {
		for _, c := range t.children[g] {
			out = append(out, in.Monitors[c])
			walk(c)
		}
	}
	walk(id)
	return out
}

func (t tree) rollup(m Monitor) (Status, int) {
	if !m.IsGroup() {
		return m.Status(), 0
	}
	under := t.monitorsUnder(m.ID)
	if !m.Active {
		return StatusPaused, len(under)
	}
	if len(under) == 0 {
		return m.Status(), 0
	}
	worst := under[0].Status()
	for _, c := range under[1:] {
		if severity(c.Status()) < severity(worst) {
			worst = c.Status()
		}
	}
	return worst, len(under)
}

// Tree is the monitor list as the instance screen shows it: groups first,
// each followed by what is in it, then the monitors in no group.
func (in Instance) Tree(v View) []Row {
	t := in.buildTree()
	q := strings.ToLower(strings.TrimSpace(v.Query))
	filtering := v.Filtering()

	var rows []Row
	var walk func(ids []int, depth int, groupMatched bool)
	walk = func(ids []int, depth int, groupMatched bool) {
		for _, id := range t.sorted(ids, v.Order) {
			m := in.Monitors[id]
			status, n := t.rollup(m)
			if !m.IsGroup() {
				if (groupMatched || matches(m, q)) && v.Show.keeps(status) {
					rows = append(rows, Row{Monitor: m, Depth: depth, Rollup: status})
				}
				continue
			}
			selfMatch := q != "" && strings.Contains(strings.ToLower(m.Name), q)
			folded := v.Folded[id] && !filtering
			mark := len(rows)
			rows = append(rows, Row{Monitor: m, Depth: depth, Group: true, Folded: folded, Children: n, Rollup: status})
			if folded {
				continue
			}
			before := len(rows)
			walk(t.children[id], depth+1, groupMatched || selfMatch)
			if filtering && len(rows) == before && !(selfMatch && v.Show == ShowAll) {
				rows = rows[:mark] // nothing in it is shown: neither is the group
			}
		}
	}
	walk(t.top, 0, false)
	return rows
}

func matches(m Monitor, q string) bool {
	if q == "" {
		return true
	}
	if strings.Contains(strings.ToLower(m.Name), q) || strings.Contains(strings.ToLower(m.Target()), q) {
		return true
	}
	for _, tag := range m.Tags {
		if strings.Contains(strings.ToLower(tag.Name), q) || (tag.Value != "" && strings.Contains(strings.ToLower(tag.Value), q)) {
			return true
		}
	}
	return false
}

// sorted orders siblings: groups before monitors, each by the view's order,
// ties by name and then id so the list never jumps between two frames.
func (t tree) sorted(ids []int, o Order) []int {
	out := append([]int(nil), ids...)
	type key struct {
		group    bool
		severity int
		ping     float64
		hasPing  bool
		uptime   float64
		hasUp    bool
		name     string
		id       int
	}
	keys := make(map[int]key, len(out))
	for _, id := range out {
		m := t.in.Monitors[id]
		st, _ := t.rollup(m)
		k := key{group: m.IsGroup(), severity: severity(st), name: strings.ToLower(m.Name), id: id}
		k.ping, k.hasPing = t.ping(m)
		k.uptime, k.hasUp = t.uptime(m)
		keys[id] = k
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := keys[out[i]], keys[out[j]]
		if a.group != b.group {
			return a.group
		}
		switch o {
		case OrderStatus:
			if a.severity != b.severity {
				return a.severity < b.severity
			}
		case OrderPing:
			if a.hasPing != b.hasPing {
				return a.hasPing
			}
			if a.ping != b.ping {
				return a.ping > b.ping
			}
		case OrderUptime:
			if a.hasUp != b.hasUp {
				return a.hasUp
			}
			if a.uptime != b.uptime {
				return a.uptime < b.uptime
			}
		}
		if a.name != b.name {
			return a.name < b.name
		}
		return a.id < b.id
	})
	return out
}

// ping is a monitor's last ping, or a group's slowest monitor's.
func (t tree) ping(m Monitor) (float64, bool) {
	if !m.IsGroup() {
		b, ok := m.Last()
		return b.Ping, ok && b.HasPing
	}
	var worst float64
	found := false
	for _, c := range t.monitorsUnder(m.ID) {
		if p, ok := t.ping(c); ok && (!found || p > worst) {
			worst, found = p, true
		}
	}
	return worst, found
}

// uptime is a monitor's 24 h uptime, or a group's worst monitor's.
func (t tree) uptime(m Monitor) (float64, bool) {
	if !m.IsGroup() {
		return m.Uptime24, m.HasUptime
	}
	var worst float64
	found := false
	for _, c := range t.monitorsUnder(m.ID) {
		if u, ok := t.uptime(c); ok && (!found || u < worst) {
			worst, found = u, true
		}
	}
	return worst, found
}
