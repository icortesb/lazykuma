package kuma

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// StatusPage is one of Kuma's public status pages. Config is every setting
// Kuma sent, as it sent it: saveStatusPage replaces the page with what it
// receives, so an edit starts from all of it.
type StatusPage struct {
	ID          int
	Slug        string
	Title       string
	Description string
	Published   bool
	Config      map[string]any
}

// StatusPageList is every status page, sent once after login. Kuma does not
// send it again after a change; the client keeps it current itself.
type StatusPageList struct{ Pages []StatusPage }

func (StatusPageList) event() {}

// PageMonitor is a monitor shown on a status page. SendURL and URL are the
// page's own link for it, kept as they are on a save.
type PageMonitor struct {
	ID      int
	Name    string
	SendURL bool
	URL     string
}

// PageSection is a named group of monitors on a status page.
type PageSection struct {
	ID       int // 0 for a section Kuma has not stored yet
	Name     string
	Monitors []PageMonitor
}

// PageIncident is the message pinned at the top of a status page.
type PageIncident struct {
	ID               int
	Title, Content   string
	Style            string // one of IncidentStyles
	Pinned           bool
	Created, Updated string // as Kuma writes them
}

// PublicPage is what a status page shows its visitors: its sections and
// its incidents. Kuma gives the sections only here.
type PublicPage struct {
	Sections  []PageSection
	Incidents []PageIncident
}

// IncidentStyles are the looks Kuma offers an incident, in its order.
var IncidentStyles = []string{"info", "warning", "danger", "primary", "light", "dark"}

var slugRule = regexp.MustCompile(`^[A-Za-z0-9]+(?:-[A-Za-z0-9]+)*$`)

// ValidSlug reports whether Kuma accepts s as a page's slug.
func ValidSlug(s string) bool { return slugRule.MatchString(s) }

// SlugFrom suggests a slug for a title: lower case, and every run of other
// characters one dash.
func SlugFrom(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			dash = false
			continue
		}
		dash = true
	}
	return b.String()
}

// PageURL is where a status page is published: base's scheme, host and any
// path prefix a reverse proxy needs, with /status/<slug> appended. Base's
// own query string, if it carries one for that proxy, is kept; its
// fragment is dropped. Parsing base first, rather than gluing strings, is
// what keeps a path prefix and a query string from landing in the wrong
// place (see FetchPublicPage). If base does not parse, the strings are
// joined as before, malformed as that path may end up.
func PageURL(base, slug string) string {
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil {
		return strings.TrimRight(strings.TrimSpace(base), "/") + "/status/" + slug
	}
	u = u.JoinPath("status", slug)
	u.Fragment, u.RawFragment = "", ""
	return u.String()
}

func decodeStatusPage(raw json.RawMessage) (StatusPage, error) {
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return StatusPage{}, err
	}
	var typed struct {
		ID          int      `json:"id"`
		Slug        string   `json:"slug"`
		Title       string   `json:"title"`
		Description string   `json:"description"`
		Published   flexBool `json:"published"`
	}
	if err := json.Unmarshal(raw, &typed); err != nil {
		return StatusPage{}, err
	}
	return StatusPage{ID: typed.ID, Slug: typed.Slug, Title: typed.Title, Description: typed.Description,
		Published: bool(typed.Published), Config: cfg}, nil
}

func decodeStatusPageList(a json.RawMessage) (StatusPageList, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(a, &raw); err != nil {
		return StatusPageList{}, err
	}
	out := StatusPageList{Pages: make([]StatusPage, 0, len(raw))}
	for _, r := range raw {
		p, err := decodeStatusPage(r)
		if err != nil {
			return StatusPageList{}, err
		}
		out.Pages = append(out.Pages, p)
	}
	sort.Slice(out.Pages, func(i, j int) bool {
		ti, tj := strings.ToLower(out.Pages[i].Title), strings.ToLower(out.Pages[j].Title)
		if ti != tj {
			return ti < tj
		}
		return out.Pages[i].Slug < out.Pages[j].Slug
	})
	return out, nil
}

