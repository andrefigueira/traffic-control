// Package tower is the heart of Traffic Control: the in-memory state of who is
// in the air, what paths they hold, and the broadcast board. It is transport
// agnostic. The HTTP API, the CLI and the Claude hooks all drive this same
// type, so the behaviour is identical however you reach it.
package tower

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/andrefigueira/traffic-control/internal/protocol"
)

// Defaults. The lease is a crash backstop, not a task-protection guarantee. The
// Claude Stop hook hands off an agent's clearances at the end of every turn, so
// in normal operation a hold lives for one turn and is re-requested on the next
// edit. The lease exists to clean up after an agent that dies or wedges without
// ever firing Stop: its holds expire instead of lingering forever. A heartbeat
// (PostToolUse) refreshes the holds of an agent that is still actively working,
// so its other held paths do not expire at the lease boundary mid-task. It is
// deliberately generous because an autonomous agent may sit between tool calls
// for many minutes while it reasons.
const (
	DefaultLease       = 30 * time.Minute
	DefaultSessionIdle = 2 * time.Hour
	maxBoardEntries    = 500
)

// Tower holds all live coordination state.
type Tower struct {
	mu          sync.Mutex
	sessions    map[string]*protocol.Session
	clearances  map[string]*protocol.Clearance
	board       []protocol.BoardEntry
	broker      *Broker
	lease       time.Duration
	sessIdle    time.Duration
	seq         uint64
	startedAt   time.Time
	persistPath string // board snapshot file, empty when persistence is off
}

// New returns a tower with default timings.
func New() *Tower {
	return &Tower{
		sessions:   make(map[string]*protocol.Session),
		clearances: make(map[string]*protocol.Clearance),
		broker:     NewBroker(),
		lease:      DefaultLease,
		sessIdle:   DefaultSessionIdle,
		startedAt:  time.Now(),
	}
}

// Broker exposes the pub/sub frequency for the SSE handler.
func (t *Tower) Broker() *Broker { return t.broker }

func (t *Tower) id(prefix string) string {
	n := atomic.AddUint64(&t.seq, 1)
	return fmt.Sprintf("%s_%d_%d", prefix, time.Now().UnixNano(), n)
}

// publish is called with the lock held; it copies the event out and releases
// nothing, so callers must not block. The broker itself is non-blocking.
func (t *Tower) publish(typ string, payload interface{}) {
	t.broker.Publish(protocol.Event{Type: typ, At: time.Now(), Payload: payload})
}

// Register checks an agent in, or refreshes an existing session with the same
// callsign. New callsigns emit presence.join.
func (t *Tower) Register(callsign, project string, pid int) *protocol.Session {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	s, existed := t.sessions[callsign]
	if !existed {
		s = &protocol.Session{Callsign: callsign, Project: project, PID: pid, StartedAt: now}
		t.sessions[callsign] = s
	}
	if project != "" {
		s.Project = project
	}
	if pid != 0 {
		s.PID = pid
	}
	s.LastSeen = now
	out := *s
	if !existed {
		t.publish(protocol.EventPresenceJoin, out)
	}
	return &out
}

// Heartbeat refreshes a session and extends the lease on every clearance it
// holds, so an actively working agent never has a path yanked out from under
// it.
func (t *Tower) Heartbeat(callsign string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	s, ok := t.sessions[callsign]
	if !ok {
		return false
	}
	now := time.Now()
	s.LastSeen = now
	for _, c := range t.clearances {
		if c.Holder == callsign {
			c.ExpiresAt = now.Add(t.lease)
		}
	}
	return true
}

// Deregister removes a session and hands off everything it held.
func (t *Tower) Deregister(callsign string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.sessions[callsign]; !ok {
		return
	}
	t.releaseLocked(callsign, "")
	delete(t.sessions, callsign)
	t.publish(protocol.EventPresenceLeave, map[string]string{"callsign": callsign})
}

