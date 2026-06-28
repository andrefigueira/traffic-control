# Bulletin

A coordination and awareness daemon for local AI coding agents working on the **same tree**.

> Working name. See [Name candidates](#name-candidates).

## The problem

Run more than one Claude Code agent in the same repository and they start clobbering each other. Each agent has its own context and no idea the others exist, so two of them edit the same file and one silently overwrites the other's work. No warning, no error, just lost output that only surfaces later.

The mainstream answer today is **git worktrees**: give every agent its own checkout so they physically cannot collide, then merge at the end. That works, with real costs. You pay for N copies of the repo, N sets of dependencies and build caches, port and environment collisions, divergent local database state, and a merge step that only reveals conflicts after the parallel work is already done. Worktrees also make the agents blind to each other, so they never coordinate, they just get reconciled afterwards.

## The idea

Bulletin lets multiple agents work on **one** working tree at the same time, coordinated by shared awareness rather than isolated by copies. It is the option for when you want a single live environment: one dev server, one database, one set of generated artifacts, hot reload intact.

A background daemon holds the live state of who is in the building and what they are touching. Agents announce intent, claim the paths they are about to edit, and read a shared bulletin feed of what everyone else is doing. Conflicts are caught at second zero, before work starts, which is far cheaper than discovering them at merge time.

The model is the old office of developers. Before you dived into the auth module you said so out loud, and anyone already in there spoke up. Bulletin is that announcement layer for agents.

## How it works

Three pieces:

1. **The daemon (Go).** A long-running local process that holds live state: active sessions (presence), claims on paths and globs, and an append-only bulletin feed. It exposes a pub/sub event bus so subscribers get pushed updates the moment something changes. In-memory for speed, with durable storage so a restart does not lose the board.

2. **The Claude plugin.** Wires the daemon into Claude Code through hooks:
   - `SessionStart`: register the agent and pull the current bulletin into context, so a fresh agent immediately knows what everyone else is doing.
   - `PreToolUse` on Edit / Write / MultiEdit: ask the daemon whether the target path is claimed by another live agent. Free path, auto-claim and proceed. Claimed, block the edit and tell the agent who holds it and why.
   - `Stop` / `PostToolUse`: release or downgrade claims and post a short "done with X" update to the board.

3. **The MCP server.** Tools the agent can call deliberately: `announce`, `claim`, `release`, `whos_working_on`, `read_bulletin`, `check_path`. Hooks give automatic enforcement; MCP gives the agent a way to coordinate consciously, the same way a developer chooses to post a heads-up.

## Design stance

- **Awareness is the moat, the lock is a feature.** Hard file locking on autonomous agents is brittle: an agent crashes holding a claim, or forgets to release, and you get a deadlock. So claims default to advisory with leases and heartbeats that expire automatically. Hard blocking is opt-in for genuinely hot files.
- **Local first.** Dozens of agents on one machine is the target, so an in-process pub/sub bus over a local socket is plenty. No Kafka, no external broker.
- **Frictionless or it dies.** The whole thing lives or dies on setup cost. Install the plugin, start the daemon, done.

## Status

Concept and scaffolding. Nothing is built yet. The backlog lives in [GitHub Issues](https://github.com/andrefigueira/bulletins/issues), the plan in [ROADMAP.md](./ROADMAP.md), and the running history in [CHANGELOG.md](./CHANGELOG.md).

## Name candidates

`Bulletin` is the working name. Alternatives under consideration:

| Name | Angle |
| --- | --- |
| **Bulletin** | Literal, matches the board metaphor, descriptive |
| **Tannoy** | The office PA you announce over (trademark of a speaker brand, would need checking) |
| **Standup** | The daily developer sync ritual, instantly legible to devs |
| **Switchboard** | An operator routing and connecting the parties |
| **Dispatch** | Air-traffic style coordination and assignment |
| **Crier** | The town crier announcing to everyone within earshot |
| **Huddle** | A quick gathering to align before acting |

## Prior art

- **git worktrees** (`isolation: worktree` in Claude Code): isolation by copies, no awareness.
- **Agent Teams / shared task-list files**: a shared markdown task list with crude file locking, polled rather than pushed.
- **`agent-comms`**: the closest existing project. Agents announce files and negotiate before starting. Python stdlib, file-based, no daemon, no live push, no presence, no enforcement.

Bulletin's distinct shape is the combination: a persistent daemon, real pub/sub, live presence, a social bulletin, and first-class Claude hook enforcement plus MCP, all aimed at the same-tree case.
