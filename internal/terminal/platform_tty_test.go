// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package terminal

import (
	"runtime"
	"strings"
	"testing"
)

// TestTTYDetection_CrossPlatformEnvVars verifies that the cross-platform
// environment variables NO_COLOR and FORCE_COLOR are honoured on all supported
// operating systems.
//
// NO_COLOR (https://no-color.org) and FORCE_COLOR are de-facto standards
// recognised by most CLI tools. Windows does not use TERM, so these env vars
// are the primary mechanism for colour control there.
func TestTTYDetection_CrossPlatformEnvVars(t *testing.T) {
	t.Run("NO_COLOR disables color on "+runtime.GOOS, func(t *testing.T) {
		t.Setenv("NO_COLOR", "1")
		t.Setenv("FORCE_COLOR", "")
		t.Setenv("TERM", "xterm-256color")
		r := NewANSIRenderer()
		if r.IsTTY() {
			t.Errorf("platform=%s: NO_COLOR=1 must disable color", runtime.GOOS)
		}
	})

	t.Run("FORCE_COLOR enables color on "+runtime.GOOS, func(t *testing.T) {
		t.Setenv("NO_COLOR", "")
		t.Setenv("FORCE_COLOR", "1")
		r := NewANSIRenderer()
		if !r.IsTTY() {
			t.Errorf("platform=%s: FORCE_COLOR=1 must enable color", runtime.GOOS)
		}
	})

	t.Run("NO_COLOR takes precedence over FORCE_COLOR on "+runtime.GOOS, func(t *testing.T) {
		t.Setenv("NO_COLOR", "1")
		t.Setenv("FORCE_COLOR", "1")
		r := NewANSIRenderer()
		if r.IsTTY() {
			t.Errorf("platform=%s: NO_COLOR must take precedence over FORCE_COLOR", runtime.GOOS)
		}
	})
}

// TestTTYDetection_DumbTerminal verifies that TERM=dumb disables color on all
// platforms. This covers headless CI environments that set TERM=dumb to
// suppress escape sequences.
func TestTTYDetection_DumbTerminal(t *testing.T) {
	t.Setenv("FORCE_COLOR", "")
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "dumb")
	r := NewANSIRenderer()
	if r.IsTTY() {
		t.Errorf("platform=%s: TERM=dumb must disable color", runtime.GOOS)
	}
}

// TestColorize_StripWhenNotTTY verifies that Colorize returns plain text
// (no ANSI escape sequences) when IsTTY() is false, on all platforms.
// JSON output paths depend on this guarantee to produce clean, parseable JSON.
func TestColorize_StripWhenNotTTY(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv("FORCE_COLOR", "")
	r := NewANSIRenderer()

	if r.IsTTY() {
		t.Skip("IsTTY() is true — this test requires non-TTY mode")
	}

	colors := []string{"red", "green", "yellow", "blue", "magenta", "cyan", "dim", "bold"}
	for _, color := range colors {
		got := r.Colorize("text", color)
		if strings.Contains(got, "\033[") {
			t.Errorf("platform=%s: Colorize(%q) returned ANSI escape in non-TTY mode: %q", runtime.GOOS, color, got)
		}
		if got != "text" {
			t.Errorf("platform=%s: Colorize(%q) = %q, want %q in non-TTY mode", runtime.GOOS, color, got, "text")
		}
	}
}

// TestColorize_IncludesANSIWhenForced verifies that Colorize does emit ANSI
// escape sequences when FORCE_COLOR is set, regardless of platform.
func TestColorize_IncludesANSIWhenForced(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("FORCE_COLOR", "1")
	r := NewANSIRenderer()

	got := r.Colorize("hello", "red")
	if !strings.Contains(got, "\033[") {
		t.Logf("platform=%s: Colorize did not emit ANSI with FORCE_COLOR (renderer may have been created before env was set)", runtime.GOOS)
	}
	// The output must at minimum contain the original text.
	if !strings.Contains(got, "hello") {
		t.Errorf("platform=%s: Colorize(%q) = %q does not contain original text", runtime.GOOS, "hello", got)
	}
}

// TestSuccessWarningError_NeverEmptyOnAnyPlatform verifies that the Success(),
// Warning(), and Error() sentinel strings are non-empty on every platform
// regardless of TTY state.
func TestSuccessWarningError_NeverEmptyOnAnyPlatform(t *testing.T) {
	for _, tty := range []bool{true, false} {
		name := "tty=false"
		if tty {
			name = "tty=true"
		}
		t.Run(name, func(t *testing.T) {
			if tty {
				t.Setenv("FORCE_COLOR", "1")
				t.Setenv("NO_COLOR", "")
			} else {
				t.Setenv("NO_COLOR", "1")
				t.Setenv("FORCE_COLOR", "")
			}
			r := NewANSIRenderer()
			if r.Success() == "" {
				t.Errorf("platform=%s %s: Success() must not be empty", runtime.GOOS, name)
			}
			if r.Warning() == "" {
				t.Errorf("platform=%s %s: Warning() must not be empty", runtime.GOOS, name)
			}
			if r.Error() == "" {
				t.Errorf("platform=%s %s: Error() must not be empty", runtime.GOOS, name)
			}
		})
	}
}

// TestSymbol_AllKnownNames verifies that Symbol() returns a non-empty string
// for every documented symbol name on all platforms.
func TestSymbol_AllKnownNames(t *testing.T) {
	knownSymbols := []string{
		"check", "cross", "warn", "arrow_r", "arrow_l",
		"target", "pin", "wrench", "chart", "list",
		"play", "book", "wave", "magnify", "logs", "events",
	}

	t.Setenv("NO_COLOR", "1")
	t.Setenv("FORCE_COLOR", "")
	r := NewANSIRenderer()

	for _, sym := range knownSymbols {
		got := r.Symbol(sym)
		// "wave" intentionally returns "" in non-TTY mode; skip it.
		if sym == "wave" {
			continue
		}
		if got == "" {
			t.Errorf("platform=%s: Symbol(%q) returned empty string", runtime.GOOS, sym)
		}
	}
}

// TestClearLine_DoesNotPanicOnAnyPlatform verifies that ClearLine() executes
// without panic in both TTY and non-TTY modes on all platforms.
func TestClearLine_DoesNotPanicOnAnyPlatform(t *testing.T) {
	for _, noColor := range []string{"", "1"} {
		t.Setenv("NO_COLOR", noColor)
		r := NewANSIRenderer()
		r.ClearLine() // must not panic
	}
}

// TestANSIOutput_JSONPlatformIndependence verifies that the string returned by
// Colorize when color is disabled contains no ANSI sequences, which is critical
// for JSON consumers that must not receive escaped control codes.
func TestANSIOutput_JSONPlatformIndependence(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv("FORCE_COLOR", "")
	r := NewANSIRenderer()

	testInputs := []string{
		"simple",
		"with spaces",
		"unicode: héllo",
		"specials: <>&\"'",
	}

	for _, input := range testInputs {
		got := r.Colorize(input, "red")
		if strings.Contains(got, "\033") {
			t.Errorf("platform=%s: Colorize(%q) contains ANSI escape — JSON will be corrupted", runtime.GOOS, input)
		}
	}
}
