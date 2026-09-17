package kuma

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
)

// RawMonitor is a monitor as Kuma itself keeps it: 119 fields, most of
// which the TUI neither knows nor should invent. editMonitor refuses
// anything less than the whole object, so monitors travel unchanged.
type RawMonitor map[string]any

// GetMonitor is the whole monitor, the shape EditMonitor wants back.
func (s *Session) GetMonitor(ctx context.Context, id int) (RawMonitor, error) {
	raw, err := s.emit(ctx, "getMonitor", id)
	if err != nil {
		return nil, err
	}
	var r struct {
		OK      bool       `json:"ok"`
		Msg     string     `json:"msg"`
		Monitor RawMonitor `json:"monitor"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw[0], &r); err != nil {
			return nil, fmt.Errorf("kuma: getMonitor reply: %w", err)
		}
	}
	if !r.OK {
		return nil, &ReplyError{Msg: r.Msg}
	}
	return r.Monitor, nil
}

// AddMonitor creates a monitor and returns its id.
func (s *Session) AddMonitor(ctx context.Context, m RawMonitor) (int, error) {
	raw, err := s.emit(ctx, "add", map[string]any(m))
	if err != nil {
		return 0, err
	}
	var r struct {
		OK        bool   `json:"ok"`
		Msg       string `json:"msg"`
		MonitorID int    `json:"monitorID"`
	}
	if len(raw) > 0 {
		json.Unmarshal(raw[0], &r)
	}
	if !r.OK {
		return 0, &ReplyError{Msg: r.Msg}
	}
	return r.MonitorID, nil
}

// EditMonitor saves a monitor. m must be a whole monitor, id included:
// Kuma rejects a partial object rather than merging it.
func (s *Session) EditMonitor(ctx context.Context, m RawMonitor) error {
	if _, ok := m["id"]; !ok {
		return fmt.Errorf("kuma: editMonitor needs the monitor's id")
	}
	r, err := s.call(ctx, "editMonitor", map[string]any(m))
	if err != nil {
		return err
	}
	return r.err()
}

// DeleteMonitor removes a monitor and its history.
func (s *Session) DeleteMonitor(ctx context.Context, id int) error {
	r, err := s.call(ctx, "deleteMonitor", id)
	if err != nil {
		return err
	}
	return r.err()
}

// SaveNotification creates a notification channel, or edits the one with
// the given id (0 creates). cfg holds the provider's own fields plus name,
// type and isDefault. It returns the channel's id.
func (s *Session) SaveNotification(ctx context.Context, cfg map[string]any, id int) (int, error) {
	var arg any
	if id != 0 {
		arg = id
	}
	raw, err := s.emit(ctx, "addNotification", cfg, arg)
	if err != nil {
		return 0, err
	}
	var r struct {
		OK  bool   `json:"ok"`
		Msg string `json:"msg"`
		ID  int    `json:"id"`
	}
	if len(raw) > 0 {
		json.Unmarshal(raw[0], &r)
	}
	if !r.OK {
		return 0, &ReplyError{Msg: r.Msg}
	}
	return r.ID, nil
}

// DeleteNotification removes a channel.
func (s *Session) DeleteNotification(ctx context.Context, id int) error {
	r, err := s.call(ctx, "deleteNotification", id)
	if err != nil {
		return err
	}
	return r.err()
}

// TestNotification asks Kuma to send a test message. The error carries the
// provider's own complaint, which is what makes a wrong token obvious.
func (s *Session) TestNotification(ctx context.Context, cfg map[string]any) error {
	r, err := s.call(ctx, "testNotification", cfg)
	if err != nil {
		return err
	}
	return r.err()
}

// AddMaintenance creates a maintenance window and returns its id.
func (s *Session) AddMaintenance(ctx context.Context, m map[string]any) (int, error) {
	raw, err := s.emit(ctx, "addMaintenance", m)
	if err != nil {
		return 0, err
	}
	var r struct {
		OK            bool   `json:"ok"`
		Msg           string `json:"msg"`
		MaintenanceID int    `json:"maintenanceID"`
	}
	if len(raw) > 0 {
		json.Unmarshal(raw[0], &r)
	}
	if !r.OK {
		return 0, &ReplyError{Msg: r.Msg}
	}
	return r.MaintenanceID, nil
}

// SetMaintenanceMonitors says which monitors a maintenance covers.
func (s *Session) SetMaintenanceMonitors(ctx context.Context, id int, monitorIDs []int) error {
	monitors := make([]map[string]any, 0, len(monitorIDs))
	for _, mid := range monitorIDs {
		monitors = append(monitors, map[string]any{"id": mid})
	}
	r, err := s.call(ctx, "addMonitorMaintenance", id, monitors)
	if err != nil {
		return err
	}
	return r.err()
}

// DeleteMaintenance ends a maintenance window for good.
func (s *Session) DeleteMaintenance(ctx context.Context, id int) error {
	r, err := s.call(ctx, "deleteMaintenance", id)
	if err != nil {
		return err
	}
	return r.err()
}

// NewGroup is a group monitor ready for AddMonitor: the fields Kuma's add
// reads for every monitor, with a group's type. A group checks nothing; its
// interval only paces how often Kuma recomputes it from its children.
func NewGroup(name string) RawMonitor {
	return RawMonitor{
		"type": "group", "name": name, "parent": nil,
		"interval": 60, "retryInterval": 60, "maxretries": 0, "active": true,
		"accepted_statuscodes": []string{"200-299"}, "notificationIDList": map[string]bool{},
		"conditions": []any{}, "kafkaProducerBrokers": []any{}, "kafkaProducerSaslOptions": map[string]any{},
	}
}

// cloneDrops are the properties getMonitor returns that add would try to
// store as columns Kuma does not have; Kuma's own clone removes the same.
var cloneDrops = []string{
	"id", "includeSensitiveData", "maintenance", "childrenIDs", "forceInactive",
	"path", "pathName", "screenshot", "tags",
}

// ForClone is a copy of a whole monitor that AddMonitor accepts as a new
// one: same settings, same group, "copy of" its name. A push monitor gets a
// token of its own: Kuma's add stores whatever token it is given (the web UI
// makes one in the browser), so keeping the source's would have two
// monitors share a push URL, and dropping it would leave the clone without
// one. The tags are not in it: add ignores them, so the caller adds them
// after.
func ForClone(m RawMonitor) RawMonitor {
	out := make(RawMonitor, len(m))
	for k, v := range m {
		out[k] = v
	}
	for _, k := range cloneDrops {
		delete(out, k)
	}
	if out["type"] == "push" {
		out["pushToken"] = newPushToken()
	}
	name, _ := m["name"].(string)
	out["name"] = "copy of " + name
	return out
}

// pushTokenChars and pushTokenLen match the tokens Kuma's web UI makes for a
// push monitor, so a clone's push URL looks like any other.
const (
	pushTokenChars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	pushTokenLen   = 32
)

// newPushToken is a random push token. The token is the only thing that
// authenticates a push, so it comes from crypto/rand; rand.Text is not used
// because its alphabet is not Kuma's. Bytes at or above the largest multiple
// of the alphabet's length are skipped, so no character is likelier than
// another. rand.Read never returns an error: it crashes the program instead.
func newPushToken() string {
	limit := byte(256 - 256%len(pushTokenChars))
	out := make([]byte, 0, pushTokenLen)
	var buf [pushTokenLen]byte
	for len(out) < pushTokenLen {
		rand.Read(buf[:])
		for _, b := range buf {
			if b < limit && len(out) < pushTokenLen {
				out = append(out, pushTokenChars[int(b)%len(pushTokenChars)])
			}
		}
	}
	return string(out)
}

// DeleteGroup removes a group. withMonitors deletes the monitors in it, and
// their history, too; otherwise Kuma keeps them, outside any group.
func (s *Session) DeleteGroup(ctx context.Context, id int, withMonitors bool) error {
	r, err := s.call(ctx, "deleteMonitor", id, withMonitors)
	if err != nil {
		return err
	}
	return r.err()
}

// TagDef is a tag as Kuma defines it, apart from any monitor.
type TagDef struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

// TagColor is one of the colours Kuma's own tag editor offers.
type TagColor struct{ Name, Hex string }

// TagColors is Kuma's palette, in its order, so a tag made here looks like
// one made in the web UI.
var TagColors = []TagColor{
	{"gray", "#4B5563"}, {"red", "#DC2626"}, {"orange", "#D97706"}, {"green", "#059669"},
	{"blue", "#2563EB"}, {"indigo", "#4F46E5"}, {"purple", "#7C3AED"}, {"pink", "#DB2777"},
}

// Tags is every tag this Kuma has. Kuma never pushes the list: it is asked
// for.
func (s *Session) Tags(ctx context.Context) ([]TagDef, error) {
	raw, err := s.emit(ctx, "getTags")
	if err != nil {
		return nil, err
	}
	var r struct {
		OK   bool     `json:"ok"`
		Msg  string   `json:"msg"`
		Tags []TagDef `json:"tags"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw[0], &r); err != nil {
			return nil, fmt.Errorf("kuma: getTags reply: %w", err)
		}
	}
	if !r.OK {
		return nil, &ReplyError{Msg: r.Msg}
	}
	return r.Tags, nil
}

