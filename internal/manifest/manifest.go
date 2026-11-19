// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Package manifest produces and verifies signed release manifests.
//
// A ReleaseManifest lists every artifact in a release (binary archives,
// checksums file, SBOM reference, version metadata) together with its
// SHA-256 hash, size, and platform label. The manifest itself is then
// signed with an Ed25519 key using the same signer infrastructure as
// audit:sign, producing a SignedManifest that can be verified offline
// with nothing more than the public key and the Go standard library.
//
// JSON layout (SignedManifest):
//
//	{
//	  "schema_version": "1",
//	  "version":        "v1.2.3",
//	  "commit":         "<sha>",
//	  "build_date":     "2026-01-01T00:00:00Z",
//	  "sbom_ref":       "glassbox-v1.2.3.spdx.json",   // optional
//	  "artifacts": [
//	    {
//	      "name":     "glassbox-linux-amd64.tar.gz",
//	      "platform": "linux/amd64",
//	      "sha256":   "<hex>",
//	      "size":     12345678,
//	      "kind":     "archive"
//	    },
//	    ...
//	  ],
//	  "provenance": {
//	    "signer_identity": "ci-pipeline",
//	    "key_id":          "<fingerprint>",
//	    "algorithm":       "ed25519"
//	  },
//	  "manifest_hash": "<hex SHA-256 of canonical JSON of the above>",
//	  "signature":     "<hex Ed25519 signature over manifest_hash bytes>",
//	  "public_key":    "<hex Ed25519 public key>"
//	}
package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

// SchemaVersion is the manifest format version embedded in every manifest.
const SchemaVersion = "1"

// ArtifactKind classifies what a release artifact is.
type ArtifactKind string

const (
	KindArchive  ArtifactKind = "archive"   // tar.gz or zip wrapping a binary
	KindChecksum ArtifactKind = "checksums" // SHA-256 checksum file
	KindMetadata ArtifactKind = "metadata"  // version.txt, SBOM, etc.
	KindBinary   ArtifactKind = "binary"    // raw executable (rare in releases)
)

// Artifact describes a single release file.
type Artifact struct {
	// Name is the bare filename (no directory component).
	Name string `json:"name"`
	// Platform is the target platform in "os/arch" form (e.g. "linux/amd64").
	// Empty for platform-independent artifacts such as checksums or SBOM.
	Platform string `json:"platform,omitempty"`
	// SHA256 is the lower-case hex-encoded SHA-256 digest of the file.
	SHA256 string `json:"sha256"`
	// Size is the file size in bytes.
	Size int64 `json:"size"`
	// Kind classifies the artifact.
	Kind ArtifactKind `json:"kind"`
}

// ManifestProvenance carries metadata about the signing key and identity,
// mirroring the SignatureProvenance type in the audit package so consumers
// have a consistent mental model.
type ManifestProvenance struct {
	// SignerIdentity is a human-readable label for who signed the manifest
	// (e.g. "ci-pipeline", "release-bot@example.com").
	SignerIdentity string `json:"signer_identity,omitempty"`
	// KeyID is an opaque identifier for the key used (fingerprint, ARN, label).
	KeyID string `json:"key_id,omitempty"`
	// Algorithm is the signing algorithm reported by the Signer (e.g. "ed25519").
	Algorithm string `json:"algorithm,omitempty"`
}

// BuildProvenance records the source and build inputs that produced a release.
// It is embedded in every ReleaseManifest so consumers can trace an artifact
// back to the exact source revision and declared build environment without
// needing access to any CI secret.
type BuildProvenance struct {
	// SourceRepository is the canonical VCS URL of the source repository.
	// E.g. "https://github.com/dotandev/glassbox"
	SourceRepository string `json:"source_repository,omitempty"`
	// SourceRef is the fully-qualified Git ref that was built
	// (e.g. "refs/tags/v1.2.3" or "refs/heads/main").
	SourceRef string `json:"source_ref,omitempty"`
	// CommitSHA is the full 40-character hex SHA of the source commit.
	CommitSHA string `json:"commit_sha,omitempty"`
	// GoVersion is the Go toolchain version used to compile the binaries,
	// as reported by `go version` (e.g. "go1.26.0 linux/amd64").
	GoVersion string `json:"go_version,omitempty"`
	// BuildRunnerOS is the operating system of the build runner
	// (e.g. "ubuntu-24.04").
	BuildRunnerOS string `json:"build_runner_os,omitempty"`
	// BuildWorkflow is the CI workflow file that produced this release
	// (e.g. ".github/workflows/release.yml").
	BuildWorkflow string `json:"build_workflow,omitempty"`
	// BuildRunID is the CI run identifier that can be used to locate build logs.
	// For GitHub Actions this is the numeric run ID.
	BuildRunID string `json:"build_run_id,omitempty"`
	// SourceDateEpoch is the Unix timestamp used as SOURCE_DATE_EPOCH to
	// produce reproducible archives (equals the commit timestamp).
	SourceDateEpoch int64 `json:"source_date_epoch,omitempty"`
}

