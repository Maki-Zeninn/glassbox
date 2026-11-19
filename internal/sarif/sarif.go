// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Package sarif implements a typed SARIF 2.1.0 exporter for Glassbox security
// findings. It produces output that validates against the official SARIF 2.1.0
// JSON schema and can be imported into GitHub Advanced Security, VS Code's
// SARIF viewer extension, and any other SARIF-compatible tool.
//
// # SARIF version
//
// This package targets SARIF 2.1.0 as defined by the OASIS standard:
//
//	https://docs.oasis-open.org/sarif/sarif/v2.1.0/sarif-v2.1.0.html
//
// The schema URI embedded in generated output is the normative SARIF 2.1.0
// schema:
//
//	https://json.schemastore.org/sarif-2.1.0.json
//
// # Rule IDs and stability
//
// Each Glassbox finding type maps to a stable SARIF rule ID of the form
// "GB-XXXX". The numeric suffix is derived from a deterministic hash of the
// finding type and title so that rule IDs never change across re-runs for the
// same finding class. The full mapping is documented in docs/sarif-export.md.
//
// # Fingerprints
//
// Finding fingerprints are computed as the SHA-256 of the canonical fields
// (ruleId + message + locationUri + startLine) and encoded as a 16-char hex
// prefix. This ensures that identical findings produce identical fingerprints
// across runs — allowing CI tools to deduplicate and suppress noise.
package sarif

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dotandev/glassbox/internal/security"
	"github.com/dotandev/glassbox/internal/version"
)

// SARIFVersion is the SARIF schema version this package targets.
const SARIFVersion = "2.1.0"

// SchemaURI is the normative JSON schema URI embedded in generated output.
const SchemaURI = "https://json.schemastore.org/sarif-2.1.0.json"

// ruleIDPrefix is the stable prefix for all Glassbox-originated rule IDs.
const ruleIDPrefix = "GB"

// ─── SARIF schema types ───────────────────────────────────────────────────────

// Log is the root SARIF document object.
type Log struct {
	Schema  string `json:"$schema"`
	Version string `json:"version"`
	Runs    []Run  `json:"runs"`
}

// Run represents a single invocation of the Glassbox analysis tool.
type Run struct {
	Tool        Tool        `json:"tool"`
	Results     []Result    `json:"results"`
	Artifacts   []Artifact  `json:"artifacts,omitempty"`
	Invocations []Invocation `json:"invocations"`
}

// Tool identifies the analysis tool that produced the run.
type Tool struct {
	Driver ToolComponent `json:"driver"`
}

// ToolComponent describes the primary tool driver.
type ToolComponent struct {
	Name           string `json:"name"`
	Version        string `json:"version"`
	InformationURI string `json:"informationUri"`
	Rules          []Rule `json:"rules,omitempty"`
}

// Rule is a descriptor for a single analysis rule.
type Rule struct {
	ID               string           `json:"id"`
	Name             string           `json:"name"`
	ShortDescription Message          `json:"shortDescription"`
	FullDescription  *Message         `json:"fullDescription,omitempty"`
	DefaultConfig    *RuleConfig      `json:"defaultConfiguration,omitempty"`
	HelpURI          string           `json:"helpUri,omitempty"`
	Properties       *RuleProperties  `json:"properties,omitempty"`
}

// RuleConfig holds the default severity level for a rule.
type RuleConfig struct {
	Level string `json:"level"`
}

// RuleProperties carries additional metadata attached to a rule.
type RuleProperties struct {
	// Tags is an optional list of tool-specific categorisation labels.
	Tags     []string `json:"tags,omitempty"`
	// Precision describes how likely a finding is to be a true positive.
	Precision string `json:"precision,omitempty"`
}

// Message is a human-readable text value used in various SARIF fields.
type Message struct {
	Text string `json:"text"`
}

// Result is a single finding emitted during the run.
type Result struct {
	RuleID    string     `json:"ruleId"`
	Level     string     `json:"level"`
	Message   Message    `json:"message"`
	Locations []Location `json:"locations,omitempty"`
	// Fingerprints provides stable identifiers for deduplication across runs.
	Fingerprints map[string]string `json:"fingerprints,omitempty"`
	// Suppressions lists any active suppressions for this finding.
	Suppressions []Suppression `json:"suppressions,omitempty"`
	// PartialFingerprints provides sub-fingerprints computed from individual fields.
	PartialFingerprints map[string]string `json:"partialFingerprints,omitempty"`
	// Properties carries Glassbox-specific extension fields.
	Properties *ResultProperties `json:"properties,omitempty"`
}

