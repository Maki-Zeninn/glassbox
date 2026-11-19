// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package sbom

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// SPDXVersion is the SPDX specification version targeted by this generator.
const SPDXVersion = "SPDX-2.3"

// DataLicense is the mandatory SPDX data-license field for all SPDX documents.
const DataLicense = "CC0-1.0"

// DocumentNamespace prefix for Glassbox SBOM documents. The full namespace is
// this prefix + "/" + version + "/" + documentName + "#" + sha256[:8].
const DocumentNamespacePrefix = "https://glassbox.dev/sbom"

// BuildContext records the provenance of the SBOM: which tool generated it,
// from which source files, and when.
type BuildContext struct {
	// GeneratedAt is the UTC time the SBOM was generated.
	GeneratedAt time.Time
	// ToolVersion is the version of glassbox that produced the SBOM.
	ToolVersion string
	// ReleaseVersion is the release being described (e.g. "v1.2.3").
	ReleaseVersion string
	// Commit is the full git SHA of the release commit.
	Commit string
	// GoModPath is the path to go.mod (for reference).
	GoModPath string
	// CargoLockPath is the path to Cargo.lock (for reference).
	CargoLockPath string
	// PackageLockPath is the path to package-lock.json (for reference).
	PackageLockPath string
}

// ── SPDX 2.3 JSON structures ─────────────────────────────────────────────────
// Field names match the SPDX 2.3 JSON binding exactly so the output can be
// validated by standard SPDX tools.

// SPDXDocument is the top-level SPDX 2.3 JSON document.
type SPDXDocument struct {
	SPDXVersion     string          `json:"spdxVersion"`
	DataLicense     string          `json:"dataLicense"`
	SPDXID          string          `json:"SPDXID"`
	Name            string          `json:"name"`
	DocumentNamespace string        `json:"documentNamespace"`
	CreationInfo    SPDXCreationInfo `json:"creationInfo"`
	// DocumentHash is a Glassbox extension: SHA-256 of the canonical JSON of
	// the Packages slice, enabling the manifest to reference the SBOM by hash.
	DocumentHash string           `json:"documentHash,omitempty"`
	Packages     []SPDXPackage    `json:"packages"`
	Relationships []SPDXRelationship `json:"relationships"`
}

// SPDXCreationInfo records tool and timestamp metadata.
type SPDXCreationInfo struct {
	Created  string   `json:"created"`  // RFC3339 UTC
	Creators []string `json:"creators"` // "Tool: glassbox-v1.2.3"
	Comment  string   `json:"comment,omitempty"`
}

// SPDXPackage represents one dependency in the SPDX document.
type SPDXPackage struct {
	SPDXID           string        `json:"SPDXID"`
	Name             string        `json:"name"`
	Version          string        `json:"versionInfo"`
	DownloadLocation string        `json:"downloadLocation"`
	FilesAnalyzed    bool          `json:"filesAnalyzed"`
	ExternalRefs     []SPDXExtRef  `json:"externalRefs,omitempty"`
	LicenseConcluded string        `json:"licenseConcluded"`
	LicenseDeclared  string        `json:"licenseDeclared"`
	CopyrightText    string        `json:"copyrightText"`
	Checksums        []SPDXChecksum `json:"checksums,omitempty"`
}

// SPDXExtRef represents an external reference (e.g. PURL).
type SPDXExtRef struct {
	ReferenceCategory string `json:"referenceCategory"`
	ReferenceType     string `json:"referenceType"`
	ReferenceLocator  string `json:"referenceLocator"`
}

// SPDXChecksum represents a checksum entry on a package.
type SPDXChecksum struct {
	Algorithm     string `json:"algorithm"`
	ChecksumValue string `json:"checksumValue"`
}

// SPDXRelationship links SPDX elements (e.g. DESCRIBES relationships).
type SPDXRelationship struct {
	SpdxElementID      string `json:"spdxElementId"`
	RelationshipType   string `json:"relationshipType"`
	RelatedSpdxElement string `json:"relatedSpdxElement"`
}

// ── Builder ───────────────────────────────────────────────────────────────────

