package main

import (
	"path/filepath"
	"testing"
)

func TestRunDoctorAllGood(t *testing.T) {
	startTower(t) // sets TC_ADDR to a live tower
	dir := initGitRepo(t)
	// Wire the project so hooks and mcp checks pass.
	if err := cmdInstallClaude([]string{"--project", dir}); err != nil {
		t.Fatal(err)
	}

	checks := byName(runDoctor(dir))
	for _, name := range []string{"tower", "git", "hooks", "mcp"} {
		if !checks[name].ok {
			t.Fatalf("check %q should pass: %+v", name, checks[name])
		}
	}
}

func TestRunDoctorNothingWired(t *testing.T) {
	deadAddr(t)        // tower unreachable
	dir := t.TempDir() // not a git repo, no .claude, no .mcp.json

	checks := byName(runDoctor(dir))
	for _, name := range []string{"tower", "git", "hooks", "mcp"} {
		if checks[name].ok {
			t.Fatalf("check %q should warn in a bare dir: %+v", name, checks[name])
		}
	}
}

func TestHooksWiredAndMcpWired(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, ".claude", "settings.json")
	mcp := filepath.Join(dir, ".mcp.json")
	if hooksWired(settings) || mcpWired(mcp) {
		t.Fatal("a bare project should report nothing wired")
	}
	if err := cmdInstallClaude([]string{"--project", dir}); err != nil {
		t.Fatal(err)
	}
	if !hooksWired(settings) {
		t.Fatal("hooks should be detected after install")
	}
	if !mcpWired(mcp) {
		t.Fatal("mcp should be detected after install")
	}
}

func byName(checks []doctorCheck) map[string]doctorCheck {
	m := make(map[string]doctorCheck, len(checks))
	for _, c := range checks {
		m[c.name] = c
	}
	return m
}
