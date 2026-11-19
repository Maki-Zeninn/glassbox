// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cache

// eviction.go implements the scored eviction strategy behind
// `glassbox cache clean --strategy scored`.
//
// Plain LRU evicts by last-access time alone, which treats every entry as
// equally valuable. In practice that is wrong in both directions: a
// TransactionEnvelope fetched while debugging a failing transaction is
// re-examined constantly and is worth keeping, while a speculative ledger
// entry snapshot that is never read again is pure overhead. The scorer
// combines four factors so the cache sheds the entries that cost the most disk
// and return the least value.
//
// Scoring formula
//
//	score = w_type     * typeFactor
//	      + w_freq     * frequencyFactor
//	      + w_size     * sizeFactor
//	      + w_network  * networkFactor
//	      + w_idle     * idleFactor
//
// Every factor is normalised to [0, 1] where 1 means "evict first":
//
//	typeFactor      1 for disposable entries (ledger_entry, other), lower for
//	                high-value entries (envelope, footprint, wasm_code).
//	frequencyFactor 1 - normalised access count, so a never-read entry scores 1.
//	sizeFactor      normalised entry size, so a large entry costs more to keep.
//	networkFactor   1 for testnet, 0 for mainnet, so testnet is shed first.
//	idleFactor      normalised idle time since last access.
//
// The weights live in [cache.eviction] in the cache config (see
// docs/cache-eviction.md) and default to the values in DefaultEvictionWeights.

import (
	"sort"
	"strings"
	"time"
)

// EntryType classifies a cache entry for the purposes of eviction.
type EntryType string

// Recognised entry types. Unknown values are treated as EntryTypeOther.
const (
	// EntryTypeEnvelope is a cached transaction envelope. High value: it is
	// re-examined repeatedly while debugging a failing transaction.
	EntryTypeEnvelope EntryType = "envelope"
	// EntryTypeFootprint is a cached ledger footprint. High value, and cheap
	// to keep because it is small.
	EntryTypeFootprint EntryType = "footprint"
	// EntryTypeLedgerEntry is a single cached ledger key/value. Low value
	// unless it took part in a recent simulation.
	EntryTypeLedgerEntry EntryType = "ledger_entry"
	// EntryTypeWASMCode is a cached WASM binary. Reusable across
	// transactions, so worth keeping, but large.
	EntryTypeWASMCode EntryType = "wasm_code"
	// EntryTypeOther is anything unrecognised.
	EntryTypeOther EntryType = "other"
)

// ParseEntryType normalises a stored entry type string. Unknown values become
// EntryTypeOther so a new type written by a newer version cannot break
// eviction scoring.
func ParseEntryType(value string) EntryType {
	switch EntryType(strings.ToLower(strings.TrimSpace(value))) {
	case EntryTypeEnvelope:
		return EntryTypeEnvelope
	case EntryTypeFootprint:
		return EntryTypeFootprint
	case EntryTypeLedgerEntry:
		return EntryTypeLedgerEntry
	case EntryTypeWASMCode:
		return EntryTypeWASMCode
	default:
		return EntryTypeOther
	}
}

// typeEvictionFactor returns how readily an entry of the given type should be
// evicted, in [0, 1]. Higher means evict first.
//
// The values encode retention value: envelopes and footprints are kept longest
// because they are reused; individual ledger entries go first because a
// speculative fetch is rarely re-read.
var typeEvictionFactor = map[EntryType]float64{
	EntryTypeLedgerEntry: 1.00,
	EntryTypeOther:       0.90,
	EntryTypeWASMCode:    0.50,
	EntryTypeFootprint:   0.35,
	EntryTypeEnvelope:    0.20,
}

// EvictionWeights are the configurable scoring weights, corresponding to the
// [cache.eviction] section of the cache config.
type EvictionWeights struct {
	// TypeWeight scales the entry type factor.
	TypeWeight float64 `json:"type_weight"`
	// FrequencyWeight scales the access frequency factor.
	FrequencyWeight float64 `json:"frequency_weight"`
	// SizeWeight scales the entry size factor.
	SizeWeight float64 `json:"size_weight"`
	// NetworkWeight scales the network factor.
	NetworkWeight float64 `json:"network_weight"`
	// IdleWeight scales the idle-time factor.
	IdleWeight float64 `json:"idle_weight"`
}

// DefaultEvictionWeights returns the shipped weights. They sum to 1 so scores
// stay in [0, 1] and remain comparable across runs and across strategies.
func DefaultEvictionWeights() EvictionWeights {
	return EvictionWeights{
		TypeWeight:      0.30,
		FrequencyWeight: 0.30,
		SizeWeight:      0.20,
		NetworkWeight:   0.10,
		IdleWeight:      0.10,
	}
}

