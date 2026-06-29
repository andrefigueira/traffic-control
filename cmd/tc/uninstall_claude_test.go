package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestUninstallRemovesWiring(t *testing.T) {
	dir := t.TempDir()
	if err := cmdInstallClaude([]string{"--project", dir}); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(dir, ".claude", "settings.json")
	mcp := filepath.Join(dir, ".mcp.json")
	if !hooksWired(settings) || !mcpWired(mcp) {
		t.Fatal("precondition: install should have wired the project")
	}

	if err := cmdUninstallClaude([]string{"--project", dir}); err != nil {
		t.Fatal(err)
	}
	if hooksWired(settings) {
		t.Fatal("hooks should be gone after uninstall")
	}
	if mcpWired(mcp) {
		t.Fatal("mcp server should be gone after uninstall")
	}
}

func TestUninstallPreservesOtherConfig(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(settings, 0o755); err != nil {
		t.Fatal(err)
	}
	// Seed config that belongs to the user and another tool's hook + MCP server.
	seed := `{"model":"opus","hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"some other tool"}]}]}}`
	if err := os.WriteFile(filepath.Join(settings, "settings.json"), []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	mcpSeed := `{"mcpServers":{"other":{"command":"x"}}}`
	if err := os.WriteFile(filepath.Join(dir, ".mcp.json"), []byte(mcpSeed), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := cmdInstallClaude([]string{"--project", dir}); err != nil {
		t.Fatal(err)
	}
	if err := cmdUninstallClaude([]string{"--project", dir}); err != nil {
		t.Fatal(err)
	}

	// Our wiring is gone; theirs survives.
	var root map[string]interface{}
	b, _ := os.ReadFile(filepath.Join(settings, "settings.json"))
	_ = json.Unmarshal(b, &root)
	if root["model"] != "opus" {
		t.Fatal("unrelated settings should be preserved")
	}
	hooks := root["hooks"].(map[string]interface{})
	ss := hooks["SessionStart"].([]interface{})
	if len(ss) != 1 {
		t.Fatalf("the other tool's SessionStart hook should remain, got %d entries", len(ss))
	}
	if eventHasCommandContaining(hooks, "SessionStart", "hook session-start") {
		t.Fatal("our hook should be gone")
	}

	var mroot map[string]interface{}
	mb, _ := os.ReadFile(filepath.Join(dir, ".mcp.json"))
	_ = json.Unmarshal(mb, &mroot)
	servers := mroot["mcpServers"].(map[string]interface{})
	if _, ok := servers["other"]; !ok {
		t.Fatal("the other MCP server should be preserved")
	}
	if _, ok := servers["traffic-control"]; ok {
		t.Fatal("our MCP server should be gone")
	}
}

func TestUninstallIsIdempotentAndSafeOnBareProject(t *testing.T) {
	dir := t.TempDir()
	// No config at all: uninstall must be a harmless no-op and create no files.
	if err := cmdUninstallClaude([]string{"--project", dir}); err != nil {
		t.Fatalf("uninstall on a bare project should not error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Fatal("uninstall should not create a settings file")
	}
	if _, err := os.Stat(filepath.Join(dir, ".mcp.json")); !os.IsNotExist(err) {
		t.Fatal("uninstall should not create an .mcp.json")
	}
}

func TestIsTCHookCommand(t *testing.T) {
	for _, cmd := range []string{
		"/usr/bin/tc hook session-start",
		"tc hook pre-tool-use",
		"/x/y/tc hook stop",
	} {
		if !isTCHookCommand(cmd) {
			t.Fatalf("%q should be recognized as ours", cmd)
		}
	}
	for _, cmd := range []string{"some other tool", "tc serve", "claude hook custom"} {
		if isTCHookCommand(cmd) {
			t.Fatalf("%q should not be recognized as ours", cmd)
		}
	}
}