// ReleaseManifest is the unsigned body of the manifest. It is serialised to
// canonical JSON and hashed before signing.
type ReleaseManifest struct {
	// SchemaVersion identifies the manifest format version.
	SchemaVersion string `json:"schema_version"`
	// Version is the release version string (e.g. "v1.2.3").
	Version string `json:"version"`
	// Commit is the full git commit SHA.
	Commit string `json:"commit"`
	// BuildDate is the UTC timestamp when the release was built.
	BuildDate string `json:"build_date"`
	// SBOMRef is the filename of the SBOM artifact included in this release,
	// if one was generated. Empty when no SBOM is present.
	SBOMRef string `json:"sbom_ref,omitempty"`
	// Artifacts lists every release file, sorted lexicographically by Name.
	// Each artifact must appear exactly once.
	Artifacts []Artifact `json:"artifacts"`
	// Provenance carries optional signer metadata.
	Provenance *ManifestProvenance `json:"provenance,omitempty"`
	// BuildProvenance records the source revision and build environment that
	// produced this release. All fields are optional; an empty struct is
	// omitted from JSON output. Consumers can use this to reproduce the build
	// or verify that an artifact came from the declared source.
	BuildProvenance *BuildProvenance `json:"build_provenance,omitempty"`
}

// SignedManifest is the complete on-disk structure: the manifest body plus
// the cryptographic fields that allow independent verification.
type SignedManifest struct {
	ReleaseManifest

	// ManifestHash is the hex-encoded SHA-256 of the canonical JSON of
	// the ReleaseManifest fields above (provenance included). This is the
	// exact bytes that were signed.
	ManifestHash string `json:"manifest_hash"`
	// Signature is the hex-encoded Ed25519 signature over ManifestHash bytes
	// (i.e. Sign(sha256(canonical_json))).
	Signature string `json:"signature"`
	// PublicKey is the hex-encoded Ed25519 public key that produced Signature.
	PublicKey string `json:"public_key"`
}

// New builds a ReleaseManifest from the supplied parameters.
// artifactDir is the directory containing the release artifacts; each file in
// artifacts is hashed and measured. The returned manifest has Artifacts sorted
// by name so the canonical hash is stable regardless of filesystem order.
func New(version, commit, buildDate, sbomRef, artifactDir string, files []ArtifactEntry, provenance *ManifestProvenance) (*ReleaseManifest, error) {
	return NewWithBuildProvenance(version, commit, buildDate, sbomRef, artifactDir, files, provenance, nil)
}

// NewWithBuildProvenance is like New but also accepts a BuildProvenance that
// records the source revision and build environment.
func NewWithBuildProvenance(version, commit, buildDate, sbomRef, artifactDir string, files []ArtifactEntry, provenance *ManifestProvenance, bp *BuildProvenance) (*ReleaseManifest, error) {
	m := &ReleaseManifest{
		SchemaVersion:   SchemaVersion,
		Version:         version,
		Commit:          commit,
		BuildDate:       buildDate,
		SBOMRef:         sbomRef,
		Provenance:      provenance,
		BuildProvenance: bp,
	}

	for _, e := range files {
		path := artifactDir + "/" + e.Name
		a, err := hashArtifact(path, e.Name, e.Platform, e.Kind)
		if err != nil {
			return nil, fmt.Errorf("hashing %s: %w", e.Name, err)
		}
		m.Artifacts = append(m.Artifacts, a)
	}

	// Stable sort so the canonical hash never depends on input order.
	sort.Slice(m.Artifacts, func(i, j int) bool {
		return m.Artifacts[i].Name < m.Artifacts[j].Name
	})

	return m, nil
}

// ArtifactEntry is the input type for New describing one file to include.
type ArtifactEntry struct {
	Name     string
	Platform string
	Kind     ArtifactKind
}

// hashArtifact opens path, streams its content through SHA-256, and returns
// a fully populated Artifact.
func hashArtifact(path, name, platform string, kind ArtifactKind) (Artifact, error) {
	f, err := os.Open(path)
	if err != nil {
		return Artifact{}, err
	}
	defer f.Close()

	h := sha256.New()
	size, err := io.Copy(h, f)
	if err != nil {
		return Artifact{}, fmt.Errorf("reading %s: %w", path, err)
	}

	return Artifact{
		Name:     name,
		Platform: platform,
		SHA256:   hex.EncodeToString(h.Sum(nil)),
		Size:     size,
		Kind:     kind,
	}, nil
}