// EvictionConfig is the [cache.eviction] configuration section.
type EvictionConfig struct {
	// Weights are the scoring weights.
	Weights EvictionWeights `json:"weights"`
	// IdleAfter is how long an entry must be untouched before its idle factor
	// reaches 1. Zero disables the idle factor's contribution.
	IdleAfter time.Duration `json:"idle_after"`
	// HighValueTypes lists entry types that are retained regardless of score
	// until the target cannot be met without evicting one.
	HighValueTypes []EntryType `json:"high_value_types"`
}

// DefaultEvictionConfig returns the shipped eviction configuration.
func DefaultEvictionConfig() EvictionConfig {
	return EvictionConfig{
		Weights:         DefaultEvictionWeights(),
		IdleAfter:       7 * 24 * time.Hour,
		HighValueTypes:  []EntryType{EntryTypeEnvelope, EntryTypeFootprint},
	}
}

// CacheEntry is the metadata the scorer needs for one cache entry.
type CacheEntry struct {
	// Key identifies the entry, usually the file name or digest.
	Key string
	// Path is the on-disk location, empty for in-memory entries.
	Path string
	// Type classifies the entry for the type factor.
	Type EntryType
	// SizeBytes is the entry's on-disk size.
	SizeBytes int64
	// AccessCount is the number of times the entry has been read.
	AccessCount int64
	// LastAccess is when the entry was last read.
	LastAccess time.Time
	// CreatedAt is when the entry was written.
	CreatedAt time.Time
	// Network is the network the entry was fetched from, e.g. "testnet".
	Network string
}

// ScoredEntry pairs a cache entry with its eviction score. Higher scores are
// evicted first.
type ScoredEntry struct {
	Entry CacheEntry
	// Score is the weighted sum of the normalised factors, in [0, 1].
	Score float64
	// Factors holds the per-factor contributions, for --dry-run output and
	// for explaining why an entry was chosen.
	Factors map[string]float64
}

// EvictionScorer computes eviction scores for cache entries.
type EvictionScorer struct {
	config EvictionConfig
	// now is injectable so idle-time factors are deterministic in tests.
	now time.Time
	// maxSizeBytes is the largest single entry observed, used to normalise
	// the size factor across a candidate set.
	maxSizeBytes int64
	// maxAccessCount is the highest access count observed, used to normalise
	// the frequency factor.
	maxAccessCount int64
}

// NewEvictionScorer creates a scorer. A zero config falls back to the defaults.
func NewEvictionScorer(config EvictionConfig) *EvictionScorer {
	if config.Weights == (EvictionWeights{}) {
		config.Weights = DefaultEvictionWeights()
	}
	if config.IdleAfter == 0 {
		config.IdleAfter = DefaultEvictionConfig().IdleAfter
	}
	return &EvictionScorer{config: config, now: time.Now()}
}

// WithClock returns a scorer with a fixed clock, for deterministic tests.
func (s *EvictionScorer) WithClock(now time.Time) *EvictionScorer {
	s.now = now
	return s
}

// Config returns the scorer's configuration.
func (s *EvictionScorer) Config() EvictionConfig {
	return s.config
}

// calibrate records the normalising maxima across the candidate set. Size is
// normalised against the largest entry and frequency against the most-accessed
// entry, so the factors span the full [0, 1] range even for a small or
// homogeneous set.
func (s *EvictionScorer) calibrate(entries []CacheEntry) {
	s.maxSizeBytes = 0
	s.maxAccessCount = 0
	for _, entry := range entries {
		if entry.SizeBytes > s.maxSizeBytes {
			s.maxSizeBytes = entry.SizeBytes
		}
		if entry.AccessCount > s.maxAccessCount {
			s.maxAccessCount = entry.AccessCount
		}
	}
}

// typeFactor returns the eviction factor for an entry type.
func (s *EvictionScorer) typeFactor(entry CacheEntry) float64 {
	if factor, ok := typeEvictionFactor[entry.Type]; ok {
		return factor
	}
	return typeEvictionFactor[EntryTypeOther]
}

// frequencyFactor returns 1 for a never-read entry, falling to 0 for the
// most-accessed entry in the set.
func (s *EvictionScorer) frequencyFactor(entry CacheEntry) float64 {
	if s.maxAccessCount <= 0 {
		return 1
	}
	access := entry.AccessCount
	if access < 0 {
		access = 0
	}
	if access > s.maxAccessCount {
		access = s.maxAccessCount
	}
	return 1 - float64(access)/float64(s.maxAccessCount)
}

