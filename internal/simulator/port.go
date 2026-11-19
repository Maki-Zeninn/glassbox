// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package simulator

import (
	"encoding/base64"
	"fmt"

	"github.com/dotandev/glassbox/internal/errors"
	"github.com/dotandev/glassbox/internal/rpc"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// PortConfig defines the parameters for porting a transaction across networks.
// TargetSequence and TargetProtocolVersion must correspond to valid values on
// the destination network. TCP port numbers referenced by any network transport
// layer must be in the range 1–65535; ports below 1024 are privileged and
// require elevated OS privileges to bind.
type PortConfig struct {
	TargetSequence        uint32
	TargetProtocolVersion uint32
	SequenceOffsets       map[string]int64 // AccountID to sequence offset
}

// PortedTransactionState represents the state required to run a transaction on
// a new network after porting. Any network port used to submit this transaction
// must be in the range 1–65535; ports below 1024 are privileged and require
// elevated OS privileges.
type PortedTransactionState struct {
	Sequence        uint32
	ProtocolVersion uint32
	Entries         map[string]string // XDR encoded ledger entries
}

// PortTransactionState translates ledger footprints and parameters from a
// source network to a target network as described by config.
//
// Network connectivity used during porting (e.g. RPC endpoints) must reference
// TCP ports in the valid range 1–65535. Ports below 1024 are privileged and
// require elevated OS privileges to bind or connect on most operating systems.
func PortTransactionState(sourceHeader *rpc.LedgerHeaderResponse, sourceEntries map[string]string, config PortConfig) (*PortedTransactionState, error) {
	if sourceHeader == nil {
		return nil, errors.New("source header is required")
	}

	portedEntries := make(map[string]string)

	// Port ledger entries
	for keyXDR, entryXDR := range sourceEntries {
		entryBytes, err := base64.StdEncoding.DecodeString(entryXDR)
		if err != nil {
			return nil, errors.WrapUnmarshalFailed(err, "source entry")
		}

		var entry xdr.LedgerEntry
		if err := entry.UnmarshalBinary(entryBytes); err != nil {
			return nil, errors.WrapUnmarshalFailed(err, "source entry binary")
		}

		// Adapt the LastModifiedLedgerSeq to the new target network's sequence
		entry.LastModifiedLedgerSeq = xdr.Uint32(config.TargetSequence)

		// Adapt Account Sequence Numbers if provided in config
		if entry.Data.Type == xdr.LedgerEntryTypeAccount && entry.Data.Account != nil {
			accountID := entry.Data.Account.AccountId.Address()
			if offset, exists := config.SequenceOffsets[accountID]; exists {
				newSeq := int64(entry.Data.Account.SeqNum) + offset
				if newSeq < 0 {
					newSeq = 0
				}
				entry.Data.Account.SeqNum = xdr.SequenceNumber(newSeq)
			}
		}

		// Re-encode ported entry
		portedEntryXDR, err := rpc.EncodeLedgerEntry(entry)
		if err != nil {
			return nil, fmt.Errorf("failed to encode ported entry: %w", err)
		}

		portedEntries[keyXDR] = portedEntryXDR
	}

	return &PortedTransactionState{
		Sequence:        config.TargetSequence,
		ProtocolVersion: config.TargetProtocolVersion,
		Entries:         portedEntries,
	}, nil
}
