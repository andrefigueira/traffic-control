# Roadmap

A phased plan. Each phase is shippable and useful on its own. Issues track the detail.

## Phase 0: Scaffolding (done)

- [x] Concept, README, roadmap, changelog
- [x] Private GitHub repo and issue backlog
- [x] Go module and repository layout
- [x] Daemon skeleton with a local transport and a health check

## Phase 1: MVP, a working board (done)

The smallest thing that proves the idea. Drives entirely through a CLI so it is testable without Claude.

- [x] Session registration: agents join and leave, presence is tracked
- [x] Clearance: request and hand off a path, with an in-memory holder map
- [x] Broadcast board: post and read announcements (flight plans and done updates)
- [x] Pub/sub frequency: subscribers receive pushed events over the local transport
- [x] CLI client: `flightplan`, `clearance`, `handoff`, `whos-flying`, `board`

Exit criteria: two terminal sessions can see each other, request clearance on paths, and a second request on a held path is reported as a conflict alert.

## Phase 2: Claude integration (done)

The point where it stops being a toy and starts saving real work.

- [x] `SessionStart` hook registers the agent and pulls the board into context
- [x] `PreToolUse` hook requests clearance on Edit / Write / MultiEdit, blocks exclusive conflicts
- [x] `Stop` hook hands off clearances
- [x] MCP server: `file_flight_plan`, `request_clearance`, `handoff`, `whos_flying`, `read_board`, `check_path`
- [x] Docs: quickstart and integration guide (`install.sh`, `tc install-claude`)

Exit criteria: two Claude Code agents in the same tree, the second is held in a pattern when it reaches for a file the first is editing and told who holds it.

## Phase 3: Robustness (in progress)

Make it trustworthy enough to leave running.

- [x] Glob and directory clearances, not just single files
- [x] Conflict policy: advisory-by-default with opt-in hard blocking (`TC_ENFORCE=1`)
- [x] Graceful degradation when the tower is down (agents proceed, warn, do not break)
- [~] Leases: clearances auto-expire and refresh on heartbeat. Heartbeat timing for long-reasoning agents is still open.
- [ ] Durable storage so a tower restart keeps the board
- [ ] Close the Bash edit bypass, or scope it out explicitly

## Phase 4: The scope

Make the coordination visible to the human in the loop.

- [ ] TUI or small web dashboard (the scope): live presence, who holds what clearance, the rolling board
- [ ] Notifications for conflict alerts and announcements

## Phase 5: Beyond one machine (maybe)

Only if demand is real.

- [ ] Coordinate across worktrees as well as within one tree, so it complements rather than competes
- [ ] Multi-machine coordination over a shared broker
- [ ] Editor integrations beyond Claude Code

## Open questions

- How hard should the default enforcement be before it annoys more than it helps?
- Is the board valuable enough on its own that worktree users would run it just for presence?
- What is the right granularity for clearance: file, symbol, directory, or intent?
- The validation experiment: run two real agents in one tree on a real task and log how often they collide on the *same path* versus break each other *across paths*. If cross-path breakage dominates, the awareness board matters far more than path clearance, and the product should lean further that way.
