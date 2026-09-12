package kuma

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Status is what a heartbeat says about a monitor.
type Status int

const (
	StatusDown        Status = 0
	StatusUp          Status = 1
	StatusPending     Status = 2
	StatusMaintenance Status = 3
)

// Event is anything a Supervisor reports: what the server pushed, decoded,
// and what happened to the connection.
type Event interface{ event() }

// Monitor is the part of a Kuma monitor the TUI shows.
type Monitor struct {
	ID          int
	Name        string
	Type        string
	URL         string
	Hostname    string
	Port        int
	Active      bool // false when paused
	Maintenance bool
	Parent      int // 0 when the monitor is not in a group
	Interval    int // seconds
}

// Target is what the monitor watches, the way the web UI shows it.
func (m Monitor) Target() string {
	switch {
	case m.URL != "" && m.URL != "https://":
		return m.URL
	case m.Hostname != "" && m.Port != 0:
		return m.Hostname + ":" + strconv.Itoa(m.Port)
	case m.Hostname != "":
		return m.Hostname
	}
	return m.Type
}

// Beat is one heartbeat.
type Beat struct {
	MonitorID int
	Status    Status
	Time      time.Time // UTC
	Msg       string
	Ping      float64 // milliseconds; meaningful only when HasPing
	HasPing   bool
	Important bool // the status changed on this beat
}

// Server events.
type (
	// MonitorList is the whole list: monitors missing from it are gone.
	MonitorList struct{ Monitors map[int]Monitor }
	// MonitorUpdate carries the monitors that changed (updateMonitorIntoList).
	MonitorUpdate struct{ Monitors map[int]Monitor }
	// MonitorDeleted is a monitor removed from the web UI.
	MonitorDeleted struct{ ID int }
	// Heartbeat is one live beat.
	Heartbeat struct{ Beat Beat }
	// HeartbeatList is a monitor's recent beats, oldest first. Overwrite
	// means they replace what the client holds for that monitor.
	HeartbeatList struct {
		MonitorID int
		Beats     []Beat
		Overwrite bool
	}
	// AvgPing is the monitor's average ping; Valid is false when it has none.
	AvgPing struct {
		MonitorID int
		Ms        float64
		Valid     bool
	}
	// Uptime is the share of up beats, 0 to 1, over Period: "24" (hours),
	// "720" (hours) or "1y".
	Uptime struct {
		MonitorID int
		Period    string
		Ratio     float64
	}
	// CertInfo is the TLS certificate of an https monitor.
	CertInfo struct {
		MonitorID     int
		Valid         bool
		DaysRemaining int
	}
	// Info is the server's version. Version is empty before login.
	Info struct{ Version string }
)

// Connection events, produced by the Supervisor rather than the server.
type (
	Connecting   struct{}
	Connected    struct{}
	Disconnected struct{ Err error }
	// AuthFailed stops the Supervisor until Retry: either there is no token
	// stored (NoToken) or Kuma refused it (Msg says why).
	AuthFailed struct {
		NoToken bool
		Msg     string
	}
	// Unsupported is a server lazykuma does not speak to, such as Kuma v1.
	Unsupported struct{ Version string }
)

func (MonitorList) event()    {}
func (MonitorUpdate) event()  {}
func (MonitorDeleted) event() {}
func (Heartbeat) event()      {}
func (HeartbeatList) event()  {}
func (AvgPing) event()        {}
func (Uptime) event()         {}
func (CertInfo) event()       {}
func (Info) event()           {}
func (Connecting) event()     {}
func (Connected) event()      {}
func (Disconnected) event()   {}
func (AuthFailed) event()     {}
func (Unsupported) event()    {}

