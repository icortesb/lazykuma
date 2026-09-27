package kuma

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"
)

const pageConfig = `{"id":1,"slug":"shop-status","title":"Shop status","description":null,"icon":"/icon.svg","theme":"auto","autoRefreshInterval":300,"published":true,"showTags":false,"domainNameList":[],"customCSS":"body{}","footerText":null,"showPoweredBy":true,"analyticsId":null,"analyticsScriptUrl":null,"analyticsType":null,"showCertificateExpiry":false,"showOnlyLastHeartbeat":false,"rssTitle":null}`

// pageFake answers the status page calls the way Kuma 2.5.3 did when probed.
// The returned *url.Values is the query string the fake's public-page route
// last saw, so a test can check FetchPublicPage's cache-busting parameter.
func pageFake(t *testing.T) (*fakeKuma, *Session, *url.Values) {
	t.Helper()
	login := kumaLogin(false)
	var lastQuery url.Values
	f := newFakeKuma(t, func(event string, args []json.RawMessage) any {
		switch event {
		case "addStatusPage":
			var title, slug string
			json.Unmarshal(args[0], &title)
			json.Unmarshal(args[1], &slug)
			if !ValidSlug(slug) {
				return map[string]any{"ok": false, "msg": "Invalid Slug"}
			}
			return map[string]any{"ok": true, "msg": "successAdded", "msgi18n": true, "slug": "shop-status"}
		case "getStatusPage":
			return json.RawMessage(`{"ok":true,"config":` + pageConfig + `}`)
		case "saveStatusPage":
			return json.RawMessage(`{"ok":true,"publicGroupList":[]}`)
		case "postIncident":
			var inc map[string]any
			json.Unmarshal(args[1], &inc)
			inc["id"], inc["pin"], inc["createdDate"] = 3, true, "2026-09-27 16:13:25"
			return map[string]any{"ok": true, "incident": inc}
		case "unpinIncident", "deleteStatusPage":
			return map[string]any{"ok": true}
		}
		return login(event, args)
	})
	f.Handle("/api/status-page/shop-status", func(w http.ResponseWriter, r *http.Request) {
		lastQuery = r.URL.Query()
		w.Write([]byte(`{"config":{"slug":"shop-status"},"incidents":[{"id":3,"style":"danger","title":"Down","content":"We are on it","pin":true,"active":true,"createdDate":"2026-09-27 16:13:25","lastUpdatedDate":null,"status_page_id":1}],"publicGroupList":[{"id":1,"name":"Services","weight":1,"monitorList":[{"id":1,"name":"web","sendUrl":0,"type":"http"},{"id":2,"name":"api","sendUrl":1,"type":"http","url":"https://api.example.com"}]}],"maintenanceList":[]}`))
	})
	s := dial(t, f)
	if err := s.LoginByToken(context.Background(), "jwt"); err != nil {
		t.Fatal(err)
	}
	return f, s, &lastQuery
}

func TestSlugs(t *testing.T) {
	for in, want := range map[string]string{
		"Shop status":         "shop-status",
		"  Día de la API!!  ": "d-a-de-la-api",
		"already-good":        "already-good",
		"---":                 "",
	} {
		if got := SlugFrom(in); got != want {
			t.Errorf("SlugFrom(%q) = %q, want %q", in, got, want)
		}
	}
	for s, ok := range map[string]bool{"shop-status": true, "Shop-2": true, "a": true, "-a": false, "a-": false, "a--b": false, "a b": false, "": false} {
		if ValidSlug(s) != ok {
			t.Errorf("ValidSlug(%q) = %v", s, !ok)
		}
	}
	if got := PageURL("https://kuma.example.com/", "shop-status"); got != "https://kuma.example.com/status/shop-status" {
		t.Errorf("PageURL = %q", got)
	}
}