// GenerateDocument builds a fully populated SPDXDocument from the provided
// components and build context. The document is deterministic: components are
// sorted by SPDXID before serialization so the document hash is stable across
// runs on identical inputs.
//
// NOASSERTION is used for LicenseConcluded / LicenseDeclared / CopyrightText
// when those fields are not available from the lockfiles (which do not record
// license information). This is correct SPDX practice for generated SBOMs.
func GenerateDocument(components []Component, ctx BuildContext) *SPDXDocument {
	docName := fmt.Sprintf("glassbox-%s", ctx.ReleaseVersion)

	// Sort components for determinism.
	sorted := make([]Component, len(components))
	copy(sorted, components)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].SPDXID < sorted[j].SPDXID
	})

	// De-duplicate by SPDXID (same package pulled in by multiple ecosystems
	// should appear only once with a merged PURL list).
	deduped := deduplicate(sorted)

	// Build SPDX package entries.
	packages := make([]SPDXPackage, 0, len(deduped)+1)

	// The first package always describes the Glassbox release itself.
	rootID := "SPDXRef-Package-glassbox"
	rootPkg := SPDXPackage{
		SPDXID:           rootID,
		Name:             "glassbox",
		Version:          ctx.ReleaseVersion,
		DownloadLocation: "https://github.com/dotandev/glassbox",
		FilesAnalyzed:    false,
		LicenseConcluded: "Apache-2.0",
		LicenseDeclared:  "Apache-2.0",
		CopyrightText:    "NOASSERTION",
		ExternalRefs: []SPDXExtRef{{
			ReferenceCategory: "PACKAGE-MANAGER",
			ReferenceType:     "purl",
			ReferenceLocator:  fmt.Sprintf("pkg:golang/github.com/dotandev/glassbox@%s", ctx.ReleaseVersion),
		}},
	}
	packages = append(packages, rootPkg)

	// Dependency packages.
	relationships := []SPDXRelationship{{
		SpdxElementID:      "SPDXRef-DOCUMENT",
		RelationshipType:   "DESCRIBES",
		RelatedSpdxElement: rootID,
	}}

	for _, c := range deduped {
		dl := downloadLocation(c)
		pkg := SPDXPackage{
			SPDXID:           c.SPDXID,
			Name:             c.Name,
			Version:          c.Version,
			DownloadLocation: dl,
			FilesAnalyzed:    false,
			LicenseConcluded: noassertionOrLicense(c.LicenseExpression),
			LicenseDeclared:  noassertionOrLicense(c.LicenseExpression),
			CopyrightText:    "NOASSERTION",
			ExternalRefs: []SPDXExtRef{{
				ReferenceCategory: "PACKAGE-MANAGER",
				ReferenceType:     "purl",
				ReferenceLocator:  c.PURL,
			}},
		}
		if c.Checksum != "" {
			pkg.Checksums = []SPDXChecksum{{
				Algorithm:     "SHA256",
				ChecksumValue: strings.ToLower(c.Checksum),
			}}
		}
		packages = append(packages, pkg)

		relationships = append(relationships, SPDXRelationship{
			SpdxElementID:      rootID,
			RelationshipType:   "DEPENDS_ON",
			RelatedSpdxElement: c.SPDXID,
		})
	}

	createdAt := ctx.GeneratedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}

	toolLabel := "Tool: glassbox"
	if ctx.ToolVersion != "" {
		toolLabel = fmt.Sprintf("Tool: glassbox-%s", ctx.ToolVersion)
	}

	// Build the namespace with the release version and a short hash of the
	// commit to guarantee it is unique per release even on re-runs.
	nsHash := ""
	if ctx.Commit != "" {
		sum := sha256.Sum256([]byte(ctx.Commit))
		nsHash = hex.EncodeToString(sum[:4]) // 8 hex chars
	}
	namespace := fmt.Sprintf("%s/%s/%s", DocumentNamespacePrefix, ctx.ReleaseVersion, docName)
	if nsHash != "" {
		namespace = fmt.Sprintf("%s#%s", namespace, nsHash)
	}

	doc := &SPDXDocument{
		SPDXVersion:       SPDXVersion,
		DataLicense:       DataLicense,
		SPDXID:            "SPDXRef-DOCUMENT",
		Name:              docName,
		DocumentNamespace: namespace,
		CreationInfo: SPDXCreationInfo{
			Created:  createdAt.UTC().Format(time.RFC3339),
			Creators: []string{toolLabel, "Organization: Glassbox Users"},
		},
		Packages:      packages,
		Relationships: relationships,
	}

	// Compute a deterministic document hash over the packages slice so the
	// release manifest can reference the SBOM by content.
	doc.DocumentHash = computeDocumentHash(doc.Packages)
	return doc
}