// Location pinpoints a finding in source code.
type Location struct {
	PhysicalLocation *PhysicalLocation `json:"physicalLocation,omitempty"`
	// LogicalLocation is used when a result has no source file to point at,
	// for example a WASM instruction offset that failed to resolve.
	LogicalLocation *LogicalLocation `json:"logicalLocation,omitempty"`
}

// LogicalLocation identifies a result by a program-level construct rather than
// by a source range, such as a WASM instruction offset.
type LogicalLocation struct {
	Name               string `json:"name,omitempty"`
	FullyQualifiedName string `json:"fullyQualifiedName,omitempty"`
	Kind               string `json:"kind,omitempty"`
}

// PhysicalLocation references a specific location in an artifact.
type PhysicalLocation struct {
	ArtifactLocation ArtifactLocation `json:"artifactLocation"`
	Region           *Region          `json:"region,omitempty"`
}

// ArtifactLocation is a reference to a file or resource.
type ArtifactLocation struct {
	// URI is relative to the repository root when possible.
	URI        string `json:"uri"`
	// URIBaseID identifies the root to which URI is relative.
	// Set to "%SRCROOT%" for repository-relative paths.
	URIBaseID  string `json:"uriBaseId,omitempty"`
	Index      *int   `json:"index,omitempty"`
}

// Region describes a specific line/column range within an artifact.
type Region struct {
	StartLine   int `json:"startLine,omitempty"`
	StartColumn int `json:"startColumn,omitempty"`
	EndLine     int `json:"endLine,omitempty"`
	EndColumn   int `json:"endColumn,omitempty"`
}

// Artifact is a file that was analyzed or referenced.
type Artifact struct {
	Location    ArtifactLocation `json:"location"`
	Description *Message         `json:"description,omitempty"`
}

// Suppression records a suppression decision for a result.
type Suppression struct {
	Kind          string   `json:"kind"` // "inSource" | "external"
	Status        string   `json:"status,omitempty"` // "accepted" | "rejected" | "underReview"
	Justification string   `json:"justification,omitempty"`
}

// Invocation records information about a single run of the tool.
type Invocation struct {
	ExecutionSuccessful bool      `json:"executionSuccessful"`
	StartTimeUTC        time.Time `json:"startTimeUtc,omitempty"`
	EndTimeUTC          time.Time `json:"endTimeUtc,omitempty"`
}

// ResultProperties carries Glassbox-specific extension fields on a result.
type ResultProperties struct {
	// FindingType is the Glassbox-internal FindingType value.
	FindingType string `json:"glassbox/findingType,omitempty"`
	// Evidence is the raw evidence string from the detector.
	Evidence string `json:"glassbox/evidence,omitempty"`
	// Suppressed reports whether this finding was suppressed in Glassbox.
	Suppressed bool `json:"glassbox/suppressed,omitempty"`
}

// ─── finding → rule ID mapping ────────────────────────────────────────────────

// ruleIDForFinding returns a stable rule ID for a finding by hashing its type
// and title. The result is always in the form "GB-XXXX" where XXXX is a
// zero-padded 4-digit decimal derived from the first 16 bits of the SHA-256
// of "<type>:<title>".
func ruleIDForFinding(f security.Finding) string {
	h := sha256.Sum256([]byte(string(f.Type) + ":" + f.Title))
	// Use the first 2 bytes as a uint16 → range [0, 65535]; cap at 9999 for
	// readability so IDs fit in 4 digits.
	n := (int(h[0])<<8 | int(h[1])) % 10000
	return fmt.Sprintf("%s-%04d", ruleIDPrefix, n)
}

// levelForSeverity converts a Glassbox severity to a SARIF level string.
func levelForSeverity(sev security.Severity) string {
	switch sev {
	case security.SeverityHigh:
		return "error"
	case security.SeverityMedium:
		return "warning"
	case security.SeverityLow:
		return "note"
	case security.SeverityInfo:
		return "note"
	default:
		return "none"
	}
}

