//go:build integration

package kuma

import (
	"context"
	"testing"
	"time"
)

// TestIntegrationGroupsAndTags checks, on a real Kuma, the group and tag
// facts phase A is built on: a child names its group in parent, editMonitor
// moves it, tags come back on the monitor, a clone is accepted by add, and
// deleting a group can keep its monitors.
func TestIntegrationGroupsAndTags(t *testing.T) {
	url := itEnv("LAZYKUMA_IT_URL", "http://localhost:3902")
	user := itEnv("LAZYKUMA_IT_USER", "admin")
	pass := itEnv("LAZYKUMA_IT_PASS", "lazykuma-it-Passw0rd")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	s0, err := Dial(ctx, url)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	s0.call(ctx, "setup", user, pass) // not ok once a user exists: fine
	s0.Close()
	token, err := Login(ctx, url, user, pass, "")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	s, err := Dial(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	// Registered first so it runs last (t.Cleanup is LIFO): every other
	// cleanup below needs s open to delete what it made.
	t.Cleanup(func() { s.Close() })
	if err := s.LoginByToken(ctx, token); err != nil {
		t.Fatal(err)
	}

	groupID, err := s.AddMonitor(ctx, NewGroup("it-group"))
	if err != nil {
		t.Fatalf("add group: %v", err)
	}
	t.Cleanup(func() {
		// The test itself deletes the group further down; a leftover
		// "already deleted" complaint here is fine. What matters is that a
		// t.Fatal before that point does not strand it, or the monitors it
		// still holds, against a Kuma that outlives this test.
		cctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := s.DeleteGroup(cctx, groupID, false); err != nil {
			t.Logf("cleanup: delete group %d: %v", groupID, err)
		}
	})
	childID, err := s.AddMonitor(ctx, RawMonitor{
		"type": "http", "name": "it-child", "url": "https://example.com", "method": "GET",
		"interval": 60, "retryInterval": 60, "maxretries": 0,
		"accepted_statuscodes": []string{"200-299"}, "notificationIDList": map[string]bool{},
		"conditions": []any{}, "kafkaProducerBrokers": []any{}, "kafkaProducerSaslOptions": map[string]any{},
	})
	if err != nil {
		t.Fatalf("add child: %v", err)
	}
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := s.DeleteMonitor(cctx, childID); err != nil {
			t.Logf("cleanup: delete child %d: %v", childID, err)
		}
	})

	// Move the child into the group the way the UI does.
	child, err := s.GetMonitor(ctx, childID)
	if err != nil {
		t.Fatal(err)
	}
	child["parent"] = float64(groupID)
	if err := s.EditMonitor(ctx, child); err != nil {
		t.Fatalf("move: %v", err)
	}

	tag, err := s.AddTag(ctx, "it-tag", TagColors[4].Hex)
	if err != nil {
		t.Fatalf("add tag: %v", err)
	}
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := s.DeleteTag(cctx, tag.ID); err != nil {
			t.Logf("cleanup: delete tag %d: %v", tag.ID, err)
		}
	})
	if err := s.AddMonitorTag(ctx, tag.ID, childID, "eu"); err != nil {
		t.Fatalf("tag the child: %v", err)
	}
	tags, err := s.Tags(ctx)
	if err != nil || len(tags) == 0 {
		t.Fatalf("Tags = %v, %v", tags, err)
	}

	// Clone: add must accept what ForClone leaves.
	full, err := s.GetMonitor(ctx, childID)
	if err != nil {
		t.Fatal(err)
	}
	cloneID, err := s.AddMonitor(ctx, ForClone(full))
	if err != nil {
		t.Fatalf("clone: %v", err)
	}
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := s.DeleteMonitor(cctx, cloneID); err != nil {
			t.Logf("cleanup: delete clone %d: %v", cloneID, err)
		}
	})

	// A fresh session's monitor list shows the tree and the tag.
	events := make(chan Event, 1024)
	sup := NewSupervisor(url, func() string { return token }, func(ev Event) { events <- ev })
	sctx, stop := context.WithCancel(ctx)
	defer stop()
	go sup.Run(sctx)
	var list map[int]Monitor
	for list == nil {
		select {
		case ev := <-events:
			if ml, ok := ev.(MonitorList); ok {
				list = ml.Monitors
			}
		case <-ctx.Done():
			t.Fatal("no monitor list")
		}
	}
	if !list[groupID].IsGroup() || list[childID].Parent != groupID || list[cloneID].Parent != groupID {
		t.Fatalf("tree: group %+v child %+v clone %+v", list[groupID], list[childID], list[cloneID])
	}
	if got := list[childID].Tags; len(got) != 1 || got[0].ID != tag.ID || got[0].Value != "eu" {
		t.Fatalf("child tags = %+v", got)
	}

	// Delete the group, keeping its monitors: they become loose.
	if err := s.DeleteGroup(ctx, groupID, false); err != nil {
		t.Fatalf("delete group: %v", err)
	}
	after, err := s.GetMonitor(ctx, childID)
	if err != nil {
		t.Fatalf("the child went with its group: %v", err)
	}
	if after["parent"] != nil {
		t.Fatalf("child parent after delete = %v", after["parent"])
	}
	// The rest of the cleanup (child, clone, tag, and a no-op retry of the
	// group delete above) runs through t.Cleanup, registered next to each
	// object's creation, so it still runs if an assertion above t.Fatals.
}
