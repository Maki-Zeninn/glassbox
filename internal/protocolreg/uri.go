// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package protocolreg

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/dotandev/glassbox/internal/simulator"
)

// traceparentPattern matches a W3C traceparent header value:
//
//	<version>-<trace-id>-<parent-id>-<trace-flags>
//
// version    — 2 lowercase hex digits (must be "00" for the current spec)
// trace-id   — 32 lowercase hex digits (128-bit)
// parent-id  — 16 lowercase hex digits (64-bit)
// trace-flags — 2 lowercase hex digits (e.g. "01" = sampled)
//
// Reference: https://www.w3.org/TR/trace-context/#traceparent-header
var traceparentPattern = regexp.MustCompile(
	`^[0-9a-f]{2}-[0-9a-f]{32}-[0-9a-f]{16}-[0-9a-f]{2}$`,
)

// traceIDPattern matches a standalone 32-character lowercase hex trace ID.
var traceIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// spanIDPattern matches a standalone 16-character lowercase hex span ID.
var spanIDPattern = regexp.MustCompile(`^[0-9a-f]{16}$`)

const (
	// maxTracestateLen is the maximum length of the tracestate header value.
	// W3C specifies 512 bytes as the practical limit per list-member.
	maxTracestateLen = 512
)

var txHashPattern = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)

const (
	maxSourceLen    = 256
	maxSignatureLen = 512
	maxURILen       = 4096
	maxMockEntries  = 32
	maxManifestLen  = 1024
)

// allowedNetworks is the set of valid network identifiers for the deep link.
var allowedNetworks = map[string]bool{
	"testnet":   true,
	"mainnet":   true,
	"futurenet": true,
}

// allowedViews is the set of valid view mode identifiers for the deep link.
var allowedViews = map[string]bool{
	"trace":      true,
	"flamegraph": true,
	"events":     true,
	"auth":       true,
	"budget":     true,
	"storage":    true,
}

// ParsedDebugURI holds the validated fields extracted from a glassbox:// debug URI.
//
// Supported URI format:
//
//	glassbox://debug/<txhash>?network=<n>[&op=<i>][&operation=<i>][&view=<v>][&source=<s>][&signature=<s>][&traceparent=<w3c>][&tracestate=<w3c>][&trace-id=<hex32>][&span-id=<hex16>]
//
// Query parameters:
//   - network     (required) — one of: testnet, mainnet, futurenet
//   - op          (optional) — zero-based operation index (alias for "operation")
//   - operation   (optional) — zero-based operation index (legacy; "op" takes precedence)
//   - view        (optional) — initial view mode: trace, flamegraph, events, auth, budget, storage
//   - source      (optional) — free-form source identifier (e.g. "dashboard")
//   - signature   (optional) — free-form signature hint
//   - traceparent (optional) — W3C traceparent header for distributed trace correlation
//     (format: 00-<32hex>-<16hex>-<2hex>, e.g. 00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01)
//   - tracestate  (optional) — W3C tracestate vendor data accompanying traceparent
//   - trace-id    (optional) — standalone 32-character hex trace ID (used when traceparent absent)
//   - span-id     (optional) — standalone 16-character hex span ID (used when traceparent absent)
type ParsedDebugURI struct {
	// Raw is the original unmodified URI string.
	Raw string
	// TransactionHash is the 64-character lowercase hex transaction hash.
	TransactionHash string
	// Network is the validated network identifier (testnet, mainnet, futurenet).
	Network string
	// Op is the zero-based operation index, populated from the "op" or "operation" query parameter.
	// nil means no operation was specified.
	Op *int
	// Operation is an alias for Op retained for backward compatibility.
	// It always mirrors Op.
	Operation *int
	// View is the requested initial view mode (trace, flamegraph, events, auth, budget, storage).
	// Empty string means no view was specified and the default view should be used.
	View string
	// Source is an optional free-form source identifier.
	Source string
	// Signature is an optional free-form signature hint.
	Signature string
	// ProtocolVersion is the optional protocol version override for simulation.
	ProtocolVersion *uint32
	// MockLedgerManifest is the optional path to a mock ledger JSON manifest.
	MockLedgerManifest string
	// MockLedgerEntries is the optional list of mock ledger key:value overrides.
	MockLedgerEntries []string

	// ── Trace context fields ──────────────────────────────────────────────
	//
	// These fields carry W3C Trace Context identifiers so that a deep-link
	// invocation can be correlated with the originating distributed trace.
	// Populating them allows the Glassbox UI and backend to attach trace spans
	// to the correct parent context, improving trace accuracy and session
	// attribution.

	// Traceparent is the validated W3C traceparent header value, if present.
	// Format: 00-<trace-id:32hex>-<parent-id:16hex>-<flags:2hex>
	// Example: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	Traceparent string
	// Tracestate is the optional W3C tracestate vendor data accompanying Traceparent.
	Tracestate string
	// TraceID is the 32-character hex trace identifier. Extracted from Traceparent
	// when present; otherwise populated directly from the "trace-id" query parameter.
	TraceID string
	// SpanID is the 16-character hex span/parent-span identifier. Extracted from
	// Traceparent when present; otherwise populated from the "span-id" query parameter.
	SpanID string
}

