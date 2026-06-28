// Command tc is the whole of Traffic Control in one binary: the tower daemon,
// the CLI you drive it with, the Claude Code hook entrypoints, and the MCP
// server. One binary keeps installation as small as it can be.
package main

import (
	"fmt"
	"os"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `tc - Traffic Control: AI traffic control for coding agents on one tree

Usage: tc <command> [flags]

Daemon:
  serve            Run the tower (the coordination daemon)
  status           Show tower health and who is currently flying

Coordinate (talk to a running tower):
  flightplan MSG   Post a flight plan to the board (what you are about to do)
  done MSG         Post a done update to the board
  clearance PATH   Request clearance to work on a path
  handoff [PATH]   Release a path (or all your paths if PATH is omitted)
  check PATH       Show whether a path is already held
  whos-flying      List the agents currently checked in
  board            Read the broadcast board
  watch            Stream the frequency (live events)

Claude Code integration:
  hook EVENT       Hook entrypoint (session-start | pre-tool-use | stop)
  mcp              Run the MCP server over stdio
  install-claude   Print or apply the Claude Code wiring (see --help)

Other:
  version          Print the version

Environment:
  TC_ADDR          Tower address (default 127.0.0.1:7700)
  TC_CALLSIGN      Your identity on the board (default derived from user/host)
  TC_ENFORCE       If "1", pre-tool-use blocks on any held path, not just exclusive
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]

	var err error
	switch cmd {
	case "serve":
		err = cmdServe(args)
	case "status":
		err = cmdStatus(args)
	case "flightplan":
		err = cmdBoardPost(args, "flightplan")
	case "done":
		err = cmdBoardPost(args, "done")
	case "clearance":
		err = cmdClearance(args)
	case "handoff":
		err = cmdHandoff(args)
	case "check":
		err = cmdCheck(args)
	case "whos-flying", "who":
		err = cmdWhosFlying(args)
	case "board":
		err = cmdBoard(args)
	case "watch":
		err = cmdWatch(args)
	case "hook":
		err = cmdHook(args)
	case "mcp":
		err = cmdMCP(args)
	case "install-claude":
		err = cmdInstallClaude(args)
	case "version", "--version", "-v":
		fmt.Printf("tc %s\n", version)
	case "help", "--help", "-h":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "error: "+err.Error())
		os.Exit(1)
	}
}
