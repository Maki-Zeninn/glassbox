// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package errors

import (
	stdliberrors "errors"
	"fmt"
)

// formatBytes converts bytes to a human-readable string (e.g., "1.5 MB")
func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// New is a proxy to the standard errors.New
func New(text string) error {
	return stdliberrors.New(text)
}

// Is is a proxy to the standard errors.Is
func Is(err, target error) bool {
	return stdliberrors.Is(err, target)
}

// As is a proxy to the standard errors.As
func As(err error, target any) bool {
	return stdliberrors.As(err, target)
}

// Hint returns the actionable remediation guidance attached to an error via
// ErstError.Hint, or "" if the error carries none. The CLI uses it to show the
// user how to recover from a failure instead of printing only a low-level error.
func Hint(err error) string {
	var e *ErstError
	if As(err, &e) {
		return e.Hint
	}
	return ""
}

// Sentinel errors for comparison with errors.Is
var (
	ErrTransactionNotFound    = stdliberrors.New("transaction not found")
	ErrRPCConnectionFailed    = stdliberrors.New("RPC connection failed")
	ErrRPCTimeout             = stdliberrors.New("RPC request timed out")
	ErrAllRPCFailed           = stdliberrors.New("all RPC endpoints failed")
	ErrSimulatorNotFound      = stdliberrors.New("simulator binary not found")
	ErrSimulationFailed       = stdliberrors.New("simulation execution failed")
	ErrSimCrash               = stdliberrors.New("simulator process crashed")
	ErrInvalidNetwork         = stdliberrors.New("invalid network")
	ErrMarshalFailed          = stdliberrors.New("failed to marshal request")
	ErrUnmarshalFailed        = stdliberrors.New("failed to unmarshal response")
	ErrSimulationLogicError   = stdliberrors.New("simulation logic error")
	ErrRPCError               = stdliberrors.New("RPC server returned an error")
	ErrValidationFailed       = stdliberrors.New("validation failed")
	ErrProtocolUnsupported    = stdliberrors.New("unsupported protocol version")
	ErrArgumentRequired       = stdliberrors.New("required argument missing")
	ErrAuditLogInvalid        = stdliberrors.New("audit log verification failed")
	ErrSessionNotFound        = stdliberrors.New("session not found")
	ErrUnauthorized           = stdliberrors.New("unauthorized")
	ErrLedgerNotFound         = stdliberrors.New("ledger not found")
	ErrLedgerArchived         = stdliberrors.New("ledger has been archived")
	ErrRateLimitExceeded      = stdliberrors.New("rate limit exceeded")
	ErrRPCResponseTooLarge    = stdliberrors.New("RPC response too large")
	ErrRPCRequestTooLarge     = stdliberrors.New("RPC request payload too large")
	ErrConfigFailed           = stdliberrors.New("configuration error")
	ErrNetworkNotFound        = stdliberrors.New("network not found")
	ErrMissingLedgerKey       = stdliberrors.New("missing ledger key in footprint")
	// Footprint XDR decoder sentinels [Issue #1114]
	ErrEmptyFootprint         = stdliberrors.New("empty ledger footprint")
	ErrOverlappingFootprint   = stdliberrors.New("overlapping ledger key in footprint")
	ErrOversizedFootprint     = stdliberrors.New("footprint exceeds MAX_LEDGER_ENTRIES")
	ErrUnknownLedgerKeyType   = stdliberrors.New("unknown ledger key type")
	ErrFootprintXDRVersion    = stdliberrors.New("footprint encoded with wrong XDR version prefix")
	ErrWasmInvalid            = stdliberrors.New("invalid WASM file")
	ErrSpecNotFound           = stdliberrors.New("contract spec not found")
	ErrShellExit              = stdliberrors.New("exit")
	ErrRegistryConflict       = stdliberrors.New("protocol registry conflict detected")
	ErrLedgerSequenceMismatch = stdliberrors.New("ledger sequence mismatch")
	// ErrSourceDiscoveryFailed is returned when source discovery for a contract
	// fails and no fallback path is available or all fallback stages were exhausted.
	ErrSourceDiscoveryFailed = stdliberrors.New("source discovery failed")
	// ErrRPCInvalidResponse is returned when a JSON-RPC response fails
	// structural validation at the RPC boundary (missing required fields,
	// wrong types, or invalid error envelope shape).
	ErrRPCInvalidResponse = stdliberrors.New("RPC response failed validation")
	// ErrSessionConflict is returned when a concurrent writer has already
	// advanced the session revision past the value the caller last read
	// [Issue #813].
	ErrSessionConflict = stdliberrors.New("session write conflict")
	// ErrSessionLockHeld is returned when the advisory lock for a session is
	// currently held by another live process [Issue #813].
	ErrSessionLockHeld = stdliberrors.New("session advisory lock is held by another process")
	// ErrAnalysisTruncated is returned when the Go-side analysis pipeline was
	// halted early due to a resource budget (timeout, max nodes, depth, or
	// input bytes) being exhausted [Issue #838].  Partial findings already
	// emitted are valid; deeper subtree findings are absent.
	ErrAnalysisTruncated = stdliberrors.New("analysis truncated: resource budget exhausted")

	// ErrKMSThrottled is returned when AWS KMS responds with a throttling
	// error and the retry budget is exhausted [Issue #805].
	ErrKMSThrottled = stdliberrors.New("KMS throttled: retry budget exhausted")
	// ErrKMSUnauthorized is returned when KMS rejects a call due to
	// insufficient permissions or key state [Issue #805].
	ErrKMSUnauthorized = stdliberrors.New("KMS authorization failed")
	// ErrKMSTransientFailure is returned when a KMS call fails with a
	// transient infrastructure error and the retry budget is exhausted [Issue #805].
	ErrKMSTransientFailure = stdliberrors.New("KMS transient failure: retry budget exhausted")

	// ErrAuditDirPolicyViolation is returned when audit:verify-dir finds a
	// directory-level policy violation that per-file checks alone would miss
	// [Issue #806].
	ErrAuditDirPolicyViolation = stdliberrors.New("audit directory policy violation")
)

