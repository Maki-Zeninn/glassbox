// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// generate-release-manifest builds a signed release manifest for a Glassbox
// release and writes it to stdout (or --output).
//
// Usage:
//
//	generate-release-manifest \
//	  --dist         dist/release \
//	  --version      v1.2.3 \
//	  --commit       <sha> \
//	  --build-date   2026-01-01T00:00:00Z \
//	  --signing-key  ./release-key.pem \
//	  --output       dist/release/manifest.json
//
// The tool hashes every artifact listed via --artifact flags (or auto-detected
// from the --dist directory), signs the manifest with the supplied Ed25519 key,
// and writes a SignedManifest JSON file.
//
// Signing key:
//
//	Supply a PKCS#8 PEM Ed25519 private key via --signing-key (file path or
//	literal PEM) or the GLASSBOX_MANIFEST_SIGNING_KEY environment variable.
//	The private key is never written to disk by this tool.
package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dotandev/glassbox/internal/manifest"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// flags holds all parsed command-line options.
type flags struct {
	dist           string
	version        string
	commit         string
	buildDate      string
	sbomRef        string
	signingKey     string
	signerIdentity string
	keyID          string
	output         string
	verify         bool
	jsonOnly       bool
	// Build provenance fields
	buildSourceRepository string
	buildSourceRef        string
	buildGoVersion        string
	buildRunnerOS         string
	buildWorkflow         string
	buildRunID            string
}