// AddTag creates a tag and returns it with its id.
func (s *Session) AddTag(ctx context.Context, name, color string) (TagDef, error) {
	raw, err := s.emit(ctx, "addTag", map[string]string{"name": name, "color": color})
	if err != nil {
		return TagDef{}, err
	}
	var r struct {
		OK  bool   `json:"ok"`
		Msg string `json:"msg"`
		Tag TagDef `json:"tag"`
	}
	if len(raw) > 0 {
		json.Unmarshal(raw[0], &r)
	}
	if !r.OK {
		return TagDef{}, &ReplyError{Msg: r.Msg}
	}
	return r.Tag, nil
}

// EditTag renames or recolours a tag, on every monitor carrying it.
func (s *Session) EditTag(ctx context.Context, t TagDef) error {
	r, err := s.call(ctx, "editTag", t)
	if err != nil {
		return err
	}
	return r.err()
}

// DeleteTag removes a tag from Kuma and from every monitor.
func (s *Session) DeleteTag(ctx context.Context, id int) error {
	r, err := s.call(ctx, "deleteTag", id)
	if err != nil {
		return err
	}
	return r.err()
}

// AddMonitorTag puts a tag on a monitor, with a value that may be empty.
func (s *Session) AddMonitorTag(ctx context.Context, tagID, monitorID int, value string) error {
	r, err := s.call(ctx, "addMonitorTag", tagID, monitorID, value)
	if err != nil {
		return err
	}
	return r.err()
}

// DeleteMonitorTag takes a tag off a monitor. Kuma matches the value too, so
// it must be the value the monitor carries.
func (s *Session) DeleteMonitorTag(ctx context.Context, tagID, monitorID int, value string) error {
	r, err := s.call(ctx, "deleteMonitorTag", tagID, monitorID, value)
	if err != nil {
		return err
	}
	return r.err()
}
