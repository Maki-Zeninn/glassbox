// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package testhelpers

import (
	"github.com/dotandev/glassbox/internal/rpc"
)

// RPCFixture is a minimal builder for rpc.TransactionResponse used in regression tests.
type RPCFixture struct {
	EnvelopeXdr   string
	ResultXdr     string
	ResultMetaXdr string
	Status        string
	Ledger        uint32
}

// NewRPCFixture creates a new RPC fixture with stub defaults.
func NewRPCFixture() *RPCFixture {
	return &RPCFixture{
		EnvelopeXdr:   "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
		ResultXdr:     "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
		ResultMetaXdr: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
		Status:        "SUCCESS",
		Ledger:        1000,
	}
}

// WithEnvelope sets the envelope XDR (replaces the stub).
func (f *RPCFixture) WithEnvelope(xdr string) *RPCFixture {
	f.EnvelopeXdr = xdr
	return f
}

// WithResultMeta sets the result meta XDR (replaces the stub).
func (f *RPCFixture) WithResultMeta(xdr string) *RPCFixture {
	f.ResultMetaXdr = xdr
	return f
}

// NotFound sets the status to NOT_FOUND and clears all XDR fields.
func (f *RPCFixture) NotFound() *RPCFixture {
	f.Status = "NOT_FOUND"
	f.EnvelopeXdr = ""
	f.ResultXdr = ""
	f.ResultMetaXdr = ""
	f.Ledger = 0
	return f
}

// Build converts the fixture into an rpc.TransactionResponse.
func (f *RPCFixture) Build() *rpc.TransactionResponse {
	return &rpc.TransactionResponse{
		EnvelopeXdr:   f.EnvelopeXdr,
		ResultXdr:     f.ResultXdr,
		ResultMetaXdr: f.ResultMetaXdr,
		// Status is not exported by the rpc package; the response is assumed
		// to be success when XDR fields are non-empty. For NOT_FOUND, use
		// empty XDR and rely on the caller to interpret as a 404.
	}
}

// CanonicalTxHash is the stub transaction hash used consistently across all
// regression test fixtures. It is 64 valid hex characters.
const CanonicalTxHash = "5c0a1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab"

// CanonicalNetwork is the network used in regression fixtures unless the test
// explicitly exercises network-name validation.
const CanonicalNetwork = "testnet"

// CanonicalEnvelopeXDR is a minimal valid-looking base64 stub used when the
// test does not need real envelope content.
const CanonicalEnvelopeXDR = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

// CanonicalTimestamp is the ISO-8601 stub timestamp embedded in audit and
// session fixtures.
const CanonicalTimestamp = "2026-01-01T00:00:00Z"
