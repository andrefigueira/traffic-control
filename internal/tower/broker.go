package tower

import (
	"sync"

	"github.com/andrefigueira/traffic-control/internal/protocol"
)

// Broker is a tiny in-process pub/sub frequency. Subscribers each get a
// buffered channel; a slow subscriber drops events rather than stalling the
// tower. For a local, single-machine deployment this is all the fan-out we
// need, and it keeps the dependency list at zero.
type Broker struct {
	mu   sync.Mutex
	subs map[int]chan protocol.Event
	next int
}

// NewBroker returns a ready broker.
func NewBroker() *Broker {
	return &Broker{subs: make(map[int]chan protocol.Event)}
}

// Subscribe registers a new listener and returns its id and channel. Call
// Unsubscribe with the id when finished.
func (b *Broker) Subscribe() (int, <-chan protocol.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := b.next
	b.next++
	ch := make(chan protocol.Event, 64)
	b.subs[id] = ch
	return id, ch
}

// Unsubscribe removes a listener and closes its channel.
func (b *Broker) Unsubscribe(id int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if ch, ok := b.subs[id]; ok {
		delete(b.subs, id)
		close(ch)
	}
}

// Publish delivers an event to every current subscriber. Delivery is
// best-effort: if a subscriber's buffer is full the event is dropped for that
// subscriber so one stuck listener cannot block the rest.
func (b *Broker) Publish(ev protocol.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ch := range b.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

// Count returns the number of active subscribers (used in health output).
func (b *Broker) Count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}
