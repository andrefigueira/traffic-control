package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
	case "Bash":
		// Bash carries no file_path, so it cannot be coordinated up front. Record
		// the working tree's current dirty set so the matching PostToolUse can see
		// what the command changed and claim those paths after the fact.
		snapshotBashState(in)
		return
	default:
		return // not a file mutation we track
	}

	var ti struct {
		FilePath string `json:"file_path"`
	}
	_ = json.Unmarshal(in.ToolInput, &ti)
	if ti.FilePath == "" {
		return
	}
	ws := workspaceRoot(in.Cwd)
	relPath := keyPath(ti.FilePath, in.Cwd, ws)

	// Make coordination self-healing: if the tower is not up, try to start it
	// rather than only pinging. If it still cannot be reached, allow the edit
	// but say so on stderr so the human knows this edit went uncoordinated.
	if !ensureTowerRunning() {
		warnDegraded("tower unreachable, this edit is not coordinated with other agents")
		return
	}
	// Budget the call: short normally, long enough to wait out a holding pattern
	// when one is configured under enforce.
	hold := holdTimeout()
	budget := 2 * time.Second
	if hold > 0 {
		budget += hold
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	c := client.FromEnv()
	callsign := hookCallsign(in)
	_, _ = c.Register(ctx, callsign, hookProject(in), os.Getpid())

	mode := protocol.ModeAdvisory
	if os.Getenv("TC_ENFORCE") == "1" {
		mode = protocol.ModeExclusive
	}
	res, err := c.RequestClearance(ctx, callsign, ws, relPath, mode, "editing", 0)
	if err != nil {
		warnDegraded("clearance request failed mid-call, allowing the edit: " + err.Error())
		return
	}
	if !res.Granted && hold > 0 {
		// Holding pattern: rather than fail the edit outright, wait a bounded time
		// for the holder to hand off. The wait is capped, so two agents blocked on
		// each other both time out and deny instead of deadlocking.
		fmt.Fprintf(os.Stderr, "Traffic Control: %s is held; holding up to %s for a handoff...\n", relPath, hold)
		res, err = waitForClearance(ctx, c, callsign, ws, relPath, mode, hold, res)
		if err != nil {
			warnDegraded("clearance request failed during holding pattern, allowing the edit: " + err.Error())
			return
		}
	}
	if !res.Granted {
		emitPreToolDeny(fmt.Sprintf("Traffic Control: %s Another agent is working here; coordinate on the board (tc board) or pick a different file.", res.Message))
		return
	}
	// Gather advisory context to inject without a permissionDecision. Returning
	// "allow" here would auto-approve the tool (skipping the user's normal prompt)
	// and the reason would go to the user, not the model, so context injection is
	// the right tool. Path overlaps and (opt-in) symbol coupling are combined into
	// one message, since only one hook output can be emitted.
	var notes []string
	if res.Advisory {
		// A clearance overlap names the holder and path; a flight-plan overlap has
		// no Conflict clearance to name, so fall back to the tower's own message,
		// which already describes the plan. Surfacing only the Conflict != nil case
		// would make flight-plan warnings invisible to the agent, which is the whole
		// point of filing one.
		if res.Conflict != nil {
			notes = append(notes, fmt.Sprintf("%s is also touching %s. Proceed with care and avoid clobbering their work.", res.Conflict.Holder, res.Conflict.Path))
		} else {
			notes = append(notes, res.Message)
		}
	}
	if os.Getenv("TC_SYMBOLS") == "1" {
		held, _ := c.Clearances(ctx)
		notes = append(notes, symbolCoupling(relPath, in.Cwd, ws, held, callsign)...)
	}
	if len(notes) > 0 {
		emitPreToolContext("Traffic Control: " + strings.Join(notes, " "))
	}
}

// holdTimeout reads TC_HOLD_TIMEOUT (seconds). Zero or unset means the holding
// pattern is off and a blocked edit is denied immediately, the default. It only
// has teeth under enforce, where an edit can actually be blocked.
func holdTimeout() time.Duration {
	n, err := strconv.Atoi(strings.TrimSpace(os.Getenv("TC_HOLD_TIMEOUT")))
	if err != nil || n <= 0 {
		return 0
	}
	return time.Duration(n) * time.Second
}

// waitForClearance polls for a held path to clear, up to hold. It is entered
// only after an initial denial, which it returns unchanged if the path never
// frees, so the caller can deny with a meaningful message. A grant short
// circuits the wait. The poll uses the call's context, so it also stops if the
// overall budget runs out.
func waitForClearance(ctx context.Context, c *client.Client, callsign, workspace, path, mode string, hold time.Duration, initial *protocol.ClearanceResult) (*protocol.ClearanceResult, error) {
	res := initial
	deadline := time.Now().Add(hold)
	const poll = 500 * time.Millisecond
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return res, nil
		case <-time.After(poll):
		}
		r, err := c.RequestClearance(ctx, callsign, workspace, path, mode, "editing", 0)
		if err != nil {
			return res, err
		}
		res = r
		if res.Granted {
			return res, nil
		}
	}
	return res, nil
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
	callsign := hookCallsign(in)
	_ = c.Heartbeat(ctx, callsign)

	// Bash can mutate files without ever firing the Edit/Write hooks (sed -i, a
	// formatter, a codemod). Diff the working tree against the pre-command
	// snapshot and claim advisory clearances for whatever changed, so a Bash edit
	// becomes visible to other agents instead of being a silent blind spot.
	if in.ToolName == "Bash" {
		coordinateBashChanges(ctx, c, callsign, in)
	}
}

