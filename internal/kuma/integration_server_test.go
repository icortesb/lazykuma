//go:build integration

package kuma

import (
	"context"
	"strings"
	"testing"
	"time"
)

// waitForList blocks on events until one of kind E satisfies pred, or 30s
// pass. It is how this test picks the pushed list that reflects a write out
// of the stream a Supervisor delivers (login pushes one too, and every
// write to a kind pushes another).
func waitForList[E Event](t *testing.T, events <-chan Event, pred func(E) bool) E {
	t.Helper()
	timeout := time.NewTimer(30 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case ev := <-events:
			if l, ok := ev.(E); ok && pred(l) {
				return l
			}
		case <-timeout.C:
			var zero E
			t.Fatalf("deadline waiting for %T", zero)
			return zero
		}
	}
}

// TestIntegrationServer checks the admin calls phase D adds against a real
// Kuma: API keys, proxies, Docker hosts, and the database maintenance
// calls, each confirmed by the list Kuma pushes after the write.
func TestIntegrationServer(t *testing.T) {
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
	t.Cleanup(func() { s.Close() }) // registered first: it runs last
	if err := s.LoginByToken(ctx, token); err != nil {
		t.Fatal(err)
	}

	// A Supervisor on the same token collects what Kuma pushes: apiKeyList,
	// proxyList and dockerHostList at login, and again after every write to
	// their kind.
	events := make(chan Event, 1024)
	sup := NewSupervisor(url, func() string { return token }, func(ev Event) { events <- ev })
	sctx, stop := context.WithCancel(ctx)
	defer stop()
	go sup.Run(sctx)

	// 1. A key with no expiry comes back active, "active", and no Expires.
	key, keyID, err := s.AddAPIKey(ctx, "it-key", "")
	if err != nil {
		t.Fatalf("AddAPIKey: %v", err)
	}
	if !strings.HasPrefix(key, "uk") || keyID == 0 {
		// Never interpolate the key itself: Kuma shows it exactly once, and
		// constraints.md says it is never logged anywhere.
		t.Fatalf("AddAPIKey: prefix \"uk\" = %v, len(key) = %d, id = %d", strings.HasPrefix(key, "uk"), len(key), keyID)
	}
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer ccancel()
		if err := s.DeleteAPIKey(cctx, keyID); err != nil {
			t.Logf("cleanup: delete key %d: %v", keyID, err)
		}
	})
	list := waitForList(t, events, func(l APIKeyList) bool {
		for _, k := range l.Keys {
			if k.ID == keyID {
				return true
			}
		}
		return false
	})
	found := findAPIKey(list.Keys, keyID)
	if !found.Active || found.Status != "active" || found.Expires != "" {
		t.Fatalf("new key = %+v", found)
	}

	// 2. A key given a past expiry comes back "expired".
	_, oldID, err := s.AddAPIKey(ctx, "it-old", "2020-01-01 00:00")
	if err != nil {
		t.Fatalf("AddAPIKey(expired): %v", err)
	}
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer ccancel()
		if err := s.DeleteAPIKey(cctx, oldID); err != nil {
			t.Logf("cleanup: delete key %d: %v", oldID, err)
		}
	})
	list = waitForList(t, events, func(l APIKeyList) bool {
		for _, k := range l.Keys {
			if k.ID == oldID {
				return true
			}
		}
		return false
	})
	old := findAPIKey(list.Keys, oldID)
	if old.Status != "expired" {
		t.Fatalf("expired key = %+v", old)
	}

	// 3. Disabling it shows Active false and "inactive"; re-enabling and
	// deleting both keys leaves neither in a later list.
	if err := s.SetAPIKeyActive(ctx, keyID, false); err != nil {
		t.Fatalf("SetAPIKeyActive(false): %v", err)
	}
	list = waitForList(t, events, func(l APIKeyList) bool {
		k := findAPIKey(l.Keys, keyID)
		return k.ID == keyID && !k.Active
	})
	disabled := findAPIKey(list.Keys, keyID)
	if disabled.Active || disabled.Status != "inactive" {
		t.Fatalf("disabled key = %+v", disabled)
	}
	if err := s.SetAPIKeyActive(ctx, keyID, true); err != nil {
		t.Fatalf("SetAPIKeyActive(true): %v", err)
	}
	waitForList(t, events, func(l APIKeyList) bool {
		k := findAPIKey(l.Keys, keyID)
		return k.ID == keyID && k.Active
	})
	if err := s.DeleteAPIKey(ctx, keyID); err != nil {
		t.Fatalf("DeleteAPIKey: %v", err)
	}
	if err := s.DeleteAPIKey(ctx, oldID); err != nil {
		t.Fatalf("DeleteAPIKey(old): %v", err)
	}
	waitForList(t, events, func(l APIKeyList) bool {
		return findAPIKey(l.Keys, keyID).ID == 0 && findAPIKey(l.Keys, oldID).ID == 0
	})

	// 4. A new proxy with auth comes back with its username.
	proxyID, err := s.SaveProxy(ctx, Proxy{
		Protocol: "socks5", Host: "10.0.0.5", Port: 1080, Auth: true, Username: "u", Password: "p", // ggignore: a test's fake password
	}, false)
	if err != nil || proxyID == 0 {
		t.Fatalf("SaveProxy: %d, %v", proxyID, err)
	}
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer ccancel()
		if err := s.DeleteProxy(cctx, proxyID); err != nil {
			t.Logf("cleanup: delete proxy %d: %v", proxyID, err)
		}
	})
	list2 := waitForList(t, events, func(l ProxyList) bool {
		return findProxy(l.Proxies, proxyID).ID == proxyID
	})
	px := findProxy(list2.Proxies, proxyID)
	if !px.Auth || px.Username != "u" {
		t.Fatalf("new proxy = %+v", redactProxy(px))
	}

	// 5. Editing it without auth or credentials changes protocol, host and
	// port. Kuma keeps the username the edit left out (the probe's finding
	// this test relies on: an addProxy that omits username/password keeps
	// what is stored, the same as any other omitted field on an edit).
	if _, err := s.SaveProxy(ctx, Proxy{ID: proxyID, Protocol: "http", Host: "10.0.0.6", Port: 3128}, false); err != nil {
		t.Fatalf("SaveProxy(edit): %v", err)
	}
	list2 = waitForList(t, events, func(l ProxyList) bool {
		p := findProxy(l.Proxies, proxyID)
		return p.ID == proxyID && p.Protocol == "http"
	})
	edited := findProxy(list2.Proxies, proxyID)
	if edited.Host != "10.0.0.6" || edited.Port != 3128 {
		t.Fatalf("edited proxy = %+v", redactProxy(edited))
	}
	if edited.Username != "u" {
		t.Fatalf("edited proxy lost its username: %+v, want it kept as \"u\"", redactProxy(edited))
	}

	// 6. An unsupported protocol is refused; deleting the proxy leaves it
	// out of a later list.
	if _, err := s.SaveProxy(ctx, Proxy{Protocol: "ftp", Host: "10.0.0.7", Port: 21}, false); err == nil ||
		!strings.Contains(err.Error(), "Unsupported proxy protocol") {
		t.Fatalf("SaveProxy(ftp) = %v, want an error mentioning the protocol", err)
	}
	if err := s.DeleteProxy(ctx, proxyID); err != nil {
		t.Fatalf("DeleteProxy: %v", err)
	}
	waitForList(t, events, func(l ProxyList) bool {
		return findProxy(l.Proxies, proxyID).ID == 0
	})

	// 7. A Docker host over a socket saves; testing it fails since there is
	// no Docker in the Kuma container; deleting it leaves it out.
	hostID, err := s.SaveDockerHost(ctx, DockerHost{Name: "it-docker", Type: "socket", Daemon: "/var/run/docker.sock"})
	if err != nil || hostID == 0 {
		t.Fatalf("SaveDockerHost: %d, %v", hostID, err)
	}
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer ccancel()
		if err := s.DeleteDockerHost(cctx, hostID); err != nil {
			t.Logf("cleanup: delete docker host %d: %v", hostID, err)
		}
	})
	waitForList(t, events, func(l DockerHostList) bool {
		return findDockerHost(l.Hosts, hostID).ID == hostID
	})
	if _, err := s.TestDockerHost(ctx, DockerHost{Type: "socket", Daemon: "/var/run/docker.sock"}); err == nil {
		t.Fatal("TestDockerHost against a container with no Docker socket succeeded")
	} else if !strings.Contains(err.Error(), "/var/run/docker.sock") && !strings.Contains(err.Error(), "ENOENT") {
		// A wrong-reason failure (say, a bad request) must not pass silently
		// as "expected": the error has to actually name the socket.
		t.Fatalf("TestDockerHost error = %v, want it to mention the socket", err)
	} else {
		t.Logf("TestDockerHost (expected failure): %v", err)
	}
	if err := s.DeleteDockerHost(ctx, hostID); err != nil {
		t.Fatalf("DeleteDockerHost: %v", err)
	}
	waitForList(t, events, func(l DockerHostList) bool {
		return findDockerHost(l.Hosts, hostID).ID == 0
	})

	// 8. The database calls: the integration Kuma uses SQLite, so its size
	// is nonzero; shrinking it and clearing statistics both succeed.
	size, err := s.DatabaseSize(ctx)
	if err != nil {
		t.Fatalf("DatabaseSize: %v", err)
	}
	if size <= 0 {
		t.Fatalf("DatabaseSize = %d, want > 0 on SQLite", size)
	}
	if err := s.ShrinkDatabase(ctx); err != nil {
		t.Fatalf("ShrinkDatabase: %v", err)
	}
	if err := s.ClearStatistics(ctx); err != nil {
		t.Fatalf("ClearStatistics: %v", err)
	}
}

func findAPIKey(keys []APIKey, id int) APIKey {
	for _, k := range keys {
		if k.ID == id {
			return k
		}
	}
	return APIKey{}
}

// redactProxy is Proxy with its password blanked out, for a failure message:
// constraints.md says a proxy's password is never displayed.
func redactProxy(p Proxy) Proxy {
	if p.Password != "" {
		p.Password = "REDACTED"
	}
	return p
}

func findProxy(proxies []Proxy, id int) Proxy {
	for _, p := range proxies {
		if p.ID == id {
			return p
		}
	}
	return Proxy{}
}

func findDockerHost(hosts []DockerHost, id int) DockerHost {
	for _, h := range hosts {
		if h.ID == id {
			return h
		}
	}
	return DockerHost{}
}
