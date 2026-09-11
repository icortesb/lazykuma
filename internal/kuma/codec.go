// Package kuma talks to an Uptime Kuma v2 server over its Socket.IO API.
//
// It speaks the Engine.IO v4 and Socket.IO v5 framing itself, over a plain
// websocket: Kuma needs text frames, events and acks, which is too little to
// be worth a Socket.IO library.
package kuma

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Frames the client sends that carry no data.
const (
	framePong    = "3"
	frameConnect = "40"
)

type packetKind int

const (
	kindOther        packetKind = iota
	kindOpen                    // 0{"sid":…,"pingInterval":…,"pingTimeout":…}
	kindClose                   // 1, or 41 for the namespace
	kindPing                    // 2
	kindConnect                 // 40{"sid":…}
	kindConnectError            // 44{"message":…}
	kindEvent                   // 42["name",args…]
	kindAck                     // 43<id>[reply…]
)

type packet struct {
	kind  packetKind
	data  string            // the JSON after the type: open, connect, connect error
	event string            // kindEvent
	ackID int               // kindAck
	args  []json.RawMessage // kindEvent: the arguments after the name; kindAck: the reply
}

func decode(frame string) (packet, error) {
	if frame == "" {
		return packet{}, errors.New("empty frame")
	}
	switch frame[0] {
	case '0':
		return packet{kind: kindOpen, data: frame[1:]}, nil
	case '1':
		return packet{kind: kindClose}, nil
	case '2':
		return packet{kind: kindPing}, nil
	case '4':
		// A Socket.IO packet, below.
	default:
		return packet{kind: kindOther}, nil
	}
	if len(frame) < 2 {
		return packet{}, fmt.Errorf("short frame %q", frame)
	}
	body := frame[2:]
	switch frame[1] {
	case '0':
		return packet{kind: kindConnect, data: body}, nil
	case '1':
		return packet{kind: kindClose}, nil
	case '4':
		return packet{kind: kindConnectError, data: body}, nil
	case '2':
		// Kuma never asks the client for an ack, but an id is legal here.
		_, arr, err := splitID(body)
		if err != nil {
			return packet{}, err
		}
		if len(arr) == 0 {
			return packet{}, fmt.Errorf("event without a name: %q", frame)
		}
		var name string
		if err := json.Unmarshal(arr[0], &name); err != nil {
			return packet{}, fmt.Errorf("event name: %w", err)
		}
		return packet{kind: kindEvent, event: name, args: arr[1:]}, nil
	case '3':
		id, arr, err := splitID(body)
		if err != nil {
			return packet{}, err
		}
		return packet{kind: kindAck, ackID: id, args: arr}, nil
	}
	return packet{kind: kindOther}, nil
}

// splitID separates the optional numeric id written in front of a JSON array.
func splitID(body string) (int, []json.RawMessage, error) {
	i := strings.IndexByte(body, '[')
	if i < 0 {
		return 0, nil, fmt.Errorf("no payload in %q", body)
	}
	id := 0
	if i > 0 {
		n, err := strconv.Atoi(body[:i])
		if err != nil {
			return 0, nil, fmt.Errorf("bad id %q", body[:i])
		}
		id = n
	}
	var arr []json.RawMessage
	if err := json.Unmarshal([]byte(body[i:]), &arr); err != nil {
		return 0, nil, fmt.Errorf("payload: %w", err)
	}
	return id, arr, nil
}

// encodeEmit is an event the server answers with an ack of the same id.
func encodeEmit(ackID int, event string, args ...any) (string, error) {
	payload, err := json.Marshal(append([]any{event}, args...))
	if err != nil {
		return "", err
	}
	return "42" + strconv.Itoa(ackID) + string(payload), nil
}

type handshake struct {
	SID          string `json:"sid"`
	PingInterval int    `json:"pingInterval"` // milliseconds
	PingTimeout  int    `json:"pingTimeout"`  // milliseconds
}
