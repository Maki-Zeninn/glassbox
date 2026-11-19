// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

// This file extends the doctor command with the --bundle flag that generates
// a redacted diagnostics archive.  It is kept separate from doctor.go to avoid
// growing that file further.

import (
	"fmt"

	"github.com/dotandev/glassbox/internal/diagnostics"
)

// runDoctorBundle collects doctor check results, converts them to
// diagnostics.CheckResult values (redacting paths), and writes a portable
// diagnostics archive to outputPath.
//
// It returns the path of the written archive so the caller can print it.
func runDoctorBundle(checks []DependencyStatus, outputPath string) (string, error) {
	mapped := make([]diagnostics.CheckResult, 0, len(checks))
	for _, d := range checks {
		cr := diagnostics.CheckResult{
			ID:      string(d.ID),
			Name:    d.Name,
			OK:      d.Installed,
			Version: d.Version,
			FixHint: d.FixHint,
			Path:    diagnostics.RedactPath(d.Path),
		}
		mapped = append(mapped, cr)
	}

	path, err := diagnostics.GenerateBundle(
		rootCmd.Context(),
		diagnostics.BundleOptions{
			OutputPath:    outputPath,
			IncludeChecks: mapped,
		},
	)
	if err != nil {
		return "", fmt.Errorf("generate diagnostics bundle: %w", err)
	}
	return path, nil
}
