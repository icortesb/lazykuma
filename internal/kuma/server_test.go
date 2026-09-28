package kuma

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodeAdminLists(t *testing.T) {
	ev, ok, err := DecodeEvent("apiKeyList", []json.RawMessage{json.RawMessage(`[{"id":2,"name":"old","userID":1,"createdDate":"2026-09-27 23:59:32","active":1,"expires":"2020-01-01 00:00:00","status":"expired"},{"id":1,"name":"grafana","userID":1,"createdDate":"2026-09-27 23:59:32","active":0,"expires":null,"status":"inactive"}]`)})
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	keys := ev.(APIKeyList).Keys
	if len(keys) != 2 || keys[0] != (APIKey{ID: 1, Name: "grafana", Created: "2026-09-27 23:59:32", Status: "inactive"}) ||
		keys[1].Expires != "2020-01-01 00:00:00" || !keys[1].Active || keys[1].Status != "expired" {
		t.Fatalf("keys = %+v", keys)
	}

	ev, ok, err = DecodeEvent("proxyList", []json.RawMessage{json.RawMessage(`[{"id":1,"userId":1,"protocol":"socks5","host":"10.0.0.5","port":1080,"auth":1,"username":"u","password":"p","active":1,"default":1,"createdDate":"x"}]`)})
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if px := ev.(ProxyList).Proxies; len(px) != 1 || px[0] != (Proxy{ID: 1, Protocol: "socks5", Host: "10.0.0.5", Port: 1080, Auth: true, Username: "u", Password: "p", Default: true}) { // ggignore: a test's fake password
		t.Fatalf("proxies = %+v", px)
	}

	ev, ok, err = DecodeEvent("dockerHostList", []json.RawMessage{json.RawMessage(`[{"id":1,"userID":1,"dockerDaemon":"/var/run/docker.sock","dockerType":"socket","name":"local"}]`)})
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if h := ev.(DockerHostList).Hosts; len(h) != 1 || h[0] != (DockerHost{ID: 1, Name: "local", Type: "socket", Daemon: "/var/run/docker.sock"}) {
		t.Fatalf("hosts = %+v", h)
	}
}

// adminFake answers the admin calls the way Kuma 2.5.3 did when probed.
func adminFake(t *testing.T) (*fakeKuma, *Session) {
	t.Helper()
	login := kumaLogin(false)
	f := newFakeKuma(t, func(event string, args []json.RawMessage) any {
		switch event {
		case "addAPIKey":
			return map[string]any{"ok": true, "msg": "successAdded", "msgi18n": true, "key": "uk1_secret", "keyID": 1}
		case "enableAPIKey", "disableAPIKey", "deleteAPIKey", "deleteProxy", "deleteDockerHost", "shrinkDatabase", "clearStatistics":
			return map[string]any{"ok": true}
		case "addProxy":
			var p map[string]any
			json.Unmarshal(args[0], &p)
			if p["protocol"] == "ftp" {
				return map[string]any{"ok": false, "msg": "\n                Unsupported proxy protocol \"ftp."}
			}
			return map[string]any{"ok": true, "msg": "Saved.", "id": 4}
		case "addDockerHost":
			return map[string]any{"ok": true, "msg": "Saved.", "id": 2}
		case "testDockerHost":
			var h map[string]any
			json.Unmarshal(args[0], &h)
			if h["dockerType"] == "tcp" {
				return map[string]any{"ok": true, "msg": "Connected Successfully. Amount of containers: 3"}
			}
			return map[string]any{"ok": false, "msg": "connect ENOENT /var/run/docker.sock"}
		case "getDatabaseSize":
			return map[string]any{"ok": true, "size": 61440}
		}
		return login(event, args)
	})
	s := dial(t, f)
	if err := s.LoginByToken(context.Background(), "jwt"); err != nil {
		t.Fatal(err)
	}
	return f, s
}

