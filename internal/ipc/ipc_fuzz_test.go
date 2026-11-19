// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

//go:build go1.18

package ipc

import "testing"

// FuzzUnmarshalSimulationRequest fuzzes IPC simulation request JSON parsing.
//
// Security boundary: these messages arrive from the Rust simulator over a local
// socket. Malformed JSON must not panic or hang the Go side. The fuzzer targets
// the JSON decoder and the struct field mapping, not the business logic.
func FuzzUnmarshalSimulationRequest(f *testing.F) {
	f.Add([]byte(`{"network":"testnet","request_id":"r1","version":"1","xdr":""}`))
	f.Add([]byte(`{"network":"mainnet","request_id":"","version":"0","xdr":"AAAAAA=="}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`[]`))
	f.Add([]byte(`null`))
	f.Add([]byte(nil))
	f.Add([]byte(`{"xdr":null,"network":null}`))
	// Overlong field values.
	f.Add([]byte(`{"network":"` + string(make([]byte, 10000)) + `"}`))
	// Deeply nested (should not stack-overflow).
	f.Add([]byte(`{"request_id":{"a":{"b":{"c":"d"}}}}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		req, err := UnmarshalSimulationRequestSchema(data)
		if err != nil {
			return
		}
		// Successful parse must round-trip without panicking.
		_, _ = req.Marshal()
	})
}

// FuzzUnmarshalSimulationResponse fuzzes IPC simulation response JSON parsing.
func FuzzUnmarshalSimulationResponse(f *testing.F) {
	f.Add([]byte(`{"status":"ok","cost":{"cpu_insns":"0","mem_bytes":"0"}}`))
	f.Add([]byte(`{"status":"error","error":{"code":"WASM_TRAP","message":"unreachable"}}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`null`))
	f.Add([]byte(nil))
	f.Add([]byte(`{"results":[],"cost":null}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		resp, err := UnmarshalSimulationResponseSchema(data)
		if err != nil {
			return
		}
		_, _ = resp.Marshal()
	})
}

// FuzzUnmarshalHandshakeRequest fuzzes IPC handshake request JSON parsing.
func FuzzUnmarshalHandshakeRequest(f *testing.F) {
	f.Add([]byte(`{"type":"handshake","protocol_version":21,"client_version":"1.0.0","required_features":[]}`))
	f.Add([]byte(`{"type":"handshake","protocol_version":0}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`"string value"`))
	f.Add([]byte(nil))

	f.Fuzz(func(t *testing.T, data []byte) {
		req, err := UnmarshalHandshakeRequest(data)
		if err != nil {
			return
		}
		_, _ = req.Marshal()
	})
}

// FuzzIPCErrorClassification fuzzes IPC error code classification with arbitrary
// code and message strings sourced from the Rust simulator.
//
// classifyByMessage is a security-relevant function: if it panics or returns an
// unexpected nil, callers may crash or misclassify errors in ways that hide
// security-relevant failure modes.
func FuzzIPCErrorClassification(f *testing.F) {
	f.Add("SIMULATION_FAILED", "failed to execute")
	f.Add("WASM_TRAP", "wasm trap: unreachable")
	f.Add("INVALID_INPUT", "decode Envelope: unexpected end of input")
	f.Add("ERR_MEMORY_LIMIT_EXCEEDED", "memory limit exceeded: 134MB > 128MB")
	f.Add("CONTRACT_TRAP", "out of bounds memory access")
	f.Add("PROTOCOL_UNSUPPORTED", "protocol version mismatch")
	f.Add("", "")
	f.Add(string(make([]byte, 1000)), string(make([]byte, 1000)))
	// Strings that could confuse string-contains heuristics.
	f.Add("OK", "decode Envelope: ok but also wasm trap unreachable and InvalidInput")
	f.Add("decode WASM", "stack overflow decode LedgerKey")

	f.Fuzz(func(t *testing.T, code, message string) {
		e := &Error{Code: code, Message: message}
		result := e.ToErstError()
		if result == nil {
			t.Fatal("ToErstError must never return nil")
		}
	})
}