// RequestClearance is the core admission decision. It expires stale state, then
// looks for an overlapping clearance held by another callsign. An exclusive
// conflict is denied; an advisory overlap is granted but flagged so the caller
// can warn. A request that only overlaps the caller's own clearances is always
// granted (and refreshes them).
func (t *Tower) RequestClearance(callsign, reqPath, mode, note string, ttl time.Duration) protocol.ClearanceResult {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.expireLocked()

	if mode == "" {
		mode = protocol.ModeAdvisory
	}
	if ttl <= 0 {
		ttl = t.lease
	}
	reqPath = protocol.NormalizePath(reqPath)

	// Scan all overlapping holds by other agents. Pick deterministically (the
	// earliest granted) so the holder named in the message is stable rather
	// than whatever Go's randomized map iteration lands on first.
	var hardConflict *protocol.Clearance
	var advisoryOverlap *protocol.Clearance
	for _, c := range t.clearances {
		if c.Holder == callsign {
			continue
		}
		if !protocol.PathsOverlap(c.Path, reqPath) {
			continue
		}
		// An exclusive clearance held by someone else, or an exclusive request
		// against any existing hold, is a hard conflict.
		if c.Mode == protocol.ModeExclusive || mode == protocol.ModeExclusive {
			if hardConflict == nil || c.GrantedAt.Before(hardConflict.GrantedAt) {
				cc := *c
				hardConflict = &cc
			}
			continue
		}
		if advisoryOverlap == nil || c.GrantedAt.Before(advisoryOverlap.GrantedAt) {
			cc := *c
			advisoryOverlap = &cc
		}
	}
	if hardConflict != nil {
		t.publish(protocol.EventConflictAlert, map[string]interface{}{
			"requester": callsign, "path": reqPath, "held_by": hardConflict.Holder, "held_path": hardConflict.Path,
		})
		return protocol.ClearanceResult{
			Granted:  false,
			Conflict: hardConflict,
			Message:  fmt.Sprintf("%s is holding %s (%s). Hold for handoff or coordinate on the board.", hardConflict.Holder, hardConflict.Path, hardConflict.Mode),
		}
	}

	// Grant (or refresh an existing identical hold by this caller).
	now := time.Now()
	var granted *protocol.Clearance
	for _, c := range t.clearances {
		if c.Holder == callsign && protocol.NormalizePath(c.Path) == reqPath {
			c.ExpiresAt = now.Add(ttl)
			c.Mode = mode
			if note != "" {
				c.Note = note
			}
			granted = c
			break
		}
	}
	if granted == nil {
		granted = &protocol.Clearance{
			ID:        t.id("clr"),
			Path:      reqPath,
			Holder:    callsign,
			Mode:      mode,
			Note:      note,
			GrantedAt: now,
			ExpiresAt: now.Add(ttl),
		}
		t.clearances[granted.ID] = granted
	}
	out := *granted
	t.publish(protocol.EventClearanceGranted, out)

	res := protocol.ClearanceResult{Granted: true, Clearance: &out, Message: "cleared"}
	if advisoryOverlap != nil {
		res.Advisory = true
		res.Conflict = advisoryOverlap
		res.Message = fmt.Sprintf("cleared, but %s also holds %s (advisory). Worth a look at the board.", advisoryOverlap.Holder, advisoryOverlap.Path)
		t.publish(protocol.EventAdvisoryOverlap, map[string]interface{}{
			"requester": callsign, "path": reqPath, "held_by": advisoryOverlap.Holder, "held_path": advisoryOverlap.Path,
		})
	} else if fp := t.flightPlanOverlapLocked(callsign, reqPath); fp != nil {
		// No live clearance overlaps, but another agent that is still flying
		// filed a flight plan over this path. Flight plans live on the board,
		// which survives a Stop handoff, so this is the awareness signal that
		// outlasts a turn-scoped clearance.
		res.Advisory = true
		res.Message = fmt.Sprintf("cleared, but %s filed a flight plan over %s. Check the board before you change their ground.", fp.Callsign, strings.Join(fp.Paths, ", "))
		t.publish(protocol.EventAdvisoryOverlap, map[string]interface{}{
			"requester": callsign, "path": reqPath, "held_by": fp.Callsign, "flightplan": fp.Message,
		})
	}
	return res
}

// flightPlanOverlapLocked returns the most recent flight plan filed by another
// agent that is still flying and whose declared paths overlap reqPath. Plans
// from agents who have left are ignored, so a stale plan does not warn forever.
// Called with t.mu held.
func (t *Tower) flightPlanOverlapLocked(callsign, reqPath string) *protocol.BoardEntry {
	for i := len(t.board) - 1; i >= 0; i-- { // newest first
		e := t.board[i]
		if e.Kind != protocol.KindFlightPlan || e.Callsign == callsign {
			continue
		}
		if _, live := t.sessions[e.Callsign]; !live {
			continue
		}
		for _, p := range e.Paths {
			if protocol.PathsOverlap(p, reqPath) {
				ec := e
				return &ec
			}
		}
	}
	return nil
}

// Handoff releases the caller's clearances overlapping the given path. An empty
// path releases everything the caller holds.
func (t *Tower) Handoff(callsign, releasePath string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.releaseLocked(callsign, releasePath)
}

func (t *Tower) releaseLocked(callsign, releasePath string) int {
	releasePath = protocol.NormalizePath(releasePath)
	released := 0
	for id, c := range t.clearances {
		if c.Holder != callsign {
			continue
		}
		if releasePath != "" && !protocol.PathsOverlap(c.Path, releasePath) {
			continue
		}
		out := *c
		delete(t.clearances, id)
		t.publish(protocol.EventClearanceHandoff, out)
		released++
	}
	return released
}

