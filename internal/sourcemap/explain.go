// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package sourcemap

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// ResolutionStage identifies which pipeline stage produced or rejected a candidate.
type ResolutionStage string

const (
	StageInputGuard      ResolutionStage = "input_guard"
	StageBuildManifest   ResolutionStage = "build_manifest"
	StageCache           ResolutionStage = "cache"
	StageRegistry        ResolutionStage = "registry"
	StageGitHub          ResolutionStage = "github"
	StageLocalOverride   ResolutionStage = "local_override"
	StageFullDWARF       ResolutionStage = "full_dwarf"
	StagePartialDWARF    ResolutionStage = "partial_dwarf"
	StageSymbolHeuristic ResolutionStage = "symbol_heuristic"
	StageCargoManifest   ResolutionStage = "cargo_manifest"
	StageNone            ResolutionStage = "none"
)

// ExplainEntry records one candidate examined during resolution.
// It never contains raw binary data or full source file contents.
type ExplainEntry struct {
	Stage    ResolutionStage `json:"stage"`
	File     string          `json:"file,omitempty"`
	Line     int             `json:"line,omitempty"`
	Function string          `json:"function,omitempty"`
	Quality  string          `json:"quality,omitempty"`
	Accepted bool            `json:"accepted"`
	Reason   string          `json:"reason"`
}

// ExplainTrace is the auditable decision trail for a single resolution attempt.
type ExplainTrace struct {
	// WasmAddr is the WASM instruction address being resolved (mapping pipeline only).
	WasmAddr   uint64         `json:"wasm_addr,omitempty"`
	Candidates []ExplainEntry `json:"candidates"`
	Summary    string         `json:"summary"`
	Confidence int            `json:"confidence"`
	Quality    string         `json:"quality"`
	ResolvedAt time.Time      `json:"resolved_at"`
}

func (t *ExplainTrace) add(e ExplainEntry) {
	t.Candidates = append(t.Candidates, e)
}

func (t *ExplainTrace) finalizeMappingResult(result *FallbackResult) {
	if result == nil {
		t.Quality = MappingQualityUnknown.String()
		t.Confidence = ConfidenceUnknown
		t.Summary = "resolution produced no result"
		return
	}
	t.Quality = result.Quality.String()
	t.Confidence = result.Confidence
	t.ResolvedAt = time.Now()
	switch {
	case result.File != "" && result.Line > 0:
		t.Summary = fmt.Sprintf("resolved to %s:%d (confidence %d, %s)",
			result.File, result.Line, result.Confidence, result.MatchKind)
	case result.File != "":
		t.Summary = fmt.Sprintf("resolved to %s (confidence %d, %s — line unknown)",
			result.File, result.Confidence, result.MatchKind)
	default:
		t.Summary = fmt.Sprintf("unresolved (confidence %d)", result.Confidence)
	}
}

