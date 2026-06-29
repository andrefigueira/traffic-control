package tower

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/andrefigueira/traffic-control/internal/protocol"
)

func TestRegisterAndWhosFlying(t *testing.T) {
	tw := New()
	tw.Register("alpha", "proj", 1)
	tw.Register("bravo", "proj", 2)
	tw.Register("alpha", "proj", 1) // idempotent refresh
	if got := len(tw.WhosFlying()); got != 2 {
		t.Fatalf("expected 2 sessions, got %d", got)
	}
}

func TestAdvisoryOverlapStillGrants(t *testing.T) {
	tw := New()
	tw.Register("alpha", "p", 0)
	tw.Register("bravo", "p", 0)

	first := tw.RequestClearance("alpha", "internal/api/server.go", protocol.ModeAdvisory, "", 0)
	if !first.Granted {
		t.Fatalf("first request should be granted")
	}
	// Bravo asks for the same path, advisory. It is granted but flagged.
	second := tw.RequestClearance("bravo", "internal/api/server.go", protocol.ModeAdvisory, "", 0)
	if !second.Granted {
		t.Fatalf("advisory overlap should still grant")
	}
	if !second.Advisory || second.Conflict == nil || second.Conflict.Holder != "alpha" {
		t.Fatalf("expected advisory flag pointing at alpha, got %+v", second)
	}
}

func TestExclusiveConflictDenied(t *testing.T) {
	tw := New()
	tw.Register("alpha", "p", 0)
	tw.Register("bravo", "p", 0)

	if r := tw.RequestClearance("alpha", "cmd/tc/main.go", protocol.ModeExclusive, "refactor", 0); !r.Granted {
		t.Fatalf("alpha exclusive should be granted")
	}
	r := tw.RequestClearance("bravo", "cmd/tc/main.go", protocol.ModeAdvisory, "", 0)
	if r.Granted {
		t.Fatalf("request against an exclusive hold must be denied")
	}
	if r.Conflict == nil || r.Conflict.Holder != "alpha" {
		t.Fatalf("denial should name the holder, got %+v", r)
	}
}

func TestHoldingOwnPathRefreshes(t *testing.T) {
	tw := New()
	tw.Register("alpha", "p", 0)
	r1 := tw.RequestClearance("alpha", "x.go", protocol.ModeExclusive, "", time.Minute)
	r2 := tw.RequestClearance("alpha", "x.go", protocol.ModeExclusive, "", time.Hour)
	if !r1.Granted || !r2.Granted {
		t.Fatalf("re-requesting your own path should always grant")
	}
	if len(tw.Clearances()) != 1 {
		t.Fatalf("re-request should refresh, not duplicate; got %d", len(tw.Clearances()))
	}
	if !r2.Clearance.ExpiresAt.After(r1.Clearance.ExpiresAt) {
		t.Fatalf("second request should extend the lease")
	}
}

func TestHandoffReleases(t *testing.T) {
	tw := New()
	tw.Register("alpha", "p", 0)
	tw.Register("bravo", "p", 0)
	tw.RequestClearance("alpha", "shared.go", protocol.ModeExclusive, "", 0)

	if n := tw.Handoff("alpha", "shared.go"); n != 1 {
		t.Fatalf("expected to release 1 clearance, released %d", n)
	}
	// Now bravo can take it.
	if r := tw.RequestClearance("bravo", "shared.go", protocol.ModeExclusive, "", 0); !r.Granted {
		t.Fatalf("after handoff bravo should be cleared, got %+v", r)
	}
}

func TestDirectoryClearanceCoversChildren(t *testing.T) {
	tw := New()
	tw.Register("alpha", "p", 0)
	tw.Register("bravo", "p", 0)
	tw.RequestClearance("alpha", "internal/", protocol.ModeExclusive, "sweeping", 0)

	r := tw.RequestClearance("bravo", "internal/api/server.go", protocol.ModeAdvisory, "", 0)
	if r.Granted {
		t.Fatalf("a child path should conflict with an exclusive directory clearance")
	}
}

func TestExpiry(t *testing.T) {
	tw := New()
	tw.Register("alpha", "p", 0)
	tw.RequestClearance("alpha", "temp.go", protocol.ModeExclusive, "", time.Millisecond)
	time.Sleep(5 * time.Millisecond)
	tw.Sweep()
	if got := len(tw.Clearances()); got != 0 {
		t.Fatalf("expired clearance should be swept, got %d", got)
	}
}

func TestBoardPostAndRead(t *testing.T) {
	tw := New()
	tw.PostBoard("alpha", protocol.KindFlightPlan, "refactoring auth", []string{"auth.go"})
	tw.PostBoard("bravo", protocol.KindDone, "finished migrations", nil)
	entries := tw.ReadBoard(10)
	if len(entries) != 2 {
		t.Fatalf("expected 2 board entries, got %d", len(entries))
	}
	if entries[len(entries)-1].Callsign != "bravo" {
		t.Fatalf("board should return newest last")
	}
}