type LedgerNotFoundError struct {
	Sequence uint32
	Message  string
}

func (e *LedgerNotFoundError) Error() string {
	return e.Message
}

func (e *LedgerNotFoundError) Is(target error) bool {
	return target == ErrLedgerNotFound
}

type LedgerArchivedError struct {
	Sequence uint32
	Message  string
}

func (e *LedgerArchivedError) Error() string {
	return e.Message
}

func (e *LedgerArchivedError) Is(target error) bool {
	return target == ErrLedgerArchived
}

type RateLimitError struct {
	Message string
}

func (e *RateLimitError) Error() string {
	return e.Message
}

func (e *RateLimitError) Is(target error) bool {
	return target == ErrRateLimitExceeded
}

// ResponseTooLargeError indicates the Soroban RPC response exceeded server limits.
type ResponseTooLargeError struct {
	URL     string
	Message string
}

func (e *ResponseTooLargeError) Error() string {
	return e.Message
}

func (e *ResponseTooLargeError) Is(target error) bool {
	return target == ErrRPCResponseTooLarge
}

// MissingLedgerKeyError is returned when partial simulation halts because
// a required ledger key is absent from the provided state snapshot.
type MissingLedgerKeyError struct {
	Key string
}

func (e *MissingLedgerKeyError) Error() string {
	return fmt.Sprintf("%v: %s", ErrMissingLedgerKey, e.Key)
}

func (e *MissingLedgerKeyError) Is(target error) bool {
	return target == ErrMissingLedgerKey
}