func run(args []string) error {
	fs := flag.NewFlagSet("generate-release-manifest", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var f flags
	fs.StringVar(&f.dist, "dist", "dist/release", "Directory containing release artifacts")
	fs.StringVar(&f.version, "version", "", "Release version string (e.g. v1.2.3) [required]")
	fs.StringVar(&f.commit, "commit", "", "Full git commit SHA [required]")
	fs.StringVar(&f.buildDate, "build-date", "", "Build timestamp in RFC3339 UTC (e.g. 2026-01-01T00:00:00Z) [required]")
	fs.StringVar(&f.sbomRef, "sbom-ref", "", "Filename of the SBOM artifact in the release (optional)")
	fs.StringVar(&f.signingKey, "signing-key", "", "PKCS#8 PEM Ed25519 private key file path or literal PEM (or GLASSBOX_MANIFEST_SIGNING_KEY)")
	fs.StringVar(&f.signerIdentity, "signer-identity", "", "Human-readable signer identity stored in provenance (optional)")
	fs.StringVar(&f.keyID, "key-id", "", "Opaque key identifier stored in provenance (optional)")
	fs.StringVar(&f.output, "output", "", "Write signed manifest to this file instead of stdout")
	fs.BoolVar(&f.verify, "verify", false, "Verify the written manifest immediately after signing (default: true when --output is set)")
	fs.BoolVar(&f.jsonOnly, "json-only", false, "Suppress informational messages; write only the JSON manifest")
	// Build provenance flags — all optional, populated by CI environment.
	fs.StringVar(&f.buildSourceRepository, "build-source-repository", "", "VCS URL of the source repository (e.g. https://github.com/org/repo)")
	fs.StringVar(&f.buildSourceRef, "build-source-ref", "", "Fully-qualified git ref (e.g. refs/tags/v1.2.3)")
	fs.StringVar(&f.buildGoVersion, "build-go-version", "", "Go toolchain version string (e.g. go1.26.0 linux/amd64)")
	fs.StringVar(&f.buildRunnerOS, "build-runner-os", "", "Build runner operating system (e.g. ubuntu-24.04)")
	fs.StringVar(&f.buildWorkflow, "build-workflow", "", "CI workflow file that produced this release")
	fs.StringVar(&f.buildRunID, "build-run-id", "", "CI run ID (for log lookup)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	// Validate required flags.
	var missing []string
	if f.version == "" {
		missing = append(missing, "--version")
	}
	if f.commit == "" {
		missing = append(missing, "--commit")
	}
	if f.buildDate == "" {
		missing = append(missing, "--build-date")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required flag(s): %s", strings.Join(missing, ", "))
	}

	// Resolve signing key: flag > env.
	keyPEM := f.signingKey
	if keyPEM == "" {
		keyPEM = os.Getenv("GLASSBOX_MANIFEST_SIGNING_KEY")
	}
	if keyPEM == "" {
		return errors.New("signing key required: use --signing-key or GLASSBOX_MANIFEST_SIGNING_KEY")
	}

	signer, pubHex, err := loadSigner(keyPEM)
	if err != nil {
		return fmt.Errorf("loading signing key: %w", err)
	}

	logf := func(format string, a ...interface{}) {
		if !f.jsonOnly {
			fmt.Fprintf(os.Stderr, format+"\n", a...)
		}
	}

	logf("Scanning artifacts in %s ...", f.dist)
	entries, err := detectArtifacts(f.dist)
	if err != nil {
		return fmt.Errorf("detecting artifacts: %w", err)
	}
	logf("  Found %d artifact(s)", len(entries))

	// Auto-detect SBOM reference when the flag was not explicitly set.
	if f.sbomRef == "" {
		if detected := detectSBOMRef(f.dist); detected != "" {
			f.sbomRef = detected
			logf("  Auto-detected SBOM: %s", f.sbomRef)
		}
	}

	// Build signer provenance if any identity flags were set.
	var prov *manifest.ManifestProvenance
	if f.signerIdentity != "" || f.keyID != "" {
		prov = &manifest.ManifestProvenance{
			SignerIdentity: f.signerIdentity,
			KeyID:          f.keyID,
			Algorithm:      "ed25519",
		}
	}

	// Build source/build provenance from flags.
	var bp *manifest.BuildProvenance
	if f.buildSourceRepository != "" || f.buildSourceRef != "" || f.buildGoVersion != "" ||
		f.buildRunnerOS != "" || f.buildWorkflow != "" || f.buildRunID != "" {
		bp = &manifest.BuildProvenance{
			SourceRepository: f.buildSourceRepository,
			SourceRef:        f.buildSourceRef,
			CommitSHA:        f.commit,
			GoVersion:        f.buildGoVersion,
			BuildRunnerOS:    f.buildRunnerOS,
			BuildWorkflow:    f.buildWorkflow,
			BuildRunID:       f.buildRunID,
		}
		logf("  Build provenance: repo=%s ref=%s run=%s", shortHex(bp.SourceRepository), bp.SourceRef, bp.BuildRunID)
	}

	logf("Building manifest ...")
	m, err := manifest.NewWithBuildProvenance(f.version, f.commit, f.buildDate, f.sbomRef, f.dist, entries, prov, bp)
	if err != nil {
		return fmt.Errorf("building manifest: %w", err)
	}

	logf("Signing manifest with key %s ...", shortHex(pubHex))
	sm, err := manifest.Sign(m, signer)
	if err != nil {
		return fmt.Errorf("signing manifest: %w", err)
	}

	data, err := json.MarshalIndent(sm, "", "  ")
	if err != nil {
		return fmt.Errorf("serialising manifest: %w", err)
	}
	data = append(data, '\n')

	// Write output.
	if f.output != "" {
		if err := os.WriteFile(f.output, data, 0644); err != nil {
			return fmt.Errorf("writing manifest to %s: %w", f.output, err)
		}
		logf("Manifest written to %s", f.output)

		// Verify after write unless explicitly disabled.
		if f.verify || f.output != "" {
			logf("Verifying manifest ...")
			result := manifest.Verify(sm)
			if !result.Valid {
				return fmt.Errorf("post-sign verification failed (hash_valid=%v, sig_valid=%v, artifacts_complete=%v)",
					result.HashValid, result.SignatureValid, result.ArtifactsComplete)
			}
			logf("  [PASS] manifest_hash verified")
			logf("  [PASS] signature verified")
			logf("  [PASS] %d artifact(s) listed, no duplicates", len(sm.Artifacts))
		}
	} else {
		_, err = os.Stdout.Write(data)
		if err != nil {
			return fmt.Errorf("writing to stdout: %w", err)
		}
	}

	logf("Done. manifest_hash=%s", shortHex(sm.ManifestHash))
	return nil
}

// detectArtifacts scans dir and classifies every file into an ArtifactEntry.
// It skips the manifest file itself (manifest.json / *.manifest.json) to avoid
// self-referential entries.
// It also returns the filename of the first SBOM file found (if any) so the
// caller can use it as the default --sbom-ref when the flag was not set.
func detectArtifacts(dir string) ([]manifest.ArtifactEntry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var result []manifest.ArtifactEntry
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()

		// Skip manifest files to avoid self-reference.
		if strings.HasSuffix(name, "manifest.json") || name == "manifest.json" {
			continue
		}

		result = append(result, manifest.ArtifactEntry{
			Name:     name,
			Platform: platformFromName(name),
			Kind:     kindFromName(name),
		})
	}
	return result, nil
}

// detectSBOMRef scans dir for the first SBOM artifact (.spdx.json, .sbom.json,
// .cdx.json) and returns its filename. Returns "" when none is found.
func detectSBOMRef(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() && isSBOMFile(e.Name()) {
			return e.Name()
		}
	}
	return ""
}

