package main

import (
	"context"
	"flag"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/andrefigueira/traffic-control/internal/client"
)

// cmdDoctor reports whether the current project is wired up and the tower is
// reachable, so a user can tell at a glance why coordination is or is not
// happening. It is purely informational and never fails.
func cmdDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	project := fs.String("project", ".", "project directory to check")
	_ = fs.Parse(args)

	checks := runDoctor(*project)
	allOK := true
	for _, c := range checks {
		mark := "ok"
		if !c.ok {
			mark = "warn"
			allOK = false
		}
		fmt.Printf("[%-4s] %-7s %s\n", mark, c.name, c.detail)
	}
	if allOK {
		fmt.Println("\nall checks passed.")
	} else {
		fmt.Println("\nsome checks need attention (see above).")
	}
	return nil
}

// doctorCheck is one line of the report.
type doctorCheck struct {
	name   string
	ok     bool
	detail string
}

// runDoctor gathers the checks. It is separated from printing so it can be
// tested directly.
func runDoctor(project string) []doctorCheck {
	var checks []doctorCheck

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	if err := client.FromEnv().Ping(ctx); err == nil {
		checks = append(checks, doctorCheck{"tower", true, "reachable at " + client.Addr()})
	} else {
		checks = append(checks, doctorCheck{"tower", false, "not reachable at " + client.Addr() + " (run `tc serve`, or it auto-starts on the first agent)"})
	}

	if gitDirtySet(project) != nil {
		checks = append(checks, doctorCheck{"git", true, "working tree detected"})
	} else {
		checks = append(checks, doctorCheck{"git", false, "not a git work tree (Bash-edit awareness needs git)"})
	}

	settings := filepath.Join(project, ".claude", "settings.json")
	if hooksWired(settings) {
		checks = append(checks, doctorCheck{"hooks", true, settings})
	} else {
		checks = append(checks, doctorCheck{"hooks", false, "not wired (run `tc install-claude`)"})
	}

	mcp := filepath.Join(project, ".mcp.json")
	if mcpWired(mcp) {
		checks = append(checks, doctorCheck{"mcp", true, mcp})
	} else {
		checks = append(checks, doctorCheck{"mcp", false, "not wired (run `tc install-claude`)"})
	}

	return checks
}

// hooksWired reports whether the settings file carries our SessionStart hook,
// the marker that `tc install-claude` has run here.
func hooksWired(path string) bool {
	root, err := readJSONObject(path)
	if err != nil {
		return false
	}
	hooks, _ := root["hooks"].(map[string]interface{})
	return eventHasCommandContaining(hooks, "SessionStart", "hook session-start")
}

func eventHasCommandContaining(hooks map[string]interface{}, event, substr string) bool {
	arr, _ := hooks[event].([]interface{})
	for _, item := range arr {
		m, _ := item.(map[string]interface{})
		hs, _ := m["hooks"].([]interface{})
		for _, h := range hs {
			hm, _ := h.(map[string]interface{})
			if cmd, _ := hm["command"].(string); strings.Contains(cmd, substr) {
				return true
			}
		}
	}
	return false
}

// mcpWired reports whether the MCP config registers our server.
func mcpWired(path string) bool {
	root, err := readJSONObject(path)
	if err != nil {
		return false
	}
	servers, _ := root["mcpServers"].(map[string]interface{})
	_, ok := servers["traffic-control"]
	return ok
}
