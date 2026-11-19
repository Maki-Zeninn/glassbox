// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Package sbom produces versioned Software Bill of Materials documents in
// SPDX 2.3 JSON format for Glassbox releases.
//
// A Glassbox release spans three ecosystems:
//
//   - Go modules      – parsed from `go list -m -json all` output
//   - Cargo crates    – parsed from Cargo.lock (TOML [[package]] tables)
//   - npm packages    – parsed from package-lock.json (v2/v3 lockfile)
//
// Components from all three ecosystems are merged under a single SPDX document
// with stable, deterministic component identifiers so the document hash is
// reproducible across CI runs on identical lockfiles.
//
// SPDX reference: https://spdx.github.io/spdx-spec/v2.3/
package sbom

import "fmt"

// Ecosystem identifies which package manager a component originates from.
type Ecosystem string

const (
	EcosystemGo    Ecosystem = "go"
	EcosystemCargo Ecosystem = "cargo"
	EcosystemNPM   Ecosystem = "npm"
)

// Component is a normalized dependency entry drawn from any ecosystem. All
// fields except LicenseExpression and Checksum are required.
type Component struct {
	// Ecosystem is the package manager this component was sourced from.
	Ecosystem Ecosystem
	// Name is the module/crate/package name as it appears in the lockfile.
	Name string
	// Version is the resolved version string from the lockfile.
	Version string
	// PURL is the Package URL uniquely identifying this component across
	// ecosystems. It is constructed deterministically from Ecosystem/Name/Version.
	PURL string
	// LicenseExpression is the SPDX license expression when available in the
	// source manifest; empty when not declared.
	LicenseExpression string
	// Checksum is the hex-encoded SHA-256 of the source archive when recorded
	// in the lockfile (Cargo checksums, Go module hashes). Empty otherwise.
	Checksum string
	// SPDXID is the unique element identifier within this SPDX document,
	// e.g. "SPDXRef-go-github.com/foo/bar-v1.0.0".
	SPDXID string
}

// BuildPURL constructs a Package URL (https://github.com/package-url/purl-spec)
// for the given ecosystem, name, and version.
//
//	go    → pkg:golang/<name>@<version>
//	cargo → pkg:cargo/<name>@<version>
//	npm   → pkg:npm/<name>@<version>
func BuildPURL(eco Ecosystem, name, version string) string {
	var t string
	switch eco {
	case EcosystemGo:
		t = "golang"
	case EcosystemCargo:
		t = "cargo"
	case EcosystemNPM:
		t = "npm"
	default:
		t = string(eco)
	}
	return fmt.Sprintf("pkg:%s/%s@%s", t, name, version)
}

// BuildSPDXID constructs a deterministic SPDX element identifier for a
// component. Characters not allowed in SPDX ID values (anything other than
// letters, digits, ".", "-") are replaced with "-".
func BuildSPDXID(eco Ecosystem, name, version string) string {
	safe := func(s string) string {
		b := make([]byte, 0, len(s))
		for i := 0; i < len(s); i++ {
			c := s[i]
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
				(c >= '0' && c <= '9') || c == '.' || c == '-' {
				b = append(b, c)
			} else {
				b = append(b, '-')
			}
		}
		return string(b)
	}
	return fmt.Sprintf("SPDXRef-%s-%s-%s", eco, safe(name), safe(version))
}
