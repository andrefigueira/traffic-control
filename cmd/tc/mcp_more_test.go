package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/andrefigueira/traffic-control/internal/client"
	"github.com/andrefigueira/traffic-control/internal/protocol"
)

func TestMcpCallsign(t *testing.T) {
	t.Run("env wins", func(t *testing.T) {
		t.Setenv("TC_CALLSIGN", "mcp-id")
		if got := mcpCallsign(); got != "mcp-id" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("derives when unset", func(t *testing.T) {
		t.Setenv("TC_CALLSIGN", "")
		if mcpCallsign() == "" {
			t.Fatal("derived callsign must not be empty")
		}
	})
}

func TestMcpCwdAndPath(t *testing.T) {
	if mcpCwd() == "" {
		t.Fatal("mcpCwd should return the working directory")
	}
	if got := mcpPath(""); got != "" {
		t.Fatalf("empty path should stay empty, got %q", got)
	}
	if got := mcpPath("rel.go"); got != "rel.go" {
		t.Fatalf("a relative path under cwd should normalize to itself, got %q", got)
	}
}

func TestHandleRPCInitialize(t *testing.T) {
	c := client.New("127.0.0.1:1") // register is best-effort and ignored
	resp := handleRPC(c, "tester", rpcRequest{ID: json.RawMessage("1"), Method: "initialize"})
	if resp.Error != nil {
		t.Fatalf("initialize should not error: %+v", resp.Error)
	}
	res := resp.Result.(map[string]interface{})
	if res["protocolVersion"] != "2024-11-05" {
		t.Fatalf("protocolVersion = %v", res["protocolVersion"])
	}
	si := res["serverInfo"].(map[string]interface{})
	if si["name"] != "traffic-control" {
		t.Fatalf("serverInfo = %+v", si)
	}
}

func TestHandleRPCPing(t *testing.T) {
	c := client.New("127.0.0.1:1")
	resp := handleRPC(c, "tester", rpcRequest{ID: json.RawMessage("9"), Method: "ping"})
	if resp.Error != nil || resp.Result == nil {
		t.Fatalf("ping should return an empty result, got err=%+v res=%+v", resp.Error, resp.Result)
	}
}

func TestDispatchToolWhosFlying(t *testing.T) {
	c, _ := startTower(t)
	ctx := context.Background()
	out, err := dispatchTool(ctx, c, "me", "whos_flying", nil)
	if err != nil || !strings.Contains(out, "Nobody is flying") {
		t.Fatalf("empty: err=%v out=%q", err, out)
	}
	if _, err := c.Register(ctx, "alpha", "p", 0); err != nil {
		t.Fatal(err)
	}
	out, err = dispatchTool(ctx, c, "me", "whos_flying", nil)
	if err != nil || !strings.Contains(out, "alpha") {
		t.Fatalf("populated: err=%v out=%q", err, out)
	}
}

func TestDispatchToolReadBoard(t *testing.T) {
	c, _ := startTower(t)
	ctx := context.Background()
	out, err := dispatchTool(ctx, c, "me", "read_board", nil)
	if err != nil || !strings.Contains(out, "board is empty") {
		t.Fatalf("empty: err=%v out=%q", err, out)
	}
	if _, err := c.PostBoard(ctx, "alpha", protocol.KindNote, "hello", nil); err != nil {
		t.Fatal(err)
	}
	out, err = dispatchTool(ctx, c, "me", "read_board", json.RawMessage(`{"limit":5}`))
	if err != nil || !strings.Contains(out, "hello") {
		t.Fatalf("populated: err=%v out=%q", err, out)
	}
}

func TestDispatchToolFileFlightPlan(t *testing.T) {
	c, tw := startTower(t)
	ctx := context.Background()
	if _, err := dispatchTool(ctx, c, "me", "file_flight_plan", json.RawMessage(`{}`)); err == nil {
		t.Fatal("a flight plan with no message should error")
	}
	out, err := dispatchTool(ctx, c, "me", "file_flight_plan", json.RawMessage(`{"message":"reworking","paths":["a.go"]}`))
	if err != nil || !strings.Contains(out, "Flight plan posted") {
		t.Fatalf("err=%v out=%q", err, out)
	}
	if got := tw.ReadBoard(10); len(got) != 1 || got[0].Kind != protocol.KindFlightPlan {
		t.Fatalf("board = %+v", got)
	}
}

func TestDispatchToolRequestClearance(t *testing.T) {
	c, _ := startTower(t)
	ctx := context.Background()
	if _, err := dispatchTool(ctx, c, "me", "request_clearance", json.RawMessage(`{}`)); err == nil {
		t.Fatal("a clearance with no path should error")
	}
	out, err := dispatchTool(ctx, c, "me", "request_clearance", json.RawMessage(`{"path":"x.go"}`))
	if err != nil || !strings.Contains(out, "CLEARED") {
		t.Fatalf("grant: err=%v out=%q", err, out)
	}
	// Another agent now holds it exclusively, so the next request is denied.
	if _, err := c.RequestClearance(ctx, "other", "y.go", protocol.ModeExclusive, "", 0); err != nil {
		t.Fatal(err)
	}
	out, err = dispatchTool(ctx, c, "me", "request_clearance", json.RawMessage(`{"path":"y.go"}`))
	if err != nil || !strings.Contains(out, "DENIED") {
		t.Fatalf("deny: err=%v out=%q", err, out)
	}
}

func TestDispatchToolRequestClearanceEnforceFloor(t *testing.T) {
	c, _ := startTower(t)
	t.Setenv("TC_ENFORCE", "1")
	ctx := context.Background()
	// Someone holds it advisory; under enforce the model's request is forced to
	// exclusive, which turns the overlap into a hard conflict and a denial.
	if _, err := c.RequestClearance(ctx, "other", "z.go", protocol.ModeAdvisory, "", 0); err != nil {
		t.Fatal(err)
	}
	out, err := dispatchTool(ctx, c, "me", "request_clearance", json.RawMessage(`{"path":"z.go","mode":"advisory"}`))
	if err != nil || !strings.Contains(out, "DENIED") {
		t.Fatalf("enforce should override advisory and deny: err=%v out=%q", err, out)
	}
}

func TestDispatchToolHandoff(t *testing.T) {
	c, _ := startTower(t)
	ctx := context.Background()
	if _, err := c.RequestClearance(ctx, "me", "x.go", protocol.ModeExclusive, "", 0); err != nil {
		t.Fatal(err)
	}
	out, err := dispatchTool(ctx, c, "me", "handoff", json.RawMessage(`{}`))
	if err != nil || !strings.Contains(out, "Handed off 1") {
		t.Fatalf("err=%v out=%q", err, out)
	}
}

func TestDispatchToolCheckPath(t *testing.T) {
	c, _ := startTower(t)
	ctx := context.Background()
	if _, err := dispatchTool(ctx, c, "me", "check_path", json.RawMessage(`{}`)); err == nil {
		t.Fatal("check with no path should error")
	}
	out, err := dispatchTool(ctx, c, "me", "check_path", json.RawMessage(`{"path":"free.go"}`))
	if err != nil || !strings.Contains(out, "is clear") {
		t.Fatalf("clear: err=%v out=%q", err, out)
	}
	if _, err := c.RequestClearance(ctx, "other", "held.go", protocol.ModeExclusive, "", 0); err != nil {
		t.Fatal(err)
	}
	out, err = dispatchTool(ctx, c, "me", "check_path", json.RawMessage(`{"path":"held.go"}`))
	if err != nil || !strings.Contains(out, "is held by other") {
		t.Fatalf("held: err=%v out=%q", err, out)
	}
}

func TestDispatchToolUnknown(t *testing.T) {
	c := client.New("127.0.0.1:1")
	if _, err := dispatchTool(context.Background(), c, "me", "no_such_tool", nil); err == nil {
		t.Fatal("an unknown tool must error")
	}
}

func TestMcpCallWrapsErrorsAsToolText(t *testing.T) {
	c, _ := startTower(t)
	// Unknown tool flows through mcpCall and must come back as an error tool result.
	params, _ := json.Marshal(map[string]interface{}{"name": "no_such_tool"})
	res := mcpCall(c, "me", params)
	if res["isError"] != true {
		t.Fatalf("expected isError true, got %+v", res)
	}
	content := res["content"].([]map[string]interface{})
	if !strings.Contains(content[0]["text"].(string), "error") {
		t.Fatalf("content = %+v", content)
	}
}

func TestToolText(t *testing.T) {
	ok := toolText("hi", false)
	if ok["isError"] != false {
		t.Fatalf("isError = %v", ok["isError"])
	}
	content := ok["content"].([]map[string]interface{})
	if content[0]["type"] != "text" || content[0]["text"] != "hi" {
		t.Fatalf("content = %+v", content)
	}
}