// Wrap functions for consistent error wrapping
func WrapTransactionNotFound(err error) error {
	return &ErstError{
		Code:    ErstTransactionNotFound,
		Message: "transaction not found",
		OrigErr: err,
		Hint:    "Check the transaction hash and confirm --network matches where it was submitted (testnet, mainnet, or futurenet). If the transaction is recent the RPC may not have indexed it yet — retry shortly or use --watch to wait for it.",
	}
}

func WrapRPCConnectionFailed(err error) error {
	return &ErstError{
		Code:    ErstRPCConnectionFailed,
		Message: "RPC connection failed",
		OrigErr: err,
		Hint:    "The RPC endpoint could not be reached. Check your internet connection and the endpoint, pass a known-good one with --rpc-url <url>, and make sure it serves the selected --network.",
	}
}

func WrapSimulatorNotFound(msg string) error {
	return &ErstError{
		Code:    ErstSimulatorNotFound,
		Message: msg,
	}
}

func WrapSimulationFailed(err error, stderr string) error {
	msg := "simulation execution failed"
	if stderr != "" {
		msg = fmt.Sprintf("simulation execution failed: %s", stderr)
	} else if err != nil {
		msg = fmt.Sprintf("simulation execution failed: %s", err.Error())
	}
	return &ErstError{
		Code:    ErstSimulationFailed,
		Message: msg,
		OrigErr: err,
	}
}

func WrapInvalidNetwork(network string) error {
	return &ErstError{
		Code:    ErstInvalidNetwork,
		Message: fmt.Sprintf("invalid network %q — must be one of: testnet, mainnet, futurenet", network),
	}
}

func WrapMarshalFailed(err error) error {
	return &ErstError{
		Code:    ErstValidationFailed,
		Message: "failed to marshal request",
		OrigErr: err,
	}
}

func WrapUnmarshalFailed(err error, output string) error {
	return &ErstError{
		Code:    ErstValidationFailed,
		Message: output,
		OrigErr: err,
	}
}

func WrapSimulationLogicError(msg string) error {
	return &ErstError{
		Code:    ErstSimulationLogicError,
		Message: msg,
	}
}

func WrapRPCTimeout(err error) error {
	return &ErstError{
		Code:    ErstRPCTimeout,
		Message: "RPC request timed out",
		OrigErr: err,
		Hint:    "The RPC endpoint did not respond in time. It may be overloaded or slow — retry, raise the timeout, or switch to a different endpoint with --rpc-url <url>.",
	}
}

func WrapAllRPCFailed() error {
	return &ErstError{
		Code:    ErstAllRPCFailed,
		Message: "all RPC endpoints failed",
		Hint: "All configured RPC endpoints failed. Provide a working endpoint with --rpc-url <url>, " +
			"run 'glassbox doctor' for a full environment check, or check 'glassbox status --network <name>'.",
	}
}

// WrapAllRPCFailedWithDiagnostics wraps an all-endpoints-failed condition and
// embeds the failure summary as part of the remediation hint so JSON output
// carries the diagnostic context.
func WrapAllRPCFailedWithDiagnostics(summary string) error {
	return &ErstError{
		Code:    ErstAllRPCFailed,
		Message: "all RPC endpoints failed",
		Hint:    summary,
	}
}

func WrapRPCError(url string, msg string, code int) error {
	return &ErstError{
		Code:    ErstRPCError,
		Message: fmt.Sprintf("from %s: %s (code %d)", url, msg, code),
	}
}

func WrapSimCrash(err error, stderr string) error {
	msg := stderr
	if msg == "" && err != nil {
		msg = err.Error()
	}
	return &ErstError{
		Code:    ErstSimCrash,
		Message: msg,
		OrigErr: err,
	}
}

func WrapValidationError(msg string) error {
	return &ErstError{
		Code:    ErstValidationFailed,
		Message: msg,
	}
}

func WrapProtocolUnsupported(version uint32) error {
	return &ErstError{
		Code:    ErstValidationFailed,
		Message: fmt.Sprintf("unsupported protocol version: %d", version),
		Hint:    "This build of Glassbox does not support that Soroban protocol version. Update to a newer Glassbox release, or target a transaction on a network running a supported protocol.",
	}
}

