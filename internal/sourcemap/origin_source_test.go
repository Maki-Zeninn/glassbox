// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package sourcemap

// origin_source_test.go exercises the SourceOrigin int enum, ClassifySourcePath,
// ExtractGitOrigin / parseCargoLockForGitOrigin, ExternalURLForResult,
// docsRSURLForPath, and gitHubURLForCargoGit.
//
// It is intentionally kept in the same package (sourcemap) so it can test
// unexported helpers (parseCargoLockForGitOrigin, docsRSURLForPath, etc.)
// without exposing them in the public API.

import (
	"fmt"
	"strings"
	"testing"
)

// ─── SourceOrigin.OriginPrefix ────────────────────────────────────────────────

func TestSourceOrigin_OriginPrefix(t *testing.T) {
	cases := []struct {
		origin SourceOrigin
		want   string
	}{
		{OriginLocal, "[local]"},
		{OriginRegistry, "[crates.io]"},
		{OriginGit, "[git]"},
		{OriginUnknownSource, ""},
	}
	for _, tc := range cases {
		got := tc.origin.OriginPrefix()
		if got != tc.want {
			t.Errorf("SourceOrigin(%d).OriginPrefix() = %q, want %q", tc.origin, got, tc.want)
		}
	}
}

// ─── ClassifySourcePath ───────────────────────────────────────────────────────

func TestClassifySourcePath_EmptyPath(t *testing.T) {
	if got := ClassifySourcePath("", "/workspace/my-contract", ""); got != OriginUnknownSource {
		t.Errorf("empty path: got %v, want OriginUnknownSource", got)
	}
}

func TestClassifySourcePath_LocalWorkspace(t *testing.T) {
	root := "/workspace/my-contract"
	cases := []string{
		"/workspace/my-contract/src/lib.rs",
		"/workspace/my-contract/src/token.rs",
		"/workspace/my-contract/src/sub/module.rs",
	}
	for _, p := range cases {
		if got := ClassifySourcePath(p, root, ""); got != OriginLocal {
			t.Errorf("path=%q: got %v, want OriginLocal", p, got)
		}
	}
}

func TestClassifySourcePath_RelativePath_Local(t *testing.T) {
	// Relative paths are workspace-relative → local.
	if got := ClassifySourcePath("src/lib.rs", "/workspace/my-contract", ""); got != OriginLocal {
		t.Errorf("relative path: got %v, want OriginLocal", got)
	}
}

func TestClassifySourcePath_NoWorkspaceRoot_RelativePath_Local(t *testing.T) {
	// Without a root, conservative default is OriginLocal.
	if got := ClassifySourcePath("src/lib.rs", "", ""); got != OriginLocal {
		t.Errorf("no root + relative: got %v, want OriginLocal", got)
	}
}

func TestClassifySourcePath_RegistryCrate_Unix(t *testing.T) {
	cases := []string{
		"/home/user/.cargo/registry/src/index.crates.io-6f17d22bba15001f/serde-1.0.203/src/lib.rs",
		"/root/.cargo/registry/src/github.com-1ecc6299db9ec823/stellar-sdk-0.9.0/src/lib.rs",
	}
	for _, p := range cases {
		got := ClassifySourcePath(p, "/workspace/my-contract", "")
		if got != OriginRegistry {
			t.Errorf("registry path=%q: got %v, want OriginRegistry", p, got)
		}
	}
}

func TestClassifySourcePath_GitDependency_Unix(t *testing.T) {
	cases := []string{
		"/home/user/.cargo/git/checkouts/soroban-sdk-abc123/def456/src/lib.rs",
		"/root/.cargo/git/checkouts/rs-soroban-sdk-deadbeef/cafebabe/src/token.rs",
	}
	for _, p := range cases {
		got := ClassifySourcePath(p, "/workspace/my-contract", "")
		if got != OriginGit {
			t.Errorf("git path=%q: got %v, want OriginGit", p, got)
		}
	}
}

