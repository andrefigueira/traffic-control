# Traffic Control

**AI traffic control for coding agents sharing one working tree.**

The clue is in the pun. Air traffic control exists so independent aircraft can use the same airspace without colliding: a tower grants clearance, keeps everyone separated, and broadcasts who is where. Traffic Control does the same for AI coding agents working in the same repository.

## The problem

Run more than one Claude Code agent in the same repository and they clobber each other. Each agent has its own context and no idea the others exist, so two of them edit the same file and one silently overwrites the other's work. No warning, no error, just lost output that surfaces later.

The mainstream answer is **git worktrees**: give every agent its own checkout so they physically cannot collide, then merge at the end. That works, with real costs. You pay for N copies of the repo, N sets of dependencies and build caches, port and environment collisions, divergent local database state, and a merge step that only reveals conflicts after the parallel work is done. Worktrees also leave the agents blind to each other.

## The idea

Traffic Control is the **same-tree alternative to worktrees**. Multiple agents work on one working directory at the same time, kept apart by clearance and awareness rather than by copies. It is the option for when you want a single live environment: one dev server, one database, one set of generated artifacts, hot reload intact. Conflicts are caught at second zero, before work starts, which is far cheaper than discovering them at merge time.

The model is air traffic control, and the metaphor gives the whole product its vocabulary.

## The vocabulary

| Aviation | In Traffic Control |
| --- | --- |
| **The tower** | The daemon. Everything checks in with it. |
| **Flight plan** | An agent declaring what it is about to work on. |
| **Clearance** | A granted claim on a path. You are cleared to edit. |
| **Separation** | The core guarantee: keep two agents off the same file. |
| **Holding pattern** | What a blocked agent does while a path is held by another. |
| **Handoff** | Releasing or passing a claim to another agent. |
| **Conflict alert** | The warning when two agents reach for the same path. |
| **The frequency** | The pub/sub channel everyone is tuned to. |
| **Callsign** | An agent's identity on the board. |
| **The scope** | The dashboard: who is in the air and what they hold. |

## How it works

Three pieces:

1. **The tower (Go daemon).** A long-running local process holding live state: active sessions (presence), clearances on paths and globs, and an append-only broadcast feed. A pub/sub frequency pushes updates the moment anything changes. In-memory for speed, durable so a restart keeps the board.

2. **The Claude plugin.** Wires the tower into Claude Code via hooks:
   - `SessionStart`: register the agent and pull the current board into context, so a fresh agent immediately knows what everyone else is doing.
   - `PreToolUse` on Edit / Write / MultiEdit: request clearance for the target path. Cleared, proceed. Held by another live agent, enter a holding pattern and tell the agent who has it and why.
   - `Stop` / `PostToolUse`: hand off claims and post a short done update.

3. **The MCP server.** Tools the agent calls deliberately: `file_flight_plan`, `request_clearance`, `handoff`, `whos_flying`, `read_board`, `check_path`. Hooks give automatic enforcement, MCP gives conscious coordination.

## Design stance

- **Separation is the product, the hard lock is one setting.** Hard locking on autonomous agents is brittle: an agent crashes holding a clearance, or forgets to release it, and the tree deadlocks. So clearances default to advisory with leases and heartbeats that expire automatically. Hard blocking is opt-in for genuinely hot files.
- **Local first.** Dozens of agents on one machine is the target, so an in-process pub/sub frequency over a local socket is plenty. No external broker.
- **Frictionless or it dies.** Install the plugin, start the tower, done.

## The name

`Traffic Control`, on the **AI traffic control** play on air traffic control. The aviation metaphor is the whole identity, from the tower down to the radio chatter. The name also sidesteps crowded developer-tool namespaces: `Tower` is a paid Git client, `Squawk` is a Postgres linter, and most of the obvious single words were already taken.

## Status

Concept and scaffolding. Nothing is built yet. The backlog lives in [GitHub Issues](https://github.com/andrefigueira/traffic-control/issues), the plan in [ROADMAP.md](./ROADMAP.md), and the running history in [CHANGELOG.md](./CHANGELOG.md).

## Prior art

- **git worktrees** (`isolation: worktree` in Claude Code): isolation by copies, no awareness.
- **Agent Teams / shared task-list files**: a shared markdown task list with crude file locking, polled rather than pushed.
- **`agent-comms`**: the closest existing project. Agents announce files and negotiate before starting. Python stdlib, file-based, no daemon, no live push, no presence, no enforcement.

Traffic Control's distinct shape is the combination: a persistent tower, real pub/sub, live presence, a broadcast board, and first-class Claude hook enforcement plus MCP, all aimed at the same-tree case.
