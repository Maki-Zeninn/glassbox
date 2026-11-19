// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package heuristic

import (
"strings"
"testing"
)

func TestComputeRuleSetVersion_Deterministic(t *testing.T) {
v1 := BuiltinVersion()
v2 := BuiltinVersion()
if v1.ContentHash != v2.ContentHash {
t.Errorf("BuiltinVersion not deterministic: %s vs %s", v1.ContentHash, v2.ContentHash)
}
if v1.Name != BuiltinRuleSetName {
t.Errorf("expected name %q, got %q", BuiltinRuleSetName, v1.Name)
}
if v1.Version != BuiltinRuleSetVersion {
t.Errorf("expected version %q, got %q", BuiltinRuleSetVersion, v1.Version)
}
}

func TestComputeRuleSetVersion_ChangesOnRuleChange(t *testing.T) {
dir := t.TempDir()
rulesA := `{"version":"1","rules":[{"id":"r1","priority":1,"patterns":["foo"],"template":"a"}]}`
rulesB := `{"version":"1","rules":[{"id":"r1","priority":1,"patterns":["bar"],"template":"a"}]}`

pathA := writeRuleFile(t, dir, "a.json", rulesA)
pathB := writeRuleFile(t, dir, "b.json", rulesB)

rsA, err := LoadRulesFromFiles([]string{pathA})
if err != nil {
t.Fatal(err)
}
rsB, err := LoadRulesFromFiles([]string{pathB})
if err != nil {
t.Fatal(err)
}

vA := ComputeRuleSetVersion(rsA, "test", "1.0.0")
vB := ComputeRuleSetVersion(rsB, "test", "1.0.0")

if vA.ContentHash == vB.ContentHash {
t.Error("expected different ContentHash after pattern change")
}
}

func TestComputeRuleSetVersion_StableAcrossLoadOrder(t *testing.T) {
// Two files with the same rules in different order should hash identically.
dir := t.TempDir()
file1 := `{"version":"1","rules":[
{"id":"alpha","priority":10,"patterns":["alpha"],"template":"a"},
{"id":"beta","priority":20,"patterns":["beta"],"template":"b"}
]}`
file2 := `{"version":"1","rules":[
{"id":"beta","priority":5,"patterns":["beta"],"template":"b"},
{"id":"alpha","priority":15,"patterns":["alpha"],"template":"a"}
]}`

p1 := writeRuleFile(t, dir, "f1.json", file1)
p2 := writeRuleFile(t, dir, "f2.json", file2)

rs1, err := LoadRulesFromFiles([]string{p1})
if err != nil {
t.Fatal(err)
}
rs2, err := LoadRulesFromFiles([]string{p2})
if err != nil {
t.Fatal(err)
}

v1 := ComputeRuleSetVersion(rs1, "test", "1.0.0")
v2 := ComputeRuleSetVersion(rs2, "test", "1.0.0")

if v1.ContentHash != v2.ContentHash {
t.Errorf("expected same ContentHash regardless of load order\n  got %s\n  and %s", v1.ContentHash, v2.ContentHash)
}
}

func TestRuleSetVersionString(t *testing.T) {
v := RuleSetVersion{Name: "builtin", Version: "1.0.0", ContentHash: "abcdef1234567890"}
s := v.String()
if !strings.Contains(s, "builtin") {
t.Errorf("String() missing name: %s", s)
}
if !strings.Contains(s, "1.0.0") {
t.Errorf("String() missing version: %s", s)
}
if !strings.Contains(s, "abcdef12") {
t.Errorf("String() missing hash prefix: %s", s)
}
}

func TestEvaluateVersioned_RecordsRuleID(t *testing.T) {
in := Input{
TxHash:  "aaaaaa000000bbbbbb",
Network: "testnet",
Status:  "error",
Error:   "Error(Budget, CpuLimitExceeded)",
BudgetUsage: nil,
}
result := defaultEngine.EvaluateVersioned(in)
if result.RuleID == "" {
t.Error("EvaluateVersioned should record which rule fired")
}
if result.RuleVersion.ContentHash == "" {
t.Error("EvaluateVersioned should record a non-empty ContentHash")
}
if result.Suggestion == "" {
t.Error("EvaluateVersioned should include the suggestion text")
}
}

func TestEvaluateVersioned_Fallback_EmptyRuleID(t *testing.T) {
in := Input{
TxHash:  "aaaaaa000000bbbbbb",
Network: "mainnet",
Status:  "error",
Error:   "completely unknown error zzzzzzzz",
}
result := defaultEngine.EvaluateVersioned(in)
// Fallback path produces no matched rule ID.
if result.RuleID != "" {
t.Errorf("expected empty RuleID for fallback, got %q", result.RuleID)
}
if result.RuleVersion.ContentHash == "" {
t.Error("RuleVersion should still be populated even for fallback")
}
}

func TestCheckRuleVersion_Compatible(t *testing.T) {
ver := BuiltinVersion()
status := CheckRuleVersion(defaultEngine, ver, BuiltinRuleSetName, BuiltinRuleSetVersion)
if status != RuleVersionCompatible {
t.Errorf("expected compatible, got %s", status)
}
}

func TestCheckRuleVersion_Mismatch(t *testing.T) {
stale := RuleSetVersion{
Name:        BuiltinRuleSetName,
Version:     "0.9.0",
ContentHash: strings.Repeat("a", 64),
}
status := CheckRuleVersion(defaultEngine, stale, BuiltinRuleSetName, BuiltinRuleSetVersion)
if status != RuleVersionMismatch {
t.Errorf("expected mismatch, got %s", status)
}
}

func TestCheckRuleVersion_Unknown(t *testing.T) {
zero := RuleSetVersion{Name: "builtin", Version: "1.0.0", ContentHash: ""}
status := CheckRuleVersion(defaultEngine, zero, BuiltinRuleSetName, BuiltinRuleSetVersion)
if status != RuleVersionUnknown {
t.Errorf("expected unknown, got %s", status)
}
}