func TestClassifySourcePath_CargoHome_Registry(t *testing.T) {
	cargoHome := "/custom/cargo-home"
	p := "/custom/cargo-home/registry/src/index.crates.io-abc/tokio-1.38.0/src/lib.rs"
	if got := ClassifySourcePath(p, "/workspace", cargoHome); got != OriginRegistry {
		t.Errorf("custom CARGO_HOME registry: got %v, want OriginRegistry", got)
	}
}

func TestClassifySourcePath_CargoHome_Git(t *testing.T) {
	cargoHome := "/custom/cargo-home"
	p := "/custom/cargo-home/git/checkouts/my-crate-abc/def/src/lib.rs"
	if got := ClassifySourcePath(p, "/workspace", cargoHome); got != OriginGit {
		t.Errorf("custom CARGO_HOME git: got %v, want OriginGit", got)
	}
}

func TestClassifySourcePath_AbsoluteOutsideRoot_Unknown(t *testing.T) {
	// Absolute path that is outside the workspace root and not a Cargo path
	// → OriginUnknownSource (cannot tell if it's a dep without cargo patterns).
	got := ClassifySourcePath("/usr/lib/some/system.rs", "/workspace/my-contract", "")
	if got != OriginUnknownSource {
		t.Errorf("absolute outside root: got %v, want OriginUnknownSource", got)
	}
}

func TestClassifySourcePath_WindowsRegistryPath(t *testing.T) {
	p := `C:\Users\dev\.cargo\registry\src\index.crates.io-6f17d22bba15001f\serde-1.0.0\src\lib.rs`
	got := ClassifySourcePath(p, `C:\Users\dev\project`, "")
	if got != OriginRegistry {
		t.Errorf("Windows registry path: got %v, want OriginRegistry", got)
	}
}

func TestClassifySourcePath_WindowsGitPath(t *testing.T) {
	p := `C:\Users\dev\.cargo\git\checkouts\soroban-sdk-abc\def\src\lib.rs`
	got := ClassifySourcePath(p, `C:\Users\dev\project`, "")
	if got != OriginGit {
		t.Errorf("Windows git path: got %v, want OriginGit", got)
	}
}

// ─── parseCargoLockForGitOrigin ───────────────────────────────────────────────

// sampleCargoLock is a realistic Cargo.lock v3 fragment with one registry and
// one git-sourced dependency.
const sampleCargoLock = `
# This file is automatically @generated by Cargo.
# It is not intended for manual editing.
version = 3

[[package]]
name = "my-contract"
version = "0.1.0"

[[package]]
name = "serde"
version = "1.0.203"
source = "registry+https://github.com/rust-lang/crates.io-index"
checksum = "7253ab4de971e72fb7be983802300c30b5a7f0c2e56fab8abfc6a214307c0094"

[[package]]
name = "soroban-sdk"
version = "21.7.6"
source = "git+https://github.com/stellar/rs-soroban-sdk?rev=a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2#a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"
checksum = "abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdef0123456789abcdef01"

[[package]]
name = "soroban-env-host"
version = "21.7.6"
source = "git+https://github.com/stellar/rs-soroban-env?branch=main#deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
`