// ParseDebugURI parses and validates a glassbox:// debug URI.
//
// Returns a descriptive error for each class of invalid input:
//   - empty URI
//   - null bytes or control characters
//   - wrong scheme
//   - wrong host (not "debug")
//   - missing or malformed transaction hash
//   - missing or invalid network
//   - invalid op/operation index (non-numeric or negative)
//   - unrecognised view mode
func ParseDebugURI(raw string) (*ParsedDebugURI, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("protocol URI must not be empty")
	}
	if len(raw) > maxURILen {
		return nil, fmt.Errorf("protocol URI exceeds maximum length (%d characters, max %d)", len(raw), maxURILen)
	}
	// Reject null bytes and ASCII control characters to prevent injection attacks.
	for i := 0; i < len(raw); i++ {
		if raw[i] == 0x00 {
			return nil, fmt.Errorf("protocol URI must not contain null bytes")
		}
		if raw[i] < 0x20 && raw[i] != '\t' {
			return nil, fmt.Errorf("protocol URI must not contain control characters (found 0x%02x)", raw[i])
		}
	}
	// Reject path traversal sequences anywhere in the URI.
	if strings.Contains(raw, "..") {
		return nil, fmt.Errorf("protocol URI must not contain path traversal sequences")
	}
	if !strings.HasPrefix(raw, Scheme+"://") {
		return nil, fmt.Errorf("invalid protocol URI: expected %s://", Scheme)
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse protocol URI: %w", err)
	}

	if parsed.Host != "debug" {
		return nil, fmt.Errorf("invalid protocol host %q: expected \"debug\"", parsed.Host)
	}

	transactionHash := strings.TrimPrefix(parsed.EscapedPath(), "/")
	transactionHash, err = url.PathUnescape(transactionHash)
	if err != nil {
		return nil, fmt.Errorf("decode transaction hash: %w", err)
	}
	if !txHashPattern.MatchString(transactionHash) {
		return nil, fmt.Errorf("invalid transaction hash %q: must be a 64-character hex string", transactionHash)
	}

	q := parsed.Query()

	// --- network (required) ---
	network := q.Get("network")
	if network == "" {
		return nil, fmt.Errorf("missing required query parameter: network")
	}
	if !allowedNetworks[network] {
		return nil, fmt.Errorf("invalid network %q: must be one of testnet, mainnet, futurenet", network)
	}

	source := q.Get("source")
	if len(source) > maxSourceLen {
		return nil, fmt.Errorf(
			"source parameter is too long (%d characters, max %d)",
			len(source), maxSourceLen,
		)
	}
	if strings.ContainsRune(source, 0) {
		return nil, fmt.Errorf("source parameter contains null bytes and cannot be used")
	}

	signature := q.Get("signature")
	if len(signature) > maxSignatureLen {
		return nil, fmt.Errorf(
			"signature parameter is too long (%d characters, max %d)",
			len(signature), maxSignatureLen,
		)
	}
	if strings.ContainsRune(signature, 0) {
		return nil, fmt.Errorf("signature parameter contains null bytes and cannot be used")
	}

	result := &ParsedDebugURI{
		Raw:             raw,
		TransactionHash: transactionHash,
		Network:         network,
		Source:          source,
		Signature:       signature,
	}

	// --- protocol-version (optional) ---
	protoVerStr := q.Get("protocol-version")
	if protoVerStr != "" {
		protoVer, err := strconv.ParseUint(protoVerStr, 10, 32)
		if err != nil || protoVer == 0 {
			return nil, fmt.Errorf("invalid protocol-version %q: must be a positive integer\n"+
				"  Fix: use a supported version number (e.g. 20, 21, or 22)", protoVerStr)
		}
		val := uint32(protoVer)
		if err := simulator.Validate(val); err != nil {
			return nil, fmt.Errorf("invalid protocol-version %d: %w\n"+
				"  Fix: use a supported protocol version (e.g. 20, 21, or 22)\n"+
				"  Tip: run 'glassbox version' to see all supported versions", val, err)
		}
		result.ProtocolVersion = &val
	}

	// --- mock-ledger-manifest (optional) ---
	mockManifest := q.Get("mock-ledger-manifest")
	if mockManifest != "" {
		if strings.ContainsRune(mockManifest, 0) {
			return nil, fmt.Errorf("mock-ledger-manifest parameter contains null bytes and cannot be used")
		}
		if len(mockManifest) > maxManifestLen {
			return nil, fmt.Errorf(
				"mock-ledger-manifest parameter is too long (%d characters, max %d)",
				len(mockManifest), maxManifestLen,
			)
		}
		// Reject path traversal sequences in manifest paths.
		if strings.Contains(mockManifest, "..") {
			return nil, fmt.Errorf("mock-ledger-manifest parameter must not contain path traversal sequences")
		}
		result.MockLedgerManifest = mockManifest
	}

	// --- mock-ledger-entry (optional, repeatable) ---
	mockEntries := q["mock-ledger-entry"]
	if len(mockEntries) > maxMockEntries {
		return nil, fmt.Errorf(
			"too many mock-ledger-entry parameters (%d, max %d)",
			len(mockEntries), maxMockEntries,
		)
	}
	if len(mockEntries) > 0 {
		seen := make(map[string]bool, len(mockEntries))
		for _, entry := range mockEntries {
			entry = strings.TrimSpace(entry)
			if entry == "" {
				continue
			}
			if seen[entry] {
				return nil, fmt.Errorf("duplicate mock-ledger-entry parameter %q", entry)
			}
			seen[entry] = true
			parts := strings.SplitN(entry, ":", 2)
			if len(parts) != 2 || parts[0] == "" {
				return nil, fmt.Errorf("invalid mock-ledger-entry format %q — expected key:value\n"+
					"  Fix: specify both key and value as non-empty colon-separated strings", entry)
			}
			val := parts[1]
			if val == "" {
				return nil, fmt.Errorf("mock-ledger-entry %q has an empty value\n"+
					"  Fix: specify a non-empty base64-encoded value after the colon", entry)
			}
			if _, decErr := base64.StdEncoding.DecodeString(val); decErr != nil {
				return nil, fmt.Errorf("mock-ledger-entry %q has an invalid base64 value: %v\n"+
					"  Fix: ensure the value after the colon is valid base64", entry, decErr)
			}
			result.MockLedgerEntries = append(result.MockLedgerEntries, entry)
		}
	}

	// --- op / operation (optional, "op" takes precedence) ---
	opStr := q.Get("op")
	if opStr == "" {
		opStr = q.Get("operation")
	}
	if opStr != "" {
		parsedOp, parseErr := strconv.Atoi(opStr)
		if parseErr != nil || parsedOp < 0 {
			return nil, fmt.Errorf(
				"invalid operation index %q: must be a non-negative integer\n"+
					"  Fix: use a whole number >= 0 (e.g. op=0 for the first operation)",
				opStr,
			)
		}
		// Guard against values that parsed as int on 64-bit but would overflow
		// on 32-bit platforms or downstream consumers expecting a reasonable index.
		const maxOpIndex = 65535
		if parsedOp > maxOpIndex {
			return nil, fmt.Errorf(
				"operation index %d exceeds the maximum allowed value (%d)\n"+
					"  Fix: use an index in the range 0–%d",
				parsedOp, maxOpIndex, maxOpIndex,
			)
		}
		result.Op = &parsedOp
		result.Operation = &parsedOp
	}

	// --- view (optional) ---
	if view := q.Get("view"); view != "" {
		if !allowedViews[view] {
			return nil, fmt.Errorf("invalid view %q: must be one of trace, flamegraph, events, auth, budget, storage", view)
		}
		result.View = view
	}

	// --- traceparent (optional, W3C Trace Context) ---
	//
	// When present the value must be a valid W3C traceparent header:
	//   <version>-<trace-id>-<parent-id>-<flags>
	//   e.g. 00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01
	//
	// On success, TraceID and SpanID are extracted from the traceparent so
	// callers never need to parse the compound value themselves.
	if tp := q.Get("traceparent"); tp != "" {
		tp = strings.ToLower(strings.TrimSpace(tp))
		if strings.ContainsRune(tp, 0) {
			return nil, fmt.Errorf(
				"traceparent parameter contains null bytes and cannot be used",
			)
		}
		if !traceparentPattern.MatchString(tp) {
			return nil, fmt.Errorf(
				"invalid traceparent %q: must follow W3C format 00-<32hex>-<16hex>-<2hex>\n"+
					"  Fix: use a valid W3C traceparent value, e.g. 00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
				tp,
			)
		}
		parts := strings.SplitN(tp, "-", 4)
		// parts[0] = version, parts[1] = trace-id, parts[2] = parent-id, parts[3] = flags
		if parts[0] != "00" {
			return nil, fmt.Errorf(
				"unsupported traceparent version %q: only version \"00\" is supported\n"+
					"  Fix: use a traceparent beginning with \"00-\"",
				parts[0],
			)
		}
		// Reject all-zero trace-id (invalid per spec §2.2.3).
		if parts[1] == strings.Repeat("0", 32) {
			return nil, fmt.Errorf(
				"invalid traceparent: trace-id must not be all zeros\n"+
					"  Fix: use a non-zero 32-character hex trace ID",
			)
		}
		// Reject all-zero parent-id (invalid per spec §2.2.4).
		if parts[2] == strings.Repeat("0", 16) {
			return nil, fmt.Errorf(
				"invalid traceparent: parent-id must not be all zeros\n"+
					"  Fix: use a non-zero 16-character hex span ID",
			)
		}
		result.Traceparent = tp
		result.TraceID = parts[1]
		result.SpanID = parts[2]
	}

	// --- tracestate (optional, W3C Trace Context) ---
	//
	// tracestate carries vendor-specific trace data and is only meaningful
	// alongside a traceparent. We accept it without requiring traceparent so
	// that partial propagation does not hard-fail, but we validate length and
	// reject null bytes.
	if ts := q.Get("tracestate"); ts != "" {
		if strings.ContainsRune(ts, 0) {
			return nil, fmt.Errorf(
				"tracestate parameter contains null bytes and cannot be used",
			)
		}
		if len(ts) > maxTracestateLen {
			return nil, fmt.Errorf(
				"tracestate parameter is too long (%d characters, max %d)\n"+
					"  Fix: truncate the tracestate value to at most %d characters",
				len(ts), maxTracestateLen, maxTracestateLen,
			)
		}
		result.Tracestate = ts
	}

	// --- trace-id (optional, standalone) ---
	//
	// Used when the caller cannot produce a full traceparent (e.g. older
	// clients). If traceparent was already parsed, the trace-id param is
	// silently ignored in favour of the richer traceparent value.
	if result.TraceID == "" {
		if tid := q.Get("trace-id"); tid != "" {
			tid = strings.ToLower(strings.TrimSpace(tid))
			if strings.ContainsRune(tid, 0) {
				return nil, fmt.Errorf(
					"trace-id parameter contains null bytes and cannot be used",
				)
			}
			if !traceIDPattern.MatchString(tid) {
				return nil, fmt.Errorf(
					"invalid trace-id %q: must be a 32-character lowercase hex string\n"+
						"  Fix: provide a valid 128-bit trace identifier, e.g. 4bf92f3577b34da6a3ce929d0e0e4736",
					tid,
				)
			}
			if tid == strings.Repeat("0", 32) {
				return nil, fmt.Errorf(
					"invalid trace-id: must not be all zeros\n"+
						"  Fix: use a non-zero 32-character hex trace ID",
				)
			}
			result.TraceID = tid
		}
	}

	// --- span-id (optional, standalone) ---
	//
	// Used alongside trace-id when traceparent is absent. If traceparent was
	// already parsed, the span-id param is silently ignored.
	if result.SpanID == "" {
		if sid := q.Get("span-id"); sid != "" {
			sid = strings.ToLower(strings.TrimSpace(sid))
			if strings.ContainsRune(sid, 0) {
				return nil, fmt.Errorf(
					"span-id parameter contains null bytes and cannot be used",
				)
			}
			if !spanIDPattern.MatchString(sid) {
				return nil, fmt.Errorf(
					"invalid span-id %q: must be a 16-character lowercase hex string\n"+
						"  Fix: provide a valid 64-bit span identifier, e.g. 00f067aa0ba902b7",
					sid,
				)
			}
			if sid == strings.Repeat("0", 16) {
				return nil, fmt.Errorf(
					"invalid span-id: must not be all zeros\n"+
						"  Fix: use a non-zero 16-character hex span ID",
				)
			}
			result.SpanID = sid
		}
	}

	// Reject unknown query parameters to prevent injection via unexpected fields.
	knownParams := map[string]bool{
		"network":              true,
		"op":                   true,
		"operation":            true,
		"view":                 true,
		"source":               true,
		"signature":            true,
		"protocol-version":     true,
		"mock-ledger-manifest": true,
		"mock-ledger-entry":    true,
		"traceparent":          true,
		"tracestate":           true,
		"trace-id":             true,
		"span-id":              true,
	}
	for param := range q {
		if !knownParams[param] {
			return nil, fmt.Errorf("unknown query parameter %q", param)
		}
	}

	return result, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Trace-step URI: glassbox://trace/<txhash>/step/<N>?network=…[&file=…][&line=…][&col=…][&view=…]
