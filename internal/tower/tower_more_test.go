package tower

import (
	"testing"
	"time"

	"github.com/andrefigueira/traffic-control/internal/protocol"
)

func TestStats(t *testing.T) {
	tw := New()
	tw.Register("alpha", "p", 0)
	tw.RequestClearance("alpha", "", "x.go", protocol.ModeExclusive, "", 0)
	tw.PostBoard("alpha", "", protocol.KindNote, "hi", nil)
	id, _ := tw.Broker().Subscribe()
	defer tw.Broker().Unsubscribe(id)

	s := tw.Stats()
	if s.Sessions != 1 || s.Clearances != 1 || s.BoardSize != 1 || s.Subscribers != 1 {
		t.Fatalf("stats = %+v", s)
	}
	if s.StartedAt.IsZero() {
		t.Fatal("StartedAt should be set")
	}
}

func TestReadBoardLimits(t *testing.T) {
	tw := New()
	for i := 0; i < 4; i++ {
		tw.PostBoard("a", "", protocol.KindNote, "m", nil)
	}
	if got := len(tw.ReadBoard(0)); got != 4 {
		t.Fatalf("limit 0 should return all, got %d", got)
	}
	if got := len(tw.ReadBoard(-1)); got != 4 {
		t.Fatalf("negative limit should return all, got %d", got)
	}
	if got := len(tw.ReadBoard(100)); got != 4 {
		t.Fatalf("oversized limit should clamp to all, got %d", got)
	}
	if got := len(tw.ReadBoard(2)); got != 2 {
		t.Fatalf("limit 2 should return 2, got %d", got)
	}
}

func TestBoardCapTrimsOldest(t *testing.T) {
	tw := New()
	total := maxBoardEntries + 50
	for i := 0; i < total; i++ {
		tw.PostBoard("a", "", protocol.KindNote, "m", nil)
	}
	all := tw.ReadBoard(0)
	if len(all) != maxBoardEntries {
		t.Fatalf("board should cap at %d, got %d", maxBoardEntries, len(all))
	}
}

func TestRequestClearanceDefaults(t *testing.T) {
	tw := New()
	tw.Register("alpha", "p", 0)
	r := tw.RequestClearance("alpha", "", "x.go", "", "", 0)
	if !r.Granted {
		t.Fatal("should be granted")
	}
	if r.Clearance.Mode != protocol.ModeAdvisory {
		t.Fatalf("empty mode should default to advisory, got %q", r.Clearance.Mode)
	}
	// A zero ttl falls back to the tower lease, so the expiry sits ~DefaultLease out.
	wantMin := time.Now().Add(DefaultLease - time.Minute)
	if r.Clearance.ExpiresAt.Before(wantMin) {
		t.Fatalf("zero ttl should use the default lease, expiry = %v", r.Clearance.ExpiresAt)
	}
}

func TestDeregisterUnknownIsNoop(t *testing.T) {
	tw := New()
	tw.Register("alpha", "p", 0)
	tw.Deregister("ghost") // must not panic or remove the wrong session
	if len(tw.WhosFlying()) != 1 {
		t.Fatal("deregistering an unknown callsign should change nothing")
	}
}

func TestHeartbeatUnknownReturnsFalse(t *testing.T) {
	tw := New()
	if tw.Heartbeat("ghost") {
		t.Fatal("heartbeat on an unknown session should return false")
	}
}

func TestCheckClearPath(t *testing.T) {
	tw := New()
	if res := tw.Check("", "nothing.go"); res.Held {
		t.Fatalf("an unheld path should report Held=false, got %+v", res)
	}
}

func TestHandoffSpecificVsAll(t *testing.T) {
	tw := New()
	tw.Register("alpha", "p", 0)
	tw.RequestClearance("alpha", "", "a.go", protocol.ModeExclusive, "", 0)
	tw.RequestClearance("alpha", "", "b.go", protocol.ModeExclusive, "", 0)

	if n := tw.Handoff("alpha", "a.go"); n != 1 {
		t.Fatalf("targeted handoff should release exactly 1, got %d", n)
	}
	if n := tw.Handoff("alpha", ""); n != 1 {
		t.Fatalf("empty path should release the remaining hold, got %d", n)
	}
	if len(tw.Clearances()) != 0 {
		t.Fatal("nothing should be left held")
	}
}

func TestPostBoardDefaultsKind(t *testing.T) {
	tw := New()
	e := tw.PostBoard("alpha", "", "", "a bare note", nil)
	if e.Kind != protocol.KindNote {
		t.Fatalf("empty kind should default to note, got %q", e.Kind)
	}
}

