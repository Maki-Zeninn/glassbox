// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package simulator

// host_calls.go — Capture host function calls with typed arguments.
// Issue #532: Capture host function calls with typed arguments
//
// Records host function name, normalized argument types, result or error,
// gas delta, and source step without exposing sensitive payloads unintentionally.

import (
	"encoding/json"
	"fmt"
	"strings"
)

// HostCallRecord captures a single host function call during execution.
type HostCallRecord struct {
	// FunctionName is the name of the host function invoked.
	FunctionName string `json:"function_name"`

	// Step is the trace step index where this call occurred.
	Step int `json:"step"`

	// ContractID is the contract that made the call.
	ContractID string `json:"contract_id,omitempty"`

	// Arguments are the normalized, size-limited argument values.
	// Each argument is a HostCallArg with a type tag and truncated value.
	Arguments []HostCallArg `json:"arguments,omitempty"`

	// Result is the return value (nil if the call failed).
	Result *HostCallValue `json:"result,omitempty"`

	// Error is the error message if the call failed (empty on success).
	Error string `json:"error,omitempty"`

	// GasDelta is the CPU instruction delta caused by this call.
	GasDelta *uint64 `json:"gas_delta,omitempty"`

	// MemoryDelta is the memory delta caused by this call.
	MemoryDelta *uint64 `json:"memory_delta,omitempty"`

	// SourceStep is the source-level step identifier.
	SourceFile string `json:"source_file,omitempty"`
	SourceLine int    `json:"source_line,omitempty"`

	// Duration is the execution time in nanoseconds.
	DurationNs int64 `json:"duration_ns,omitempty"`

	// Redacted indicates whether any arguments were redacted.
	Redacted bool `json:"redacted,omitempty"`
}

// HostCallArg represents a single typed argument to a host function call.
type HostCallArg struct {
	// Type is the normalized type tag (e.g., "Address", "Bytes", "Symbol", "U64").
	Type string `json:"type"`

	// Value is the string representation of the argument, truncated to MaxValueLen.
	Value string `json:"value"`

	// Truncated is true when the value was longer than MaxValueLen.
	Truncated bool `json:"truncated,omitempty"`

	// Redacted is true when the argument matched a sensitive field pattern.
	Redacted bool `json:"redacted,omitempty"`
}

// HostCallValue represents a typed return value from a host function call.
type HostCallValue struct {
	Type      string `json:"type"`
	Value     string `json:"value"`
	Truncated bool   `json:"truncated,omitempty"`
}

// Constants for host call recording
const (
	// MaxValueLen is the maximum length of a serialized argument value.
	// Oversized values are truncated with an ellipsis suffix.
	MaxValueLen = 256

	// MaxArgCount limits the number of arguments recorded per call.
	MaxArgCount = 32
)

// SensitiveHostArgPatterns are field name patterns that trigger redaction.
var SensitiveHostArgPatterns = []string{
	"secret", "private", "key", "seed", "mnemonic", "password",
}

// NormalizeHostCallArg converts a raw argument value into a typed, size-limited
// HostCallArg. The value is truncated if it exceeds MaxValueLen.
func NormalizeHostCallArg(name string, raw interface{}) HostCallArg {
	typeTag := inferArgType(raw)
	valueStr := fmt.Sprintf("%v", raw)

	truncated := false
	if len(valueStr) > MaxValueLen {
		valueStr = valueStr[:MaxValueLen] + "..."
		truncated = true
	}

	redacted := false
	if isSensitiveHostArg(name) {
		valueStr = "REDACTED"
		redacted = true
	}

	return HostCallArg{
		Type:      typeTag,
		Value:     valueStr,
		Truncated: truncated,
		Redacted:  redacted,
	}
}

// inferArgType maps a Go value to a normalized type tag.
func inferArgType(v interface{}) string {
	switch v.(type) {
	case string:
		return "String"
	case int, int32, int64, uint, uint32, uint64:
		return "Int"
	case bool:
		return "Bool"
	case []byte:
		return "Bytes"
	case []interface{}:
		return "Vec"
	case map[string]interface{}:
		return "Map"
	case nil:
		return "Void"
	default:
		return "Unknown"
	}
}

func isSensitiveHostArg(name string) bool {
	lower := strings.ToLower(name)
	for _, p := range SensitiveHostArgPatterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// HostCallRecorder accumulates host call records during execution.
type HostCallRecorder struct {
	Records []HostCallRecord
	maxSize int
}

// NewHostCallRecorder creates a recorder with the given maximum record count.
func NewHostCallRecorder(maxSize int) *HostCallRecorder {
	if maxSize <= 0 {
		maxSize = 10000
	}
	return &HostCallRecorder{
		Records: make([]HostCallRecord, 0),
		maxSize:  maxSize,
	}
}

// Record captures a single host function call.
func (r *HostCallRecorder) Record(rec HostCallRecord) {
	if len(r.Records) >= r.maxSize {
		return // Drop oldest to maintain bounded size
		r.Records = r.Records[1:]
	}

	// Truncate arguments
	if len(rec.Arguments) > MaxArgCount {
		rec.Arguments = rec.Arguments[:MaxArgCount]
		rec.Redacted = true
	}

	// Redaction check
	for i := range rec.Arguments {
		if rec.Arguments[i].Redacted {
			rec.Redacted = true
		}
	}

	r.Records = append(r.Records, rec)
}

// ToJSON serializes all recorded host calls.
func (r *HostCallRecorder) ToJSON() ([]byte, error) {
	return json.MarshalIndent(r.Records, "", "  ")
}

// Count returns the number of recorded host calls.
func (r *HostCallRecorder) Count() int {
	return len(r.Records)
}

// ByFunction returns all calls to a specific host function.
func (r *HostCallRecorder) ByFunction(fnName string) []HostCallRecord {
	var result []HostCallRecord
	for _, rec := range r.Records {
		if rec.FunctionName == fnName {
			result = append(result, rec)
		}
	}
	return result
}

// FailedCalls returns all host calls that returned an error.
func (r *HostCallRecorder) FailedCalls() []HostCallRecord {
	var result []HostCallRecord
	for _, rec := range r.Records {
		if rec.Error != "" {
			result = append(result, rec)
		}
	}
	return result
}
