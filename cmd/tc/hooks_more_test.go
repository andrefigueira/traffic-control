package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrefigueira/traffic-control/internal/protocol"
)

func TestHookCallsign(t *testing.T) {
	t.Run("derives from a long session id, truncated", func(t *testing.T) {
		got := hookCallsign(hookInput{SessionID: "abcdefghijklmnopqrstuvwxyz"})
		if got != "claude-abcdefghijkl" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("short session id is used whole", func(t *testing.T) {
		if got := hookCallsign(hookInput{SessionID: "abc"}); got != "claude-abc" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("falls back to resolveCallsign without a session id", func(t *testing.T) {
		t.Setenv("TC_CALLSIGN", "human")
		if got := hookCallsign(hookInput{}); got != "human" {
			t.Fatalf("got %q, want the resolved fallback", got)
		}
	})
}

func TestHookProject(t *testing.T) {
	if got := hookProject(hookInput{Cwd: "/home/me/code/myproj"}); got != "myproj" {
		t.Fatalf("got %q", got)
	}
	if got := hookProject(hookInput{}); got != "" {
		t.Fatalf("empty cwd should give empty project, got %q", got)
	}
}

func TestEvalBestEffort(t *testing.T) {
	if got := evalBestEffort(""); got != "" {
		t.Fatalf("empty in, empty out; got %q", got)
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// An existing directory resolves to itself once symlinks are evaluated.
	if got := evalBestEffort(dir); got != dir {
		t.Fatalf("existing dir: got %q want %q", got, dir)
	}
	// A not-yet-created file resolves its existing ancestor and re-appends the
	// missing tail rather than failing outright.
	missing := filepath.Join(dir, "nope", "new.go")
	if got := evalBestEffort(missing); got != missing {
		t.Fatalf("missing tail: got %q want %q", got, missing)
	}
}

func TestEmitSessionContext(t *testing.T) {
	out := captureStdout(t, func() { emitSessionContext("hello world") })
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("not json: %v (%s)", err, out)
	}
	hso := m["hookSpecificOutput"].(map[string]interface{})
	if hso["hookEventName"] != "SessionStart" || hso["additionalContext"] != "hello world" {
		t.Fatalf("payload = %+v", hso)
	}
}

func TestEmitPreToolDeny(t *testing.T) {
	out := captureStdout(t, func() { emitPreToolDeny("no") })
	var m map[string]interface{}
	_ = json.Unmarshal([]byte(out), &m)
	hso := m["hookSpecificOutput"].(map[string]interface{})
	if hso["permissionDecision"] != "deny" || hso["permissionDecisionReason"] != "no" {
		t.Fatalf("payload = %+v", hso)
	}
}

func TestEmitPreToolContext(t *testing.T) {
	out := captureStdout(t, func() { emitPreToolContext("ctx") })
	var m map[string]interface{}
	_ = json.Unmarshal([]byte(out), &m)
	hso := m["hookSpecificOutput"].(map[string]interface{})
	if hso["additionalContext"] != "ctx" {
		t.Fatalf("payload = %+v", hso)
	}
	if _, hasDecision := hso["permissionDecision"]; hasDecision {
		t.Fatal("context injection must not carry a permission decision")
	}
}

func TestCmdHookArgValidation(t *testing.T) {
	setStdin(t, "")
	if err := cmdHook(nil); err == nil {
		t.Fatal("expected an error with no hook event")
	}
	setStdin(t, "{}")
	// An unknown event is a no-op, never an error: hooks must never break the agent.
	if err := cmdHook([]string{"frobnicate"}); err != nil {
		t.Fatalf("unknown event should not error, got %v", err)
	}
}

func TestHookSessionStartRegistersAndInjects(t *testing.T) {
	_, tw := startTower(t)
	out := captureStdout(t, func() {
		hookSessionStart(hookInput{SessionID: "sess123456789", Cwd: "/tmp/myproj"})
	})
	if !strings.Contains(out, "Traffic Control is active") {
		t.Fatalf("session context not injected: %q", out)
	}
	flying := tw.WhosFlying()
	if len(flying) != 1 || !strings.HasPrefix(flying[0].Callsign, "claude-sess") {
		t.Fatalf("agent not registered: %+v", flying)
	}
}

func TestHookPreToolUseIgnoresNonMutatingTools(t *testing.T) {
	_, tw := startTower(t)
	hookPreToolUse(hookInput{ToolName: "Read", ToolInput: json.RawMessage(`{"file_path":"x.go"}`)})
	if len(tw.Clearances()) != 0 {
		t.Fatal("a read should never take a clearance")
	}
}

func TestHookPreToolUseIgnoresEmptyPath(t *testing.T) {
	_, tw := startTower(t)
	hookPreToolUse(hookInput{ToolName: "Edit", ToolInput: json.RawMessage(`{}`)})
	if len(tw.Clearances()) != 0 {
		t.Fatal("no file_path means nothing to coordinate")
	}
}

func TestHookPreToolUseGrants(t *testing.T) {
	_, tw := startTower(t)
	in := hookInput{SessionID: "me", ToolName: "Edit", ToolInput: json.RawMessage(`{"file_path":"app.go"}`)}
	out := captureStdout(t, func() { hookPreToolUse(in) })
	if out != "" {
		t.Fatalf("a clean grant should emit nothing, got %q", out)
	}
	clrs := tw.Clearances()
	if len(clrs) != 1 || clrs[0].Path != "app.go" || clrs[0].Holder != "claude-me" {
		t.Fatalf("clearance = %+v", clrs)
	}
}

func TestHookPreToolUseAdvisoryInjectsContext(t *testing.T) {
	c, _ := startTower(t)
	// Another agent already holds the path, advisory.
	if _, err := c.RequestClearance(context.Background(), "other", "shared.go", protocol.ModeAdvisory, "", 0); err != nil {
		t.Fatal(err)
	}
	in := hookInput{SessionID: "me", ToolName: "Edit", ToolInput: json.RawMessage(`{"file_path":"shared.go"}`)}
	out := captureStdout(t, func() { hookPreToolUse(in) })
	if !strings.Contains(out, "Traffic Control:") {
		t.Fatalf("advisory overlap should inject context, got %q", out)
	}
	var m map[string]interface{}
	_ = json.Unmarshal([]byte(out), &m)
	hso := m["hookSpecificOutput"].(map[string]interface{})
	if _, hasDecision := hso["permissionDecision"]; hasDecision {
		t.Fatal("an advisory overlap must not deny the tool")
	}
}

func TestHookPreToolUseEnforceDenies(t *testing.T) {
	c, _ := startTower(t)
	t.Setenv("TC_ENFORCE", "1")
	if _, err := c.RequestClearance(context.Background(), "other", "locked.go", protocol.ModeAdvisory, "", 0); err != nil {
		t.Fatal(err)
	}
	in := hookInput{SessionID: "me", ToolName: "Write", ToolInput: json.RawMessage(`{"file_path":"locked.go"}`)}
	out := captureStdout(t, func() { hookPreToolUse(in) })
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("not json: %v (%s)", err, out)
	}
	hso := m["hookSpecificOutput"].(map[string]interface{})
	if hso["permissionDecision"] != "deny" {
		t.Fatalf("enforced conflict should deny, got %+v", hso)
	}
}

func TestHookPreToolUseTowerDownWarnsButAllows(t *testing.T) {
	deadAddr(t)
	t.Setenv("TC_NO_AUTOSTART", "1") // do not try to spawn a tower in a test
	in := hookInput{SessionID: "me", ToolName: "Edit", ToolInput: json.RawMessage(`{"file_path":"x.go"}`)}
	var stdout string
	stderr := captureStderr(t, func() {
		stdout = captureStdout(t, func() { hookPreToolUse(in) })
	})
	if stdout != "" {
		t.Fatalf("a degraded edit must not deny; stdout = %q", stdout)
	}
	if !strings.Contains(stderr, "not coordinated") {
		t.Fatalf("expected a degraded warning on stderr, got %q", stderr)
	}
}

func TestHookStopHandsOff(t *testing.T) {
	c, tw := startTower(t)
	if _, err := c.RequestClearance(context.Background(), "claude-me", "x.go", protocol.ModeExclusive, "", 0); err != nil {
		t.Fatal(err)
	}
	hookStop(hookInput{SessionID: "me"})
	if len(tw.Clearances()) != 0 {
		t.Fatalf("stop should hand off all holds, %d left", len(tw.Clearances()))
	}
}

func TestHookStopTowerDownWarns(t *testing.T) {
	deadAddr(t)
	stderr := captureStderr(t, func() { hookStop(hookInput{SessionID: "me"}) })
	if !strings.Contains(stderr, "hand off") {
		t.Fatalf("expected a handoff warning, got %q", stderr)
	}
}

func TestHookPostToolUseTowerDownIsSilent(t *testing.T) {
	deadAddr(t)
	out := captureStdout(t, func() {
		stderr := captureStderr(t, func() { hookPostToolUse(hookInput{SessionID: "me"}) })
		if stderr != "" {
			t.Fatalf("post-tool-use must stay silent when the tower is down, stderr=%q", stderr)
		}
	})
	if out != "" {
		t.Fatalf("post-tool-use must not write stdout, got %q", out)
	}
}

func TestHookPostToolUseRefreshesLease(t *testing.T) {
	c, tw := startTower(t)
	ctx := context.Background()
	// Heartbeat only refreshes a known session, so the agent must be checked in
	// first, exactly as PreToolUse does before it ever holds a path.
	if _, err := c.Register(ctx, "claude-me", "p", 0); err != nil {
		t.Fatal(err)
	}
	r, err := c.RequestClearance(ctx, "claude-me", "x.go", protocol.ModeExclusive, "", 60)
	if err != nil {
		t.Fatal(err)
	}
	before := r.Clearance.ExpiresAt
	hookPostToolUse(hookInput{SessionID: "me"})
	after := tw.Clearances()[0].ExpiresAt
	if !after.After(before) {
		t.Fatalf("heartbeat should extend the lease: before %v after %v", before, after)
	}
}
