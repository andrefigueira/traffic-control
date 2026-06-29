package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestStateDir(t *testing.T) {
	t.Run("honours TC_STATE_DIR", func(t *testing.T) {
		t.Setenv("TC_STATE_DIR", "/custom/state")
		if got := stateDir(); got != "/custom/state" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("defaults under the home directory", func(t *testing.T) {
		t.Setenv("TC_STATE_DIR", "")
		got := stateDir()
		if !strings.HasSuffix(got, ".traffic-control") {
			t.Fatalf("got %q, want a .traffic-control dir", got)
		}
	})
	t.Run("falls back to temp when home is undiscoverable", func(t *testing.T) {
		t.Setenv("TC_STATE_DIR", "")
		t.Setenv("HOME", "") // makes UserHomeDir fail on unix
		got := stateDir()
		if runtime.GOOS != "windows" && !strings.HasPrefix(got, os.TempDir()) {
			t.Fatalf("got %q, want a path under the temp dir", got)
		}
	})
}

func TestPidFilePath(t *testing.T) {
	t.Setenv("TC_STATE_DIR", "/s")
	if got := pidFile(); got != filepath.Join("/s", "tower.pid") {
		t.Fatalf("got %q", got)
	}
}

func TestWriteAndRemovePidFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TC_STATE_DIR", dir)
	if err := writePidFile(); err != nil {
		t.Fatalf("writePidFile: %v", err)
	}
	b, err := os.ReadFile(pidFile())
	if err != nil {
		t.Fatalf("read pidfile: %v", err)
	}
	if got := strings.TrimSpace(string(b)); got != strconv.Itoa(os.Getpid()) {
		t.Fatalf("pidfile = %q, want our pid %d", got, os.Getpid())
	}
	removePidFile()
	if _, err := os.Stat(pidFile()); !os.IsNotExist(err) {
		t.Fatalf("pidfile should be gone, stat err = %v", err)
	}
}

func TestPingTower(t *testing.T) {
	t.Run("false when nothing listens", func(t *testing.T) {
		t.Setenv("TC_ADDR", "127.0.0.1:1")
		if pingTower(200 * time.Millisecond) {
			t.Fatal("expected false against a dead address")
		}
	})
	t.Run("true against a live tower", func(t *testing.T) {
		startTower(t) // sets TC_ADDR
		if !pingTower(time.Second) {
			t.Fatal("expected true against a live tower")
		}
	})
}

func TestEnsureTowerRunning(t *testing.T) {
	t.Run("true when one is already up", func(t *testing.T) {
		startTower(t)
		if !ensureTowerRunning() {
			t.Fatal("expected true with a live tower")
		}
	})
	t.Run("false when down and autostart is disabled", func(t *testing.T) {
		t.Setenv("TC_ADDR", "127.0.0.1:1")
		t.Setenv("TC_NO_AUTOSTART", "1")
		if ensureTowerRunning() {
			t.Fatal("expected false: no tower and autostart opted out")
		}
	})
}

func TestCmdStopErrors(t *testing.T) {
	t.Run("no pidfile", func(t *testing.T) {
		t.Setenv("TC_STATE_DIR", t.TempDir())
		if err := cmdStop(nil); err == nil {
			t.Fatal("expected an error with no pidfile")
		}
	})
	t.Run("unreadable pidfile", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("TC_STATE_DIR", dir)
		if err := os.WriteFile(filepath.Join(dir, "tower.pid"), []byte("not-a-number"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := cmdStop(nil); err == nil {
			t.Fatal("expected an error on a non-numeric pidfile")
		}
	})
}

// TestCmdStopSignalsProcess starts a real child, records its pid, and verifies
// cmdStop signals it down. Unix-only because it relies on SIGTERM semantics.
func TestCmdStopSignalsProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGTERM semantics are unix-only")
	}
	dir := t.TempDir()
	t.Setenv("TC_STATE_DIR", dir)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, "sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tower.pid"), []byte(strconv.Itoa(cmd.Process.Pid)), 0o644); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		if err := cmdStop(nil); err != nil {
			t.Fatalf("cmdStop: %v", err)
		}
	})
	if !strings.Contains(out, "stopped tower") {
		t.Fatalf("output = %q", out)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done: // the process exited because it was signalled
	case <-time.After(5 * time.Second):
		t.Fatal("process was not stopped by cmdStop")
	}
}
