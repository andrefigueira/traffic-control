# Changelog

All notable changes to this project are recorded here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project aims to follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html) once releases begin.

## [Unreleased]

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
