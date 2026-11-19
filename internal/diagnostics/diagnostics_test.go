// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package diagnostics

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── Collector.Start / DoneFunc ────────────────────────────────────────────────

func TestCollector_RecordsSuccessfulPhase(t *testing.T) {
	c := NewCollector()

	done := c.Start(PhaseRPCFetch)
	time.Sleep(2 * time.Millisecond)
	done(nil) // success

	recs := c.Records()
	if len(recs) != 1 {
		t.Fatalf("expected 1 record, got %d", len(recs))
	}
	r := recs[0]
	if r.Phase != PhaseRPCFetch {
		t.Errorf("phase = %q, want %q", r.Phase, PhaseRPCFetch)
	}
	if r.Failed {
		t.Error("Failed should be false for a nil error")
	}
	if r.DurationMs < 0 {
		t.Errorf("DurationMs should be non-negative, got %f", r.DurationMs)
	}
}

func TestCollector_RecordsFailedPhase(t *testing.T) {
	c := NewCollector()

	done := c.Start(PhaseSimulator)
	done(errors.New("simulator crashed"))

	recs := c.Records()
	if len(recs) != 1 {
		t.Fatalf("expected 1 record, got %d", len(recs))
	}
	if !recs[0].Failed {
		t.Error("Failed should be true when done is called with a non-nil error")
	}
	if recs[0].Phase != PhaseSimulator {
		t.Errorf("phase = %q, want %q", recs[0].Phase, PhaseSimulator)
	}
}

func TestCollector_MultiplePhases_PreservesOrder(t *testing.T) {
	c := NewCollector()

	phases := []Phase{PhaseRPCFetch, PhaseSimulator, PhaseDecode, PhaseSourceMap, PhaseTraceExport}
	for _, p := range phases {
		c.Start(p)(nil)
	}

	recs := c.Records()
	if len(recs) != len(phases) {
		t.Fatalf("expected %d records, got %d", len(phases), len(recs))
	}
	for i, want := range phases {
		if recs[i].Phase != want {
			t.Errorf("record[%d].Phase = %q, want %q", i, recs[i].Phase, want)
		}
	}
}

func TestCollector_PhaseClosedOnError_DurationIsPositive(t *testing.T) {
	c := NewCollector()

	done := c.Start(PhaseDecode)
	time.Sleep(1 * time.Millisecond)
	done(errors.New("decode error"))

	recs := c.Records()
	if recs[0].DurationMs <= 0 {
		t.Errorf("expected positive duration even on error, got %f", recs[0].DurationMs)
	}
}

// ── Noop ─────────────────────────────────────────────────────────────────────

func TestNoop_IsDisabled(t *testing.T) {
	c := Noop()
	if c.Enabled() {
		t.Error("Noop() collector should report Enabled() == false")
	}
}

func TestNoop_RecordsNothing(t *testing.T) {
	c := Noop()

	// Start + done should be a no-op — no panic, no record.
	done := c.Start(PhaseRPCFetch)
	done(nil)
	done(errors.New("err")) // calling done twice should also not panic

	if len(c.Records()) != 0 {
		t.Error("Noop() collector should not accumulate records")
	}
}

func TestNoop_PrintHuman_WritesNothing(t *testing.T) {
	c := Noop()
	var buf bytes.Buffer
	c.PrintHuman(&buf)
	if buf.Len() != 0 {
		t.Errorf("Noop().PrintHuman() should write nothing, got %q", buf.String())
	}
}

// ── Injectable interface ──────────────────────────────────────────────────────

// CollectorIface is the minimal interface callers depend on; ensure both
// NewCollector() and Noop() satisfy it without a cast.
type CollectorIface interface {
	Start(Phase) DoneFunc
	Enabled() bool
	Records() []PhaseRecord
	PrintHuman(w interface{ Write([]byte) (int, error) })
}

func TestCollector_SatisfiesInterface(t *testing.T) {
	// Compile-time: both types must satisfy CollectorIface.
	var _ *Collector = NewCollector()
	var _ *Collector = Noop()
}

func TestCollector_InjectableForTests(t *testing.T) {
	// Pattern used by command tests: pass in Noop() when timing is not the
	// subject under test, NewCollector() when it is.
	run := func(c *Collector) []PhaseRecord {
		done := c.Start(PhaseSimulator)
		done(nil)
		return c.Records()
	}

	// Enabled collector accumulates.
	enabled := NewCollector()
	if got := run(enabled); len(got) != 1 {
		t.Errorf("enabled collector: expected 1 record, got %d", len(got))
	}

	// Noop collector stays silent.
	noop := Noop()
	if got := run(noop); len(got) != 0 {
		t.Errorf("noop collector: expected 0 records, got %d", len(got))
	}
}

// ── BuildTimingsBlock / JSON ──────────────────────────────────────────────────

