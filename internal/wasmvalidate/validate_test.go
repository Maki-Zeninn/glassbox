// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package wasmvalidate

import (
	"bytes"
	"testing"
)

var wasmMagic = []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}

type testSection struct {
	id      byte
	payload []byte
}

func buildModule(sections ...testSection) []byte {
	var out bytes.Buffer
	out.Write(wasmMagic)
	for _, s := range sections {
		out.WriteByte(s.id)
		out.Write(encodeULEB32(uint32(len(s.payload))))
		out.Write(s.payload)
	}
	return out.Bytes()
}

func encodeULEB32(v uint32) []byte {
	var out []byte
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			b |= 0x80
		}
		out = append(out, b)
		if v == 0 {
			break
		}
	}
	return out
}

func encodeString(s string) []byte {
	var out bytes.Buffer
	out.Write(encodeULEB32(uint32(len(s))))
	out.WriteString(s)
	return out.Bytes()
}

func buildImportSection(funcImports int) testSection {
	var payload bytes.Buffer
	payload.Write(encodeULEB32(uint32(funcImports)))
	for i := 0; i < funcImports; i++ {
		payload.Write(encodeString("env"))
		payload.Write(encodeString("f"))
		payload.WriteByte(0x00) // kind: function
		payload.Write(encodeULEB32(0))
	}
	return testSection{id: sectionImport, payload: payload.Bytes()}
}

func buildFunctionSection(count int) testSection {
	var payload bytes.Buffer
	payload.Write(encodeULEB32(uint32(count)))
	for i := 0; i < count; i++ {
		payload.Write(encodeULEB32(0))
	}
	return testSection{id: sectionFunction, payload: payload.Bytes()}
}

func buildCodeSection(bodies int) testSection {
	var payload bytes.Buffer
	payload.Write(encodeULEB32(uint32(bodies)))
	for i := 0; i < bodies; i++ {
		body := []byte{0x00, 0x0b} // no locals, end
		payload.Write(encodeULEB32(uint32(len(body))))
		payload.Write(body)
	}
	return testSection{id: sectionCode, payload: payload.Bytes()}
}

func buildCustomSection(name string, contentSize int) testSection {
	var payload bytes.Buffer
	payload.Write(encodeString(name))
	payload.Write(make([]byte, contentSize))
	return testSection{id: sectionCustom, payload: payload.Bytes()}
}

func TestValidate_ValidMinimalModule(t *testing.T) {
	module := buildModule(
		buildImportSection(1),
		buildFunctionSection(2),
		buildCodeSection(2),
	)
	report := Validate(module, DefaultLimits())
	if !report.OK {
		t.Fatalf("expected OK, got issues: %+v", report.Issues)
	}
	if len(report.Issues) != 0 {
		t.Fatalf("expected zero issues, got %d", len(report.Issues))
	}
	if report.SectionCount != 3 {
		t.Fatalf("expected 3 sections, got %d", report.SectionCount)
	}
}

func TestValidate_EmptyInput(t *testing.T) {
	report := Validate(nil, DefaultLimits())
	if report.OK {
		t.Fatalf("expected failure for empty input")
	}
	if report.Issues[0].Class != ClassStructural || report.Issues[0].Field != "header" {
		t.Fatalf("unexpected issue: %+v", report.Issues[0])
	}
}

func TestValidate_BadMagic(t *testing.T) {
	module := []byte{0xde, 0xad, 0xbe, 0xef, 0x01, 0x00, 0x00, 0x00}
	report := Validate(module, DefaultLimits())
	if report.OK {
		t.Fatalf("expected failure for bad magic")
	}
	if report.Issues[0].Field != "magic" || report.Issues[0].Class != ClassStructural {
		t.Fatalf("unexpected issue: %+v", report.Issues[0])
	}
}

func TestValidate_UnsupportedVersion(t *testing.T) {
	module := []byte{0x00, 0x61, 0x73, 0x6d, 0x02, 0x00, 0x00, 0x00}
	report := Validate(module, DefaultLimits())
	if report.OK || report.Issues[0].Field != "version" {
		t.Fatalf("expected version issue, got %+v", report)
	}
}

func TestValidate_TruncatedSection(t *testing.T) {
	// Section header claims a large size but the module ends early.
	module := append(append([]byte{}, wasmMagic...), 0x01, 0xff, 0xff, 0x03)
	report := Validate(module, DefaultLimits())
	if report.OK {
		t.Fatalf("expected failure for truncated section")
	}
	if report.Issues[0].Class != ClassStructural {
		t.Fatalf("expected structural failure, got %+v", report.Issues[0])
	}
}

