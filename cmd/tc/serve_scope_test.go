package main

import (
	"net"
	"strings"
	"testing"
)

// TestCmdServeBindError covers the only synchronously-testable serve branch: if
// the port is already taken, serve must fail cleanly before any side effects.
func TestCmdServeBindError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	addr := ln.Addr().String()

	err = cmdServe([]string{"--addr", addr})
	if err == nil {
		t.Fatal("expected a bind error on an occupied port")
	}
	if !strings.Contains(err.Error(), "could not bind") {
		t.Fatalf("error = %v", err)
	}
}

func TestCmdScopeTowerDown(t *testing.T) {
	t.Setenv("TC_ADDR", "127.0.0.1:1")
	err := cmdScope(nil)
	if err == nil || !strings.Contains(err.Error(), "not reachable") {
		t.Fatalf("expected a not-reachable error, got %v", err)
	}
}