// platformFromName infers the target platform from a filename such as
// "glassbox-linux-amd64.tar.gz" → "linux/amd64".
func platformFromName(name string) string {
	// Strip extensions to get the base name.
	base := strings.TrimSuffix(name, filepath.Ext(name))
	base = strings.TrimSuffix(base, ".tar")

	lower := strings.ToLower(base)
	switch {
	case strings.Contains(lower, "linux") && strings.Contains(lower, "amd64"):
		return "linux/amd64"
	case strings.Contains(lower, "linux") && strings.Contains(lower, "arm64"):
		return "linux/arm64"
	case strings.Contains(lower, "darwin") && strings.Contains(lower, "amd64"):
		return "darwin/amd64"
	case strings.Contains(lower, "darwin") && strings.Contains(lower, "arm64"):
		return "darwin/arm64"
	case strings.Contains(lower, "windows") && strings.Contains(lower, "amd64"):
		return "windows/amd64"
	}
	return "" // platform-independent (checksums, version.txt, SBOM, etc.)
}

// kindFromName classifies a filename into an ArtifactKind.
func kindFromName(name string) manifest.ArtifactKind {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".zip"):
		return manifest.KindArchive
	case strings.Contains(lower, "checksum") || strings.HasSuffix(lower, ".sha256"):
		return manifest.KindChecksum
	// SPDX / CycloneDX SBOM files and other metadata are all KindMetadata.
	case strings.HasSuffix(lower, ".spdx.json"), strings.HasSuffix(lower, ".sbom.json"),
		strings.HasSuffix(lower, ".cdx.json"),
		strings.HasSuffix(lower, ".txt"), strings.HasSuffix(lower, ".json"):
		return manifest.KindMetadata
	default:
		return manifest.KindBinary
	}
}

// isSBOMFile returns true when name is an SBOM artifact that should be
// auto-detected as the --sbom-ref when no explicit flag was provided.
func isSBOMFile(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".spdx.json") ||
		strings.HasSuffix(lower, ".sbom.json") ||
		strings.HasSuffix(lower, ".cdx.json")
}

// loadSigner parses keyPEM (a file path or literal PEM string) and returns an
// ed25519Signer and the hex-encoded public key.
func loadSigner(keyPEM string) (*ed25519Signer, string, error) {
	data := []byte(keyPEM)

	// If it does not start with a PEM header, treat it as a file path.
	if !strings.HasPrefix(strings.TrimSpace(keyPEM), "-----BEGIN") {
		b, err := os.ReadFile(keyPEM)
		if err != nil {
			return nil, "", fmt.Errorf("reading key file %q: %w", keyPEM, err)
		}
		data = b
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return nil, "", errors.New("no PEM block found in signing key")
	}

	priv, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, "", fmt.Errorf("parsing PKCS#8 key: %w", err)
	}

	ed, ok := priv.(ed25519.PrivateKey)
	if !ok {
		return nil, "", errors.New("signing key must be an Ed25519 private key")
	}

	pub := ed.Public().(ed25519.PublicKey)
	pubHex := hex.EncodeToString(pub)

	// Compute a key fingerprint (SHA-256 of the public key bytes).
	sum := sha256.Sum256(pub)
	_ = sum // available if callers want it; pubHex is the canonical identifier

	return &ed25519Signer{priv: ed, pub: pub}, pubHex, nil
}

// ed25519Signer wraps an in-memory Ed25519 key and satisfies manifest.Signer.
type ed25519Signer struct {
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
}

func (s *ed25519Signer) Sign(data []byte) ([]byte, error) {
	return ed25519.Sign(s.priv, data), nil
}
func (s *ed25519Signer) PublicKey() ([]byte, error) { return []byte(s.pub), nil }
func (s *ed25519Signer) Algorithm() string          { return "ed25519" }

// shortHex returns first 8 + "…" + last 8 chars of a hex string.
func shortHex(h string) string {
	if len(h) <= 16 {
		return h
	}
	return h[:8] + "…" + h[len(h)-8:]
}
