package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestPickerChooses(t *testing.T) {
	p := newPicker("New monitor", "what does it watch?", []option{
		{kindLabel(kindHTTP), kindHTTP},
		{kindLabel(kindPing), kindPing},
		{"Other type…", "other"},
	})
	out := ansi.Strip(p.View(80, 24))
	for _, want := range []string{"New monitor", "what does it watch?", "HTTP — a URL answers", "Other type…"} {
		if !strings.Contains(out, want) {
			t.Errorf("view lacks %q:\n%s", want, out)
		}
	}

	p, act, v := p.Update(keyMsg("j"))
	if act != formNone {
		t.Fatalf("j = %v", act)
	}
	p, act, v = p.Update(keyMsg("enter"))
	if act != formSubmit || v != kindPing {
		t.Fatalf("enter = %v %q", act, v)
	}
	if _, act, _ = p.Update(keyMsg("esc")); act != formCancel {
		t.Error("esc did not cancel")
	}
	// The cursor stops at the ends rather than wrapping.
	p.cursor = 2
	if p, _, _ = p.Update(keyMsg("j")); p.cursor != 2 {
		t.Errorf("cursor ran past the end: %d", p.cursor)
	}
}

func TestConfirm(t *testing.T) {
	c := confirm{question: "Delete nextcloud?", detail: "its history goes with it"}
	out := ansi.Strip(c.View())
	if !strings.Contains(out, "Delete nextcloud?") || !strings.Contains(out, "history goes with it") {
		t.Errorf("view:\n%s", out)
	}
	for _, tt := range []struct {
		key           string
		answered, yes bool
	}{{"y", true, true}, {"n", true, false}, {"esc", true, false}, {"j", false, false}} {
		answered, yes := c.Update(keyMsg(tt.key))
		if answered != tt.answered || yes != tt.yes {
			t.Errorf("%s = %v %v, want %v %v", tt.key, answered, yes, tt.answered, tt.yes)
		}
	}
}
