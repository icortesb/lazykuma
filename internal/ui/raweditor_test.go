package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/icortesb/lazykuma/internal/kuma"
)

func typeInEditor(e rawEditor, s string) rawEditor {
	for _, r := range s {
		e, _, _ = e.Update(keyMsg(string(r)))
	}
	return e
}

// at moves the cursor onto the named field.
func at(t *testing.T, e rawEditor, key string) rawEditor {
	t.Helper()
	for i, k := range e.keys {
		if k == key {
			e.cursor = i
			return e
		}
	}
	t.Fatalf("no field %q in %v", key, e.keys)
	return e
}

func TestRawEditorEditsAValue(t *testing.T) {
	e := newRawEditor("monitor", 0, rawSkeleton("dns"))
	e = at(t, e, "name")
	e, _, _ = e.Update(keyMsg("enter"))
	if !e.editing {
		t.Fatal("enter did not open the value")
	}
	e, _, _ = e.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	e.input.SetValue(`"dns check"`)
	e, _, _ = e.Update(keyMsg("enter"))

	mon, err := e.Values()
	if err != nil {
		t.Fatal(err)
	}
	if mon["name"] != "dns check" || mon["type"] != "dns" {
		t.Fatalf("monitor = %v", mon)
	}
	// Numbers stay numbers, lists stay lists.
	if mon["interval"] != float64(60) {
		t.Errorf("interval = %T %v", mon["interval"], mon["interval"])
	}
	if codes, ok := mon["accepted_statuscodes"].([]any); !ok || codes[0] != "200-299" {
		t.Errorf("accepted = %T %v", mon["accepted_statuscodes"], mon["accepted_statuscodes"])
	}
}

func TestRawEditorRefusesValuesThatAreNotJSON(t *testing.T) {
	e := newRawEditor("monitor", 0, rawSkeleton("dns"))
	e = at(t, e, "name")
	e, _, _ = e.Update(keyMsg("enter"))
	e.input.SetValue("dns check") // unquoted: not JSON
	e, _, _ = e.Update(keyMsg("enter"))

	if !e.editing {
		t.Fatal("a bad value closed the editor anyway")
	}
	if !strings.Contains(e.err, "not JSON") {
		t.Fatalf("err = %q", e.err)
	}
	if !strings.Contains(ansi.Strip(e.View(80, 24)), "not JSON") {
		t.Error("the view does not say why")
	}
}

func TestRawEditorAddsAndRemovesFields(t *testing.T) {
	e := newRawEditor("monitor", 0, rawSkeleton("dns"))
	before := len(e.keys)

	e, _, _ = e.Update(keyMsg("a"))
	e = typeInEditor(e, "dns_resolve_server")
	e, _, _ = e.Update(keyMsg("enter"))
	if len(e.keys) != before+1 || e.vals["dns_resolve_server"] != `""` {
		t.Fatalf("keys = %v", e.keys)
	}
	// The cursor lands on the new field, ready to be filled.
	if e.keys[e.cursor] != "dns_resolve_server" {
		t.Fatalf("cursor on %q", e.keys[e.cursor])
	}
	e, _, _ = e.Update(keyMsg("enter"))
	e.input.SetValue(`"1.1.1.1"`)
	e, _, _ = e.Update(keyMsg("enter"))

	e = at(t, e, "timeout")
	e, _, _ = e.Update(keyMsg("d"))
	if _, still := e.vals["timeout"]; still {
		t.Fatal("d did not remove the field")
	}

	e = at(t, e, "name")
	e, _, _ = e.Update(keyMsg("enter"))
	e.input.SetValue(`"resolver"`)
	e, _, _ = e.Update(keyMsg("enter"))

	mon, err := e.Values()
	if err != nil {
		t.Fatal(err)
	}
	if mon["dns_resolve_server"] != "1.1.1.1" {
		t.Errorf("monitor = %v", mon)
	}
	if _, there := mon["timeout"]; there {
		t.Error("the deleted field came back")
	}
}

func TestRawEditorKeepsUnknownFieldsOfAnExistingMonitor(t *testing.T) {
	mon := kuma.RawMonitor{
		"id": float64(4), "type": "dns", "name": "resolver", "interval": float64(60),
		"dns_resolve_type": "A", "weight": float64(2000), "conditions": []any{},
	}
	e := newRawEditor("monitor", 0, mon)
	if e.id != 4 {
		t.Fatalf("id = %d", e.id)
	}
	out, err := e.Values()
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]any{"dns_resolve_type": "A", "weight": float64(2000), "id": float64(4)} {
		if out[k] != want {
			t.Errorf("%s = %v, want %v", k, out[k], want)
		}
	}
	if !strings.Contains(ansi.Strip(e.View(80, 24)), "Fields of monitor 4") {
		t.Error("the view does not name the monitor")
	}
}

func TestRawEditorNeedsANameAndAType(t *testing.T) {
	e := newRawEditor("monitor", 0, rawSkeleton("dns")) // name is empty
	if _, err := e.Values(); err == nil || !strings.Contains(err.Error(), "name is empty") {
		t.Fatalf("err = %v", err)
	}
	e = at(t, e, "type")
	e, _, _ = e.Update(keyMsg("enter"))
	e.input.SetValue(`""`)
	e, _, _ = e.Update(keyMsg("enter"))
	e = at(t, e, "name")
	e, _, _ = e.Update(keyMsg("enter"))
	e.input.SetValue(`"x"`)
	e, _, _ = e.Update(keyMsg("enter"))
	if _, err := e.Values(); err == nil || !strings.Contains(err.Error(), "type is empty") {
		t.Fatalf("err = %v", err)
	}
}

func TestRawEditorSavesAndCancels(t *testing.T) {
	e := newRawEditor("monitor", 0, rawSkeleton("dns"))
	if _, act, _ := e.Update(tea.KeyMsg{Type: tea.KeyCtrlS}); act != formSubmit {
		t.Fatalf("ctrl+s = %v", act)
	}
	if _, act, _ := e.Update(keyMsg("esc")); act != formCancel {
		t.Fatalf("esc = %v", act)
	}
	// esc while editing a value only closes the value.
	e, _, _ = e.Update(keyMsg("enter"))
	e2, act, _ := e.Update(keyMsg("esc"))
	if act != formNone || e2.editing {
		t.Fatalf("esc while editing = %v, editing %v", act, e2.editing)
	}
}