// precisionForType returns a SARIF precision label for the finding type.
func precisionForType(ft security.FindingType) string {
	switch ft {
	case security.FindingVerifiedRisk:
		return "high"
	case security.FindingHeuristicWarn:
		return "medium"
	default:
		return "low"
	}
}

// ─── fingerprint ─────────────────────────────────────────────────────────────

// fingerprintForResult computes a stable 16-char hex fingerprint for a result.
// The fingerprint is keyed on "primaryLocationLineHash/v1" as required by GitHub
// Code Scanning for stable deduplication.
func fingerprintForResult(ruleID, message, locationURI string, startLine int) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%d",
		ruleID, message, locationURI, startLine)))
	return fmt.Sprintf("%x", h[:8]) // 16 hex chars
}

// ─── ExportOptions ────────────────────────────────────────────────────────────

// ExportOptions controls optional behaviour of the SARIF exporter.
type ExportOptions struct {
	// ArtifactURI is the file path or URI of the artifact that was scanned.
	// When non-empty it is added to the run's artifacts list and referenced
	// from every result's physicalLocation.
	ArtifactURI string

	// SuppressedFindings is the list of findings that were suppressed in
	// Glassbox. They are included in the SARIF output with a suppression
	// record so tools can distinguish suppressed from active findings.
	SuppressedFindings []security.Finding

	// RunStartTime is the wall-clock time at which the analysis started.
	// Defaults to time.Now() when zero.
	RunStartTime time.Time

	// RunEndTime is when the analysis completed. Defaults to time.Now().
	RunEndTime time.Time
}

// ─── Exporter ─────────────────────────────────────────────────────────────────

// Export converts a slice of Glassbox security findings into a SARIF 2.1.0 Log.
// Active findings are represented as open results; suppressed findings from
// opts.SuppressedFindings are included with a suppression record.
// The function is deterministic: given the same inputs it always produces the
// same output (results are sorted by ruleId then message text).
func Export(findings []security.Finding, opts ExportOptions) Log {
	now := time.Now().UTC()
	start := opts.RunStartTime
	if start.IsZero() {
		start = now
	}
	end := opts.RunEndTime
	if end.IsZero() {
		end = now
	}

	// Collect all unique rules referenced by active + suppressed findings.
	ruleMap := make(map[string]Rule)
	addRule := func(f security.Finding) {
		id := ruleIDForFinding(f)
		if _, exists := ruleMap[id]; exists {
			return
		}
		level := levelForSeverity(f.Severity)
		ruleMap[id] = Rule{
			ID:   id,
			Name: sanitizeRuleName(f.Title),
			ShortDescription: Message{Text: f.Title},
			DefaultConfig:    &RuleConfig{Level: level},
			Properties: &RuleProperties{
				Tags:      tagsForFinding(f),
				Precision: precisionForType(f.Type),
			},
		}
	}
	for _, f := range findings {
		addRule(f)
	}
	for _, f := range opts.SuppressedFindings {
		addRule(f)
	}

	// Sort rules for deterministic output.
	rules := make([]Rule, 0, len(ruleMap))
	for _, r := range ruleMap {
		rules = append(rules, r)
	}
	sort.Slice(rules, func(i, j int) bool { return rules[i].ID < rules[j].ID })

	// Build results for active findings.
	results := make([]Result, 0, len(findings)+len(opts.SuppressedFindings))
	for _, f := range findings {
		results = append(results, resultForFinding(f, false, opts.ArtifactURI))
	}
	// Build results for suppressed findings.
	for _, f := range opts.SuppressedFindings {
		r := resultForFinding(f, true, opts.ArtifactURI)
		results = append(results, r)
	}

	// Sort results for deterministic output: ruleId ASC, message ASC.
	sort.Slice(results, func(i, j int) bool {
		if results[i].RuleID != results[j].RuleID {
			return results[i].RuleID < results[j].RuleID
		}
		return results[i].Message.Text < results[j].Message.Text
	})

	// Build artifacts list.
	var artifacts []Artifact
	if opts.ArtifactURI != "" {
		artifacts = []Artifact{
			{Location: ArtifactLocation{URI: opts.ArtifactURI, URIBaseID: "%SRCROOT%"}},
		}
	}

	run := Run{
		Tool: Tool{
			Driver: ToolComponent{
				Name:           "Glassbox",
				Version:        version.Version,
				InformationURI: "https://github.com/dotandev/glassbox",
				Rules:          rules,
			},
		},
		Results:   results,
		Artifacts: artifacts,
		Invocations: []Invocation{
			{
				ExecutionSuccessful: true,
				StartTimeUTC:        start.UTC(),
				EndTimeUTC:          end.UTC(),
			},
		},
	}

	return Log{
		Schema:  SchemaURI,
		Version: SARIFVersion,
		Runs:    []Run{run},
	}
}

