// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package simulator

import (
	"encoding/base64"
	"errors"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// buildV1Envelope creates a minimal TransactionV1 envelope and returns its
// base64 XDR representation. If ops is nil, a single payment operation is used.
func buildV1Envelope(t *testing.T, ops []xdr.Operation) string {
	t.Helper()
	var src [32]byte
	src[0] = 1
	srcMux, err := xdr.NewMuxedAccount(xdr.CryptoKeyTypeKeyTypeEd25519, xdr.Uint256(src))
	if err != nil {
		t.Fatalf("NewMuxedAccount: %v", err)
	}
	if ops == nil {
		var dst [32]byte
		dst[0] = 2
		dstMux, err := xdr.NewMuxedAccount(xdr.CryptoKeyTypeKeyTypeEd25519, xdr.Uint256(dst))
		if err != nil {
			t.Fatalf("NewMuxedAccount dst: %v", err)
		}
		ops = []xdr.Operation{{
			Body: xdr.OperationBody{
				Type: xdr.OperationTypePayment,
				PaymentOp: &xdr.PaymentOp{
					Destination: xdr.MuxedAccount(dstMux),
					Asset:       xdr.Asset{Type: xdr.AssetTypeAssetTypeNative},
					Amount:      100,
				},
			},
		}}
	}
	tx := xdr.Transaction{
		SourceAccount: xdr.MuxedAccount(srcMux),
		Fee:           xdr.Uint32(100),
		SeqNum:        1,
		Cond:          xdr.Preconditions{Type: xdr.PreconditionTypePrecondNone},
		Memo:          xdr.Memo{Type: xdr.MemoTypeMemoNone},
		Operations:    ops,
		Ext:           xdr.TransactionExt{V: 0},
	}
	env := xdr.TransactionEnvelope{
		Type: xdr.EnvelopeTypeEnvelopeTypeTx,
		V1:   &xdr.TransactionV1Envelope{Tx: tx},
	}
	b, err := env.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// buildV0Envelope creates a minimal TransactionV0 envelope.
func buildV0Envelope(t *testing.T) string {
	t.Helper()
	var src [32]byte
	src[0] = 3
	tx := xdr.TransactionV0{
		SourceAccountEd25519: xdr.Uint256(src),
		Fee:                  xdr.Uint32(200),
		SeqNum:               xdr.SequenceNumber(2),
		Memo:                 xdr.Memo{Type: xdr.MemoTypeMemoNone},
		Operations:           []xdr.Operation{},
	}
	env := xdr.TransactionEnvelope{
		Type: xdr.EnvelopeTypeEnvelopeTypeTxV0,
		V0:   &xdr.TransactionV0Envelope{Tx: tx},
	}
	b, err := env.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary V0: %v", err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// buildFeeBumpEnvelope wraps the given V1 base64 envelope in a FeeBump.
func buildFeeBumpEnvelope(t *testing.T, innerB64 string) string {
	t.Helper()
	innerRaw, err := base64.StdEncoding.DecodeString(innerB64)
	if err != nil {
		t.Fatalf("DecodeString inner: %v", err)
	}
	var inner xdr.TransactionEnvelope
	if err := xdr.SafeUnmarshal(innerRaw, &inner); err != nil {
		t.Fatalf("SafeUnmarshal inner: %v", err)
	}
	if inner.Type != xdr.EnvelopeTypeEnvelopeTypeTx {
		t.Fatalf("inner must be V1 for FeeBump")
	}
	var feeSrc [32]byte
	feeSrc[0] = 9
	fbSrc := xdr.MuxedAccount{
		Type:    xdr.CryptoKeyTypeKeyTypeEd25519,
		Ed25519: (*xdr.Uint256)(&feeSrc),
	}
	v1Inner := inner.MustV1()
	fb := xdr.FeeBumpTransaction{
		FeeSource: fbSrc,
		Fee:       1000,
		InnerTx: xdr.FeeBumpTransactionInnerTx{
			Type: xdr.EnvelopeTypeEnvelopeTypeTx,
			V1:   &v1Inner,
		},
	}
	env := xdr.TransactionEnvelope{
		Type:    xdr.EnvelopeTypeEnvelopeTypeTxFeeBump,
		FeeBump: &xdr.FeeBumpTransactionEnvelope{Tx: fb},
	}
	b, err := env.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary FeeBump: %v", err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// ── DecodeEnvelopeXDR ─────────────────────────────────────────────────────────

func TestDecodeEnvelopeXDR_V1(t *testing.T) {
	b64 := buildV1Envelope(t, nil)
	decoded, err := DecodeEnvelopeXDR(b64)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decoded.Variant != VariantV1 {
		t.Errorf("Variant = %q, want %q", decoded.Variant, VariantV1)
	}
	if decoded.SourceAccount == "" {
		t.Error("SourceAccount must not be empty")
	}
	if decoded.Fee != 100 {
		t.Errorf("Fee = %d, want 100", decoded.Fee)
	}
	if len(decoded.Operations) != 1 {
		t.Errorf("Operations = %d, want 1", len(decoded.Operations))
	}
}

func TestDecodeEnvelopeXDR_V0(t *testing.T) {
	b64 := buildV0Envelope(t)
	decoded, err := DecodeEnvelopeXDR(b64)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decoded.Variant != VariantV0 {
		t.Errorf("Variant = %q, want %q", decoded.Variant, VariantV0)
	}
	if decoded.Fee != 200 {
		t.Errorf("Fee = %d, want 200", decoded.Fee)
	}
}

func TestDecodeEnvelopeXDR_FeeBump(t *testing.T) {
	innerB64 := buildV1Envelope(t, nil)
	b64 := buildFeeBumpEnvelope(t, innerB64)
	decoded, err := DecodeEnvelopeXDR(b64)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decoded.Variant != VariantFeeBump {
		t.Errorf("Variant = %q, want %q", decoded.Variant, VariantFeeBump)
	}
	if decoded.InnerEnvelope == nil {
		t.Fatal("InnerEnvelope must not be nil for FeeBump")
	}
	if decoded.InnerEnvelope.Variant != VariantV1 {
		t.Errorf("InnerEnvelope.Variant = %q, want %q", decoded.InnerEnvelope.Variant, VariantV1)
	}
}

// ── Round-trip: auth entries ───────────────────────────────────────────────────

func TestDecodeEnvelopeXDR_AuthEntries(t *testing.T) {
	// Build a V1 envelope with an InvokeHostFunctionOp that contains one auth
	// entry using source-account credentials (the simplest form — no extra fields).
	contractID := xdr.Hash{0x01, 0x02}
	auth := xdr.SorobanAuthorizationEntry{
		Credentials: xdr.SorobanCredentials{
			Type: xdr.SorobanCredentialsTypeSorobanCredentialsSourceAccount,
		},
		RootInvocation: xdr.SorobanAuthorizedInvocation{
			Function: xdr.SorobanAuthorizedFunction{
				Type: xdr.SorobanAuthorizedFunctionTypeSorobanAuthorizedFunctionTypeContractFn,
				ContractFn: &xdr.InvokeContractArgs{
					ContractAddress: xdr.ScAddress{
						Type:       xdr.ScAddressTypeScAddressTypeContract,
						ContractId: (*xdr.ContractId)(&contractID),
					},
					FunctionName: "transfer",
				},
			},
		},
	}
	ihfOp := xdr.Operation{
		Body: xdr.OperationBody{
			Type: xdr.OperationTypeInvokeHostFunction,
			InvokeHostFunctionOp: &xdr.InvokeHostFunctionOp{
				HostFunction: xdr.HostFunction{
					Type: xdr.HostFunctionTypeHostFunctionTypeInvokeContract,
					InvokeContract: &xdr.InvokeContractArgs{
						ContractAddress: xdr.ScAddress{
							Type:       xdr.ScAddressTypeScAddressTypeContract,
							ContractId: (*xdr.ContractId)(&contractID),
						},
						FunctionName: "transfer",
					},
				},
				Auth: []xdr.SorobanAuthorizationEntry{auth},
			},
		},
	}

	b64 := buildV1Envelope(t, []xdr.Operation{ihfOp})
	decoded, err := DecodeEnvelopeXDR(b64)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(decoded.AuthEntries) != 1 {
		t.Fatalf("AuthEntries len = %d, want 1", len(decoded.AuthEntries))
	}
	// Each auth entry must be valid base64.
	if _, decErr := base64.StdEncoding.DecodeString(decoded.AuthEntries[0]); decErr != nil {
		t.Errorf("AuthEntries[0] is not valid base64: %v", decErr)
	}
}

// ── Footprint extraction ──────────────────────────────────────────────────────

func TestDecodeEnvelopeXDR_FootprintExtracted(t *testing.T) {
	var src [32]byte
	src[0] = 1
	srcMux, _ := xdr.NewMuxedAccount(xdr.CryptoKeyTypeKeyTypeEd25519, xdr.Uint256(src))

	contractIDBytes := xdr.Hash{0xAA}
	roKey := xdr.LedgerKey{
		Type: xdr.LedgerEntryTypeContractData,
		ContractData: &xdr.LedgerKeyContractData{
			Contract: xdr.ScAddress{
				Type:       xdr.ScAddressTypeScAddressTypeContract,
				ContractId: (*xdr.ContractId)(&contractIDBytes),
			},
			Key:        xdr.ScVal{Type: xdr.ScValTypeScvBool},
			Durability: xdr.ContractDataDurabilityPersistent,
		},
	}
	fp := xdr.LedgerFootprint{
		ReadOnly:  []xdr.LedgerKey{roKey},
		ReadWrite: []xdr.LedgerKey{},
	}
	sorobanData := &xdr.SorobanTransactionData{
		Resources: xdr.SorobanResources{
			Footprint: fp,
		},
	}
	tx := xdr.Transaction{
		SourceAccount: xdr.MuxedAccount(srcMux),
		Fee:           100,
		SeqNum:        1,
		Cond:          xdr.Preconditions{Type: xdr.PreconditionTypePrecondNone},
		Memo:          xdr.Memo{Type: xdr.MemoTypeMemoNone},
		Operations:    []xdr.Operation{},
		Ext: xdr.TransactionExt{
			V:           1,
			SorobanData: sorobanData,
		},
	}
	env := xdr.TransactionEnvelope{
		Type: xdr.EnvelopeTypeEnvelopeTypeTx,
		V1:   &xdr.TransactionV1Envelope{Tx: tx},
	}
	b, err := env.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}
	b64 := base64.StdEncoding.EncodeToString(b)

	decoded, err := DecodeEnvelopeXDR(b64)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decoded.Footprint == nil {
		t.Fatal("Footprint must be non-nil for transaction with Soroban ext")
	}
	if len(decoded.Footprint.ReadOnly) != 1 {
		t.Errorf("ReadOnly len = %d, want 1", len(decoded.Footprint.ReadOnly))
	}
	if len(decoded.Footprint.ReadWrite) != 0 {
		t.Errorf("ReadWrite len = %d, want 0", len(decoded.Footprint.ReadWrite))
	}
}

func TestDecodeEnvelopeXDR_NoFootprintForNonSoroban(t *testing.T) {
	b64 := buildV1Envelope(t, nil)
	decoded, err := DecodeEnvelopeXDR(b64)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decoded.Footprint != nil {
		t.Error("Footprint should be nil for non-Soroban V1 transaction")
	}
}

// ── Error cases ───────────────────────────────────────────────────────────────

func TestDecodeEnvelopeXDR_InvalidBase64(t *testing.T) {
	_, err := DecodeEnvelopeXDR("not!!base64")
	if err == nil {
		t.Fatal("expected error for invalid base64")
	}
}

func TestDecodeEnvelopeXDR_MalformedXDR(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString([]byte("garbage xdr data"))
	_, err := DecodeEnvelopeXDR(b64)
	if err == nil {
		t.Fatal("expected error for malformed XDR")
	}
}

func TestDecodeEnvelopeXDR_UnsupportedVariant_ReturnsStableError(t *testing.T) {
	// Construct a minimal envelope with an unsupported type by manipulating bytes.
	// Since we cannot easily construct a real unsupported variant, we verify the
	// error path via an envelope that decodes but has an unrecognised type field
	// by simulating ValidateEnvelopeXDR on a zero envelope (type 0 = V0 or TX).
	// Instead, test via a wrapper that returns ErrUnsupportedVariant when the inner
	// type is not V1.
	innerEnv := xdr.FeeBumpTransactionInnerTx{
		// Type 0 is EnvelopeTypeEnvelopeTypeTxV0 which is not valid as FeeBump inner.
		Type: xdr.EnvelopeTypeEnvelopeTypeTxV0,
	}
	_, err := decodeFeeBumpInner(innerEnv)
	if err == nil {
		t.Fatal("expected ErrUnsupportedVariant for V0 inner tx")
	}
	if !errors.Is(err, ErrUnsupportedVariant) {
		t.Errorf("expected ErrUnsupportedVariant, got %T: %v", err, err)
	}
}

// ── ValidateEnvelopeXDR ───────────────────────────────────────────────────────

func TestValidateEnvelopeXDR_Valid(t *testing.T) {
	b64 := buildV1Envelope(t, nil)
	if err := ValidateEnvelopeXDR(b64); err != nil {
		t.Errorf("unexpected error for valid V1 envelope: %v", err)
	}
}

func TestValidateEnvelopeXDR_Invalid(t *testing.T) {
	if err := ValidateEnvelopeXDR("not-valid"); err == nil {
		t.Error("expected error for invalid envelope")
	}
}

// ── DecodeFootprintXDR ────────────────────────────────────────────────────────

func TestDecodeFootprintXDR(t *testing.T) {
	roKey := xdr.LedgerKey{
		Type: xdr.LedgerEntryTypeAccount,
		Account: &xdr.LedgerKeyAccount{
			AccountId: xdr.AccountId{
				Type:    xdr.PublicKeyTypePublicKeyTypeEd25519,
				Ed25519: &xdr.Uint256{0x01},
			},
		},
	}
	rwKey := xdr.LedgerKey{
		Type: xdr.LedgerEntryTypeAccount,
		Account: &xdr.LedgerKeyAccount{
			AccountId: xdr.AccountId{
				Type:    xdr.PublicKeyTypePublicKeyTypeEd25519,
				Ed25519: &xdr.Uint256{0x02},
			},
		},
	}
	fp := xdr.LedgerFootprint{
		ReadOnly:  []xdr.LedgerKey{roKey},
		ReadWrite: []xdr.LedgerKey{rwKey},
	}
	b, err := fp.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}
	b64 := base64.StdEncoding.EncodeToString(b)

	decoded, err := DecodeFootprintXDR(b64)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(decoded.ReadOnly) != 1 {
		t.Errorf("ReadOnly = %d, want 1", len(decoded.ReadOnly))
	}
	if len(decoded.ReadWrite) != 1 {
		t.Errorf("ReadWrite = %d, want 1", len(decoded.ReadWrite))
	}
}

func TestDecodeFootprintXDR_InvalidBase64(t *testing.T) {
	_, err := DecodeFootprintXDR("!!")
	if err == nil {
		t.Fatal("expected error for invalid base64")
	}
}

// ── Empty input ───────────────────────────────────────────────────────────────

// TestXDRDecoder_EmptyInput asserts that passing an empty byte slice (encoded
// as an empty base64 string) to the XDR decoder returns a descriptive,
// non-nil error rather than panicking or returning a nil result silently.
// Empty envelopes arise from truncated RPC responses and empty network buffers.
func TestXDRDecoder_EmptyInput(t *testing.T) {
	result, err := DecodeEnvelopeXDR("")
	if err == nil {
		t.Fatal("expected a non-nil error for empty input, got nil")
	}
	if result != nil {
		t.Errorf("expected nil result for empty input, got %+v", result)
	}
}
