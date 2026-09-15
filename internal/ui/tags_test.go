package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestTagsScreenListsAndCreates(t *testing.T) {
	h := onInstance(t, twoMonitors())
	h.press("t")
	v := h.view()
	if !strings.Contains(v, "home · tags") || !strings.Contains(v, "region") {
		t.Fatalf("no tags screen:\n%s", v)
	}
	h.press("n")
	h.typeText("prod")
	h.press("tab")                         // to the colour row
	h.send(tea.KeyMsg{Type: tea.KeyRight}) // gray → red
	h.press("enter")
	f := h.fakes["home"]
	if !f.Sent(`["addTag",{"color":"#DC2626","name":"prod"}]`) {
		t.Fatalf("tag not sent: %v", f.Frames())
	}
	if !strings.Contains(h.view(), "created tag prod") || !strings.Contains(h.view(), "home · tags") {
		t.Errorf("did not return to the tags:\n%s", h.view())
	}
}

func TestTagsEditAndDelete(t *testing.T) {
	h := onInstance(t, twoMonitors())
	h.press("t", "e")
	h.m.tform.fields[0].SetValue("area")
	h.press("enter")
	if !h.fakes["home"].Sent(`["editTag",{"id":4,"name":"area","color":"#2563EB"}]`) {
		t.Fatalf("edit not sent: %v", h.fakes["home"].Frames())
	}
	h.press("d")
	if !strings.Contains(h.view(), `Delete the tag "region"?`) {
		t.Fatalf("no confirmation:\n%s", h.view())
	}
	h.press("y")
	if !h.fakes["home"].Sent(`["deleteTag",4]`) {
		t.Fatalf("delete not sent: %v", h.fakes["home"].Frames())
	}
}