// CanonicalJSON serialises m to deterministic JSON: keys are always in
// Go struct field order (which matches the json tags), no HTML escaping,
// indented with two spaces for human readability. This is the bytes that
// are hashed and signed.
func CanonicalJSON(m *ReleaseManifest) ([]byte, error) {
	// We re-encode through a round-trip to guarantee field ordering matches
	// the struct definition regardless of any map fields that might appear in
	// future versions.
	data, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("marshal manifest: %w", err)
	}

	// Unmarshal into an ordered representation using encoding/json's struct
	// path (not a map) so key order is deterministic.
	var ordered ReleaseManifest
	if err := json.Unmarshal(data, &ordered); err != nil {
		return nil, fmt.Errorf("round-trip unmarshal: %w", err)
	}

	canonical, err := json.MarshalIndent(ordered, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("canonical marshal: %w", err)
	}
	return canonical, nil
}

// Hash returns the hex-encoded SHA-256 of the canonical JSON of m.
// This is the value stored in SignedManifest.ManifestHash.
func Hash(m *ReleaseManifest) (string, []byte, error) {
	canonical, err := CanonicalJSON(m)
	if err != nil {
		return "", nil, err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), sum[:], nil
}

// Sign signs m with the provided signer and returns a SignedManifest.
// The signer must implement Sign([]byte)([]byte, error) and PublicKey()([]byte, error).
func Sign(m *ReleaseManifest, s Signer) (*SignedManifest, error) {
	hashHex, hashBytes, err := Hash(m)
	if err != nil {
		return nil, fmt.Errorf("hashing manifest: %w", err)
	}

	sig, err := s.Sign(hashBytes)
	if err != nil {
		return nil, fmt.Errorf("signing manifest: %w", err)
	}

	pub, err := s.PublicKey()
	if err != nil {
		return nil, fmt.Errorf("retrieving public key: %w", err)
	}

	return &SignedManifest{
		ReleaseManifest: *m,
		ManifestHash:    hashHex,
		Signature:       hex.EncodeToString(sig),
		PublicKey:       hex.EncodeToString(pub),
	}, nil
}

// Signer is the subset of signer.Signer needed by this package, extracted as
// a local interface so the manifest package does not import the signer package
// (avoiding a circular dependency if signer ever imports manifest).
type Signer interface {
	Sign(data []byte) ([]byte, error)
	PublicKey() ([]byte, error)
	Algorithm() string
}

// Verify checks that sm.Signature is a valid Ed25519 signature over
// sm.ManifestHash using sm.PublicKey, and that ManifestHash matches a
// freshly computed hash of the manifest body.
//
// It returns a VerifyResult so callers can display each check separately.
func Verify(sm *SignedManifest) VerifyResult {
	result := VerifyResult{}

	// 1. Re-derive the hash from the manifest body.
	hashHex, _, err := Hash(&sm.ReleaseManifest)
	if err != nil {
		result.Error = fmt.Sprintf("failed to compute manifest hash: %v", err)
		return result
	}
	result.HashValid = strings.EqualFold(hashHex, sm.ManifestHash)

	// 2. Decode public key.
	pubBytes, err := hex.DecodeString(sm.PublicKey)
	if err != nil || len(pubBytes) != 32 {
		result.Error = "invalid public_key field (must be 64 hex chars / 32 bytes)"
		return result
	}

	// 3. Decode signature.
	sigBytes, err := hex.DecodeString(sm.Signature)
	if err != nil || len(sigBytes) != 64 {
		result.Error = "invalid signature field (must be 128 hex chars / 64 bytes)"
		return result
	}

	// 4. Verify Ed25519 signature over the hash bytes.
	hashBytes, _ := hex.DecodeString(sm.ManifestHash)
	result.SignatureValid = verifyEd25519(pubBytes, hashBytes, sigBytes)

	// 5. Check every artifact is listed exactly once.
	result.ArtifactsComplete, result.DuplicateNames = checkArtifacts(sm.Artifacts)

	result.Valid = result.HashValid && result.SignatureValid && result.ArtifactsComplete

	return result
}

// VerifyResult captures the outcome of each individual check in Verify.
type VerifyResult struct {
	Valid              bool
	HashValid          bool
	SignatureValid     bool
	ArtifactsComplete  bool
	DuplicateNames     []string
	Error              string
}

// checkArtifacts returns (true, nil) when every artifact name is unique.
func checkArtifacts(artifacts []Artifact) (bool, []string) {
	seen := make(map[string]int, len(artifacts))
	for _, a := range artifacts {
		seen[a.Name]++
	}
	var dups []string
	for name, count := range seen {
		if count > 1 {
			dups = append(dups, name)
		}
	}
	if len(dups) > 0 {
		sort.Strings(dups)
		return false, dups
	}
	return true, nil
}

// FormatTimestamp formats t as RFC3339 UTC, matching the BuildDate field style.
func FormatTimestamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}
