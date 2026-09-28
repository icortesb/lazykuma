package kuma

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// APIKey is a key for Kuma's metrics endpoint. Kuma shows the secret only
// once, when it is made; the list never carries it.
type APIKey struct {
	ID      int
	Name    string
	Active  bool
	Expires string // "" for never, else as Kuma stores it, server-local
	Created string
	Status  string // "active", "inactive" or "expired", as Kuma judges it
}

// Proxy is an outgoing proxy monitors can check through. Kuma sends its
// password in the clear; it is kept only to be sent back, never shown.
type Proxy struct {
	ID       int
	Protocol string // one of ProxyProtocols
	Host     string
	Port     int
	Auth     bool
	Username string
	Password string
	Default  bool
}

// DockerHost is a Docker daemon docker monitors watch containers on.
type DockerHost struct {
	ID     int
	Name   string
	Type   string // "socket" or "tcp"
	Daemon string // a socket path, or a tcp:// or http(s):// address
}

// ProxyProtocols are the protocols Kuma accepts for a proxy, in its order.
var ProxyProtocols = []string{"http", "https", "socks", "socks5", "socks5h", "socks4"}

// The lists Kuma sends after login and again after each change to them.
type (
	APIKeyList     struct{ Keys []APIKey }
	ProxyList      struct{ Proxies []Proxy }
	DockerHostList struct{ Hosts []DockerHost }
)

func (APIKeyList) event()     {}
func (ProxyList) event()      {}
func (DockerHostList) event() {}

func decodeAPIKeyList(a json.RawMessage) (APIKeyList, error) {
	var raw []struct {
		ID      int      `json:"id"`
		Name    string   `json:"name"`
		Active  flexBool `json:"active"`
		Expires string   `json:"expires"`
		Created string   `json:"createdDate"`
		Status  string   `json:"status"`
	}
	if err := json.Unmarshal(a, &raw); err != nil {
		return APIKeyList{}, err
	}
	out := APIKeyList{Keys: make([]APIKey, 0, len(raw))}
	for _, r := range raw {
		out.Keys = append(out.Keys, APIKey{ID: r.ID, Name: r.Name, Active: bool(r.Active), Expires: r.Expires, Created: r.Created, Status: r.Status})
	}
	sort.Slice(out.Keys, func(i, j int) bool { return out.Keys[i].ID < out.Keys[j].ID })
	return out, nil
}

func decodeProxyList(a json.RawMessage) (ProxyList, error) {
	var raw []struct {
		ID       int      `json:"id"`
		Protocol string   `json:"protocol"`
		Host     string   `json:"host"`
		Port     int      `json:"port"`
		Auth     flexBool `json:"auth"`
		Username string   `json:"username"`
		Password string   `json:"password"`
		Default  flexBool `json:"default"`
	}
	if err := json.Unmarshal(a, &raw); err != nil {
		return ProxyList{}, err
	}
	out := ProxyList{Proxies: make([]Proxy, 0, len(raw))}
	for _, r := range raw {
		out.Proxies = append(out.Proxies, Proxy{ID: r.ID, Protocol: r.Protocol, Host: r.Host, Port: r.Port,
			Auth: bool(r.Auth), Username: r.Username, Password: r.Password, Default: bool(r.Default)})
	}
	sort.Slice(out.Proxies, func(i, j int) bool { return out.Proxies[i].ID < out.Proxies[j].ID })
	return out, nil
}

func decodeDockerHostList(a json.RawMessage) (DockerHostList, error) {
	var raw []struct {
		ID     int    `json:"id"`
		Name   string `json:"name"`
		Type   string `json:"dockerType"`
		Daemon string `json:"dockerDaemon"`
	}
	if err := json.Unmarshal(a, &raw); err != nil {
		return DockerHostList{}, err
	}
	out := DockerHostList{Hosts: make([]DockerHost, 0, len(raw))}
	for _, r := range raw {
		out.Hosts = append(out.Hosts, DockerHost{ID: r.ID, Name: r.Name, Type: r.Type, Daemon: r.Daemon})
	}
	sort.Slice(out.Hosts, func(i, j int) bool { return out.Hosts[i].ID < out.Hosts[j].ID })
	return out, nil
}

// idReply reads the {ok, msg, id|keyID|…} replies of the admin calls.
type idReply struct {
	OK    bool   `json:"ok"`
	Msg   string `json:"msg"`
	ID    int    `json:"id"`
	Key   string `json:"key"`
	KeyID int    `json:"keyID"`
	Size  int64  `json:"size"`
}

