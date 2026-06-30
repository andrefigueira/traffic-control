# Changelog

All notable changes to this project are recorded here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project aims to follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html) once releases begin.

## [Unreleased]

### Added

- **Activity logging and `tc report`.** The tower now records every event (joins, clearances, conflicts, advisories, handoffs, expirations, board posts) to `events.jsonl` in the state dir, one JSON line each. `tc report` summarizes it into a usage/performance view: the time window, agents seen, clearances granted, and crucially the number of conflicts caught, plus the tower's live stats (flying, held, watchers, dropped events, uptime) when it is reachable. The log is written off the tower lock and is best-effort, so it can never wedge coordination. Opt out with `TC_NO_LOG=1`.

### Fixed

- **Separate git worktrees no longer falsely collide.** Coordination is now scoped by working tree (the git toplevel), so two agents editing the same relative path in different worktrees (or under Claude Code's `isolation: worktree`) are seen as the distinct files they are, not a conflict. Clearances, checks, and flight-plan warnings are all workspace-scoped; symbol coupling only compares files within one tree. Paths are keyed relative to the tree root, so two agents in different subdirectories of one tree also agree on a file's identity. This makes Traffic Control safe to run alongside worktrees rather than fighting their isolation.

## [0.2.0] - 2026-06-30

### Added

- **Desktop notifications.** `tc watch --notify` pops an OS-level notification (macOS and Linux) on conflict alerts and advisory overlaps, the events worth surfacing when you are not watching the scope. Best-effort, like opening the browser: a platform without a notifier simply does nothing. This closes the last open Phase 4 item.
- **`tc doctor`.** A setup check that reports whether the tower is reachable, the working tree is a git repo, and the project's Claude hooks and MCP server are wired, so it is obvious at a glance why coordination is or is not happening.
- **`tc uninstall-claude`.** The inverse of `install-claude`: removes the hooks and MCP server this tool added and leaves the rest of the config untouched. Idempotent, and it only strips its own hook objects, so a matcher entry shared with another tool keeps that tool's hooks.
- **MIT license.** The project is now MIT licensed (`LICENSE`), matching the contribution invite in the README.

### Changed

- **Symbol-coupling awareness now covers TypeScript/JavaScript and Python, not just Go.** Each language has its own exported-declaration patterns, and coupling is only compared within one language, so a Go `Process` and a Python `Process` never cross-warn. Still `TC_SYMBOLS=1`, heuristic, and advisory.

## [0.1.0] - 2026-06-29

### Reliability follow-ups

Closing the two failure modes most likely to bite a real multi-agent run.

Added:

- **Bash-edit awareness.** Bash can rewrite files without firing the Edit/Write hooks (`sed -i`, a formatter, a codemod). The Bash `PreToolUse` hook now snapshots the working tree's dirty set and the matching `PostToolUse` hook diffs it, claiming advisory clearances for whatever the command changed. A Bash edit becomes visible to other agents instead of being a silent blind spot. It is after-the-fact awareness, needs a git work tree, and is capped at 50 paths per command so a repo-wide codemod cannot flood the tower. `tc install-claude` now matches `Bash` on the Pre/Post hooks.
- **Session-long MCP heartbeat.** The MCP server refreshes its session and held clearances on a 45s ticker for as long as it runs, so a long-reasoning turn that sits for minutes between tool calls no longer risks a hold expiring at the lease boundary. The beat is best-effort and never writes to stdout, so the JSON-RPC framing is untouched.
- **The board survives a tower restart.** Flight plans and notes are persisted to a snapshot file in the state dir and reloaded on boot, so a tower bounce no longer wipes the awareness layer. Holds and presence stay in memory by design, since they are turn-scoped and re-acquired on the next hook. Writes are atomic and best-effort, so a disk error never breaks coordination.
- **Holding pattern under enforce.** Set `TC_HOLD_TIMEOUT=N` and a blocked edit waits up to N seconds for the holder to hand off before being denied, instead of failing outright. The wait is bounded, so two agents blocked on each other both time out and deny rather than deadlocking. Off by default, so existing enforce behaviour is unchanged.
- **Symbol-coupling awareness (experimental).** A first cut at semantic coupling, the gap path clearance cannot see. Set `TC_SYMBOLS=1` and `PreToolUse` warns when the Go file you are about to edit and a file another agent holds share an exported symbol that one defines and the other uses, in either direction. It is a heuristic (regex over source, kept dependency-free on purpose), Go-only, capped, and advisory, so it never blocks. Off by default.

### Hardening pass (review + research follow-up)

A round of fixes and features driven by a deep code review and a literature survey (see `RESEARCH-AND-GAPS.md`).

Added:

- **Recursive `**` globs in clearances.** A claim on `internal/**` now covers every file in the subtree, matching across path separators. Previously `**` matched nothing nested, so a subtree claim silently locked nothing and an exclusive hold could no-op.
- **Flight plans now drive coordination.** Paths on a flight plan are checked against later clearance requests, so filing a plan actually warns other agents off those paths. Plans persist after a turn ends and stop warning once their author leaves, so they are the awareness signal that outlasts a turn-scoped clearance.
- **`PostToolUse` heartbeat hook.** An actively working agent refreshes its held paths on every edit, so an earlier hold does not expire at the lease boundary mid-turn. `tc install-claude` now wires four hooks.
- **Fail-loud coordination.** `PreToolUse` auto-starts the tower instead of only pinging it, and every degraded path now writes a one-line notice to stderr, so a silent loss of coordination is visible. The broker counts dropped events, surfaced in `/healthz` and called out by `tc status`. `tc watch` reconnects with backoff instead of exiting on the first blip.
- **Advisory overlaps emit a `clearance.advisory` event,** so the scope and `tc watch` show two agents on one file, not just hard conflicts.
- **`TC_ENFORCE=1` is a real floor on MCP.** A model cannot place a weaker-than-exclusive hold through `request_clearance` under that policy.

Fixed:

- **Absolute and relative paths to the same file now collide.** Paths are anchored to the session cwd and symlink-resolved best-effort, on the hook and MCP entry points alike, so one agent passing an absolute path and another a relative one are recognized as the same file.
- **Case-insensitive filesystems.** Path comparison folds case on macOS and Windows, so `src/App.go` and `src/app.go` are the same file there. Stored paths keep their original case for display.
- **Backslashes are converted to separators only on Windows,** so a legitimate Unix filename containing a backslash is no longer corrupted.
- **Pidfile race on auto-start.** The tower binds its port before claiming the pidfile, so a second tower that loses the race for the port can no longer overwrite the live tower's pidfile.

Changed:

- The lease is documented as a crash backstop, not a task-protection guarantee. The `Stop` hook hands off holds every turn by design, and the new heartbeat keeps an active agent's other holds alive.

### Added

- **MVP implementation in Go (zero dependencies).** The `tc` binary is the tower daemon, the CLI, the Claude hook handler, and the MCP server in one.
  - Tower core: sessions and presence, advisory and exclusive clearances with directory and glob overlap matching, a broadcast board, leases, and a non-blocking pub/sub broker. Unit tests cover the core.
  - HTTP API over `127.0.0.1:7700` with a Server-Sent Events frequency.
  - CLI: `serve`, `status`, `flightplan`, `done`, `clearance`, `handoff`, `check`, `whos-flying`, `board`, `watch`.
  - Claude Code hooks: `SessionStart` (register and inject the board), `PreToolUse` (request clearance, block exclusive conflicts), `Stop` (hand off clearances). They degrade gracefully and never block an agent when the tower is down.
  - MCP stdio server exposing `file_flight_plan`, `request_clearance`, `handoff`, `whos_flying`, `read_board`, `check_path`.
  - `tc install-claude` to merge the hooks and MCP server into a project's config, plus `install.sh` and a `Makefile`.
- Honest-limitations section in the README (file-level only, Bash bypass, advisory default, lease timing, in-memory, platform risk), reflecting the hostile review.
- **The scope**: a zero-dependency live web dashboard the tower serves at its root, showing presence, held clearances, the board, and a running frequency of events (conflict alerts called out), updating over SSE. Opened with `tc scope`.
- GitHub Actions CI: gofmt check, vet, build, and `go test -race` on push and pull request.

### Changed

- Reframed the positioning away from "alternative to worktrees" toward a **complement** for the shared-tree case, leading with the awareness board rather than the lock, after an adversarial review found the lock-first framing overclaimed.
- Initial concept and project framing: AI traffic control for coding agents sharing one working tree.

### Fixed

Following a code review of the MVP:

- **PreToolUse advisory overlap** now injects model context via `additionalContext` with no permission decision. It previously returned `permissionDecision: "allow"`, which auto-approved the edit (skipping the user's normal prompt) and sent the warning to the user rather than the model.
- **MCP server no longer replies to JSON-RPC notifications.** Notifications are detected structurally (absent id) rather than by a hard-coded method list, so id-less messages never get a spurious response.
- **Conflict messages name the earliest-granted holder deterministically** instead of whichever entry Go's randomized map iteration landed on.
- **`install-claude` merge is idempotent** on the exact hook command string, so re-running it never duplicates entries.
- **Path overlap treats only `*` and `?` as glob markers**, so literal bracket filenames (a Next.js route like `app/[id].tsx`) match literally rather than as a character class.
- Added integration tests for the HTTP API, the MCP JSON-RPC layer, `install-claude` idempotency, and tower concurrency under the race detector.
- README describing the problem, the idea, the air traffic control vocabulary, the three-piece architecture (tower daemon, Claude plugin, MCP server), and the design stance of separation-first with advisory clearances.
- Roadmap with phased delivery from scaffolding through MVP, Claude integration, robustness, the scope (dashboard), and possible multi-machine support.
- Private GitHub repository and an issue backlog mapping the roadmap.

### Changed

- Renamed the project from the working name "Bulletin" to **Traffic Control**, on the "AI traffic control" play on air traffic control. Repository renamed from `bulletins` to `traffic-control`. The aviation metaphor now carries the product vocabulary (tower, clearance, separation, holding pattern, handoff, conflict alert).
