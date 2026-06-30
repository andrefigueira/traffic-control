package tower

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/andrefigueira/traffic-control/internal/protocol"
)

func TestBoardSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.json")

	tw := New()
	tw.EnablePersistence(path)
	tw.PostBoard("alpha", "", protocol.KindFlightPlan, "reworking auth", []string{"auth/"})
	tw.PostBoard("bravo", "", protocol.KindDone, "migrations done", nil)

	// A fresh tower pointed at the same file reloads the board, newest last.
	restarted := New()
	restarted.EnablePersistence(path)
	got := restarted.ReadBoard(0)
	if len(got) != 2 {
		t.Fatalf("expected 2 reloaded entries, got %d", len(got))
	}
	if got[0].Message != "reworking auth" || got[1].Callsign != "bravo" {
		t.Fatalf("reloaded board out of order: %+v", got)
	}
}

func TestPersistenceMissingFileStartsEmpty(t *testing.T) {
	tw := New()
	tw.EnablePersistence(filepath.Join(t.TempDir(), "nope.json"))
	if len(tw.ReadBoard(0)) != 0 {
		t.Fatal("a missing snapshot should start empty")
	}
}

func TestPersistenceCorruptFileRecovers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0o644); err != nil {
		t.Fatal(err)
	}
	tw := New()
	tw.EnablePersistence(path) // must not panic
	if len(tw.ReadBoard(0)) != 0 {
		t.Fatal("a corrupt snapshot should start empty")
	}
	// A later post overwrites the corrupt file cleanly, restoring durability.
	tw.PostBoard("a", "", protocol.KindNote, "hi", nil)
	restarted := New()
	restarted.EnablePersistence(path)
	if len(restarted.ReadBoard(0)) != 1 {
		t.Fatal("the board should persist again after recovering from corruption")
	}
}

func TestPersistenceCapsOnLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.json")
	var entries []protocol.BoardEntry
	for i := 0; i < maxBoardEntries+25; i++ {
		entries = append(entries, protocol.BoardEntry{Message: "m"})
	}
	b, _ := json.Marshal(entries)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	tw := New()
	tw.EnablePersistence(path)
	if got := len(tw.ReadBoard(0)); got != maxBoardEntries {
		t.Fatalf("load should cap at %d, got %d", maxBoardEntries, got)
	}
}

func TestPersistenceOffByDefault(t *testing.T) {
	// New() alone never touches disk; PostBoard must not error or persist.
	tw := New()
	tw.PostBoard("a", "", protocol.KindNote, "hi", nil)
	if len(tw.ReadBoard(0)) != 1 {
		t.Fatal("in-memory board should still work without persistence")
	}
}
