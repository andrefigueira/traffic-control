package main

import (
	"runtime"
	"strings"
	"testing"

	"github.com/andrefigueira/traffic-control/internal/protocol"
)

func TestNotifyMessage(t *testing.T) {
	t.Run("conflict", func(t *testing.T) {
		ev := protocol.Event{Type: protocol.EventConflictAlert, Payload: map[string]interface{}{
			"requester": "bravo", "path": "x.go", "held_by": "alpha",
		}}
		title, body, ok := notifyMessage(ev)
		if !ok {
			t.Fatal("a conflict should be surfaced")
		}
		if !strings.Contains(title, "conflict") {
			t.Fatalf("title = %q", title)
		}
		for _, want := range []string{"bravo", "x.go", "alpha"} {
			if !strings.Contains(body, want) {
				t.Fatalf("body %q missing %q", body, want)
			}
		}
	})
	t.Run("advisory overlap", func(t *testing.T) {
		ev := protocol.Event{Type: protocol.EventAdvisoryOverlap, Payload: map[string]interface{}{
			"requester": "bravo", "path": "x.go", "held_by": "alpha",
		}}
		_, _, ok := notifyMessage(ev)
		if !ok {
			t.Fatal("an overlap should be surfaced")
		}
	})
	t.Run("uninteresting events are skipped", func(t *testing.T) {
		for _, typ := range []string{protocol.EventBoardPosted, protocol.EventPresenceJoin, protocol.EventClearanceGranted} {
			ev := protocol.Event{Type: typ, Payload: map[string]interface{}{"callsign": "a"}}
			if _, _, ok := notifyMessage(ev); ok {
				t.Fatalf("%s should not fire a notification", typ)
			}
		}
	})
	t.Run("non-map payload is safe", func(t *testing.T) {
		if _, _, ok := notifyMessage(protocol.Event{Type: protocol.EventConflictAlert, Payload: "nope"}); ok {
			t.Fatal("a non-map payload should not fire")
		}
	})
}

func TestNotifyCommand(t *testing.T) {
	name, args := notifyCommand("Title", "Body")
	switch runtime.GOOS {
	case "darwin":
		if name != "osascript" {
			t.Fatalf("darwin should use osascript, got %q", name)
		}
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "display notification") || !strings.Contains(joined, "Body") || !strings.Contains(joined, "Title") {
			t.Fatalf("osascript args missing pieces: %v", args)
		}
	case "linux":
		if name != "notify-send" || args[0] != "Title" || args[1] != "Body" {
			t.Fatalf("linux notify-send args = %q %v", name, args)
		}
	default:
		if name != "" {
			t.Fatalf("unsupported platform should yield no command, got %q", name)
		}
	}
}
