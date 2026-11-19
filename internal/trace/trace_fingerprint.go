// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package trace

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
)

// fingerprintSchemaTag versions the fingerprint input layout. Bump it whenever
// the set of fields hashed below changes so that fingerprints from older
// binaries can never collide with fingerprints from newer ones.
const fingerprintSchemaTag = "glassbox-trace-fp-v1"

// Fingerprint returns a stable SHA-256 hex digest identifying the semantic
// content of the trace. It is used to key persisted viewer state so that
// state saved for one trace is never applied to a different one — including
// a re-fetched trace for the same transaction whose steps have changed.
//
// Only stable, semantic fields participate: the transaction hash, the number
// of steps, and each step's index, operation, event type, contract ID,
// function, and error. Volatile fields (timestamps, the mutable CurrentStep
// cursor, lazily populated snapshots, memory dumps) are excluded so that
// simply viewing or re-loading a trace does not change its fingerprint.
func (t *ExecutionTrace) Fingerprint() string {
	if t == nil {
		return ""
	}

	h := sha256.New()
	// Every field is written with a trailing NUL separator so that adjacent
	// values can never run together and collide ("ab"+"c" vs "a"+"bc").
	writeField := func(s string) {
		_, _ = h.Write([]byte(s))
		_, _ = h.Write([]byte{0})
	}

	writeField(fingerprintSchemaTag)
	writeField(t.TransactionHash)
	writeField(strconv.Itoa(len(t.States)))
	for i := range t.States {
		s := &t.States[i]
		writeField(strconv.Itoa(s.Step))
		writeField(s.Operation)
		writeField(s.EventType)
		writeField(s.ContractID)
		writeField(s.Function)
		writeField(s.Error)
	}

	return hex.EncodeToString(h.Sum(nil))
}
