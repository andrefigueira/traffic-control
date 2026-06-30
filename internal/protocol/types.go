// Package protocol holds the wire types shared by the tower, the client, the
// CLI, the Claude hooks and the MCP server. Keeping them in one place means the
// HTTP boundary and the in-process callers speak exactly the same language.
package protocol

import (
	"path"
	"runtime"
	"strings"
	"time"
)

// caseInsensitiveFS is true on platforms whose default filesystem folds case
// (macOS APFS/HFS+, Windows NTFS), where src/App.go and src/app.go are the same
// file. Path comparison folds case on these platforms so two spellings of one
// file are recognized as a single path. Stored paths keep their original case
// for display.
var caseInsensitiveFS = runtime.GOOS == "darwin" || runtime.GOOS == "windows"

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
	EventAdvisoryOverlap  = "clearance.advisory"
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
	Holder    string    `json:"holder"`              // the holder's callsign
	Workspace string    `json:"workspace,omitempty"` // the working tree this hold lives in
	Mode      string    `json:"mode"`
	Note      string    `json:"note,omitempty"`
	GrantedAt time.Time `json:"granted_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// BoardEntry is one line on the broadcast board: a flight plan, a done update,
// or a free note. This is the awareness layer, and the part that is useful
// even with no locking at all.
type BoardEntry struct {
	ID        string    `json:"id"`
	Callsign  string    `json:"callsign"`
	Workspace string    `json:"workspace,omitempty"` // the working tree this entry is about
	Kind      string    `json:"kind"`
	Message   string    `json:"message"`
	Paths     []string  `json:"paths,omitempty"`
	PostedAt  time.Time `json:"posted_at"`
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
	// Backslash is a path separator only on Windows. On Unix it is a legal
	// filename character, so rewriting it unconditionally would corrupt real
	// paths. Convert only where it actually means "separator".
	if runtime.GOOS == "windows" {
		p = strings.ReplaceAll(p, "\\", "/")
	}
	trailing := strings.HasSuffix(p, "/")
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
	// Compare with case folded on case-insensitive filesystems so two spellings
	// of one file (src/App.go vs src/app.go on macOS) are seen as overlapping.
	fa, fb := foldPath(a), foldPath(b)
	if fa == fb {
		return true
	}
	// Only treat a side as a glob when it actually contains * or ?, so literal
	// filenames with brackets (a Next.js route like app/[id].tsx) match
	// literally rather than being read as a character class.
	if hasGlobMeta(a) && matchGlob(fa, fb) {
		return true
	}
	if hasGlobMeta(b) && matchGlob(fb, fa) {
		return true
	}
	return dirAncestor(fa, fb) || dirAncestor(fb, fa)
}

// foldPath lowercases on case-insensitive filesystems for comparison only.
func foldPath(p string) string {
	if caseInsensitiveFS {
		return strings.ToLower(p)
	}
	return p
}

// hasGlobMeta reports whether p looks like a glob pattern (contains * or ?).
func hasGlobMeta(p string) bool {
	return strings.ContainsAny(p, "*?")
}

// matchGlob reports whether name matches the glob pattern. Unlike the standard
// path.Match, ** matches across path separators (zero or more whole segments),
// so a clearance on internal/** covers internal/api/server.go. A single * and ?
// still match only within one segment, via path.Match per segment.
func matchGlob(pattern, name string) bool {
	return matchSegments(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

func matchSegments(pat, name []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			rest := pat[1:]
			if len(rest) == 0 {
				return true // trailing ** swallows whatever remains, including nothing
			}
			// ** matches zero or more segments: try every split point.
			for i := 0; i <= len(name); i++ {
				if matchSegments(rest, name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		if ok, _ := path.Match(pat[0], name[0]); !ok {
			return false
		}
		pat, name = pat[1:], name[1:]
	}
	return len(name) == 0
}

// dirAncestor reports whether dir is a directory ancestor of p.
func dirAncestor(dir, p string) bool {
	d := strings.TrimSuffix(dir, "/")
	if d == "" {
		return false
	}
	return strings.HasPrefix(p, d+"/")
}