// Marshal serializes a SARIF Log to indented JSON. The output is valid UTF-8
// and is safe to write directly to a file.
func Marshal(log Log) ([]byte, error) {
	data, err := json.MarshalIndent(log, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("sarif: marshal failed: %w", err)
	}
	return data, nil
}

// ExportJSON is a convenience function that calls Export then Marshal.
func ExportJSON(findings []security.Finding, opts ExportOptions) ([]byte, error) {
	log := Export(findings, opts)
	return Marshal(log)
}

// ─── internal helpers ─────────────────────────────────────────────────────────

func resultForFinding(f security.Finding, suppressed bool, artifactURI string) Result {
	ruleID := ruleIDForFinding(f)
	level := levelForSeverity(f.Severity)

	// Build the message text: description is primary; append evidence when set.
	msgText := f.Description
	if f.Evidence != "" {
		msgText = fmt.Sprintf("%s (evidence: %s)", f.Description, f.Evidence)
	}

	var locations []Location
	if artifactURI != "" {
		idx := 0
		locations = []Location{
			{
				PhysicalLocation: &PhysicalLocation{
					ArtifactLocation: ArtifactLocation{
						URI:       artifactURI,
						URIBaseID: "%SRCROOT%",
						Index:     &idx,
					},
				},
			},
		}
	}

	fp := fingerprintForResult(ruleID, msgText, artifactURI, 0)

	r := Result{
		RuleID:  ruleID,
		Level:   level,
		Message: Message{Text: msgText},
		Locations: locations,
		Fingerprints: map[string]string{
			"primaryLocationLineHash/v1": fp,
		},
		PartialFingerprints: map[string]string{
			"ruleId/v1":   ruleID,
			"severity/v1": string(f.Severity),
			"title/v1":    f.Title,
		},
		Properties: &ResultProperties{
			FindingType: string(f.Type),
			Evidence:    f.Evidence,
			Suppressed:  suppressed,
		},
	}

	if suppressed {
		r.Suppressions = []Suppression{
			{
				Kind:          "external",
				Status:        "accepted",
				Justification: "Finding suppressed via Glassbox suppression registry",
			},
		}
	}

	return r
}

// sanitizeRuleName converts a finding title to a camelCase rule name safe for
// SARIF rule name fields (alphanumeric only, no spaces or special chars).
func sanitizeRuleName(title string) string {
	words := strings.Fields(title)
	var b strings.Builder
	for i, w := range words {
		clean := strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
				return r
			}
			return -1
		}, w)
		if clean == "" {
			continue
		}
		if i == 0 {
			b.WriteString(strings.ToLower(clean[:1]) + clean[1:])
		} else {
			b.WriteString(strings.ToUpper(clean[:1]) + clean[1:])
		}
	}
	name := b.String()
	if name == "" {
		return "glassboxFinding"
	}
	return name
}

// tagsForFinding returns categorisation tags for a finding.
func tagsForFinding(f security.Finding) []string {
	tags := []string{"security", "soroban"}
	switch f.Severity {
	case security.SeverityHigh:
		tags = append(tags, "high-severity")
	case security.SeverityMedium:
		tags = append(tags, "medium-severity")
	case security.SeverityLow:
		tags = append(tags, "low-severity")
	}
	if f.Type == security.FindingVerifiedRisk {
		tags = append(tags, "verified")
	} else if f.Type == security.FindingHeuristicWarn {
		tags = append(tags, "heuristic")
	}
	return tags
}