func WrapCliArgumentRequired(arg string) error {
	return &ErstError{
		Code:    ErstValidationFailed,
		Message: fmt.Sprintf("--%s is required but was not provided", arg),
	}
}

func WrapAuditLogInvalid(msg string) error {
	return &ErstError{
		Code:    ErstValidationFailed,
		Message: msg,
	}
}

func WrapSessionNotFound(sessionID string) error {
	return &ErstError{
		Code:    ErstValidationFailed,
		Message: sessionID,
	}
}

func WrapUnauthorized(msg string) error {
	if msg != "" {
		return &ErstError{
			Code:    ErstUnauthorized,
			Message: msg,
		}
	}
	return &ErstError{
		Code:    ErstUnauthorized,
		Message: "unauthorized",
	}
}

func WrapLedgerNotFound(sequence uint32) error {
	return &ErstError{
		Code:    ErstLedgerNotFound,
		Message: fmt.Sprintf("ledger %d not found (may be archived or not yet created)", sequence),
	}
}

func WrapLedgerArchived(sequence uint32) error {
	return &ErstError{
		Code:    ErstLedgerArchived,
		Message: fmt.Sprintf("ledger %d has been archived and is no longer available", sequence),
	}
}

func WrapRateLimitExceeded() error {
	return &ErstError{
		Code:    ErstRateLimitExceeded,
		Message: "rate limit exceeded, please try again later",
	}
}

func WrapConfigError(msg string, err error) error {
	return &ErstError{
		Code:    ErstConfigFailed,
		Message: msg,
	}
}

func WrapNetworkNotFound(network string) error {
	return &ErstError{
		Code:    ErstNetworkNotFound,
		Message: network,
	}
}

func WrapWasmInvalid(msg string) error {
	return fmt.Errorf("%w: %s", ErrWasmInvalid, msg)
}

func WrapSpecNotFound() error {
	return fmt.Errorf("%w: no contractspecv0 section found; is this a compiled Soroban contract?", ErrSpecNotFound)
}

// WrapRPCResponseTooLarge wraps an HTTP 413 response into a readable message
// explaining that the Soroban RPC response exceeded the server's size limit.
func WrapRPCResponseTooLarge(url string) error {
	return &ResponseTooLargeError{
		URL: url,
		Message: fmt.Sprintf(
			"%v: the response from %s exceeded the server's maximum allowed size; "+
				"reduce the request scope (e.g. fewer ledger keys) or contact the RPC provider"+
				" to increase the Soroban RPC response limit",
			ErrRPCResponseTooLarge, url),
	}
}

// WrapRPCRequestTooLarge returns an error when the JSON payload exceeds
// the maximum allowed size (10MB) to prevent network submission.
func WrapRPCRequestTooLarge(sizeBytes int64, maxSizeBytes int64) error {
	return fmt.Errorf(
		"%v: request payload size (%s) exceeds maximum allowed size (%s). "+
			"This payload is too large to submit to the network. "+
			"Consider reducing the amount of data being sent (e.g., fewer ledger entries, "+
			"smaller transaction envelopes, or breaking the request into smaller chunks)",
		ErrRPCRequestTooLarge,
		formatBytes(sizeBytes),
		formatBytes(maxSizeBytes),
	)
}

// RPCInvalidResponseError is returned when a JSON-RPC response is structurally
// invalid. It carries the endpoint URL and the RPC method name so diagnostics
// can identify exactly which call produced the malformed data without echoing
// any response body that might contain credentials.
type RPCInvalidResponseError struct {
	// Endpoint is the URL of the RPC server that returned the response.
	Endpoint string
	// Method is the JSON-RPC method name (e.g. "getLedgerEntries").
	Method string
	// Field is the name of the missing or invalid field, if known.
	Field string
	// Reason is a human-readable description of the validation failure.
	Reason string
}