// Check reports whether any live clearance overlaps the path.
func (t *Tower) Check(p string) protocol.CheckResult {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.expireLocked()
	p = protocol.NormalizePath(p)
	for _, c := range t.clearances {
		if protocol.PathsOverlap(c.Path, p) {
			out := *c
			return protocol.CheckResult{Held: true, Clearance: &out}
		}
	}
	return protocol.CheckResult{Held: false}
}

// WhosFlying returns the current sessions, most recently seen first.
func (t *Tower) WhosFlying() []protocol.Session {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.expireLocked()
	out := make([]protocol.Session, 0, len(t.sessions))
	for _, s := range t.sessions {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastSeen.After(out[j].LastSeen) })
	return out
}

// Clearances returns all live clearances.
func (t *Tower) Clearances() []protocol.Clearance {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.expireLocked()
	out := make([]protocol.Clearance, 0, len(t.clearances))
	for _, c := range t.clearances {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].GrantedAt.After(out[j].GrantedAt) })
	return out
}

// PostBoard appends an entry to the broadcast board.
func (t *Tower) PostBoard(callsign, kind, message string, paths []string) protocol.BoardEntry {
	t.mu.Lock()
	defer t.mu.Unlock()
	if kind == "" {
		kind = protocol.KindNote
	}
	e := protocol.BoardEntry{
		ID:       t.id("brd"),
		Callsign: callsign,
		Kind:     kind,
		Message:  message,
		Paths:    paths,
		PostedAt: time.Now(),
	}
	t.board = append(t.board, e)
	if len(t.board) > maxBoardEntries {
		t.board = t.board[len(t.board)-maxBoardEntries:]
	}
	t.persistLocked()
	t.publish(protocol.EventBoardPosted, e)
	return e
}

// EnablePersistence points the tower at a board snapshot file, loading any
// entries already there so flight plans and notes survive a restart. Only the
// board is persisted on purpose: clearances are turn-scoped and re-requested,
// and sessions re-register on the next hook, so resurrecting either after a
// crash would do more harm than good. It is best-effort, so a missing or corrupt
// file simply starts empty.
func (t *Tower) EnablePersistence(path string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.persistPath = path
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var board []protocol.BoardEntry
	if json.Unmarshal(b, &board) != nil {
		return // corrupt snapshot: start empty rather than refuse to run
	}
	if len(board) > maxBoardEntries {
		board = board[len(board)-maxBoardEntries:]
	}
	t.board = board
}

// persistLocked writes the board to disk if persistence is on. Called with
// t.mu held. The write is atomic (temp file then rename) and best-effort: a
// disk error never breaks coordination, it only loses durability for that beat.
func (t *Tower) persistLocked() {
	if t.persistPath == "" {
		return
	}
	b, err := json.Marshal(t.board)
	if err != nil {
		return
	}
	tmp := t.persistPath + ".tmp"
	if os.WriteFile(tmp, b, 0o644) != nil {
		return
	}
	_ = os.Rename(tmp, t.persistPath)
}

// ReadBoard returns the most recent entries, newest last, capped at limit.
func (t *Tower) ReadBoard(limit int) []protocol.BoardEntry {
	t.mu.Lock()
	defer t.mu.Unlock()
	if limit <= 0 || limit > len(t.board) {
		limit = len(t.board)
	}
	out := make([]protocol.BoardEntry, limit)
	copy(out, t.board[len(t.board)-limit:])
	return out
}

// Sweep expires stale clearances and idle sessions. The API server calls this
// on a ticker so a crashed agent's holds do not linger.
func (t *Tower) Sweep() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.expireLocked()
}

func (t *Tower) expireLocked() {
	now := time.Now()
	for id, c := range t.clearances {
		if now.After(c.ExpiresAt) {
			out := *c
			delete(t.clearances, id)
			t.publish(protocol.EventClearanceExpired, out)
		}
	}
	for cs, s := range t.sessions {
		if now.Sub(s.LastSeen) > t.sessIdle {
			t.releaseLocked(cs, "")
			delete(t.sessions, cs)
			t.publish(protocol.EventPresenceLeave, map[string]string{"callsign": cs, "reason": "idle"})
		}
	}
}

// Stats is a small snapshot for the health endpoint.
type Stats struct {
	Sessions      int       `json:"sessions"`
	Clearances    int       `json:"clearances"`
	BoardSize     int       `json:"board_size"`
	Subscribers   int       `json:"subscribers"`
	DroppedEvents uint64    `json:"dropped_events"`
	StartedAt     time.Time `json:"started_at"`
}

// Stats returns current counts.
func (t *Tower) Stats() Stats {
	t.mu.Lock()
	defer t.mu.Unlock()
	return Stats{
		Sessions:      len(t.sessions),
		Clearances:    len(t.clearances),
		BoardSize:     len(t.board),
		Subscribers:   t.broker.Count(),
		DroppedEvents: t.broker.Dropped(),
		StartedAt:     t.startedAt,
	}
}
