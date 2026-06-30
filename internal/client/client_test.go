package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/andrefigueira/traffic-control/internal/protocol"
)

// recorder captures what the client actually put on the wire so the tests can
// assert the method, escaped path, query and body, not just the decoded result.
type recorder struct {
	method   string
	path     string // escaped, so %2F survives for assertions
	rawQuery string
	body     []byte
	ct       string
}

// stub spins up a server that records the incoming request and replies with a
// fixed status and body. addrOf strips the scheme so it can feed client.New.
func stub(t *testing.T, status int, respBody string, rec *recorder) *Client {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rec != nil {
			rec.method = r.Method
			rec.path = r.URL.EscapedPath()
			rec.rawQuery = r.URL.RawQuery
			rec.ct = r.Header.Get("Content-Type")
			rec.body, _ = io.ReadAll(r.Body)
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, respBody)
	}))
	t.Cleanup(ts.Close)
	return New(strings.TrimPrefix(ts.URL, "http://"))
}

func TestAddr(t *testing.T) {
	t.Run("falls back to default when unset", func(t *testing.T) {
		t.Setenv("TC_ADDR", "")
		if got := Addr(); got != DefaultAddr {
			t.Fatalf("Addr() = %q, want default %q", got, DefaultAddr)
		}
	})
	t.Run("honours TC_ADDR", func(t *testing.T) {
		t.Setenv("TC_ADDR", "10.0.0.1:9000")
		if got := Addr(); got != "10.0.0.1:9000" {
			t.Fatalf("Addr() = %q", got)
		}
	})
	t.Run("trims surrounding whitespace", func(t *testing.T) {
		t.Setenv("TC_ADDR", "  host:1  ")
		if got := Addr(); got != "host:1" {
			t.Fatalf("Addr() = %q, want trimmed", got)
		}
	})
	t.Run("blank TC_ADDR is treated as unset", func(t *testing.T) {
		t.Setenv("TC_ADDR", "   ")
		if got := Addr(); got != DefaultAddr {
			t.Fatalf("Addr() = %q, want default", got)
		}
	})
}

func TestNewAndFromEnvBaseURL(t *testing.T) {
	if c := New("example:1"); c.base != "http://example:1" {
		t.Fatalf("New base = %q", c.base)
	}
	t.Setenv("TC_ADDR", "host:2")
	if c := FromEnv(); c.base != "http://host:2" {
		t.Fatalf("FromEnv base = %q", c.base)
	}
}

func TestPingAndHealth(t *testing.T) {
	t.Run("ping ok", func(t *testing.T) {
		var rec recorder
		c := stub(t, 200, `{"sessions":2}`, &rec)
		if err := c.Ping(context.Background()); err != nil {
			t.Fatalf("Ping: %v", err)
		}
		if rec.method != http.MethodGet || rec.path != "/healthz" {
			t.Fatalf("ping hit %s %s", rec.method, rec.path)
		}
	})
	t.Run("health decodes stats", func(t *testing.T) {
		c := stub(t, 200, `{"sessions":3,"clearances":1}`, nil)
		stats, err := c.Health(context.Background())
		if err != nil {
			t.Fatalf("Health: %v", err)
		}
		if stats["sessions"].(float64) != 3 {
			t.Fatalf("stats = %+v", stats)
		}
	})
	t.Run("ping reports unreachable tower", func(t *testing.T) {
		c := New("127.0.0.1:1") // nothing listens on port 1
		err := c.Ping(context.Background())
		if err == nil || !strings.Contains(err.Error(), "tower unreachable") {
			t.Fatalf("expected unreachable error, got %v", err)
		}
	})
}

func TestRegisterSendsCorrectBody(t *testing.T) {
	var rec recorder
	c := stub(t, 200, `{"callsign":"alpha","project":"p","pid":7}`, &rec)
	s, err := c.Register(context.Background(), "alpha", "p", 7)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if s.Callsign != "alpha" || s.PID != 7 {
		t.Fatalf("decoded session = %+v", s)
	}
	if rec.method != http.MethodPost || rec.path != "/sessions" {
		t.Fatalf("hit %s %s", rec.method, rec.path)
	}
	if rec.ct != "application/json" {
		t.Fatalf("content-type = %q, want application/json", rec.ct)
	}
	var sent map[string]interface{}
	if err := json.Unmarshal(rec.body, &sent); err != nil {
		t.Fatalf("body not json: %v (%s)", err, rec.body)
	}
	if sent["callsign"] != "alpha" || sent["project"] != "p" || sent["pid"].(float64) != 7 {
		t.Fatalf("body = %+v", sent)
	}
}

