package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/andrefigueira/traffic-control/internal/protocol"
	"github.com/andrefigueira/traffic-control/internal/tower"
)

func writeEvents(t *testing.T, path string, events []protocol.Event) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, e := range events {
		if err := enc.Encode(e); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBuildReport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	now := time.Now()
	writeEvents(t, path, []protocol.Event{
		{Type: protocol.EventPresenceJoin, At: now, Payload: map[string]interface{}{"callsign": "alpha"}},
		{Type: protocol.EventClearanceGranted, At: now.Add(time.Second), Payload: map[string]interface{}{"holder": "alpha", "path": "x.go"}},
		{Type: protocol.EventConflictAlert, At: now.Add(2 * time.Second), Payload: map[string]interface{}{"requester": "bravo", "held_by": "alpha", "path": "x.go"}},
		{Type: protocol.EventBoardPosted, At: now.Add(3 * time.Second), Payload: map[string]interface{}{"callsign": "alpha"}},
	})

	rep, err := buildReport(path)
	if err != nil {
		t.Fatal(err)
	}
	if rep.events != 4 {
		t.Fatalf("events = %d, want 4", rep.events)
	}
	if rep.byType[protocol.EventConflictAlert] != 1 || rep.byType[protocol.EventClearanceGranted] != 1 {
		t.Fatalf("type counts = %+v", rep.byType)
	}
	if len(rep.callsigns) != 2 || !rep.callsigns["alpha"] || !rep.callsigns["bravo"] {
		t.Fatalf("callsigns = %v", rep.callsigns)
	}
	if !rep.last.After(rep.first) {
		t.Fatalf("window first %v last %v", rep.first, rep.last)
	}
}

func TestBuildReportMissingFileIsEmpty(t *testing.T) {
	rep, err := buildReport(filepath.Join(t.TempDir(), "nope.jsonl"))
	if err != nil {
		t.Fatalf("missing log should not error: %v", err)
	}
	if rep.events != 0 {
		t.Fatalf("missing log should be empty, got %d", rep.events)
	}
}

func TestBuildReportSkipsTornLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "e.jsonl")
	good := `{"type":"presence.join","at":"` + time.Now().Format(time.RFC3339) + `","payload":{"callsign":"a"}}`
	if err := os.WriteFile(path, []byte(good+"\n{ this line is torn\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := buildReport(path)
	if err != nil {
		t.Fatal(err)
	}
	if rep.events != 1 {
		t.Fatalf("a torn line should be skipped, events = %d", rep.events)
	}
}

// TestStreamEventLogCaptures drives the real logger against a real tower and
// confirms published events land in the activity log.
func TestStreamEventLogCaptures(t *testing.T) {
	tw := tower.New()
	path := filepath.Join(t.TempDir(), "events.jsonl")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { streamEventLog(ctx, tw, path); close(done) }()

	// Wait until the logger has subscribed before publishing.
	deadline := time.Now().Add(2 * time.Second)
	for tw.Broker().Count() == 0 {
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("logger never subscribed")
		}
		time.Sleep(time.Millisecond)
	}
	tw.Register("alpha", "p", 0)
	tw.RequestClearance("alpha", "", "x.go", protocol.ModeExclusive, "", 0)

	deadline = time.Now().Add(2 * time.Second)
	for {
		rep, _ := buildReport(path)
		if rep.byType[protocol.EventPresenceJoin] >= 1 && rep.byType[protocol.EventClearanceGranted] >= 1 {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("log did not capture events: %+v", rep.byType)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
}