// maxBashClaims caps how many paths a single Bash command can claim, so a
// repo-wide formatter or codemod cannot flood the tower with clearances.
const maxBashClaims = 50

// coordinateBashChanges compares the working tree to the pre-command snapshot
// and claims advisory clearances for the newly changed paths. It is awareness
// after the fact, so other agents reaching for those files get the usual overlap
// warning; the change itself has already happened.
func coordinateBashChanges(ctx context.Context, c *client.Client, callsign string, in hookInput) {
	before := readBashSnapshot(in)
	removeBashSnapshot(in)
	after := gitDirtySet(in.Cwd)
	if after == nil {
		return // not a git repo, or git is unavailable
	}
	ws := workspaceRoot(in.Cwd)
	claimed := 0
	for p := range after {
		if before[p] {
			continue // already dirty before the command, already coordinated
		}
		if claimed >= maxBashClaims {
			break
		}
		claimed++
		_, _ = c.RequestClearance(ctx, callsign, ws, keyPath(p, in.Cwd, ws), protocol.ModeAdvisory, "edited via Bash", 0)
	}
}

// gitDirtySet returns the set of paths git reports as changed in cwd, or nil if
// cwd is not a git work tree or git cannot be run. Paths are relative to cwd, so
// they match the keys the Edit/Write hooks produce for the same files.
func gitDirtySet(cwd string) map[string]bool {
	if cwd == "" {
		return nil
	}
	// -z gives NUL-separated, unquoted paths, which sidesteps git's quoting of
	// names with spaces or unicode.
	out, err := exec.Command("git", "-C", cwd, "status", "--porcelain=v1", "-z").Output()
	if err != nil {
		return nil
	}
	set := map[string]bool{}
	parts := strings.Split(string(out), "\x00")
	for i := 0; i < len(parts); i++ {
		e := parts[i]
		if len(e) < 4 {
			continue
		}
		// Entry is "XY PATH"; for a rename/copy the source path is the next token.
		path := e[3:]
		if e[0] == 'R' || e[0] == 'C' {
			i++ // skip the source path that follows a rename/copy destination
		}
		set[path] = true
	}
	return set
}

// bashSnapshotPath is where the pre-command dirty set is stashed between the
// Bash PreToolUse and PostToolUse hooks, keyed by session so concurrent agents
// do not clobber each other.
func bashSnapshotPath(in hookInput) string {
	id := in.SessionID
	if id == "" {
		id = "default"
	}
	id = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, id)
	return filepath.Join(stateDir(), "bash-"+id+".snap")
}

func snapshotBashState(in hookInput) {
	set := gitDirtySet(in.Cwd)
	if set == nil {
		return
	}
	paths := make([]string, 0, len(set))
	for p := range set {
		paths = append(paths, p)
	}
	b, err := json.Marshal(paths)
	if err != nil {
		return
	}
	if err := os.MkdirAll(stateDir(), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(bashSnapshotPath(in), b, 0o644)
}

func readBashSnapshot(in hookInput) map[string]bool {
	set := map[string]bool{}
	b, err := os.ReadFile(bashSnapshotPath(in))
	if err != nil {
		return set
	}
	var paths []string
	if json.Unmarshal(b, &paths) != nil {
		return set
	}
	for _, p := range paths {
		set[p] = true
	}
	return set
}

func removeBashSnapshot(in hookInput) { _ = os.Remove(bashSnapshotPath(in)) }

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

// workspaceRoot returns the working tree that scopes coordination: the git
// toplevel of cwd, symlink-resolved so it matches however an agent spells its
// paths. Two separate git worktrees have different toplevels, so a hold in one
// never conflicts with the same relative path in another. Falls back to the
// resolved cwd when cwd is not a git work tree, and to "" when there is no cwd.
func workspaceRoot(cwd string) string {
	if cwd == "" {
		return ""
	}
	if out, err := exec.Command("git", "-C", cwd, "rev-parse", "--show-toplevel").Output(); err == nil {
		if root := strings.TrimSpace(string(out)); root != "" {
			return evalBestEffort(root)
		}
	}
	return evalBestEffort(cwd)
}

// keyPath resolves p against the session cwd, then expresses it relative to the
// workspace root, so the clearance key is stable within a tree and distinct
// across trees. Anchoring to the workspace root (not cwd) also means two agents
// in different subdirectories of one tree key the same file identically.
func keyPath(p, cwd, ws string) string {
	abs := p
	if !filepath.IsAbs(abs) && cwd != "" {
		abs = filepath.Join(cwd, abs)
	}
	return relativize(abs, ws)
}

// cwdWorkspace returns the CLI's working directory and its workspace root, so
// CLI commands key paths the same way the hooks do and interoperate in one tree.
func cwdWorkspace() (string, string) {
	cwd, _ := os.Getwd()
	return cwd, workspaceRoot(cwd)
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
