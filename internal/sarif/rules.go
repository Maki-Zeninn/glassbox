// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package sarif

import "sort"

// This file defines the two diagnostic rule families that are not derived from
// `security.Finding` objects: GB1xxx for source map resolution failures and
// GB2xxx for security warnings about host function usage.
//
// Findings emitted by the security detector keep the existing derived
// GB-XXXX rule IDs (`ruleIDForFinding`). The rules below are *enumerated*
// instead of derived, because each one corresponds to a distinct remediation
// the developer has to perform, and because CI code-scanning needs stable IDs
// across runs to track a rule's history.

const (
	// RuleSourceMapOffsetNotFound reports a WASM instruction offset with no
	// entry in the DWARF line table.
	RuleSourceMapOffsetNotFound = "GB1001"
	// RuleSourceMapDwarfMissing reports a WASM binary with no usable debug
	// sections at all, which makes every subsequent mapping impossible.
	RuleSourceMapDwarfMissing = "GB1002"
	// RuleSourceMapNoGitRepository reports that source links could not be
	// generated because no Git repository was detected in the workspace.
	RuleSourceMapNoGitRepository = "GB1003"
	// RuleDeprecatedHostFunction reports a call to a Soroban host function
	// that is deprecated and slated for removal.
	RuleDeprecatedHostFunction = "GB2001"
	// RuleAllowlistedUnsafeHostFunction reports a call to a host function that
	// is still permitted but is known to be unsafe for the detected contract.
	RuleAllowlistedUnsafeHostFunction = "GB2002"
)

// SARIF levels used by the diagnostic rule families.
const (
	LevelError   = "error"
	LevelWarning = "warning"
	LevelNote    = "note"
)

// SourceMapFailure describes a single source map resolution failure.
//
// It is produced by internal/sourcemap during address resolution and carries
// enough context for a developer to fix the underlying build problem.
type SourceMapFailure struct {
	// RuleID is one of the GB1xxx constants in this file.
	RuleID string
	// Offset is the WASM instruction offset that could not be mapped.
	Offset uint64
	// Reason is a human-readable explanation, e.g. "no DWARF line table entry".
	Reason string
	// Fix is an optional remediation suggestion, e.g. "build with debug=true".
	Fix string
	// Function is the WASM function the offset belongs to, when known.
	Function string
}

// SecurityWarning describes a host-function security concern.
//
// It is produced by internal/security while scanning the host calls a
// contract makes and, unlike security.Finding, always carries a precise
// location: either a mapped Rust source line or the raw WASM offset.
type SecurityWarning struct {
	// RuleID is one of the GB2xxx constants in this file.
	RuleID string
	// Title is a short human-readable summary, e.g. the function name.
	Title string
	// Description explains the risk and what to do about it.
	Description string
	// File is the mapped Rust source file, empty when mapping failed.
	File string
	// Line is the 1-based source line, 0 when unknown.
	Line int
	// WasmOffset is the instruction offset, always populated.
	WasmOffset uint64
	// Function is the host function that triggered the warning.
	Function string
	// ExternalURL is a web-accessible permalink for File, populated by the
	// sourcemap pipeline when the file is a registry crate (docs.rs) or a
	// git dependency (GitHub commit link).  When non-empty it is used as
	// artifactLocation.uri in the SARIF output so reviewers get a clickable
	// link rather than a machine-local path.
	ExternalURL string
}

