//go:build integration

package kuma

import (
	"context"
	"testing"
	"time"
)

// TestIntegrationStatusPages checks the status page calls phase C adds
// against a real Kuma: creating a page, saving its settings and sections,
// reading it back the way a visitor does, and posting and unpinning an
// incident.
func TestIntegrationStatusPages(t *testing.T) {
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

	monitorID, err := s.AddMonitor(ctx, RawMonitor{
		"type": "http", "name": "it-page-web", "url": "https://example.com", "method": "GET",
		"interval": 60, "retryInterval": 60, "maxretries": 0,
		"accepted_statuscodes": []string{"200-299"}, "notificationIDList": map[string]bool{},
		"conditions": []any{}, "kafkaProducerBrokers": []any{}, "kafkaProducerSaslOptions": map[string]any{},
	})
	if err != nil {
		t.Fatalf("add monitor: %v", err)
	}
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer ccancel()
		if err := s.DeleteMonitor(cctx, monitorID); err != nil {
			t.Logf("cleanup: delete monitor %d: %v", monitorID, err)
		}
	})

	slug, err := s.AddStatusPage(ctx, "It Page", "It-Page")
	if err != nil {
		t.Fatalf("AddStatusPage: %v", err)
	}
	if slug != "it-page" {
		t.Fatalf("AddStatusPage slug = %q, want %q", slug, "it-page")
	}
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer ccancel()
		if err := s.DeleteStatusPage(cctx, slug); err != nil {
			// The test itself deletes the page further down; a leftover
			// "already deleted" complaint here is fine.
			t.Logf("cleanup: delete status page %q: %v", slug, err)
		}
	})

	if _, err := s.AddStatusPage(ctx, "Bad", "bad slug"); err == nil {
		t.Error("a bad slug was accepted")
	}

	page, err := s.GetStatusPage(ctx, slug)
	if err != nil {
		t.Fatalf("GetStatusPage: %v", err)
	}
	if page.Title != "It Page" || page.Config["icon"] != "/icon.svg" {
		t.Fatalf("GetStatusPage = %+v", page)
	}

	page.Config["description"] = "from lazykuma"
	page.Config["customCSS"] = "body{color:red}"
	if err := s.SaveStatusPage(ctx, slug, page.Config, []PageSection{
		{Name: "Services", Monitors: []PageMonitor{{ID: monitorID}}},
	}); err != nil {
		t.Fatalf("SaveStatusPage: %v", err)
	}

	page, err = s.GetStatusPage(ctx, slug)
	if err != nil {
		t.Fatalf("GetStatusPage after save: %v", err)
	}
	if page.Config["description"] != "from lazykuma" || page.Config["customCSS"] != "body{color:red}" {
		t.Fatalf("GetStatusPage after save = %+v", page.Config)
	}

	pub, err := FetchPublicPage(ctx, url, slug)
	if err != nil {
		t.Fatalf("FetchPublicPage: %v", err)
	}
	if len(pub.Sections) != 1 || pub.Sections[0].Name != "Services" || len(pub.Sections[0].Monitors) != 1 ||
		pub.Sections[0].Monitors[0].ID != monitorID || pub.Sections[0].Monitors[0].Name != "it-page-web" {
		t.Fatalf("FetchPublicPage sections = %+v", pub.Sections)
	}
	sectionID := pub.Sections[0].ID

	// Saving again with the section's own id updates it in place rather than
	// creating a second one.
	if err := s.SaveStatusPage(ctx, slug, page.Config, []PageSection{
		{ID: sectionID, Name: "Core", Monitors: []PageMonitor{{ID: monitorID}}},
	}); err != nil {
		t.Fatalf("SaveStatusPage (rename section): %v", err)
	}
	pub, err = FetchPublicPage(ctx, url, slug)
	if err != nil {
		t.Fatalf("FetchPublicPage after rename: %v", err)
	}
	if len(pub.Sections) != 1 || pub.Sections[0].ID != sectionID || pub.Sections[0].Name != "Core" {
		t.Fatalf("FetchPublicPage after rename = %+v", pub.Sections)
	}

	current := []PageSection{{ID: sectionID, Name: "Core", Monitors: []PageMonitor{{ID: monitorID}}}}

	inc, err := s.PostIncident(ctx, slug, PageIncident{Title: "Down", Content: "We are on it", Style: "danger"})
	if err != nil {
		t.Fatalf("PostIncident: %v", err)
	}
	if inc.ID == 0 || !inc.Pinned {
		t.Fatalf("PostIncident = %+v", inc)
	}
	// Kuma 2.5.3 caches the public status-page endpoint for 5 minutes and
	// only saveStatusPage and deleteStatusPage flush that cache; postIncident
	// and unpinIncident do not, so a visitor's page can lag behind an
	// incident by up to 5 minutes. Resave the page's own settings, unchanged,
	// to flush the cache without waiting it out.
	if err := s.SaveStatusPage(ctx, slug, page.Config, current); err != nil {
		t.Fatalf("SaveStatusPage (flush after PostIncident): %v", err)
	}
	pub, err = FetchPublicPage(ctx, url, slug)
	if err != nil {
		t.Fatalf("FetchPublicPage after PostIncident: %v", err)
	}
	found := false
	for _, got := range pub.Incidents {
		if got.ID == inc.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("public page incidents = %+v, want %d among them", pub.Incidents, inc.ID)
	}

	if err := s.UnpinIncident(ctx, slug); err != nil {
		t.Fatalf("UnpinIncident: %v", err)
	}
	if err := s.SaveStatusPage(ctx, slug, page.Config, current); err != nil {
		t.Fatalf("SaveStatusPage (flush after UnpinIncident): %v", err)
	}
	pub, err = FetchPublicPage(ctx, url, slug)
	if err != nil {
		t.Fatalf("FetchPublicPage after UnpinIncident: %v", err)
	}
	// Kuma drops an unpinned incident from the public feed entirely, rather
	// than keeping it there unpinned.
	if len(pub.Incidents) != 0 {
		t.Fatalf("public page incidents after UnpinIncident = %+v, want none", pub.Incidents)
	}

	if err := s.DeleteStatusPage(ctx, slug); err != nil {
		t.Fatalf("DeleteStatusPage: %v", err)
	}
	if _, err := FetchPublicPage(ctx, url, slug); err == nil {
		t.Fatal("FetchPublicPage still answers for a deleted status page")
	}
}
