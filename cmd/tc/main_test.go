package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCLISmoke builds the real binary once and drives it as a subprocess, which
// is the only way to exercise main()'s dispatch and the process exit codes the
// harness contract depends on.
func TestCLISmoke(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "tc")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	run := func(args ...string) (string, string, int) {
		cmd := exec.Command(bin, args...)
		// Point at a dead tower with autostart off so command paths fail fast and
		// deterministically instead of spawning a real daemon.
		cmd.Env = append(os.Environ(), "TC_ADDR=127.0.0.1:1", "TC_NO_AUTOSTART=1")
		var so, se bytes.Buffer
		cmd.Stdout, cmd.Stderr = &so, &se
		err := cmd.Run()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatalf("run %v: %v", args, err)
		}
		return so.String(), se.String(), code
	}

	t.Run("version exits 0", func(t *testing.T) {
		so, _, code := run("version")
		if code != 0 || !strings.Contains(so, "tc ") {
			t.Fatalf("code=%d out=%q", code, so)
		}
	})
	t.Run("help exits 0", func(t *testing.T) {
		so, _, code := run("--help")
		if code != 0 || !strings.Contains(so, "Usage:") {
			t.Fatalf("code=%d out=%q", code, so)
		}
	})
	t.Run("no args exits 2 with usage", func(t *testing.T) {
		_, se, code := run()
		if code != 2 || !strings.Contains(se, "Usage:") {
			t.Fatalf("code=%d err=%q", code, se)
		}
	})
	t.Run("unknown command exits 2", func(t *testing.T) {
		_, se, code := run("bogus")
		if code != 2 || !strings.Contains(se, "unknown command") {
			t.Fatalf("code=%d err=%q", code, se)
		}
	})
	t.Run("a failing command exits 1", func(t *testing.T) {
		// check needs the tower, which is dead, so this drives the error->exit(1) path.
		_, se, code := run("check", "x.go")
		if code != 1 || !strings.Contains(se, "error:") {
			t.Fatalf("code=%d err=%q", code, se)
		}
	})
}