func (e *RPCInvalidResponseError) Error() string {
	if e.Field != "" {
		return fmt.Sprintf(
			"%v: %s from %s: field %q: %s",
			ErrRPCInvalidResponse, e.Method, e.Endpoint, e.Field, e.Reason,
		)
	}
	return fmt.Sprintf(
		"%v: %s from %s: %s",
		ErrRPCInvalidResponse, e.Method, e.Endpoint, e.Reason,
	)
}

func (e *RPCInvalidResponseError) Is(target error) bool {
	return target == ErrRPCInvalidResponse
}

// WrapRPCInvalidResponse returns a diagnostic error for a structurally invalid
// RPC response. No response body or credential data should be passed; only the
// field name and a structural reason are included.
func WrapRPCInvalidResponse(endpoint, method, field, reason string) error {
	return &RPCInvalidResponseError{
		Endpoint: endpoint,
		Method:   method,
		Field:    field,
		Reason:   reason,
	}
}

func WrapMissingLedgerKey(key string) error {
	return &MissingLedgerKeyError{Key: key}
}

// WrapSourceDiscoveryFailed returns an actionable error when all source
// discovery stages (registry, GitHub, override, prompt) are exhausted and
// no source mapping can be provided. The message includes the contract ID
// and a remediation hint so users know how to proceed.
func WrapSourceDiscoveryFailed(contractID string, hint string) error {
	msg := fmt.Sprintf("source discovery failed for contract %q", contractID)
	h := hint
	if h == "" {
		h = "Provide the contract source directory with --contract-source <path>, " +
			"or recompile with 'debug = true' in [profile.release] for DWARF symbols. " +
			"Run 'glassbox doctor' for a full environment check."
	}
	return &ErstError{
		Code:    ErstSourceDiscoveryFailed,
		Message: msg,
		Hint:    h,
	}
}

// WrapSessionConflict returns a structured error for a session write conflict
// (concurrent-writer race, Issue #813).
func WrapSessionConflict(sessionID string, expected, actual int64) error {
	return &ErstError{
		Code: ErstSessionConflict,
		Message: fmt.Sprintf(
			"session %q write conflict: expected revision %d but disk has revision %d",
			sessionID, expected, actual,
		),
		Hint: "Another Glassbox process saved this session while you were editing it. " +
			"Run 'glassbox session resume " + sessionID + "' to reload the latest version, " +
			"or re-run your save with --force to overwrite it.",
	}
}

// WrapSessionLockHeld returns a structured error when the advisory lock for a
// session is currently held by a live process (Issue #813).
func WrapSessionLockHeld(sessionID string, holderPID int) error {
	return &ErstError{
		Code: ErstSessionLockHeld,
		Message: fmt.Sprintf(
			"session %q advisory lock is held by process %d",
			sessionID, holderPID,
		),
		Hint: "Another Glassbox instance is currently saving this session. " +
			"Wait for it to finish and retry. If the other process has crashed, " +
			"the lock will be cleared automatically after 5 minutes.",
	}
}

// LedgerSequenceMismatchError is returned when a transaction's referenced
// ledger sequence does not match the sequence in the current replay state.
type LedgerSequenceMismatchError struct {
	// TxSequence is the ledger sequence the transaction references.
	TxSequence uint32
	// ReplaySequence is the ledger sequence present in the local replay state.
	ReplaySequence uint32
}

func (e *LedgerSequenceMismatchError) Error() string {
	return fmt.Sprintf(
		"%v: transaction references ledger %d but replay state is at ledger %d",
		ErrLedgerSequenceMismatch, e.TxSequence, e.ReplaySequence,
	)
}

func (e *LedgerSequenceMismatchError) Is(target error) bool {
	return target == ErrLedgerSequenceMismatch
}

// WrapLedgerSequenceMismatch wraps a ledger sequence mismatch with both sequence numbers.
func WrapLedgerSequenceMismatch(txSeq, replaySeq uint32) error {
	return &LedgerSequenceMismatchError{TxSequence: txSeq, ReplaySequence: replaySeq}
}

