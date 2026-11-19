// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// provenance_record.go — typed provenance records linking inputs and outputs.
//
// A ProvenanceRecord captures the causal chain between the inputs that were
// consumed by an operation and the outputs it produced, using content hashes
// rather than mutable paths.  This allows an incident responder to verify
// that a given artifact was produced from a specific, unchanged set of inputs.
//
// What provenance proves:
//   - That a specific set of inputs (by hash) was consumed by a versioned tool
//     at a recorded timestamp to produce specific outputs (by hash).
//   - That neither the inputs nor the outputs have been silently modified.
//
// What provenance does NOT prove:
//   - The correctness of the tool itself.
//   - The security of the environment in which the tool ran.
//   - That secrets or private keys were not compromised.
//
// Secrets and private source contents are excluded unless the user explicitly
// opts in via EmbedSourceContent on individual InputRef entries.
package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ProvenanceRecord links a set of inputs to a set of derived outputs via
// content hashes, tool versions, and a timestamp.  It is immutable once
// produced — the intent is to append a new record for each operation rather
// than modifying an existing one.
type ProvenanceRecord struct {
	// RecordID is a stable identifier for this record, derived from the
	// content hash of the record itself.  Set automatically by Finalize.
	RecordID string `json:"record_id,omitempty"`

	// Operation names the operation that was performed (e.g. "debug",
	// "sign", "export", "import", "migration").
	Operation string `json:"operation"`

	// ToolName is the name of the tool that produced this record.
	ToolName string `json:"tool_name"`

	// ToolVersion is the exact version of the tool.
	ToolVersion string `json:"tool_version"`

	// Timestamp is the UTC wall-clock time the operation completed.
	Timestamp time.Time `json:"timestamp"`

	// Configuration holds non-secret configuration values relevant to the
	// operation (e.g. network name, schema version).  Values that might
	// contain credentials are excluded.
	Configuration map[string]string `json:"configuration,omitempty"`

	// Inputs lists every input consumed by the operation, identified by
	// hash rather than mutable path.
	Inputs []InputRef `json:"inputs,omitempty"`

	// Outputs lists every output produced by the operation, identified by
	// hash.
	Outputs []OutputRef `json:"outputs,omitempty"`

	// ChainPredecessorID is the RecordID of the immediately preceding
	// ProvenanceRecord in the chain, when the operation derives from a
	// prior recorded state.  Empty for genesis records.
	ChainPredecessorID string `json:"chain_predecessor_id,omitempty"`

	// Verified is true when the record's input and output hashes have been
	// re-verified against the actual artifacts and all hashes matched.
	// Set by VerifyRecord.
	Verified bool `json:"verified,omitempty"`
}

// InputRef identifies a single input artifact by its content hash and a
// human-readable role name.  The path is not stored; only the hash is
// required for verification.
type InputRef struct {
	// Role is a short, stable identifier for the input's purpose (e.g.
	// "transaction_envelope", "ledger_state", "source_artifact").
	Role string `json:"role"`
	// SHA256 is the lowercase hex-encoded SHA-256 hash of the input content.
	SHA256 string `json:"sha256"`
	// Size is the byte length of the input, for sanity-checking.
	Size int64 `json:"size,omitempty"`
	// MediaType is an optional MIME type hint (e.g. "application/octet-stream").
	MediaType string `json:"media_type,omitempty"`
	// EmbedSourceContent, when true, means the content itself has been
	// embedded elsewhere in the session or bundle (e.g. as EnvelopeXDR).
	// This flag is informational only — it does not cause content to be
	// embedded by this struct.
	EmbedSourceContent bool `json:"embed_source_content,omitempty"`
}

// OutputRef identifies a single output artifact by its content hash and role.
type OutputRef struct {
	// Role describes what this output represents (e.g. "signed_audit_log",
	// "session_archive", "trace_json").
	Role string `json:"role"`
	// SHA256 is the lowercase hex-encoded SHA-256 hash of the output content.
	SHA256 string `json:"sha256"`
	// Size is the byte length of the output.
	Size int64 `json:"size,omitempty"`
	// MediaType is an optional MIME type hint.
	MediaType string `json:"media_type,omitempty"`
}

// ChainMismatch describes a single input-output hash discrepancy found by
// VerifyRecord.
type ChainMismatch struct {
	// Role is the InputRef or OutputRef role that does not match.
	Role string
	// RecordedHash is the SHA-256 stored in the ProvenanceRecord.
	RecordedHash string
	// ActualHash is the SHA-256 computed from the live artifact.
	ActualHash string
	// IsInput is true for input mismatches, false for output mismatches.
	IsInput bool
}

func (m *ChainMismatch) String() string {
	kind := "output"
	if m.IsInput {
		kind = "input"
	}
	return fmt.Sprintf("%s %q: recorded hash %s, actual hash %s",
		kind, m.Role, shortSHA(m.RecordedHash), shortSHA(m.ActualHash))
}

// RecordVerificationResult is the output of VerifyRecord.
type RecordVerificationResult struct {
	// OK is true when all input and output hashes matched.
	OK bool
	// Mismatches lists every hash discrepancy found.
	Mismatches []ChainMismatch
	// InputsChecked is the number of inputs that were verified.
	InputsChecked int
	// OutputsChecked is the number of outputs that were verified.
	OutputsChecked int
}