func TestAdminCalls(t *testing.T) {
	f, s := adminFake(t)
	ctx := context.Background()

	key, id, err := s.AddAPIKey(ctx, "grafana", "")
	if err != nil || key != "uk1_secret" || id != 1 || !f.Sent(`["addAPIKey",{"active":1,"expires":null,"name":"grafana"}]`) {
		t.Fatalf("AddAPIKey = %q %d %v; %v", key, id, err, f.Frames())
	}
	if _, _, err := s.AddAPIKey(ctx, "ci", "2026-12-31 23:59"); err != nil || !f.Sent(`"expires":"2026-12-31 23:59"`) {
		t.Fatalf("AddAPIKey with expiry: %v %v", err, f.Frames())
	}
	if err := s.SetAPIKeyActive(ctx, 1, false); err != nil || !f.Sent(`["disableAPIKey",1]`) {
		t.Fatalf("disable: %v", err)
	}
	if err := s.SetAPIKeyActive(ctx, 1, true); err != nil || !f.Sent(`["enableAPIKey",1]`) {
		t.Fatalf("enable: %v", err)
	}
	if err := s.DeleteAPIKey(ctx, 1); err != nil || !f.Sent(`["deleteAPIKey",1]`) {
		t.Fatalf("delete key: %v", err)
	}

	pid, err := s.SaveProxy(ctx, Proxy{Protocol: "socks5", Host: "10.0.0.5", Port: 1080, Auth: true, Username: "u", Password: "p", Default: true}, false) // ggignore: a test's fake password
	if err != nil || pid != 4 || !f.Sent(`["addProxy",{"applyExisting":false,"auth":true,"default":true,"host":"10.0.0.5","password":"p","port":1080,"protocol":"socks5","username":"u"},null]`) {
		t.Fatalf("new proxy: %d %v %v", pid, err, f.Frames())
	}
	// An edit names the proxy; without auth, no credentials are sent, and
	// Kuma keeps the ones it has.
	if _, err := s.SaveProxy(ctx, Proxy{ID: 4, Protocol: "http", Host: "10.0.0.6", Port: 3128}, true); err != nil ||
		!f.Sent(`{"applyExisting":true,"auth":false,"default":false,"host":"10.0.0.6","port":3128,"protocol":"http"},4]`) {
		t.Fatalf("edit proxy: %v %v", err, f.Frames())
	}
	if _, err := s.SaveProxy(ctx, Proxy{Protocol: "ftp", Host: "x", Port: 1}, false); err == nil || !strings.Contains(err.Error(), "Unsupported proxy protocol") {
		t.Fatalf("bad protocol: %v", err)
	}
	if err := s.DeleteProxy(ctx, 4); err != nil || !f.Sent(`["deleteProxy",4]`) {
		t.Fatalf("delete proxy: %v", err)
	}

	hid, err := s.SaveDockerHost(ctx, DockerHost{Name: "local", Type: "socket", Daemon: "/var/run/docker.sock"})
	if err != nil || hid != 2 || !f.Sent(`["addDockerHost",{"dockerDaemon":"/var/run/docker.sock","dockerType":"socket","name":"local"},null]`) {
		t.Fatalf("new docker host: %d %v %v", hid, err, f.Frames())
	}
	if _, err := s.SaveDockerHost(ctx, DockerHost{ID: 2, Name: "local", Type: "socket", Daemon: "/run/docker.sock"}); err != nil || !f.Sent(`"name":"local"},2]`) {
		t.Fatalf("edit docker host: %v", err)
	}
	if msg, err := s.TestDockerHost(ctx, DockerHost{Name: "r", Type: "tcp", Daemon: "tcp://10.0.0.7:2375"}); err != nil || msg != "Connected Successfully. Amount of containers: 3" {
		t.Fatalf("test ok: %q %v", msg, err)
	}
	if _, err := s.TestDockerHost(ctx, DockerHost{Name: "l", Type: "socket", Daemon: "/var/run/docker.sock"}); err == nil || !strings.Contains(err.Error(), "ENOENT") {
		t.Fatalf("test failing: %v", err)
	}
	if err := s.DeleteDockerHost(ctx, 2); err != nil || !f.Sent(`["deleteDockerHost",2]`) {
		t.Fatalf("delete docker host: %v", err)
	}

	if size, err := s.DatabaseSize(ctx); err != nil || size != 61440 {
		t.Fatalf("DatabaseSize = %d %v", size, err)
	}
	if err := s.ShrinkDatabase(ctx); err != nil || !f.Sent(`"shrinkDatabase"`) {
		t.Fatalf("shrink: %v", err)
	}
	if err := s.ClearStatistics(ctx); err != nil || !f.Sent(`"clearStatistics"`) {
		t.Fatalf("clear: %v", err)
	}
}