// DecodeEvent turns a server event into a typed Event. ok is false for the
// events the TUI does not use; that is not an error, Kuma sends many.
func DecodeEvent(name string, args []json.RawMessage) (ev Event, ok bool, err error) {
	defer func() {
		if err != nil {
			err = fmt.Errorf("%s: %w", name, err)
		}
	}()
	arg := func(i int) (json.RawMessage, error) {
		if i >= len(args) {
			return nil, fmt.Errorf("missing argument %d", i)
		}
		return args[i], nil
	}

	switch name {
	case "monitorList", "updateMonitorIntoList":
		a, err := arg(0)
		if err != nil {
			return nil, false, err
		}
		mons, err := decodeMonitors(a)
		if err != nil {
			return nil, false, err
		}
		if name == "monitorList" {
			return MonitorList{Monitors: mons}, true, nil
		}
		return MonitorUpdate{Monitors: mons}, true, nil

	case "deleteMonitorFromList":
		a, err := arg(0)
		if err != nil {
			return nil, false, err
		}
		id, err := decodeID(a)
		if err != nil {
			return nil, false, err
		}
		return MonitorDeleted{ID: id}, true, nil

	case "heartbeat":
		a, err := arg(0)
		if err != nil {
			return nil, false, err
		}
		b, err := decodeBeat(a)
		if err != nil {
			return nil, false, err
		}
		return Heartbeat{Beat: b}, true, nil

	case "heartbeatList":
		a0, err := arg(0)
		if err != nil {
			return nil, false, err
		}
		a1, err := arg(1)
		if err != nil {
			return nil, false, err
		}
		id, err := decodeID(a0)
		if err != nil {
			return nil, false, err
		}
		var raw []json.RawMessage
		if err := json.Unmarshal(a1, &raw); err != nil {
			return nil, false, err
		}
		hl := HeartbeatList{MonitorID: id, Beats: make([]Beat, 0, len(raw))}
		for _, r := range raw {
			b, err := decodeBeat(r)
			if err != nil {
				return nil, false, err
			}
			b.MonitorID = id
			hl.Beats = append(hl.Beats, b)
		}
		if len(args) > 2 {
			// Lenient on purpose: an odd flag leaves Overwrite false, and a
			// merge is safe because beats are deduplicated by time.
			_ = json.Unmarshal(args[2], &hl.Overwrite)
		}
		return hl, true, nil

	case "avgPing":
		a0, err := arg(0)
		if err != nil {
			return nil, false, err
		}
		id, err := decodeID(a0)
		if err != nil {
			return nil, false, err
		}
		ap := AvgPing{MonitorID: id}
		if len(args) > 1 {
			var ms *float64
			if err := json.Unmarshal(args[1], &ms); err != nil {
				return nil, false, err
			}
			if ms != nil {
				ap.Ms, ap.Valid = *ms, true
			}
		}
		return ap, true, nil

	case "uptime":
		if len(args) < 3 {
			return nil, false, fmt.Errorf("want 3 arguments, got %d", len(args))
		}
		id, err := decodeID(args[0])
		if err != nil {
			return nil, false, err
		}
		var ratio float64
		if err := json.Unmarshal(args[2], &ratio); err != nil {
			return nil, false, err
		}
		period := strings.Trim(string(args[1]), `"`)
		return Uptime{MonitorID: id, Period: period, Ratio: ratio}, true, nil

	case "certInfo":
		if len(args) < 2 {
			return nil, false, fmt.Errorf("want 2 arguments, got %d", len(args))
		}
		id, err := decodeID(args[0])
		if err != nil {
			return nil, false, err
		}
		// The certificate comes as a JSON document inside a JSON string.
		var doc string
		if err := json.Unmarshal(args[1], &doc); err != nil {
			return nil, false, err
		}
		var tls struct {
			Valid    bool `json:"valid"`
			CertInfo struct {
				DaysRemaining int `json:"daysRemaining"`
			} `json:"certInfo"`
		}
		if err := json.Unmarshal([]byte(doc), &tls); err != nil {
			return nil, false, err
		}
		return CertInfo{MonitorID: id, Valid: tls.Valid, DaysRemaining: tls.CertInfo.DaysRemaining}, true, nil

	case "notificationList":
		a, err := arg(0)
		if err != nil {
			return nil, false, err
		}
		nl, err := decodeNotificationList(a)
		return nl, err == nil, err

	case "maintenanceList":
		a, err := arg(0)
		if err != nil {
			return nil, false, err
		}
		ml, err := decodeMaintenanceList(a)
		return ml, err == nil, err

	case "monitorTypeList":
		a, err := arg(0)
		if err != nil {
			return nil, false, err
		}
		mt, err := decodeMonitorTypes(a)
		return mt, err == nil, err

	case "info":
		a, err := arg(0)
		if err != nil {
			return nil, false, err
		}
		var info struct {
			Version string `json:"version"`
		}
		if err := json.Unmarshal(a, &info); err != nil {
			return nil, false, err
		}
		return Info{Version: info.Version}, true, nil
	}
	return nil, false, nil
}

