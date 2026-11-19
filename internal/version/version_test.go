// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package version

import (
	"strings"
	"testing"
)

func TestIsDev_DefaultVersion(t *testing.T) {
	orig := Version
	t.Cleanup(func() { Version = orig })

	Version = "0.0.0-dev"
	if !IsDev() {
		t.Error("IsDev() should return true for default dev version")
	}
}

func TestIsDev_RealVersion(t *testing.T) {
	orig := Version
	t.Cleanup(func() { Version = orig })

	Version = "1.2.3"
	if IsDev() {
		t.Error("IsDev() should return false for a real version")
	}
}

func TestShortSHA_Full(t *testing.T) {
	orig := CommitSHA
	t.Cleanup(func() { CommitSHA = orig })

	CommitSHA = "abcdef1234567890"
	got := ShortSHA()
	if got != "abcdef12" {
		t.Errorf("ShortSHA() = %q; want %q", got, "abcdef12")
	}
}

func TestShortSHA_Short(t *testing.T) {
	orig := CommitSHA
	t.Cleanup(func() { CommitSHA = orig })

	CommitSHA = "abc"
	got := ShortSHA()
	if got != "abc" {
		t.Errorf("ShortSHA() = %q; want %q", got, "abc")
	}
}

func TestShortSHA_Unknown(t *testing.T) {
	orig := CommitSHA
	t.Cleanup(func() { CommitSHA = orig })

	CommitSHA = "unknown"
	got := ShortSHA()
	if got != "unknown" {
		t.Errorf("ShortSHA() = %q; want %q", got, "unknown")
	}
}

func TestUserAgent_Format(t *testing.T) {
	origV := Version
	origC := CommitSHA
	origB := BuildDate
	t.Cleanup(func() {
		Version = origV
		CommitSHA = origC
		BuildDate = origB
	})

	Version = "1.2.3"
	CommitSHA = "deadbeef1234"
	BuildDate = "2026-01-01"

	ua := UserAgent()
	if ua != "glassbox/1.2.3 (deadbeef, 2026-01-01)" {
		t.Errorf("UserAgent() = %q; expected %q", ua, "glassbox/1.2.3 (deadbeef, 2026-01-01)")
	}

	BuildDate = "unknown"
	uaUnknown := UserAgent()
	if uaUnknown != "glassbox/1.2.3 (deadbeef)" {
		t.Errorf("UserAgent() = %q; expected %q", uaUnknown, "glassbox/1.2.3 (deadbeef)")
	}
}

func TestUserAgent_DevBuild(t *testing.T) {
	origV := Version
	origC := CommitSHA
	origB := BuildDate
	t.Cleanup(func() {
		Version = origV
		CommitSHA = origC
		BuildDate = origB
	})

	Version = "0.0.0-dev"
	CommitSHA = "unknown"
	BuildDate = "unknown"

	ua := UserAgent()
	if !strings.Contains(ua, "0.0.0-dev") {
		t.Errorf("UserAgent() = %q; expected dev version", ua)
	}
}

// ── ValidateVersionString ──────────────────────────────────────────────────

func TestValidateVersionString_Valid(t *testing.T) {
	tests := []string{"0.0.0", "1.0.0", "10.20.30", "0.0.1"}
	for _, v := range tests {
		if err := ValidateVersionString(v); err != nil {
			t.Errorf("ValidateVersionString(%q) returned error: %v", v, err)
		}
	}
}

func TestValidateVersionString_Empty(t *testing.T) {
	err := ValidateVersionString("")
	if err == nil {
		t.Fatal("expected error for empty version string")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error should mention 'empty', got: %q", err.Error())
	}
}

func TestValidateVersionString_TooFewComponents(t *testing.T) {
	err := ValidateVersionString("1.0")
	if err == nil {
		t.Fatal("expected error for two-component version")
	}
}

func TestValidateVersionString_TooManyComponents(t *testing.T) {
	err := ValidateVersionString("1.0.0.1")
	if err == nil {
		t.Fatal("expected error for four-component version")
	}
}

func TestValidateVersionString_NonNumeric(t *testing.T) {
	err := ValidateVersionString("1.0.beta")
	if err == nil {
		t.Fatal("expected error for non-numeric patch component")
	}
	if !strings.Contains(err.Error(), "non-digit") {
		t.Errorf("error should mention non-digit, got: %q", err.Error())
	}
}

func TestValidateVersionString_LeadingZero(t *testing.T) {
	for _, v := range []string{"01.0.0", "1.02.3", "1.0.00"} {
		err := ValidateVersionString(v)
		if err == nil {
			t.Fatalf("expected error for %q (semver forbids leading zeros)", v)
		}
		if !strings.Contains(err.Error(), "leading zero") {
			t.Errorf("error for %q should mention 'leading zero', got: %q", v, err.Error())
		}
	}
}