// ─────────────────────────────────────────────────────────────────────────────

const (
	// maxStepIndex is the upper bound accepted for the step index in a trace URI.
	// Traces with more than 65535 steps are not expected in current usage.
	maxStepIndex = 65535

	// maxFileLen is the maximum length of a URL-encoded source file path
	// embedded in a trace-step URI.
	maxFileLen = 4096
)

// allowedTraceViews is the set of valid view mode identifiers for trace-step
// deep links. It overlaps with allowedViews but adds a "source" panel that is
// only meaningful in the trace-navigation context.
var allowedTraceViews = map[string]bool{
	"trace":      true,
	"source":     true,
	"flamegraph": true,
	"events":     true,
	"auth":       true,
	"budget":     true,
	"storage":    true,
}

// ParsedTraceStepURI holds the validated fields extracted from a
// glassbox://trace/<txhash>/step/<N> URI.
//
// URI format:
//
//	glassbox://trace/<txhash>/step/<N>?network=<n>[&file=<path>][&line=<n>][&col=<n>][&view=<mode>]
//
// Required parameters:
//   - network — one of: testnet, mainnet, futurenet
//
// Optional parameters:
//   - file — URL-encoded source file path for direct editor navigation
//   - line — 1-based source line number
//   - col  — 1-based column number
//   - view — initial panel: trace, source, flamegraph, events, auth, budget, storage
type ParsedTraceStepURI struct {
	// Raw is the original unmodified URI string.
	Raw string
	// TransactionHash is the 64-character lowercase hex transaction hash.
	TransactionHash string
	// StepIndex is the zero-based step index within the execution trace.
	StepIndex int
	// Network is the validated network identifier.
	Network string
	// File is the optional URL-decoded source file path for editor navigation.
	// Empty when not specified.
	File string
	// Line is the optional 1-based source line number. 0 means not specified.
	Line int
	// Column is the optional 1-based column number. 0 means not specified.
	Column int
	// View is the requested initial view panel. Empty means use the default.
	View string
}

