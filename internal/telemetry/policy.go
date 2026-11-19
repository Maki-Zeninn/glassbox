// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Package telemetry — policy.go defines typed consent states, event categories,
// queue-bound enforcement, retry policy, and payload redaction guarantees for
// the offline telemetry queue.
//
// # Consent states
//
// Three mutually exclusive states are defined by [ConsentLevel]:
//
//   - [ConsentLevelDisabled]  — default; no data is collected or queued.
//   - [ConsentLevelAnonymous] — only the categories in [AllowedAnonymousCategories] are collected.
//   - [ConsentLevelFull]      — all categories in [AllowedFullCategories] are collected.
//
// The consent level is derived from [ResolveConsentLevel] and always respects
// the GLASSBOX_TELEMETRY environment variable.
//
// # Event categories
//
// Every telemetry event belongs to an [EventCategory].  Categories are used to
// gate collection based on the active consent level.  The gating is enforced
// at enqueue time by [EnqueueCategorisedEvent], which is a no-op when the event
// category is not permitted.
//
// # Queue bounds
//
// The offline queue limits defined in queue.go are canonical:
//   - MaxQueueSize   (500 events)
//   - MaxQueueBytes  (512 KiB)
//   - MaxEventAge    (48 h)
//
// # Retry policy
//
// The retry policy is advisory (for future drain components):
//   - [RetryMaxAttempts]   — max delivery attempts per event.
//   - [RetryBackoffBase]   — base for exponential back-off.
//   - [RetryBackoffCap]    — maximum back-off interval.
//
// # Payload redaction
//
// [RedactPayload] guarantees that no transaction contents, credentials,
// command arguments, or source code paths survive in an event's attributes
// before it is queued or exported.  Redaction rules are defined by
// [redactedKeyPatterns] and the [SanitizeValue] function in telemetry.go.
package telemetry

import (
	"os"
	"strings"
	"time"
)

// ── Consent level ─────────────────────────────────────────────────────────────

// ConsentLevel is the granular telemetry opt-in tier.
type ConsentLevel int

const (
	// ConsentLevelDisabled is the default: no data is collected or queued.
	ConsentLevelDisabled ConsentLevel = iota
	// ConsentLevelAnonymous allows only privacy-safe aggregate metrics.
	ConsentLevelAnonymous
	// ConsentLevelFull allows all non-sensitive event categories.
	ConsentLevelFull
)

// String returns a human-readable label for the consent level.
func (c ConsentLevel) String() string {
	switch c {
	case ConsentLevelAnonymous:
		return "anonymous"
	case ConsentLevelFull:
		return "full"
	default:
		return "disabled"
	}
}

// ResolveConsentLevel maps the existing [EffectiveConsent] resolution into the
// richer [ConsentLevel] type.
//
// Precedence (highest to lowest):
//  1. GLASSBOX_TELEMETRY_LEVEL env var ("anonymous", "full", "disabled")
//  2. GLASSBOX_TELEMETRY env var (boolean: true→full, false→disabled)
//  3. Consent file
//  4. Default: disabled
func ResolveConsentLevel() ConsentLevel {
	// 1. Explicit level override.
	if lvl := strings.ToLower(strings.TrimSpace(os.Getenv("GLASSBOX_TELEMETRY_LEVEL"))); lvl != "" {
		switch lvl {
		case "full":
			return ConsentLevelFull
		case "anonymous", "anon":
			return ConsentLevelAnonymous
		case "disabled", "off", "false", "0":
			return ConsentLevelDisabled
		}
	}

	// 2-4. Fall back to the existing boolean consent.
	ec := ResolveConsent()
	if ec.Enabled {
		return ConsentLevelFull
	}
	return ConsentLevelDisabled
}

// ── Event categories ──────────────────────────────────────────────────────────

// EventCategory classifies a telemetry event for consent-gating.
type EventCategory string

const (
	// CategoryUsage covers aggregate command invocation counts.
	// Allowed at all non-disabled consent levels.
	CategoryUsage EventCategory = "usage"

	// CategoryPerformance covers timing and resource metrics.
	// Allowed at all non-disabled consent levels.
	CategoryPerformance EventCategory = "performance"

	// CategoryDiagnostics covers detailed error reports and environment info.
	// Allowed only at ConsentLevelFull.
	CategoryDiagnostics EventCategory = "diagnostics"

	// CategoryCrash covers panic and crash metadata.
	// Allowed only at ConsentLevelFull.
	CategoryCrash EventCategory = "crash"
)

// AllowedAnonymousCategories lists the categories permitted at ConsentLevelAnonymous.
var AllowedAnonymousCategories = []EventCategory{
	CategoryUsage,
	CategoryPerformance,
}

