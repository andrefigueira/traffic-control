package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/andrefigueira/traffic-control/internal/client"
	"github.com/andrefigueira/traffic-control/internal/protocol"
)

// cmdMCP runs a minimal MCP server over stdio so Claude can call the
// coordination tools directly. Framing is newline-delimited JSON-RPC 2.0, which
// is the MCP stdio transport. stdout carries only protocol messages; everything
// else goes to stderr.
// mcpHeartbeatInterval is how often the live MCP session refreshes its holds.
// It is comfortably inside the tower lease so a path is never swept mid-task,
// and the session-idle window so a quiet session is not dropped.
const mcpHeartbeatInterval = 45 * time.Second

func cmdMCP(_ []string) error {
	callsign := mcpCallsign()
	c := client.FromEnv()

	// Keep this session and any clearances it holds alive for as long as the MCP
	// server is running, independent of tool-call cadence. A long-reasoning turn
	// can sit for many minutes between tool calls; without this its holds could
	// expire at the lease boundary while the agent is still working. The MCP
	// process lives for the whole session, so it is the right place to do this.
	// The heartbeat is best-effort and silent: a down tower simply no-ops, and
	// nothing is ever written to stdout, so the JSON-RPC framing is untouched.
	hbCtx, stopHeartbeat := context.WithCancel(context.Background())
	defer stopHeartbeat()
	go mcpHeartbeatLoop(hbCtx, c, callsign, mcpHeartbeatInterval)

	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()

	for in.Scan() {
		line := in.Bytes()
		if len(line) == 0 {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}
		if isNotification(req) {
			// JSON-RPC 2.0: a request without an id is a notification and must
			// never receive a response.
			continue
		}
		resp := handleRPC(c, callsign, req)
		b, _ := json.Marshal(resp)
		out.Write(b)
		out.WriteByte('\n')
		out.Flush()
	}
	return nil
}

// mcpHeartbeatLoop refreshes the session on a ticker until ctx is cancelled.
// Each beat is bounded by a short timeout so a wedged tower cannot stall it.
func mcpHeartbeatLoop(ctx context.Context, c *client.Client, callsign string, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			beatCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			_ = c.Heartbeat(beatCtx, callsign)
			cancel()
		}
	}
}

func mcpCallsign() string {
	if v := os.Getenv("TC_CALLSIGN"); v != "" {
		return v
	}
	return resolveCallsign("")
}

// mcpCwd is the project root the MCP server runs in. Claude launches the server
// in the project directory, so this anchors paths the same way the hooks do,
// which keeps an MCP-acquired hold and a hook-acquired hold on the same file
// comparable instead of one absolute and one relative.
func mcpCwd() string {
	if d, err := os.Getwd(); err == nil {
		return d
	}
	return ""
}

// mcpPath canonicalizes a path argument the same way the hooks do.
func mcpPath(p string) string {
	if p == "" {
		return ""
	}
	return relativize(p, mcpCwd())
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// isNotification reports whether a JSON-RPC message is a notification, i.e. it
// carries no id. The MCP stdio transport sends notifications/initialized and
// others this way, and the spec forbids replying to them.
func isNotification(req rpcRequest) bool {
	return len(req.ID) == 0
}

func handleRPC(c *client.Client, callsign string, req rpcRequest) rpcResponse {
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		// register the MCP identity so it shows on the board
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, _ = c.Register(ctx, callsign, "", os.Getpid())
		cancel()
		resp.Result = map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
			"serverInfo":      map[string]interface{}{"name": "traffic-control", "version": version},
		}
	case "ping":
		resp.Result = map[string]interface{}{}
	case "tools/list":
		resp.Result = map[string]interface{}{"tools": mcpTools()}
	case "tools/call":
		resp.Result = mcpCall(c, callsign, req.Params)
	default:
		resp.Error = &rpcError{Code: -32601, Message: "method not found: " + req.Method}
	}
	return resp
}

