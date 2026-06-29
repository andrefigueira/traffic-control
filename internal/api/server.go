// Package api exposes the tower over local HTTP, including a Server-Sent Events
// stream for the frequency. The transport is deliberately plain: localhost
// HTTP with JSON, no auth, no TLS. It is a single-user, single-machine
// coordinator, so the simplest thing that works is the right thing.
package api

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/andrefigueira/traffic-control/internal/tower"
)

//go:embed scope.html
var scopeHTML []byte

// Server wraps a tower with HTTP handlers.
type Server struct {
	tw *tower.Tower
}

// New returns a Server over the given tower.
func New(tw *tower.Tower) *Server { return &Server{tw: tw} }

// Handler builds the router.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.scope)
	mux.HandleFunc("GET /scope", s.scope)
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("POST /sessions", s.register)
	mux.HandleFunc("GET /sessions", s.whosFlying)
	mux.HandleFunc("DELETE /sessions/{callsign}", s.deregister)
	mux.HandleFunc("POST /sessions/{callsign}/heartbeat", s.heartbeat)
	mux.HandleFunc("POST /clearances", s.requestClearance)
	mux.HandleFunc("GET /clearances", s.listClearances)
	mux.HandleFunc("POST /clearances/handoff", s.handoff)
	mux.HandleFunc("GET /clearances/check", s.check)
	mux.HandleFunc("POST /board", s.postBoard)
	mux.HandleFunc("GET /board", s.readBoard)
	mux.HandleFunc("GET /events", s.events)
	return mux
}

// Serve binds addr and serves until ctx is cancelled. Binding here means a
// failure to claim the port (a tower is already running) is reported before any
// side effects, like the pidfile, are taken.
func (s *Server) Serve(ctx context.Context, addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return s.ServeListener(ctx, ln)
}

// ServeListener starts the sweeper and blocks serving HTTP on an already-bound
// listener until ctx is cancelled. Taking the listener separately lets the
// caller bind the port first and only then claim the pidfile, so a second tower
// that loses the race for the port never clobbers the live tower's pidfile.
func (s *Server) ServeListener(ctx context.Context, ln net.Listener) error {
	// ReadHeaderTimeout guards against a slow-header client. Read and Write
	// timeouts are intentionally left unset so the SSE stream is not killed.
	srv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.tw.Sweep()
			}
		}
	}()

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// --- handlers ---

func (s *Server) scope(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(scopeHTML)
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.tw.Stats())
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Callsign string `json:"callsign"`
		Project  string `json:"project"`
		PID      int    `json:"pid"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.Callsign == "" {
		writeErr(w, http.StatusBadRequest, "callsign is required")
		return
	}
	writeJSON(w, http.StatusOK, s.tw.Register(in.Callsign, in.Project, in.PID))
}

func (s *Server) whosFlying(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.tw.WhosFlying())
}

func (s *Server) deregister(w http.ResponseWriter, r *http.Request) {
	s.tw.Deregister(r.PathValue("callsign"))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) heartbeat(w http.ResponseWriter, r *http.Request) {
	ok := s.tw.Heartbeat(r.PathValue("callsign"))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": ok})
}

func (s *Server) requestClearance(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Callsign   string `json:"callsign"`
		Path       string `json:"path"`
		Mode       string `json:"mode"`
		Note       string `json:"note"`
		TTLSeconds int    `json:"ttl_seconds"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.Callsign == "" || in.Path == "" {
		writeErr(w, http.StatusBadRequest, "callsign and path are required")
		return
	}
	ttl := time.Duration(in.TTLSeconds) * time.Second
	writeJSON(w, http.StatusOK, s.tw.RequestClearance(in.Callsign, in.Path, in.Mode, in.Note, ttl))
}

func (s *Server) listClearances(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.tw.Clearances())
}

func (s *Server) handoff(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Callsign string `json:"callsign"`
		Path     string `json:"path"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.Callsign == "" {
		writeErr(w, http.StatusBadRequest, "callsign is required")
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"released": s.tw.Handoff(in.Callsign, in.Path)})
}

func (s *Server) check(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if p == "" {
		writeErr(w, http.StatusBadRequest, "path query param is required")
		return
	}
	writeJSON(w, http.StatusOK, s.tw.Check(p))
}

func (s *Server) postBoard(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Callsign string   `json:"callsign"`
		Kind     string   `json:"kind"`
		Message  string   `json:"message"`
		Paths    []string `json:"paths"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.Callsign == "" || in.Message == "" {
		writeErr(w, http.StatusBadRequest, "callsign and message are required")
		return
	}
	writeJSON(w, http.StatusOK, s.tw.PostBoard(in.Callsign, in.Kind, in.Message, in.Paths))
}

func (s *Server) readBoard(w http.ResponseWriter, r *http.Request) {
	limit := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		fmt.Sscanf(v, "%d", &limit)
	}
	writeJSON(w, http.StatusOK, s.tw.ReadBoard(limit))
}

// events streams the frequency as Server-Sent Events.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	id, ch := s.tw.Broker().Subscribe()
	defer s.tw.Broker().Unsubscribe(id)

	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case ev, open := <-ch:
			if !open {
				return
			}
			b, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, b)
			flusher.Flush()
		}
	}
}

// --- helpers ---

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func readJSON(w http.ResponseWriter, r *http.Request, v interface{}) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return false
	}
	return true
}