// ResolveWithExplain maps a WASM instruction address to a source location and
// records the full decision trail. Every stage that was tried appears in the
// returned ExplainTrace with its acceptance status and the reason for that decision.
//
// The trace never includes raw WASM binary data or full source file contents.
func (m *FallbackMapper) ResolveWithExplain(wasmData []byte, addr uint64) (*FallbackResult, ExplainTrace) {
	trace := ExplainTrace{WasmAddr: addr, ResolvedAt: time.Now()}

	if len(wasmData) < 8 {
		trace.add(ExplainEntry{
			Stage:    StageInputGuard,
			Accepted: true,
			Reason: fmt.Sprintf("WASM data is %d bytes — too small to contain a valid binary; "+
				"source location for address 0x%x cannot be resolved", len(wasmData), addr),
		})
		result := m.Resolve(wasmData, addr)
		trace.finalizeMappingResult(result)
		return result, trace
	}

	// Stage 1: full DWARF
	if result := m.tryFullDWARF(wasmData, addr); result != nil {
		trace.add(ExplainEntry{
			Stage:    StageFullDWARF,
			File:     result.File,
			Line:     result.Line,
			Function: result.Function,
			Quality:  result.Quality.String(),
			Accepted: true,
			Reason:   explainFullDWARFAccepted(result),
		})
		trace.finalizeMappingResult(result)
		return result, trace
	}
	trace.add(ExplainEntry{
		Stage:    StageFullDWARF,
		Accepted: false,
		Reason:   "no usable DWARF line tables found, or address not covered by any subprogram",
	})

	// Stage 2: partial DWARF
	if result := m.tryPartialDWARF(wasmData, addr); result != nil {
		trace.add(ExplainEntry{
			Stage:    StagePartialDWARF,
			File:     result.File,
			Quality:  result.Quality.String(),
			Accepted: true,
			Reason:   "file name inferred from .debug_line table; .debug_info is absent or stripped",
		})
		trace.finalizeMappingResult(result)
		return result, trace
	}
	trace.add(ExplainEntry{
		Stage:    StagePartialDWARF,
		Accepted: false,
		Reason:   "no .debug_line section found in WASM custom sections",
	})

	// Stage 3: symbol heuristics
	if result := m.trySymbolHeuristics(wasmData, addr); result != nil {
		trace.add(ExplainEntry{
			Stage:    StageSymbolHeuristic,
			File:     result.File,
			Function: result.Function,
			Quality:  result.Quality.String(),
			Accepted: true,
			Reason:   "source path inferred from Rust mangled symbol in WASM name section or .debug_str",
		})
		trace.finalizeMappingResult(result)
		return result, trace
	}
	trace.add(ExplainEntry{
		Stage:    StageSymbolHeuristic,
		Accepted: false,
		Reason:   "no recognisable Rust symbol names found in WASM name section or .debug_str",
	})

	// Stage 4: Cargo manifest discovery
	if result := m.tryCargoDiscovery(wasmData, addr); result != nil {
		trace.add(ExplainEntry{
			Stage:    StageCargoManifest,
			File:     result.File,
			Quality:  result.Quality.String(),
			Accepted: true,
			Reason:   "source root inferred from Cargo.toml found under project root",
		})
		trace.finalizeMappingResult(result)
		return result, trace
	}
	trace.add(ExplainEntry{
		Stage:    StageCargoManifest,
		Accepted: false,
		Reason:   "no Cargo.toml found within 3 directory levels of project root",
	})

	// Stage 5: nothing found
	result := &FallbackResult{
		Quality: MappingQualityUnknown,
		Warning: capabilityWarning(wasmData, addr),
	}
	applyConfidence(result, addr, MatchUnknown, "none", "")
	trace.add(ExplainEntry{
		Stage:    StageNone,
		Accepted: false,
		Reason:   "all mapping stages exhausted; WASM address cannot be mapped to a source location",
	})
	trace.finalizeMappingResult(result)
	return result, trace
}

func explainFullDWARFAccepted(result *FallbackResult) string {
	if result.Quality == MappingQualityFull && result.Line > 0 {
		return "full DWARF line table resolved address to exact file and line number"
	}
	if result.Function != "" {
		return "subprogram entry covers address; exact line number unavailable (recompile with debug = true)"
	}
	return "DWARF resolved address to source location"
}

