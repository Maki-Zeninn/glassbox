// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package security

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ReportFormatter formats security findings with suppression information.
type ReportFormatter struct {
	includeSuppressed bool
	showRawJSON       bool
}

// NewReportFormatter creates a new report formatter.
func NewReportFormatter(includeSuppressed bool) *ReportFormatter {
	return &ReportFormatter{
		includeSuppressed: includeSuppressed,
		showRawJSON:       false,
	}
}

// FormatDetectorReport formats a detector result with suppression information.
func (f *ReportFormatter) FormatDetectorReport(result DetectorResultWithSuppression) string {
	var builder strings.Builder
	
	builder.WriteString("Security Findings Report\n")
	builder.WriteString("========================\n\n")
	
	// Active findings
	builder.WriteString(fmt.Sprintf("Active Findings: %d\n", len(result.ActiveFindings)))
	if len(result.ActiveFindings) > 0 {
		for i, finding := range result.ActiveFindings {
			builder.WriteString(fmt.Sprintf("\n  %d. [%s] %s\n", i+1, finding.Severity, finding.Title))
			builder.WriteString(fmt.Sprintf("     Type: %s\n", finding.Type))
			if finding.Evidence != "" {
				builder.WriteString(fmt.Sprintf("     Evidence: %s\n", finding.Evidence))
			}
			if finding.Description != "" {
				builder.WriteString(fmt.Sprintf("     Description: %s\n", finding.Description))
			}
		}
	} else {
		builder.WriteString("  No active findings.\n")
	}
	
	// Suppressed findings
	if f.includeSuppressed && len(result.SuppressedFindings) > 0 {
		builder.WriteString(fmt.Sprintf("\n\nSuppressed Findings: %d\n", len(result.SuppressedFindings)))
		for i, suppressed := range result.SuppressedFindings {
			builder.WriteString(fmt.Sprintf("\n  %d. [%s] %s (SUPPRESSED)\n", i+1, suppressed.Finding.Severity, suppressed.Finding.Title))
			builder.WriteString(fmt.Sprintf("     Type: %s\n", suppressed.Finding.Type))
			if suppressed.Finding.Evidence != "" {
				builder.WriteString(fmt.Sprintf("     Evidence: %s\n", suppressed.Finding.Evidence))
			}
			
			// Suppression metadata
			if suppressed.Record != nil {
				builder.WriteString(fmt.Sprintf("     Suppression Reason: %s\n", suppressed.Record.Reason))
				builder.WriteString(fmt.Sprintf("     Suppression Owner: %s\n", suppressed.Record.Owner))
				if !suppressed.Record.ExpiresAt.IsZero() {
					builder.WriteString(fmt.Sprintf("     Expires: %s\n", suppressed.Record.ExpiresAt.UTC().Format(time.RFC3339)))
				}
				if suppressed.Record.Reviewer != "" {
					builder.WriteString(fmt.Sprintf("     Reviewed by: %s\n", suppressed.Record.Reviewer))
				}
				if suppressed.Record.Signature != "" {
					builder.WriteString(fmt.Sprintf("     Signed: Yes\n"))
				}
			}
		}
	}
	
	return builder.String()
}

// FormatSecretReport formats a secret scan result with suppression information.
func (f *ReportFormatter) FormatSecretReport(result ScanResultWithSuppression) string {
	var builder strings.Builder
	
	builder.WriteString("Secret Scan Report\n")
	builder.WriteString("==================\n\n")
	
	// Active findings
	builder.WriteString(fmt.Sprintf("Active Secrets: %d\n", len(result.ActiveFindings)))
	if len(result.ActiveFindings) > 0 {
		for i, finding := range result.ActiveFindings {
			builder.WriteString(fmt.Sprintf("\n  %d. [%s] at %s\n", i+1, finding.Type, finding.Location))
			if finding.Context != "" {
				builder.WriteString(fmt.Sprintf("     Context: %s\n", finding.Context))
			}
		}
	} else {
		builder.WriteString("  No active secrets.\n")
	}
	
	// Suppressed findings
	if f.includeSuppressed && len(result.SuppressedFindings) > 0 {
		builder.WriteString(fmt.Sprintf("\n\nSuppressed Secrets: %d\n", len(result.SuppressedFindings)))
		for i, suppressed := range result.SuppressedFindings {
			builder.WriteString(fmt.Sprintf("\n  %d. [%s] at %s (SUPPRESSED)\n", i+1, suppressed.Finding.Type, suppressed.Finding.Location))
			if suppressed.Finding.Context != "" {
				builder.WriteString(fmt.Sprintf("     Context: %s\n", suppressed.Finding.Context))
			}
			
			// Suppression metadata
			if suppressed.Record != nil {
				builder.WriteString(fmt.Sprintf("     Suppression Reason: %s\n", suppressed.Record.Reason))
				builder.WriteString(fmt.Sprintf("     Suppression Owner: %s\n", suppressed.Record.Owner))
				if !suppressed.Record.ExpiresAt.IsZero() {
					builder.WriteString(fmt.Sprintf("     Expires: %s\n", suppressed.Record.ExpiresAt.UTC().Format(time.RFC3339)))
				}
				if suppressed.Record.Reviewer != "" {
					builder.WriteString(fmt.Sprintf("     Reviewed by: %s\n", suppressed.Record.Reviewer))
				}
			}
		}
	}
	
	return builder.String()
}

// FormatDetectorJSON formats a detector result as JSON with suppression information.
func (f *ReportFormatter) FormatDetectorJSON(result DetectorResultWithSuppression) (string, error) {
	data := map[string]interface{}{
		"active_findings":     result.ActiveFindings,
		"suppressed_findings": result.SuppressedFindings,
		"active_count":        len(result.ActiveFindings),
		"suppressed_count":    len(result.SuppressedFindings),
	}
	
	jsonBytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal detector result: %w", err)
	}
	
	return string(jsonBytes), nil
}

// FormatSecretJSON formats a secret scan result as JSON with suppression information.
func (f *ReportFormatter) FormatSecretJSON(result ScanResultWithSuppression) (string, error) {
	data := map[string]interface{}{
		"active_findings":     result.ActiveFindings,
		"suppressed_findings": result.SuppressedFindings,
		"active_count":        len(result.ActiveFindings),
		"suppressed_count":    len(result.SuppressedFindings),
		"has_secrets":         result.HasSecrets,
	}
	
	jsonBytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal secret result: %w", err)
	}
	
	return string(jsonBytes), nil
}

// FormatRawFindings returns all findings without suppression applied.
// This ensures raw JSON always contains all findings.
func (f *ReportFormatter) FormatRawFindings(findings []Finding) (string, error) {
	jsonBytes, err := json.MarshalIndent(findings, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal raw findings: %w", err)
	}
	return string(jsonBytes), nil
}

// FormatRawSecretFindings returns all secret findings without suppression applied.
func (f *ReportFormatter) FormatRawSecretFindings(findings []SecretFinding) (string, error) {
	jsonBytes, err := json.MarshalIndent(findings, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal raw secret findings: %w", err)
	}
	return string(jsonBytes), nil
}

// FindingsToJSON serialises a slice of findings to a JSON string. This is a
// thin helper used by the scan command's --format json output path.
func FindingsToJSON(findings []Finding) string {
	if findings == nil {
		findings = []Finding{}
	}
	data := map[string]interface{}{
		"findings": findings,
		"count":    len(findings),
	}
	out, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return `{"findings":[],"count":0}`
	}
	return string(out)
}
