// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package security

import "strings"

// contains is a test helper used across test files in this package.
func contains(s, substr string) bool {
return strings.Contains(s, substr)
}