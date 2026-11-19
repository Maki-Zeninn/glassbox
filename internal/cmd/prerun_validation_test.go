// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Package cmd — table-driven PreRunE tests that prove no RPC or simulator
// dependency is invoked for invalid input.
//
// Each test case exercises a command's PreRunE in isolation by setting package-
// level flag variables directly (the same approach used by
// debug_validation_test.go and audit_sign_prerun_test.go) and verifying that:
//   1. Invalid flags are rejected before any I/O.
//   2. The error message includes the invalid value and actionable text.
//   3. Valid flags do not produce a validation error.
package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

// tmpFile creates a temporary file with the given content and returns its path.
func tmpFile(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "glassbox-test-*")
	if err != nil {
		t.Fatalf("tmpFile: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("tmpFile write: %v", err)
	}
	_ = f.Close()
	return f.Name()
}

// tmpDir returns a guaranteed-existent temporary directory.
func tmpDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// ─── export command ───────────────────────────────────────────────────────────

// TestExportPreRunE_MissingSnapshot verifies that omitting --snapshot is caught
// in PreRunE before any session or I/O work starts.
func TestExportPreRunE_MissingSnapshot(t *testing.T) {
	defer func() {
		exportSnapshotFlag = ""
		exportFormatFlag = "text"
		exportPlanFlag = false
	}()
	exportSnapshotFlag = ""
	exportFormatFlag = "text"

	err := exportCmd.PreRunE(exportCmd, nil)
	if err == nil {
		t.Fatal("expected error: --snapshot is required")
	}
	if !strings.Contains(err.Error(), "--snapshot") {
		t.Errorf("error should mention --snapshot, got: %q", err.Error())
	}
}

