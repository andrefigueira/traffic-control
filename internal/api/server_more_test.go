package api

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/andrefigueira/traffic-control/internal/tower"
)

func newTestServer(t *testing.T) (*httptest.Server, *tower.Tower) {
	t.Helper()
	tw := tower.New()
	ts := httptest.NewServer(New(tw).Handler())
	t.Cleanup(ts.Close)
	return ts, tw
}

// post is a small helper for hitting JSON endpoints with a raw body so the
// validation branches can be exercised directly.
func post(t *testing.T, url, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

func TestRegisterValidation(t *testing.T) {
	ts, _ := newTestServer(t)
	t.Run("empty callsign is rejected", func(t *testing.T) {
		resp := post(t, ts.URL+"/sessions", `{"callsign":""}`)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
	})
	t.Run("invalid json is rejected", func(t *testing.T) {
		resp := post(t, ts.URL+"/sessions", `{not json`)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
		var body map[string]string
		_ = json.NewDecoder(resp.Body).Decode(&body)
		if !strings.Contains(body["error"], "invalid json") {
			t.Fatalf("error = %q", body["error"])
		}
	})
}

func TestRequestClearanceValidation(t *testing.T) {
	ts, _ := newTestServer(t)
	for _, body := range []string{`{"callsign":"a"}`, `{"path":"x.go"}`, `{}`} {
		resp := post(t, ts.URL+"/clearances", body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("body %s: status = %d, want 400", body, resp.StatusCode)
		}
		resp.Body.Close()
	}
}

func TestHandoffValidation(t *testing.T) {
	ts, _ := newTestServer(t)
	resp := post(t, ts.URL+"/clearances/handoff", `{"path":"x.go"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a missing callsign", resp.StatusCode)
	}
}

func TestCheckValidation(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, err := http.Get(ts.URL + "/clearances/check")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a missing path", resp.StatusCode)
	}
}

func TestPostBoardValidation(t *testing.T) {
	ts, _ := newTestServer(t)
	for _, body := range []string{`{"callsign":"a"}`, `{"message":"hi"}`} {
		resp := post(t, ts.URL+"/board", body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("body %s: status = %d, want 400", body, resp.StatusCode)
		}
		resp.Body.Close()
	}
}

func TestHeartbeatUnknownCallsign(t *testing.T) {
	ts, _ := newTestServer(t)
	resp := post(t, ts.URL+"/sessions/ghost/heartbeat", ``)
	defer resp.Body.Close()
	var body map[string]bool
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body["ok"] {
		t.Fatal("heartbeat on an unknown session should report ok:false")
	}
}

func TestDeregisterAlwaysOK(t *testing.T) {
	ts, _ := newTestServer(t)
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/sessions/whoever", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]bool
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if !body["ok"] {
		t.Fatalf("deregister should report ok:true, got %+v", body)
	}
}

func TestReadBoardRespectsLimit(t *testing.T) {
	ts, tw := newTestServer(t)
	for i := 0; i < 5; i++ {
		tw.PostBoard("a", "note", "msg", nil)
	}
	resp, err := http.Get(ts.URL + "/board?limit=2")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var entries []map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&entries)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries with limit=2, got %d", len(entries))
	}
}

func TestEmptyCollectionsAreArraysNotNull(t *testing.T) {
	ts, _ := newTestServer(t)
	for _, path := range []string{"/sessions", "/clearances"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		var arr []interface{}
		if err := json.NewDecoder(resp.Body).Decode(&arr); err != nil {
			t.Fatalf("GET %s: not a json array: %v", path, err)
		}
		resp.Body.Close()
		if arr == nil {
			t.Fatalf("GET %s should return [] not null", path)
		}
	}
}

// flushPipe is an http.ResponseWriter that streams writes into an io.Pipe and
// satisfies http.Flusher, so the events handler can be driven directly with a
// context the test owns. Driving it over a real socket would mean teardown waits
// on the handler's own disconnect detection (its 20s keepalive), which is slow
// and beside the point being tested.
type flushPipe struct {
	header http.Header
	w      *io.PipeWriter
}

func (f *flushPipe) Header() http.Header         { return f.header }
func (f *flushPipe) WriteHeader(int)             {}
func (f *flushPipe) Write(b []byte) (int, error) { return f.w.Write(b) }
func (f *flushPipe) Flush()                      {}

// TestEventsStreamDeliversEvent drives the real events handler and confirms a
// tower event reaches the wire framed as Server-Sent Events.
func TestEventsStreamDeliversEvent(t *testing.T) {
	tw := tower.New()
	s := New(tw)

	pr, pw := io.Pipe()
	rw := &flushPipe{header: http.Header{}, w: pw}
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/events", nil).WithContext(ctx)
	go func() {
		s.events(rw, req)
		_ = pw.Close()
	}()

	// Wait until the handler has actually subscribed before publishing, so the
	// event cannot be emitted into the gap before the subscription exists.
	deadline := time.Now().Add(2 * time.Second)
	for tw.Broker().Count() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("handler never subscribed to the broker")
		}
		time.Sleep(time.Millisecond)
	}
	tw.Register("alpha", "p", 0)

	found := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(pr)
		for sc.Scan() {
			if strings.Contains(sc.Text(), "presence.join") {
				found <- sc.Text()
				return
			}
		}
		found <- ""
	}()

	select {
	case line := <-found:
		if line == "" {
			t.Fatal("never saw the presence.join frame")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for an SSE frame")
	}
	if ct := rw.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type = %q", ct)
	}

	// Cancelling our own context ends the handler at once; closing the read end
	// unblocks it even if it is mid-write.
	cancel()
	_ = pr.Close()
}

// nonFlusher is a ResponseWriter that deliberately does NOT implement
// http.Flusher (httptest.ResponseRecorder does, so it cannot be used here).
type nonFlusher struct {
	h    http.Header
	code int
}

func (n *nonFlusher) Header() http.Header         { return n.h }
func (n *nonFlusher) Write(b []byte) (int, error) { return len(b), nil }
func (n *nonFlusher) WriteHeader(c int)           { n.code = c }

// TestEventsRejectsNonFlushingWriter covers the negative branch: a writer that
// cannot stream gets a 500 rather than a half-open connection.
func TestEventsRejectsNonFlushingWriter(t *testing.T) {
	s := New(tower.New())
	nf := &nonFlusher{h: http.Header{}}
	s.events(nf, httptest.NewRequest(http.MethodGet, "/events", nil))
	if nf.code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", nf.code)
	}
}

func TestServeBindErrorOnBusyPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	s := New(tower.New())
	if err := s.Serve(context.Background(), ln.Addr().String()); err == nil {
		t.Fatal("expected a bind error on an occupied port")
	}
}

func TestServeListenerShutsDownOnContextCancel(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := New(tower.New())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.ServeListener(ctx, ln) }()

	// Let it come up, then cancel and confirm a clean return.
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("clean shutdown should return nil, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ServeListener did not return after context cancel")
	}
}
