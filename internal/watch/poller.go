// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package watch

import (
	"context"
	"fmt"
	"time"
)

type PollerConfig struct {
	// MaxAttempts is the maximum number of poll attempts before giving up.
	// Zero uses the default of 60.
	MaxAttempts int
	// InitialInterval is the wait duration before the second poll attempt.
	// Zero uses the default of 1 second.
	InitialInterval time.Duration
	// MaxInterval is the upper bound on the exponential back-off delay between
	// attempts.  Zero uses the default of 10 seconds.
	MaxInterval time.Duration
	// TimeoutDuration is the wall-clock deadline for the entire polling
	// operation; the context passed to Poll is cancelled after this duration.
	// Zero uses the default of 30 seconds.
	TimeoutDuration time.Duration
}

type Poller struct {
	config PollerConfig
}

type PollResult struct {
	Found bool
	Data  interface{}
	Error error
}

func NewPoller(config PollerConfig) *Poller {
	if config.MaxAttempts == 0 {
		config.MaxAttempts = 60
	}
	if config.InitialInterval == 0 {
		config.InitialInterval = 1 * time.Second
	}
	if config.MaxInterval == 0 {
		config.MaxInterval = 10 * time.Second
	}
	if config.TimeoutDuration == 0 {
		config.TimeoutDuration = 30 * time.Second
	}

	return &Poller{config: config}
}

func (p *Poller) Poll(ctx context.Context, checkFunc func(ctx context.Context) (interface{}, error), onAttempt func(attempt int)) (*PollResult, error) {
	ctx, cancel := context.WithTimeout(ctx, p.config.TimeoutDuration)
	defer cancel()

	interval := p.config.InitialInterval
	attempt := 0

	for {
		select {
		case <-ctx.Done():
			return &PollResult{Found: false, Error: ctx.Err()}, nil
		default:
		}

		attempt++

		if onAttempt != nil {
			onAttempt(attempt)
		}

		data, err := checkFunc(ctx)
		if err == nil && data != nil {
			return &PollResult{Found: true, Data: data}, nil
		}

		if attempt >= p.config.MaxAttempts {
			return &PollResult{Found: false, Error: fmt.Errorf("polling stopped after %d attempts", attempt)}, nil
		}

		select {
		case <-time.After(interval):
			interval = p.exponentialBackoff(interval)
		case <-ctx.Done():
			return &PollResult{Found: false, Error: ctx.Err()}, nil
		}
	}
}

func (p *Poller) exponentialBackoff(current time.Duration) time.Duration {
	next := current * 2
	if next > p.config.MaxInterval {
		next = p.config.MaxInterval
	}
	return next
}