// AddStatusPage creates an empty page and returns its slug as Kuma stored
// it, in lower case.
func (s *Session) AddStatusPage(ctx context.Context, title, slug string) (string, error) {
	raw, err := s.emit(ctx, "addStatusPage", title, slug)
	if err != nil {
		return "", err
	}
	var r struct {
		OK   bool   `json:"ok"`
		Msg  string `json:"msg"`
		Slug string `json:"slug"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw[0], &r); err != nil {
			return "", fmt.Errorf("kuma: addStatusPage reply: %w", err)
		}
	}
	if !r.OK {
		return "", &ReplyError{Msg: r.Msg}
	}
	return r.Slug, nil
}

// GetStatusPage is a page's settings, all of them. Its sections are not
// among them: FetchPublicPage has those.
func (s *Session) GetStatusPage(ctx context.Context, slug string) (StatusPage, error) {
	raw, err := s.emit(ctx, "getStatusPage", slug)
	if err != nil {
		return StatusPage{}, err
	}
	var r struct {
		OK     bool            `json:"ok"`
		Msg    string          `json:"msg"`
		Config json.RawMessage `json:"config"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw[0], &r); err != nil {
			return StatusPage{}, fmt.Errorf("kuma: getStatusPage reply: %w", err)
		}
	}
	if !r.OK {
		return StatusPage{}, &ReplyError{Msg: r.Msg}
	}
	return decodeStatusPage(r.Config)
}

// SaveStatusPage replaces a page's settings and sections with these. The
// logo is sent back as the page has it, which keeps it. This also flushes
// Kuma's public-page cache, so a visitor browsing the page directly (with
// no cache-busting query string, unlike FetchPublicPage) sees the change
// immediately too.
func (s *Session) SaveStatusPage(ctx context.Context, slug string, config map[string]any, sections []PageSection) error {
	icon, _ := config["icon"].(string)
	if icon == "" {
		icon = "/icon.svg" // Kuma's own default
	}
	groups := make([]map[string]any, 0, len(sections))
	for _, sec := range sections {
		mons := make([]map[string]any, 0, len(sec.Monitors))
		for _, m := range sec.Monitors {
			mon := map[string]any{"id": m.ID}
			if m.SendURL {
				mon["sendUrl"] = true
				if m.URL != "" {
					mon["url"] = m.URL
				}
			}
			mons = append(mons, mon)
		}
		g := map[string]any{"name": sec.Name, "monitorList": mons}
		if sec.ID != 0 {
			g["id"] = sec.ID
		}
		groups = append(groups, g)
	}
	r, err := s.call(ctx, "saveStatusPage", slug, config, icon, groups)
	if err != nil {
		return err
	}
	return r.err()
}

// DeleteStatusPage removes a page, its sections and its incidents. This
// also flushes Kuma's public-page cache, so a visitor holding a stale copy
// of the page stops seeing it.
func (s *Session) DeleteStatusPage(ctx context.Context, slug string) error {
	r, err := s.call(ctx, "deleteStatusPage", slug)
	if err != nil {
		return err
	}
	return r.err()
}

