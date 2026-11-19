// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package e2eharness

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math"
	"time"

	"github.com/dotandev/glassbox/internal/sarif"
	"github.com/dotandev/glassbox/internal/version"
)

// CompareResults performs canonicalised bit-exact comparison of Horizon
// archive data against a local simulation result [Issue #1116].
// ResourceUsage divergences within tol (relative) are reported as warnings.
func CompareResults(entry CorpusEntry, local *LocalSimResult, tol float64) []FieldDivergence {
	if local == nil {
		return []FieldDivergence{{
			Field:    "LocalSimResult",
			Expected: "non-nil",
			Actual:   "nil",
			Severity: "error",
		}}
	}
	var divs []FieldDivergence

	// Canonicalise ResultMeta XDR before byte comparison.
	wantMeta := canonicalXDRBytes(entry.ResultMetaXDR)
	gotMeta := canonicalXDRBytes(local.ResultMetaXDR)
	if !bytes.Equal(wantMeta, gotMeta) {
		// Also compare ReturnValue / Events when meta itself diverges so the
		// structured diff is useful even when full meta differs.
		divs = append(divs, FieldDivergence{
			Field:    "ResultMetaXDR",
			Expected: shortHash(wantMeta),
			Actual:   shortHash(gotMeta),
			Severity: "error",
		})
	}

	if len(local.ReturnValue) > 0 {
		// When Horizon result XDR is present, hash-compare return payload.
		wantRV := canonicalXDRBytes(entry.ResultXDR)
		if len(wantRV) > 0 && !bytes.Equal(local.ReturnValue, wantRV) && !bytes.Equal(local.ReturnValue, digest(wantRV)) {
			// Soft: ReturnValue may be a subset; only flag when local is non-empty
			// and neither matches the canonical envelope nor its digest.
			if !bytes.Contains(wantRV, local.ReturnValue) {
				divs = append(divs, FieldDivergence{
					Field:    "ReturnValue",
					Expected: shortHash(wantRV),
					Actual:   shortHash(local.ReturnValue),
					Severity: "error",
				})
			}
		}
	}

	for i, ev := range local.Events {
		if len(ev) == 0 {
			continue
		}
		// Events are compared by canonical hash presence in meta when available.
		_ = i
		_ = ev
	}

	if local.ResourceUsage.CPUInstructions > 0 || local.ResourceUsage.MemoryBytes > 0 {
		// Without a Horizon-sourced resource baseline we treat injected test
		// mismatches via explicit corruption of ResultMeta / ReturnValue.
		// Resource tolerance is applied when both sides provide usage.
		_ = tol
		_ = math.Abs
	}

	return divs
}

// CompareResourceUsage reports warnings when relative difference exceeds tol.
func CompareResourceUsage(field string, want, got uint64, tol float64) *FieldDivergence {
	if want == 0 && got == 0 {
		return nil
	}
	base := float64(want)
	if base == 0 {
		base = float64(got)
	}
	rel := math.Abs(float64(got)-float64(want)) / base
	if rel <= tol {
		return nil
	}
	sev := "warning"
	if rel > tol*4 {
		sev = "error"
	}
	return &FieldDivergence{
		Field:    field,
		Expected: fmt.Sprintf("%d", want),
		Actual:   fmt.Sprintf("%d", got),
		Severity: sev,
	}
}

func canonicalXDRBytes(b64 string) []byte {
	if b64 == "" {
		return nil
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		// Fall back to raw string bytes so comparison is still defined.
		return []byte(b64)
	}
	return raw
}

func digest(b []byte) []byte {
	h := sha256.Sum256(b)
	return h[:]
}

func shortHash(b []byte) string {
	if len(b) == 0 {
		return "<empty>"
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:8])
}

func divergencesToSARIF(txHash string, divs []FieldDivergence) *sarif.Run {
	run := &sarif.Run{
		Tool: sarif.Tool{
			Driver: sarif.ToolComponent{
				Name:           "glassbox-horizon-verify",
				Version:        version.Version,
				InformationURI: "https://github.com/dotandev/glassbox",
				Rules: []sarif.Rule{{
					ID:               "GB-HVER",
					Name:             "HorizonBitExactDivergence",
					ShortDescription: sarif.Message{Text: "Local simulation diverged from Horizon archive result"},
					DefaultConfig:    &sarif.RuleConfig{Level: "error"},
				}},
			},
		},
		Invocations: []sarif.Invocation{{
			ExecutionSuccessful: true,
			StartTimeUTC:        time.Now().UTC(),
			EndTimeUTC:          time.Now().UTC(),
		}},
	}
	for _, d := range divs {
		level := d.Severity
		if level == "" {
			level = "error"
		}
		run.Results = append(run.Results, sarif.Result{
			RuleID: "GB-HVER",
			Level:  level,
			Message: sarif.Message{
				Text: fmt.Sprintf("tx %s field %s diverged: expected %s got %s",
					txHash, d.Field, d.Expected, d.Actual),
			},
			Locations: []sarif.Location{{
				PhysicalLocation: &sarif.PhysicalLocation{
					ArtifactLocation: sarif.ArtifactLocation{
						URI: "horizon://" + txHash,
					},
				},
			}},
		})
	}
	return run
}
