package tower

import (
	"testing"
	"time"

	"github.com/andrefigueira/traffic-control/internal/protocol"
)

func TestBrokerSubscribeCountUnsubscribe(t *testing.T) {
	b := NewBroker()
	if b.Count() != 0 {
		t.Fatalf("fresh broker should have 0 subscribers, got %d", b.Count())
	}
	id1, _ := b.Subscribe()
	id2, _ := b.Subscribe()
	if b.Count() != 2 {
		t.Fatalf("expected 2 subscribers, got %d", b.Count())
	}
	if id1 == id2 {
		t.Fatal("subscriber ids must be distinct")
	}
	b.Unsubscribe(id1)
	if b.Count() != 1 {
		t.Fatalf("expected 1 subscriber after unsubscribe, got %d", b.Count())
	}
}

func TestBrokerPublishDelivers(t *testing.T) {
	b := NewBroker()
	_, ch := b.Subscribe()
	b.Publish(protocol.Event{Type: protocol.EventBoardPosted})
	select {
	case ev := <-ch:
		if ev.Type != protocol.EventBoardPosted {
			t.Fatalf("got %q", ev.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("event was not delivered")
	}
}

func TestBrokerUnsubscribeClosesChannel(t *testing.T) {
	b := NewBroker()
	id, ch := b.Subscribe()
	b.Unsubscribe(id)
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("channel should be closed after unsubscribe")
		}
	case <-time.After(time.Second):
		t.Fatal("reading a closed channel should not block")
	}
	// Unsubscribing an unknown id must be a safe no-op.
	b.Unsubscribe(9999)
}

func TestBrokerDropsWhenSubscriberBufferFull(t *testing.T) {
	b := NewBroker()
	b.Subscribe() // never drained
	// The buffer is 64; publishing well past that forces drops rather than blocking.
	for i := 0; i < 200; i++ {
		b.Publish(protocol.Event{Type: protocol.EventBoardPosted})
	}
	if b.Dropped() == 0 {
		t.Fatal("a full subscriber buffer should increment the dropped counter")
	}
}

func TestBrokerPublishWithNoSubscribers(t *testing.T) {
	b := NewBroker()
	b.Publish(protocol.Event{Type: protocol.EventBoardPosted}) // must not panic
	if b.Dropped() != 0 {
		t.Fatalf("no subscribers means no drops, got %d", b.Dropped())
	}
}
