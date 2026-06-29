// Package client is a thin HTTP wrapper around the tower API. The CLI, the
// Claude hooks and the MCP server all talk to the tower through this one type,
// so they cannot drift apart.
package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/andrefigueira/traffic-control/internal/protocol"
)

// DefaultAddr is where the tower listens unless TC_ADDR says otherwise. 7700 is
// the squawk code an aircraft sets for a general emergency, which felt apt.
const DefaultAddr = "127.0.0.1:7700"

// Addr resolves the tower address from the environment, falling back to the
// default.
func Addr() string {
	if v := strings.TrimSpace(os.Getenv("TC_ADDR")); v != "" {
		return v
	}
	return DefaultAddr
}

// Client talks to a tower.
type Client struct {
	base string
	http *http.Client
}

// New returns a client for the given host:port.
func New(addr string) *Client {
	return &Client{
		base: "http://" + addr,
		http: &http.Client{Timeout: 5 * time.Second},
	}
}

// FromEnv returns a client pointed at Addr().
func FromEnv() *Client { return New(Addr()) }

func (c *Client) do(ctx context.Context, method, path string, body, out interface{}) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("tower unreachable at %s: %w", c.base, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("tower returned %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// Ping returns nil if the tower answers its health check.
func (c *Client) Ping(ctx context.Context) error {
	var stats map[string]interface{}
	return c.do(ctx, http.MethodGet, "/healthz", nil, &stats)
}

// Health returns the tower's stats snapshot (sessions, clearances, subscribers,
// dropped events). It is the same payload as /healthz.
func (c *Client) Health(ctx context.Context) (map[string]interface{}, error) {
	var stats map[string]interface{}
	err := c.do(ctx, http.MethodGet, "/healthz", nil, &stats)
	return stats, err
}

// Register checks an agent in.
func (c *Client) Register(ctx context.Context, callsign, project string, pid int) (*protocol.Session, error) {
	var out protocol.Session
	err := c.do(ctx, http.MethodPost, "/sessions",
		map[string]interface{}{"callsign": callsign, "project": project, "pid": pid}, &out)
	return &out, err
}

// Deregister checks an agent out.
func (c *Client) Deregister(ctx context.Context, callsign string) error {
	return c.do(ctx, http.MethodDelete, "/sessions/"+url.PathEscape(callsign), nil, nil)
}

// Heartbeat refreshes a session and its clearances.
func (c *Client) Heartbeat(ctx context.Context, callsign string) error {
	return c.do(ctx, http.MethodPost, "/sessions/"+url.PathEscape(callsign)+"/heartbeat", nil, nil)
}

// WhosFlying lists current sessions.
func (c *Client) WhosFlying(ctx context.Context) ([]protocol.Session, error) {
	var out []protocol.Session
	err := c.do(ctx, http.MethodGet, "/sessions", nil, &out)
	return out, err
}

// RequestClearance asks to hold a path.
func (c *Client) RequestClearance(ctx context.Context, callsign, path, mode, note string, ttlSeconds int) (*protocol.ClearanceResult, error) {
	var out protocol.ClearanceResult
	err := c.do(ctx, http.MethodPost, "/clearances", map[string]interface{}{
		"callsign": callsign, "path": path, "mode": mode, "note": note, "ttl_seconds": ttlSeconds,
	}, &out)
	return &out, err
}

// Handoff releases the caller's clearances overlapping path (empty releases all).
func (c *Client) Handoff(ctx context.Context, callsign, path string) (int, error) {
	var out struct {
		Released int `json:"released"`
	}
	err := c.do(ctx, http.MethodPost, "/clearances/handoff",
		map[string]interface{}{"callsign": callsign, "path": path}, &out)
	return out.Released, err
}

// Check reports whether a path is spoken for.
func (c *Client) Check(ctx context.Context, path string) (*protocol.CheckResult, error) {
	var out protocol.CheckResult
	err := c.do(ctx, http.MethodGet, "/clearances/check?path="+url.QueryEscape(path), nil, &out)
	return &out, err
}

// Clearances lists all live holds.
func (c *Client) Clearances(ctx context.Context) ([]protocol.Clearance, error) {
	var out []protocol.Clearance
	err := c.do(ctx, http.MethodGet, "/clearances", nil, &out)
	return out, err
}

// PostBoard adds an entry to the broadcast board.
func (c *Client) PostBoard(ctx context.Context, callsign, kind, message string, paths []string) (*protocol.BoardEntry, error) {
	var out protocol.BoardEntry
	err := c.do(ctx, http.MethodPost, "/board", map[string]interface{}{
		"callsign": callsign, "kind": kind, "message": message, "paths": paths,
	}, &out)
	return &out, err
}

// ReadBoard returns the most recent entries.
func (c *Client) ReadBoard(ctx context.Context, limit int) ([]protocol.BoardEntry, error) {
	var out []protocol.BoardEntry
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/board?limit=%d", limit), nil, &out)
	return out, err
}

// Events connects to the SSE stream and delivers events until ctx is cancelled
// or the connection drops. It uses its own client with no timeout.
func (c *Client) Events(ctx context.Context) (<-chan protocol.Event, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/events", nil)
	if err != nil {
		return nil, err
	}
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("tower unreachable at %s: %w", c.base, err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("event stream returned %d", resp.StatusCode)
	}
	out := make(chan protocol.Event)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var ev protocol.Event
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev); err != nil {
				continue
			}
			select {
			case out <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}
