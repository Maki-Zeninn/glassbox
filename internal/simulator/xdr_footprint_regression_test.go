// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package simulator

import (
	"encoding/base64"
	"errors"
	"testing"

	gberrors "github.com/dotandev/glassbox/internal/errors"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// footprintCase exercises DecodeFootprintBytes / DecodeFootprintXDR against
// XDR footprint boundary conditions [Issue #1114].
//
// Regression guide: docs/regression-test-guide.md
type footprintCase struct {
	name            string
	inputXDR        []byte
	expectErr       bool
	expectErrCode   string
	expectTruncated bool
}

func accountKey(b byte) xdr.LedgerKey {
	uid := xdr.Uint256{b}
	return xdr.LedgerKey{
		Type: xdr.LedgerEntryTypeAccount,
		Account: &xdr.LedgerKeyAccount{
			AccountId: xdr.AccountId{
				Type:    xdr.PublicKeyTypePublicKeyTypeEd25519,
				Ed25519: &uid,
			},
		},
	}
}

func marshalFootprint(t *testing.T, fp xdr.LedgerFootprint) []byte {
	t.Helper()
	b, err := fp.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}
	return b
}

// TestXDRFootprintRegression covers the six required boundary conditions.
func TestXDRFootprintRegression(t *testing.T) {
	// Structured regression template: docs/regression-test-guide.md
	// Fixture helpers (SimRequestFixture / SimResponseFixture) live in
	// internal/testhelpers and are exercised by the broader harness; this
	// file targets DecodeFootprintBytes directly to avoid an import cycle.
	overlapKey := accountKey(0xAA)
	overlapFP := marshalFootprint(t, xdr.LedgerFootprint{
		ReadOnly:  []xdr.LedgerKey{overlapKey},
		ReadWrite: []xdr.LedgerKey{overlapKey},
	})

	// Oversized: MaxLedgerEntries+1 distinct account keys.
	oversized := make([]xdr.LedgerKey, MaxLedgerEntries+1)
	for i := range oversized {
		oversized[i] = accountKey(byte(i + 1))
	}
	oversizedFP := marshalFootprint(t, xdr.LedgerFootprint{ReadOnly: oversized})

	validRO := marshalFootprint(t, xdr.LedgerFootprint{
		ReadOnly:  []xdr.LedgerKey{accountKey(0x01)},
		ReadWrite: []xdr.LedgerKey{accountKey(0x02)},
	})

	// Wrong XDR version prefix: force a 0xFFFF0000 discriminant prefix.
	wrongPrefix := []byte{0xFF, 0xFF, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}

	// Unknown / future ledger key type discriminant 0x0000FFFF followed by
	// enough trailing zeros that SafeUnmarshal may surface an enum error.
	unknownType := []byte{
		0x00, 0x00, 0x00, 0x01, // ReadOnly length = 1
		0x00, 0x00, 0xFF, 0xFF, // LedgerEntryType = 0xFFFF
		0x00, 0x00, 0x00, 0x00, // pad / stub body
		0x00, 0x00, 0x00, 0x00, // ReadWrite length = 0
	}

	cases := []footprintCase{
		{
			name:          "zero_entry_footprint",
			inputXDR:      marshalFootprint(t, xdr.LedgerFootprint{}),
			expectErr:     true,
			expectErrCode: string(gberrors.ErstEmptyFootprint),
		},
		{
			name:          "read_write_overlap",
			inputXDR:      overlapFP,
			expectErr:     true,
			expectErrCode: string(gberrors.ErstOverlappingFootprint),
		},
		{
			name:          "oversized_max_ledger_entries",
			inputXDR:      oversizedFP,
			expectErr:     true,
			expectErrCode: string(gberrors.ErstOversizedFootprint),
		},
		{
			name:          "unknown_ledger_key_type",
			inputXDR:      unknownType,
			expectErr:     true,
			expectErrCode: string(gberrors.ErstUnknownLedgerKeyType),
		},
		{
			name:          "wrong_xdr_version_prefix",
			inputXDR:      wrongPrefix,
			expectErr:     true,
			expectErrCode: string(gberrors.ErstFootprintXDRVersion),
		},
		{
			name:            "valid_disjoint_sets",
			inputXDR:        validRO,
			expectErr:       false,
			expectTruncated: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decoded, err := DecodeFootprintBytes(tc.inputXDR)
			if tc.expectErr {
				if err == nil {
					t.Fatalf("expected error with code %s, got nil", tc.expectErrCode)
				}
				code := gberrors.ClassifyError(err)
				if code == nil || string(code.Code) != tc.expectErrCode {
					// Fallback: match via errors.Is against the sentinel.
					var matched bool
					switch tc.expectErrCode {
					case string(gberrors.ErstEmptyFootprint):
						matched = errors.Is(err, gberrors.ErrEmptyFootprint)
					case string(gberrors.ErstOverlappingFootprint):
						matched = errors.Is(err, gberrors.ErrOverlappingFootprint)
					case string(gberrors.ErstOversizedFootprint):
						matched = errors.Is(err, gberrors.ErrOversizedFootprint)
					case string(gberrors.ErstUnknownLedgerKeyType):
						matched = errors.Is(err, gberrors.ErrUnknownLedgerKeyType)
					case string(gberrors.ErstFootprintXDRVersion):
						matched = errors.Is(err, gberrors.ErrFootprintXDRVersion)
					}
					if !matched {
						gotCode := ""
						if code != nil {
							gotCode = string(code.Code)
						}
						t.Fatalf("error=%v code=%q, want code %s (or matching sentinel)", err, gotCode, tc.expectErrCode)
					}
				}
				if tc.name == "read_write_overlap" && err != nil {
					msg := err.Error()
					if msg == "" {
						t.Fatal("overlap error must name the overlapping key")
					}
					// Message must mention the overlapping key (base64 fragment).
					if !containsAny(msg, "overlapping key") {
						t.Errorf("overlap error message %q must name the overlapping key", msg)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if decoded == nil {
				t.Fatal("expected non-nil decoded footprint")
			}
			if decoded.Truncated != tc.expectTruncated {
				t.Errorf("Truncated=%v, want %v", decoded.Truncated, tc.expectTruncated)
			}
			// Round-trip via base64 path as well.
			b64 := base64.StdEncoding.EncodeToString(tc.inputXDR)
			if _, err2 := DecodeFootprintXDR(b64); err2 != nil {
				t.Fatalf("DecodeFootprintXDR: %v", err2)
			}
		})
	}
}
