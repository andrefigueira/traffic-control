package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCmdInstallClaudePrint(t *testing.T) {
	out := captureStdout(t, func() {
		if err := cmdInstallClaude([]string{"--print"}); err != nil {
			t.Fatalf("cmdInstallClaude --print: %v", err)
		}
	})
	// Print mode emits both snippets and writes no files.
	if !strings.Contains(out, "settings.json") || !strings.Contains(out, "mcpServers") {
		t.Fatalf("print output missing snippets: %q", out)
	}
	if !strings.Contains(out, "SessionStart") || !strings.Contains(out, "PreToolUse") {
		t.Fatalf("print output missing hook events: %q", out)
	}
}

func TestCmdInstallClaudeWritesConfig(t *testing.T) {
	dir := t.TempDir()
	out := captureStdout(t, func() {
		if err := cmdInstallClaude([]string{"--project", dir}); err != nil {
			t.Fatalf("cmdInstallClaude: %v", err)
		}
	})
	if !strings.Contains(out, "wired Traffic Control") {
		t.Fatalf("output = %q", out)
	}

	settings := filepath.Join(dir, ".claude", "settings.json")
	b, err := os.ReadFile(settings)
	if err != nil {
		t.Fatalf("settings not written: %v", err)
	}
	var root map[string]interface{}
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("settings not valid json: %v", err)
	}
	hooks := root["hooks"].(map[string]interface{})
	for _, ev := range []string{"SessionStart", "PreToolUse", "PostToolUse", "Stop"} {
		if _, ok := hooks[ev]; !ok {
			t.Fatalf("missing hook event %q in %+v", ev, hooks)
		}
	}
	// Bash must be in the Pre/Post matchers so Bash-driven edits are coordinated.
	for _, ev := range []string{"PreToolUse", "PostToolUse"} {
		entry := hooks[ev].([]interface{})[0].(map[string]interface{})
		if m, _ := entry["matcher"].(string); !strings.Contains(m, "Bash") {
			t.Fatalf("%s matcher should include Bash, got %q", ev, m)
		}
	}

	mcpPath := filepath.Join(dir, ".mcp.json")
	mb, err := os.ReadFile(mcpPath)
	if err != nil {
		t.Fatalf(".mcp.json not written: %v", err)
	}
	var mroot map[string]interface{}
	_ = json.Unmarshal(mb, &mroot)
	if _, ok := mroot["mcpServers"].(map[string]interface{})["traffic-control"]; !ok {
		t.Fatalf("mcp server not registered: %s", mb)
	}
}

func TestReadJSONObject(t *testing.T) {
	t.Run("missing file is an empty object", func(t *testing.T) {
		root, err := readJSONObject(filepath.Join(t.TempDir(), "nope.json"))
		if err != nil || len(root) != 0 {
			t.Fatalf("err=%v root=%+v", err, root)
		}
	})
	t.Run("invalid json errors", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "bad.json")
		_ = os.WriteFile(p, []byte("{not json"), 0o644)
		if _, err := readJSONObject(p); err == nil {
			t.Fatal("expected an error on invalid json")
		}
	})
	t.Run("valid json round-trips", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "ok.json")
		_ = os.WriteFile(p, []byte(`{"a":1}`), 0o644)
		root, err := readJSONObject(p)
		if err != nil || root["a"].(float64) != 1 {
			t.Fatalf("err=%v root=%+v", err, root)
		}
	})
}

func TestWriteJSONObjectRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "out.json") // dir does not exist yet
	if err := writeJSONObject(p, map[string]interface{}{"k": "v"}); err != nil {
		t.Fatalf("writeJSONObject: %v", err)
	}
	root, err := readJSONObject(p)
	if err != nil || root["k"] != "v" {
		t.Fatalf("round trip err=%v root=%+v", err, root)
	}
}

func TestCommandInAndArrayHasCommand(t *testing.T) {
	arr := []interface{}{
		map[string]interface{}{"hooks": []interface{}{
			map[string]interface{}{"type": "command", "command": "tc hook stop"},
		}},
	}
	if got := commandIn(arr); got != "tc hook stop" {
		t.Fatalf("commandIn = %q", got)
	}
	if !arrayHasCommand(arr, "tc hook stop") {
		t.Fatal("arrayHasCommand should find the present command")
	}
	if arrayHasCommand(arr, "tc hook other") {
		t.Fatal("arrayHasCommand should not find an absent command")
	}
	if got := commandIn(nil); got != "" {
		t.Fatalf("commandIn(nil) = %q, want empty", got)
	}
}

func TestCmdHookEntry(t *testing.T) {
	e := cmdHookEntry("/usr/bin/tc", "session-start")
	if e["type"] != "command" || e["command"] != "/usr/bin/tc hook session-start" {
		t.Fatalf("entry = %+v", e)
	}
}
