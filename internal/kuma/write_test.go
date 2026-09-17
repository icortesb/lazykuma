package kuma

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// writeFake answers the write calls the way a real Kuma 2.5.3 does, as
// captured by probing one: replies, ids, and its refusal of partial edits.
func writeFake(t *testing.T) (*fakeKuma, *Session) {
	t.Helper()
	monitors := map[int]RawMonitor{}
	next := 0
	login := kumaLogin(false)
	f := newFakeKuma(t, func(event string, args []json.RawMessage) any {
		switch event {
		case "add":
			var m RawMonitor
			json.Unmarshal(args[0], &m)
			next++
			m["id"] = float64(next)
			monitors[next] = m
			return map[string]any{"ok": true, "msg": "successAdded", "monitorID": next}
		case "getMonitor":
			var id int
			json.Unmarshal(args[0], &id)
			m, ok := monitors[id]
			if !ok {
				return map[string]any{"ok": false, "msg": "Cannot read properties of null (reading 'id')"}
			}
			return map[string]any{"ok": true, "monitor": m}
		case "editMonitor":
			var m RawMonitor
			json.Unmarshal(args[0], &m)
			// Kuma reads fields the whole object is expected to carry; a
			// partial one dies inside its own code.
			if _, ok := m["accepted_statuscodes"]; !ok {
				return map[string]any{"ok": false, "msg": "Cannot read properties of undefined (reading 'every')"}
			}
			id := int(m["id"].(float64))
			monitors[id] = m
			return map[string]any{"ok": true, "msg": "Saved."}
		case "deleteMonitor":
			// Also serves DeleteGroup's three-argument form: the id is
			// args[0] either way, and withMonitors does not matter to this
			// fake, which never tracks group membership.
			var id int
			json.Unmarshal(args[0], &id)
			delete(monitors, id)
			return map[string]any{"ok": true, "msg": "successDeleted"}
		case "getTags":
			return map[string]any{"ok": true, "tags": []map[string]any{{"id": 4, "name": "region", "color": "#2563EB"}}}
		case "addTag":
			var tag map[string]any
			json.Unmarshal(args[0], &tag)
			tag["id"] = 8
			return map[string]any{"ok": true, "tag": tag}
		case "editTag", "deleteTag", "addMonitorTag", "deleteMonitorTag":
			return map[string]any{"ok": true, "msg": "Saved."}
		case "addNotification":
			var id int
			if len(args) > 1 {
				json.Unmarshal(args[1], &id)
			}
			if id == 0 {
				id = 7
			}
			return map[string]any{"ok": true, "msg": "Saved.", "id": id}
		case "deleteNotification":
			return map[string]any{"ok": true, "msg": "successDeleted"}
		case "testNotification":
			return map[string]any{"ok": false, "msg": "Request failed with status code 401"}
		case "addMaintenance":
			return map[string]any{"ok": true, "msg": "successAdded", "maintenanceID": 3}
		case "addMonitorMaintenance", "deleteMaintenance":
			return map[string]any{"ok": true, "msg": "successAdded"}
		}
		return login(event, args)
	})
	s := dial(t, f)
	if err := s.LoginByToken(context.Background(), "jwt"); err != nil {
		t.Fatal(err)
	}
	return f, s
}

func sampleMonitor() RawMonitor {
	return RawMonitor{
		"type": "http", "name": "web", "url": "https://example.com", "method": "GET",
		"interval": 60, "retryInterval": 60, "maxretries": 0,
		"accepted_statuscodes": []string{"200-299"}, "notificationIDList": map[string]bool{},
	}
}