// sizeFactor returns the normalised entry size. Large entries cost more disk,
// so they are evicted before small ones at equal access frequency.
func (s *EvictionScorer) sizeFactor(entry CacheEntry) float64 {
	if s.maxSizeBytes <= 0 {
		return 0
	}
	size := entry.SizeBytes
	if size < 0 {
		size = 0
	}
	if size > s.maxSizeBytes {
		size = s.maxSizeBytes
	}
	return float64(size) / float64(s.maxSizeBytes)
}

// networkEvictionFactor returns 1 for testnet entries and 0 for mainnet, so
// testnet data is shed first when the cache is under pressure. Unknown
// networks sit between the two.
func networkEvictionFactor(entry CacheEntry) float64 {
	switch strings.ToLower(strings.TrimSpace(entry.Network)) {
	case "testnet":
		return 1
	case "mainnet":
		return 0
	case "":
		return 0.5
	default:
		return 0.5
	}
}

// idleFactor returns the normalised time since last access. A never-accessed
// entry is treated as fully idle.
func (s *EvictionScorer) idleFactor(entry CacheEntry) float64 {
	if s.config.IdleAfter <= 0 {
		return 0
	}
	if entry.LastAccess.IsZero() {
		return 1
	}
	idle := s.now.Sub(entry.LastAccess)
	if idle <= 0 {
		return 0
	}
	if idle >= s.config.IdleAfter {
		return 1
	}
	return idle.Seconds() / s.config.IdleAfter.Seconds()
}

// Score computes the eviction score for a single entry against the current
// calibration. Call ScoreAll rather than this method directly, so the maxima
// are calibrated across the whole candidate set first.
func (s *EvictionScorer) Score(entry CacheEntry) ScoredEntry {
	factors := map[string]float64{
		"type":      s.typeFactor(entry),
		"frequency": s.frequencyFactor(entry),
		"size":      s.sizeFactor(entry),
		"network":   networkEvictionFactor(entry),
		"idle":      s.idleFactor(entry),
	}

	w := s.config.Weights
	score := w.TypeWeight*factors["type"] +
		w.FrequencyWeight*factors["frequency"] +
		w.SizeWeight*factors["size"] +
		w.NetworkWeight*factors["network"] +
		w.IdleWeight*factors["idle"]

	return ScoredEntry{Entry: entry, Score: score, Factors: factors}
}

// ScoreAll calibrates against the candidate set and returns scores ordered
// highest score first, i.e. most evictable first.
func (s *EvictionScorer) ScoreAll(entries []CacheEntry) []ScoredEntry {
	s.calibrate(entries)

	scored := make([]ScoredEntry, 0, len(entries))
	for _, entry := range entries {
		scored = append(scored, s.Score(entry))
	}

	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].Score != scored[j].Score {
			return scored[i].Score > scored[j].Score
		}
		// Deterministic tie-break so a --dry-run report is stable across runs.
		return scored[i].Entry.Key < scored[j].Entry.Key
	})

	return scored
}

// SelectEvictionCandidates returns the minimum set of entries to evict to free
// at least targetFreeBytes, highest score first.
//
// The selection is greedy: entries are taken in descending score order until the
// accumulated size reaches the target. That is optimal for reaching a size
// target with the fewest entries under a total order, and it degrades
// gracefully when the target cannot be met — in that case every candidate is
// returned, which is exactly what a "free as much as possible" clean needs.
//
// When targetFreeBytes is zero or negative, nothing is selected, because the
// caller's goal is to keep the cache as-is.
func SelectEvictionCandidates(entries []CacheEntry, targetFreeBytes int64) []CacheEntry {
	return SelectEvictionCandidatesWithScorer(entries, targetFreeBytes, NewEvictionScorer(DefaultEvictionConfig()))
}

// SelectEvictionCandidatesWithScorer is SelectEvictionCandidates with an
// explicit scorer, so callers can use configured weights.
func SelectEvictionCandidatesWithScorer(
	entries []CacheEntry,
	targetFreeBytes int64,
	scorer *EvictionScorer,
) []CacheEntry {
	if targetFreeBytes <= 0 || len(entries) == 0 {
		return nil
	}
	if scorer == nil {
		scorer = NewEvictionScorer(DefaultEvictionConfig())
	}

	scored := scorer.ScoreAll(entries)

	candidates := make([]CacheEntry, 0, len(scored))
	var freed int64
	for _, candidate := range scored {
		if freed >= targetFreeBytes {
			break
		}
		candidates = append(candidates, candidate.Entry)
		freed += candidate.Entry.SizeBytes
	}

	return candidates
}

// TotalSizeBytes sums the on-disk size of the given entries.
func TotalSizeBytes(entries []CacheEntry) int64 {
	var total int64
	for _, entry := range entries {
		if entry.SizeBytes > 0 {
			total += entry.SizeBytes
		}
	}
	return total
}