func TestDeregisterEscapesCallsign(t *testing.T) {
	var rec recorder
	c := stub(t, 200, ``, &rec)
	if err := c.Deregister(context.Background(), "team/one two"); err != nil {
		t.Fatalf("Deregister: %v", err)
	}
	if rec.method != http.MethodDelete {
		t.Fatalf("method = %s", rec.method)
	}
	// The slash and space must be percent-encoded so they cannot be read as a
	// path separator or break routing.
	if rec.path != "/sessions/team%2Fone%20two" {
		t.Fatalf("escaped path = %q", rec.path)
	}
}

func TestHeartbeatPath(t *testing.T) {
	var rec recorder
	c := stub(t, 200, ``, &rec)
	if err := c.Heartbeat(context.Background(), "alpha"); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if rec.method != http.MethodPost || rec.path != "/sessions/alpha/heartbeat" {
		t.Fatalf("hit %s %s", rec.method, rec.path)
	}
}

func TestWhosFlyingDecodes(t *testing.T) {
	c := stub(t, 200, `[{"callsign":"a"},{"callsign":"b"}]`, nil)
	out, err := c.WhosFlying(context.Background())
	if err != nil {
		t.Fatalf("WhosFlying: %v", err)
	}
	if len(out) != 2 || out[1].Callsign != "b" {
		t.Fatalf("out = %+v", out)
	}
}

func TestRequestClearanceBodyAndDecode(t *testing.T) {
	var rec recorder
	c := stub(t, 200, `{"granted":true,"message":"cleared"}`, &rec)
	res, err := c.RequestClearance(context.Background(), "alpha", "", "x.go", "exclusive", "note", 30)
	if err != nil {
		t.Fatalf("RequestClearance: %v", err)
	}
	if !res.Granted || res.Message != "cleared" {
		t.Fatalf("res = %+v", res)
	}
	if rec.path != "/clearances" {
		t.Fatalf("path = %q", rec.path)
	}
	var sent map[string]interface{}
	_ = json.Unmarshal(rec.body, &sent)
	if sent["mode"] != "exclusive" || sent["ttl_seconds"].(float64) != 30 || sent["note"] != "note" {
		t.Fatalf("body = %+v", sent)
	}
}

func TestHandoffReturnsReleasedCount(t *testing.T) {
	var rec recorder
	c := stub(t, 200, `{"released":3}`, &rec)
	n, err := c.Handoff(context.Background(), "alpha", "x.go")
	if err != nil {
		t.Fatalf("Handoff: %v", err)
	}
	if n != 3 {
		t.Fatalf("released = %d", n)
	}
	if rec.path != "/clearances/handoff" {
		t.Fatalf("path = %q", rec.path)
	}
}

func TestCheckEscapesPathQuery(t *testing.T) {
	var rec recorder
	c := stub(t, 200, `{"held":true,"clearance":{"holder":"alpha"}}`, &rec)
	res, err := c.Check(context.Background(), "", "dir/a b.go")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !res.Held || res.Clearance.Holder != "alpha" {
		t.Fatalf("res = %+v", res)
	}
	// QueryEscape encodes the space as + and the slash stays literal in a query.
	if !strings.Contains(rec.rawQuery, "path=dir%2Fa+b.go") {
		t.Fatalf("query = %q", rec.rawQuery)
	}
}

func TestClearancesDecodes(t *testing.T) {
	c := stub(t, 200, `[{"id":"clr_1","path":"x.go","holder":"a"}]`, nil)
	out, err := c.Clearances(context.Background())
	if err != nil || len(out) != 1 || out[0].Path != "x.go" {
		t.Fatalf("Clearances err=%v out=%+v", err, out)
	}
}

