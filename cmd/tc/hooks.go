package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/andrefigueira/traffic-control/internal/client"
	"github.com/andrefigueira/traffic-control/internal/protocol"
)

// hookInput is the JSON Claude Code feeds a hook on stdin. We only read the
// fields we use.
type hookInput struct {
	SessionID     string          `json:"session_id"`
	Cwd           string          `json:"cwd"`
	HookEventName string          `json:"hook_event_name"`
	ToolName      string          `json:"tool_name"`
	ToolInput     json.RawMessage `json:"tool_input"`
	Source        string          `json:"source"`
}

// Guiding rule for every hook: never break the agent. If the tower is down, if
// the input is malformed, if anything goes sideways, we exit 0 and let the tool
// proceed. Coordination is an enhancement, not a gate that can wedge the agent.
func cmdHook(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: tc hook <session-start|pre-tool-use|stop>")
	}
	raw, _ := io.ReadAll(os.Stdin)
	var in hookInput
	_ = json.Unmarshal(raw, &in)

	switch args[0] {
	case "session-start", "SessionStart":
		hookSessionStart(in)
	case "pre-tool-use", "PreToolUse":
		hookPreToolUse(in)
	case "stop", "Stop":
		hookStop(in)
	default:
		// Unknown hook: do nothing, allow.
	}
	return nil
}

func hookCallsign(in hookInput) string {
	if in.SessionID != "" {
		// A short, readable callsign from the session id.
		s := in.SessionID
		if len(s) > 12 {
			s = s[:12]
		}
		return "claude-" + s
	}
	return resolveCallsign("")
}

func hookProject(in hookInput) string {
	if in.Cwd != "" {
		return filepath.Base(in.Cwd)
	}
	return ""
}

func hookClient() (*client.Client, context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	return client.FromEnv(), ctx, cancel
}

// hookSessionStart registers the agent and injects the current board into
// context, so a fresh agent immediately sees who else is working and on what.
func hookSessionStart(in hookInput) {
	c, ctx, cancel := hookClient()
	defer cancel()
	if err := c.Ping(ctx); err != nil {
		return // tower not running, stay silent
	}
	callsign := hookCallsign(in)
	_, _ = c.Register(ctx, callsign, hookProject(in), os.Getpid())

	sessions, _ := c.WhosFlying(ctx)
	board, _ := c.ReadBoard(ctx, 10)

	var b strings.Builder
	b.WriteString("Traffic Control is active. You are sharing this working tree with other agents.\n")
	b.WriteString(fmt.Sprintf("Your callsign: %s\n", callsign))

	others := 0
	for _, s := range sessions {
		if s.Callsign == callsign {
			continue
		}
		if others == 0 {
			b.WriteString("Currently flying:\n")
		}
		others++
		b.WriteString(fmt.Sprintf("  - %s (%s)\n", s.Callsign, s.Project))
	}
	if others == 0 {
		b.WriteString("No other agents are currently checked in.\n")
	}
	if len(board) > 0 {
		b.WriteString("Recent board activity:\n")
		for _, e := range board {
			b.WriteString(fmt.Sprintf("  - %s [%s] %s\n", e.Callsign, e.Kind, e.Message))
		}
	}
	b.WriteString("Before large edits, post a flight plan and the tower will warn others off your files.\n")

	emitSessionContext(b.String())
}

// hookPreToolUse requests clearance for file-mutating tools and blocks when a
// path is held under an exclusive clearance by another agent.
func hookPreToolUse(in hookInput) {
	switch in.ToolName {
	case "Edit", "Write", "MultiEdit", "NotebookEdit":
		// these carry a file_path we can coordinate on
	default:
		return // not a file mutation we track (Bash edits are a known gap)
	}

	var ti struct {
		FilePath string `json:"file_path"`
	}
	_ = json.Unmarshal(in.ToolInput, &ti)
	if ti.FilePath == "" {
		return
	}
	relPath := relativize(ti.FilePath, in.Cwd)

	c, ctx, cancel := hookClient()
	defer cancel()
	if err := c.Ping(ctx); err != nil {
		return // tower down: degrade gracefully, allow the edit
	}
	callsign := hookCallsign(in)
	_, _ = c.Register(ctx, callsign, hookProject(in), os.Getpid())

	mode := protocol.ModeAdvisory
	if os.Getenv("TC_ENFORCE") == "1" {
		mode = protocol.ModeExclusive
	}
	res, err := c.RequestClearance(ctx, callsign, relPath, mode, "editing", 0)
	if err != nil {
		return // could not reach tower mid-call: allow
	}
	if !res.Granted {
		emitPreToolDeny(fmt.Sprintf("Traffic Control: %s Another agent is working here; coordinate on the board (tc board) or pick a different file.", res.Message))
		return
	}
	if res.Advisory && res.Conflict != nil {
		// Inject context so the model knows it is on shared ground, without a
		// permissionDecision. Returning "allow" here would auto-approve the
		// tool (skipping the user's normal prompt) and the reason would go to
		// the user, not the model, so it would not achieve the intent.
		emitPreToolContext(fmt.Sprintf("Traffic Control: cleared, but %s is also touching %s. Proceed with care and avoid clobbering their work.", res.Conflict.Holder, res.Conflict.Path))
	}
}

// hookStop hands off the agent's clearances when its turn ends. The next turn
// re-requests them through PreToolUse, so holds never outlive active work.
func hookStop(in hookInput) {
	c, ctx, cancel := hookClient()
	defer cancel()
	if err := c.Ping(ctx); err != nil {
		return
	}
	_, _ = c.Handoff(ctx, hookCallsign(in), "")
}

// relativize tries to express an absolute path relative to cwd, so clearances
// are comparable across agents that may pass absolute or relative paths.
func relativize(p, cwd string) string {
	if cwd == "" || !filepath.IsAbs(p) {
		return protocol.NormalizePath(p)
	}
	if rel, err := filepath.Rel(cwd, p); err == nil && !strings.HasPrefix(rel, "..") {
		return protocol.NormalizePath(rel)
	}
	return protocol.NormalizePath(p)
}

// --- hook stdout protocols ---

func emitSessionContext(ctx string) {
	out := map[string]interface{}{
		"hookSpecificOutput": map[string]interface{}{
			"hookEventName":     "SessionStart",
			"additionalContext": ctx,
		},
	}
	_ = json.NewEncoder(os.Stdout).Encode(out)
}

func emitPreToolDeny(reason string) {
	out := map[string]interface{}{
		"hookSpecificOutput": map[string]interface{}{
			"hookEventName":            "PreToolUse",
			"permissionDecision":       "deny",
			"permissionDecisionReason": reason,
		},
	}
	_ = json.NewEncoder(os.Stdout).Encode(out)
}

// emitPreToolContext injects context for the model without a permission
// decision, so the normal permission flow (prompts and rules) is untouched.
func emitPreToolContext(ctx string) {
	out := map[string]interface{}{
		"hookSpecificOutput": map[string]interface{}{
			"hookEventName":     "PreToolUse",
			"additionalContext": ctx,
		},
	}
	_ = json.NewEncoder(os.Stdout).Encode(out)
}
