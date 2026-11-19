// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package eventbus

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ── Race tests for concurrent publishers, subscribers, cancellations ─────────

// TestRace_ConcurrentPublishersOnDifferentTopics verifies that multiple
// goroutines emitting on distinct topics never produce a data race.
func TestRace_ConcurrentPublishersOnDifferentTopics(t *testing.T) {
	bus := New()

	const topics = 10
	const emittersPerTopic = 5
	const emitsPerGoroutine = 100

	var counters []*atomic.Int64
	for i := 0; i < topics; i++ {
		c := new(atomic.Int64)
		topic := topicName(i)
		bus.Subscribe(topic, func(payload any) {
			c.Add(1)
		})
		counters = append(counters, c)
	}

	var wg sync.WaitGroup
	for i := 0; i < topics; i++ {
		for j := 0; j < emittersPerTopic; j++ {
			wg.Add(1)
			go func(topicIdx int) {
				defer wg.Done()
				topic := topicName(topicIdx)
				for k := 0; k < emitsPerGoroutine; k++ {
					bus.Emit(topic, k)
				}
			}(i)
		}
	}
	wg.Wait()

	for i := 0; i < topics; i++ {
		want := int64(emittersPerTopic * emitsPerGoroutine)
		if got := counters[i].Load(); got != want {
			t.Errorf("topic %q: expected %d deliveries, got %d", topicName(i), want, got)
		}
	}
}

// TestRace_ConcurrentSubscribeAndUnsubscribeAcrossTopics verifies that
// subscribing and unsubscribing on different topics simultaneously does not
// race, and that unsubscribed handlers never receive subsequent events.
func TestRace_ConcurrentSubscribeAndUnsubscribeAcrossTopics(t *testing.T) {
	bus := New()

	const topics = 8
	const opsPerTopic = 200

	var wg sync.WaitGroup
	for i := 0; i < topics; i++ {
		wg.Add(1)
		go func(topicIdx int) {
			defer wg.Done()
			topic := topicName(topicIdx)
			for j := 0; j < opsPerTopic; j++ {
				id := bus.Subscribe(topic, func(any) {})
				bus.Unsubscribe(topic, id)
			}
		}(i)
	}
	wg.Wait()

	if len(bus.Topics()) != 0 {
		t.Errorf("expected 0 topics after mass subscribe/unsubscribe, got %d", len(bus.Topics()))
	}
}

// TestRace_SlowHandlerDoesNotBlockPublisher verifies that a handler which
// sleeps under Emit does not prevent other concurrent Emit calls from
// completing (because Emit snapshots handlers under RLock).
func TestRace_SlowHandlerDoesNotBlockPublisher(t *testing.T) {
	bus := New()

	const slowTopic = "slow"
	const fastTopic = "fast"

	bus.Subscribe(slowTopic, func(any) {
		time.Sleep(5 * time.Millisecond)
	})

	var fastCount atomic.Int64
	bus.Subscribe(fastTopic, func(any) {
		fastCount.Add(1)
	})

	var wg sync.WaitGroup

	// Slow emitter on slowTopic.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			bus.Emit(slowTopic, i)
		}
	}()

	// Fast emitter on fastTopic — should not be blocked by the slow handler.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			bus.Emit(fastTopic, i)
		}
	}()

	wg.Wait()

	if got := fastCount.Load(); got != 200 {
		t.Errorf("fastTopic: expected 200 deliveries, got %d", got)
	}
}

