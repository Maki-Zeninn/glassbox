// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Package logger provides structured logging with JSON and text output,
// severity filtering, request correlation, log rotation, and retention.
//
// Stable JSON field names:
//
//	time        - RFC 3339 timestamp
//	level       - severity string (DEBUG, INFO, WARN, ERROR)
//	msg         - log message
//	source      - file:line (added when AddSource=true)
//	correlation_id - per-request/operation correlation ID (if set in context)
//
// Sensitive fields are redacted via RedactPIN and the sensitive-key denylist
// before any bytes reach the underlying writer.
package logger

import (
	"context"
	"io"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"sync"
)

var (
	Logger *slog.Logger
	level  = new(slog.LevelVar)
	mu     sync.Mutex
)

// sensitiveKeyPatterns lists key substrings whose values must always be
// redacted in structured log records. Keys are matched case-insensitively.
var sensitiveKeyPatterns = []string{
	"pin",
	"password",
	"secret",
	"token",
	"private_key",
	"privatekey",
	"api_key",
	"apikey",
	"credential",
}

// isSensitiveKey returns true when the log attribute key matches a known
// sensitive pattern. The caller should redact the value before emitting.
func isSensitiveKey(key string) bool {
	lower := strings.ToLower(key)
	for _, pat := range sensitiveKeyPatterns {
		if strings.Contains(lower, pat) {
			return true
		}
	}
	return false
}

// RedactPIN replaces any HSM PIN values in the input string with "*****".
// This is used to prevent sensitive credentials from appearing in logs.
func RedactPIN(s string) string {
	// Pattern 1: GLASSBOX_PKCS11_PIN=1234 or GLASSBOX_PKCS11_PIN: 1234
	pattern1 := regexp.MustCompile(`GLASSBOX_PKCS11_PIN[=:]\s*[^\s"']+`)
	s = pattern1.ReplaceAllString(s, "GLASSBOX_PKCS11_PIN=*****")

	// Pattern 2: JSON-style "pin": "1234" or 'pin': '1234'
	pattern2 := regexp.MustCompile(`(["']pin["']\s*[:=]\s*["'])([^"']+)`)
	s = pattern2.ReplaceAllString(s, `$1*****`)

	return s
}

// redactingWriter wraps an io.Writer and redacts PIN values from all output.
type redactingWriter struct {
	w io.Writer
}

func (rw *redactingWriter) Write(p []byte) (n int, err error) {
	redacted := RedactPIN(string(p))
	return rw.w.Write([]byte(redacted))
}

// Custom log levels
const (
	LevelTrace = slog.Level(-8) // More verbose than Debug (-4)
)

// correlationKey is the context key used to store a per-request correlation ID.
type correlationKey struct{}

// WithCorrelation returns a new Context carrying the given correlation ID.
// The correlation ID is automatically included in every structured log record
// emitted via ContextLogger.
func WithCorrelation(ctx context.Context, id string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, correlationKey{}, id)
}

// CorrelationFromContext extracts the correlation ID from ctx.
// Returns "" when not set.
func CorrelationFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if id, ok := ctx.Value(correlationKey{}).(string); ok {
		return id
	}
	return ""
}

// ContextLogger returns a *slog.Logger pre-populated with the correlation ID
// stored in ctx (if any). When no correlation ID is present it returns the
// global Logger unchanged.
func ContextLogger(ctx context.Context) *slog.Logger {
	if ctx == nil {
		return Logger
	}
	if id := CorrelationFromContext(ctx); id != "" {
		return Logger.With("correlation_id", id)
	}
	return Logger
}

func init() {
	lvl := parseLevelFromEnv()
	initLogger(lvl, os.Stderr, false)
}

func parseLevelFromEnv() slog.Level {
	env := strings.ToUpper(os.Getenv("GLASSBOX_LOG_LEVEL"))
	return ParseLevel(env)
}

