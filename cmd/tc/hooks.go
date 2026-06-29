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
	case "post-tool-use", "PostToolUse":
		hookPostToolUse(in)
	case "stop", "Stop":
		hookStop(in)
	default:
		// Unknown hook: do nothing, allow.
	}
	return nil
}

// warnDegraded writes a one-line notice to stderr when coordination is not
// available for an edit. The edit still proceeds: this only makes a silent loss
// of coordination visible to the human watching the session, instead of letting
// the tool quietly stop coordinating with no signal at all.
func warnDegraded(msg string) {
	fmt.Fprintln(os.Stderr, "Traffic Control (degraded): "+msg)
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

// hookSessionStart registers the agent and injects the live situation into
// context, so a fresh agent immediately sees who else is working, which files
// are held, and recent board activity. It auto-starts the tower if needed.
func hookSessionStart(in hookInput) {
	if !ensureTowerRunning() {
		return // no tower and could not start one; stay silent, never block
	}
	c, ctx, cancel := hookClient()
	defer cancel()
	callsign := hookCallsign(in)
	_, _ = c.Register(ctx, callsign, hookProject(in), os.Getpid())

	sessions, _ := c.WhosFlying(ctx)
	clearances, _ := c.Clearances(ctx)
	board, _ := c.ReadBoard(ctx, 10)
	emitSessionContext(buildSessionContext(callsign, sessions, clearances, board))
}

// buildSessionContext renders the awareness block injected at session start. It
// is pure so it can be tested directly.
func buildSessionContext(callsign string, sessions []protocol.Session, clearances []protocol.Clearance, board []protocol.BoardEntry) string {
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

	held := 0
	for _, cl := range clearances {
		if cl.Holder == callsign {
			continue
		}
		if held == 0 {
			b.WriteString("Files currently held (coordinate before editing these):\n")
		}
		held++
		b.WriteString(fmt.Sprintf("  - %s held by %s (%s)\n", cl.Path, cl.Holder, cl.Mode))
	}

	if len(board) > 0 {
		b.WriteString("Recent board activity:\n")
		for _, e := range board {
			b.WriteString(fmt.Sprintf("  - %s [%s] %s\n", e.Callsign, e.Kind, e.Message))
		}
	}
	b.WriteString("Before a large or multi-file change, file a flight plan with the paths you will touch (the file_flight_plan tool, or `tc flightplan`). Other agents get an advisory warning when they reach for those paths, and the plan stays on the board after your turn ends.\n")
	return b.String()
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

	// Make coordination self-healing: if the tower is not up, try to start it
	// rather than only pinging. If it still cannot be reached, allow the edit
	// but say so on stderr so the human knows this edit went uncoordinated.
	if !ensureTowerRunning() {
		warnDegraded("tower unreachable, this edit is not coordinated with other agents")
		return
	}
	c, ctx, cancel := hookClient()
	defer cancel()
	callsign := hookCallsign(in)
	_, _ = c.Register(ctx, callsign, hookProject(in), os.Getpid())

	mode := protocol.ModeAdvisory
	if os.Getenv("TC_ENFORCE") == "1" {
		mode = protocol.ModeExclusive
	}
	res, err := c.RequestClearance(ctx, callsign, relPath, mode, "editing", 0)
	if err != nil {
		warnDegraded("clearance request failed mid-call, allowing the edit: " + err.Error())
		return
	}
	if !res.Granted {
		emitPreToolDeny(fmt.Sprintf("Traffic Control: %s Another agent is working here; coordinate on the board (tc board) or pick a different file.", res.Message))
		return
	}
	if res.Advisory {
		// Inject context so the model knows it is on shared ground, without a
		// permissionDecision. Returning "allow" here would auto-approve the
		// tool (skipping the user's normal prompt) and the reason would go to
		// the user, not the model, so it would not achieve the intent.
		//
		// A clearance overlap names the holder and path; a flight-plan overlap
		// has no Conflict clearance to name, so fall back to the tower's own
		// message, which already describes the plan. Surfacing only the
		// Conflict != nil case would make flight-plan warnings invisible to the
		// agent, which is the whole point of filing one.
		msg := res.Message
		if res.Conflict != nil {
			msg = fmt.Sprintf("cleared, but %s is also touching %s. Proceed with care and avoid clobbering their work.", res.Conflict.Holder, res.Conflict.Path)
		}
		emitPreToolContext("Traffic Control: " + msg)
	}
}

// hookPostToolUse refreshes the agent's lease on every path it currently holds.
// Without this, a hold acquired early in a long turn could expire at the lease
// boundary while the agent is still working on a different file. It is the
// heartbeat that keeps an actively-working agent's ground from being swept out
// from under it. It never blocks and never speaks to the model.
func hookPostToolUse(in hookInput) {
	c, ctx, cancel := hookClient()
	defer cancel()
	if err := c.Ping(ctx); err != nil {
		return // tower down: nothing to refresh, stay silent
	}
	_ = c.Heartbeat(ctx, hookCallsign(in))
}

// hookStop hands off the agent's clearances when its turn ends. The next turn
// re-requests them through PreToolUse, so holds never outlive active work. If
// the tower is unreachable, the holds fall to the lease backstop instead.
func hookStop(in hookInput) {
	c, ctx, cancel := hookClient()
	defer cancel()
	if err := c.Ping(ctx); err != nil {
		warnDegraded("could not hand off clearances at turn end; they will expire on the lease")
		return
	}
	_, _ = c.Handoff(ctx, hookCallsign(in), "")
}

// relativize expresses a file path as a stable, repo-relative form so two
// agents that pass the same physical file differently (one absolute, one
// relative, one through a symlink) still produce the same clearance key.
//
//   - A relative path is anchored to the SESSION cwd (in.Cwd), not the hook
//     process's own working directory, which may differ.
//   - Symlinks are resolved best-effort on both the file and the cwd, so a link
//     to the same file compares equal. A not-yet-created file (a fresh Write)
//     has no inode to resolve, so it falls back to the lexical path.
//   - A file under the cwd becomes relative; anything else stays absolute.
func relativize(p, cwd string) string {
	abs := p
	if !filepath.IsAbs(abs) && cwd != "" {
		abs = filepath.Join(cwd, abs)
	}
	abs = evalBestEffort(abs)
	base := evalBestEffort(cwd)
	if base == "" || !filepath.IsAbs(abs) {
		return protocol.NormalizePath(abs)
	}
	if rel, err := filepath.Rel(base, abs); err == nil && !strings.HasPrefix(rel, "..") {
		return protocol.NormalizePath(rel)
	}
	return protocol.NormalizePath(abs)
}

// evalBestEffort resolves symlinks in p. A bare filepath.EvalSymlinks fails
// outright on a path whose final element does not exist yet, which is every
// fresh Write of a new file. That left a new file keyed by its unresolved
// absolute path while the cwd was resolved, so on a symlinked tree (the macOS
// default for /var and /tmp) the same file keyed differently before and after
// it existed. This resolves the longest existing ancestor and re-appends the
// not-yet-created tail, so a new file and the same file once written produce an
// identical key.
func evalBestEffort(p string) string {
	if p == "" {
		return ""
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	dir, file := filepath.Split(p)
	dir = strings.TrimSuffix(dir, string(filepath.Separator))
	if dir == "" || dir == p {
		return p
	}
	return filepath.Join(evalBestEffort(dir), file)
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