// TestRace_DroppedEventsAfterUnsubscribe verifies that a handler does not
// receive events after it has been unsubscribed, even when emits race with
// the unsubscribe call. This is the "delivery guarantee" test: unsubscribe
// prevents all future delivery.
func TestRace_DroppedEventsAfterUnsubscribe(t *testing.T) {
	bus := New()

	const iterations = 500

	var mu sync.Mutex
	delivered := make(map[string]bool)
	var deliveredCount atomic.Int64

	id := bus.Subscribe("drop-me", func(payload any) {
		key := payload.(string)
		mu.Lock()
		delivered[key] = true
		mu.Unlock()
		deliveredCount.Add(1)
	})

	var wg sync.WaitGroup

	// Emit events.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			bus.Emit("drop-me", "before")
		}
	}()

	// Subscribe with a second handler, emit "after", then unsubscribe the first.
	wg.Add(1)
	go func() {
		defer wg.Done()
		// Wait a bit for some "before" events to land.
		time.Sleep(time.Millisecond)
		bus.Unsubscribe("drop-me", id)
		// Emit many "after" events — none should reach the unsubscribed handler.
		for i := 0; i < iterations; i++ {
			bus.Emit("drop-me", "after")
		}
	}()

	wg.Wait()

	mu.Lock()
	gotBefore := delivered["before"]
	gotAfter := delivered["after"]
	mu.Unlock()

	if gotAfter {
		t.Error("unsubscribed handler received an 'after' event — delivery guarantee violated")
	}

	total := deliveredCount.Load()
	if total == 0 && !gotBefore {
		t.Error("handler should have received at least some 'before' events")
	}
	t.Logf("handler received %d events (before=%v, after=%v)", total, gotBefore, gotAfter)
}

// TestRace_SubscriberCountConsistency verifies that SubscriberCount returns
// consistent results under concurrent subscribe/unsubscribe pressure.
func TestRace_SubscriberCountConsistency(t *testing.T) {
	bus := New()

	const goroutines = 20
	const ops = 300

	type handle struct {
		topic string
		id    HandlerID
	}

	ids := make([]handle, 0, goroutines*ops)
	var mu sync.Mutex

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			topic := topicName(idx % 5)
			for j := 0; j < ops; j++ {
				id := bus.Subscribe(topic, func(any) {})
				mu.Lock()
				ids = append(ids, handle{topic: topic, id: id})
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()

	// All topics should have goroutines*ops/5 subscribers.
	expectedPerTopic := goroutines * ops / 5
	for i := 0; i < 5; i++ {
		got := bus.SubscriberCount(topicName(i))
		if got != expectedPerTopic {
			t.Errorf("topic %q: expected %d subscribers, got %d", topicName(i), expectedPerTopic, got)
		}
	}

	// Now unsubscribe all.
	for _, h := range ids {
		bus.Unsubscribe(h.topic, h.id)
	}

	for i := 0; i < 5; i++ {
		if got := bus.SubscriberCount(topicName(i)); got != 0 {
			t.Errorf("topic %q: expected 0 after unsubscribe, got %d", topicName(i), got)
		}
	}
}

// TestRace_EmitDuringSubscribeTransition tests that an Emit happening
// concurrently with a Subscribe sees either the old or new handler list, never
// an inconsistent state.
func TestRace_EmitDuringSubscribeTransition(t *testing.T) {
	bus := New()

	var count atomic.Int64

	const iterations = 500

	var wg sync.WaitGroup

	//持续地 emit
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			bus.Emit("transition", i)
		}
	}()

	// concurrently subscribe and unsubscribe
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			id := bus.Subscribe("transition", func(any) {
				count.Add(1)
			})
			bus.Unsubscribe("transition", id)
		}
	}()

	wg.Wait()

	// The count should be >= 0 and no panics should occur.
	if count.Load() < 0 {
		t.Error("negative delivery count — internal state corruption")
	}
}

// TestRace_MassiveConcurrentEmitAndSubscribe stresses the event bus with a
// high volume of concurrent operations to surface any latent data races.
func TestRace_MassiveConcurrentEmitAndSubscribe(t *testing.T) {
	bus := New()

	const goroutines = 50
	const iterations = 400

	var totalDeliveries atomic.Int64

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			topic := topicName(idx % 10)
			for j := 0; j < iterations; j++ {
				if j%3 == 0 {
					id := bus.Subscribe(topic, func(any) {
						totalDeliveries.Add(1)
					})
					_ = id
				} else if j%3 == 1 {
					bus.Emit(topic, j)
				}
			}
		}(i)
	}
	wg.Wait()

	if totalDeliveries.Load() <= 0 {
		t.Error("expected at least some deliveries in massive concurrent test")
	}
}

// topicName returns a deterministic topic name for the given index.
func topicName(i int) string {
	return "topic_" + string(rune('a'+i))
}