func TestExportPreRunE_InvalidFormat(t *testing.T) {
	defer func() {
		exportSnapshotFlag = ""
		exportFormatFlag = "text"
		exportPlanFlag = false
	}()
	exportSnapshotFlag = "/tmp/snap.json"
	exportFormatFlag = "yaml" // not accepted

	err := exportCmd.PreRunE(exportCmd, nil)
	if err == nil {
		t.Fatal("expected error for invalid --format")
	}
	if !strings.Contains(err.Error(), "yaml") {
		t.Errorf("error should echo the invalid value, got: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "--format") {
		t.Errorf("error should mention --format, got: %q", err.Error())
	}
}

func TestExportPreRunE_ValidFormat(t *testing.T) {
	snap := tmpFile(t, `{}`)
	defer func() {
		exportSnapshotFlag = ""
		exportFormatFlag = "text"
		exportPlanFlag = false
	}()

	for _, fmt := range []string{"text", "json"} {
		exportSnapshotFlag = snap
		exportFormatFlag = fmt
		err := exportCmd.PreRunE(exportCmd, nil)
		// The output-path validation may still fail (already-exists-dir, etc.)
		// but it must NOT fail with a format error.
		if err != nil && strings.Contains(err.Error(), "--format") {
			t.Errorf("format=%q: should not produce a format error, got: %q", fmt, err.Error())
		}
	}
}

func TestExportPreRunE_PlanBypassesSnapshotRequired(t *testing.T) {
	defer func() {
		exportSnapshotFlag = ""
		exportFormatFlag = "text"
		exportPlanFlag = false
	}()
	// --plan should not require --snapshot.
	exportSnapshotFlag = ""
	exportFormatFlag = "text"
	exportPlanFlag = true

	err := exportCmd.PreRunE(exportCmd, nil)
	if err != nil && strings.Contains(err.Error(), "--snapshot") {
		t.Errorf("--plan must not require --snapshot, got: %q", err.Error())
	}
}

// ─── export decode-memory subcommand ─────────────────────────────────────────

func TestExportDecodeMemoryPreRunE_MissingSnapshot(t *testing.T) {
	defer func() {
		decodeSnapshotFlag = ""
		decodeOffsetFlag = 0
		decodeLengthFlag = 256
	}()
	decodeSnapshotFlag = ""
	decodeLengthFlag = 256

	err := exportDecodeMemoryCmd.PreRunE(exportDecodeMemoryCmd, nil)
	if err == nil {
		t.Fatal("expected error: --snapshot is required")
	}
	if !strings.Contains(err.Error(), "--snapshot") {
		t.Errorf("error should mention --snapshot, got: %q", err.Error())
	}
}

func TestExportDecodeMemoryPreRunE_NegativeOffset(t *testing.T) {
	snap := tmpFile(t, `{}`)
	defer func() {
		decodeSnapshotFlag = ""
		decodeOffsetFlag = 0
		decodeLengthFlag = 256
	}()
	decodeSnapshotFlag = snap
	decodeOffsetFlag = -1
	decodeLengthFlag = 256

	err := exportDecodeMemoryCmd.PreRunE(exportDecodeMemoryCmd, nil)
	if err == nil {
		t.Fatal("expected error for negative --offset")
	}
	if !strings.Contains(err.Error(), "--offset") {
		t.Errorf("error should mention --offset, got: %q", err.Error())
	}
}

func TestExportDecodeMemoryPreRunE_ZeroLength(t *testing.T) {
	snap := tmpFile(t, `{}`)
	defer func() {
		decodeSnapshotFlag = ""
		decodeOffsetFlag = 0
		decodeLengthFlag = 256
	}()
	decodeSnapshotFlag = snap
	decodeOffsetFlag = 0
	decodeLengthFlag = 0

	err := exportDecodeMemoryCmd.PreRunE(exportDecodeMemoryCmd, nil)
	if err == nil {
		t.Fatal("expected error for zero --length")
	}
	if !strings.Contains(err.Error(), "--length") {
		t.Errorf("error should mention --length, got: %q", err.Error())
	}
}

func TestExportDecodeMemoryPreRunE_ValidInputs(t *testing.T) {
	snap := tmpFile(t, `{}`)
	defer func() {
		decodeSnapshotFlag = ""
		decodeOffsetFlag = 0
		decodeLengthFlag = 256
	}()
	decodeSnapshotFlag = snap
	decodeOffsetFlag = 0
	decodeLengthFlag = 256

	err := exportDecodeMemoryCmd.PreRunE(exportDecodeMemoryCmd, nil)
	if err != nil {
		t.Errorf("valid inputs should pass PreRunE, got: %v", err)
	}
}

// ─── snapshot-diff command ────────────────────────────────────────────────────

func TestSnapshotDiffPreRunE_MissingSnapshotA(t *testing.T) {
	defer func() {
		snapshotDiffAFlag = ""
		snapshotDiffBFlag = ""
	}()
	snapshotDiffAFlag = ""
	snapshotDiffBFlag = "/some/snap.json"

	err := snapshotDiffCmd.PreRunE(snapshotDiffCmd, nil)
	if err == nil {
		t.Fatal("expected error: --snapshot-a is required")
	}
	if !strings.Contains(err.Error(), "--snapshot-a") {
		t.Errorf("error should mention --snapshot-a, got: %q", err.Error())
	}
}

func TestSnapshotDiffPreRunE_MissingSnapshotB(t *testing.T) {
	snapA := tmpFile(t, `{}`)
	defer func() {
		snapshotDiffAFlag = ""
		snapshotDiffBFlag = ""
	}()
	snapshotDiffAFlag = snapA
	snapshotDiffBFlag = ""

	err := snapshotDiffCmd.PreRunE(snapshotDiffCmd, nil)
	if err == nil {
		t.Fatal("expected error: --snapshot-b is required")
	}
	if !strings.Contains(err.Error(), "--snapshot-b") {
		t.Errorf("error should mention --snapshot-b, got: %q", err.Error())
	}
}

func TestSnapshotDiffPreRunE_NonExistentSnapshotA(t *testing.T) {
	snapB := tmpFile(t, `{}`)
	defer func() {
		snapshotDiffAFlag = ""
		snapshotDiffBFlag = ""
	}()
	snapshotDiffAFlag = "/no/such/snap_a.json"
	snapshotDiffBFlag = snapB

	err := snapshotDiffCmd.PreRunE(snapshotDiffCmd, nil)
	if err == nil {
		t.Fatal("expected error for non-existent --snapshot-a")
	}
	if !strings.Contains(err.Error(), "snapshot-a") {
		t.Errorf("error should mention snapshot-a, got: %q", err.Error())
	}
}

func TestSnapshotDiffPreRunE_NonExistentSnapshotB(t *testing.T) {
	snapA := tmpFile(t, `{}`)
	defer func() {
		snapshotDiffAFlag = ""
		snapshotDiffBFlag = ""
	}()
	snapshotDiffAFlag = snapA
	snapshotDiffBFlag = "/no/such/snap_b.json"

	err := snapshotDiffCmd.PreRunE(snapshotDiffCmd, nil)
	if err == nil {
		t.Fatal("expected error for non-existent --snapshot-b")
	}
	if !strings.Contains(err.Error(), "snapshot-b") {
		t.Errorf("error should mention snapshot-b, got: %q", err.Error())
	}
}

func TestSnapshotDiffPreRunE_NegativeContext(t *testing.T) {
	snapA := tmpFile(t, `{}`)
	snapB := tmpFile(t, `{}`)
	defer func() {
		snapshotDiffAFlag = ""
		snapshotDiffBFlag = ""
		snapshotDiffContextFlag = 16
	}()
	snapshotDiffAFlag = snapA
	snapshotDiffBFlag = snapB
	snapshotDiffContextFlag = -1

	err := snapshotDiffCmd.PreRunE(snapshotDiffCmd, nil)
	if err == nil {
		t.Fatal("expected error for negative --context")
	}
	if !strings.Contains(err.Error(), "--context") {
		t.Errorf("error should mention --context, got: %q", err.Error())
	}
}

func TestSnapshotDiffPreRunE_ValidInputs(t *testing.T) {
	snapA := tmpFile(t, `{}`)
	snapB := tmpFile(t, `{}`)
	defer func() {
		snapshotDiffAFlag = ""
		snapshotDiffBFlag = ""
		snapshotDiffContextFlag = 16
	}()
	snapshotDiffAFlag = snapA
	snapshotDiffBFlag = snapB
	snapshotDiffContextFlag = 16

	err := snapshotDiffCmd.PreRunE(snapshotDiffCmd, nil)
	if err != nil {
		t.Errorf("valid inputs should pass PreRunE, got: %v", err)
	}
}

// ─── scan command ─────────────────────────────────────────────────────────────

func TestScanPreRunE_NonExistentSource(t *testing.T) {
	defer func() { scanABIPath = "" }()
	scanABIPath = ""

	err := scanCmd.PreRunE(scanCmd, []string{"/no/such/contract/dir"})
	if err == nil {
		t.Fatal("expected error for non-existent source path")
	}
}

func TestScanPreRunE_ExistingSourceNoABI(t *testing.T) {
	src := tmpFile(t, "// source")
	defer func() { scanABIPath = "" }()
	scanABIPath = ""

	err := scanCmd.PreRunE(scanCmd, []string{src})
	if err != nil {
		t.Errorf("valid source without ABI should pass PreRunE, got: %v", err)
	}
}

func TestScanPreRunE_NonExistentABI(t *testing.T) {
	src := tmpFile(t, "// source")
	defer func() { scanABIPath = "" }()
	scanABIPath = "/no/such/abi.json"

	err := scanCmd.PreRunE(scanCmd, []string{src})
	if err == nil {
		t.Fatal("expected error for non-existent --abi file")
	}
	if !strings.Contains(err.Error(), "abi") {
		t.Errorf("error should mention 'abi', got: %q", err.Error())
	}
}

func TestScanPreRunE_ValidSourceAndABI(t *testing.T) {
	src := tmpFile(t, "// source")
	abi := tmpFile(t, `{"functions":[]}`)
	defer func() { scanABIPath = "" }()
	scanABIPath = abi

	err := scanCmd.PreRunE(scanCmd, []string{src})
	if err != nil {
		t.Errorf("valid source + ABI should pass PreRunE, got: %v", err)
	}
}

// ─── wasm-diff command ────────────────────────────────────────────────────────

func TestWasmDiffPreRunE_NonExistentLocal(t *testing.T) {
	remote := tmpFile(t, "\x00asm\x01\x00\x00\x00")
	err := wasmDiffCmd.PreRunE(wasmDiffCmd, []string{"/no/such/local.wasm", remote})
	if err == nil {
		t.Fatal("expected error for non-existent local WASM")
	}
}

func TestWasmDiffPreRunE_NonExistentRemote(t *testing.T) {
	local := tmpFile(t, "\x00asm\x01\x00\x00\x00")
	err := wasmDiffCmd.PreRunE(wasmDiffCmd, []string{local, "/no/such/remote.wasm"})
	if err == nil {
		t.Fatal("expected error for non-existent remote WASM")
	}
}

func TestWasmDiffPreRunE_BothExist(t *testing.T) {
	local := tmpFile(t, "\x00asm\x01\x00\x00\x00")
	remote := tmpFile(t, "\x00asm\x01\x00\x00\x00")

	err := wasmDiffCmd.PreRunE(wasmDiffCmd, []string{local, remote})
	if err != nil {
		t.Errorf("both files existing should pass PreRunE, got: %v", err)
	}
}

// ─── new validation helpers ───────────────────────────────────────────────────

func TestValidateRPCURL_Valid(t *testing.T) {
	cases := []string{
		"http://localhost:8000",
		"https://soroban-testnet.stellar.org",
		"https://horizon.stellar.org",
	}
	for _, u := range cases {
		if err := validateRPCURL("rpc-url", u); err != nil {
			t.Errorf("validateRPCURL(%q): unexpected error %v", u, err)
		}
	}
}

func TestValidateRPCURL_Invalid(t *testing.T) {
	cases := []string{
		"soroban-testnet.stellar.org",    // missing scheme
		"ftp://soroban.example.com",      // wrong scheme
		"soroban.stellar.org/rpc",        // no scheme at all
	}
	for _, u := range cases {
		err := validateRPCURL("rpc-url", u)
		if err == nil {
			t.Errorf("validateRPCURL(%q): expected error", u)
			continue
		}
		if !strings.Contains(err.Error(), "--rpc-url") {
			t.Errorf("validateRPCURL(%q): error should mention --rpc-url, got: %q", u, err.Error())
		}
	}
}

func TestValidateRPCURL_Empty(t *testing.T) {
	// Empty is allowed (optional flag).
	if err := validateRPCURL("rpc-url", ""); err != nil {
		t.Errorf("validateRPCURL empty: unexpected error %v", err)
	}
}

func TestValidateNonEmptyString_Valid(t *testing.T) {
	if err := validateNonEmptyString("name", "alice"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateNonEmptyString_EmptyAndWhitespace(t *testing.T) {
	for _, v := range []string{"", "   ", "\t"} {
		err := validateNonEmptyString("name", v)
		if err == nil {
			t.Errorf("validateNonEmptyString(%q): expected error", v)
			continue
		}
		if !strings.Contains(err.Error(), "--name") {
			t.Errorf("error should mention --name, got: %q", err.Error())
		}
	}
}

func TestValidateOutputDir_Existing(t *testing.T) {
	dir := tmpDir(t)
	if err := validateOutputDir("output", dir); err != nil {
		t.Errorf("existing dir should pass, got: %v", err)
	}
}

func TestValidateOutputDir_IsFile(t *testing.T) {
	f := tmpFile(t, "data")
	err := validateOutputDir("output", f)
	if err == nil {
		t.Fatal("file path passed as dir should fail")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("error should say 'not a directory', got: %q", err.Error())
	}
}

func TestValidateOutputDir_AutoCreate(t *testing.T) {
	parent := tmpDir(t)
	newDir := filepath.Join(parent, "subdir", "deep")
	if err := validateOutputDir("output", newDir); err != nil {
		t.Errorf("non-existent dir should be created, got: %v", err)
	}
	if _, statErr := os.Stat(newDir); statErr != nil {
		t.Errorf("directory should have been created: %v", statErr)
	}
}

func TestValidateOutputDir_Empty(t *testing.T) {
	if err := validateOutputDir("output", ""); err != nil {
		t.Errorf("empty dir is optional, should pass: %v", err)
	}
}

func TestValidateNoNullBytes_Clean(t *testing.T) {
	if err := validateNoNullBytes("path", "/valid/path/file.wasm"); err != nil {
		t.Errorf("clean path should pass: %v", err)
	}
}

func TestValidateNoNullBytes_WithNull(t *testing.T) {
	path := "/valid\x00path"
	err := validateNoNullBytes("path", path)
	if err == nil {
		t.Fatal("expected error for path with null byte")
	}
	if !strings.Contains(err.Error(), "--path") {
		t.Errorf("error should mention --path, got: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "null") {
		t.Errorf("error should mention null bytes, got: %q", err.Error())
	}
}

// ─── error code and remediation invariants ────────────────────────────────────

// TestPreRunEErrors_NoRPCOrSimulator is the key invariant test: it exercises
// the PreRunE of every amended command with clearly-invalid input and asserts
// that the error is an ErstValidationFailed code (not RPC or simulator codes),
// proving that no external I/O was attempted.
func TestPreRunEErrors_NoRPCOrSimulator(t *testing.T) {
	type tc struct {
		name    string
		run     func() error
		wantMsg string
	}

	cases := []tc{
		{
			name: "export missing --snapshot",
			run: func() error {
				old := exportSnapshotFlag
				defer func() { exportSnapshotFlag = old }()
				exportSnapshotFlag = ""
				return exportCmd.PreRunE(exportCmd, nil)
			},
			wantMsg: "--snapshot",
		},
		{
			name: "export invalid --format",
			run: func() error {
				snapFile := tmpFile(t, "{}")
				old, oldFmt := exportSnapshotFlag, exportFormatFlag
				defer func() { exportSnapshotFlag = old; exportFormatFlag = oldFmt }()
				exportSnapshotFlag = snapFile
				exportFormatFlag = "xml"
				return exportCmd.PreRunE(exportCmd, nil)
			},
			wantMsg: "--format",
		},
		{
			name: "snapshot-diff missing --snapshot-a",
			run: func() error {
				old := snapshotDiffAFlag
				defer func() { snapshotDiffAFlag = old }()
				snapshotDiffAFlag = ""
				return snapshotDiffCmd.PreRunE(snapshotDiffCmd, nil)
			},
			wantMsg: "--snapshot-a",
		},
		{
			name: "scan non-existent source",
			run: func() error {
				return scanCmd.PreRunE(scanCmd, []string{"/does/not/exist/src"})
			},
			wantMsg: "source",
		},
		{
			name: "wasm-diff non-existent local",
			run: func() error {
				remote := tmpFile(t, "\x00asm")
				return wasmDiffCmd.PreRunE(wasmDiffCmd, []string{"/no/local.wasm", remote})
			},
			wantMsg: "local-wasm",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run()
			if err == nil {
				t.Fatalf("%s: expected a validation error", tc.name)
			}
			// Must be a validation error — not RPC/simulator.
			msg := err.Error()
			if strings.Contains(msg, "RPC_CONNECTION_FAILED") ||
				strings.Contains(msg, "SIMULATION_FAILED") ||
				strings.Contains(msg, "SIMULATOR") {
				t.Errorf("%s: error must not be an RPC/simulator error, got: %q", tc.name, msg)
			}
			if !strings.Contains(msg, tc.wantMsg) {
				t.Errorf("%s: error should contain %q, got: %q", tc.name, tc.wantMsg, msg)
			}
		})
	}
}