// ParseTraceStepURI parses and validates a glassbox://trace/ step-navigation URI.
//
// Returns a descriptive error for each class of invalid input (empty URI,
// wrong scheme, wrong host, missing/malformed hash, missing step path,
// non-numeric or negative step, missing or invalid network, unknown parameter,
// invalid view mode).
func ParseTraceStepURI(raw string) (*ParsedTraceStepURI, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("protocol URI must not be empty")
	}
	if len(raw) > maxURILen {
		return nil, fmt.Errorf("protocol URI exceeds maximum length (%d characters, max %d)", len(raw), maxURILen)
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] == 0x00 {
			return nil, fmt.Errorf("protocol URI must not contain null bytes")
		}
		if raw[i] < 0x20 && raw[i] != '\t' {
			return nil, fmt.Errorf("protocol URI must not contain control characters (found 0x%02x)", raw[i])
		}
	}
	if strings.Contains(raw, "..") {
		return nil, fmt.Errorf("protocol URI must not contain path traversal sequences")
	}
	if !strings.HasPrefix(raw, Scheme+"://") {
		return nil, fmt.Errorf("invalid protocol URI: expected %s://", Scheme)
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse protocol URI: %w", err)
	}

	if parsed.Host != "trace" {
		return nil, fmt.Errorf("invalid protocol host %q: expected \"trace\" for trace-step URIs", parsed.Host)
	}

	// Path must be /<txhash>/step/<N>
	// url.Parse keeps the leading slash in Path; strip it.
	rawPath := strings.TrimPrefix(parsed.EscapedPath(), "/")
	segments := strings.Split(rawPath, "/")
	// Expect exactly three segments: <txhash>, "step", <N>
	if len(segments) != 3 {
		return nil, fmt.Errorf(
			"trace URI has wrong path structure %q: expected /<txhash>/step/<N>",
			rawPath,
		)
	}

	txHashRaw, err := url.PathUnescape(segments[0])
	if err != nil {
		return nil, fmt.Errorf("decode transaction hash: %w", err)
	}
	if !txHashPattern.MatchString(txHashRaw) {
		return nil, fmt.Errorf("invalid transaction hash %q: must be a 64-character hex string", txHashRaw)
	}

	if segments[1] != "step" {
		return nil, fmt.Errorf(
			"trace URI is missing required path segment: expected \"step\", got %q",
			segments[1],
		)
	}

	stepStr, err := url.PathUnescape(segments[2])
	if err != nil {
		return nil, fmt.Errorf("decode step index: %w", err)
	}
	stepIdx, parseErr := strconv.Atoi(stepStr)
	if parseErr != nil || stepIdx < 0 {
		return nil, fmt.Errorf(
			"invalid step index %q: must be a non-negative integer\n"+
				"  Fix: use a whole number >= 0 (e.g. step/0 for the first step)",
			stepStr,
		)
	}
	if stepIdx > maxStepIndex {
		return nil, fmt.Errorf(
			"step index %d exceeds the maximum allowed value (%d)\n"+
				"  Fix: use an index in the range 0–%d",
			stepIdx, maxStepIndex, maxStepIndex,
		)
	}

	q := parsed.Query()

	// --- network (required) ---
	network := q.Get("network")
	if network == "" {
		return nil, fmt.Errorf("missing required query parameter: network")
	}
	if !allowedNetworks[network] {
		return nil, fmt.Errorf("invalid network %q: must be one of testnet, mainnet, futurenet", network)
	}

	result := &ParsedTraceStepURI{
		Raw:             raw,
		TransactionHash: txHashRaw,
		StepIndex:       stepIdx,
		Network:         network,
	}

	// --- file (optional) ---
	if fileRaw := q.Get("file"); fileRaw != "" {
		if len(fileRaw) > maxFileLen {
			return nil, fmt.Errorf(
				"file parameter is too long (%d characters, max %d)",
				len(fileRaw), maxFileLen,
			)
		}
		if strings.ContainsRune(fileRaw, 0) {
			return nil, fmt.Errorf("file parameter contains null bytes and cannot be used")
		}
		result.File = fileRaw
	}

	// --- line (optional) ---
	if lineStr := q.Get("line"); lineStr != "" {
		lineVal, lineErr := strconv.Atoi(lineStr)
		if lineErr != nil || lineVal < 1 {
			return nil, fmt.Errorf(
				"invalid line %q: must be a positive integer (1-based)\n"+
					"  Fix: use a whole number >= 1",
				lineStr,
			)
		}
		result.Line = lineVal
	}

	// --- col (optional) ---
	if colStr := q.Get("col"); colStr != "" {
		colVal, colErr := strconv.Atoi(colStr)
		if colErr != nil || colVal < 1 {
			return nil, fmt.Errorf(
				"invalid col %q: must be a positive integer (1-based)\n"+
					"  Fix: use a whole number >= 1",
				colStr,
			)
		}
		result.Column = colVal
	}

	// --- view (optional) ---
	if view := q.Get("view"); view != "" {
		if !allowedTraceViews[view] {
			return nil, fmt.Errorf(
				"invalid view %q: must be one of trace, source, flamegraph, events, auth, budget, storage",
				view,
			)
		}
		result.View = view
	}

	// Reject unknown query parameters.
	knownTraceParams := map[string]bool{
		"network": true,
		"file":    true,
		"line":    true,
		"col":     true,
		"view":    true,
	}
	for param := range q {
		if !knownTraceParams[param] {
			return nil, fmt.Errorf("unknown query parameter %q", param)
		}
	}

	return result, nil
}
