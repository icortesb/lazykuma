package kuma

import (
	"context"
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
