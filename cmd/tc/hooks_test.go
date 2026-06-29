package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrefigueira/traffic-control/internal/protocol"
)

func TestBuildSessionContextIncludesHeldClearances(t *testing.T) {
	sessions := []protocol.Session{
		{Callsign: "claude-me", Project: "p"},
		{Callsign: "claude-other", Project: "p"},
	}
	clearances := []protocol.Clearance{
		{Path: "internal/api/server.go", Holder: "claude-other", Mode: protocol.ModeExclusive},
		{Path: "mine.go", Holder: "claude-me", Mode: protocol.ModeAdvisory},
	}
	board := []protocol.BoardEntry{
		{Callsign: "claude-other", Kind: protocol.KindFlightPlan, Message: "doing stuff"},
	}
	out := buildSessionContext("claude-me", sessions, clearances, board)

	if !strings.Contains(out, "claude-other") {
		t.Error("should mention the other flying agent")
	}
	if !strings.Contains(out, "Files currently held") {
		t.Error("should announce held files")
	}
	if !strings.Contains(out, "internal/api/server.go held by claude-other (exclusive)") {
		t.Errorf("should name the held path and holder; got:\n%s", out)
	}
	if strings.Contains(out, "mine.go") {
		t.Error("should not warn the agent about its own holds")
	}
	if !strings.Contains(out, "doing stuff") {
		t.Error("should include board activity")
	}
}

func TestBuildSessionContextEmpty(t *testing.T) {
	out := buildSessionContext("solo", nil, nil, nil)
	if !strings.Contains(out, "No other agents are currently checked in") {
		t.Errorf("empty context should say nobody is checked in; got:\n%s", out)
	}
}

// TestRelativizeAbsAndRelMatch locks in the fix for the absolute-vs-relative
// hole: the same physical file passed as an absolute path or as a path relative
// to the session cwd must normalize to the same clearance key.
func TestRelativizeAbsAndRelMatch(t *testing.T) {
	// Resolve symlinks on the temp dir up front, because on macOS t.TempDir()
	// lives under /var, which is itself a symlink to /private/var.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	abs := filepath.Join(dir, "src", "a.go")

	fromRel := relativize("src/a.go", dir)
	fromAbs := relativize(abs, dir)

	if fromRel != fromAbs {
		t.Fatalf("abs and rel forms should match: rel=%q abs=%q", fromRel, fromAbs)
	}
	if fromRel != "src/a.go" {
		t.Fatalf("expected src/a.go, got %q", fromRel)
	}
}

// TestRelativizeNewFileUnderSymlinkedCwd guards the real-world case the previous
// test masked: a not-yet-created file (a fresh Write) under a cwd that is itself
// a symlink (the macOS default for anything under /var or /tmp). The cwd is
// deliberately NOT pre-resolved, exactly as Claude passes it.
func TestRelativizeNewFileUnderSymlinkedCwd(t *testing.T) {
	cwd := t.TempDir() // unresolved on purpose
	abs := filepath.Join(cwd, "src", "new.go")

	fromRel := relativize("src/new.go", cwd) // file does not exist yet
	fromAbs := relativize(abs, cwd)

	if fromRel != "src/new.go" {
		t.Fatalf("a new file under a symlinked cwd should key relative, got %q", fromRel)
	}
	if fromRel != fromAbs {
		t.Fatalf("abs and rel of a new file must match: rel=%q abs=%q", fromRel, fromAbs)
	}
}
