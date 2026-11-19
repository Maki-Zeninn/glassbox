// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package version

import (
	"fmt"
	"strings"
)

var (
	// Version is the SDK version, populated by ldflags during build.
	// Defaults to "0.0.0-dev" when running without a proper build.
	Version = "0.0.0-dev"
	// CommitSHA is the git commit SHA, populated by ldflags during build.
	CommitSHA = "unknown"
	// BuildDate is the build date, populated by ldflags during build.
	BuildDate = "unknown"
)

// IsDev reports whether the binary was built without ldflags version injection.
// This is true when Version still carries the default "0.0.0-dev" placeholder.
func IsDev() bool {
	return Version == "0.0.0-dev"
}

const shortSHALen = 8

// ShortSHA returns the first 8 characters of CommitSHA, or "unknown" if unset.
func ShortSHA() string {
	if len(CommitSHA) >= shortSHALen {
		return CommitSHA[:shortSHALen]
	}
	return CommitSHA
}

// UserAgent returns a User-Agent / metadata string suitable for RPC headers
// and diagnostic output: "glassbox/<version> (<commit>)" or
// "glassbox/<version> (<commit>, <build-date>)".
func UserAgent() string {
	if BuildDate != "unknown" {
		return fmt.Sprintf("glassbox/%s (%s, %s)", Version, ShortSHA(), BuildDate)
	}
	return fmt.Sprintf("glassbox/%s (%s)", Version, ShortSHA())
}

// VersionValidationError describes a version string format or value problem.
type VersionValidationError struct {
	Value    string
	Reason   string
	Expected string
}

func (e *VersionValidationError) Error() string {
	return fmt.Sprintf("invalid version %q: %s (expected: %s)", e.Value, e.Reason, e.Expected)
}

// ValidateVersionString checks that v looks like a valid semver version string.
// Accepted format: MAJOR.MINOR.PATCH where each component is a non-negative integer.
func ValidateVersionString(v string) error {
	if v == "" {
		return &VersionValidationError{
			Value:    v,
			Reason:   "version string is empty",
			Expected: "MAJOR.MINOR.PATCH (e.g. 1.0.0)",
		}
	}

	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return &VersionValidationError{
			Value:    v,
			Reason:   fmt.Sprintf("expected 3 components, got %d", len(parts)),
			Expected: "MAJOR.MINOR.PATCH (e.g. 1.0.0)",
		}
	}

	for i, p := range parts {
		if p == "" {
			return &VersionValidationError{
				Value:    v,
				Reason:   fmt.Sprintf("component %d is empty", i+1),
				Expected: "MAJOR.MINOR.PATCH with all numeric components",
			}
		}
		for _, c := range p {
			if c < '0' || c > '9' {
				return &VersionValidationError{
					Value:    v,
					Reason:   fmt.Sprintf("component %d contains non-digit %q", i+1, c),
					Expected: "MAJOR.MINOR.PATCH with all numeric components",
				}
			}
		}
		if len(p) > 1 && p[0] == '0' {
			return &VersionValidationError{Value: v, Reason: fmt.Sprintf("component %d has a leading zero", i+1), Expected: "MAJOR.MINOR.PATCH with no leading zeros"}
		}
	}

	return nil
}