func (s *Session) idCall(ctx context.Context, event string, args ...any) (idReply, error) {
	raw, err := s.emit(ctx, event, args...)
	if err != nil {
		return idReply{}, err
	}
	var r idReply
	if len(raw) > 0 {
		if err := json.Unmarshal(raw[0], &r); err != nil {
			return idReply{}, fmt.Errorf("kuma: %s reply: %w", event, err)
		}
	}
	if !r.OK {
		// Kuma pads some messages with the whitespace of its own source.
		return idReply{}, &ReplyError{Msg: strings.Join(strings.Fields(r.Msg), " ")}
	}
	return r, nil
}

// AddAPIKey makes a key and returns its secret, which Kuma never shows
// again, and its id. expires is "" for a key that never expires, else a
// server-local "YYYY-MM-DD HH:mm", the form Kuma's own dialog sends.
func (s *Session) AddAPIKey(ctx context.Context, name, expires string) (string, int, error) {
	var exp any
	if expires != "" {
		exp = expires
	}
	r, err := s.idCall(ctx, "addAPIKey", map[string]any{"name": name, "expires": exp, "active": 1})
	if err != nil {
		return "", 0, err
	}
	return r.Key, r.KeyID, nil
}

// SetAPIKeyActive enables or disables a key.
func (s *Session) SetAPIKeyActive(ctx context.Context, id int, active bool) error {
	event := "disableAPIKey"
	if active {
		event = "enableAPIKey"
	}
	_, err := s.idCall(ctx, event, id)
	return err
}

// DeleteAPIKey removes a key; anything using it stops working.
func (s *Session) DeleteAPIKey(ctx context.Context, id int) error {
	_, err := s.idCall(ctx, "deleteAPIKey", id)
	return err
}

// SaveProxy creates a proxy, or edits the one with p.ID, and returns its id.
// Credentials go only with auth on: an edit that leaves them out keeps the
// ones Kuma has. applyExisting sets the proxy on every monitor.
func (s *Session) SaveProxy(ctx context.Context, p Proxy, applyExisting bool) (int, error) {
	body := map[string]any{
		"protocol": p.Protocol, "host": p.Host, "port": p.Port,
		"auth": p.Auth, "default": p.Default, "applyExisting": applyExisting,
	}
	if p.Auth {
		body["username"], body["password"] = p.Username, p.Password
	}
	var id any
	if p.ID != 0 {
		id = p.ID
	}
	r, err := s.idCall(ctx, "addProxy", body, id)
	if err != nil {
		return 0, err
	}
	return r.ID, nil
}

// DeleteProxy removes a proxy; monitors using it go without one.
func (s *Session) DeleteProxy(ctx context.Context, id int) error {
	_, err := s.idCall(ctx, "deleteProxy", id)
	return err
}

func dockerBody(h DockerHost) map[string]any {
	return map[string]any{"name": h.Name, "dockerType": h.Type, "dockerDaemon": h.Daemon}
}

// SaveDockerHost creates a Docker host, or edits the one with h.ID.
func (s *Session) SaveDockerHost(ctx context.Context, h DockerHost) (int, error) {
	var id any
	if h.ID != 0 {
		id = h.ID
	}
	r, err := s.idCall(ctx, "addDockerHost", dockerBody(h), id)
	if err != nil {
		return 0, err
	}
	return r.ID, nil
}

// TestDockerHost asks Kuma to reach a daemon, saved or not, and returns
// what it said. Kuma gives up after about six seconds.
func (s *Session) TestDockerHost(ctx context.Context, h DockerHost) (string, error) {
	r, err := s.idCall(ctx, "testDockerHost", dockerBody(h))
	if err != nil {
		return "", err
	}
	return r.Msg, nil
}

// DeleteDockerHost removes a Docker host; monitors on it lose it.
func (s *Session) DeleteDockerHost(ctx context.Context, id int) error {
	_, err := s.idCall(ctx, "deleteDockerHost", id)
	return err
}

// DatabaseSize is Kuma's SQLite file size in bytes; 0 on MariaDB.
func (s *Session) DatabaseSize(ctx context.Context) (int64, error) {
	r, err := s.idCall(ctx, "getDatabaseSize")
	if err != nil {
		return 0, err
	}
	return r.Size, nil
}

// ShrinkDatabase compacts Kuma's SQLite file; on MariaDB it does nothing.
func (s *Session) ShrinkDatabase(ctx context.Context) error {
	_, err := s.idCall(ctx, "shrinkDatabase")
	return err
}

// ClearStatistics deletes every monitor's uptime statistics. There is no
// undo.
func (s *Session) ClearStatistics(ctx context.Context) error {
	_, err := s.idCall(ctx, "clearStatistics")
	return err
}