func TestSessionIdleSweep(t *testing.T) {
	tw := New()
	tw.sessIdle = 5 * time.Millisecond // white-box: shorten the idle window
	tw.Register("alpha", "p", 0)
	tw.RequestClearance("alpha", "", "x.go", protocol.ModeExclusive, "", time.Hour)

	time.Sleep(20 * time.Millisecond)
	tw.Sweep()
	if len(tw.WhosFlying()) != 0 {
		t.Fatal("an idle session should be swept")
	}
	if len(tw.Clearances()) != 0 {
		t.Fatal("an idle session's holds should be released when it is swept")
	}
}

func TestExpiredClearancePublishesEvent(t *testing.T) {
	tw := New()
	id, ch := tw.Broker().Subscribe()
	defer tw.Broker().Unsubscribe(id)
	tw.Register("alpha", "p", 0)
	tw.RequestClearance("alpha", "", "x.go", protocol.ModeExclusive, "", time.Millisecond)
	time.Sleep(5 * time.Millisecond)
	tw.Sweep()

	deadline := time.After(time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Type == protocol.EventClearanceExpired {
				return
			}
		case <-deadline:
			t.Fatal("expected a clearance.expired event")
		}
	}
}

func TestClearancesSortedNewestFirst(t *testing.T) {
	tw := New()
	tw.Register("alpha", "p", 0)
	tw.RequestClearance("alpha", "", "old.go", protocol.ModeExclusive, "", time.Hour)
	time.Sleep(2 * time.Millisecond)
	tw.RequestClearance("alpha", "", "new.go", protocol.ModeExclusive, "", time.Hour)

	clrs := tw.Clearances()
	if len(clrs) != 2 || clrs[0].Path != "new.go" {
		t.Fatalf("clearances should be newest-first, got %+v", clrs)
	}
}

func TestExclusiveRequestOverAdvisoryHoldDenied(t *testing.T) {
	tw := New()
	tw.Register("alpha", "p", 0)
	tw.Register("bravo", "p", 0)
	tw.RequestClearance("alpha", "", "x.go", protocol.ModeAdvisory, "", 0)
	// An exclusive *request* must hard-conflict even against an advisory hold.
	r := tw.RequestClearance("bravo", "", "x.go", protocol.ModeExclusive, "", 0)
	if r.Granted {
		t.Fatalf("an exclusive request over any existing hold must be denied, got %+v", r)
	}
}

func TestWorkspacesAreIsolated(t *testing.T) {
	tw := New()
	tw.Register("alpha", "p", 0)
	tw.Register("bravo", "p", 0)
	// Same relative path, two different working trees (e.g. separate worktrees).
	if r := tw.RequestClearance("alpha", "/work/treeA", "internal/x.go", protocol.ModeExclusive, "", 0); !r.Granted {
		t.Fatalf("alpha should be granted in tree A: %+v", r)
	}
	// Bravo edits the same relative path in a DIFFERENT tree: must be granted, the
	// files are physically distinct.
	r := tw.RequestClearance("bravo", "/work/treeB", "internal/x.go", protocol.ModeExclusive, "", 0)
	if !r.Granted {
		t.Fatalf("a hold in another worktree must not conflict, got %+v", r)
	}
	// Same tree, same path: that DOES conflict.
	conflict := tw.RequestClearance("bravo", "/work/treeA", "internal/x.go", protocol.ModeExclusive, "", 0)
	if conflict.Granted {
		t.Fatalf("same path in the same tree must still conflict, got %+v", conflict)
	}
}

func TestCheckIsWorkspaceScoped(t *testing.T) {
	tw := New()
	tw.Register("alpha", "p", 0)
	tw.RequestClearance("alpha", "/work/treeA", "x.go", protocol.ModeExclusive, "", 0)
	if res := tw.Check("/work/treeB", "x.go"); res.Held {
		t.Fatal("a check in another worktree must not see the hold")
	}
	if res := tw.Check("/work/treeA", "x.go"); !res.Held {
		t.Fatal("a check in the holding worktree should see it")
	}
}

func TestFlightPlanOverlapIsWorkspaceScoped(t *testing.T) {
	tw := New()
	tw.Register("alpha", "p", 0)
	tw.Register("bravo", "p", 0)
	tw.PostBoard("alpha", "/work/treeA", protocol.KindFlightPlan, "reworking auth", []string{"auth/"})
	// Bravo reaches for a path under that plan but in a different tree: no warning.
	r := tw.RequestClearance("bravo", "/work/treeB", "auth/login.go", protocol.ModeAdvisory, "", 0)
	if r.Advisory {
		t.Fatalf("a flight plan in another worktree should not warn, got %+v", r)
	}
	// Same tree: it warns.
	r = tw.RequestClearance("bravo", "/work/treeA", "auth/login.go", protocol.ModeAdvisory, "", 0)
	if !r.Advisory {
		t.Fatalf("a flight plan in the same tree should warn, got %+v", r)
	}
}