// TestPageURLBases checks that PageURL parses base rather than gluing
// strings onto it, so a path prefix (a reverse proxy) and a query string (say
// a proxy's own token) both land in the right place instead of the path
// swallowing the query or the query swallowing the path.
func TestPageURLBases(t *testing.T) {
	for _, tc := range []struct {
		name, base, want string
	}{
		{"bare", "https://kuma.example.com", "https://kuma.example.com/status/shop-status"},
		{"path prefix, trailing slash", "https://kuma.example.com/kuma/", "https://kuma.example.com/kuma/status/shop-status"},
		{"query string", "https://kuma.example.com/kuma?token=abc", "https://kuma.example.com/kuma/status/shop-status?token=abc"},
	} {
		if got := PageURL(tc.base, "shop-status"); got != tc.want {
			t.Errorf("PageURL(%q) = %q, want %q", tc.base, got, tc.want)
		}
	}
}

// TestFetchPublicPageBases checks the same for FetchPublicPage: it hits the
// right path on Kuma (a reverse proxy's prefix kept) and keeps any query
// string base already carries alongside the cache-busting one it adds.
func TestFetchPublicPageBases(t *testing.T) {
	var gotPath string
	var gotQuery url.Values
	respond := func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.Query()
		w.Write([]byte(`{"config":{"slug":"it-page"},"incidents":[],"publicGroupList":[],"maintenanceList":[]}`))
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status-page/it-page", respond)
	mux.HandleFunc("/kuma/api/status-page/it-page", respond)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	for _, tc := range []struct {
		name, base, wantPath string
		wantQuery            map[string]string // besides the cache-busting parameter
	}{
		{"bare", srv.URL, "/api/status-page/it-page", nil},
		{"path prefix, trailing slash", srv.URL + "/kuma/", "/kuma/api/status-page/it-page", nil},
		{"query string", srv.URL + "/kuma?token=abc", "/kuma/api/status-page/it-page", map[string]string{"token": "abc"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotPath, gotQuery = "", nil
			if _, err := FetchPublicPage(context.Background(), tc.base, "it-page"); err != nil {
				t.Fatal(err)
			}
			if gotPath != tc.wantPath {
				t.Errorf("path = %q, want %q", gotPath, tc.wantPath)
			}
			if gotQuery.Get("lazykuma") == "" {
				t.Errorf("no cache-busting query parameter: %v", gotQuery)
			}
			for k, want := range tc.wantQuery {
				if got := gotQuery.Get(k); got != want {
					t.Errorf("query %s = %q, want %q", k, got, want)
				}
			}
		})
	}
}

func TestDecodeStatusPageList(t *testing.T) {
	list := `{"2":` + `{"id":2,"slug":"docs","title":"Docs","published":false}` + `,"1":` + pageConfig + `}`
	ev, ok, err := DecodeEvent("statusPageList", []json.RawMessage{json.RawMessage(list)})
	if err != nil || !ok {
		t.Fatalf("decode: %v %v", ok, err)
	}
	pages := ev.(StatusPageList).Pages
	if len(pages) != 2 || pages[0].Slug != "docs" || pages[1].Slug != "shop-status" {
		t.Fatalf("pages = %+v", pages)
	}
	p := pages[1]
	if p.ID != 1 || p.Title != "Shop status" || !p.Published || p.Config["customCSS"] != "body{}" {
		t.Fatalf("page = %+v", p)
	}
}