// WrapAnalysisTruncated returns a structured error indicating that the analysis
// pipeline was halted early because a resource budget was exhausted [Issue #838].
// phase identifies which phase was truncated (e.g. "depth_analysis",
// "cost_annotation", "source_scan", "parser"), and reason names the budget that
// triggered truncation (e.g. "timeout", "max_nodes", "max_depth", "max_bytes").
// Callers must never present the accompanying partial findings as complete.
func WrapAnalysisTruncated(phase, reason string) error {
	return &ErstError{
		Code:    ErstAnalysisTruncated,
		Message: fmt.Sprintf("analysis phase %q truncated: %s budget exhausted", phase, reason),
		Hint: "The analyzer reached a configured resource limit before completing. " +
			"Reported findings are from the portion of the trace that was visited and are valid, " +
			"but findings that required deeper traversal are absent. " +
			"Raise --analyzer-timeout, --max-nodes, --max-depth, or --max-input-bytes to analyze the full trace.",
	}
}

// WrapInternal wraps an unexpected internal failure (e.g. ID generation,
// unexpected I/O) that does not belong to any more specific sentinel family
// [Issue #762]. Using this instead of returning a bare fmt.Errorf keeps every
// command-facing failure a stable ErstError with a consistent code and exit
// bucket rather than surfacing as ErstUnknown to automation.
func WrapInternal(msg string, err error) error {
	return &ErstError{
		Code:    ErstInternalError,
		Message: fmt.Sprintf("%s: %v", msg, err),
		OrigErr: err,
	}
}

const (
	// RPC origin
	CodeRPCConnectionFailed  ErstErrorCode = "RPC_CONNECTION_FAILED"
	CodeRPCTimeout           ErstErrorCode = "RPC_TIMEOUT"
	CodeRPCAllFailed         ErstErrorCode = "RPC_ALL_ENDPOINTS_FAILED"
	CodeRPCError             ErstErrorCode = "RPC_SERVER_ERROR"
	CodeRPCResponseTooLarge  ErstErrorCode = "RPC_RESPONSE_TOO_LARGE"
	CodeRPCRequestTooLarge   ErstErrorCode = "RPC_REQUEST_TOO_LARGE"
	CodeRPCRateLimitExceeded ErstErrorCode = "RPC_RATE_LIMIT_EXCEEDED"
	CodeRPCMarshalFailed     ErstErrorCode = "RPC_MARSHAL_FAILED"
	CodeRPCUnmarshalFailed   ErstErrorCode = "RPC_UNMARSHAL_FAILED"
	CodeTransactionNotFound  ErstErrorCode = "RPC_TRANSACTION_NOT_FOUND"
	CodeLedgerNotFound       ErstErrorCode = "RPC_LEDGER_NOT_FOUND"
	CodeLedgerArchived       ErstErrorCode = "RPC_LEDGER_ARCHIVED"
	// CodeRPCInvalidResponse is emitted when a JSON-RPC response passes HTTP
	// delivery but fails structural validation (missing required fields, wrong
	// types, or an invalid error-envelope shape).
	CodeRPCInvalidResponse ErstErrorCode = "RPC_INVALID_RESPONSE"

	// Simulator origin
	CodeSimNotFound            ErstErrorCode = "SIM_BINARY_NOT_FOUND"
	CodeSimCrash               ErstErrorCode = "SIM_PROCESS_CRASHED"
	CodeSimExecFailed          ErstErrorCode = "SIM_EXECUTION_FAILED"
	CodeSimMemoryLimitExceeded ErstErrorCode = "ERR_MEMORY_LIMIT_EXCEEDED"
	CodeSimLogicError          ErstErrorCode = "SIM_LOGIC_ERROR"
	CodeSimProtoUnsup          ErstErrorCode = "SIM_PROTOCOL_UNSUPPORTED"
	// Aliases used by the simulator lifecycle mapper.
	CodeSimulatorError    = CodeSimExecFailed
	CodeSimulatorNotFound = CodeSimNotFound
	CodeSimulatorCrashed  = CodeSimCrash

	// Shared / general
	CodeValidationFailed ErstErrorCode = "VALIDATION_FAILED"
	CodeConfigFailed     ErstErrorCode = "CONFIG_ERROR"
	CodeUnknown          ErstErrorCode = "UNKNOWN"
)

