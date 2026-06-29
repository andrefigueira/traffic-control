package main

import (
	"flag"
	"fmt"
	"path/filepath"
	"strings"
)

// cmdUninstallClaude is the inverse of cmdInstallClaude: it removes the hooks and
// MCP server Traffic Control added to a project's config and leaves everything
// else untouched. It is idempotent, so unwiring a project that was never wired
// (or already unwired) is a harmless no-op.
func cmdUninstallClaude(args []string) error {
	fs := flag.NewFlagSet("uninstall-claude", flag.ExitOnError)
	project := fs.String("project", ".", "project directory to unwire")
	_ = fs.Parse(args)

	settingsPath := filepath.Join(*project, ".claude", "settings.json")
	if err := unwireSettings(settingsPath); err != nil {
		return err
	}
	mcpConfigPath := filepath.Join(*project, ".mcp.json")
	if err := unwireMCP(mcpConfigPath); err != nil {
		return err
	}
	fmt.Printf("removed Traffic Control wiring from %s (if any was present)\n", *project)
	return nil
}

// unwireSettings strips our hook commands from .claude/settings.json. It removes
// only our hook objects, dropping an entry only once it has no hooks left, so a
// matcher entry shared with another tool keeps that tool's hooks.
func unwireSettings(path string) error {
	root, err := readJSONObject(path)
	if err != nil {
		return err
	}
	hooks, _ := root["hooks"].(map[string]interface{})
	if hooks == nil {
		return nil // nothing was wired
	}
	for event := range hooks {
		arr, _ := hooks[event].([]interface{})
		var kept []interface{}
		for _, item := range arr {
			m, _ := item.(map[string]interface{})
			hs, ok := m["hooks"].([]interface{})
			if !ok {
				kept = append(kept, item) // shape we do not own, leave it
				continue
			}
			var keptHooks []interface{}
			for _, h := range hs {
				hm, _ := h.(map[string]interface{})
				cmd, _ := hm["command"].(string)
				if !isTCHookCommand(cmd) {
					keptHooks = append(keptHooks, h)
				}
			}
			// Drop the entry only when it was entirely ours; otherwise keep what
			// remains.
			if len(keptHooks) > 0 {
				m["hooks"] = keptHooks
				kept = append(kept, m)
			} else if len(hs) == 0 {
				kept = append(kept, m)
			}
		}
		if len(kept) > 0 {
			hooks[event] = kept
		} else {
			delete(hooks, event)
		}
	}
	if len(hooks) == 0 {
		delete(root, "hooks")
	} else {
		root["hooks"] = hooks
	}
	return writeJSONObject(path, root)
}

// isTCHookCommand recognizes a command string this tool installed, across any
// binary path, by its `hook <event>` shape.
func isTCHookCommand(cmd string) bool {
	for _, ev := range []string{"session-start", "pre-tool-use", "post-tool-use", "stop"} {
		if strings.Contains(cmd, "hook "+ev) {
			return true
		}
	}
	return false
}

// unwireMCP removes our server from .mcp.json, leaving any others in place.
func unwireMCP(path string) error {
	root, err := readJSONObject(path)
	if err != nil {
		return err
	}
	servers, _ := root["mcpServers"].(map[string]interface{})
	if servers == nil {
		return nil
	}
	delete(servers, "traffic-control")
	if len(servers) == 0 {
		delete(root, "mcpServers")
	} else {
		root["mcpServers"] = servers
	}
	return writeJSONObject(path, root)
}