// ResolveWithExplain runs the full source-discovery pipeline for contractID
// and records which stage accepted or rejected each candidate.
//
// The returned trace is suitable for display with FormatExplainText or
// serialisation with FormatExplainJSON. It never includes raw binary data,
// full source file contents, or authentication tokens.
func (r *Resolver) ResolveWithExplain(ctx context.Context, contractID string) (*SourceCode, ExplainTrace, error) {
	trace := ExplainTrace{ResolvedAt: time.Now()}

	if err := validateContractID(contractID); err != nil {
		return nil, trace, fmt.Errorf("invalid contract ID %q: %w\n"+
			"  Contract IDs must start with 'C' and be exactly 56 characters long.", contractID, err)
	}

	// Stage 0: build manifest — highest priority.
	if r.buildManifest != nil {
		reason := fmt.Sprintf("build manifest at %q supplies source root %q at revision %s",
			r.buildManifestPath, r.buildManifest.SourceRoot,
			shortSHA(r.buildManifest.RepositoryRevision))
		trace.add(ExplainEntry{
			Stage:    StageBuildManifest,
			File:     r.buildManifest.SourceRoot,
			Accepted: true,
			Quality:  "full",
			Reason:   reason,
		})
		trace.Summary = fmt.Sprintf("source root resolved from build manifest: %s", r.buildManifest.SourceRoot)
		trace.Quality = "full"
		src := &SourceCode{
			ContractID: contractID,
			Repository: r.buildManifest.SourceRoot,
			WasmHash:   r.buildManifest.ArtifactHash,
			Files:      map[string]string{},
			FetchedAt:  time.Now(),
		}
		return src, trace, nil
	}
	trace.add(ExplainEntry{
		Stage:    StageBuildManifest,
		Accepted: false,
		Reason:   "no build manifest configured (--build-manifest not provided and no glassbox-build-manifest.json auto-discovered)",
	})

	// Stage 1: local cache.
	if r.cache != nil {
		if cached := r.cache.Get(contractID); cached != nil {
			trace.add(ExplainEntry{
				Stage:    StageCache,
				File:     cached.Repository,
				Accepted: true,
				Quality:  "full",
				Reason: fmt.Sprintf("source previously cached; repository %q fetched at %s",
					cached.Repository, cached.FetchedAt.Format(time.RFC3339)),
			})
			trace.Summary = fmt.Sprintf("source resolved from local cache: %s", cached.Repository)
			trace.Quality = "full"
			return cached, trace, nil
		}
		trace.add(ExplainEntry{
			Stage:    StageCache,
			Accepted: false,
			Reason:   "contract not present in local source cache",
		})
	} else {
		trace.add(ExplainEntry{
			Stage:    StageCache,
			Accepted: false,
			Reason:   "local cache not configured (use --cache-dir or GLASSBOX_CACHE_DIR to enable)",
		})
	}

	// Stage 2: registry (stellar.expert).
	source, regErr := r.registry.FetchVerifiedSource(ctx, contractID)
	if source != nil {
		trace.add(ExplainEntry{
			Stage:    StageRegistry,
			File:     source.Repository,
			Accepted: true,
			Quality:  "full",
			Reason:   fmt.Sprintf("registry returned verified source; repository %q", source.Repository),
		})
		trace.Summary = fmt.Sprintf("source resolved from registry (stellar.expert): %s", source.Repository)
		trace.Quality = "full"
		if r.cache != nil {
			_ = r.cache.Put(source)
		}
		return source, trace, nil
	}
	regReason := "registry (stellar.expert) returned no verified source for this contract"
	if regErr != nil {
		regReason = fmt.Sprintf("registry lookup failed: %v", regErr)
	}
	trace.add(ExplainEntry{
		Stage:    StageRegistry,
		Accepted: false,
		Reason:   regReason,
	})

	// Stage 3: GitHub retriever.
	if r.githubRetriever != nil {
		ghSource, ghErr := r.githubRetriever.Retrieve(ctx, contractID)
		if ghSource != nil {
			trace.add(ExplainEntry{
				Stage:    StageGitHub,
				File:     ghSource.Repository,
				Accepted: true,
				Quality:  "full",
				Reason:   fmt.Sprintf("GitHub retriever fetched source from %s", ghSource.Repository),
			})
			trace.Summary = fmt.Sprintf("source resolved via GitHub: %s", ghSource.Repository)
			trace.Quality = "full"
			if r.cache != nil {
				_ = r.cache.Put(ghSource)
			}
			return ghSource, trace, nil
		}
		ghReason := "GitHub retriever returned no source"
		if ghErr != nil {
			ghReason = fmt.Sprintf("GitHub retrieval failed: %v", ghErr)
		}
		trace.add(ExplainEntry{
			Stage:    StageGitHub,
			Accepted: false,
			Reason:   ghReason,
		})
	} else {
		trace.add(ExplainEntry{
			Stage:    StageGitHub,
			Accepted: false,
			Reason:   "GitHub retriever not configured",
		})
	}

	// Stage 4: --contract-source override.
	if r.contractSourceOverride != "" {
		overridePath := strings.TrimSpace(r.contractSourceOverride)
		if overridePath == "" {
			trace.add(ExplainEntry{
				Stage:    StageLocalOverride,
				File:     r.contractSourceOverride,
				Accepted: false,
				Reason:   "--contract-source value is empty or whitespace-only",
			})
			return nil, trace, fmt.Errorf(
				"--contract-source: value must not be empty or whitespace\n" +
					"  Provide the path to your contract's source directory (the one containing src/).")
		}
		info, statErr := os.Stat(overridePath)
		if statErr != nil {
			reason := fmt.Sprintf("--contract-source %q does not exist or is not accessible: %v", overridePath, statErr)
			trace.add(ExplainEntry{
				Stage:    StageLocalOverride,
				File:     overridePath,
				Accepted: false,
				Reason:   reason,
			})
			if os.IsNotExist(statErr) {
				return nil, trace, fmt.Errorf("--contract-source: directory not found: %q", overridePath)
			}
			return nil, trace, fmt.Errorf("--contract-source: cannot access %q: %w", overridePath, statErr)
		}
		if !info.IsDir() {
			trace.add(ExplainEntry{
				Stage:    StageLocalOverride,
				File:     overridePath,
				Accepted: false,
				Reason:   fmt.Sprintf("--contract-source %q is a file, not a directory", overridePath),
			})
			return nil, trace, fmt.Errorf("--contract-source: %q is a file, not a directory", overridePath)
		}
		trace.add(ExplainEntry{
			Stage:    StageLocalOverride,
			File:     overridePath,
			Accepted: true,
			Quality:  "full",
			Reason:   fmt.Sprintf("--contract-source override accepted: directory %q exists and is accessible", overridePath),
		})
		trace.Summary = fmt.Sprintf("source resolved from --contract-source override: %s", overridePath)
		trace.Quality = "full"
		return &SourceCode{
			ContractID: contractID,
			Repository: overridePath,
			Files:      map[string]string{},
			FetchedAt:  time.Now(),
		}, trace, nil
	}
	trace.add(ExplainEntry{
		Stage:    StageLocalOverride,
		Accepted: false,
		Reason:   "--contract-source flag not provided",
	})

	// All automatic stages exhausted.
	trace.add(ExplainEntry{
		Stage:    StageNone,
		Accepted: false,
		Reason:   "all source-discovery stages exhausted without finding a source location",
	})
	trace.Quality = "unknown"
	if r.nonInteractive {
		trace.Summary = "source not found: all stages exhausted (non-interactive mode)"
		return nil, trace, fmt.Errorf(
			"%w for contract %q\n"+
				"  Stages tried: build manifest, cache, registry (stellar.expert), GitHub retriever, --contract-source override\n"+
				"  To resolve: provide --build-manifest <path> or --contract-source <path>,\n"+
				"  or verify the contract on stellar.expert to enable registry lookup.\n"+
				"  Use --skip-source-mapping to proceed without source mapping.",
			ErrSourceNotFound, contractID,
		)
	}
	trace.Summary = "source not found automatically; interactive prompt would be shown"
	return nil, trace, nil
}

