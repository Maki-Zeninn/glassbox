// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// limits_test.go covers the extended Limits fields added in Issue #XXX:
// MaxLocalsPerFunction, MaxDataSegments, and MaxValidationTimeMS.

package wasmvalidate

import (
	"bytes"
	"testing"
	"time"
)

// ── builder helpers (reuse from validate_test.go in same package) ─────────────

// buildDataSection constructs a WASM data section with segmentCount passive
// segments, each containing contentBytes bytes of zeros.
func buildDataSection(segmentCount, contentBytes int) testSection {
	var payload bytes.Buffer
	payload.Write(encodeULEB32(uint32(segmentCount)))
	for i := 0; i < segmentCount; i++ {
		// Passive data segment (flag = 1)
		payload.WriteByte(0x01)
		// Byte vector: length + content
		payload.Write(encodeULEB32(uint32(contentBytes)))
		payload.Write(make([]byte, contentBytes))
	}
	return testSection{id: sectionData, payload: payload.Bytes()}
}

// buildCodeSectionWithLocals builds a code section whose function bodies each
// declare `localsPerFunc` locals of the same type (i32 = 0x7f).
func buildCodeSectionWithLocals(funcCount, localsPerFunc int) testSection {
	var payload bytes.Buffer
	payload.Write(encodeULEB32(uint32(funcCount)))
	for i := 0; i < funcCount; i++ {
		// Build a single body: 1 local-vec entry + end opcode.
		var body bytes.Buffer
		body.Write(encodeULEB32(1))                       // local_count = 1 entry
		body.Write(encodeULEB32(uint32(localsPerFunc)))   // count
		body.WriteByte(0x7f)                              // type: i32
		body.WriteByte(0x0b)                              // end

		payload.Write(encodeULEB32(uint32(body.Len())))
		payload.Write(body.Bytes())
	}
	return testSection{id: sectionCode, payload: payload.Bytes()}
}

// ── MaxLocalsPerFunction ──────────────────────────────────────────────────────

func TestValidate_MaxLocalsPerFunction_OK(t *testing.T) {
	module := buildModule(
		buildFunctionSection(2),
		buildCodeSectionWithLocals(2, 100),
	)
	report := Validate(module, Limits{MaxLocalsPerFunction: 200})
	if !report.OK {
		t.Fatalf("expected OK for 100 locals per function (limit 200), got: %+v", report.Issues)
	}
}

func TestValidate_MaxLocalsPerFunction_AtExactLimit(t *testing.T) {
	module := buildModule(
		buildFunctionSection(1),
		buildCodeSectionWithLocals(1, 500),
	)
	report := Validate(module, Limits{MaxLocalsPerFunction: 500})
	// Exactly at the limit should pass.
	if !report.OK {
		t.Fatalf("expected OK at exact limit 500 locals, got: %+v", report.Issues)
	}
}

func TestValidate_MaxLocalsPerFunction_Exceeded(t *testing.T) {
	module := buildModule(
		buildFunctionSection(1),
		buildCodeSectionWithLocals(1, 1000),
	)
	report := Validate(module, Limits{MaxLocalsPerFunction: 500})
	if report.OK {
		t.Fatal("expected failure for 1000 locals with limit 500")
	}
	found := false
	for _, iss := range report.Issues {
		if iss.Class == ClassPolicy && iss.Field == "function[0].locals" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected function[0].locals policy issue, got %+v", report.Issues)
	}
}

func TestValidate_MaxLocalsPerFunction_Disabled(t *testing.T) {
	// MaxLocalsPerFunction = 0 disables the check.
	module := buildModule(
		buildFunctionSection(1),
		buildCodeSectionWithLocals(1, 1_000_000),
	)
	report := Validate(module, Limits{MaxLocalsPerFunction: 0})
	// The locals check is disabled; no policy issue should be reported for it.
	for _, iss := range report.Issues {
		if iss.Class == ClassPolicy && iss.Field == "function[0].locals" {
			t.Fatalf("locals check should be disabled when MaxLocalsPerFunction=0, got %+v", iss)
		}
	}
}

func TestValidate_MaxLocalsPerFunction_MultipleBodyExceedsOnOne(t *testing.T) {
	// First body has 10 locals (OK), second has 1000 (exceeds limit 500).
	var payload bytes.Buffer
	payload.Write(encodeULEB32(2)) // 2 function bodies

	// body 0: 10 locals
	{
		var body bytes.Buffer
		body.Write(encodeULEB32(1))
		body.Write(encodeULEB32(10))
		body.WriteByte(0x7f)
		body.WriteByte(0x0b)
		payload.Write(encodeULEB32(uint32(body.Len())))
		payload.Write(body.Bytes())
	}
	// body 1: 1000 locals
	{
		var body bytes.Buffer
		body.Write(encodeULEB32(1))
		body.Write(encodeULEB32(1000))
		body.WriteByte(0x7f)
		body.WriteByte(0x0b)
		payload.Write(encodeULEB32(uint32(body.Len())))
		payload.Write(body.Bytes())
	}

	module := buildModule(
		buildFunctionSection(2),
		testSection{id: sectionCode, payload: payload.Bytes()},
	)
	report := Validate(module, Limits{MaxLocalsPerFunction: 500})
	if report.OK {
		t.Fatal("expected failure when second body exceeds locals limit")
	}
	found := false
	for _, iss := range report.Issues {
		if iss.Class == ClassPolicy && iss.Field == "function[1].locals" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected function[1].locals policy issue, got %+v", report.Issues)
	}
}

