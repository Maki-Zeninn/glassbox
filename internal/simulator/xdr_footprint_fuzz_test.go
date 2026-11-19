// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

//go:build go1.18
// +build go1.18

package simulator

import (
	"encoding/binary"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// FuzzFootprintDecode passes random and mutated footprint bytes into
// DecodeFootprintBytes and asserts the decoder never panics [Issue #1114].
func FuzzFootprintDecode(f *testing.F) {
	empty, _ := xdr.LedgerFootprint{}.MarshalBinary()
	f.Add(empty)

	uid := xdr.Uint256{0x42}
	key := xdr.LedgerKey{
		Type: xdr.LedgerEntryTypeAccount,
		Account: &xdr.LedgerKeyAccount{
			AccountId: xdr.AccountId{
				Type:    xdr.PublicKeyTypePublicKeyTypeEd25519,
				Ed25519: &uid,
			},
		},
	}
	one, _ := (xdr.LedgerFootprint{ReadOnly: []xdr.LedgerKey{key}}).MarshalBinary()
	f.Add(one)

	f.Add([]byte{0xFF, 0xFF, 0x00, 0x00})

	unk := make([]byte, 16)
	binary.BigEndian.PutUint32(unk[0:4], 1)
	binary.BigEndian.PutUint32(unk[4:8], 0xFFFF)
	f.Add(unk)

	big := make([]byte, 8)
	binary.BigEndian.PutUint32(big[0:4], uint32(MaxLedgerEntries+50))
	f.Add(big)

	f.Fuzz(func(t *testing.T, data []byte) {
		mutated := append([]byte(nil), data...)
		if len(mutated) >= 4 {
			count := binary.BigEndian.Uint32(mutated[0:4])
			switch count % 3 {
			case 0:
				binary.BigEndian.PutUint32(mutated[0:4], 0)
			case 1:
				binary.BigEndian.PutUint32(mutated[0:4], uint32(MaxLedgerEntries+1))
			}
		}
		if len(mutated) >= 8 {
			binary.BigEndian.PutUint32(mutated[4:8], 0xFFFF)
		}
		_, _ = DecodeFootprintBytes(mutated)
		_, _ = DecodeFootprintBytes(data)
	})
}
