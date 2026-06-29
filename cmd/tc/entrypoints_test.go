package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestCmdMCPLoop feeds the stdio JSON-RPC framing the MCP transport uses and
// confirms requests get exactly one response while notifications and garbage get
// none.
func TestCmdMCPLoop(t *testing.T) {
	t.Setenv("TC_ADDR", "127.0.0.1:1") // tools/list needs no tower
	setStdin(t, strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`, // a notification: no reply
		`not even json`, // garbage: skipped
		"",              // blank line: skipped
	}, "\n")+"\n")

	out := captureStdout(t, func() {
		if err := cmdMCP(nil); err != nil {
			t.Fatalf("cmdMCP: %v", err)
		}
	})

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected exactly one response line, got %d: %q", len(lines), out)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal([]byte(lines[0]), &resp); err != nil {
		t.Fatalf("response not json: %v (%s)", err, lines[0])
	}
	if resp["id"].(float64) != 1 {
		t.Fatalf("response should echo id 1, got %+v", resp["id"])
	}
	if !strings.Contains(lines[0], `"tools"`) {
		t.Fatalf("tools/list response missing tools: %s", lines[0])
	}
}

// TestCmdHookRoutesEvents exercises the hook entrypoint's event switch through
// the real stdin path against a live tower, end to end.
func TestCmdHookRoutesEvents(t *testing.T) {
	_, tw := startTower(t)

	setStdin(t, `{"session_id":"router","cwd":"/tmp/proj"}`)
	out := captureStdout(t, func() {
		if err := cmdHook([]string{"session-start"}); err != nil {
			t.Fatalf("session-start: %v", err)
		}
	})
	if !strings.Contains(out, "Traffic Control is active") {
		t.Fatalf("session-start should inject context, got %q", out)
	}

	setStdin(t, `{"session_id":"router","tool_name":"Edit","tool_input":{"file_path":"app.go"}}`)
	captureStdout(t, func() {
		if err := cmdHook([]string{"pre-tool-use"}); err != nil {
			t.Fatalf("pre-tool-use: %v", err)
		}
	})
	if len(tw.Clearances()) == 0 {
		t.Fatal("pre-tool-use should take a clearance")
	}

	setStdin(t, `{"session_id":"router"}`)
	if err := cmdHook([]string{"post-tool-use"}); err != nil {
		t.Fatalf("post-tool-use: %v", err)
	}

	setStdin(t, `{"session_id":"router"}`)
	if err := cmdHook([]string{"stop"}); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if len(tw.Clearances()) != 0 {
		t.Fatal("stop should hand off all clearances")
	}
}

// TestTrivialHelpers covers the small constructors that have no branches but are
// part of the wiring, so a regression in them is caught rather than silently
// shipped.
func TestTrivialHelpers(t *testing.T) {
	if mustCli() == nil {
		t.Fatal("mustCli returned nil")
	}
	ctx, cancel := backgroundCtx()
	if ctx == nil {
		t.Fatal("backgroundCtx returned a nil context")
	}
	cancel()
	// detachSysProcAttr is platform-specific; just confirm it does not panic.
	_ = detachSysProcAttr()
}
