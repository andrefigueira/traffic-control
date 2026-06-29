package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

// cmdInstallClaude wires Traffic Control into a project's Claude Code config:
// the four hooks into .claude/settings.json and the MCP server into .mcp.json.
// It merges into existing config rather than clobbering it, and is idempotent.
func cmdInstallClaude(args []string) error {
	fs := flag.NewFlagSet("install-claude", flag.ExitOnError)
	project := fs.String("project", ".", "project directory to wire up")
	print := fs.Bool("print", false, "print the config snippets instead of writing them")
	_ = fs.Parse(args)

	// Use the absolute path to this binary so hooks work even off PATH.
	bin, err := os.Executable()
	if err != nil || bin == "" {
		bin = "tc"
	}

	hooks := map[string]interface{}{
		"SessionStart": []interface{}{
			map[string]interface{}{"hooks": []interface{}{cmdHookEntry(bin, "session-start")}},
		},
		"PreToolUse": []interface{}{
			map[string]interface{}{
				"matcher": "Edit|Write|MultiEdit|NotebookEdit|Bash",
				"hooks":   []interface{}{cmdHookEntry(bin, "pre-tool-use")},
			},
		},
		"PostToolUse": []interface{}{
			map[string]interface{}{
				"matcher": "Edit|Write|MultiEdit|NotebookEdit|Bash",
				"hooks":   []interface{}{cmdHookEntry(bin, "post-tool-use")},
			},
		},
		"Stop": []interface{}{
			map[string]interface{}{"hooks": []interface{}{cmdHookEntry(bin, "stop")}},
		},
	}
	mcp := map[string]interface{}{
		"traffic-control": map[string]interface{}{"command": bin, "args": []interface{}{"mcp"}},
	}

	if *print {
		fmt.Println("// add to .claude/settings.json")
		printJSON(map[string]interface{}{"hooks": hooks})
		fmt.Println("\n// add to .mcp.json")
		printJSON(map[string]interface{}{"mcpServers": mcp})
		return nil
	}

	settingsPath := filepath.Join(*project, ".claude", "settings.json")
	if err := mergeSettings(settingsPath, hooks); err != nil {
		return err
	}
	mcpConfigPath := filepath.Join(*project, ".mcp.json")
	if err := mergeMCP(mcpConfigPath, mcp); err != nil {
		return err
	}
	fmt.Printf("wired Traffic Control into %s\n", *project)
	fmt.Printf("  hooks: %s\n", settingsPath)
	fmt.Printf("  mcp:   %s\n", mcpConfigPath)
	fmt.Println("just run Claude Code in this project; the first agent auto-starts the tower (or run `tc serve` yourself).")
	return nil
}

func cmdHookEntry(bin, event string) map[string]interface{} {
	return map[string]interface{}{"type": "command", "command": bin + " hook " + event}
}

// mergeSettings adds our hook events to .claude/settings.json without
// disturbing other settings or duplicating our entries.
func mergeSettings(path string, hooks map[string]interface{}) error {
	root, err := readJSONObject(path)
	if err != nil {
		return err
	}
	existing, _ := root["hooks"].(map[string]interface{})
	if existing == nil {
		existing = map[string]interface{}{}
	}
	for event, entry := range hooks {
		arr, _ := existing[event].([]interface{})
		add := entry.([]interface{})
		cmd := commandIn(add)
		if cmd == "" || !arrayHasCommand(arr, cmd) {
			existing[event] = append(arr, add...)
		}
	}
	root["hooks"] = existing
	return writeJSONObject(path, root)
}

func mergeMCP(path string, servers map[string]interface{}) error {
	root, err := readJSONObject(path)
	if err != nil {
		return err
	}
	existing, _ := root["mcpServers"].(map[string]interface{})
	if existing == nil {
		existing = map[string]interface{}{}
	}
	for name, cfg := range servers {
		existing[name] = cfg
	}
	root["mcpServers"] = existing
	return writeJSONObject(path, root)
}

// commandIn returns the first hook command string found in an entry array.
func commandIn(arr []interface{}) string {
	for _, item := range arr {
		m, _ := item.(map[string]interface{})
		hs, _ := m["hooks"].([]interface{})
		for _, h := range hs {
			hm, _ := h.(map[string]interface{})
			if cmd, _ := hm["command"].(string); cmd != "" {
				return cmd
			}
		}
	}
	return ""
}

// arrayHasCommand reports whether a hook array already contains the exact
// command string, which keeps the merge idempotent across re-runs.
func arrayHasCommand(arr []interface{}, cmd string) bool {
	for _, item := range arr {
		m, _ := item.(map[string]interface{})
		hs, _ := m["hooks"].([]interface{})
		for _, h := range hs {
			hm, _ := h.(map[string]interface{})
			if c, _ := hm["command"].(string); c == cmd {
				return true
			}
		}
	}
	return false
}

func readJSONObject(path string) (map[string]interface{}, error) {
	root := map[string]interface{}{}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return root, nil
		}
		return nil, err
	}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &root); err != nil {
			return nil, fmt.Errorf("%s is not valid JSON: %w", path, err)
		}
	}
	return root, nil
}

func writeJSONObject(path string, root map[string]interface{}) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o644)
}

func printJSON(v interface{}) {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(b))
}
