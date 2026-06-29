package main

import (
	"context"
	"strings"
	"testing"

	"github.com/andrefigueira/traffic-control/internal/protocol"
)

func TestCmdStatus(t *testing.T) {
	t.Run("reports a live tower", func(t *testing.T) {
		c, _ := startTower(t)
		if _, err := c.Register(context.Background(), "alpha", "proj", 0); err != nil {
			t.Fatal(err)
		}
		out := captureStdout(t, func() {
			if err := cmdStatus(nil); err != nil {
				t.Fatalf("cmdStatus: %v", err)
			}
		})
		if !strings.Contains(out, "tower is up") {
			t.Fatalf("output = %q", out)
		}
		if !strings.Contains(out, "alpha") {
			t.Fatalf("status should list flying agents, got %q", out)
		}
	})
	t.Run("errors when the tower is down", func(t *testing.T) {
		deadAddr(t)
		err := cmdStatus(nil)
		if err == nil {
			t.Fatal("expected an error when the tower is unreachable")
		}
	})
}

func TestCmdClearance(t *testing.T) {
	t.Run("granted", func(t *testing.T) {
		startTower(t)
		out := captureStdout(t, func() {
			if err := cmdClearance([]string{"--callsign", "alpha", "x.go"}); err != nil {
				t.Fatalf("cmdClearance: %v", err)
			}
		})
		if !strings.Contains(out, "CLEARED") {
			t.Fatalf("output = %q", out)
		}
	})
	t.Run("denied on an exclusive conflict", func(t *testing.T) {
		startTower(t)
		if err := cmdClearance([]string{"--callsign", "alpha", "--mode", "exclusive", "shared.go"}); err != nil {
			t.Fatalf("alpha clearance: %v", err)
		}
		err := cmdClearance([]string{"--callsign", "bravo", "shared.go"})
		if err == nil || !strings.Contains(err.Error(), "DENIED") {
			t.Fatalf("expected a DENIED error, got %v", err)
		}
	})
	t.Run("requires a path", func(t *testing.T) {
		startTower(t)
		if err := cmdClearance(nil); err == nil {
			t.Fatal("expected an error when no path is given")
		}
	})
}

func TestCmdHandoff(t *testing.T) {
	c, _ := startTower(t)
	if _, err := c.RequestClearance(context.Background(), "alpha", "x.go", protocol.ModeExclusive, "", 0); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if err := cmdHandoff([]string{"--callsign", "alpha", "x.go"}); err != nil {
			t.Fatalf("cmdHandoff: %v", err)
		}
	})
	if !strings.Contains(out, "handed off 1") {
		t.Fatalf("output = %q", out)
	}
}

func TestCmdHandoffTowerDown(t *testing.T) {
	deadAddr(t)
	if err := cmdHandoff([]string{"--callsign", "alpha"}); err == nil {
		t.Fatal("expected an error when the tower is down")
	}
}

func TestCmdCheck(t *testing.T) {
	t.Run("requires a path", func(t *testing.T) {
		startTower(t)
		if err := cmdCheck(nil); err == nil {
			t.Fatal("expected an error with no path")
		}
	})
	t.Run("reports a clear path", func(t *testing.T) {
		startTower(t)
		out := captureStdout(t, func() {
			if err := cmdCheck([]string{"free.go"}); err != nil {
				t.Fatalf("cmdCheck: %v", err)
			}
		})
		if !strings.Contains(out, "is clear") {
			t.Fatalf("output = %q", out)
		}
	})
	t.Run("reports a held path", func(t *testing.T) {
		c, _ := startTower(t)
		if _, err := c.RequestClearance(context.Background(), "alpha", "held.go", protocol.ModeExclusive, "", 0); err != nil {
			t.Fatal(err)
		}
		out := captureStdout(t, func() {
			if err := cmdCheck([]string{"held.go"}); err != nil {
				t.Fatalf("cmdCheck: %v", err)
			}
		})
		if !strings.Contains(out, "held by alpha") {
			t.Fatalf("output = %q", out)
		}
	})
}

func TestCmdBoardPost(t *testing.T) {
	t.Run("requires a message", func(t *testing.T) {
		startTower(t)
		if err := cmdBoardPost(nil, "flightplan"); err == nil {
			t.Fatal("expected an error with no message")
		}
	})
	t.Run("posts a flight plan", func(t *testing.T) {
		startTower(t)
		out := captureStdout(t, func() {
			if err := cmdBoardPost([]string{"--callsign", "alpha", "reworking", "the", "auth"}, "flightplan"); err != nil {
				t.Fatalf("cmdBoardPost: %v", err)
			}
		})
		if !strings.Contains(out, "posted to the board") || !strings.Contains(out, "reworking the auth") {
			t.Fatalf("output = %q", out)
		}
	})
}

func TestCmdWhosFlying(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		startTower(t)
		out := captureStdout(t, func() {
			if err := cmdWhosFlying(nil); err != nil {
				t.Fatalf("cmdWhosFlying: %v", err)
			}
		})
		if !strings.Contains(out, "nobody is flying") {
			t.Fatalf("output = %q", out)
		}
	})
	t.Run("lists agents", func(t *testing.T) {
		c, _ := startTower(t)
		if _, err := c.Register(context.Background(), "alpha", "myproj", 0); err != nil {
			t.Fatal(err)
		}
		out := captureStdout(t, func() {
			if err := cmdWhosFlying(nil); err != nil {
				t.Fatalf("cmdWhosFlying: %v", err)
			}
		})
		if !strings.Contains(out, "alpha") || !strings.Contains(out, "myproj") {
			t.Fatalf("output = %q", out)
		}
	})
	t.Run("errors when the tower is down", func(t *testing.T) {
		deadAddr(t)
		if err := cmdWhosFlying(nil); err == nil {
			t.Fatal("expected an error when the tower is unreachable")
		}
	})
}

func TestCmdBoard(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		startTower(t)
		out := captureStdout(t, func() {
			if err := cmdBoard(nil); err != nil {
				t.Fatalf("cmdBoard: %v", err)
			}
		})
		if !strings.Contains(out, "the board is empty") {
			t.Fatalf("output = %q", out)
		}
	})
	t.Run("shows entries with paths", func(t *testing.T) {
		c, _ := startTower(t)
		if _, err := c.PostBoard(context.Background(), "alpha", protocol.KindFlightPlan, "auth work", []string{"auth.go"}); err != nil {
			t.Fatal(err)
		}
		out := captureStdout(t, func() {
			if err := cmdBoard([]string{"--limit", "5"}); err != nil {
				t.Fatalf("cmdBoard: %v", err)
			}
		})
		if !strings.Contains(out, "auth work") || !strings.Contains(out, "auth.go") {
			t.Fatalf("output = %q", out)
		}
	})
}

func TestSummarize(t *testing.T) {
	cases := []struct {
		name    string
		payload interface{}
		want    string
	}{
		{"callsign preferred", map[string]interface{}{"callsign": "alpha", "path": "x.go"}, "alpha"},
		{"holder", map[string]interface{}{"holder": "bravo"}, "bravo"},
		{"requester for conflict", map[string]interface{}{"requester": "charlie", "held_by": "d"}, "charlie"},
		{"falls back to path", map[string]interface{}{"path": "x.go"}, "x.go"},
		{"message last", map[string]interface{}{"message": "hi"}, "hi"},
		{"non-map yields empty", "not a map", ""},
		{"empty map yields empty", map[string]interface{}{}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := summarize(c.payload); got != c.want {
				t.Fatalf("summarize(%v) = %q, want %q", c.payload, got, c.want)
			}
		})
	}
}