func TestBuildTimingsBlock_TotalMsIsSumOfPhases(t *testing.T) {
	c := NewCollector()
	c.Start(PhaseRPCFetch)(nil)
	c.Start(PhaseSimulator)(nil)

	tb := c.BuildTimingsBlock()
	if len(tb.Phases) != 2 {
		t.Fatalf("expected 2 phases, got %d", len(tb.Phases))
	}
	want := tb.Phases[0].DurationMs + tb.Phases[1].DurationMs
	if tb.TotalMs != want {
		t.Errorf("TotalMs = %f, want sum %f", tb.TotalMs, want)
	}
}

func TestCollector_MarshalJSON_ContainsPhaseAndDuration(t *testing.T) {
	c := NewCollector()
	done := c.Start(PhaseTraceLoad)
	time.Sleep(1 * time.Millisecond)
	done(nil)

	data, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var tb TimingsBlock
	if err := json.Unmarshal(data, &tb); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}
	if len(tb.Phases) != 1 {
		t.Fatalf("expected 1 phase, got %d", len(tb.Phases))
	}
	if tb.Phases[0].Phase != PhaseTraceLoad {
		t.Errorf("phase = %q, want %q", tb.Phases[0].Phase, PhaseTraceLoad)
	}
	if tb.Phases[0].DurationMs <= 0 {
		t.Error("duration_ms should be positive in JSON output")
	}
}

func TestCollector_MarshalJSON_FailedFieldOmittedOnSuccess(t *testing.T) {
	c := NewCollector()
	c.Start(PhaseRPCFetch)(nil)

	data, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	// "failed" key must be absent when the phase succeeded (omitempty).
	if strings.Contains(string(data), `"failed"`) {
		t.Errorf("JSON should not contain 'failed' key for a successful phase, got: %s", data)
	}
}

func TestCollector_MarshalJSON_FailedFieldPresentOnError(t *testing.T) {
	c := NewCollector()
	c.Start(PhaseSimulator)(errors.New("boom"))

	data, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	if !strings.Contains(string(data), `"failed":true`) {
		t.Errorf("JSON should contain 'failed':true for an errored phase, got: %s", data)
	}
}

// ── PrintHuman ────────────────────────────────────────────────────────────────

func TestPrintHuman_ContainsPhaseNames(t *testing.T) {
	c := NewCollector()
	c.Start(PhaseRPCFetch)(nil)
	c.Start(PhaseSimulator)(errors.New("fail"))

	var buf bytes.Buffer
	c.PrintHuman(&buf)
	out := buf.String()

	if !strings.Contains(out, string(PhaseRPCFetch)) {
		t.Errorf("human output missing phase %q\n%s", PhaseRPCFetch, out)
	}
	if !strings.Contains(out, string(PhaseSimulator)) {
		t.Errorf("human output missing phase %q\n%s", PhaseSimulator, out)
	}
}

func TestPrintHuman_FailedPhasesMarked(t *testing.T) {
	c := NewCollector()
	c.Start(PhaseDecode)(errors.New("decode failed"))

	var buf bytes.Buffer
	c.PrintHuman(&buf)

	if !strings.Contains(buf.String(), "[failed]") {
		t.Errorf("human output should mark failed phases with [failed], got:\n%s", buf.String())
	}
}

func TestPrintHuman_EmptyCollector_WritesNothing(t *testing.T) {
	c := NewCollector() // enabled but no phases recorded
	var buf bytes.Buffer
	c.PrintHuman(&buf)
	// With no records the header should be suppressed.
	if strings.Contains(buf.String(), "Phase Timings") {
		t.Errorf("empty collector should not write timing header, got:\n%s", buf.String())
	}
}

// ── Concurrency ───────────────────────────────────────────────────────────────

func TestCollector_ConcurrentStarts_NoRace(t *testing.T) {
	c := NewCollector()
	const goroutines = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			done := c.Start(PhaseSimulator)
			done(nil)
		}()
	}
	wg.Wait()

	recs := c.Records()
	if len(recs) != goroutines {
		t.Errorf("expected %d records after concurrent starts, got %d", goroutines, len(recs))
	}
}

func TestCollector_CorrelationID(t *testing.T) {
	c := NewCollector()
	c.SetCorrelationID("corr-999")
	if c.CorrelationID() != "corr-999" {
		t.Fatalf("expected CorrelationID 'corr-999', got %q", c.CorrelationID())
	}

	block := c.BuildTimingsBlock()
	if block.CorrelationID != "corr-999" {
		t.Errorf("expected TimingsBlock.CorrelationID 'corr-999', got %q", block.CorrelationID)
	}

	var buf bytes.Buffer
	c.PrintHuman(&buf)
	if !strings.Contains(buf.String(), "Correlation ID: corr-999") {
		t.Errorf("PrintHuman output should contain Correlation ID, got:\n%s", buf.String())
	}

	jsonData, err := c.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON error: %v", err)
	}
	if !strings.Contains(string(jsonData), `"correlation_id":"corr-999"`) && !strings.Contains(string(jsonData), `"correlation_id": "corr-999"`) {
		t.Errorf("JSON output should contain correlation_id, got:\n%s", string(jsonData))
	}
}