// codeToSentinel maps each ErstErrorCode to its corresponding sentinel error
// so that errors.Is(erstErr, sentinel) works reliably.
var codeToSentinel = map[ErstErrorCode]error{
	CodeRPCConnectionFailed:    ErrRPCConnectionFailed,
	CodeRPCTimeout:             ErrRPCTimeout,
	CodeRPCAllFailed:           ErrAllRPCFailed,
	CodeRPCError:               ErrRPCError,
	CodeRPCResponseTooLarge:    ErrRPCResponseTooLarge,
	CodeRPCRequestTooLarge:     ErrRPCRequestTooLarge,
	CodeRPCRateLimitExceeded:   ErrRateLimitExceeded,
	CodeRPCMarshalFailed:       ErrMarshalFailed,
	CodeRPCUnmarshalFailed:     ErrUnmarshalFailed,
	CodeTransactionNotFound:    ErrTransactionNotFound,
	CodeLedgerNotFound:         ErrLedgerNotFound,
	CodeLedgerArchived:         ErrLedgerArchived,
	CodeRPCInvalidResponse:     ErrRPCInvalidResponse,
	CodeSimNotFound:            ErrSimulatorNotFound,
	CodeSimCrash:               ErrSimCrash,
	CodeSimExecFailed:          ErrSimulationFailed,
	CodeSimMemoryLimitExceeded: ErrSimulationFailed,
	CodeSimLogicError:          ErrSimulationLogicError,
	CodeSimProtoUnsup:          ErrProtocolUnsupported,
	CodeValidationFailed:       ErrValidationFailed,
	CodeConfigFailed:           ErrConfigFailed,
	// Session concurrency [Issue #813]
	ErstSessionConflict: ErrSessionConflict,
	ErstSessionLockHeld: ErrSessionLockHeld,
	// Analyzer resource budgets [Issue #838]
	ErstAnalysisTruncated: ErrAnalysisTruncated,
	// KMS signing [Issue #805]
	ErstKMSThrottled:        ErrKMSThrottled,
	ErstKMSUnauthorized:     ErrKMSUnauthorized,
	ErstKMSTransientFailure: ErrKMSTransientFailure,
	// Audit directory policy [Issue #806]
	ErstAuditDirPolicyViolation: ErrAuditDirPolicyViolation,
}

// newErstError is the internal constructor.
func newErstError(code ErstErrorCode, message string, original error) *ErstError {
	if message == "" && original != nil {
		message = original.Error()
	}
	return &ErstError{Code: code, Message: message, OrigErr: original}
}

// --- Typed constructors for RPC boundary ---

// NewRPCError wraps any RPC error into the unified type.
func NewRPCError(code ErstErrorCode, original error) *ErstError {
	return newErstError(code, "", original)
}

// --- Typed constructors for Simulator boundary ---

// NewSimError wraps any Simulator error into the unified type.
func NewSimError(code ErstErrorCode, original error) *ErstError {
	return newErstError(code, "", original)
}

// NewSimErrorMsg wraps a simulator error with an explicit message (for string-only errors).
func NewSimErrorMsg(code ErstErrorCode, message string) *ErstError {
	return newErstError(code, message, nil)
}

// IsErstCode checks if an error carries a specific ErstErrorCode.
func IsErstCode(err error, code ErstErrorCode) bool {
	var e *ErstError
	if As(err, &e) {
		return e.Code == code
	}
	return false
}

