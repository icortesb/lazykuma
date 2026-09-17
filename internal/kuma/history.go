package kuma

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"
)

// ChartPoint is one bucket of a monitor's history: how many checks were up
// and down in it, and their pings. Kuma makes a bucket per minute for a day
// or less, per hour up to thirty days, per day beyond.
type ChartPoint struct {
	Time             time.Time // the bucket's start, UTC
	Up, Down         int
	AvgPing, MinPing float64
	MaxPing          float64
}

// ChartData is a monitor's history over the last hours, oldest first. Kuma
// answers newest first and includes the bucket in progress even when no
// check has landed in it yet; that one says nothing and is left out.
func (s *Session) ChartData(ctx context.Context, monitorID, hours int) ([]ChartPoint, error) {
	raw, err := s.emit(ctx, "getMonitorChartData", monitorID, hours)
	if err != nil {
		return nil, err
	}
	var r struct {
		OK   bool   `json:"ok"`
		Msg  string `json:"msg"`
		Data []struct {
			Up        int     `json:"up"`
			Down      int     `json:"down"`
			AvgPing   float64 `json:"avgPing"`
			MinPing   float64 `json:"minPing"`
			MaxPing   float64 `json:"maxPing"`
			Timestamp int64   `json:"timestamp"`
		} `json:"data"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw[0], &r); err != nil {
			return nil, fmt.Errorf("kuma: getMonitorChartData reply: %w", err)
		}
	}
	if !r.OK {
		return nil, &ReplyError{Msg: r.Msg}
	}
	out := make([]ChartPoint, 0, len(r.Data))
	for _, d := range r.Data {
		if d.Up == 0 && d.Down == 0 {
			continue
		}
		out = append(out, ChartPoint{
			Time: time.Unix(d.Timestamp, 0).UTC(), Up: d.Up, Down: d.Down,
			AvgPing: d.AvgPing, MinPing: d.MinPing, MaxPing: d.MaxPing,
		})
	}
	slices.SortFunc(out, func(a, b ChartPoint) int { return a.Time.Compare(b.Time) })
	return out, nil
}

// ImportantBeats is a page of a monitor's state changes, newest first:
// count of them after skipping offset.
func (s *Session) ImportantBeats(ctx context.Context, monitorID, offset, count int) ([]Beat, error) {
	raw, err := s.emit(ctx, "monitorImportantHeartbeatListPaged", monitorID, offset, count)
	if err != nil {
		return nil, err
	}
	var r struct {
		OK   bool              `json:"ok"`
		Msg  string            `json:"msg"`
		Data []json.RawMessage `json:"data"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw[0], &r); err != nil {
			return nil, fmt.Errorf("kuma: monitorImportantHeartbeatListPaged reply: %w", err)
		}
	}
	if !r.OK {
		return nil, &ReplyError{Msg: r.Msg}
	}
	out := make([]Beat, 0, len(r.Data))
	for _, d := range r.Data {
		b, err := decodeBeat(d)
		if err != nil {
			return nil, fmt.Errorf("kuma: monitorImportantHeartbeatListPaged: %w", err)
		}
		if b.MonitorID == 0 {
			b.MonitorID = monitorID
		}
		out = append(out, b)
	}
	return out, nil
}

// ClearEvents blanks a monitor's state-change messages; its uptime stays.
func (s *Session) ClearEvents(ctx context.Context, monitorID int) error {
	r, err := s.call(ctx, "clearEvents", monitorID)
	if err != nil {
		return err
	}
	return r.err()
}

// ClearHeartbeats deletes a monitor's uptime statistics and restarts it, so
// its chart and uptime start again from now.
func (s *Session) ClearHeartbeats(ctx context.Context, monitorID int) error {
	r, err := s.call(ctx, "clearHeartbeats", monitorID)
	if err != nil {
		return err
	}
	return r.err()
}
