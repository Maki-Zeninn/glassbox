// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package audit

// policy_level.go maps the outcome of a directory verification onto an exit
// code according to a selected policy level.
//
// Per-file signature verification answers "is this file internally valid?".
// Directory verification answers "is the collection tamper-evident?", and an
// adversary with write access to the directory can delete, reorder or splice
// entries that all verify individually. Those collection-level irregularities —
// a gap in the sequence, a broken hash-chain link, an unsigned file, a missing
// manifest — are what the policy level governs.
//
// The three levels exist because the right response is contextual. A
// production audit log is legally required to be complete, so any irregularity
// is fatal. A developer's local log directory is best-effort, so the same
// irregularity should be reported without failing the command.

import (
	"strconv"
	"strings"
)

// PolicyLevel selects how strict directory verification is.
type PolicyLevel string

// Policy levels, ordered from strictest to most permissive.const (
	// PolicyStrict fails on any irregularity: a sequence gap, a missing or
	// broken chain link, an unsigned file, a structural problem, or a
	// signature failure. This is the correct level for a log directory that is
	// subject to an audit or a retention obligation.
	PolicyStrict PolicyLevel = "strict"

	// PolicyWarn reports every irregularity but exits successfully. Signature
	// failures are still fatal, because a bad signature means a file is not
	// merely out of place — it is not authentic.
	PolicyWarn PolicyLevel = "warn"

	// PolicyPermissive only fails on cryptographic signature failures. Chain
	// and structural irregularities are reported for information.
	PolicyPermissive PolicyLevel = "permissive"
)

// PolicyLevels lists the valid policy levels, in documentation order.
func PolicyLevels() []PolicyLevel {
	return []PolicyLevel{PolicyStrict, PolicyWarn, PolicyPermissive}
}

// DefaultPolicyLevel is used when the caller does not select one. Strict is the
// default because failing loudly is the safe behaviour for an audit tool.
const DefaultPolicyLevel = PolicyStrict

// IsValidPolicyLevel reports whether level is a recognised policy level.
func IsValidPolicyLevel(level PolicyLevel) bool {
	switch level {
	case PolicyStrict, PolicyWarn, PolicyPermissive:
		return true
	default:
		return false
	}
}

// ParsePolicyLevel validates and normalises a policy level string. An empty
// string selects DefaultPolicyLevel.
func ParsePolicyLevel(value string) (PolicyLevel, bool) {
	trimmed := PolicyLevel(strings.ToLower(strings.TrimSpace(value)))
	if trimmed == "" {
		return DefaultPolicyLevel, true
	}
	if !IsValidPolicyLevel(trimmed) {
		return "", false
	}
	return trimmed, true
}

// ChainIssueKind classifies one chain irregularity.
type ChainIssueKind string

// Chain issue kinds.
const (
	// IssueSequenceGap means a segment is missing from the ordered sequence.
	IssueSequenceGap ChainIssueKind = "sequence_gap"
	// IssueBrokenChainLink means previous_segment_hash does not match the
	// previous segment's computed hash.
	IssueBrokenChainLink ChainIssueKind = "broken_chain_link"
	// IssueUnsignedFile means a file in the directory has no manifest.
	IssueUnsignedFile ChainIssueKind = "unsigned_file"
	// IssueMissingManifest means a segment file is present but its manifest is
	// absent or unparseable.
	IssueMissingManifest ChainIssueKind = "missing_manifest"
	// IssueStructural means a segment body is missing, empty or corrupt.
	IssueStructural ChainIssueKind = "structural"
	// IssuePolicy means a DirPolicy constraint was violated.
	IssuePolicy ChainIssueKind = "policy_violation"
	// IssueSignature means cryptographic signature verification failed.
	IssueSignature ChainIssueKind = "signature_failure"
)

// ChainIssue is one irregularity found while verifying a directory.
type ChainIssue struct {
	// Kind classifies the irregularity.
	Kind ChainIssueKind `json:"kind"`
	// Segment is the file the issue was found in, when applicable.
	Segment string `json:"segment,omitempty"`
	// Detail explains the irregularity in human-readable form.
	Detail string `json:"detail,omitempty"`
}