func TestValidate_MalformedSizeVarint(t *testing.T) {
	// A size varint with the continuation bit set on every byte through the
	// 5-byte cap never terminates -> malformed.
	module := append(append([]byte{}, wasmMagic...), 0x01, 0x80, 0x80, 0x80, 0x80, 0x80)
	report := Validate(module, DefaultLimits())
	if report.OK {
		t.Fatalf("expected failure for malformed size varint")
	}
	if report.Issues[0].Class != ClassStructural {
		t.Fatalf("expected structural failure, got %+v", report.Issues[0])
	}
}

func TestValidate_OversizedModule(t *testing.T) {
	module := buildModule(buildImportSection(0))
	report := Validate(module, Limits{MaxModuleSize: len(module) - 1})
	if report.OK {
		t.Fatalf("expected failure for oversized module")
	}
	if report.Issues[0].Field != "module_size" || report.Issues[0].Class != ClassPolicy {
		t.Fatalf("unexpected issue: %+v", report.Issues[0])
	}
}

func TestValidate_TooManySections(t *testing.T) {
	module := buildModule(buildImportSection(0), buildFunctionSection(0), buildCodeSection(0))
	report := Validate(module, Limits{MaxSectionCount: 1})
	if report.OK {
		t.Fatalf("expected failure for too many sections")
	}
	found := false
	for _, iss := range report.Issues {
		if iss.Field == "section_count" && iss.Class == ClassPolicy {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected section_count policy issue, got %+v", report.Issues)
	}
}

func TestValidate_FunctionCodeCountMismatch(t *testing.T) {
	module := buildModule(buildFunctionSection(3), buildCodeSection(2))
	report := Validate(module, DefaultLimits())
	if report.OK {
		t.Fatalf("expected failure for function/code count mismatch")
	}
	if report.Issues[0].Field != "function_index" || report.Issues[0].Class != ClassStructural {
		t.Fatalf("unexpected issue: %+v", report.Issues[0])
	}
}

func TestValidate_TooManyFunctions(t *testing.T) {
	module := buildModule(buildImportSection(2), buildFunctionSection(3), buildCodeSection(3))
	report := Validate(module, Limits{MaxFunctionCount: 4})
	if report.OK {
		t.Fatalf("expected failure for too many functions")
	}
	if report.Issues[0].Field != "function_count" || report.Issues[0].Class != ClassPolicy {
		t.Fatalf("unexpected issue: %+v", report.Issues[0])
	}
}

func TestValidate_OversizedDebugSection(t *testing.T) {
	module := buildModule(buildCustomSection(".debug_info", 100))
	report := Validate(module, Limits{MaxDebugSectionSize: 10})
	if report.OK {
		t.Fatalf("expected failure for oversized debug section")
	}
	if report.Issues[0].Class != ClassPolicy {
		t.Fatalf("unexpected issue: %+v", report.Issues[0])
	}
}

func TestValidate_NonDebugCustomSectionIgnoresDebugLimit(t *testing.T) {
	module := buildModule(buildCustomSection("name", 100))
	report := Validate(module, Limits{MaxDebugSectionSize: 10})
	if !report.OK {
		t.Fatalf("expected OK, non-debug custom sections should not be bounded by MaxDebugSectionSize: %+v", report.Issues)
	}
}

func TestSections_Standalone(t *testing.T) {
	module := buildModule(buildImportSection(1), buildFunctionSection(1), buildCodeSection(1))
	sections, issues := Sections(module, DefaultLimits())
	if len(issues) != 0 {
		t.Fatalf("unexpected issues: %+v", issues)
	}
	if len(sections) != 3 {
		t.Fatalf("expected 3 sections, got %d", len(sections))
	}
	if sections[0].ID != sectionImport || sections[1].ID != sectionFunction || sections[2].ID != sectionCode {
		t.Fatalf("unexpected section order: %+v", sections)
	}
}

func TestReport_Error(t *testing.T) {
	var ok *Report
	if err := ok.Error(); err != nil {
		t.Fatalf("nil report should have nil error")
	}
	report := Validate(nil, DefaultLimits())
	if err := report.Error(); err == nil {
		t.Fatalf("expected non-nil error for failing report")
	}
}
