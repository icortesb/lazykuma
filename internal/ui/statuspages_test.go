package ui

import (
	"errors"
	"strings"
	"testing"

	"github.com/icortesb/lazykuma/internal/kuma"
	"github.com/icortesb/lazykuma/internal/state"
)

func withPages(st state.Instance) state.Instance {
	return state.Apply(st, kuma.StatusPageList{Pages: []kuma.StatusPage{
		{ID: 7, Slug: "shop-status", Title: "Shop status", Published: true, Config: map[string]any{"id": float64(7), "slug": "shop-status", "title": "Shop status", "icon": "/icon.svg", "customCSS": "body{}"}},
	}}, tBase)
}

func TestPagesListAndOpen(t *testing.T) {
	var opened string
	defer func(f func(string) error) { openURL = f }(openURL)
	openURL = func(u string) error { opened = u; return nil }

	h := onInstance(t, withPages(twoMonitors()))
	h.press("S")
	v := h.view()
	for _, want := range []string{"home · status pages", "Shop status", "shop-status", "published", "/status/shop-status"} {
		if !strings.Contains(v, want) {
			t.Errorf("list lacks %q:\n%s", want, v)
		}
	}
	h.press("o")
	if !strings.HasSuffix(opened, "/status/shop-status") || !strings.Contains(h.view(), "opened") {
		t.Fatalf("opened %q:\n%s", opened, h.view())
	}
	openURL = func(string) error { return errors.New("no browser") }
	h.press("o")
	if !strings.Contains(h.view(), "yourself") {
		t.Errorf("a failed open does not say where to go:\n%s", h.view())
	}
	h.press("esc")
	if h.m.screen != screenInstance {
		t.Fatalf("esc: %v", h.m.screen)
	}
}

func TestPagesCreateSuggestsTheSlug(t *testing.T) {
	h := onInstance(t, twoMonitors())
	h.press("S", "n")
	h.typeText("Shop Status")
	if got := h.m.pnew.fields[1].Value(); got != "shop-status" {
		t.Fatalf("suggested slug = %q", got)
	}
	h.press("tab")
	h.typeText("-eu") // the user takes over the slug
	h.press("shift+tab")
	h.typeText("!")
	if got := h.m.pnew.fields[1].Value(); got != "shop-status-eu" {
		t.Fatalf("slug after the user edited it = %q", got)
	}
	h.press("enter", "enter")
	if !h.fakes["home"].Sent(`["addStatusPage","Shop Status!","shop-status-eu"]`) {
		t.Fatalf("not created: %v", h.fakes["home"].Frames())
	}
	if h.m.screen != screenPages || !strings.Contains(h.view(), "created status page") {
		t.Errorf("after create: %v\n%s", h.m.screen, h.view())
	}
}

func TestPagesCreateRejectsABadSlug(t *testing.T) {
	h := onInstance(t, twoMonitors())
	h.press("S", "n")
	h.typeText("Shop")
	h.press("tab")
	h.m.pnew.fields[1].SetValue("bad slug")
	h.press("enter")
	if !strings.Contains(h.view(), "can't be a slug") || h.fakes["home"].Called("addStatusPage") {
		t.Fatalf("bad slug:\n%s", h.view())
	}
}

func TestPagesDeleteNeedsTheSlug(t *testing.T) {
	h := onInstance(t, withPages(twoMonitors()))
	h.press("S", "d")
	if !strings.Contains(h.view(), `Delete the status page "Shop status"?`) {
		t.Fatalf("no confirmation:\n%s", h.view())
	}
	h.typeText("shop")
	h.press("enter")
	if h.fakes["home"].Called("deleteStatusPage") || !strings.Contains(h.view(), "type shop-status to delete it") {
		t.Fatalf("deleted on a wrong slug:\n%s", h.view())
	}
	h.m.pdel.fields[0].SetValue("shop-status")
	h.press("enter")
	if !h.fakes["home"].Sent(`["deleteStatusPage","shop-status"]`) || h.m.screen != screenPages {
		t.Fatalf("delete: %v %v", h.m.screen, h.fakes["home"].Frames())
	}
}