// HashContent returns the lowercase hex-encoded SHA-256 of data, suitable
// for use as InputRef.SHA256 or OutputRef.SHA256.
func HashContent(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// NewInputRef constructs an InputRef by hashing content in place.
// It is a convenience constructor that ensures the hash is always derived
// from the actual content rather than supplied separately.
func NewInputRef(role string, content []byte) InputRef {
	return InputRef{
		Role:   role,
		SHA256: HashContent(content),
		Size:   int64(len(content)),
	}
}

// NewOutputRef constructs an OutputRef by hashing content in place.
func NewOutputRef(role string, content []byte) OutputRef {
	return OutputRef{
		Role:   role,
		SHA256: HashContent(content),
		Size:   int64(len(content)),
	}
}

// Finalize computes and sets RecordID as the SHA-256 of the record's
// canonical JSON representation (excluding the RecordID field itself).
// Callers should call Finalize before persisting or sharing a record.
func (r *ProvenanceRecord) Finalize() error {
	// Temporarily clear RecordID to produce a stable canonical hash.
	saved := r.RecordID
	r.RecordID = ""
	b, err := canonicalProvenanceJSON(r)
	r.RecordID = saved
	if err != nil {
		return fmt.Errorf("provenance record: failed to compute canonical JSON: %w", err)
	}
	r.RecordID = HashContent(b)
	return nil
}

// canonicalProvenanceJSON returns a deterministic JSON encoding of r with
// sorted map keys and sorted slices.
func canonicalProvenanceJSON(r *ProvenanceRecord) ([]byte, error) {
	// Sort configuration keys and input/output slices for determinism.
	type wireRecord struct {
		Operation          string            `json:"operation"`
		ToolName           string            `json:"tool_name"`
		ToolVersion        string            `json:"tool_version"`
		Timestamp          time.Time         `json:"timestamp"`
		Configuration      map[string]string `json:"configuration,omitempty"`
		Inputs             []InputRef        `json:"inputs,omitempty"`
		Outputs            []OutputRef       `json:"outputs,omitempty"`
		ChainPredecessorID string            `json:"chain_predecessor_id,omitempty"`
	}
	wire := wireRecord{
		Operation:          r.Operation,
		ToolName:           r.ToolName,
		ToolVersion:        r.ToolVersion,
		Timestamp:          r.Timestamp.UTC(),
		Configuration:      sortedStringMap(r.Configuration),
		ChainPredecessorID: r.ChainPredecessorID,
	}
	// Sort inputs and outputs by role for stability.
	inputs := make([]InputRef, len(r.Inputs))
	copy(inputs, r.Inputs)
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].Role < inputs[j].Role })
	wire.Inputs = inputs

	outputs := make([]OutputRef, len(r.Outputs))
	copy(outputs, r.Outputs)
	sort.Slice(outputs, func(i, j int) bool { return outputs[i].Role < outputs[j].Role })
	wire.Outputs = outputs

	return json.Marshal(wire)
}