func TestParseCargoLockForGitOrigin_HappyPath(t *testing.T) {
	sourcePath := "/home/user/.cargo/git/checkouts/soroban-sdk-abc123/def456/src/lib.rs"
	info, err := parseCargoLockForGitOrigin(sourcePath, sampleCargoLock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantRepo := "https://github.com/stellar/rs-soroban-sdk"
	if info.RepoURL != wantRepo {
		t.Errorf("RepoURL = %q, want %q", info.RepoURL, wantRepo)
	}
	wantHash := "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"
	if info.CommitHash != wantHash {
		t.Errorf("CommitHash = %q, want %q", info.CommitHash, wantHash)
	}
}

func TestParseCargoLockForGitOrigin_BranchSource(t *testing.T) {
	// soroban-env-host uses a branch= source; hash comes from the fragment.
	// The checkout directory "soroban-env-host-abc12345" matches the crate name
	// "soroban-env-host" via the HasPrefix("soroban-env-host-abc12345", "soroban-env-host-") check.
	sourcePath := "/home/user/.cargo/git/checkouts/soroban-env-host-abc12345/def/src/lib.rs"
	info, err := parseCargoLockForGitOrigin(sourcePath, sampleCargoLock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantRepo := "https://github.com/stellar/rs-soroban-env"
	if info.RepoURL != wantRepo {
		t.Errorf("RepoURL = %q, want %q", info.RepoURL, wantRepo)
	}
	wantHash := "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	if info.CommitHash != wantHash {
		t.Errorf("CommitHash = %q, want %q", info.CommitHash, wantHash)
	}
}

func TestParseCargoLockForGitOrigin_NoGitEntries(t *testing.T) {
	lockWithNoGit := `
[[package]]
name = "serde"
version = "1.0.203"
source = "registry+https://github.com/rust-lang/crates.io-index"
checksum = "abc"
`
	_, err := parseCargoLockForGitOrigin(
		"/home/user/.cargo/git/checkouts/anything/src/lib.rs",
		lockWithNoGit,
	)
	if err == nil {
		t.Error("expected error for lock with no git entries, got nil")
	}
}

func TestParseCargoLockForGitOrigin_EmptyLock(t *testing.T) {
	_, err := parseCargoLockForGitOrigin("/home/user/.cargo/git/checkouts/x/y/src/lib.rs", "")
	if err == nil {
		t.Error("expected error for empty Cargo.lock, got nil")
	}
}

// TestExtractGitOrigin_FromFile tests ExtractGitOrigin end-to-end using a
// temporary file so the filesystem read path is also exercised.
func TestExtractGitOrigin_FromFile(t *testing.T) {
	// Swap osReadFile so we don't need a real file.
	original := osReadFile
	t.Cleanup(func() { osReadFile = original })
	osReadFile = func(path string) ([]byte, error) {
		if path == "/tmp/test-Cargo.lock" {
			return []byte(sampleCargoLock), nil
		}
		return nil, fmt.Errorf("unexpected path: %s", path)
	}

	sourcePath := "/home/user/.cargo/git/checkouts/soroban-sdk-abc123/def456/src/lib.rs"
	info, err := ExtractGitOrigin(sourcePath, "/tmp/test-Cargo.lock")
	if err != nil {
		t.Fatalf("ExtractGitOrigin: %v", err)
	}
	if !strings.Contains(info.RepoURL, "stellar/rs-soroban-sdk") {
		t.Errorf("RepoURL = %q, want to contain stellar/rs-soroban-sdk", info.RepoURL)
	}
	if len(info.CommitHash) < 40 {
		t.Errorf("CommitHash = %q, want at least 40 chars", info.CommitHash)
	}
}

func TestExtractGitOrigin_EmptyLockPath(t *testing.T) {
	_, err := ExtractGitOrigin("/some/path/lib.rs", "")
	if err == nil {
		t.Error("expected error for empty cargoLockPath, got nil")
	}
}

func TestExtractGitOrigin_EmptySourcePath(t *testing.T) {
	_, err := ExtractGitOrigin("", "/tmp/Cargo.lock")
	if err == nil {
		t.Error("expected error for empty sourcePath, got nil")
	}
}

// ─── parseGitSource ───────────────────────────────────────────────────────────

func TestParseGitSource_WithRevAndFragment(t *testing.T) {
	src := "git+https://github.com/stellar/rs-soroban-sdk?rev=a1b2c3d#a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"
	repo, hash := parseGitSource(src, "")
	if repo != "https://github.com/stellar/rs-soroban-sdk" {
		t.Errorf("repo = %q", repo)
	}
	if hash != "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2" {
		t.Errorf("hash = %q", hash)
	}
}

func TestParseGitSource_FragmentOnly(t *testing.T) {
	src := "git+https://github.com/owner/repo#deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	repo, hash := parseGitSource(src, "")
	if repo != "https://github.com/owner/repo" {
		t.Errorf("repo = %q", repo)
	}
	if hash != "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef" {
		t.Errorf("hash = %q", hash)
	}
}

func TestParseGitSource_DotGitSuffix_Stripped(t *testing.T) {
	src := "git+https://github.com/owner/repo.git#deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	repo, _ := parseGitSource(src, "")
	if strings.HasSuffix(repo, ".git") {
		t.Errorf("repo %q should not end with .git", repo)
	}
}

func TestParseGitSource_ShortRevFallsBackToChecksum(t *testing.T) {
	// Fragment hash is shorter than 40 chars; checksum provides the full SHA.
	src := "git+https://github.com/owner/repo?rev=abc#shortref"
	checksum := "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab"
	_, hash := parseGitSource(src, checksum)
	if len(hash) < 40 {
		t.Errorf("expected hash of len >= 40 from checksum fallback, got %q (len %d)", hash, len(hash))
	}
}

// ─── docsRSURLForPath ─────────────────────────────────────────────────────────

func TestDocsRSURLForPath_StandardLayout(t *testing.T) {
	p := "/home/user/.cargo/registry/src/index.crates.io-6f17d22bba15001f/serde-1.0.203/src/lib.rs"
	got := docsRSURLForPath(p)
	if !strings.Contains(got, "docs.rs") {
		t.Errorf("docsRSURLForPath: got %q, want docs.rs URL", got)
	}
	if !strings.Contains(got, "serde") {
		t.Errorf("docsRSURLForPath: got %q, want serde in URL", got)
	}
	if !strings.Contains(got, "1.0.203") {
		t.Errorf("docsRSURLForPath: got %q, want version 1.0.203 in URL", got)
	}
}

func TestDocsRSURLForPath_NoRegistryMarker(t *testing.T) {
	// Non-registry path → empty string.
	got := docsRSURLForPath("/workspace/src/lib.rs")
	if got != "" {
		t.Errorf("docsRSURLForPath non-registry: got %q, want empty", got)
	}
}

func TestDocsRSURLForPath_VersionParsing(t *testing.T) {
	cases := []struct {
		path        string
		wantCrate   string
		wantVersion string
	}{
		{
			path:        "/home/.cargo/registry/src/index.crates.io-abc/tokio-1.38.0/src/runtime/mod.rs",
			wantCrate:   "tokio",
			wantVersion: "1.38.0",
		},
		{
			path:        "/home/.cargo/registry/src/index.crates.io-abc/stellar-xdr-0.0.19/src/lib.rs",
			wantCrate:   "stellar-xdr",
			wantVersion: "0.0.19",
		},
	}
	for _, tc := range cases {
		got := docsRSURLForPath(tc.path)
		if !strings.Contains(got, tc.wantCrate) {
			t.Errorf("path=%q: URL %q missing crate %q", tc.path, got, tc.wantCrate)
		}
		if !strings.Contains(got, tc.wantVersion) {
			t.Errorf("path=%q: URL %q missing version %q", tc.path, got, tc.wantVersion)
		}
	}
}

// ─── gitHubURLForCargoGit ─────────────────────────────────────────────────────

func TestGitHubURLForCargoGit_HappyPath(t *testing.T) {
	// Swap osReadFile so no real file I/O is needed.
	original := osReadFile
	t.Cleanup(func() { osReadFile = original })
	osReadFile = func(_ string) ([]byte, error) {
		return []byte(sampleCargoLock), nil
	}

	// Path matches the soroban-sdk entry in sampleCargoLock.
	sourcePath := "/home/user/.cargo/git/checkouts/soroban-sdk-abc123/def456/src/contract.rs"
	got := gitHubURLForCargoGit(sourcePath, "/workspace/Cargo.lock")

	if got == "" {
		t.Fatal("gitHubURLForCargoGit: got empty URL, want non-empty")
	}
	if !strings.HasPrefix(got, "https://github.com/stellar/rs-soroban-sdk") {
		t.Errorf("URL = %q, want github.com/stellar/rs-soroban-sdk prefix", got)
	}
	// Must contain the full 40-char commit hash from sampleCargoLock.
	if !strings.Contains(got, "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2") {
		t.Errorf("URL = %q, want commit hash a1b2c3...", got)
	}
	// Must contain the relative source path.
	if !strings.Contains(got, "src/contract.rs") {
		t.Errorf("URL = %q, want src/contract.rs in path", got)
	}
}

func TestGitHubURLForCargoGit_EmptyLockPath(t *testing.T) {
	got := gitHubURLForCargoGit("/home/.cargo/git/checkouts/foo/bar/src/lib.rs", "")
	if got != "" {
		t.Errorf("empty cargoLockPath: got %q, want empty", got)
	}
}

func TestGitHubURLForCargoGit_NoGitCheckoutsInPath(t *testing.T) {
	original := osReadFile
	t.Cleanup(func() { osReadFile = original })
	osReadFile = func(_ string) ([]byte, error) { return []byte(sampleCargoLock), nil }

	// Path does not contain /git/checkouts/ → cannot build a file-relative URL.
	got := gitHubURLForCargoGit("/workspace/src/lib.rs", "/workspace/Cargo.lock")
	if got != "" {
		t.Errorf("non-checkout path: got %q, want empty", got)
	}
}

// ─── ExternalURLForResult ─────────────────────────────────────────────────────

func TestExternalURLForResult_LocalFile_NoURL(t *testing.T) {
	got := ExternalURLForResult("/workspace/my-contract/src/lib.rs", "/workspace/my-contract", "", "")
	if got != "" {
		t.Errorf("local file: got %q, want empty", got)
	}
}

func TestExternalURLForResult_RegistryFile_DocsRS(t *testing.T) {
	p := "/home/user/.cargo/registry/src/index.crates.io-abc/serde-1.0.203/src/lib.rs"
	got := ExternalURLForResult(p, "/workspace", "", "")
	if !strings.Contains(got, "docs.rs") {
		t.Errorf("registry file: got %q, want docs.rs URL", got)
	}
}

func TestExternalURLForResult_GitFile_WithLock(t *testing.T) {
	original := osReadFile
	t.Cleanup(func() { osReadFile = original })
	osReadFile = func(_ string) ([]byte, error) { return []byte(sampleCargoLock), nil }

	p := "/home/user/.cargo/git/checkouts/soroban-sdk-abc123/def456/src/lib.rs"
	got := ExternalURLForResult(p, "/workspace", "", "/workspace/Cargo.lock")
	if got == "" {
		t.Error("git file with lock: got empty URL, want non-empty")
	}
	if !strings.Contains(got, "github.com") {
		t.Errorf("git file: got %q, want github.com URL", got)
	}
}

func TestExternalURLForResult_EmptyPath(t *testing.T) {
	got := ExternalURLForResult("", "/workspace", "", "")
	if got != "" {
		t.Errorf("empty path: got %q, want empty", got)
	}
}

// ─── splitTOMLKeyVal ──────────────────────────────────────────────────────────

func TestSplitTOMLKeyVal(t *testing.T) {
	cases := []struct {
		line    string
		wantKey string
		wantVal string
	}{
		{`name = "soroban-sdk"`, "name", "soroban-sdk"},
		{`version = "21.7.6"`, "version", "21.7.6"},
		{`source = "git+https://github.com/stellar/rs-soroban-sdk#abc"`, "source", "git+https://github.com/stellar/rs-soroban-sdk#abc"},
		{`checksum = "abcdef"`, "checksum", "abcdef"},
		{"no equals sign", "", ""},
		{"", "", ""},
	}
	for _, tc := range cases {
		gotKey, gotVal := splitTOMLKeyVal(tc.line)
		if gotKey != tc.wantKey || gotVal != tc.wantVal {
			t.Errorf("splitTOMLKeyVal(%q) = (%q,%q), want (%q,%q)",
				tc.line, gotKey, gotVal, tc.wantKey, tc.wantVal)
		}
	}
}

// ─── splitCrateVersion ────────────────────────────────────────────────────────

func TestSplitCrateVersion(t *testing.T) {
	cases := []struct {
		input       string
		wantName    string
		wantVersion string
	}{
		{"serde-1.0.203", "serde", "1.0.203"},
		{"tokio-1.38.0", "tokio", "1.38.0"},
		{"stellar-xdr-0.0.19", "stellar-xdr", "0.0.19"},
		{"my-multi-word-crate-2.0.1", "my-multi-word-crate", "2.0.1"},
		{"no-version", "no-version", ""},
	}
	for _, tc := range cases {
		gotName, gotVersion := splitCrateVersion(tc.input)
		if gotName != tc.wantName || gotVersion != tc.wantVersion {
			t.Errorf("splitCrateVersion(%q) = (%q,%q), want (%q,%q)",
				tc.input, gotName, gotVersion, tc.wantName, tc.wantVersion)
		}
	}
}

// ─── extractGitCheckoutDir ────────────────────────────────────────────────────

func TestExtractGitCheckoutDir(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/home/.cargo/git/checkouts/soroban-sdk-abc123/def456/src/lib.rs", "soroban-sdk-abc123"},
		{"/home/.cargo/git/checkouts/foo-bar-deadbeef/cafebabe/src/token.rs", "foo-bar-deadbeef"},
		{"/workspace/src/lib.rs", ""},
	}
	for _, tc := range cases {
		got := extractGitCheckoutDir(tc.path)
		if got != tc.want {
			t.Errorf("extractGitCheckoutDir(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

// ─── crateDirMatchesName ──────────────────────────────────────────────────────

func TestCrateDirMatchesName(t *testing.T) {
	cases := []struct {
		dir      string
		name     string
		want     bool
	}{
		{"soroban-sdk-abc12345", "soroban-sdk", true},
		{"soroban-env-host-deadbeef", "soroban-env-host", true},
		{"stellar_xdr-cafebabe", "stellar-xdr", true},   // underscore normalisation
		{"soroban-sdk-abc12345", "soroban-env-host", false},
		{"soroban-env-abc12345", "soroban-env-host", false}, // partial prefix must not match
		{"", "soroban-sdk", false},
		{"soroban-sdk-abc", "", false},
	}
	for _, tc := range cases {
		got := crateDirMatchesName(tc.dir, tc.name)
		if got != tc.want {
			t.Errorf("crateDirMatchesName(%q, %q) = %v, want %v", tc.dir, tc.name, got, tc.want)
		}
	}
}

// ─── FallbackResult.Origin and ExternalURL integration ───────────────────────

func TestFallbackResult_OriginFields_ZeroValue(t *testing.T) {
	// A zero-value FallbackResult should have OriginUnknownSource (0) and
	// empty ExternalURL — verifying the field defaults are correct.
	var r FallbackResult
	if r.Origin != OriginUnknownSource {
		t.Errorf("zero FallbackResult.Origin = %v, want OriginUnknownSource", r.Origin)
	}
	if r.ExternalURL != "" {
		t.Errorf("zero FallbackResult.ExternalURL = %q, want empty", r.ExternalURL)
	}
}

func TestFallbackResult_OriginPrefix_RoundTrip(t *testing.T) {
	// Assigning every SourceOrigin to FallbackResult.Origin and calling
	// OriginPrefix() should produce the expected terminal prefix.
	cases := []struct {
		origin SourceOrigin
		prefix string
	}{
		{OriginLocal, "[local]"},
		{OriginRegistry, "[crates.io]"},
		{OriginGit, "[git]"},
		{OriginUnknownSource, ""},
	}
	for _, tc := range cases {
		r := FallbackResult{Origin: tc.origin}
		if got := r.Origin.OriginPrefix(); got != tc.prefix {
			t.Errorf("origin %v: OriginPrefix() = %q, want %q", tc.origin, got, tc.prefix)
		}
	}
}
