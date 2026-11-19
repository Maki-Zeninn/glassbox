// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package eventbus

import (
	"sync"

	"github.com/dotandev/glassbox/internal/logger"
)

// HandlerID uniquely identifies a registered event listener.
// It is returned by Subscribe and must be passed to Unsubscribe.
type HandlerID uint64

// Handler is a function that receives an event payload.
type Handler func(payload any)

// EventBus is a concurrency-safe publish/subscribe event bus.
// Multiple goroutines may call Emit, Subscribe, and Unsubscribe simultaneously
// without data races or concurrent map write panics.
type EventBus struct {
	mu       sync.RWMutex
	handlers map[string]map[HandlerID]Handler
	nextID   HandlerID
}

// New returns a new, ready-to-use EventBus. It initializes the outer handler
// map only; per-topic handler maps are allocated lazily by Subscribe.
func New() *EventBus {
	return &EventBus{
		handlers: make(map[string]map[HandlerID]Handler),
	}
}

// Subscribe registers handler to be called whenever an event of the given
// topic is emitted. It returns a HandlerID that can be used to unsubscribe.
func (b *EventBus) Subscribe(topic string, handler Handler) HandlerID {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.nextID++
	id := b.nextID

	// Inner topic maps are created lazily on first Subscribe to avoid
	// allocating maps for topics that may never be used.
	if b.handlers[topic] == nil {
		b.handlers[topic] = make(map[HandlerID]Handler)
	}
	b.handlers[topic][id] = handler

	return id
}

// Unsubscribe removes the listener identified by id from the given topic.
// It is safe to call Unsubscribe from within a Handler or from a separate
// goroutine while Emit is in progress.
func (b *EventBus) Unsubscribe(topic string, id HandlerID) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if listeners, ok := b.handlers[topic]; ok {
		delete(listeners, id)
		if len(listeners) == 0 {
			delete(b.handlers, topic)
		}
	} else {
		logger.Logger.Debug("EventBus: Unsubscribe called for unknown topic", "topic", topic)
	}
}

// Emit delivers payload to all handlers currently subscribed to topic.
// It takes a snapshot of the handler list under a read lock, then releases
// the lock before invoking each handler, so Emit itself does not block
// concurrent Subscribe or Unsubscribe calls on other topics.
//
// Handlers must not call Subscribe or Unsubscribe on this EventBus; doing so
// would deadlock because Emit releases its read lock before invoking handlers,
// but Subscribe acquires a write lock. Schedule any such calls for after the
// handler returns (e.g. via a goroutine or a deferred work queue).
//
// Handlers are invoked synchronously in the calling goroutine; long-running
// handlers will delay subsequent handlers and the return of Emit.
func (b *EventBus) Emit(topic string, payload any) {
	b.mu.RLock()
	listeners := b.handlers[topic]
	// Copy handler references so we can release the lock before invoking them.
	// This prevents a deadlock if a handler calls Subscribe/Unsubscribe, and
	// also minimises lock contention for high-throughput emit paths.
	snapshot := make([]Handler, 0, len(listeners))
	for _, h := range listeners {
		snapshot = append(snapshot, h)
	}
	b.mu.RUnlock()

	for _, h := range snapshot {
		if h != nil {
			h(payload)
		}
	}
}

// Topics returns the list of topics that currently have at least one subscriber.
func (b *EventBus) Topics() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	topics := make([]string, 0, len(b.handlers))
	for t := range b.handlers {
		topics = append(topics, t)
	}
	return topics
}

// SubscriberCount returns the number of active subscribers for a topic.
func (b *EventBus) SubscriberCount(topic string) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.handlers[topic])
}
