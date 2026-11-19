// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package heuristic

import (
"crypto/sha256"
"encoding/hex"
"fmt"
"sort"
"strings"
)

// RuleSetVersion identifies a versioned snapshot of a rule set.
// It is embedded in findings and audit metadata so that reports can be
// reproduced and compared across tool versions.
type RuleSetVersion struct {
// Name is the logical name of the rule set (e.g. "builtin", "custom").
Name string `json:"name"`
// Version is the semantic version declared in the rule file.
Version string `json:"version"`
// ContentHash is a deterministic SHA-256 digest over the ordered rule IDs
// and patterns. It changes whenever any rule is added, removed, or modified.
ContentHash string `json:"content_hash"`
}

// String returns a short human-readable representation.
func (v RuleSetVersion) String() string {
hash := v.ContentHash
if len(hash) > 8 {
hash = hash[:8]
}
if v.Version != "" {
return fmt.Sprintf("%s@%s (%s)", v.Name, v.Version, hash)
}
return fmt.Sprintf("%s (%s)", v.Name, hash)
}

// BuiltinRuleSetName is the canonical name used for the embedded rule set.
const BuiltinRuleSetName = "builtin"

// BuiltinRuleSetVersion is the semantic version of the embedded rule set.
// Bump this whenever builtin_rules.json changes.
const BuiltinRuleSetVersion = "1.0.0"

// ComputeRuleSetVersion derives a deterministic ContentHash from the rules in
// rs and returns a RuleSetVersion with the given name and version string.
func ComputeRuleSetVersion(rs *RuleSet, name, version string) RuleSetVersion {
rules := rs.Rules()

// Collect and sort rule IDs for a stable digest regardless of load order.
ids := make([]string, 0, len(rules))
ruleMap := make(map[string]*Rule, len(rules))
for _, r := range rules {
ids = append(ids, r.ID)
ruleMap[r.ID] = r
}
sort.Strings(ids)

// Canonical form: "id|pattern1,pattern2,...\n" per rule.
var sb strings.Builder
for _, id := range ids {
r := ruleMap[id]
ps := make([]string, len(r.Patterns))
copy(ps, r.Patterns)
sort.Strings(ps)
sb.WriteString(id)
sb.WriteByte('|')
sb.WriteString(strings.Join(ps, ","))
sb.WriteByte('\n')
}

sum := sha256.Sum256([]byte(sb.String()))
return RuleSetVersion{
Name:        name,
Version:     version,
ContentHash: hex.EncodeToString(sum[:]),
}
}

// BuiltinVersion returns the RuleSetVersion for the package-level built-in engine.
func BuiltinVersion() RuleSetVersion {
return ComputeRuleSetVersion(defaultEngine.RuleSet(), BuiltinRuleSetName, BuiltinRuleSetVersion)
}

// VersionedResult wraps an engine evaluation with its rule-set version and
// the ID of the rule that fired, enabling stable report comparison.
type VersionedResult struct {
// RuleID is the ID of the rule that produced this result, or empty for the
// generic fallback.
RuleID string `json:"rule_id,omitempty"`
// RuleVersion is the version of the rule set the rule came from.
RuleVersion RuleSetVersion `json:"rule_version"`
// Suggestion is the rendered suggestion text.
Suggestion string `json:"suggestion"`
}

// EvaluateVersioned is like Engine.Evaluate but also returns the matching rule
// ID and rule-set version so callers can record full provenance.
func (e *Engine) EvaluateVersioned(in Input) VersionedResult {
combined := strings.Join(append(in.Events, in.Logs...), " ") + " " + in.Error

matchedID := ""
for _, rule := range e.rs.Rules() {
if !ruleApplies(rule, in, combined) {
continue
}
matchedID = rule.ID
break
}

suggestion := e.Evaluate(in)
ver := ComputeRuleSetVersion(e.rs, BuiltinRuleSetName, BuiltinRuleSetVersion)

return VersionedResult{
RuleID:      matchedID,
RuleVersion: ver,
Suggestion:  suggestion,
}
}

// RuleVersionStatus describes the compatibility of a saved version versus the
// current engine.
type RuleVersionStatus string

const (
// RuleVersionCompatible means the ContentHash matches — results are reproducible.
RuleVersionCompatible RuleVersionStatus = "compatible"
// RuleVersionMismatch means the hash differs — rules have changed.
RuleVersionMismatch RuleVersionStatus = "mismatch"
// RuleVersionUnknown means the supplied version carries no usable hash.
RuleVersionUnknown RuleVersionStatus = "unknown"
)

// unknownHash is the sentinel content hash for an unversioned rule set.
var unknownHash = strings.Repeat("0", 64)

// CheckRuleVersion compares a saved RuleSetVersion against the engine's
// current rule set. Unknown or zero hashes are reported rather than silently
// treated as compatible.
func CheckRuleVersion(e *Engine, saved RuleSetVersion, name, version string) RuleVersionStatus {
if saved.ContentHash == "" || saved.ContentHash == unknownHash {
return RuleVersionUnknown
}
current := ComputeRuleSetVersion(e.RuleSet(), name, version)
if current.ContentHash == saved.ContentHash {
return RuleVersionCompatible
}
return RuleVersionMismatch
}