type rawMonitor struct {
	ID          int      `json:"id"`
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	URL         string   `json:"url"`
	Hostname    string   `json:"hostname"`
	Port        int      `json:"port"`
	Active      flexBool `json:"active"`
	Maintenance flexBool `json:"maintenance"`
	Parent      int      `json:"parent"`
	Interval    int      `json:"interval"`
}

func decodeMonitors(a json.RawMessage) (map[int]Monitor, error) {
	var raw map[string]rawMonitor
	if err := json.Unmarshal(a, &raw); err != nil {
		return nil, err
	}
	out := make(map[int]Monitor, len(raw))
	for key, r := range raw {
		id := r.ID
		if id == 0 {
			n, err := strconv.Atoi(key)
			if err != nil {
				return nil, fmt.Errorf("monitor key %q", key)
			}
			id = n
		}
		out[id] = Monitor{
			ID: id, Name: r.Name, Type: r.Type, URL: r.URL, Hostname: r.Hostname, Port: r.Port,
			Active: bool(r.Active), Maintenance: bool(r.Maintenance), Parent: r.Parent, Interval: r.Interval,
		}
	}
	return out, nil
}

type rawBeat struct {
	MonitorID      *int     `json:"monitorID"`  // live heartbeat
	MonitorIDSnake *int     `json:"monitor_id"` // heartbeatList entries
	Status         int      `json:"status"`
	Time           string   `json:"time"`
	Msg            string   `json:"msg"`
	Ping           *float64 `json:"ping"`
	Important      flexBool `json:"important"`
}

// beatTime is how Kuma writes a heartbeat's time, in UTC. Go's parser takes
// the milliseconds after the seconds without them being in the layout.
const beatTime = "2006-01-02 15:04:05"

func decodeBeat(a json.RawMessage) (Beat, error) {
	var r rawBeat
	if err := json.Unmarshal(a, &r); err != nil {
		return Beat{}, err
	}
	t, err := time.ParseInLocation(beatTime, r.Time, time.UTC)
	if err != nil {
		return Beat{}, fmt.Errorf("beat time: %w", err)
	}
	b := Beat{Status: Status(r.Status), Time: t, Msg: r.Msg, Important: bool(r.Important)}
	switch {
	case r.MonitorID != nil:
		b.MonitorID = *r.MonitorID
	case r.MonitorIDSnake != nil:
		b.MonitorID = *r.MonitorIDSnake
	}
	if r.Ping != nil {
		b.Ping, b.HasPing = *r.Ping, true
	}
	return b, nil
}

// decodeID reads a monitor id that Kuma sends as a number or as a string.
func decodeID(a json.RawMessage) (int, error) {
	s := strings.Trim(string(a), `"`)
	id, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("monitor id %s", a)
	}
	return id, nil
}

// flexBool reads true/false as well as the 1/0 SQLite leaves in some fields.
type flexBool bool

func (b *flexBool) UnmarshalJSON(data []byte) error {
	switch string(data) {
	case "true", "1":
		*b = true
	case "false", "0", "null":
		*b = false
	default:
		return fmt.Errorf("not a boolean: %s", data)
	}
	return nil
}

// Notification is a channel Kuma sends alerts through.
type Notification struct {
	ID        int
	Name      string
	Type      string
	IsDefault bool
	Active    bool
	// Config holds the provider's own fields exactly as Kuma stores them,
	// name, type and isDefault included; it is what an edit sends back.
	Config map[string]any
}