func TestMonitorLifecycle(t *testing.T) {
	_, s := writeFake(t)
	ctx := context.Background()

	id, err := s.AddMonitor(ctx, sampleMonitor())
	if err != nil || id != 1 {
		t.Fatalf("AddMonitor = %d, %v", id, err)
	}

	full, err := s.GetMonitor(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if full["name"] != "web" || full["id"] != float64(1) {
		t.Fatalf("GetMonitor = %v", full)
	}

	full["name"] = "renamed"
	if err := s.EditMonitor(ctx, full); err != nil {
		t.Fatalf("EditMonitor: %v", err)
	}
	again, _ := s.GetMonitor(ctx, id)
	if again["name"] != "renamed" {
		t.Fatalf("edit did not stick: %v", again["name"])
	}

	if err := s.DeleteMonitor(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetMonitor(ctx, id); err == nil {
		t.Fatal("GetMonitor on a deleted monitor = nil error")
	}
}

func TestEditMonitorNeedsTheWholeMonitor(t *testing.T) {
	_, s := writeFake(t)
	ctx := context.Background()
	id, _ := s.AddMonitor(ctx, sampleMonitor())

	// No id at all: caught before it reaches Kuma.
	if err := s.EditMonitor(ctx, RawMonitor{"name": "x"}); err == nil {
		t.Fatal("EditMonitor without an id = nil error")
	}
	// An id but not the rest: Kuma's own refusal reaches the caller.
	err := s.EditMonitor(ctx, RawMonitor{"id": float64(id), "name": "x", "type": "http"})
	var r *ReplyError
	if !errors.As(err, &r) {
		t.Fatalf("partial edit = %v, want Kuma's refusal", err)
	}
}

func TestNotificationWrites(t *testing.T) {
	f, s := writeFake(t)
	ctx := context.Background()
	cfg := map[string]any{"name": "tg", "type": "telegram", "isDefault": true, "telegramBotToken": "123:abc", "telegramChatID": "42"}

	id, err := s.SaveNotification(ctx, cfg, 0)
	if err != nil || id != 7 {
		t.Fatalf("SaveNotification(new) = %d, %v", id, err)
	}
	if id, err := s.SaveNotification(ctx, cfg, 4); err != nil || id != 4 {
		t.Fatalf("SaveNotification(edit) = %d, %v", id, err)
	}
	if !f.Sent(`["addNotification",{"isDefault":true,"name":"tg","telegramBotToken":"123:abc","telegramChatID":"42","type":"telegram"},null]`) {
		t.Fatalf("a new channel must send a null id; frames: %v", f.Frames())
	}
	if err := s.DeleteNotification(ctx, 7); err != nil {
		t.Fatal(err)
	}
	// A test send carries the provider's complaint back.
	err = s.TestNotification(ctx, cfg)
	var r *ReplyError
	if !errors.As(err, &r) || r.Msg == "" {
		t.Fatalf("TestNotification = %v, want the provider's message", err)
	}
}

func TestTagWrites(t *testing.T) {
	f, s := writeFake(t)
	ctx := context.Background()

	tags, err := s.Tags(ctx)
	if err != nil || len(tags) != 1 || tags[0] != (TagDef{ID: 4, Name: "region", Color: "#2563EB"}) {
		t.Fatalf("Tags = %+v, %v", tags, err)
	}
	added, err := s.AddTag(ctx, "prod", "#DC2626")
	if err != nil || added.ID != 8 || added.Name != "prod" {
		t.Fatalf("AddTag = %+v, %v", added, err)
	}
	if !f.Sent(`["addTag",{"color":"#DC2626","name":"prod"}]`) {
		t.Errorf("addTag frame: %v", f.Frames())
	}
	if err := s.EditTag(ctx, TagDef{ID: 8, Name: "production", Color: "#DC2626"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMonitorTag(ctx, 8, 2, "eu"); err != nil {
		t.Fatal(err)
	}
	if !f.Sent(`["addMonitorTag",8,2,"eu"]`) {
		t.Errorf("addMonitorTag frame: %v", f.Frames())
	}
	if err := s.DeleteMonitorTag(ctx, 8, 2, "eu"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTag(ctx, 8); err != nil {
		t.Fatal(err)
	}
}

func TestGroupWrites(t *testing.T) {
	f, s := writeFake(t)
	ctx := context.Background()
	id, err := s.AddMonitor(ctx, NewGroup("Shop"))
	if err != nil {
		t.Fatal(err)
	}
	if !f.Sent(`"type":"group"`) || !f.Sent(`"name":"Shop"`) {
		t.Fatalf("group not sent: %v", f.Frames())
	}
	if err := s.DeleteGroup(ctx, id, false); err != nil {
		t.Fatal(err)
	}
	if !f.Sent(fmt.Sprintf(`["deleteMonitor",%d,false]`, id)) {
		t.Errorf("keep-children delete: %v", f.Frames())
	}
	if err := s.DeleteGroup(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	if !f.Sent(fmt.Sprintf(`["deleteMonitor",%d,true]`, id)) {
		t.Errorf("delete-children delete: %v", f.Frames())
	}
}

func TestForCloneDropsWhatAddCannotStore(t *testing.T) {
	src := RawMonitor{
		"id": float64(3), "name": "web", "type": "push", "pushToken": "abc", "parent": float64(1),
		"includeSensitiveData": true, "maintenance": false, "childrenIDs": []any{}, "forceInactive": false,
		"path": []any{"web"}, "pathName": "web", "screenshot": nil, "tags": []any{},
		"notificationIDList": map[string]any{"5": true},
	}
	got := ForClone(src)
	for _, k := range []string{"id", "includeSensitiveData", "maintenance", "childrenIDs", "forceInactive", "path", "pathName", "screenshot", "tags"} {
		if _, ok := got[k]; ok {
			t.Errorf("clone keeps %q", k)
		}
	}
	if got["name"] != "copy of web" || got["parent"] != float64(1) || got["notificationIDList"] == nil {
		t.Errorf("clone = %v", got)
	}
	// A push clone needs a token of its own: add stores what it is given.
	token, _ := got["pushToken"].(string)
	if len(token) != 32 || token == src["pushToken"] || strings.Trim(token, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789") != "" {
		t.Errorf("clone pushToken = %q", token)
	}
	if again, _ := ForClone(src)["pushToken"].(string); again == token {
		t.Errorf("two clones share pushToken %q", token)
	}
	if _, ok := src["id"]; !ok || src["pushToken"] != "abc" {
		t.Error("ForClone changed its argument")
	}
	if _, ok := ForClone(RawMonitor{"type": "http", "name": "web"})["pushToken"]; ok {
		t.Error("an http clone got a pushToken")
	}
}

func TestMaintenanceWrites(t *testing.T) {
	f, s := writeFake(t)
	ctx := context.Background()

	id, err := s.AddMaintenance(ctx, map[string]any{"title": "deploy", "strategy": "manual"})
	if err != nil || id != 3 {
		t.Fatalf("AddMaintenance = %d, %v", id, err)
	}
	if err := s.SetMaintenanceMonitors(ctx, id, []int{1, 2}); err != nil {
		t.Fatal(err)
	}
	if !f.Sent(`["addMonitorMaintenance",3,[{"id":1},{"id":2}]]`) {
		t.Fatalf("monitors not sent as Kuma wants; frames: %v", f.Frames())
	}
	if err := s.DeleteMaintenance(ctx, id); err != nil {
		t.Fatal(err)
	}
}
