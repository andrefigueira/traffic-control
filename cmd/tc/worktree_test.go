package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHookWorktreesDoNotCollide is the regression test for the worktree-collision
// bug: two agents in separate working trees editing the same relative path must
// not be treated as conflicting, since the files are physically distinct. A
// third agent in the SAME tree as the first still conflicts.
func TestHookWorktreesDoNotCollide(t *testing.T) {
	startTower(t)
	t.Setenv("TC_ENFORCE", "1") // hard mode, so a false collision would deny

	// Two independent git repos stand in for two worktrees: each has its own
	// toplevel, exactly as `git worktree add` produces.
	a := initGitRepo(t)
	b := initGitRepo(t)
	for _, dir := range []string{a, b} {
		if err := os.MkdirAll(filepath.Join(dir, "internal"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "internal", "x.go"), []byte("package x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	edit := func(session, dir string) string {
		in := hookInput{
			SessionID: session, Cwd: dir, ToolName: "Edit",
			ToolInput: json.RawMessage(`{"file_path":"internal/x.go"}`),
		}
		return captureStdout(t, func() { hookPreToolUse(in) })
	}

	if out := edit("wtA", a); strings.Contains(out, "deny") {
		t.Fatalf("first agent should be granted, got %q", out)
	}
	// The crux: a separate worktree editing its own internal/x.go must NOT be denied.
	if out := edit("wtB", b); strings.Contains(out, "deny") {
		t.Fatalf("a separate worktree must not collide, got a denial: %q", out)
	}
	// But a different agent in the SAME tree as A still conflicts.
	if out := edit("wtA2", a); !strings.Contains(out, "deny") {
		t.Fatalf("same worktree, same path should still conflict, got %q", out)
	}
}
