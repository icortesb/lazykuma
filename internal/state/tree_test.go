package state

import (
	"strings"
	"testing"
	"time"

	"github.com/icortesb/lazykuma/internal/kuma"
)

var tTree = time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)

// shop is two projects and a loose monitor:
//
//	Shop (group)            web up 120ms, api down
//	Blog (group)            blog up 90ms, paused-thing paused
//	  Blog/Assets (group)   cdn up 300ms
//	status-page up 50ms     (no group)
func shop() Instance {
	in := Apply(Instance{}, kuma.Connected{}, tTree)
	in = Apply(in, kuma.MonitorList{Monitors: map[int]kuma.Monitor{
		1: {ID: 1, Name: "Shop", Type: "group", Active: true},
		2: {ID: 2, Name: "web", Type: "http", URL: "https://shop.example.com", Parent: 1, Active: true,
			Tags: []kuma.Tag{{ID: 4, Name: "prod", Color: "#DC2626"}}},
		3: {ID: 3, Name: "api", Type: "http", URL: "https://api.example.com", Parent: 1, Active: true,
			Tags: []kuma.Tag{{ID: 5, Name: "region", Color: "#2563EB", Value: "eu"}}},
		10: {ID: 10, Name: "Blog", Type: "group", Active: true},
		11: {ID: 11, Name: "blog", Type: "http", URL: "https://blog.example.com", Parent: 10, Active: true},
		12: {ID: 12, Name: "paused-thing", Type: "ping", Hostname: "10.0.0.9", Parent: 10},
		13: {ID: 13, Name: "Assets", Type: "group", Parent: 10, Active: true},
		14: {ID: 14, Name: "cdn", Type: "http", URL: "https://cdn.example.com", Parent: 13, Active: true},
		20: {ID: 20, Name: "status-page", Type: "http", URL: "https://status.example.com", Active: true},
	}}, tTree)
	beat := func(id int, st kuma.Status, ping float64) {
		in = Apply(in, kuma.Heartbeat{Beat: kuma.Beat{MonitorID: id, Status: st, Ping: ping, HasPing: ping > 0, Time: tTree}}, tTree)
	}
	beat(2, kuma.StatusUp, 120)
	beat(3, kuma.StatusDown, 0)
	beat(11, kuma.StatusUp, 90)
	beat(14, kuma.StatusUp, 300)
	beat(20, kuma.StatusUp, 50)
	in = Apply(in, kuma.Uptime{MonitorID: 11, Period: "24", Ratio: 0.5}, tTree)
	in = Apply(in, kuma.Uptime{MonitorID: 2, Period: "24", Ratio: 0.99}, tTree)
	return in
}

// outline draws rows as "  name" indented by depth, with ▾/▸ on groups.
func outline(rows []Row) string {
	var b strings.Builder
	for _, r := range rows {
		b.WriteString(strings.Repeat("  ", r.Depth))
		if r.Group {
			if r.Folded {
				b.WriteString("▸ ")
			} else {
				b.WriteString("▾ ")
			}
		}
		b.WriteString(r.Name + "\n")
	}
	return b.String()
}

func TestTreeGroupsFirstThenLoose(t *testing.T) {
	got := outline(shop().Tree(View{}))
	// Status order: groups by their worst monitor (Shop has a down one),
	// then monitors down first, then by name.
	want := "▾ Shop\n  api\n  web\n▾ Blog\n  ▾ Assets\n    cdn\n  blog\n  paused-thing\nstatus-page\n"
	if got != want {
		t.Fatalf("tree:\n%s\nwant:\n%s", got, want)
	}
}

func TestTreeRollupAndChildren(t *testing.T) {
	rows := shop().Tree(View{})
	byName := map[string]Row{}
	for _, r := range rows {
		byName[r.Name] = r
	}
	if r := byName["Shop"]; !r.Group || r.Children != 2 || r.Rollup != StatusDown {
		t.Errorf("Shop = group %v, %d children, rollup %v", r.Group, r.Children, r.Rollup)
	}
	// Blog holds blog, paused-thing and, through Assets, cdn: three monitors.
	if r := byName["Blog"]; r.Children != 3 || r.Rollup != StatusUp {
		t.Errorf("Blog = %d children, rollup %v", r.Children, r.Rollup)
	}
	if r := byName["web"]; r.Group || r.Rollup != StatusUp || r.Depth != 1 {
		t.Errorf("web = %+v", r)
	}
}

func TestTreeFolding(t *testing.T) {
	got := outline(shop().Tree(View{Folded: map[int]bool{10: true}}))
	want := "▾ Shop\n  api\n  web\n▸ Blog\nstatus-page\n"
	if got != want {
		t.Fatalf("folded:\n%s\nwant:\n%s", got, want)
	}
}

