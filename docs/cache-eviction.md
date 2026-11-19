# Cache eviction

`glassbox cache clean` evicts entries. This document explains how candidates are
chosen and how to tune it.

## Why not plain LRU

Least-recently-used eviction ranks entries by last-access time alone, which
treats every entry as equally valuable. In practice that is wrong in both
directions:

- A **transaction envelope** fetched while debugging a failing transaction is
  re-examined many times during the session. Evicting it costs the developer
  time.
- A **ledger entry snapshot** fetched speculatively and never read again is
  pure disk cost.

Size matters too: two entries with the same access pattern are not equally
expensive to keep, and when the cache is under pressure, testnet data is far
cheaper to discard than mainnet data.

## Scoring formula

`EvictionScorer` computes a scalar score per entry. Every factor is normalised
to `[0, 1]` where **1 means "evict first"**:

```
score = w_type * typeFactor
      + w_freq * frequencyFactor
      + w_size * sizeFactor
      + w_net  * networkFactor
      + w_idle * idleFactor
```

| Factor | Definition |
| ------ | ---------- |
| `typeFactor` | Retention value of the entry type (table below) |
| `frequencyFactor` | `1 - accessCount / maxAccessCount` — 1 for a never-read entry |
| `sizeFactor` | `sizeBytes / maxSizeBytes` — large entries cost more to keep |
| `networkFactor` | 1 for `testnet`, 0 for `mainnet`, 0.5 for unknown |
| `idleFactor` | time since last access, normalised by `idle_after` |

The maxima used for normalisation are computed across the whole candidate set,
so the factors always span the full range even for a small or homogeneous set.

### Entry type weights

| Type | Factor | Rationale |
| ---- | ------ | --------- |
| `ledger_entry` | 1.00 | rarely re-read once fetched |
| `other` | 0.90 | unknown provenance, assume disposable |
| `wasm_code` | 0.50 | reusable across transactions but large |
| `footprint` | 0.35 | small and valuable |
| `envelope` | 0.20 | the entry a debugging session lives on |

An unrecognised type string is treated as `other` rather than erroring, so an
entry written by a newer Glassbox version cannot break eviction.

## Default weights

The shipped weights sum to 1, which keeps scores in `[0, 1]` and therefore
comparable across runs and across strategies:

```json
{
  "type_weight": 0.30,
  "frequency_weight": 0.30,
  "size_weight": 0.20,
  "network_weight": 0.10,
  "idle_weight": 0.10
}
```

## Configuration

Add an `eviction` section to the cache config at `~/.Glassbox/config.json`:

```json
{
  "max_size_bytes": 1073741824,
  "eviction": {
    "weights": {
      "type_weight": 0.40,
      "frequency_weight": 0.30,
      "size_weight": 0.10,
      "network_weight": 0.10,
      "idle_weight": 0.10
    },
    "idle_after": 604800000000,
    "high_value_types": ["envelope", "footprint"]
  }
}
```

`idle_after` is a Go `time.Duration` in nanoseconds; the default is 7 days
(604800000000). Missing fields fall back to `DefaultEvictionConfig`.

## Candidate selection

`SelectEvictionCandidates(entries, targetFreeBytes)` returns the minimum set of
entries to evict to free at least `targetFreeBytes`, ordered by descending
score.

Selection is greedy: entries are taken in descending score order until the
accumulated size reaches the target. Under a total order that yields the fewest
entries for a size target, and it degrades usefully at the edges:

- **target larger than the whole cache** — every entry is returned, which is
  what a "free as much as possible" clean needs.
- **target of zero or less** — nothing is returned; the caller's goal is to
  keep the cache as-is.
- **empty candidate set** — nothing is returned.

Ties are broken by entry key so a `--dry-run` report is stable across runs.

## Strategies

| Strategy | Behaviour |
| -------- | --------- |
| `lru` | the existing naive LRU: oldest access time first |
| `lru-ttl` | age-based, dropping entries past their TTL |
| `scored` | the strategy described above; the default |

`--dry-run` prints the candidates and their scores without deleting anything.

## Non-interactive use

When there is cache data to delete, `cache clean`, `cache clear`, and `cache
artifact clear` prompt for confirmation. In CI or another non-interactive
environment, they return an error unless the command's `--force` flag is
supplied. `--non-interactive` disables prompts; it does not approve deletion.

Use `--force` only in a job that is explicitly intended to remove the selected
cache data:

```bash
glassbox --non-interactive cache clean --force
glassbox --non-interactive cache clear --force
glassbox --non-interactive cache artifact clear \
  --type contract_code --network testnet --force
```

## API

```go
scorer := cache.NewEvictionScorer(cache.DefaultEvictionConfig())
scored := scorer.ScoreAll(entries)          // highest score first
candidates := cache.SelectEvictionCandidates(entries, targetFreeBytes)
freed := cache.TotalSizeBytes(candidates)
```

`ScoredEntry.Factors` holds the per-factor contributions, which is what
`--dry-run` reports and what you use to explain why an entry was chosen.
