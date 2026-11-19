// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package telemetry

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// Config holds OpenTelemetry configuration
type Config struct {
	Enabled     bool
	ExporterURL string
	ServiceName string
	Anonymized  bool
}

// EnvMetadata holds environment and feature metadata for telemetry events.
// Sensitive values are excluded to protect user privacy.
type EnvMetadata struct {
	// CLI version (e.g., "1.2.3")
	Version string
	// Operating system (e.g., "linux", "darwin", "windows")
	Platform string
	// Architecture (e.g., "amd64", "arm64")
	Arch string
	// Enabled feature flags (non-sensitive only)
	FeatureFlags []string
	// Whether telemetry is anonymized
	Anonymized bool
}

var (
	commandTelemetryEnabled    bool
	commandTelemetryAnonymized bool
	envMetadata                EnvMetadata
)

// silentSpanExporter wraps a SpanExporter and swallows all export errors so
// collector outages never block or log. Core SDK paths must not depend on telemetry.
type silentSpanExporter struct {
	delegate sdktrace.SpanExporter
}

func (s *silentSpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	_ = s.delegate.ExportSpans(ctx, spans)
	return nil
}

func (s *silentSpanExporter) Shutdown(ctx context.Context) error {
	_ = s.delegate.Shutdown(ctx)
	return nil
}

// Init initializes OpenTelemetry with the given configuration.
// Graceful degradation: if the metrics collector is unreachable or init fails,
// a no-op provider is used instead so the application never blocks or errors.
// Export failures are swallowed; telemetry fails silently.
func Init(ctx context.Context, config Config) (func(), error) {
	commandTelemetryEnabled = config.Enabled
	commandTelemetryAnonymized = config.Anonymized

	// Initialize environment metadata
	envMetadata = EnvMetadata{
		Version:      getVersion(),
		Platform:     runtime.GOOS,
		Arch:         runtime.GOARCH,
		FeatureFlags: getFeatureFlags(),
		Anonymized:   config.Anonymized,
	}

	if !config.Enabled {
		return func() {}, nil
	}

	// Create OTLP HTTP exporter (best-effort; short timeout to avoid blocking)
	exporter, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpoint(config.ExporterURL),
		otlptracehttp.WithInsecure(),
		otlptracehttp.WithTimeout(5*time.Second),
	)
	if err != nil {
		// Collector unreachable at init: use no-op so core paths are unaffected
		otel.SetTracerProvider(sdktrace.NewTracerProvider())
		return func() {}, nil
	}

	// Create resource
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String(config.ServiceName),
			semconv.ServiceVersionKey.String(envMetadata.Version),
		),
	)
	if err != nil {
		_ = exporter.Shutdown(ctx)
		otel.SetTracerProvider(sdktrace.NewTracerProvider())
		return func() {}, nil
	}

	// Wrap exporter so export failures never surface or log
	silent := &silentSpanExporter{delegate: exporter}

	// Create trace provider with silent exporter so collector downtime doesn't block or log
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(silent),
		sdktrace.WithResource(res),
	)

	otel.SetTracerProvider(tp)

	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tp.Shutdown(ctx)
	}, nil
}

// getVersion returns the CLI version from environment or a default.
// In production, this would be set during build via ldflags.
func getVersion() string {
	if v := os.Getenv("GLASSBOX_VERSION"); v != "" {
		return v
	}
	return "dev"
}

// getFeatureFlags returns a list of enabled feature flags.
// Only non-sensitive, user-facing flags are included.
// Always returns a non-nil slice (may be empty).
func getFeatureFlags() []string {
	flags := make([]string, 0)

	// Check for optional features that are enabled
	// These are intentionally limited to avoid exposing sensitive data
	if os.Getenv("GLASSBOX_TELEMETRY") == "true" {
		flags = append(flags, "telemetry")
	}
	if os.Getenv("GLASSBOX_CRASH_REPORTING") == "true" {
		flags = append(flags, "crash_reporting")
	}
	if os.Getenv("GLASSBOX_FAILOVER_STRATEGY") != "" {
		flags = append(flags, "failover")
	}
	if os.Getenv("GLASSBOX_SOROBAN_RPC_URLS") != "" {
		flags = append(flags, "multi_rpc")
	}

	return flags
}

// IsTelemetryEnabled returns the current effective telemetry enabled state using
// the consent resolution hierarchy:
//
//  1. GLASSBOX_TELEMETRY environment variable (takes highest precedence)
//  2. ~/.Glassbox/telemetry_consent.json consent file
//  3. Runtime flag set via Init() (for backwards-compat with --telemetry flag)
//  4. Default: disabled
//
// This is the single authoritative check used by RecordCommandUsage and
// should be used wherever code needs to decide whether to emit telemetry.
func IsTelemetryEnabled() bool {
	ec := ResolveConsent()
	// Env or consent file takes precedence over the runtime flag.
	if ec.Source != ConsentSourceDefault {
		return ec.Enabled
	}
	// Fall back to the runtime flag set during Init().
	return commandTelemetryEnabled
}