func TestStatusPageCalls(t *testing.T) {
	f, s, lastQuery := pageFake(t)
	ctx := context.Background()

	slug, err := s.AddStatusPage(ctx, "Shop status", "shop-status")
	if err != nil || slug != "shop-status" || !f.Sent(`["addStatusPage","Shop status","shop-status"]`) {
		t.Fatalf("AddStatusPage = %q, %v; frames %v", slug, err, f.Frames())
	}
	if _, err := s.AddStatusPage(ctx, "Bad", "bad slug"); err == nil {
		t.Error("a bad slug was accepted")
	}

	page, err := s.GetStatusPage(ctx, "shop-status")
	if err != nil || page.Title != "Shop status" || page.Config["customCSS"] != "body{}" {
		t.Fatalf("GetStatusPage = %+v, %v", page, err)
	}

	sections := []PageSection{
		{ID: 1, Name: "Services", Monitors: []PageMonitor{{ID: 1, Name: "web"}, {ID: 2, Name: "api", SendURL: true, URL: "https://api.example.com"}}},
		{Name: "New", Monitors: []PageMonitor{{ID: 3, Name: "db"}}},
	}
	page.Config["description"] = "All systems"
	if err := s.SaveStatusPage(ctx, "shop-status", page.Config, sections); err != nil {
		t.Fatal(err)
	}
	// The logo goes back as it was; the settings Kuma keeps and the form
	// does not show (custom CSS) go back untouched; sections in order, a
	// new one without an id, a monitor's own URL kept.
	for _, want := range []string{
		`"saveStatusPage","shop-status",`,
		`"customCSS":"body{}"`, `"description":"All systems"`,
		`,"/icon.svg",[{"id":1,"monitorList":[{"id":1},{"id":2,"sendUrl":true,"url":"https://api.example.com"}],"name":"Services"},{"monitorList":[{"id":3}],"name":"New"}]]`,
	} {
		if !f.Sent(want) {
			t.Errorf("save lacks %s: %v", want, f.Frames())
		}
	}

	inc, err := s.PostIncident(ctx, "shop-status", PageIncident{Title: "Down", Content: "We are on it", Style: "danger"})
	if err != nil || inc.ID != 3 || !inc.Pinned || !f.Sent(`["postIncident","shop-status",{"content":"We are on it","style":"danger","title":"Down"}]`) {
		t.Fatalf("PostIncident = %+v, %v; frames %v", inc, err, f.Frames())
	}
	if _, err := s.PostIncident(ctx, "shop-status", PageIncident{ID: 3, Title: "Fixed", Content: "ok", Style: "info"}); err != nil || !f.Sent(`{"content":"ok","id":3,"style":"info","title":"Fixed"}`) {
		t.Fatalf("edit incident: %v; frames %v", err, f.Frames())
	}
	if err := s.UnpinIncident(ctx, "shop-status"); err != nil || !f.Sent(`["unpinIncident","shop-status"]`) {
		t.Fatalf("UnpinIncident: %v", err)
	}
	if err := s.DeleteStatusPage(ctx, "shop-status"); err != nil || !f.Sent(`["deleteStatusPage","shop-status"]`) {
		t.Fatalf("DeleteStatusPage: %v", err)
	}

	pub, err := FetchPublicPage(ctx, f.URL(), "shop-status")
	if err != nil {
		t.Fatal(err)
	}
	wantSections := []PageSection{{ID: 1, Name: "Services", Monitors: []PageMonitor{{ID: 1, Name: "web"}, {ID: 2, Name: "api", SendURL: true, URL: "https://api.example.com"}}}}
	if len(pub.Sections) != 1 || pub.Sections[0].Name != "Services" || !slices.Equal(pub.Sections[0].Monitors, wantSections[0].Monitors) {
		t.Fatalf("sections = %+v", pub.Sections)
	}
	if len(pub.Incidents) != 1 || pub.Incidents[0] != (PageIncident{ID: 3, Title: "Down", Content: "We are on it", Style: "danger", Pinned: true, Created: "2026-09-27 16:13:25"}) {
		t.Fatalf("incidents = %+v", pub.Incidents)
	}
	// A unique query string is Kuma's own cache-busting mechanism (apicache
	// keys on the full request URL); FetchPublicPage relies on it to always
	// read a fresh page rather than a cached one.
	if lastQuery.Get("lazykuma") == "" {
		t.Errorf("FetchPublicPage sent no cache-busting query parameter: %v", *lastQuery)
	}
	if _, err := FetchPublicPage(ctx, f.URL(), "missing"); err == nil {
		t.Error("a missing page is not an error")
	}
}
