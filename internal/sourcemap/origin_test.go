// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package sourcemap

import (
	"testing"
)

// projectRoot is the canonical project root stub used across tests.
const projectRoot = "/workspace/my-contract"

func classify(t *testing.T, path string) OriginClass {
	t.Helper()
	return NewClassifier(ClassifierOptions{ProjectRoot: projectRoot}).Classify(path)
}

// ── Empty / unknown ──────────────────────────────────────────────────────────

func TestClassify_EmptyPath_Unknown(t *testing.T) {
	if got := classify(t, ""); got != OriginUnknown {
		t.Errorf("empty path: got %q, want %q", got, OriginUnknown)
	}
}

// ── User source ──────────────────────────────────────────────────────────────

func TestClassify_UserSource_UnderProjectRoot(t *testing.T) {
	cases := []string{
		"/workspace/my-contract/src/lib.rs",
		"/workspace/my-contract/src/token.rs",
		"/workspace/my-contract/src/sub/module.rs",
	}
	for _, p := range cases {
		if got := classify(t, p); got != OriginUser {
			t.Errorf("path=%q: got %q, want %q", p, got, OriginUser)
		}
	}
}

func TestClassify_RelativePath_User(t *testing.T) {
	// Relative paths are assumed to be workspace-relative → user.
	if got := classify(t, "src/lib.rs"); got != OriginUser {
		t.Errorf("got %q, want %q", got, OriginUser)
	}
}

func TestClassify_NoProjectRoot_RelativePath_User(t *testing.T) {
	c := NewClassifier(ClassifierOptions{})
	if got := c.Classify("src/lib.rs"); got != OriginUser {
		t.Errorf("got %q, want %q", got, OriginUser)
	}
}

// ── Generated source ─────────────────────────────────────────────────────────

func TestClassify_WasmExtension_Generated(t *testing.T) {
	cases := []string{
		"my_contract.wasm",
		"/project/target/wasm32-unknown-unknown/release/my_contract.wasm",
		"./out/contract.wasm",
	}
	for _, p := range cases {
		if got := classify(t, p); got != OriginGenerated {
			t.Errorf("path=%q: got %q, want %q", p, got, OriginGenerated)
		}
	}
}

func TestClassify_Wasm32TargetDir_Generated(t *testing.T) {
	cases := []string{
		"/project/target/wasm32-unknown-unknown/release/build/my_crate-abc/out/foo.rs",
		"/project/target/wasm32/release/my_contract.wasm",
	}
	for _, p := range cases {
		if got := classify(t, p); got != OriginGenerated {
			t.Errorf("path=%q: got %q, want %q", p, got, OriginGenerated)
		}
	}
}

func TestClassify_GenericTargetDir_Generated(t *testing.T) {
	cases := []string{
		"/project/target/debug/build/foo.rs",
		"/workspace/my-contract/target/release/build/bar.rs",
		"target/debug/build/baz.rs",
	}
	for _, p := range cases {
		if got := classify(t, p); got != OriginGenerated {
			t.Errorf("path=%q: got %q, want %q", p, got, OriginGenerated)
		}
	}
}

func TestClassify_ExtraBuildDir_Generated(t *testing.T) {
	c := NewClassifier(ClassifierOptions{
		ProjectRoot:    projectRoot,
		ExtraBuildDirs: []string{"_generated/", "codegen_out"},
	})
	cases := []string{
		"/project/_generated/contract.rs",
		"/project/codegen_out/tokens.rs",
	}
	for _, p := range cases {
		if got := c.Classify(p); got != OriginGenerated {
			t.Errorf("path=%q: got %q, want %q", p, got, OriginGenerated)
		}
	}
}

// ── External source ──────────────────────────────────────────────────────────

func TestClassify_CargoRegistry_External(t *testing.T) {
	cases := []string{
		"/home/user/.cargo/registry/src/github.com-1ecc6299db9ec823/serde-1.0.0/src/lib.rs",
		"/home/user/.cargo/git/checkouts/soroban-sdk-abc/src/lib.rs",
		"/root/.cargo/registry/src/index.crates.io-6f17d22bba15001f/stellar-sdk-0.9.0/src/lib.rs",
	}
	for _, p := range cases {
		if got := classify(t, p); got != OriginExternal {
			t.Errorf("path=%q: got %q, want %q", p, got, OriginExternal)
		}
	}
}

func TestClassify_AbsoluteOutsideRoot_External(t *testing.T) {
	cases := []string{
		"/usr/lib/stellar/sdk.go",
		"/opt/rust/lib/rustlib/src/rust/library/std/src/lib.rs",
		"/home/other_user/project/src/lib.rs",
	}
	for _, p := range cases {
		if got := classify(t, p); got != OriginExternal {
			t.Errorf("path=%q: got %q, want %q", p, got, OriginExternal)
		}
	}
}

func TestClassify_ExtraExternalPrefix_External(t *testing.T) {
	c := NewClassifier(ClassifierOptions{
		ProjectRoot:           projectRoot,
		ExtraExternalPrefixes: []string{"/vendor/"},
	})
	if got := c.Classify("/vendor/some-crate/src/lib.rs"); got != OriginExternal {
		t.Errorf("got %q, want %q", got, OriginExternal)
	}
}

func TestClassify_NoProjectRoot_AbsoluteOutside_User(t *testing.T) {
	// Without a ProjectRoot configured, absolute paths outside any known
	// build/cargo pattern fall through to OriginUser (safe default).
	c := NewClassifier(ClassifierOptions{})
	if got := c.Classify("/some/random/path/lib.rs"); got != OriginUser {
		t.Errorf("got %q, want %q", got, OriginUser)
	}
}

// ── Label ────────────────────────────────────────────────────────────────────

func TestOriginClass_Label(t *testing.T) {
	cases := []struct {
		class OriginClass
		want  string
	}{
		{OriginUser, ""},
		{OriginGenerated, "[generated]"},
		{OriginExternal, "[external]"},
		{OriginUnknown, "[unknown origin]"},
	}
	for _, tc := range cases {
		if got := tc.class.Label(); got != tc.want {
			t.Errorf("OriginClass(%q).Label() = %q, want %q", tc.class, got, tc.want)
		}
	}
}

// ── ClassifyPath convenience wrapper ─────────────────────────────────────────

func TestClassifyPath_Convenience(t *testing.T) {
	got := ClassifyPath("/workspace/my-contract/src/lib.rs", "/workspace/my-contract")
	if got != OriginUser {
		t.Errorf("ClassifyPath: got %q, want %q", got, OriginUser)
	}
	got = ClassifyPath("target/debug/out.rs", "")
	if got != OriginGenerated {
		t.Errorf("ClassifyPath target/: got %q, want %q", got, OriginGenerated)
	}
}

// ── Windows path handling ─────────────────────────────────────────────────────

func TestClassify_WindowsCargoPath_External(t *testing.T) {
	// Windows-style backslashes are normalised internally.
	p := `C:\Users\dev\.cargo\registry\src\index.crates.io-6f17d22bba15001f\serde-1.0.0\src\lib.rs`
	c := NewClassifier(ClassifierOptions{})
	if got := c.Classify(p); got != OriginExternal {
		t.Errorf("Windows cargo path: got %q, want %q", got, OriginExternal)
	}
}

func TestClassify_WindowsTargetPath_Generated(t *testing.T) {
	p := `C:\Users\dev\project\target\wasm32-unknown-unknown\release\contract.wasm`
	c := NewClassifier(ClassifierOptions{})
	if got := c.Classify(p); got != OriginGenerated {
		t.Errorf("Windows target path: got %q, want %q", got, OriginGenerated)
	}
}