// RecordCommandUsage emits a lightweight command usage event with environment metadata.
func RecordCommandUsage(ctx context.Context, command string) {
	if !IsTelemetryEnabled() {
		return
	}

	tracer := GetTracer()
	ctx, span := tracer.Start(ctx, "command_usage")
	defer span.End()

	if command == "" {
		command = "unknown"
	}

	// Sanitize the command name: truncate at 64 chars and strip any characters
	// that are not alphanumeric, dash, colon, or underscore. This prevents
	// user-controlled input (e.g. a malformed deep-link subcommand) from leaking
	// arbitrary data to the OTLP collector.
	command = sanitizeCommandName(command)

	// Set core command attributes
	attrs := []attribute.KeyValue{
		attribute.String("command.name", command),
		attribute.Bool("telemetry.anonymized", commandTelemetryAnonymized),
	}
	if corrID := CorrelationIDFromContext(ctx); corrID != "" {
		attrs = append(attrs, attribute.String("correlation_id", corrID))
	}
	span.SetAttributes(attrs...)

	// Add environment metadata (only if not anonymized)
	if !commandTelemetryAnonymized {
		span.SetAttributes(
			attribute.String("env.version", envMetadata.Version),
			attribute.String("env.platform", envMetadata.Platform),
			attribute.String("env.arch", envMetadata.Arch),
		)

		// Add feature flags as a list
		if len(envMetadata.FeatureFlags) > 0 {
			span.SetAttributes(
				attribute.StringSlice("env.feature_flags", envMetadata.FeatureFlags),
			)
		}
	}

	span.AddEvent("command.usage")
}

type correlationKey struct{}

// WithCorrelationID returns a new Context carrying the given correlation ID.
func WithCorrelationID(ctx context.Context, id string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, correlationKey{}, id)
}

// CorrelationIDFromContext extracts the correlation ID from ctx, returning "" if not found.
func CorrelationIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if id, ok := ctx.Value(correlationKey{}).(string); ok {
		return id
	}
	return ""
}

// NewCorrelationID generates a unique per-operation correlation ID.
func NewCorrelationID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("corr-%x", b)
}

// EnsureCorrelationID returns a Context with a correlation ID and the correlation ID string itself.
// If ctx already has a correlation ID, it returns ctx unchanged along with that ID.
func EnsureCorrelationID(ctx context.Context) (context.Context, string) {
	if ctx == nil {
		ctx = context.Background()
	}
	if id := CorrelationIDFromContext(ctx); id != "" {
		return ctx, id
	}
	id := NewCorrelationID()
	return WithCorrelationID(ctx, id), id
}

// GetEnvMetadata returns the current environment metadata.
// This is useful for testing and debugging.
func GetEnvMetadata() EnvMetadata {
	return envMetadata
}

// GetTracer returns the global tracer instance
func GetTracer() oteltrace.Tracer {
	tp := otel.GetTracerProvider()
	return tp.Tracer("glassbox")
}

// SanitizeValue returns a privacy-preserving representation for telemetry.
// Identifiers (hash, tx, contract) are fingerprinted client-side using
// SHA-256 and represented as a 32-character hex prefix (16 bytes of the
// digest). This is intentionally lossy — full hash recovery is not possible
// from the fingerprint, and birthday collisions at 16 bytes are acceptable for
// telemetry aggregation (not cryptographic) use.
// Paths are reduced to their basename. Long strings are truncated at 128 chars.
func SanitizeValue(key, v string) string {
	if v == "" {
		return ""
	}
	lk := strings.ToLower(key)
	switch {
	case strings.Contains(lk, "hash") || strings.Contains(lk, "tx") || strings.Contains(lk, "contract"):
		h := sha256.Sum256([]byte(v))
		// Transmit only a short deterministic fingerprint — not the full hash.
		// "sha256:" prefix (7 chars) + 25 hex chars = 32 chars total.
		return fmt.Sprintf("sha256:%x", h)[:32]
	case strings.Contains(v, string(filepath.Separator)) || strings.Contains(v, "/"):
		return filepath.Base(v)
	default:
		if len(v) > 128 {
			return v[:128] + "..."
		}
		return v
	}
}

// Attr returns an attribute.KeyValue with a sanitized value suitable for
// telemetry export.
func Attr(key, v string) attribute.KeyValue {
	return attribute.String(key, SanitizeValue(key, v))
}

// sanitizeCommandName returns a safe, bounded representation of a CLI command
// name for telemetry. It accepts alphanumerics, dash, colon, and underscore —
// the characters used by all built-in glassbox commands — and replaces
// anything else with "_". The result is capped at 64 characters.
func sanitizeCommandName(name string) string {
	if len(name) > 64 {
		name = name[:64]
	}
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == ':' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}