// SourceMapRules returns the GB1xxx rules, ordered by rule ID.
func SourceMapRules() []Rule {
	return []Rule{
		{
			ID:               RuleSourceMapOffsetNotFound,
			Name:             "SourceMapOffsetNotFound",
			ShortDescription: Message{Text: "WASM instruction offset not found in DWARF debug information"},
			FullDescription: &Message{
				Text: "A WASM instruction offset could not be mapped to a Rust source line. " +
					"The contract binary was most likely built without debug information, or the " +
					"offset falls outside the compiled line table. Simulation failures at this offset " +
					"cannot be attributed to a source line.",
			},
			HelpURI:       "https://github.com/dotandev/glassbox/blob/main/docs/sarif-export.md#source-map-diagnostics-gb1xxx",
			DefaultConfig: &RuleConfig{Level: LevelWarning},
			Properties: &RuleProperties{
				Tags:     []string{"source-map", "dwarf", "debuggability"},
				Precision: "medium",
			},
		},
		{
			ID:               RuleSourceMapDwarfMissing,
			Name:             "SourceMapDwarfMissing",
			ShortDescription: Message{Text: "DWARF debug sections are missing from the WASM binary"},
			FullDescription: &Message{
				Text: "The WASM binary contains no usable DWARF sections, so no instruction offset " +
					"can be mapped to a source line. Build the contract with the debug profile " +
					"(debug = true) and re-run the simulation to get source-mapped failures.",
			},
			HelpURI:       "https://github.com/dotandev/glassbox/blob/main/docs/sarif-export.md#source-map-diagnostics-gb1xxx",
			DefaultConfig: &RuleConfig{Level: LevelError},
			Properties: &RuleProperties{
				Tags:     []string{"source-map", "dwarf", "build"},
				Precision: "high",
			},
		},
		{
			ID:               RuleSourceMapNoGitRepository,
			Name:             "SourceMapNoGitRepository",
			ShortDescription: Message{Text: "Git repository not detected, so no source links were generated"},
			FullDescription: &Message{
				Text: "Glassbox could not detect a Git repository in the workspace, so mapped " +
					"locations cannot be turned into permalinks to the source. Initialise a Git " +
					"repository, or configure external_source_repos, to get reviewable source links.",
			},
			HelpURI:       "https://github.com/dotandev/glassbox/blob/main/docs/sarif-export.md#source-map-diagnostics-gb1xxx",
			DefaultConfig: &RuleConfig{Level: LevelNote},
			Properties: &RuleProperties{
				Tags:     []string{"source-map", "git", "configuration"},
				Precision: "high",
			},
		},
	}
}

// SecurityWarningRules returns the GB2xxx rules, ordered by rule ID.
func SecurityWarningRules() []Rule {
	return []Rule{
		{
			ID:               RuleDeprecatedHostFunction,
			Name:             "DeprecatedHostFunction",
			ShortDescription: Message{Text: "Deprecated host function call detected"},
			FullDescription: &Message{
				Text: "The contract calls a Soroban host function that is deprecated and will be " +
					"removed in a future protocol release. Migrate to the supported replacement " +
					"before the host function is deleted.",
			},
			HelpURI:       "https://github.com/dotandev/glassbox/blob/main/docs/sarif-export.md#security-warnings-gb2xxx",
			DefaultConfig: &RuleConfig{Level: LevelWarning},
			Properties: &RuleProperties{
				Tags:     []string{"security", "host-function", "deprecation"},
				Precision: "high",
			},
		},
		{
			ID:               RuleAllowlistedUnsafeHostFunction,
			Name:             "AllowlistedUnsafeHostFunction",
			ShortDescription: Message{Text: "Host function is allowlisted but unsafe for this contract"},
			FullDescription: &Message{
				Text: "The contract calls a host function that Glassbox still permits, but whose " +
					"use is unsafe in this context. Add an authorization check, narrow the " +
					"arguments, or remove the call.",
			},
			HelpURI:       "https://github.com/dotandev/glassbox/blob/main/docs/sarif-export.md#security-warnings-gb2xxx",
			DefaultConfig: &RuleConfig{Level: LevelError},
			Properties: &RuleProperties{
				Tags:     []string{"security", "host-function", "allowlist"},
				Precision: "medium",
			},
		},
	}
}

// DiagnosticRules returns every enumerated diagnostic rule, ordered by rule ID.
func DiagnosticRules() []Rule {
	rules := append(SourceMapRules(), SecurityWarningRules()...)
	sort.Slice(rules, func(i, j int) bool { return rules[i].ID < rules[j].ID })
	return rules
}

// RuleByID looks up an enumerated diagnostic rule.
func RuleByID(id string) (Rule, bool) {
	for _, rule := range DiagnosticRules() {
		if rule.ID == id {
			return rule, true
		}
	}
	return Rule{}, false
}

// isDiagnosticRuleID reports whether id belongs to the GB1xxx/GB2xxx families.
func isDiagnosticRuleID(id string) bool {
	_, ok := RuleByID(id)
	return ok
}