func mcpTools() []map[string]interface{} {
	strSchema := func(props map[string]interface{}, required ...string) map[string]interface{} {
		s := map[string]interface{}{"type": "object", "properties": props}
		if len(required) > 0 {
			s["required"] = required
		}
		return s
	}
	str := map[string]interface{}{"type": "string"}
	return []map[string]interface{}{
		{
			"name":        "whos_flying",
			"description": "List the agents currently checked in to this working tree.",
			"inputSchema": strSchema(map[string]interface{}{}),
		},
		{
			"name":        "read_board",
			"description": "Read the broadcast board: recent flight plans and done updates from all agents.",
			"inputSchema": strSchema(map[string]interface{}{"limit": map[string]interface{}{"type": "integer"}}),
		},
		{
			"name":        "file_flight_plan",
			"description": "Announce what you are about to work on so other agents can steer clear. Post before a large or multi-file change.",
			"inputSchema": strSchema(map[string]interface{}{
				"message": str,
				"paths":   map[string]interface{}{"type": "array", "items": str},
			}, "message"),
		},
		{
			"name":        "request_clearance",
			"description": "Request to hold a path. mode 'advisory' (default) records intent and warns others; mode 'exclusive' asks the tower to block others from editing it.",
			"inputSchema": strSchema(map[string]interface{}{
				"path": str,
				"mode": str,
				"note": str,
			}, "path"),
		},
		{
			"name":        "handoff",
			"description": "Release a path you were holding (or omit path to release all of your holds).",
			"inputSchema": strSchema(map[string]interface{}{"path": str}),
		},
		{
			"name":        "check_path",
			"description": "Check whether a path is already held by another agent before you start editing it.",
			"inputSchema": strSchema(map[string]interface{}{"path": str}, "path"),
		},
	}
}

func mcpCall(c *client.Client, callsign string, params json.RawMessage) map[string]interface{} {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	_ = json.Unmarshal(params, &p)

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	text, err := dispatchTool(ctx, c, callsign, p.Name, p.Arguments)
	if err != nil {
		return toolText("Traffic Control error: "+err.Error(), true)
	}
	return toolText(text, false)
}

func dispatchTool(ctx context.Context, c *client.Client, callsign, name string, args json.RawMessage) (string, error) {
	switch name {
	case "whos_flying":
		sessions, err := c.WhosFlying(ctx)
		if err != nil {
			return "", err
		}
		if len(sessions) == 0 {
			return "Nobody is flying right now.", nil
		}
		b, _ := json.MarshalIndent(sessions, "", "  ")
		return string(b), nil

	case "read_board":
		var a struct {
			Limit int `json:"limit"`
		}
		_ = json.Unmarshal(args, &a)
		if a.Limit == 0 {
			a.Limit = 20
		}
		entries, err := c.ReadBoard(ctx, a.Limit)
		if err != nil {
			return "", err
		}
		if len(entries) == 0 {
			return "The board is empty.", nil
		}
		b, _ := json.MarshalIndent(entries, "", "  ")
		return string(b), nil

	case "file_flight_plan":
		var a struct {
			Message string   `json:"message"`
			Paths   []string `json:"paths"`
		}
		_ = json.Unmarshal(args, &a)
		if a.Message == "" {
			return "", fmt.Errorf("message is required")
		}
		paths := make([]string, 0, len(a.Paths))
		for _, p := range a.Paths {
			paths = append(paths, mcpPath(p))
		}
		e, err := c.PostBoard(ctx, callsign, protocol.KindFlightPlan, a.Message, paths)
		if err != nil {
			return "", err
		}
		return "Flight plan posted: " + e.Message, nil

	case "request_clearance":
		var a struct {
			Path string `json:"path"`
			Mode string `json:"mode"`
			Note string `json:"note"`
		}
		_ = json.Unmarshal(args, &a)
		if a.Path == "" {
			return "", fmt.Errorf("path is required")
		}
		mode := a.Mode
		// Operator policy floor: when TC_ENFORCE=1 the model cannot place a hold
		// weaker than exclusive, so it cannot opt out of hard coordination.
		if os.Getenv("TC_ENFORCE") == "1" {
			mode = protocol.ModeExclusive
		}
		res, err := c.RequestClearance(ctx, callsign, mcpPath(a.Path), mode, a.Note, 0)
		if err != nil {
			return "", err
		}
		if !res.Granted {
			return "DENIED: " + res.Message, nil
		}
		return "CLEARED: " + res.Message, nil

	case "handoff":
		var a struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal(args, &a)
		n, err := c.Handoff(ctx, callsign, mcpPath(a.Path))
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Handed off %d clearance(s).", n), nil

	case "check_path":
		var a struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal(args, &a)
		if a.Path == "" {
			return "", fmt.Errorf("path is required")
		}
		p := mcpPath(a.Path)
		res, err := c.Check(ctx, p)
		if err != nil {
			return "", err
		}
		if !res.Held {
			return a.Path + " is clear.", nil
		}
		return fmt.Sprintf("%s is held by %s (%s).", a.Path, res.Clearance.Holder, res.Clearance.Mode), nil

	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

func toolText(text string, isErr bool) map[string]interface{} {
	return map[string]interface{}{
		"content": []map[string]interface{}{{"type": "text", "text": text}},
		"isError": isErr,
	}
}