// PostIncident pins an incident to a page, or edits the one with inc.ID.
// FetchPublicPage sees the change immediately (it bypasses Kuma's
// public-page cache); a visitor's browser can still show Kuma's cached page
// for up to 5 minutes, since this does not flush that cache the way
// SaveStatusPage and DeleteStatusPage do.
func (s *Session) PostIncident(ctx context.Context, slug string, inc PageIncident) (PageIncident, error) {
	body := map[string]any{"title": inc.Title, "content": inc.Content, "style": inc.Style}
	if inc.ID != 0 {
		body["id"] = inc.ID
	}
	raw, err := s.emit(ctx, "postIncident", slug, body)
	if err != nil {
		return PageIncident{}, err
	}
	var r struct {
		OK       bool            `json:"ok"`
		Msg      string          `json:"msg"`
		Incident json.RawMessage `json:"incident"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw[0], &r); err != nil {
			return PageIncident{}, fmt.Errorf("kuma: postIncident reply: %w", err)
		}
	}
	if !r.OK {
		return PageIncident{}, &ReplyError{Msg: r.Msg}
	}
	return decodeIncident(r.Incident)
}

// UnpinIncident takes a page's incident down: Kuma drops it from
// FetchPublicPage's Incidents entirely rather than keeping it there
// unpinned, and FetchPublicPage sees this immediately. Like PostIncident,
// this does not flush Kuma's public-page cache, so a visitor's browser can
// still show it pinned for up to 5 minutes.
func (s *Session) UnpinIncident(ctx context.Context, slug string) error {
	r, err := s.call(ctx, "unpinIncident", slug)
	if err != nil {
		return err
	}
	return r.err()
}

func decodeIncident(raw json.RawMessage) (PageIncident, error) {
	var r struct {
		ID      int      `json:"id"`
		Title   string   `json:"title"`
		Content string   `json:"content"`
		Style   string   `json:"style"`
		Pin     flexBool `json:"pin"`
		Created string   `json:"createdDate"`
		Updated string   `json:"lastUpdatedDate"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return PageIncident{}, err
	}
	return PageIncident{ID: r.ID, Title: r.Title, Content: r.Content, Style: r.Style,
		Pinned: bool(r.Pin), Created: r.Created, Updated: r.Updated}, nil
}

// FetchPublicPage reads a status page the way its visitors do, over plain
// HTTP from the instance: it is the only place Kuma gives a page's
// sections. Kuma caches this endpoint server-side for 5 minutes, keyed on
// the full request URL including its query string (apicache's default
// key, which Kuma does not override for this route); FetchPublicPage adds
// a unique one on every call to always bypass that cache, so what it
// returns is always current, even right after PostIncident or
// UnpinIncident, neither of which flushes the cache the way SaveStatusPage
// and DeleteStatusPage do. A visitor's browser, with no such query string
// of its own, can still see Kuma's cached copy for up to 5 minutes.
//
// base is parsed first, rather than glued onto the path as a string: base
// may carry its own path prefix (a reverse proxy) or query string (say, a
// proxy's own token), and gluing strings would fold either of those into
// the wrong place, or worse, turn a "?" in base into part of the path.
// Any existing query parameters are kept alongside the cache-busting one.
func FetchPublicPage(ctx context.Context, base, slug string) (PublicPage, error) {
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil {
		return PublicPage{}, fmt.Errorf("kuma: status page %s: %w", slug, err)
	}
	u = u.JoinPath("api", "status-page", slug)
	q := u.Query()
	q.Set("lazykuma", strconv.FormatInt(time.Now().UnixNano(), 10))
	u.RawQuery = q.Encode()
	u.Fragment, u.RawFragment = "", ""
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return PublicPage{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return PublicPage{}, fmt.Errorf("kuma: status page %s: %w", slug, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return PublicPage{}, fmt.Errorf("kuma: status page %s: %s", slug, resp.Status)
	}
	var r struct {
		Incidents []json.RawMessage `json:"incidents"`
		Groups    []struct {
			ID       int    `json:"id"`
			Name     string `json:"name"`
			Monitors []struct {
				ID      int      `json:"id"`
				Name    string   `json:"name"`
				SendURL flexBool `json:"sendUrl"`
				URL     string   `json:"url"`
			} `json:"monitorList"`
		} `json:"publicGroupList"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return PublicPage{}, fmt.Errorf("kuma: status page %s: %w", slug, err)
	}
	var out PublicPage
	for _, g := range r.Groups {
		sec := PageSection{ID: g.ID, Name: g.Name}
		for _, m := range g.Monitors {
			sec.Monitors = append(sec.Monitors, PageMonitor{ID: m.ID, Name: m.Name, SendURL: bool(m.SendURL), URL: m.URL})
		}
		out.Sections = append(out.Sections, sec)
	}
	for _, raw := range r.Incidents {
		inc, err := decodeIncident(raw)
		if err != nil {
			return PublicPage{}, fmt.Errorf("kuma: status page %s incident: %w", slug, err)
		}
		out.Incidents = append(out.Incidents, inc)
	}
	return out, nil
}
