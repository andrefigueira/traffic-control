package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/andrefigueira/traffic-control/internal/client"
	"github.com/andrefigueira/traffic-control/internal/protocol"
	"github.com/andrefigueira/traffic-control/internal/tower"
)

func TestScopeServesDashboard(t *testing.T) {
	ts := httptest.NewServer(New(tower.New()).Handler())
	defer ts.Close()
	for _, path := range []string{"/", "/scope"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("GET %s: status %d", path, resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Fatalf("GET %s: content-type %q", path, ct)
		}
		if !strings.Contains(string(body), "THE SCOPE") {
			t.Fatalf("GET %s: dashboard HTML not served", path)
		}
	}
}

// TestAPIRoundTrip drives the real HTTP handlers through the real client, so a
// break anywhere on the wire (routing, JSON shapes, status codes) is caught.
func TestAPIRoundTrip(t *testing.T) {
	ts := httptest.NewServer(New(tower.New()).Handler())
	defer ts.Close()
	c := client.New(strings.TrimPrefix(ts.URL, "http://"))
	ctx := context.Background()

	if err := c.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
	if _, err := c.Register(ctx, "alpha", "proj", 0); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := c.Register(ctx, "bravo", "proj", 0); err != nil {
		t.Fatalf("register bravo: %v", err)
	}

	res, err := c.RequestClearance(ctx, "alpha", "x.go", protocol.ModeExclusive, "", 0)
	if err != nil || !res.Granted {
		t.Fatalf("alpha clearance: err=%v res=%+v", err, res)
	}

	denied, err := c.RequestClearance(ctx, "bravo", "x.go", protocol.ModeAdvisory, "", 0)
	if err != nil {
		t.Fatalf("bravo request: %v", err)
	}
	if denied.Granted {
		t.Fatal("bravo must be denied on alpha's exclusive path")
	}

	chk, err := c.Check(ctx, "x.go")
	if err != nil || !chk.Held || chk.Clearance.Holder != "alpha" {
		t.Fatalf("check x.go: err=%v res=%+v", err, chk)
	}

	if _, err := c.PostBoard(ctx, "alpha", protocol.KindFlightPlan, "working", []string{"x.go"}); err != nil {
		t.Fatalf("post board: %v", err)
	}
	board, err := c.ReadBoard(ctx, 10)
	if err != nil || len(board) != 1 {
		t.Fatalf("read board: err=%v len=%d", err, len(board))
	}

	n, err := c.Handoff(ctx, "alpha", "x.go")
	if err != nil || n != 1 {
		t.Fatalf("handoff: err=%v n=%d", err, n)
	}
	after, _ := c.Check(ctx, "x.go")
	if after.Held {
		t.Fatal("x.go should be clear after handoff")
	}
}
