// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

//go:build go1.18

package protocolreg

import "testing"

// FuzzParseDebugURI exercises the glassbox:// URI parser with arbitrary input.
//
// Security boundary: ParseDebugURI accepts untrusted strings arriving from the
// OS URL dispatcher (macOS, Linux xdg-open, Windows shell) and from the
// --deep-link CLI flag. Any panic, hang, or excessive allocation is a bug
// regardless of the error value returned.
//
// Seed corpus covers: valid URIs, empty input, scheme variants, truncated
// paths, overlong inputs, path-injection patterns, URL-encoded bytes,
// control characters, and high-codepoint Unicode.
func FuzzParseDebugURI(f *testing.F) {
	validHash := "aabbccddeeff00112233445566778899aabbccddeeff001122334455667788aa"

	// Valid inputs that should parse successfully.
	f.Add("glassbox://debug/" + validHash + "?network=testnet")
	f.Add("glassbox://debug/" + validHash + "?network=mainnet&operation=0")
	f.Add("glassbox://debug/" + validHash + "?network=futurenet&operation=999&view=source")
	f.Add("glassbox://doctor-probe")

	// Wrong or missing scheme.
	f.Add("")
	f.Add("glassbox://")
	f.Add("glassbox://debug/")
	f.Add("glassbox://debug")
	f.Add("http://evil.com/debug/" + validHash)
	f.Add("glassbox:debug/" + validHash)
	f.Add("://debug/" + validHash)

	// Malformed hash (wrong length, wrong chars).
	f.Add("glassbox://debug/ZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZ?network=testnet")
	f.Add("glassbox://debug/aaaa?network=testnet")
	f.Add("glassbox://debug/g" + validHash[1:] + "?network=testnet")

	// Query injection / unexpected values.
	f.Add("glassbox://debug/" + validHash + "?network='; DROP TABLE sessions--")
	f.Add("glassbox://debug/" + validHash + "?network=testnet&operation=-1")
	f.Add("glassbox://debug/" + validHash + "?network=testnet&operation=99999999")
	f.Add("glassbox://debug/" + validHash + "?network=testnet&view=../../../../etc/passwd")

	// Path traversal (raw and percent-encoded).
	f.Add("glassbox://debug/../../../etc/passwd?network=testnet")
	f.Add("glassbox://debug/%2F%2F%2F?network=testnet")
	f.Add("glassbox://debug/%2e%2e%2fetc%2fpasswd?network=testnet")

	// Control characters.
	f.Add("glassbox://debug/\x00?network=testnet")
	f.Add("glassbox://debug/\r\n?network=testnet")

	// Unicode outside ASCII.
	f.Add("glassbox://debug/�?network=testnet")
	f.Add("glassbox://debug/héllo?network=testnet")

	// Overlong URL (exceeds maxURILen = 4096).
	f.Add("glassbox://debug/" + validHash + "?network=" + string(make([]byte, 5000)))

	f.Fuzz(func(t *testing.T, rawURL string) {
		// Must not panic regardless of input.
		_, _ = ParseDebugURI(rawURL)
	})
}
