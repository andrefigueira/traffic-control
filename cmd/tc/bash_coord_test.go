package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/andrefigueira/traffic-control/internal/protocol"
)

// initGitRepo makes a throwaway git repo with one committed file, isolated from
// the user's global git config so signing or default-branch settings cannot
// derail it.
func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "seed")
	return dir
}

func TestGitDirtySet(t *testing.T) {
	if gitDirtySet("") != nil {
		t.Fatal("empty cwd should yield nil")
	}
	if gitDirtySet(t.TempDir()) != nil {
		t.Fatal("a non-git directory should yield nil")
	}
	dir := initGitRepo(t)
	if got := gitDirtySet(dir); got == nil || len(got) != 0 {
		t.Fatalf("a clean repo should yield an empty set, got %v", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "new.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := gitDirtySet(dir)
	if !got["new.go"] || !got["seed.txt"] {
		t.Fatalf("expected new.go and seed.txt as dirty, got %v", got)
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	t.Setenv("TC_STATE_DIR", t.TempDir())
	dir := initGitRepo(t)
	in := hookInput{SessionID: "snap", Cwd: dir}
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	snapshotBashState(in)
	got := readBashSnapshot(in)
	if !got["a.go"] {
		t.Fatalf("snapshot should record the dirty file, got %v", got)
	}
	removeBashSnapshot(in)
	if len(readBashSnapshot(in)) != 0 {
		t.Fatal("snapshot should be gone after removal")
	}
}

func TestCoordinateBashChangesClaimsChangedFiles(t *testing.T) {
	c, tw := startTower(t)
	t.Setenv("TC_STATE_DIR", t.TempDir())
	dir := initGitRepo(t)
	in := hookInput{SessionID: "bash-sess", Cwd: dir}

	snapshotBashState(in) // clean baseline
	// Simulate what a Bash command (sed -i, a formatter, a codemod) would do.
	if err := os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("rewritten\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gen.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	coordinateBashChanges(context.Background(), c, "claude-bash-sess", in)

	held := map[string]bool{}
	for _, cl := range tw.Clearances() {
		held[cl.Path] = true
		if cl.Holder != "claude-bash-sess" {
			t.Fatalf("clearance held by %q, want claude-bash-sess", cl.Holder)
		}
		if cl.Mode != protocol.ModeAdvisory {
			t.Fatalf("a Bash-coordinated hold should be advisory, got %q", cl.Mode)
		}
	}
	if !held["seed.txt"] || !held["gen.go"] {
		t.Fatalf("expected clearances for the changed files, got %v", held)
	}
}

func TestCoordinateBashChangesSkipsAlreadyDirty(t *testing.T) {
	c, tw := startTower(t)
	t.Setenv("TC_STATE_DIR", t.TempDir())
	dir := initGitRepo(t)
	in := hookInput{SessionID: "s2", Cwd: dir}

	// already.go is dirty before the Bash command runs, so it is in the snapshot.
	if err := os.WriteFile(filepath.Join(dir, "already.go"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	snapshotBashState(in)
	if err := os.WriteFile(filepath.Join(dir, "fresh.go"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}

	coordinateBashChanges(context.Background(), c, "claude-s2", in)

	held := map[string]bool{}
	for _, cl := range tw.Clearances() {
		held[cl.Path] = true
	}
	if held["already.go"] {
		t.Fatal("a file already dirty before the command should not be re-claimed")
	}
	if !held["fresh.go"] {
		t.Fatal("the newly changed file should be claimed")
	}
}

func TestCoordinateBashChangesOutsideGitIsSafe(t *testing.T) {
	c, tw := startTower(t)
	t.Setenv("TC_STATE_DIR", t.TempDir())
	in := hookInput{SessionID: "ng", Cwd: t.TempDir()} // not a git repo

	snapshotBashState(in)                                           // no-op, writes nothing
	coordinateBashChanges(context.Background(), c, "claude-ng", in) // must not panic
	if len(tw.Clearances()) != 0 {
		t.Fatal("nothing should be claimed outside a git repo")
	}
}

func TestCoordinateBashChangesCapsClaims(t *testing.T) {
	c, tw := startTower(t)
	t.Setenv("TC_STATE_DIR", t.TempDir())
	dir := initGitRepo(t)
	in := hookInput{SessionID: "flood", Cwd: dir}

	snapshotBashState(in) // clean baseline
	for i := 0; i < maxBashClaims+5; i++ {
		name := filepath.Join(dir, "f"+itoa(i)+".go")
		if err := os.WriteFile(name, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	coordinateBashChanges(context.Background(), c, "claude-flood", in)
	if got := len(tw.Clearances()); got != maxBashClaims {
		t.Fatalf("a flood of changes should be capped at %d, got %d", maxBashClaims, got)
	}
}

// itoa avoids importing strconv just for the flood test's filenames.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