func shortSHA(rev string) string {
	if len(rev) > 12 {
		return rev[:12]
	}
	return rev
}

// FormatExplainText formats an ExplainTrace as concise human-readable text.
func FormatExplainText(trace ExplainTrace) string {
	var sb strings.Builder

	if trace.WasmAddr != 0 {
		fmt.Fprintf(&sb, "Source-map explain for WASM address 0x%x\n", trace.WasmAddr)
	} else {
		sb.WriteString("Source discovery explain\n")
	}
	sep := strings.Repeat("─", 64)
	sb.WriteString(sep + "\n")

	for i, c := range trace.Candidates {
		status := "REJECTED"
		if c.Accepted {
			status = "ACCEPTED"
		}
		fmt.Fprintf(&sb, "[%d] stage=%-20s  %s\n", i+1, string(c.Stage), status)
		fmt.Fprintf(&sb, "    reason   : %s\n", c.Reason)
		if c.File != "" {
			if c.Line > 0 {
				fmt.Fprintf(&sb, "    location : %s:%d\n", c.File, c.Line)
			} else {
				fmt.Fprintf(&sb, "    location : %s\n", c.File)
			}
		}
		if c.Function != "" {
			fmt.Fprintf(&sb, "    function : %s\n", c.Function)
		}
		if c.Quality != "" {
			fmt.Fprintf(&sb, "    quality  : %s\n", c.Quality)
		}
		sb.WriteByte('\n')
	}

	sb.WriteString(sep + "\n")
	fmt.Fprintf(&sb, "summary   : %s\n", trace.Summary)
	fmt.Fprintf(&sb, "quality   : %s\n", trace.Quality)
	fmt.Fprintf(&sb, "confidence: %d / 100\n", trace.Confidence)
	return sb.String()
}

// FormatExplainJSON encodes an ExplainTrace as indented JSON.
func FormatExplainJSON(trace ExplainTrace) ([]byte, error) {
	return json.MarshalIndent(trace, "", "  ")
}
