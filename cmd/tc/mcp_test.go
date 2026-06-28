package main

import (
	"encoding/json"
	"testing"

	"github.com/andrefigueira/traffic-control/internal/client"
)

func TestIsNotification(t *testing.T) {
	if !isNotification(rpcRequest{Method: "notifications/initialized"}) {
		t.Fatal("a message with no id must be treated as a notification")
	}
	if isNotification(rpcRequest{ID: json.RawMessage("1"), Method: "initialize"}) {
		t.Fatal("a message with an id is a request, not a notification")
	}
}

func TestHandleRPCToolsList(t *testing.T) {
	c := client.New("127.0.0.1:1") // not contacted for tools/list
	resp := handleRPC(c, "tester", rpcRequest{ID: json.RawMessage("2"), Method: "tools/list"})
	if resp.Error != nil {
		t.Fatalf("tools/list should not error: %+v", resp.Error)
	}
	res := resp.Result.(map[string]interface{})
	tools := res["tools"].([]map[string]interface{})
	if len(tools) != 6 {
		t.Fatalf("expected 6 tools, got %d", len(tools))
	}
}

func TestHandleRPCUnknownMethod(t *testing.T) {
	c := client.New("127.0.0.1:1")
	resp := handleRPC(c, "tester", rpcRequest{ID: json.RawMessage("3"), Method: "bogus"})
	if resp.Error == nil || resp.Error.Code != -32601 {
		t.Fatalf("unknown method should return -32601, got %+v", resp.Error)
	}
}