func TestPostBoardBody(t *testing.T) {
	var rec recorder
	c := stub(t, 200, `{"id":"brd_1","kind":"flightplan","message":"hi"}`, &rec)
	e, err := c.PostBoard(context.Background(), "alpha", "", "flightplan", "hi", []string{"a.go", "b.go"})
	if err != nil {
		t.Fatalf("PostBoard: %v", err)
	}
	if e.Message != "hi" {
		t.Fatalf("entry = %+v", e)
	}
	var sent map[string]interface{}
	_ = json.Unmarshal(rec.body, &sent)
	paths := sent["paths"].([]interface{})
	if len(paths) != 2 || paths[0] != "a.go" {
		t.Fatalf("paths = %+v", paths)
	}
}

func TestReadBoardLimitInQuery(t *testing.T) {
	var rec recorder
	c := stub(t, 200, `[]`, &rec)
	if _, err := c.ReadBoard(context.Background(), 5); err != nil {
		t.Fatalf("ReadBoard: %v", err)
	}
	if rec.rawQuery != "limit=5" {
		t.Fatalf("query = %q", rec.rawQuery)
	}
}

// TestNon2xxBecomesError covers the negative branch: any status >= 300 must turn
// into an error that carries the status and the trimmed body.
func TestNon2xxBecomesError(t *testing.T) {
	c := stub(t, http.StatusConflict, "  already held  ", nil)
	_, err := c.RequestClearance(context.Background(), "a", "", "x.go", "", "", 0)
	if err == nil {
		t.Fatal("expected error on 409")
	}
	if !strings.Contains(err.Error(), "409") || !strings.Contains(err.Error(), "already held") {
		t.Fatalf("error should carry status and body, got %v", err)
	}
}

func TestContextDeadlineSurfacesAsError(t *testing.T) {
	// A server that never replies, against an already-cancelled context.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := New("127.0.0.1:1")
	if err := c.Ping(ctx); err == nil {
		t.Fatal("cancelled context should produce an error")
	}
}

func TestEventsStreamsAndSkipsNoise(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fl := w.(http.Flusher)
		w.WriteHeader(200)
		// Two valid frames, plus noise the client must ignore: a comment line, a
		// non-data line, and a data line with broken JSON.
		_, _ = io.WriteString(w, "data: {\"type\":\"presence.join\"}\n")
		_, _ = io.WriteString(w, ": keep-alive comment\n")
		_, _ = io.WriteString(w, "event: presence.join\n")
		_, _ = io.WriteString(w, "data: {not valid json}\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"board.posted\"}\n")
		fl.Flush()
	}))
	defer ts.Close()
	c := New(strings.TrimPrefix(ts.URL, "http://"))

	ch, err := c.Events(context.Background())
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	var got []string
	for ev := range ch { // channel closes when the handler returns
		got = append(got, ev.Type)
	}
	if len(got) != 2 || got[0] != "presence.join" || got[1] != "board.posted" {
		t.Fatalf("expected 2 clean events, got %v", got)
	}
}

func TestEventsNon200IsError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()
	c := New(strings.TrimPrefix(ts.URL, "http://"))
	if _, err := c.Events(context.Background()); err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("expected stream error carrying 500, got %v", err)
	}
}

func TestEventsUnreachableIsError(t *testing.T) {
	c := New("127.0.0.1:1")
	if _, err := c.Events(context.Background()); err == nil || !strings.Contains(err.Error(), "unreachable") {
		t.Fatalf("expected unreachable error, got %v", err)
	}
}

func TestEventsCancellationClosesChannel(t *testing.T) {
	// A handler that sends one frame then holds the connection open, so the only
	// way the stream ends is context cancellation.
	release := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fl := w.(http.Flusher)
		w.WriteHeader(200)
		_, _ = io.WriteString(w, "data: {\"type\":\"presence.join\"}\n")
		fl.Flush()
		<-release // block until the test lets go
	}))
	defer ts.Close()
	defer close(release)

	c := New(strings.TrimPrefix(ts.URL, "http://"))
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := c.Events(ctx)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	select {
	case ev, ok := <-ch:
		if !ok || ev.Type != "presence.join" {
			t.Fatalf("first event ok=%v ev=%+v", ok, ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("never received the first event")
	}
	cancel()
	select {
	case _, ok := <-ch:
		if ok {
			// drain any buffered event, then the close must follow
			select {
			case _, ok2 := <-ch:
				if ok2 {
					t.Fatal("channel should close after cancellation")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("channel did not close after cancellation")
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("channel did not close after cancellation")
	}
	_ = protocol.EventPresenceJoin // keep protocol import meaningful
}
