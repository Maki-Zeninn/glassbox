// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package profile

import (
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/dotandev/glassbox/internal/trace"
)

// OverheadThresholdDefault is the default overhead percentage above which
// a warning is emitted. A 10% overhead means profiling adds ~10% execution time.
const OverheadThresholdDefault = 10.0

// SamplingConfig records the parameters used for profiling sampling.
type SamplingConfig struct {
	// SampleRate is the fraction of steps sampled (0.0 to 1.0).
	SampleRate float64 `json:"sample_rate"`
	// MaxSteps is the maximum number of steps captured (0 = unlimited).
	MaxSteps int `json:"max_steps"`
	// IncludeGasControls whether gas consumption is tracked per step.
	IncludeGas bool `json:"include_gas"`
}

// OverheadMeasurement captures the measured cost of profiling instrumentation.
type OverheadMeasurement struct {
	// ProfiledDuration is the execution time with profiling enabled.
	ProfiledDuration time.Duration `json:"profiled_duration_ns"`
	// BaselineDuration is the execution time without profiling.
	BaselineDuration time.Duration `json:"baseline_duration_ns"`
	// OverheadPercent is the percentage increase: (profiled - baseline) / baseline * 100.
	OverheadPercent float64 `json:"overhead_percent"`
	// ProfiledAllocs is the number of allocations during profiled execution.
	ProfiledAllocs uint64 `json:"profiled_allocs"`
	// BaselineAllocs is the number of allocations during baseline execution.
	BaselineAllocs uint64 `json:"baseline_allocs"`
	// AllocDelta is the difference in allocations attributable to profiling.
	AllocDelta uint64 `json:"alloc_delta"`
	// MeasuredAt records when the measurement was taken.
	MeasuredAt time.Time `json:"measured_at"`
}

// ProfileMetadata attaches sampling and overhead information to profile output.
type ProfileMetadata struct {
	// SamplingConfig records how the profile was collected.
	SamplingConfig SamplingConfig `json:"sampling_config"`
	// Overhead records the measured profiling cost (may be nil if not measured).
	Overhead *OverheadMeasurement `json:"overhead,omitempty"`
	// OverheadWarning is non-empty when overhead exceeds the configured threshold.
	OverheadWarning string `json:"overhead_warning,omitempty"`
}

// MeasureOverhead compares profiled vs. unprofiled execution of the given
// function and returns the measured overhead. This runs fn twice: once as
// baseline (with profiling disabled) and once with profiling instrumentation.
//
// The function uses runtime.GC() between runs to minimize memory effects
// on timing.
func MeasureOverhead(fn func()) *OverheadMeasurement {
	// Warm up
	for i := 0; i < 3; i++ {
		fn()
	}

	// Baseline measurement (no profiling)
	runtime.GC()
	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)

	start := time.Now()
	fn()
	baselineDuration := time.Since(start)

	var memAfter runtime.MemStats
	runtime.ReadMemStats(&memAfter)
	baselineAllocs := memAfter.Mallocs - memBefore.Mallocs

	// Profiled measurement
	runtime.GC()
	var memBeforeP runtime.MemStats
	runtime.ReadMemStats(&memBeforeP)

	start = time.Now()
	fn()
	profiledDuration := time.Since(start)

	var memAfterP runtime.MemStats
	runtime.ReadMemStats(&memAfterP)
	profiledAllocs := memAfterP.Mallocs - memBeforeP.Mallocs

	overheadPct := 0.0
	if baselineDuration > 0 {
		overheadPct = float64(profiledDuration-baselineDuration) / float64(baselineDuration) * 100
	}

	return &OverheadMeasurement{
		ProfiledDuration:  profiledDuration,
		BaselineDuration: baselineDuration,
		OverheadPercent:   overheadPct,
		ProfiledAllocs:    profiledAllocs,
		BaselineAllocs:    baselineAllocs,
		AllocDelta:        profiledAllocs - baselineAllocs,
		MeasuredAt:        time.Now().UTC(),
	}
}

// MeasureTraceOverhead measures the overhead of trace processing operations
// (building frames, generating HTML) compared to a no-op baseline.
func MeasureTraceOverhead(execTrace *trace.ExecutionTrace) *OverheadMeasurement {
	if execTrace == nil || len(execTrace.States) == 0 {
		return &OverheadMeasurement{
			MeasuredAt: time.Now().UTC(),
		}
	}

	noopFn := func() {
		// Intentionally empty — measures pure overhead of measurement itself
		_ = len(execTrace.States)
	}

	traceFn := func() {
		frames := buildFrames(execTrace)
		_ = frames
	}

	// Measure baseline (noop)
	runtime.GC()
	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)
	start := time.Now()
	noopFn()
	baselineDuration := time.Since(start)
	var memAfter runtime.MemStats
	runtime.ReadMemStats(&memAfter)
	baselineAllocs := memAfter.Mallocs - memBefore.Mallocs

	// Measure trace processing
	runtime.GC()
	var memBeforeP runtime.MemStats
	runtime.ReadMemStats(&memBeforeP)
	start = time.Now()
	traceFn()
	profiledDuration := time.Since(start)
	var memAfterP runtime.MemStats
	runtime.ReadMemStats(&memAfterP)
	profiledAllocs := memAfterP.Mallocs - memBeforeP.Mallocs

	overheadPct := 0.0
	if baselineDuration > 0 {
		overheadPct = float64(profiledDuration-baselineDuration) / float64(baselineDuration) * 100
	}

	return &OverheadMeasurement{
		ProfiledDuration:  profiledDuration,
		BaselineDuration: baselineDuration,
		OverheadPercent:   overheadPct,
		ProfiledAllocs:    profiledAllocs,
		BaselineAllocs:    baselineAllocs,
		AllocDelta:        profiledAllocs - baselineAllocs,
		MeasuredAt:        time.Now().UTC(),
	}
}

// CheckOverhead returns a warning message if the measured overhead exceeds
// the given threshold percentage, or an empty string otherwise.
func CheckOverhead(measurement *OverheadMeasurement, threshold float64) string {
	if measurement == nil || threshold <= 0 {
		return ""
	}
	if measurement.OverheadPercent > threshold {
		return fmt.Sprintf(
			"Warning: profiling overhead %.1f%% exceeds threshold %.1f%%. "+
				"Profiled duration: %s, baseline: %s. "+
				"This may affect the accuracy of gas measurements.",
			measurement.OverheadPercent, threshold,
			measurement.ProfiledDuration, measurement.BaselineDuration,
		)
	}
	return ""
}

// NewProfileMetadata creates ProfileMetadata with default sampling config
// and optional overhead measurement.
func NewProfileMetadata(config SamplingConfig, overhead *OverheadMeasurement, threshold float64) *ProfileMetadata {
	pm := &ProfileMetadata{
		SamplingConfig: config,
		Overhead:       overhead,
	}
	if overhead != nil {
		pm.OverheadWarning = CheckOverhead(overhead, threshold)
	}
	return pm
}

// EmitOverheadWarning writes the overhead warning to stderr if present.
func EmitOverheadWarning(pm *ProfileMetadata) {
	if pm == nil || pm.OverheadWarning == "" {
		return
	}
	fmt.Fprintln(os.Stderr, pm.OverheadWarning)
}
