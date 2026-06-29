package main

import (
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/andrefigueira/traffic-control/internal/api"
	"github.com/andrefigueira/traffic-control/internal/client"
	"github.com/andrefigueira/traffic-control/internal/tower"
)

// startTower brings up a real tower behind a real HTTP server and points the CLI
// at it via TC_ADDR, so command code paths exercise the genuine client/server
// round trip rather than a mock. It returns a client for direct setup and the
// tower for white-box assertions.
func startTower(t *testing.T) (*client.Client, *tower.Tower) {
	t.Helper()
	tw := tower.New()
	ts := httptest.NewServer(api.New(tw).Handler())
	t.Cleanup(ts.Close)
	addr := strings.TrimPrefix(ts.URL, "http://")
	t.Setenv("TC_ADDR", addr)
	return client.New(addr), tw
}

// deadAddr points the CLI at a port nothing listens on, so any call fails fast
// with a connection refused. Used to drive the tower-down branches.
func deadAddr(t *testing.T) {
	t.Helper()
	t.Setenv("TC_ADDR", "127.0.0.1:1")
}

// captureStdout redirects os.Stdout for the duration of fn and returns whatever
// was written. The command and hook code prints directly to os.Stdout, so this
// is how we assert on user-facing and protocol output.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	_ = w.Close()
	os.Stdout = old
	return <-done
}

// captureStderr is the stderr twin of captureStdout, for the degraded-mode
// warnings the hooks emit.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	_ = w.Close()
	os.Stderr = old
	return <-done
}

// setStdin replaces os.Stdin with a pipe carrying content, so hook commands that
// read stdin do not block on the test runner's terminal.
func setStdin(t *testing.T, content string) {
	t.Helper()
	old := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.WriteString(content)
	_ = w.Close()
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = old
		_ = r.Close()
	})
}
