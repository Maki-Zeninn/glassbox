// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package report

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/dotandev/glassbox/internal/errors"
	"github.com/dotandev/glassbox/internal/redaction"
)

type Exporter struct {
	outputDir string
	profile   *redaction.Profile
}

func NewExporter(outputDir string) (*Exporter, error) {
	return NewExporterWithProfile(outputDir, nil)
}

// NewExporterWithProfile creates an exporter that applies the given redaction
// profile to all report data before rendering. If profile is nil, no redaction
// is applied.
func NewExporterWithProfile(outputDir string, profile *redaction.Profile) (*Exporter, error) {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, errors.WrapValidationError(fmt.Sprintf("failed to create output directory: %v", err))
	}

	return &Exporter{outputDir: outputDir, profile: profile}, nil
}

func (e *Exporter) Export(report *Report, format string) (string, error) {
	// Apply redaction profile before rendering
	if e.profile != nil {
		report = e.redactReport(report)
	}

	filename := generateFilename(report.Title, format)
	filepath := filepath.Join(e.outputDir, filename)

	var data []byte
	var err error

	switch strings.ToLower(format) {
	case "json":
		data, err = json.MarshalIndent(report, "", "  ")
	case "html":
		renderer := NewHTMLRenderer()
		data, err = renderer.Render(report)
	case "pdf":
		renderer := NewPDFRenderer()
		data, err = renderer.Render(report)
	default:
		return "", errors.WrapValidationError(fmt.Sprintf("unsupported format: %s", format))
	}

	if err != nil {
		return "", errors.WrapValidationError(fmt.Sprintf("failed to render report: %v", err))
	}

	if err := os.WriteFile(filepath, data, 0644); err != nil {
		return "", errors.WrapValidationError(fmt.Sprintf("failed to write file: %v", err))
	}

	return filepath, nil
}

func (e *Exporter) ExportMultiple(report *Report, formats []string) (map[string]string, error) {
	results := make(map[string]string)

	for _, format := range formats {
		path, err := e.Export(report, format)
		if err != nil {
			return results, errors.WrapValidationError(fmt.Sprintf("failed to export %s: %v", format, err))
		}
		results[format] = path
	}

	return results, nil
}

// redactReport returns a copy of the report with sensitive fields redacted
// according to the configured profile.
func (e *Exporter) redactReport(r *Report) *Report {
	if r == nil || e.profile == nil {
		return r
	}

	// Deep copy the report
	out := *r

	if r.Summary != nil {
		s := *r.Summary
		s.KeyFindings = redactStringSlice(e.profile, s.KeyFindings)
		out.Summary = &s
	}

	if r.Execution != nil {
		ex := *r.Execution
		ex.TransactionHash = redactValue(e.profile, ex.TransactionHash)
		for i := range ex.Steps {
			ex.Steps[i].ContractID = redactValue(e.profile, ex.Steps[i].ContractID)
			ex.Steps[i].Details = redactValue(e.profile, ex.Steps[i].Details)
			ex.Steps[i].SourceFile = redactValue(e.profile, ex.Steps[i].SourceFile)
		}
		for i := range ex.ErrorTrace {
			ex.ErrorTrace[i] = redactValue(e.profile, ex.ErrorTrace[i])
		}
		out.Execution = &ex
	}

	if r.Metadata != nil {
		m := *r.Metadata
		if m.Tags != nil {
			m.Tags = e.profile.ApplyToStringMap(m.Tags)
		}
		out.Metadata = &m
	}

	return &out
}

func redactValue(p *redaction.Profile, s string) string {
	return p.Apply(s)
}

func redactStringSlice(p *redaction.Profile, ss []string) []string {
	if ss == nil {
		return nil
	}
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = p.Apply(s)
	}
	return out
}

func generateFilename(title string, format string) string {
	sanitized := sanitizeFilename(title)
	timestamp := time.Now().Format("20060102-150405")
	return fmt.Sprintf("%s-%s.%s", sanitized, timestamp, format)
}

func sanitizeFilename(name string) string {
	reg := regexp.MustCompile("[^a-zA-Z0-9-_]")
	sanitized := reg.ReplaceAllString(name, "_")
	sanitized = strings.ToLower(sanitized)

	for strings.Contains(sanitized, "__") {
		sanitized = strings.ReplaceAll(sanitized, "__", "_")
	}

	if len(sanitized) > 50 {
		sanitized = sanitized[:50]
	}

	return sanitized
}
