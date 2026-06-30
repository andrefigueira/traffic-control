package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/andrefigueira/traffic-control/internal/tower"
)

// eventLogPath is where the tower records its activity, one JSON event per line,
// for later review with `tc report`.
func eventLogPath() string { return filepath.Join(stateDir(), "events.jsonl") }

// streamEventLog subscribes to the tower's frequency and appends every event to
// the activity log until ctx is done. It runs off the tower lock (it is just
// another subscriber), and a write error is dropped rather than disrupting
// coordination, so logging can never wedge the tower. This is what lets a run be
// reconstructed afterwards: who flew, what was held, and every conflict caught.
func streamEventLog(ctx context.Context, tw *tower.Tower, path string) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()

	id, ch := tw.Broker().Subscribe()
	defer tw.Broker().Unsubscribe(id)

	enc := json.NewEncoder(f)
	for {
		select {
		case <-ctx.Done():
			return
		case ev, open := <-ch:
			if !open {
				return
			}
			_ = enc.Encode(ev)
		}
	}
}