// GetCode returns the ErstErrorCode carried by err, or CodeUnknown when the
// error is not an *ErstError.
func GetCode(err error) ErstErrorCode {
	var e *ErstError
	if As(err, &e) {
		return e.Code
	}
	return CodeUnknown
}

// ── KMS signing errors [Issue #805] ─────────────────────────────────────────

// WrapKMSThrottled returns a structured error when AWS KMS responds with
// a throttling error and the retry budget is exhausted.
// attempts is the number of KMS API calls that were made before giving up.
// correlationID is the caller-supplied tracing id (may be empty).
func WrapKMSThrottled(attempts int, correlationID string) error {
	msg := fmt.Sprintf(
		"KMS throttled after %d attempt(s)", attempts,
	)
	if correlationID != "" {
		msg += fmt.Sprintf(" (correlation_id=%s)", correlationID)
	}
	return &ErstError{
		Code:    ErstKMSThrottled,
		Message: msg,
		Hint: "AWS KMS is throttling requests. " +
			"Increase GLASSBOX_KMS_MAX_RETRIES / GLASSBOX_KMS_MAX_BACKOFF_MS to absorb burst traffic, " +
			"or reduce the signing rate. Use --audit-log-kms-key-id to confirm the correct key.",
	}
}

// WrapKMSUnauthorized returns a structured error when AWS KMS rejects a
// Sign or GetPublicKey call because of insufficient IAM permissions, an
// invalid key ID, a disabled key, or a key pending deletion.
// code is the raw AWS error code (e.g. "AccessDeniedException").
// keyRef is a non-secret identifier for the key (alias, truncated ARN, etc.).
func WrapKMSUnauthorized(code, keyRef string) error {
	msg := fmt.Sprintf(
		"KMS authorization failed (code=%s, key=%s)", code, keyRef,
	)
	return &ErstError{
		Code:    ErstKMSUnauthorized,
		Message: msg,
		Hint: "Verify the IAM policy attached to this identity includes kms:Sign and kms:GetPublicKey " +
			"for the target key, and that the key is Enabled (not Disabled or PendingDeletion). " +
			"See docs/audit-kms-signing.md for the minimum IAM policy.",
	}
}

// WrapKMSTransientFailure returns a structured error when a KMS call fails
// with a transient infrastructure error (InternalError, ServiceUnavailable,
// etc.) and the retry budget is exhausted without a successful result.
// attempts is the total number of KMS API calls.
// lastCode is the AWS error code from the final attempt.
func WrapKMSTransientFailure(attempts int, lastCode, correlationID string) error {
	msg := fmt.Sprintf(
		"KMS transient failure after %d attempt(s), last code: %s", attempts, lastCode,
	)
	if correlationID != "" {
		msg += fmt.Sprintf(" (correlation_id=%s)", correlationID)
	}
	return &ErstError{
		Code:    ErstKMSTransientFailure,
		Message: msg,
		Hint: "AWS KMS returned a transient error. " +
			"This is usually a brief AWS service disruption. " +
			"Retry the command or increase GLASSBOX_KMS_MAX_RETRIES. " +
			"If the problem persists, check the AWS Service Health Dashboard for your region.",
	}
}

// ── Audit directory policy errors [Issue #806] ───────────────────────────────

// WrapAuditDirPolicyViolation returns a structured error for directory-level
// audit policy violations detected by audit:verify-dir.
// summary is a brief description; details lists individual violations.
func WrapAuditDirPolicyViolation(summary string, violationCount int) error {
	return &ErstError{
		Code:    ErstAuditDirPolicyViolation,
		Message: fmt.Sprintf("%s (%d violation(s))", summary, violationCount),
		Hint: "Run 'glassbox audit:verify-dir --json --dir <path>' for machine-readable details " +
			"with per-file and aggregate results. Use --policy-config to load a policy file, " +
			"or --expected-signers / --expected-schema-version to tighten checks.",
	}
}
