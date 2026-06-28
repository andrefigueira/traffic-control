# Traffic Control

**AI traffic control for coding agents sharing one working tree.**

Air traffic control lets independent aircraft use the same airspace without colliding: a tower broadcasts who is where and grants clearance to act. Traffic Control does the same for AI coding agents working in the same repository. A small background daemon (the tower) tracks who is in the air, what each agent is touching, and a shared board of what everyone is doing, then warns or blocks when two agents reach for the same file.

It is a single Go binary with zero dependencies. Install it, start the tower, and Claude Code agents coordinate automatically through hooks and an MCP server.

## What it is for

Most teams run parallel agents with **git worktrees**: each agent gets its own checkout, so they cannot touch the same files, and you merge afterwards. That is the right tool when you want isolation, and Traffic Control does not replace it.

Traffic Control is for the case worktrees do not serve: when you want several agents in **one shared tree** with **one live environment**, a single dev server, a single database, hot reload intact, without N checkouts to provision and reconcile. In that setup the agents are otherwise blind to each other. Traffic Control gives them awareness, and optional file-level clearance, so they stop overwriting each other's work.

Be clear about the trade. Worktrees give isolation and defer all conflict discovery to merge time. Traffic Control gives shared-environment coordination and catches *file-level* collisions up front. It does not catch *semantic* conflicts (one agent changing a function signature that another agent's file depends on). See [Honest limitations](#honest-limitations).

## Install

Needs Go to build. One command:

```sh
git clone https://github.com/andrefigueira/traffic-control
cd traffic-control
./install.sh
```

That builds the `tc` binary and installs it to `~/.local/bin`. Then, in any project you want coordinated:

```sh
tc serve            # start the tower (leave it running in a terminal or as a service)
cd my/project
tc install-claude   # wire this project's Claude Code hooks + MCP server
```

Run Claude Code in that project as usual. That is the whole setup.

`tc install-claude` merges three hooks into `.claude/settings.json` and the MCP server into `.mcp.json`, without disturbing your existing config. Run `tc install-claude --print` to see exactly what it adds before it writes anything.

## How the Claude integration works

`tc` is the daemon, the CLI, the hook handler, and the MCP server in one binary.

**Hooks** (automatic, no agent effort):
- `SessionStart` checks the agent in and injects the current board into its context, so a fresh agent immediately knows who else is working and on what.
- `PreToolUse` on Edit / Write / MultiEdit requests clearance for the target file. If another agent holds it exclusively, the edit is blocked and the agent is told who holds it. Otherwise it proceeds, with an advisory note if someone else is nearby.
- `Stop` hands off the agent's clearances when its turn ends, so holds never outlive active work.

**MCP tools** (deliberate, the agent chooses to call them):
`file_flight_plan`, `request_clearance`, `handoff`, `whos_flying`, `read_board`, `check_path`.

Enforcement is **advisory by default**: it warns but lets the edit through. Set `TC_ENFORCE=1` to make `PreToolUse` request exclusive clearance and hard-block on any held path.

## CLI

You can drive the tower by hand, which is also how you test it without Claude.

| Command | Does |
| --- | --- |
| `tc serve` | Run the tower |
| `tc status` | Tower health and who is flying |
| `tc flightplan "msg" --paths a,b` | Announce what you are about to do |
| `tc clearance PATH --mode exclusive` | Request to hold a path |
| `tc handoff [PATH]` | Release a path, or all of yours |
| `tc check PATH` | Is this path already held |
| `tc whos-flying` | List checked-in agents |
| `tc board` | Read the broadcast board |
| `tc watch` | Stream the live frequency |

## The scope (live dashboard)

The tower serves a zero-dependency web dashboard, the scope, at its root. With the tower running:

```sh
tc scope     # prints the URL and opens it in your browser
```

Or open `http://127.0.0.1:7700/` directly. It shows, updating live over the event stream: who is in the air, what clearances are held (advisory and exclusive), the broadcast board, and a running frequency of events with conflict alerts called out. This is the awareness layer made visible, the part that helps even when you never use the lock.

## The vocabulary

| Aviation | In Traffic Control |
| --- | --- |
| **The tower** | The daemon. Everything checks in with it. |
| **Flight plan** | An agent announcing what it is about to work on. |
| **Clearance** | A held path. Advisory by default, exclusive on request. |
| **Holding pattern** | A blocked agent waiting on a path another holds. |
| **Handoff** | Releasing a held path. |
| **Conflict alert** | The warning when two agents reach for the same path. |
| **The frequency** | The pub/sub event stream (`tc watch`, or `GET /events`). |
| **The board** | The running broadcast of flight plans and done updates. |
| **Callsign** | An agent's identity. |

## Architecture

- **The tower** (`internal/tower`): in-memory state of sessions, clearances, and the board, plus a non-blocking pub/sub broker. Transport agnostic.
- **HTTP API** (`internal/api`): localhost JSON over `127.0.0.1:7700` with a Server-Sent Events stream. No auth, no TLS; it is a single-user local coordinator.
- **The `tc` binary** (`cmd/tc`): CLI, hook handler, and MCP stdio server, all talking to the tower through one client.

## Honest limitations

This is an MVP and the design has real edges. Stated plainly rather than hidden:

- **File-level only.** Clearance is by path. It does not model semantic coupling, so two agents editing different files can still break each other's build. The board (awareness) is what helps there, not the lock.
- **Bash edits bypass it.** Hooks fire on Edit / Write / MultiEdit. An agent that writes files via `sed -i`, a formatter, or a codemod through Bash is not seen by the tower. Tracked in the issues.
- **Advisory by default does not prevent overwrites**, it only warns. Hard prevention needs `TC_ENFORCE=1`, which brings the usual locking trade-offs.
- **Leases can expire mid-task.** A long-reasoning agent that holds a path for many minutes between tool calls could in principle have its lease expire. Heartbeat timing is an open question (see issues).
- **In-memory only.** A tower restart loses current state. Durable storage is on the roadmap.
- **Platform risk.** Anthropic ships `isolation: worktree` and owns the hook API this is built on. A native coordination primitive could land at any time.

## Status

MVP built and tested: the tower, presence, the board, advisory and exclusive clearance with directory and glob matching, the pub/sub frequency, the full CLI, the three Claude hooks, and the MCP server. Unit tests cover the core; an end-to-end path is verified.

Plan in [ROADMAP.md](./ROADMAP.md), history in [CHANGELOG.md](./CHANGELOG.md), backlog in [GitHub Issues](https://github.com/andrefigueira/traffic-control/issues).

## Prior art

- **git worktrees** (`isolation: worktree` in Claude Code): isolation by separate checkouts, no awareness between agents.
- **Agent Teams / shared task-list files**: a shared markdown list with crude file locking, polled rather than pushed.
- **`agent-comms`**: agents announce files and negotiate before starting. Python stdlib, single file, no daemon, no presence, no enforcement. Simpler than Traffic Control, and for some users that simplicity is the point.

Traffic Control's bet is that a persistent tower with live presence, a broadcast board, and first-class Claude hook plus MCP integration is worth the heavier footprint for agents sharing one tree.
