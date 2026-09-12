package kuma

import (
	"encoding/json"
	"testing"
)

func TestDecode(t *testing.T) {
	tests := []struct {
		name  string
		frame string
		want  packet
	}{
		{"open", `0{"sid":"a","pingInterval":25000,"pingTimeout":20000}`,
			packet{kind: kindOpen, data: `{"sid":"a","pingInterval":25000,"pingTimeout":20000}`}},
		{"ping", `2`, packet{kind: kindPing}},
		{"engine close", `1`, packet{kind: kindClose}},
		{"namespace close", `41`, packet{kind: kindClose}},
		{"connect", `40{"sid":"b"}`, packet{kind: kindConnect, data: `{"sid":"b"}`}},
		{"connect error", `44{"message":"nope"}`, packet{kind: kindConnectError, data: `{"message":"nope"}`}},
		{"event", `42["avgPing","1",62]`,
			packet{kind: kindEvent, event: "avgPing", args: raws(`"1"`, `62`)}},
		{"event without args", `42["setup"]`, packet{kind: kindEvent, event: "setup", args: raws()}},
		{"event with an id", `427["info",{}]`, packet{kind: kindEvent, event: "info", args: raws(`{}`)}},
		{"ack", `4312[{"ok":true}]`, packet{kind: kindAck, ackID: 12, args: raws(`{"ok":true}`)}},
		{"engine noop", `6`, packet{kind: kindOther}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decode(tt.frame)
			if err != nil {
				t.Fatalf("decode(%q): %v", tt.frame, err)
			}
			if got.kind != tt.want.kind || got.data != tt.want.data || got.event != tt.want.event || got.ackID != tt.want.ackID {
				t.Fatalf("decode(%q) = %+v, want %+v", tt.frame, got, tt.want)
			}
			if len(got.args) != len(tt.want.args) {
				t.Fatalf("decode(%q) args = %d, want %d", tt.frame, len(got.args), len(tt.want.args))
			}
			for i := range got.args {
				if string(got.args[i]) != string(tt.want.args[i]) {
					t.Errorf("arg %d = %s, want %s", i, got.args[i], tt.want.args[i])
				}
			}
		})
	}
}

func TestDecodeRejectsGarbage(t *testing.T) {
	for _, frame := range []string{"", "4", `42`, `42{}`, `43x[1]`, `42[1]`, `42[]`} {
		if _, err := decode(frame); err == nil {
			t.Errorf("decode(%q) = nil error, want one", frame)
		}
	}
}

func TestEncodeEmit(t *testing.T) {
	got, err := encodeEmit(3, "login", map[string]string{"username": "a"})
	if err != nil {
		t.Fatal(err)
	}
	if want := `423["login",{"username":"a"}]`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}

	// Round trip: what we send is what a server would decode.
	p, err := decode(got)
	if err != nil || p.kind != kindEvent || p.event != "login" {
		t.Fatalf("round trip = %+v, %v", p, err)
	}
}

func raws(ss ...string) []json.RawMessage {
	out := make([]json.RawMessage, len(ss))
	for i, s := range ss {
		out[i] = json.RawMessage(s)
	}
	return out
}