// ParseLevel converts a string to a slog.Level
func ParseLevel(levelStr string) slog.Level {
	switch strings.ToUpper(levelStr) {
	case "TRACE":
		return LevelTrace
	case "DEBUG":
		return slog.LevelDebug
	case "INFO":
		return slog.LevelInfo
	case "WARN", "WARNING":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// ParseLogLevel converts a human-readable level string (e.g. "debug", "info",
// "warn", "error") into the corresponding slog.Level. Unknown values default
// to slog.LevelInfo.
func ParseLogLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "trace", "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// RustLogFilter returns the RUST_LOG-compatible filter string that corresponds
// to the given Glassbox log level name. This is used when spawning the Rust
// simulator subprocess so that a single GLASSBOX_LOG_LEVEL value controls both the
// Go logger and the Rust tracing subscriber.
func RustLogFilter(erstLevel string) string {
	switch strings.ToLower(strings.TrimSpace(erstLevel)) {
	case "trace":
		return "trace"
	case "debug":
		return "debug"
	case "info":
		return "info"
	case "warn", "warning":
		return "warn"
	case "error":
		return "error"
	default:
		return "info"
	}
}

func initLogger(lvl slog.Level, w io.Writer, useJSON bool) {
	if w == nil {
		w = os.Stderr
	}

	level.Set(lvl)

	// Wrap the writer with redactingWriter to scrub PIN values
	w = &redactingWriter{w: w}

	var handler slog.Handler
	if useJSON {
		handler = newRedactingJSONHandler(w, &slog.HandlerOptions{
			Level:     level,
			AddSource: true,
		})
	} else {
		handler = NewTextHandler(w, &slog.HandlerOptions{
			Level:     level,
			AddSource: true,
		})
	}

	Logger = slog.New(handler)
}

func SetLevel(lvl slog.Level) {
	mu.Lock()
	defer mu.Unlock()
	level.Set(lvl)
}

func SetOutput(w io.Writer, useJSON bool) {
	mu.Lock()
	defer mu.Unlock()
	initLogger(level.Level(), w, useJSON)
}

// SetOutputWithRotation configures the global Logger to write JSON records to
// path with rotation and retention support. Call Close on the returned
// RotatingFileWriter to flush and release file handles.
//
// maxSizeBytes: rotate when the file exceeds this size (0 = no size limit).
// maxAgeDays:   delete rotated files older than this many days (0 = keep all).
func SetOutputWithRotation(path string, maxSizeBytes int64, maxAgeDays int, useJSON bool) (*RotatingFileWriter, error) {
	rfw, err := NewRotatingFileWriter(path, maxSizeBytes, maxAgeDays)
	if err != nil {
		return nil, err
	}
	mu.Lock()
	defer mu.Unlock()
	initLogger(level.Level(), rfw, useJSON)
	return rfw, nil
}

// TextHandler wraps slog.TextHandler.
type TextHandler struct {
	handler slog.Handler
}

// NewTextHandler constructs a TextHandler.
func NewTextHandler(w io.Writer, opts *slog.HandlerOptions) *TextHandler {
	if opts == nil {
		opts = &slog.HandlerOptions{}
	}
	return &TextHandler{
		handler: slog.NewTextHandler(w, opts),
	}
}

func (h *TextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.handler.Enabled(ctx, level)
}

func (h *TextHandler) Handle(ctx context.Context, record slog.Record) error {
	return h.handler.Handle(ctx, record)
}

func (h *TextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &TextHandler{handler: h.handler.WithAttrs(attrs)}
}

func (h *TextHandler) WithGroup(name string) slog.Handler {
	return &TextHandler{handler: h.handler.WithGroup(name)}
}

// redactingJSONHandler wraps slog.JSONHandler and redacts attribute values
// whose keys match sensitiveKeyPatterns before writing each record.
type redactingJSONHandler struct {
	inner slog.Handler
}

func newRedactingJSONHandler(w io.Writer, opts *slog.HandlerOptions) *redactingJSONHandler {
	return &redactingJSONHandler{inner: slog.NewJSONHandler(w, opts)}
}

func (h *redactingJSONHandler) Enabled(ctx context.Context, lvl slog.Level) bool {
	return h.inner.Enabled(ctx, lvl)
}

func (h *redactingJSONHandler) Handle(ctx context.Context, r slog.Record) error {
	// Inject correlation ID from context into the record when present.
	if id := CorrelationFromContext(ctx); id != "" {
		r.AddAttrs(slog.String("correlation_id", id))
	}
	// Redact sensitive attribute values.
	var redacted []slog.Attr
	r.Attrs(func(a slog.Attr) bool {
		if isSensitiveKey(a.Key) {
			redacted = append(redacted, slog.String(a.Key, "[REDACTED]"))
		} else {
			redacted = append(redacted, a)
		}
		return true
	})
	// Build a clean record with only the redacted attrs.
	clean := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	clean.AddAttrs(redacted...)
	return h.inner.Handle(ctx, clean)
}

func (h *redactingJSONHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	safe := make([]slog.Attr, 0, len(attrs))
	for _, a := range attrs {
		if isSensitiveKey(a.Key) {
			safe = append(safe, slog.String(a.Key, "[REDACTED]"))
		} else {
			safe = append(safe, a)
		}
	}
	return &redactingJSONHandler{inner: h.inner.WithAttrs(safe)}
}

func (h *redactingJSONHandler) WithGroup(name string) slog.Handler {
	return &redactingJSONHandler{inner: h.inner.WithGroup(name)}
}

// Trace logs at trace level (more verbose than debug)
func Trace(msg string, args ...any) {
	Logger.Log(context.Background(), LevelTrace, msg, args...)
}

// GetRustLogLevel returns the Rust env_logger compatible log level string
func GetRustLogLevel() string {
	currentLevel := level.Level()
	switch {
	case currentLevel <= LevelTrace:
		return "trace"
	case currentLevel <= slog.LevelDebug:
		return "debug"
	case currentLevel <= slog.LevelInfo:
		return "info"
	case currentLevel <= slog.LevelWarn:
		return "warn"
	default:
		return "error"
	}
}

// GetRustLogFormat returns the format for Rust logger (json or text)
func GetRustLogFormat() string {
	if format := os.Getenv("GLASSBOX_LOG_FORMAT"); format == "json" {
		return "json"
	}
	return "text"
}