// Maintenance is a window during which monitors stay quiet.
type Maintenance struct {
	ID          int
	Title       string
	Description string
	Strategy    string // "manual" or "single"
	Status      string // "under-maintenance", "scheduled", "ended", "inactive"
	Active      bool
	Start, End  string // "single" only, as Kuma writes them
	Timezone    string
}

type (
	// NotificationList is every channel, sent on connect and after a change.
	NotificationList struct{ Notifications []Notification }
	// MaintenanceList is every maintenance window, by id.
	MaintenanceList struct{ Maintenances map[int]Maintenance }
	// MonitorTypes is the monitor types this server supports, sorted.
	MonitorTypes struct{ Types []string }
)

func (NotificationList) event() {}
func (MaintenanceList) event()  {}
func (MonitorTypes) event()     {}

func decodeNotificationList(a json.RawMessage) (NotificationList, error) {
	var raw []struct {
		ID        int      `json:"id"`
		Name      string   `json:"name"`
		Active    flexBool `json:"active"`
		IsDefault flexBool `json:"isDefault"`
		Config    string   `json:"config"`
	}
	if err := json.Unmarshal(a, &raw); err != nil {
		return NotificationList{}, err
	}
	out := NotificationList{Notifications: make([]Notification, 0, len(raw))}
	for _, r := range raw {
		n := Notification{ID: r.ID, Name: r.Name, Active: bool(r.Active), IsDefault: bool(r.IsDefault)}
		// config is a JSON document inside a JSON string, and the provider
		// name lives in it rather than in a column.
		if err := json.Unmarshal([]byte(r.Config), &n.Config); err != nil {
			return NotificationList{}, fmt.Errorf("notification %d config: %w", r.ID, err)
		}
		if t, ok := n.Config["type"].(string); ok {
			n.Type = t
		}
		out.Notifications = append(out.Notifications, n)
	}
	return out, nil
}

func decodeMaintenanceList(a json.RawMessage) (MaintenanceList, error) {
	var raw map[string]struct {
		ID          int      `json:"id"`
		Title       string   `json:"title"`
		Description string   `json:"description"`
		Strategy    string   `json:"strategy"`
		Status      string   `json:"status"`
		Active      flexBool `json:"active"`
		DateRange   []string `json:"dateRange"`
		Timezone    string   `json:"timezone"`
	}
	if err := json.Unmarshal(a, &raw); err != nil {
		return MaintenanceList{}, err
	}
	out := MaintenanceList{Maintenances: make(map[int]Maintenance, len(raw))}
	for _, r := range raw {
		m := Maintenance{
			ID: r.ID, Title: r.Title, Description: r.Description, Strategy: r.Strategy,
			Status: r.Status, Active: bool(r.Active), Timezone: r.Timezone,
		}
		if len(r.DateRange) > 0 {
			m.Start = r.DateRange[0]
		}
		if len(r.DateRange) > 1 {
			m.End = r.DateRange[1]
		}
		out.Maintenances[m.ID] = m
	}
	return out, nil
}

// ClassicTypes are the monitor types Kuma implements outside the registry
// it reports in monitorTypeList: http and its friends are missing from that
// list, so any type picker must offer the union of both.
var ClassicTypes = []string{"docker", "http", "json-query", "keyword", "ping", "push"}

// All is every type this server can monitor: what it reported plus the
// classic ones it leaves out, sorted and without repeats.
func (m MonitorTypes) All() []string {
	out := append(append([]string{}, m.Types...), ClassicTypes...)
	sort.Strings(out)
	return slices.Compact(out)
}

func decodeMonitorTypes(a json.RawMessage) (MonitorTypes, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(a, &raw); err != nil {
		return MonitorTypes{}, err
	}
	out := MonitorTypes{Types: make([]string, 0, len(raw))}
	for t := range raw {
		out.Types = append(out.Types, t)
	}
	sort.Strings(out.Types)
	return out, nil
}