// sortedStringMap returns a new map with the same key-value pairs, suitable
// for deterministic JSON marshalling (Go maps marshal in key-sorted order
// when using encoding/json when the map is reconstructed as a new value).
func sortedStringMap(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// VerifyRecord checks a ProvenanceRecord's input and output hashes against
// live content.  liveInputs and liveOutputs are maps from role → content.
// Missing roles in the live maps are silently skipped (the caller controls
// which artifacts are available for re-hashing).
func VerifyRecord(r *ProvenanceRecord, liveInputs, liveOutputs map[string][]byte) *RecordVerificationResult {
	result := &RecordVerificationResult{OK: true}

	for _, ref := range r.Inputs {
		content, ok := liveInputs[ref.Role]
		if !ok {
			continue // artifact not provided for re-verification — skip
		}
		result.InputsChecked++
		got := HashContent(content)
		if !strings.EqualFold(got, ref.SHA256) {
			result.Mismatches = append(result.Mismatches, ChainMismatch{
				Role:         ref.Role,
				RecordedHash: ref.SHA256,
				ActualHash:   got,
				IsInput:      true,
			})
			result.OK = false
		}
	}

	for _, ref := range r.Outputs {
		content, ok := liveOutputs[ref.Role]
		if !ok {
			continue
		}
		result.OutputsChecked++
		got := HashContent(content)
		if !strings.EqualFold(got, ref.SHA256) {
			result.Mismatches = append(result.Mismatches, ChainMismatch{
				Role:         ref.Role,
				RecordedHash: ref.SHA256,
				ActualHash:   got,
				IsInput:      false,
			})
			result.OK = false
		}
	}

	return result
}

// ProvenanceChain is an ordered list of ProvenanceRecords forming a causal
// chain.  The first record is the genesis (no predecessor); each subsequent
// record's ChainPredecessorID must equal the previous record's RecordID.
type ProvenanceChain struct {
	Records []ProvenanceRecord `json:"records"`
}

// Append adds a record to the chain, automatically setting its
// ChainPredecessorID to the last record's RecordID and calling Finalize.
func (c *ProvenanceChain) Append(r *ProvenanceRecord) error {
	if len(c.Records) > 0 {
		r.ChainPredecessorID = c.Records[len(c.Records)-1].RecordID
	}
	if err := r.Finalize(); err != nil {
		return err
	}
	c.Records = append(c.Records, *r)
	return nil
}

// VerifyChainIntegrity validates that every record in the chain has a correct
// ChainPredecessorID and that no RecordIDs are duplicated.  It does not verify
// artifact hashes — use VerifyRecord for that.
func (c *ProvenanceChain) VerifyChainIntegrity() []string {
	var issues []string
	seen := make(map[string]bool, len(c.Records))

	for i, rec := range c.Records {
		if rec.RecordID == "" {
			issues = append(issues, fmt.Sprintf("record[%d]: RecordID is empty — call Finalize before appending", i))
			continue
		}
		if seen[rec.RecordID] {
			issues = append(issues, fmt.Sprintf("record[%d]: duplicate RecordID %s", i, shortSHA(rec.RecordID)))
		}
		seen[rec.RecordID] = true

		if i == 0 {
			if rec.ChainPredecessorID != "" {
				issues = append(issues, fmt.Sprintf("record[0]: genesis record must not have a ChainPredecessorID (got %s)",
					shortSHA(rec.ChainPredecessorID)))
			}
			continue
		}
		prev := c.Records[i-1]
		if rec.ChainPredecessorID == "" {
			issues = append(issues, fmt.Sprintf("record[%d] %s: missing ChainPredecessorID (expected %s)",
				i, shortSHA(rec.RecordID), shortSHA(prev.RecordID)))
		} else if !strings.EqualFold(rec.ChainPredecessorID, prev.RecordID) {
			issues = append(issues, fmt.Sprintf(
				"record[%d] %s: ChainPredecessorID %s does not match predecessor RecordID %s",
				i, shortSHA(rec.RecordID), shortSHA(rec.ChainPredecessorID), shortSHA(prev.RecordID),
			))
		}
	}
	return issues
}

// ── Session integration ───────────────────────────────────────────────────────

// ProvenanceChainKey is the JSON key used to store a ProvenanceChain in a
// session's ExtrasJSON map when the chain is attached but not yet a first-class
// session field.
const ProvenanceChainKey = "provenance_chain"

// SessionProvenanceChain retrieves the ProvenanceChain from a session's
// ExtrasJSON, returning an empty chain when none is present.
func SessionProvenanceChain(data *Data) *ProvenanceChain {
	if data == nil || data.ExtrasJSON == nil {
		return &ProvenanceChain{}
	}
	raw, ok := data.ExtrasJSON[ProvenanceChainKey]
	if !ok {
		return &ProvenanceChain{}
	}
	var chain ProvenanceChain
	if err := json.Unmarshal(raw, &chain); err != nil {
		return &ProvenanceChain{}
	}
	return &chain
}

// AttachProvenanceChain serialises chain and stores it in data.ExtrasJSON
// under ProvenanceChainKey so it travels with session archives.
func AttachProvenanceChain(data *Data, chain *ProvenanceChain) error {
	if data == nil {
		return fmt.Errorf("cannot attach provenance chain to nil session")
	}
	if data.ExtrasJSON == nil {
		data.ExtrasJSON = make(map[string]json.RawMessage)
	}
	b, err := json.Marshal(chain)
	if err != nil {
		return fmt.Errorf("failed to serialise provenance chain: %w", err)
	}
	data.ExtrasJSON[ProvenanceChainKey] = json.RawMessage(b)
	return nil
}

// BuildSessionProvenanceRecord constructs a ProvenanceRecord for a session
// debug operation, hashing the key artifacts that were used or produced.
// Sensitive fields (private keys, credentials) are excluded.
func BuildSessionProvenanceRecord(data *Data, toolVersion string) *ProvenanceRecord {
	r := &ProvenanceRecord{
		Operation:   "debug",
		ToolName:    "glassbox",
		ToolVersion: toolVersion,
		Timestamp:   time.Now().UTC(),
		Configuration: map[string]string{
			"network":        data.Network,
			"schema_version": fmt.Sprintf("%d", data.SchemaVersion),
		},
	}
	// Hash the envelope XDR as the primary transaction input.
	if data.EnvelopeXdr != "" {
		r.Inputs = append(r.Inputs, NewInputRef("transaction_envelope", []byte(data.EnvelopeXdr)))
	}
	// Hash the simulator response as the primary output.
	if data.SimResponseJSON != "" {
		r.Outputs = append(r.Outputs, NewOutputRef("sim_response", []byte(data.SimResponseJSON)))
	}
	// Hash the trace if present.
	if data.TraceJSON != "" {
		r.Outputs = append(r.Outputs, NewOutputRef("trace", []byte(data.TraceJSON)))
	}
	return r
}

// ── helpers ───────────────────────────────────────────────────────────────────

func shortSHA(h string) string {
	if len(h) <= 16 {
		return h
	}
	return h[:8] + "…" + h[len(h)-8:]
}
