// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package depcompat

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// GoldenDir is the path under the module root where golden baselines live.
// It can be overridden in tests via GoldenDirForTest.
var GoldenDir = filepath.Join("internal", "depcompat", "testdata", "golden")

// WriteGolden writes bytes to the golden file for the given (group, kind) pair.
// It creates the directory if necessary and uses an atomic rename pattern.
func WriteGolden(goldenDir string, group DepGroup, kind OutputKind, data []byte) error {
	if err := os.MkdirAll(goldenDir, 0o755); err != nil {
		return fmt.Errorf("create golden dir: %w", err)
	}
	dst := filepath.Join(goldenDir, GoldenFileName(group, kind))
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil { //nolint:gosec
		return fmt.Errorf("write temp golden file: %w", err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp) // best-effort cleanup
		return fmt.Errorf("rename golden file: %w", err)
	}
	return nil
}

// ReadGolden reads the golden file for the given (group, kind) pair.
func ReadGolden(goldenDir string, group DepGroup, kind OutputKind) ([]byte, error) {
	path := filepath.Join(goldenDir, GoldenFileName(group, kind))
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read golden file %s: %w", path, err)
	}
	return data, nil
}

// GoldenExists returns true if the golden file for the given pair exists.
func GoldenExists(goldenDir string, group DepGroup, kind OutputKind) bool {
	path := filepath.Join(goldenDir, GoldenFileName(group, kind))
	_, err := os.Stat(path)
	return err == nil
}

// VersionInfo holds the resolved dependency versions read from go.mod / Cargo.lock.
type VersionInfo struct {
	StellarSDKVersion   string
	SorobanHostVersion  string
	Ed25519DalekVersion string
	Sha2Version         string
	GoVersion           string
	RustVersion         string
}

// ToDepVersions converts VersionInfo to the DepVersions type used in the report.
func (vi VersionInfo) ToDepVersions() DepVersions {
	return DepVersions{
		StellarSDKVersion:   vi.StellarSDKVersion,
		SorobanHostVersion:  vi.SorobanHostVersion,
		Ed25519DalekVersion: vi.Ed25519DalekVersion,
		Sha2Version:         vi.Sha2Version,
		GoVersion:           vi.GoVersion,
		RustVersion:         vi.RustVersion,
	}
}

// DetectVersions attempts to resolve the current dependency versions by
// inspecting go.mod (via `go list`) and Cargo.lock.
// It returns partial results on error — callers should log but not abort.
func DetectVersions(repoRoot string) VersionInfo {
	vi := VersionInfo{
		GoVersion: runtime.Version(),
	}

	// Resolve Go module versions via `go list -m -json`.
	vi.StellarSDKVersion = goModVersion(repoRoot, "github.com/stellar/go-stellar-sdk")

	// Resolve Rust crate versions from Cargo.lock.
	cargoLock := filepath.Join(repoRoot, "simulator", "Cargo.lock")
	lockBytes, err := os.ReadFile(cargoLock)
	if err == nil {
		vi.SorobanHostVersion = cargoLockVersion(lockBytes, "soroban-env-host")
		vi.Ed25519DalekVersion = cargoLockVersion(lockBytes, "ed25519-dalek")
		vi.Sha2Version = cargoLockVersion(lockBytes, "sha2")
	}

	// Resolve Rust toolchain version via `rustc --version`.
	if out, err := exec.Command("rustc", "--version").Output(); err == nil {
		vi.RustVersion = strings.TrimSpace(string(out))
	}

	return vi
}

// goModVersion runs `go list -m -json <module>` and returns the resolved version.
func goModVersion(repoRoot, module string) string {
	cmd := exec.Command("go", "list", "-m", "-json", module)
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	var result struct {
		Version string `json:"Version"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return ""
	}
	return result.Version
}

// cargoLockVersion extracts the first version entry for a package from Cargo.lock content.
func cargoLockVersion(lockContent []byte, pkg string) string {
	// Cargo.lock v3 format: [[package]] blocks with name = "..." and version = "..."
	// We scan for the pattern:
	//   name = "pkg"
	//   version = "x.y.z"
	reBlock := regexp.MustCompile(
		`name = "` + regexp.QuoteMeta(pkg) + `"\nversion = "([^"]+)"`,
	)
	if m := reBlock.FindSubmatch(lockContent); m != nil {
		return string(m[1])
	}
	return ""
}

// CapturedOutput holds a captured JSON output blob from the deterministic harness.
type CapturedOutput struct {
	Group    DepGroup
	Kind     OutputKind
	Data     []byte
	CaptureError string
}

// GenerateGoldenBaseline creates or overwrites all golden baseline files from
// the provided captured outputs. It is called by scripts/dep-compat-capture.sh
// via go run ./internal/depcompat/cmd/capture when the --update flag is set.
func GenerateGoldenBaseline(goldenDir string, outputs []CapturedOutput) []error {
	var errs []error
	for _, out := range outputs {
		if out.CaptureError != "" {
			errs = append(errs, fmt.Errorf("capture error for %s/%s: %s", out.Group, out.Kind, out.CaptureError))
			continue
		}
		// Normalise: parse and re-serialize with sorted keys for deterministic diff.
		normalised, err := normaliseJSON(out.Data)
		if err != nil {
			errs = append(errs, fmt.Errorf("normalise JSON for %s/%s: %w", out.Group, out.Kind, err))
			continue
		}
		if err := WriteGolden(goldenDir, out.Group, out.Kind, normalised); err != nil {
			errs = append(errs, fmt.Errorf("write golden for %s/%s: %w", out.Group, out.Kind, err))
		}
	}
	return errs
}

// normaliseJSON parses JSON and re-encodes it with sorted keys and two-space
// indentation for stable golden file content.
func normaliseJSON(data []byte) ([]byte, error) {
	var v interface{}
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	return json.MarshalIndent(v, "", "  ")
}
