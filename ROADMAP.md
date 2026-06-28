# Roadmap

A phased plan. Each phase is shippable and useful on its own. Issues track the detail.

## Phase 0: Scaffolding (current)

- [x] Concept, README, roadmap, changelog
- [x] Private GitHub repo and issue backlog
- [ ] Go module and repository layout
- [ ] Daemon skeleton with a local transport and a health check

## Phase 1: MVP, a working board

The smallest thing that proves the idea. Drives entirely through a CLI so it is testable without Claude.

- [ ] Session registration: agents join and leave, presence is tracked
- [ ] Claims: claim and release a path, with a simple in-memory owner map
- [ ] Broadcast board: post and read announcements (flight plans and done updates)
- [ ] Pub/sub frequency: subscribers receive pushed events over the local transport
- [ ] CLI client: `flightplan`, `clearance`, `handoff`, `whos-flying`, `board`

Exit criteria: two terminal sessions can see each other, request clearance on paths, and a second request on a held path is reported as a conflict alert.

## Phase 2: Claude integration

The point where it stops being a toy and starts saving real work.

- [ ] Plugin: `SessionStart` hook registers the agent and pulls the board into context
- [ ] Plugin: `PreToolUse` hook checks and auto-claims on Edit / Write / MultiEdit, blocks on conflict
- [ ] Plugin: `Stop` / `PostToolUse` hook releases claims and posts a done update
- [ ] MCP server: `file_flight_plan`, `request_clearance`, `handoff`, `whos_flying`, `read_board`, `check_path`

Exit criteria: two Claude Code agents in the same tree, the second is held in a pattern when it reaches for a file the first is editing and told who holds it.

## Phase 3: Robustness

Make it trustworthy enough to leave running.

- [ ] Leases and heartbeats: clearances auto-expire so a crashed agent never deadlocks the tree
- [ ] Durable storage so a daemon restart keeps the board
- [ ] Glob and directory claims, not just single files
- [ ] Conflict policy: advisory-by-default with opt-in hard blocking per path or pattern
- [ ] Graceful degradation when the daemon is down (agents proceed, warn, do not break)

## Phase 4: The office view

Make the coordination visible to the human in the loop.

- [ ] TUI or small web dashboard: live presence, who holds what, the rolling bulletin feed
- [ ] Notifications for conflicts and announcements

## Phase 5: Beyond one machine (maybe)

Only if demand is real.

- [ ] Coordinate across worktrees as well as within one tree, so it complements rather than competes
- [ ] Multi-machine coordination over a shared broker
- [ ] Editor integrations beyond Claude Code

## Open questions

- How hard should the default enforcement be before it annoys more than it helps?
- Is the board valuable enough on its own that worktree users would run it just for presence?
- What is the right granularity for clearance: file, symbol, directory, or intent?
