package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func sessionStartHooks() map[string]interface{} {
	return map[string]interface{}{
		"SessionStart": []interface{}{
			map[string]interface{}{"hooks": []interface{}{cmdHookEntry("tc", "session-start")}},
		},
	}
}

func sessionStartArray(t *testing.T, path string) []interface{} {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]interface{}
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	return root["hooks"].(map[string]interface{})["SessionStart"].([]interface{})
}

func TestMergeSettingsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	for i := 0; i < 3; i++ {
		if err := mergeSettings(path, sessionStartHooks()); err != nil {
			t.Fatalf("merge %d: %v", i, err)
		}
	}
	if got := len(sessionStartArray(t, path)); got != 1 {
		t.Fatalf("expected 1 SessionStart entry after repeated merges, got %d", got)
	}
}

func TestMergeSettingsPreservesExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	seed := `{"model":"opus","hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"some other tool"}]}]}}`
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := mergeSettings(path, sessionStartHooks()); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	var root map[string]interface{}
	_ = json.Unmarshal(b, &root)
	if root["model"] != "opus" {
		t.Fatal("existing settings must be preserved")
	}
	if got := len(sessionStartArray(t, path)); got != 2 {
		t.Fatalf("expected existing + ours = 2 entries, got %d", got)
	}
}

func TestMergeMCPIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".mcp.json")
	servers := map[string]interface{}{
		"traffic-control": map[string]interface{}{"command": "tc", "args": []interface{}{"mcp"}},
	}
	for i := 0; i < 3; i++ {
		if err := mergeMCP(path, servers); err != nil {
			t.Fatalf("merge %d: %v", i, err)
		}
	}
	b, _ := os.ReadFile(path)
	var root map[string]interface{}
	_ = json.Unmarshal(b, &root)
	if got := len(root["mcpServers"].(map[string]interface{})); got != 1 {
		t.Fatalf("expected 1 mcp server, got %d", got)
	}
}