// PolicyOutcome is the decision a policy level reached, ready to be turned into
// an exit code by the CLI.
type PolicyOutcome struct {
	// Level is the policy level that produced this outcome.
	Level PolicyLevel `json:"level"`
	// Fatal is true when the command must exit non-zero.
	Fatal bool `json:"fatal"`
	// Issues are every irregularity found, in detection order.
	Issues []ChainIssue `json:"issues"`
	// Reason summarises why the outcome is fatal or not.
	Reason string `json:"reason"`
}

// PolicyEnforcer applies a policy level to a chain verification result.
type PolicyEnforcer struct {
	level PolicyLevel
}

// NewPolicyEnforcer creates an enforcer for the given level. An unrecognised
// level falls back to DefaultPolicyLevel, because a typo in a config file
// should not silently downgrade an audit to advisory-only.
func NewPolicyEnforcer(level PolicyLevel) *PolicyEnforcer {
	if !IsValidPolicyLevel(level) {
		level = DefaultPolicyLevel
	}
	return &PolicyEnforcer{level: level}
}

// Level returns the enforced policy level.
func (e *PolicyEnforcer) Level() PolicyLevel {
	if e == nil || e.level == "" {
		return DefaultPolicyLevel
	}
	return e.level
}

// isSignatureFailure reports whether an issue is a cryptographic failure,
// which is fatal at every policy level.
func isSignatureFailure(issue ChainIssue) bool {
	return issue.Kind == IssueSignature
}

// Enforce decides whether the given issues are fatal at this policy level.
func (e *PolicyEnforcer) Enforce(issues []ChainIssue) PolicyOutcome {
	level := e.Level()
	outcome := PolicyOutcome{Level: level, Issues: issues}

	signatureFailures := 0
	otherIssues := 0
	for _, issue := range issues {
		if isSignatureFailure(issue) {
			signatureFailures++
		} else {
			otherIssues++
		}
	}

	switch level {
	case PolicyStrict:
		if len(issues) == 0 {
			outcome.Reason = "no issues found"
			return outcome
		}
		outcome.Fatal = true
		outcome.Reason = "strict policy: any chain irregularity is fatal"
		return outcome

	case PolicyWarn:
		if signatureFailures > 0 {
			outcome.Fatal = true
			outcome.Reason = "signature failure is fatal at warn policy"
			return outcome
		}
		outcome.Reason = "warn policy: chain issues reported without failing"
		return outcome

	case PolicyPermissive:
		if signatureFailures > 0 {
			outcome.Fatal = true
			outcome.Reason = "signature failure is fatal at permissive policy"
			return outcome
		}
		outcome.Reason = "permissive policy: only signature failures are fatal"
		return outcome

	default:
		outcome.Fatal = len(issues) > 0
		outcome.Reason = "unknown policy level, treated as strict"
		return outcome
	}
}

// EnforceResult applies the policy to a directory verification result and
// returns the outcome alongside the issues extracted from that result. This is
// the entry point used by `glassbox audit:verify --dir` and
// `glassbox audit:verify-dir`.
func (e *PolicyEnforcer) EnforceResult(result *DirPolicyResult) PolicyOutcome {
	issues := IssuesFromDirPolicyResult(result)
	return e.Enforce(issues)
}

// IssuesFromDirPolicyResult extracts the irregularities recorded by
// VerifyDirectoryWithPolicy, in a deterministic order: chain break first, then
// policy violations, then structural issues. It is the adapter between the
// existing directory verification and the policy levels.
func IssuesFromDirPolicyResult(result *DirPolicyResult) []ChainIssue {
	if result == nil {
		return nil
	}

	issues := make([]ChainIssue, 0, 3)
	if !result.ChainValid {
		issues = append(issues, ChainIssue{
			Kind:   IssueBrokenChainLink,
			Detail: "hash chain is not unbroken from the oldest to the newest segment",
		})
	}
	if result.PolicyViolations > 0 {
		issues = append(issues, ChainIssue{
			Kind:   IssuePolicy,
			Detail: pluralIssues(result.PolicyViolations, "policy violation"),
		})
	}
	if result.StructuralIssues > 0 {
		issues = append(issues, ChainIssue{
			Kind:   IssueStructural,
			Detail: pluralIssues(result.StructuralIssues, "structural issue"),
		})
	}

	return issues
}

// pluralIssues renders a count and noun, e.g. "3 structural issues".
func pluralIssues(count int, noun string) string {
	if count == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(count) + " " + noun + "s"
}
