// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package manifest

import "crypto/ed25519"

// verifyEd25519 wraps the standard library call so the main file does not
// need to import crypto/ed25519 directly. This also makes the verification
// path easy to test with dependency injection if needed.
func verifyEd25519(publicKey, message, sig []byte) bool {
	if len(publicKey) != ed25519.PublicKeySize {
		return false
	}
	if len(sig) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(ed25519.PublicKey(publicKey), message, sig)
}