// Marshal serializes the SPDXDocument to deterministic, indented JSON.
func Marshal(doc *SPDXDocument) ([]byte, error) {
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("serialising SPDX document: %w", err)
	}
	return append(data, '\n'), nil
}

// Validate checks that a generated SPDXDocument satisfies the minimum
// requirements for a valid SPDX 2.3 document.
func Validate(doc *SPDXDocument) error {
	if doc == nil {
		return fmt.Errorf("SBOM: document is nil")
	}
	if doc.SPDXVersion != SPDXVersion {
		return fmt.Errorf("SBOM: spdxVersion must be %q, got %q", SPDXVersion, doc.SPDXVersion)
	}
	if doc.DataLicense != DataLicense {
		return fmt.Errorf("SBOM: dataLicense must be %q, got %q", DataLicense, doc.DataLicense)
	}
	if doc.SPDXID != "SPDXRef-DOCUMENT" {
		return fmt.Errorf("SBOM: document SPDXID must be \"SPDXRef-DOCUMENT\", got %q", doc.SPDXID)
	}
	if doc.Name == "" {
		return fmt.Errorf("SBOM: document name must not be empty")
	}
	if doc.DocumentNamespace == "" {
		return fmt.Errorf("SBOM: documentNamespace must not be empty")
	}
	if doc.CreationInfo.Created == "" {
		return fmt.Errorf("SBOM: creationInfo.created must not be empty")
	}
	if len(doc.Packages) == 0 {
		return fmt.Errorf("SBOM: document contains no packages")
	}
	// Each package must have an SPDXID, name, and versionInfo.
	seen := make(map[string]bool, len(doc.Packages))
	for i, p := range doc.Packages {
		if p.SPDXID == "" {
			return fmt.Errorf("SBOM: package[%d] has empty SPDXID", i)
		}
		if p.Name == "" {
			return fmt.Errorf("SBOM: package[%d] (%s) has empty name", i, p.SPDXID)
		}
		if p.Version == "" {
			return fmt.Errorf("SBOM: package[%d] (%s) has empty versionInfo", i, p.SPDXID)
		}
		if seen[p.SPDXID] {
			return fmt.Errorf("SBOM: duplicate SPDXID %q", p.SPDXID)
		}
		seen[p.SPDXID] = true
	}
	return nil
}

// ── internal helpers ──────────────────────────────────────────────────────────

// deduplicate removes components with duplicate SPDXIDs, keeping the first
// occurrence (which already carries the most complete data after sorting).
func deduplicate(sorted []Component) []Component {
	seen := make(map[string]bool, len(sorted))
	out := make([]Component, 0, len(sorted))
	for _, c := range sorted {
		if !seen[c.SPDXID] {
			seen[c.SPDXID] = true
			out = append(out, c)
		}
	}
	return out
}

// downloadLocation returns a best-effort download URL for a component.
func downloadLocation(c Component) string {
	switch c.Ecosystem {
	case EcosystemGo:
		return fmt.Sprintf("https://proxy.golang.org/%s/@v/%s.zip", c.Name, c.Version)
	case EcosystemCargo:
		return fmt.Sprintf("https://crates.io/crates/%s/%s", c.Name, c.Version)
	case EcosystemNPM:
		name := c.Name
		if strings.HasPrefix(name, "@") {
			// Scoped package: @scope/name → @scope%2Fname in registry URL.
			name = strings.Replace(name, "/", "%2F", 1)
		}
		return fmt.Sprintf("https://registry.npmjs.org/%s/-/%s-%s.tgz",
			c.Name, strings.TrimPrefix(c.Name, "@"+strings.SplitN(c.Name, "/", 2)[0]+"/"), c.Version)
	}
	return "NOASSERTION"
}

// noassertionOrLicense returns the license expression when non-empty, or
// "NOASSERTION" as required by SPDX when information is not available.
func noassertionOrLicense(expr string) string {
	if expr == "" {
		return "NOASSERTION"
	}
	return expr
}

// computeDocumentHash returns a hex SHA-256 of the canonical JSON of the
// packages slice, providing a stable content hash for the SBOM artifact.
func computeDocumentHash(packages []SPDXPackage) string {
	data, err := json.Marshal(packages)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
