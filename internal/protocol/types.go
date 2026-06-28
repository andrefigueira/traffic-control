// Package protocol holds the wire types shared by the tower, the client, the
// CLI, the Claude hooks and the MCP server. Keeping them in one place means the
// HTTP boundary and the in-process callers speak exactly the same language.
package protocol

import (
	"path"
	"strings"
	"time"
)

// Event type constants. These are the names that travel over the pub/sub
// frequency and the SSE stream. They are part of the public contract, so the
// docs, the issues and this file must agree.
const (
	EventPresenceJoin     = "presence.join"
	EventPresenceLeave    = "presence.leave"
	EventClearanceGranted = "clearance.granted"
	EventClearanceHandoff = "clearance.handoff"
	EventClearanceExpired = "clearance.expired"
	EventConflictAlert    = "conflict.alert"
	EventBoardPosted      = "board.posted"
)

// Clearance modes. Advisory warns and still grants; exclusive is the opt-in
// hard mode that a PreToolUse hook will block on.
const (
	ModeAdvisory  = "advisory"
	ModeExclusive = "exclusive"
)

// Board entry kinds.
const (
	KindFlightPlan = "flightplan"
	KindDone       = "done"
	KindNote       = "note"
)

// Session is an agent currently in the air: a Claude Code session, a CLI user,
// or anything else that has checked in with the tower.
type Session struct {
	Callsign  string    `json:"callsign"`
	Project   string    `json:"project"`
	PID       int       `json:"pid,omitempty"`
	StartedAt time.Time `json:"started_at"`
	LastSeen  time.Time `json:"last_seen"`
}

// Clearance is a held path. It is advisory by default: it records intent and
// powers the conflict warning, and only blocks when its mode is exclusive.
type Clearance struct {
	ID        string    `json:"id"`
	Path      string    `json:"path"`
	Holder    string    `json:"holder"` // the holder's callsign
	Mode      string    `json:"mode"`
	Note      string    `json:"note,omitempty"`
	GrantedAt time.Time `json:"granted_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// BoardEntry is one line on the broadcast board: a flight plan, a done update,
// or a free note. This is the awareness layer, and the part that is useful
// even with no locking at all.
type BoardEntry struct {
	ID       string    `json:"id"`
	Callsign string    `json:"callsign"`
	Kind     string    `json:"kind"`
	Message  string    `json:"message"`
	Paths    []string  `json:"paths,omitempty"`
	PostedAt time.Time `json:"posted_at"`
}

// ClearanceResult is the answer to a clearance request.
type ClearanceResult struct {
	Granted   bool       `json:"granted"`
	Clearance *Clearance `json:"clearance,omitempty"`
	Conflict  *Clearance `json:"conflict,omitempty"` // the existing holder, when denied
	Advisory  bool       `json:"advisory"`           // true when an overlap exists but the mode let it through
	Message   string     `json:"message"`
}

// CheckResult answers "is this path spoken for".
type CheckResult struct {
	Held      bool       `json:"held"`
	Clearance *Clearance `json:"clearance,omitempty"`
}

// Event is one item on the frequency.
type Event struct {
	Type    string      `json:"type"`
	At      time.Time   `json:"at"`
	Payload interface{} `json:"payload,omitempty"`
}

// NormalizePath puts a path into a stable form for comparison: forward slashes,
// cleaned, with any leading "./" removed. A trailing slash is preserved so a
// directory claim keeps its meaning.
func NormalizePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return p
	}
	trailing := strings.HasSuffix(p, "/")
	p = strings.ReplaceAll(p, "\\", "/")
	cleaned := path.Clean(p)
	cleaned = strings.TrimPrefix(cleaned, "./")
	if trailing && !strings.HasSuffix(cleaned, "/") {
		cleaned += "/"
	}
	return cleaned
}

// PathsOverlap reports whether holding a clearance on path a should affect a
// request for path b. It is deliberately conservative: exact match, glob match
// in either direction, or one path being a directory ancestor of the other.
//
// This is intentionally a coarse, file-level notion of overlap. It does not and
// cannot model semantic coupling (a signature change in one file breaking a
// caller in another). That limitation is documented, not hidden.
func PathsOverlap(a, b string) bool {
	a = NormalizePath(a)
	b = NormalizePath(b)
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	if ok, _ := path.Match(a, b); ok {
		return true
	}
	if ok, _ := path.Match(b, a); ok {
		return true
	}
	return dirAncestor(a, b) || dirAncestor(b, a)
}

// dirAncestor reports whether dir is a directory ancestor of p.
func dirAncestor(dir, p string) bool {
	d := strings.TrimSuffix(dir, "/")
	if d == "" {
		return false
	}
	return strings.HasPrefix(p, d+"/")
}