func TestDeregisterReleasesHolds(t *testing.T) {
	tw := New()
	tw.Register("alpha", "p", 0)
	tw.RequestClearance("alpha", "a.go", protocol.ModeExclusive, "", 0)
	tw.Deregister("alpha")
	if len(tw.Clearances()) != 0 {
		t.Fatalf("deregister should release holds, %d left", len(tw.Clearances()))
	}
	if len(tw.WhosFlying()) != 0 {
		t.Fatalf("deregister should remove the session")
	}
}

func TestHeartbeatExtendsLease(t *testing.T) {
	tw := New()
	tw.Register("alpha", "p", 0)
	r := tw.RequestClearance("alpha", "a.go", protocol.ModeExclusive, "", time.Minute)
	before := r.Clearance.ExpiresAt
	time.Sleep(2 * time.Millisecond)
	if !tw.Heartbeat("alpha") {
		t.Fatal("heartbeat should find the session")
	}
	after := tw.Clearances()[0].ExpiresAt
	if !after.After(before) {
		t.Fatalf("heartbeat should extend the lease: before %v after %v", before, after)
	}
}

func TestFlightPlanWarnsAdvisory(t *testing.T) {
	tw := New()
	tw.Register("alpha", "p", 0)
	tw.Register("bravo", "p", 0)
	// Alpha files a flight plan over a directory but holds no clearance.
	tw.PostBoard("alpha", protocol.KindFlightPlan, "reworking auth", []string{"auth/"})

	// Bravo reaches for a file under that plan: cleared, but warned.
	r := tw.RequestClearance("bravo", "auth/login.go", protocol.ModeAdvisory, "", 0)
	if !r.Granted {
		t.Fatalf("a flight plan must never block, only warn; got %+v", r)
	}
	if !r.Advisory {
		t.Fatalf("a flight plan over the path should flag the clearance advisory; got %+v", r)
	}
	if !strings.Contains(r.Message, "flight plan") {
		t.Fatalf("advisory message should mention the flight plan; got %q", r.Message)
	}
}

func TestFlightPlanFromDepartedAgentIsIgnored(t *testing.T) {
	tw := New()
	tw.Register("alpha", "p", 0)
	tw.Register("bravo", "p", 0)
	tw.PostBoard("alpha", protocol.KindFlightPlan, "reworking auth", []string{"auth/"})
	tw.Deregister("alpha") // alpha leaves; its plan must stop warning

	r := tw.RequestClearance("bravo", "auth/login.go", protocol.ModeAdvisory, "", 0)
	if r.Advisory {
		t.Fatalf("a plan from a departed agent should not warn; got %+v", r)
	}
}

func TestAdvisoryOverlapPublishesEvent(t *testing.T) {
	tw := New()
	id, ch := tw.Broker().Subscribe()
	defer tw.Broker().Unsubscribe(id)
	tw.Register("alpha", "p", 0)
	tw.Register("bravo", "p", 0)
	tw.RequestClearance("alpha", "shared.go", protocol.ModeAdvisory, "", 0)
	tw.RequestClearance("bravo", "shared.go", protocol.ModeAdvisory, "", 0)

	deadline := time.After(time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Type == protocol.EventAdvisoryOverlap {
				return // found it
			}
		case <-deadline:
			t.Fatal("expected a clearance.advisory event for the advisory overlap")
		}
	}
}

// TestConflictHolderDeterministic guards the fix for nondeterministic holder
// reporting: the earliest granted holder must always be named.
func TestConflictHolderDeterministic(t *testing.T) {
	for i := 0; i < 10; i++ {
		tw := New()
		tw.Register("alpha", "p", 0)
		tw.Register("bravo", "p", 0)
		tw.Register("charlie", "p", 0)
		tw.RequestClearance("alpha", "a.go", protocol.ModeAdvisory, "", 0)
		time.Sleep(time.Millisecond)
		tw.RequestClearance("bravo", "a.go", protocol.ModeAdvisory, "", 0)
		res := tw.RequestClearance("charlie", "a.go", protocol.ModeExclusive, "", 0)
		if res.Granted {
			t.Fatalf("run %d: exclusive over advisory holds should be denied", i)
		}
		if res.Conflict.Holder != "alpha" {
			t.Fatalf("run %d: expected earliest holder alpha, got %s", i, res.Conflict.Holder)
		}
	}
}

// TestConcurrentRequests exercises the tower under the race detector.
func TestConcurrentRequests(t *testing.T) {
	tw := New()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			cs := fmt.Sprintf("agent-%d", n)
			tw.Register(cs, "p", 0)
			tw.RequestClearance(cs, fmt.Sprintf("file-%d.go", n%5), protocol.ModeAdvisory, "", 0)
			tw.PostBoard(cs, protocol.KindNote, "hi", nil)
			tw.WhosFlying()
			tw.Check(fmt.Sprintf("file-%d.go", n%5))
			tw.Handoff(cs, "")
		}(i)
	}
	wg.Wait()
}

func TestEventsPublished(t *testing.T) {
	tw := New()
	id, ch := tw.Broker().Subscribe()
	defer tw.Broker().Unsubscribe(id)

	tw.Register("alpha", "p", 0)
	select {
	case ev := <-ch:
		if ev.Type != protocol.EventPresenceJoin {
			t.Fatalf("expected presence.join, got %s", ev.Type)
		}
	case <-time.After(time.Second):
		t.Fatalf("no event received")
	}
}