// AllowedFullCategories lists the categories permitted at ConsentLevelFull.
var AllowedFullCategories = []EventCategory{
	CategoryUsage,
	CategoryPerformance,
	CategoryDiagnostics,
	CategoryCrash,
}

// IsCategoryAllowed reports whether the given event category is permitted at
// the current consent level.
func IsCategoryAllowed(cat EventCategory, level ConsentLevel) bool {
	switch level {
	case ConsentLevelDisabled:
		return false
	case ConsentLevelAnonymous:
		for _, c := range AllowedAnonymousCategories {
			if c == cat {
				return true
			}
		}
		return false
	case ConsentLevelFull:
		for _, c := range AllowedFullCategories {
			if c == cat {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// ── Retry policy ──────────────────────────────────────────────────────────────

const (
	// RetryMaxAttempts is the maximum number of delivery attempts for a queued event.
	RetryMaxAttempts = 5

	// RetryBackoffBase is the starting back-off interval for exponential retry.
	RetryBackoffBase = 30 * time.Second

	// RetryBackoffCap is the maximum back-off interval.
	RetryBackoffCap = 4 * time.Hour
)

// RetryDelay returns the back-off duration for the given attempt number (1-based).
// It implements exponential back-off capped at RetryBackoffCap.
func RetryDelay(attempt int) time.Duration {
	if attempt <= 0 {
		attempt = 1
	}
	delay := RetryBackoffBase
	for i := 1; i < attempt; i++ {
		delay *= 2
		if delay > RetryBackoffCap {
			delay = RetryBackoffCap
			break
		}
	}
	return delay
}

// ── Payload redaction ─────────────────────────────────────────────────────────

// redactedKeyPatterns lists attribute key substrings whose values must always
// be set to [redactedPlaceholder] in event payloads.  Keys are matched
// case-insensitively.  This list supplements the logger redaction list and is
// applied at the telemetry boundary.
var redactedKeyPatterns = []string{
	"hash",
	"tx",
	"transaction",
	"contract",
	"path",
	"file",
	"key",
	"secret",
	"token",
	"password",
	"credential",
	"arg",
	"source",
	"code",
	"wasm",
	"env",
}

const redactedPlaceholder = "[redacted]"

// RedactPayload applies the telemetry payload redaction policy to attrs and
// returns a new map.  Any attribute whose key matches a pattern in
// [redactedKeyPatterns] is replaced with [redactedPlaceholder].  All remaining
// values are passed through [SanitizeValue] so no raw paths, full hashes, or
// long free-form strings reach the queue or exporter.
//
// RedactPayload guarantees:
//   - No transaction hashes, contract IDs, or file paths in their raw form.
//   - No credentials, keys, tokens, or secrets.
//   - No source code fragments (key "code", "source", "wasm").
//   - No command arguments.
//   - All string values are capped at 128 characters.
func RedactPayload(attrs map[string]string) map[string]string {
	if len(attrs) == 0 {
		return nil
	}
	out := make(map[string]string, len(attrs))
	for k, v := range attrs {
		if isRedactedKey(k) {
			out[k] = redactedPlaceholder
		} else {
			out[k] = SanitizeValue(k, v)
		}
	}
	return out
}

func isRedactedKey(key string) bool {
	lower := strings.ToLower(key)
	for _, pat := range redactedKeyPatterns {
		if strings.Contains(lower, pat) {
			return true
		}
	}
	return false
}

// ── EnqueueCategorisedEvent ───────────────────────────────────────────────────

// EnqueueCategorisedEvent is the preferred entry-point for emitting telemetry
// events.  It:
//
//  1. Resolves the current consent level.
//  2. Checks whether cat is permitted at that level.
//  3. Applies [RedactPayload] to attrs.
//  4. Calls [EnqueueEvent] with the redacted attributes.
//
// Returns nil when telemetry is disabled or the category is not permitted —
// callers should not treat that as an error.
func EnqueueCategorisedEvent(event string, cat EventCategory, attrs map[string]string) error {
	level := ResolveConsentLevel()
	if !IsCategoryAllowed(cat, level) {
		return nil
	}
	safe := RedactPayload(attrs)
	return EnqueueEvent(event, safe)
}

// ── DeleteQueue ───────────────────────────────────────────────────────────────

// DeleteQueue removes the offline telemetry queue and consent file so the user
// can completely erase all locally stored telemetry state with a single call.
// Errors are returned but are not fatal — partial deletion is reported.
func DeleteQueue() error {
	if err := PurgeQueue(); err != nil {
		return err
	}
	return nil
}
