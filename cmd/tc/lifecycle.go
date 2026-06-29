package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/andrefigueira/traffic-control/internal/client"
)

// stateDir is where the tower keeps its pidfile and auto-start log.
func stateDir() string {
	if d := strings.TrimSpace(os.Getenv("TC_STATE_DIR")); d != "" {
		return d
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".traffic-control")
	}
	return filepath.Join(os.TempDir(), "traffic-control")
}

func pidFile() string { return filepath.Join(stateDir(), "tower.pid") }

func writePidFile() error {
	if err := os.MkdirAll(stateDir(), 0o755); err != nil {
		return err
	}
	return os.WriteFile(pidFile(), []byte(strconv.Itoa(os.Getpid())), 0o644)
}

func removePidFile() { _ = os.Remove(pidFile()) }

// pingTower reports whether a tower answers within d.
func pingTower(d time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	return client.FromEnv().Ping(ctx) == nil
}

// ensureTowerRunning returns true if a tower is reachable, auto-starting a
// detached one if needed. This is what makes the setup frictionless: after the
// plugin is installed, no separate `tc serve` terminal is required. Opt out
// with TC_NO_AUTOSTART=1.
func ensureTowerRunning() bool {
	if pingTower(400 * time.Millisecond) {
		return true
	}
	if os.Getenv("TC_NO_AUTOSTART") == "1" {
		return false
	}
	bin, err := os.Executable()
	if err != nil {
		return false
	}
	_ = os.MkdirAll(stateDir(), 0o755)
	logf, _ := os.OpenFile(filepath.Join(stateDir(), "tower.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)

	cmd := exec.Command(bin, "serve")
	cmd.SysProcAttr = detachSysProcAttr() // detach so it outlives this hook process
	if logf != nil {
		cmd.Stdout = logf
		cmd.Stderr = logf
	}
	if err := cmd.Start(); err != nil {
		return false
	}
	// Give the new tower a moment to bind and answer.
	for i := 0; i < 20; i++ {
		if pingTower(200 * time.Millisecond) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// cmdStop stops the auto-started (or any pidfile-tracked) tower.
func cmdStop(_ []string) error {
	b, err := os.ReadFile(pidFile())
	if err != nil {
		return fmt.Errorf("no tower pidfile at %s; is one running? (try `tc status`)", pidFile())
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return fmt.Errorf("unreadable pidfile %s: %w", pidFile(), err)
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("no process %d: %w", pid, err)
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("stopping tower pid %d: %w", pid, err)
	}
	fmt.Printf("stopped tower (pid %d)\n", pid)
	return nil
}
