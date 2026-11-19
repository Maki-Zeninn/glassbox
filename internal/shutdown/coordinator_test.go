// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package shutdown

import (
	"context"
	"testing"
)

func TestCoordinatorRun_LIFOAndOnce(t *testing.T) {
	c := NewCoordinator()
	order := make([]string, 0, 3)

	c.Register("first", func(ctx context.Context) error {
		_ = ctx
		order = append(order, "first")
		return nil
	})
	c.Register("second", func(ctx context.Context) error {
		_ = ctx
		order = append(order, "second")
		return nil
	})
	c.Register("third", func(ctx context.Context) error {
		_ = ctx
		order = append(order, "third")
		return nil
	})

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("unexpected run error: %v", err)
	}

	want := []string{"third", "second", "first"}
	if len(order) != len(want) {
		t.Fatalf("unexpected hook count: got %d want %d", len(order), want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("unexpected order at %d: got %s want %s", i, order[i], want[i])
		}
	}

	// Second run should be a no-op.
	order = order[:0]
	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("unexpected second run error: %v", err)
	}
	if len(order) != 0 {
		t.Fatalf("expected no hooks on second run, got %d", len(order))
	}
}

func TestCoordinator_HookPanic_DoesNotPropagate(t *testing.T) {
	c := NewCoordinator()

	// Register a hook that panics
	c.Register("panicking-hook", func(ctx context.Context) error {
		panic("test panic")
	})

	// Register a second hook that should still run after the panic
	ran := false
	c.Register("second-hook", func(ctx context.Context) error {
		ran = true
		return nil
	})

	// The test should not panic
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("panic propagated: %v", r)
		}
	}()

	if err := c.Run(context.Background()); err != nil {
		// We expect an error from the panicked hook
		_ = err
	}

	// The second hook should still have run
	if !ran {
		t.Errorf("second hook did not run; panic recovery may have stopped execution")
	}
}
