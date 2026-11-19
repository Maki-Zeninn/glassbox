// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cache

// eviction_test.go covers the scored eviction strategy, including the edge
// cases called out in the issue: a homogeneous candidate set, a set where
// every entry is the same size, and a target larger than the whole cache.

import (
	"testing"
	"time"
)

var evictionTestNow = time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)

func newTestScorer() *EvictionScorer {
	return NewEvictionScorer(DefaultEvictionConfig()).WithClock(evictionTestNow)
}

func TestParseEntryType(t *testing.T) {
	cases := map[string]EntryType{
		"envelope":     EntryTypeEnvelope,
		"  LEDGER_ENTRY": EntryTypeLedgerEntry,
		"footprint":    EntryTypeFootprint,
		"wasm_code":    EntryTypeWASMCode,
		"unknown-kind": EntryTypeOther,
		"":             EntryTypeOther,
	}

	for input, want := range cases {
		if got := ParseEntryType(input); got != want {
			t.Errorf("ParseEntryType(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestDefaultEvictionWeightsSumToOne(t *testing.T) {
	w := DefaultEvictionWeights()
	sum := w.TypeWeight + w.FrequencyWeight + w.SizeWeight + w.NetworkWeight + w.IdleWeight
	if sum < 0.999 || sum > 1.001 {
		t.Fatalf("default weights sum to %v, want ~1 so scores stay comparable", sum)
	}
}

func TestScorerOrdersDisposableEntriesBeforeReusableOnes(t *testing.T) {
	scorer := newTestScorer()
	entries := []CacheEntry{
		{Key: "envelope", Type: EntryTypeEnvelope, SizeBytes: 100, AccessCount: 50, LastAccess: evictionTestNow, Network: "mainnet"},
		{Key: "ledger", Type: EntryTypeLedgerEntry, SizeBytes: 100, AccessCount: 50, LastAccess: evictionTestNow, Network: "mainnet"},
	}

	scored := scorer.ScoreAll(entries)
	if len(scored) != 2 {
		t.Fatalf("expected 2 scored entries, got %d", len(scored))
	}
	if scored[0].Entry.Key != "ledger" {
		t.Errorf("expected the ledger entry to score highest, got %q first", scored[0].Entry.Key)
	}
	if scored[0].Score <= scored[1].Score {
		t.Errorf("expected distinct scores, got %v and %v", scored[0].Score, scored[1].Score)
	}
}

func TestScorerScoresAllFourFactors(t *testing.T) {
	scorer := newTestScorer()
	scored := scorer.Score(scorer.ScoreAll([]CacheEntry{
		{Key: "a", Type: EntryTypeEnvelope, SizeBytes: 1000, AccessCount: 0, LastAccess: evictionTestNow, Network: "testnet"},
		{Key: "b", Type: EntryTypeLedgerEntry, SizeBytes: 1000, AccessCount: 0, LastAccess: evictionTestNow, Network: "testnet"},
	})[1].Entry)

	for _, factor := range []string{"type", "frequency", "size", "network", "idle"} {
		if _, ok := scored.Factors[factor]; !ok {
			t.Errorf("expected factor %q in %v", factor, scored.Factors)
		}
	}

	if scored.Factors["type"] <= 0 || scored.Factors["type"] >= 1 {
		t.Errorf("type factor should be strictly between 0 and 1, got %v", scored.Factors["type"])
	}
}

func TestScorerNormalisesFactorsIntoUnitRange(t *testing.T) {
	scorer := newTestScorer()
	entries := []CacheEntry{
		{Key: "small", Type: EntryTypeOther, SizeBytes: 1, AccessCount: 10, LastAccess: evictionTestNow, Network: "mainnet"},
		{Key: "large", Type: EntryTypeOther, SizeBytes: 100, AccessCount: 10, LastAccess: evictionTestNow, Network: "mainnet"},
	}

	for _, scored := range scorer.ScoreAll(entries) {
		if scored.Score < 0 || scored.Score > 1 {
			t.Errorf("score for %q is %v, want within [0,1]", scored.Entry.Key, scored.Score)
		}
		for name, value := range scored.Factors {
			if value < 0 || value > 1 {
				t.Errorf("factor %q for %q is %v, want within [0,1]", name, scored.Entry.Key, value)
			}
		}
	}
}

func TestScorerGivesEqualScoresToIdenticalEntries(t *testing.T) {
	scorer := newTestScorer()
	entries := []CacheEntry{
		{Key: "a", Type: EntryTypeEnvelope, SizeBytes: 500, AccessCount: 3, LastAccess: evictionTestNow, Network: "testnet"},
		{Key: "b", Type: EntryTypeEnvelope, SizeBytes: 500, AccessCount: 3, LastAccess: evictionTestNow, Network: "testnet"},
		{Key: "c", Type: EntryTypeEnvelope, SizeBytes: 500, AccessCount: 3, LastAccess: evictionTestNow, Network: "testnet"},
	}

	scored := scorer.ScoreAll(entries)
	if scored[0].Score != scored[1].Score || scored[1].Score != scored[2].Score {
		t.Fatalf("identical entries should score identically, got %v, %v, %v",
			scored[0].Score, scored[1].Score, scored[2].Score)
	}
	// Ties must still be deterministic, broken by key.
	keys := []string{scored[0].Entry.Key, scored[1].Entry.Key, scored[2].Entry.Key}
	want := []string{"a", "b", "c"}
	for i := range keys {
		if keys[i] != want[i] {
			t.Fatalf("tie-break order = %v, want %v", keys, want)
		}
	}
}

func TestScorerHandlesEveryEntryAtMaxSize(t *testing.T) {
	scorer := newTestScorer()
	entries := []CacheEntry{
		{Key: "a", Type: EntryTypeOther, SizeBytes: 4096, AccessCount: 0, LastAccess: evictionTestNow, Network: "testnet"},
		{Key: "b", Type: EntryTypeOther, SizeBytes: 4096, AccessCount: 0, LastAccess: evictionTestNow, Network: "testnet"},
	}

	for _, scored := range scorer.ScoreAll(entries) {
		if scored.Factors["size"] != 1 {
			t.Errorf("size factor for a max-size entry = %v, want 1", scored.Factors["size"])
		}
	}
}

func TestScorerPreferentiallyEvictsTestnet(t *testing.T) {
	scorer := newTestScorer()
	entries := []CacheEntry{
		{Key: "main", Type: EntryTypeEnvelope, SizeBytes: 100, AccessCount: 5, LastAccess: evictionTestNow, Network: "mainnet"},
		{Key: "test", Type: EntryTypeEnvelope, SizeBytes: 100, AccessCount: 5, LastAccess: evictionTestNow, Network: "testnet"},
	}

	scored := scorer.ScoreAll(entries)
	if scored[0].Entry.Key != "test" {
		t.Errorf("expected the testnet entry to be evicted first, got %q", scored[0].Entry.Key)
	}
}

func TestScorerPrefersFrequentlyAccessedEntries(t *testing.T) {
	scorer := newTestScorer()
	entries := []CacheEntry{
		{Key: "hot", Type: EntryTypeEnvelope, SizeBytes: 100, AccessCount: 100, LastAccess: evictionTestNow, Network: "mainnet"},
		{Key: "cold", Type: EntryTypeEnvelope, SizeBytes: 100, AccessCount: 0, LastAccess: evictionTestNow, Network: "mainnet"},
	}

	scored := scorer.ScoreAll(entries)
	if scored[0].Entry.Key != "cold" {
		t.Errorf("expected the never-read entry to be evicted first, got %q", scored[0].Entry.Key)
	}
}

func TestScorerTreatsNeverAccessedEntryAsIdle(t *testing.T) {
	scorer := newTestScorer()
	withAccess := scorer.Score(CacheEntry{Key: "a", LastAccess: evictionTestNow})
	withoutAccess := scorer.Score(CacheEntry{Key: "b"})

	if withoutAccess.Factors["idle"] != 1 {
		t.Errorf("idle factor for a never-accessed entry = %v, want 1", withoutAccess.Factors["idle"])
	}
	if withAccess.Factors["idle"] != 0 {
		t.Errorf("idle factor for a just-accessed entry = %v, want 0", withAccess.Factors["idle"])
	}
}

func TestSelectEvictionCandidatesStopsAtTheTarget(t *testing.T) {
	entries := []CacheEntry{
		{Key: "a", Type: EntryTypeLedgerEntry, SizeBytes: 100},
		{Key: "b", Type: EntryTypeLedgerEntry, SizeBytes: 100},
		{Key: "c", Type: EntryTypeLedgerEntry, SizeBytes: 100},
	}

	candidates := SelectEvictionCandidatesWithScorer(entries, 150, newTestScorer())
	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates to free 150 bytes from 100-byte entries, got %d", len(candidates))
	}
	if got := TotalSizeBytes(candidates); got != 200 {
		t.Errorf("candidates free %d bytes, want at least the 150 byte target", got)
	}
}

func TestSelectEvictionCandidatesReturnsAllWhenTargetExceedsCacheSize(t *testing.T) {
	entries := []CacheEntry{
		{Key: "a", Type: EntryTypeLedgerEntry, SizeBytes: 10},
		{Key: "b", Type: EntryTypeLedgerEntry, SizeBytes: 10},
	}

	candidates := SelectEvictionCandidatesWithScorer(entries, 1_000_000, newTestScorer())
	if len(candidates) != len(entries) {
		t.Fatalf("expected every entry when the target exceeds the cache, got %d of %d", len(candidates), len(entries))
	}
}

func TestSelectEvictionCandidatesReturnsNothingForANonPositiveTarget(t *testing.T) {
	entries := []CacheEntry{{Key: "a", SizeBytes: 10}}
	if got := SelectEvictionCandidatesWithScorer(entries, 0, newTestScorer()); len(got) != 0 {
		t.Errorf("expected no candidates for a zero target, got %d", len(got))
	}
	if got := SelectEvictionCandidatesWithScorer(entries, -5, newTestScorer()); len(got) != 0 {
		t.Errorf("expected no candidates for a negative target, got %d", len(got))
	}
}

func TestSelectEvictionCandidatesHandlesEmptyInput(t *testing.T) {
	if got := SelectEvictionCandidatesWithScorer(nil, 100, newTestScorer()); len(got) != 0 {
		t.Errorf("expected no candidates for an empty entry set, got %d", len(got))
	}
}

func TestSelectEvictionCandidatesOrdersByScore(t *testing.T) {
	entries := []CacheEntry{
		{Key: "keep", Type: EntryTypeEnvelope, SizeBytes: 100, AccessCount: 50, LastAccess: evictionTestNow, Network: "mainnet"},
		{Key: "drop", Type: EntryTypeLedgerEntry, SizeBytes: 100, AccessCount: 0, LastAccess: time.Time{}, Network: "testnet"},
	}

	candidates := SelectEvictionCandidatesWithScorer(entries, 50, newTestScorer())
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	if candidates[0].Key != "drop" {
		t.Errorf("expected the low-value testnet entry to be selected, got %q", candidates[0].Key)
	}
}

func TestTotalSizeBytesIgnoresNegativeSizes(t *testing.T) {
	entries := []CacheEntry{{SizeBytes: 100}, {SizeBytes: -5}, {SizeBytes: 50}}
	if got := TotalSizeBytes(entries); got != 150 {
		t.Errorf("TotalSizeBytes = %d, want 150", got)
	}
}

func TestNewEvictionScorerFillsInMissingConfig(t *testing.T) {
	scorer := NewEvictionScorer(EvictionConfig{}).WithClock(evictionTestNow)
	if scorer.Config().Weights != DefaultEvictionWeights() {
		t.Errorf("expected default weights to be filled in, got %+v", scorer.Config().Weights)
	}
	if scorer.Config().IdleAfter == 0 {
		t.Error("expected a non-zero default IdleAfter")
	}
}