func TestTreeQuery(t *testing.T) {
	in := shop()
	for _, tt := range []struct{ q, want string }{
		// A match inside a group keeps the group, unfolded, around it.
		{"cdn", "▾ Blog\n  ▾ Assets\n    cdn\n"},
		// Tag names and values match too.
		{"prod", "▾ Shop\n  web\n"},
		{"eu", "▾ Shop\n  api\n"},
		// Targets match.
		{"status.example", "status-page\n"},
		// A group's own name brings all of it.
		{"shop", "▾ Shop\n  api\n  web\n"},
	} {
		got := outline(in.Tree(View{Query: tt.q, Folded: map[int]bool{1: true, 10: true, 13: true}}))
		if got != tt.want {
			t.Errorf("query %q:\n%s\nwant:\n%s", tt.q, got, tt.want)
		}
	}
}

func TestTreeShow(t *testing.T) {
	in := shop()
	if got := outline(in.Tree(View{Show: ShowDown})); got != "▾ Shop\n  api\n" {
		t.Errorf("down:\n%s", got)
	}
	if got := outline(in.Tree(View{Show: ShowPaused})); got != "▾ Blog\n  paused-thing\n" {
		t.Errorf("paused:\n%s", got)
	}
}

func TestTreeOrders(t *testing.T) {
	in := shop()
	// By ping, slowest first, at every level; a monitor without a ping last.
	if got := outline(in.Tree(View{Order: OrderPing})); got != "▾ Blog\n  ▾ Assets\n    cdn\n  blog\n  paused-thing\n▾ Shop\n  web\n  api\nstatus-page\n" {
		t.Errorf("ping:\n%s", got)
	}
	// By uptime, worst first; unknown uptime last.
	if got := outline(in.Tree(View{Order: OrderUptime})); !strings.HasPrefix(got, "▾ Blog\n  ▾ Assets\n    cdn\n  blog\n") {
		t.Errorf("uptime:\n%s", got)
	}
	if got := outline(in.Tree(View{Order: OrderName})); got != "▾ Blog\n  ▾ Assets\n    cdn\n  blog\n  paused-thing\n▾ Shop\n  api\n  web\nstatus-page\n" {
		t.Errorf("name:\n%s", got)
	}
}

func TestTreeSurvivesAParentCycleAndAMissingParent(t *testing.T) {
	in := Apply(Apply(Instance{}, kuma.Connected{}, tTree), kuma.MonitorList{Monitors: map[int]kuma.Monitor{
		1: {ID: 1, Name: "a", Type: "group", Parent: 2, Active: true},
		2: {ID: 2, Name: "b", Type: "group", Parent: 1, Active: true},
		3: {ID: 3, Name: "orphan", Type: "http", Parent: 99, Active: true},
	}}, tTree)
	got := outline(in.Tree(View{}))
	if !strings.Contains(got, "orphan") {
		t.Fatalf("orphan lost:\n%s", got)
	}
	// Every monitor shows exactly once, even in a cycle Kuma should never
	// produce: the cycle is broken at the top level.
	for _, name := range []string{"a", "b", "orphan"} {
		if strings.Count(got, name+"\n") != 1 {
			t.Errorf("%q shows %d times:\n%s", name, strings.Count(got, name+"\n"), got)
		}
	}
}

func TestTreeKeepsWhatIsInASubgroupWhoseGroupIsGone(t *testing.T) {
	in := Apply(Apply(Instance{}, kuma.Connected{}, tTree), kuma.MonitorList{Monitors: map[int]kuma.Monitor{
		// EU was in a group Kuma no longer lists; cdn is in EU.
		4: {ID: 4, Name: "EU", Type: "group", Parent: 99, Active: true},
		5: {ID: 5, Name: "cdn", Type: "http", URL: "https://cdn.example.com", Parent: 4, Active: true},
		// A loop, and a monitor inside it: the loop breaks at the top level,
		// and what only hangs from it stays where it is.
		1: {ID: 1, Name: "a", Type: "group", Parent: 2, Active: true},
		2: {ID: 2, Name: "b", Type: "group", Parent: 1, Active: true},
		3: {ID: 3, Name: "inner", Type: "http", URL: "https://inner.example.com", Parent: 1, Active: true},
	}}, tTree)
	if got := outline(in.Tree(View{Order: OrderName})); got != "▾ a\n  inner\n▾ b\n▾ EU\n  cdn\n" {
		t.Errorf("tree:\n%s", got)
	}
}

func TestDescendants(t *testing.T) {
	got := map[int]bool{}
	for _, m := range shop().Descendants(10) {
		got[m.ID] = true
	}
	if len(got) != 4 || !got[11] || !got[12] || !got[13] || !got[14] {
		t.Fatalf("Descendants(10) = %v", got)
	}
}

func TestCountsSkipGroups(t *testing.T) {
	c := shop().Counts()
	if c.Total != 6 || c.Up != 4 || c.Down != 1 || c.Paused != 1 {
		t.Fatalf("counts = %+v", c)
	}
}
