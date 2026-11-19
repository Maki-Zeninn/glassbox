// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package profile

import (
	"strings"
	"testing"
	"time"

	"github.com/dotandev/glassbox/internal/trace"
)

func TestMeasureOverhead_BasicMeasurement(t *testing.T) {
	fn := func() {
		sum := 0
		for i := 0; i < 1000; i++ {
			sum += i
		}
		_ = sum
	}

	m := MeasureOverhead(fn)
	if m == nil {
		t.Fatal("expected non-nil measurement")
	}
	if m.BaselineDuration <= 0 {
		t.Error("baseline duration should be positive")
	}
	if m.ProfiledDuration <= 0 {
		t.Error("profiled duration should be positive")
	}
	if m.MeasuredAt.IsZero() {
		t.Error("measured_at should be set")
	}
}

func TestCheckOverhead_BelowThreshold(t *testing.T) {
	m := &OverheadMeasurement{
		OverheadPercent:   5.0,
		ProfiledDuration:  105 * time.Millisecond,
		BaselineDuration:  100 * time.Millisecond,
	}

	warning := CheckOverhead(m, 10.0)
	if warning != "" {
		t.Errorf("expected no warning below threshold, got: %s", warning)
	}
}

func TestCheckOverhead_AboveThreshold(t *testing.T) {
	m := &OverheadMeasurement{
		OverheadPercent:   15.0,
		ProfiledDuration:  115 * time.Millisecond,
		BaselineDuration:  100 * time.Millisecond,
	}

	warning := CheckOverhead(m, 10.0)
	if warning == "" {
		t.Error("expected warning above threshold")
	}
	if !strings.Contains(warning, "15.0%") {
		t.Error("warning should contain overhead percentage")
	}
	if !strings.Contains(warning, "10.0%") {
		t.Error("warning should contain threshold percentage")
	}
}

func TestCheckOverhead_NilMeasurement(t *testing.T) {
	warning := CheckOverhead(nil, 10.0)
	if warning != "" {
		t.Error("expected no warning for nil measurement")
	}
}

func TestCheckOverhead_ZeroThreshold(t *testing.T) {
	m := &OverheadMeasurement{OverheadPercent: 5.0}
	warning := CheckOverhead(m, 0)
	if warning != "" {
		t.Error("expected no warning for zero threshold")
	}
}

func TestNewProfileMetadata_WithOverhead(t *testing.T) {
	m := &OverheadMeasurement{
		OverheadPercent:  5.0,
		MeasuredAt:       time.Now().UTC(),
	}
	config := SamplingConfig{
		SampleRate: 1.0,
		MaxSteps:   1000,
		IncludeGas: true,
	}

	pm := NewProfileMetadata(config, m, 10.0)
	if pm == nil {
		t.Fatal("expected non-nil metadata")
	}
	if pm.SamplingConfig.SampleRate != 1.0 {
		t.Errorf("expected sample rate 1.0, got %f", pm.SamplingConfig.SampleRate)
	}
	if pm.Overhead == nil {
		t.Error("expected overhead measurement to be set")
	}
	if pm.OverheadWarning != "" {
		t.Error("expected no warning below threshold")
	}
}

func TestNewProfileMetadata_OverheadExceedsThreshold(t *testing.T) {
	m := &OverheadMeasurement{
		OverheadPercent:  20.0,
		MeasuredAt:       time.Now().UTC(),
	}

	pm := NewProfileMetadata(SamplingConfig{}, m, 10.0)
	if pm.OverheadWarning == "" {
		t.Error("expected warning when overhead exceeds threshold")
	}
}

func TestNewProfileMetadata_NilOverhead(t *testing.T) {
	pm := NewProfileMetadata(SamplingConfig{}, nil, 10.0)
	if pm.Overhead != nil {
		t.Error("expected nil overhead when none provided")
	}
	if pm.OverheadWarning != "" {
		t.Error("expected no warning with nil overhead")
	}
}

func TestMeasureTraceOverhead_NilTrace(t *testing.T) {
	m := MeasureTraceOverhead(nil)
	if m == nil {
		t.Fatal("expected non-nil measurement even for nil trace")
	}
	if m.OverheadPercent != 0 {
		t.Error("expected zero overhead for nil trace")
	}
}

func TestMeasureTraceOverhead_EmptyTrace(t *testing.T) {
	tr := trace.NewExecutionTrace("tx-empty", 10)
	m := MeasureTraceOverhead(tr)
	if m == nil {
		t.Fatal("expected non-nil measurement")
	}
}

func TestMeasureTraceOverhead_WithStates(t *testing.T) {
	tr := trace.NewExecutionTrace("tx-test", 10)
	tr.AddState(trace.ExecutionState{
		Operation: "call",
		Function:  "transfer",
		HostState: map[string]interface{}{"gas_used": float64(1000)},
	})
	tr.AddState(trace.ExecutionState{
		Operation: "call",
		Function:  "balance_of",
		HostState: map[string]interface{}{"gas_used": float64(500)},
	})

	m := MeasureTraceOverhead(tr)
	if m == nil {
		t.Fatal("expected non-nil measurement")
	}
	if m.ProfiledDuration <= 0 {
		t.Error("profiled duration should be positive")
	}
}

func TestSamplingConfig(t *testing.T) {
	config := SamplingConfig{
		SampleRate: 0.5,
		MaxSteps:   500,
		IncludeGas: true,
	}

	pm := NewProfileMetadata(config, nil, 10.0)
	if pm.SamplingConfig.SampleRate != 0.5 {
		t.Errorf("expected sample rate 0.5, got %f", pm.SamplingConfig.SampleRate)
	}
	if pm.SamplingConfig.MaxSteps != 500 {
		t.Errorf("expected max steps 500, got %d", pm.SamplingConfig.MaxSteps)
	}
	if !pm.SamplingConfig.IncludeGas {
		t.Error("expected IncludeGas to be true")
	}
}