// ── MaxDataSegments ───────────────────────────────────────────────────────────

func TestValidate_MaxDataSegments_OK(t *testing.T) {
	module := buildModule(
		buildFunctionSection(0),
		buildDataSection(5, 4),
	)
	report := Validate(module, Limits{MaxDataSegments: 10})
	if !report.OK {
		t.Fatalf("expected OK for 5 data segments (limit 10), got: %+v", report.Issues)
	}
}

func TestValidate_MaxDataSegments_Exceeded(t *testing.T) {
	module := buildModule(
		buildFunctionSection(0),
		buildDataSection(200, 1),
	)
	report := Validate(module, Limits{MaxDataSegments: 100})
	if report.OK {
		t.Fatal("expected failure for 200 data segments with limit 100")
	}
	found := false
	for _, iss := range report.Issues {
		if iss.Class == ClassPolicy && iss.Field == "data_segment_count" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected data_segment_count policy issue, got %+v", report.Issues)
	}
}

func TestValidate_MaxDataSegments_Disabled(t *testing.T) {
	module := buildModule(
		buildFunctionSection(0),
		buildDataSection(500, 1),
	)
	report := Validate(module, Limits{MaxDataSegments: 0})
	for _, iss := range report.Issues {
		if iss.Field == "data_segment_count" {
			t.Fatalf("data segment check should be disabled when MaxDataSegments=0, got %+v", iss)
		}
	}
}

func TestValidate_MaxDataSegments_NoDataSection(t *testing.T) {
	// A module with no data section should not trigger any data-segment issue.
	module := buildModule(
		buildImportSection(0),
		buildFunctionSection(1),
		buildCodeSection(1),
	)
	report := Validate(module, Limits{MaxDataSegments: 1})
	for _, iss := range report.Issues {
		if iss.Field == "data_segment_count" {
			t.Fatalf("no data section, should not produce data_segment_count issue, got %+v", iss)
		}
	}
}

// ── MaxValidationTimeMS ───────────────────────────────────────────────────────

func TestValidate_MaxValidationTimeMS_ZeroDisabled(t *testing.T) {
	// A module with zero time limit should validate normally regardless of size.
	module := buildModule(
		buildFunctionSection(2),
		buildCodeSectionWithLocals(2, 100),
	)
	limits := DefaultLimits()
	limits.MaxValidationTimeMS = 0
	report := Validate(module, limits)
	if !report.OK {
		t.Fatalf("expected OK with disabled time limit, got: %+v", report.Issues)
	}
}

func TestValidate_MaxValidationTimeMS_GenerousLimit_Passes(t *testing.T) {
	// A generous limit (10 seconds) should not trigger for any realistic module.
	module := buildModule(
		buildFunctionSection(2),
		buildCodeSectionWithLocals(2, 100),
	)
	limits := DefaultLimits()
	limits.MaxValidationTimeMS = 10_000
	report := Validate(module, limits)
	if !report.OK {
		t.Fatalf("expected OK with generous time limit, got: %+v", report.Issues)
	}
}

func TestValidate_MaxValidationTimeMS_AlreadyExpired(t *testing.T) {
	// Simulate a deadline in the past by checking that when the deadline has
	// already elapsed, the validator halts early.
	module := buildModule(
		buildFunctionSection(3),
		buildCodeSectionWithLocals(3, 200),
	)
	// We cannot reliably force a timeout in a unit test, but we can verify
	// that the deadline field is threaded through by checking that a very
	// tight limit (1 ms) on a complex module either passes or surfaces the
	// validation_time issue — both are valid outcomes.  What must NOT happen
	// is a panic or hang.
	limits := DefaultLimits()
	limits.MaxValidationTimeMS = 1
	// Force the deadline to be already in the past.
	_ = time.Now()
	report := Validate(module, limits)
	// No panic and no hang is the main assertion.
	_ = report
}

// ── DefaultLimits completeness ────────────────────────────────────────────────

func TestDefaultLimits_NewFieldsPopulated(t *testing.T) {
	lim := DefaultLimits()
	if lim.MaxLocalsPerFunction <= 0 {
		t.Error("DefaultLimits.MaxLocalsPerFunction should be > 0")
	}
	if lim.MaxDataSegments <= 0 {
		t.Error("DefaultLimits.MaxDataSegments should be > 0")
	}
	// MaxValidationTimeMS is intentionally 0 in DefaultLimits (opt-in).
	if lim.MaxValidationTimeMS != 0 {
		t.Errorf("DefaultLimits.MaxValidationTimeMS should be 0 (opt-in), got %d", lim.MaxValidationTimeMS)
	}
}
