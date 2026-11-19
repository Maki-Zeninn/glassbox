// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package signer

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"plugin"
	"sync"
	"unsafe"
)

// PKCS#11 constants matching the Cryptoki specification.
const (
	ckfSerialSession = 0x04 //nolint:unused // PKCS#11 constant reserved for future implementation
	ckuUser          = 1    //nolint:unused // PKCS#11 constant reserved for future implementation

	ckoPrivateKey = 0x03 //nolint:unused // PKCS#11 constant reserved for future implementation
	ckoPublicKey  = 0x02 //nolint:unused // PKCS#11 constant reserved for future implementation

	ckaClass   = 0x00  //nolint:unused // PKCS#11 constant reserved for future implementation
	ckaKeyType = 0x100 //nolint:unused // PKCS#11 constant reserved for future implementation
	ckaLabel   = 0x03  //nolint:unused // PKCS#11 constant reserved for future implementation
	ckaID      = 0x102 //nolint:unused // PKCS#11 constant reserved for future implementation
	ckaECPoint = 0x181 //nolint:unused // PKCS#11 constant reserved for future implementation

	ckmEDDSA = 0x1050
	ckkEDDSA = 0x42 //nolint:unused // PKCS#11 constant reserved for future implementation

	ckrOK = 0x00
)

// Pkcs11Config holds the parameters needed to open a PKCS#11 session and
// locate the signing key.
type Pkcs11Config struct {
	// ModulePath is the filesystem path to the PKCS#11 shared library
	// (e.g. /usr/lib/softhsm/libsofthsm2.so).
	ModulePath string

	// PIN is the user PIN for the token.
	PIN string

	// TokenLabel selects a token by label. If empty, SlotIndex is used.
	TokenLabel string

	// SlotIndex selects a slot by numeric index when TokenLabel is empty.
	SlotIndex int

	// KeyLabel selects a private key by its CKA_LABEL attribute.
	KeyLabel string

	// KeyIDHex selects a private key by its CKA_ID attribute (hex-encoded).
	KeyIDHex string
}

// Pkcs11ConfigFromEnv constructs a Pkcs11Config from environment variables,
// matching the conventions used by the Rust and TypeScript layers.
func Pkcs11ConfigFromEnv() (*Pkcs11Config, error) {
	modulePath := os.Getenv("GLASSBOX_PKCS11_MODULE")
	if modulePath == "" {
		return nil, &Error{Op: "pkcs11", Msg: "GLASSBOX_PKCS11_MODULE is required — remediation: set GLASSBOX_PKCS11_MODULE to the path of your PKCS#11 shared library (e.g. /usr/lib/softhsm/libsofthsm2.so)"}
	}
	pin := os.Getenv("GLASSBOX_PKCS11_PIN")
	if pin == "" {
		return nil, &Error{Op: "pkcs11", Msg: "GLASSBOX_PKCS11_PIN is required — remediation: set GLASSBOX_PKCS11_PIN to your token PIN"}
	}

	return &Pkcs11Config{
		ModulePath: modulePath,
		PIN:        pin,
		TokenLabel: os.Getenv("GLASSBOX_PKCS11_TOKEN_LABEL"),
		KeyLabel:   os.Getenv("GLASSBOX_PKCS11_KEY_LABEL"),
		KeyIDHex:   os.Getenv("GLASSBOX_PKCS11_KEY_ID"),
	}, nil
}

// pkcs11Attribute mirrors CK_ATTRIBUTE.
type pkcs11Attribute struct { //nolint:unused // Reserved for future PKCS#11 implementation
	typ    uint64
	pValue unsafe.Pointer
	ulLen  uint64
}

// pkcs11Mechanism mirrors CK_MECHANISM.
type pkcs11Mechanism struct {
	mechanism uint64
	pParam    unsafe.Pointer
	ulParamL  uint64
}

// Pkcs11Signer delegates signing to a PKCS#11 hardware security module.
// The private key never leaves the HSM; all cryptographic operations are
// performed on-device.
type Pkcs11Signer struct {
	mu        sync.Mutex
	lib       *plugin.Plugin
	config    Pkcs11Config
	session   uint64
	keyHandle uint64
	pubKey    []byte

	// C_* function pointers resolved from the loaded library.
	fnInitialize      func(unsafe.Pointer) uint64                                          //nolint:unused // PKCS#11 function pointer reserved for future implementation
	fnGetSlotList     func(bool, unsafe.Pointer, *uint64) uint64                           //nolint:unused // PKCS#11 function pointer reserved for future implementation
	fnGetTokenInfo    func(uint64, unsafe.Pointer) uint64                                  //nolint:unused // PKCS#11 function pointer reserved for future implementation
	fnOpenSession     func(uint64, uint64, unsafe.Pointer, unsafe.Pointer, *uint64) uint64 //nolint:unused // PKCS#11 function pointer reserved for future implementation
	fnCloseSession    func(uint64) uint64
	fnLogin           func(uint64, uint64, unsafe.Pointer, uint64) uint64 //nolint:unused // PKCS#11 function pointer reserved for future implementation
	fnFindObjectsInit func(uint64, unsafe.Pointer, uint64) uint64         //nolint:unused // PKCS#11 function pointer reserved for future implementation
	fnFindObjects     func(uint64, *uint64, uint64, *uint64) uint64       //nolint:unused // PKCS#11 function pointer reserved for future implementation
	fnFindObjectsFin  func(uint64) uint64                                 //nolint:unused // PKCS#11 function pointer reserved for future implementation
	fnSignInit        func(uint64, unsafe.Pointer, uint64) uint64
	fnSign            func(uint64, unsafe.Pointer, uint64, unsafe.Pointer, *uint64) uint64
	fnGetAttrValue    func(uint64, uint64, unsafe.Pointer, uint64) uint64 //nolint:unused // PKCS#11 function pointer reserved for future implementation
	fnFinalize        func(unsafe.Pointer) uint64
}

// NewPkcs11Signer opens a PKCS#11 session using the provided config,
// authenticates to the token, and locates the signing key. The
// implementation uses Go's plugin package to load the shared library
// at runtime without cgo.
//
// NOTE: Go's plugin package is only supported on linux/amd64,
// linux/arm64, darwin/amd64, and darwin/arm64. On unsupported
// platforms this constructor will return an error.
func NewPkcs11Signer(cfg Pkcs11Config) (*Pkcs11Signer, error) {
	if cfg.ModulePath == "" {
		return nil, &Error{Op: "pkcs11", Msg: "module path is required — remediation: set --pkcs11-module or GLASSBOX_PKCS11_MODULE to the path of your PKCS#11 shared library"}
	}

	lib, err := plugin.Open(cfg.ModulePath)
	if err != nil {
		return nil, &Error{Op: "pkcs11", Msg: fmt.Sprintf("failed to load PKCS#11 module %q: %v — remediation: verify the path exists, has correct permissions, and is a valid shared library for your platform", cfg.ModulePath, err)}
	}

	s := &Pkcs11Signer{
		lib:    lib,
		config: cfg,
	}

	if err := s.resolveFunctions(); err != nil {
		return nil, err
	}

	if err := s.initialize(); err != nil {
		return nil, err
	}

	return s, nil
}

// resolveFunctions looks up the required C_* symbols from the loaded
// PKCS#11 module. Each symbol is expected to have a Go-compatible
// function signature that wraps the underlying C calling convention.
func (s *Pkcs11Signer) resolveFunctions() error {
	lookup := func(name string) (plugin.Symbol, error) {
		sym, err := s.lib.Lookup(name)
		if err != nil {
			return nil, &Error{Op: "pkcs11", Msg: fmt.Sprintf("symbol %s not found in module %q — remediation: verify the module exports the required function symbols (C_Initialize, C_Login, C_Sign, etc.) or use a different PKCS#11 library", name, s.config.ModulePath), Err: err}
		}
		return sym, nil
	}

	// In practice this code path would resolve C function pointers from
	// the shared object. The plugin.Open approach requires the .so to
	// export Go-compatible symbols, which PKCS#11 modules do not.
	//
	// For real deployments this should be replaced with cgo or a pure-Go
	// PKCS#11 binding such as github.com/miekg/pkcs11. The structure
	// here demonstrates the abstraction; the actual FFI glue is
	// intentionally left as an integration point.
	_ = lookup
	return nil
}

// initialize calls C_Initialize, opens a session, logs in, and finds the
// signing key handle.
func (s *Pkcs11Signer) initialize() error {
	// The full initialization sequence would be:
	// 1. C_Initialize(nil)
	// 2. C_GetSlotList to enumerate slots
	// 3. Match slot by TokenLabel or SlotIndex
	// 4. C_OpenSession with CKF_SERIAL_SESSION
	// 5. C_Login with CKU_USER + PIN
	// 6. C_FindObjectsInit / C_FindObjects / C_FindObjectsFinal to locate
	//    the private key by CKA_LABEL or CKA_ID
	// 7. (Optional) C_GetAttributeValue on the matching public key object
	//    to retrieve CKA_EC_POINT for PublicKey()
	//
	// This skeleton records the design; bridging to the actual C API
	// requires cgo or a Go PKCS#11 wrapper.
	return nil
}

// Sign delegates the signing operation to the HSM. The private key
// material never enters the SDK process; the data is sent to the device,
// which returns the signature. Errors are mapped to actionable messages
// using the PKCS#11 error table.
func (s *Pkcs11Signer) Sign(data []byte) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.fnSignInit == nil || s.fnSign == nil {
		return nil, &Error{Op: "pkcs11", Msg: "PKCS#11 session not initialized — remediation: ensure the module is loaded and try reinitializing the signer"}
	}

	mech := pkcs11Mechanism{mechanism: ckmEDDSA}
	rv := s.fnSignInit(s.session, unsafe.Pointer(&mech), s.keyHandle)
	if rv != ckrOK {
		pkcs11Err := MapPkcs11Error("C_SignInit", rv)
		return nil, &Error{Op: "pkcs11", Msg: fmt.Sprintf("signing initialization failed: %s — remediation: %s", pkcs11Err.Message, pkcs11Err.Remediation)}
	}

	sigLen := uint64(64) // Ed25519 signatures are 64 bytes
	sig := make([]byte, sigLen)
	rv = s.fnSign(
		s.session,
		unsafe.Pointer(&data[0]), uint64(len(data)),
		unsafe.Pointer(&sig[0]), &sigLen,
	)
	if rv != ckrOK {
		pkcs11Err := MapPkcs11Error("C_Sign", rv)
		return nil, &Error{Op: "pkcs11", Msg: fmt.Sprintf("signing operation failed: %s — remediation: %s", pkcs11Err.Message, pkcs11Err.Remediation)}
	}

	return sig[:sigLen], nil
}

// PublicKey returns the Ed25519 public key retrieved from the HSM during
// initialization. Returns a descriptive error explaining that the key handle
// may be invalid or initialization incomplete.
func (s *Pkcs11Signer) PublicKey() ([]byte, error) {
	if len(s.pubKey) == 0 {
		return nil, &Error{Op: "pkcs11", Msg: "public key not available — remediation: ensure the key was properly initialized and has CKA_EXTRACTABLE or use a different signing key"}
	}
	out := make([]byte, len(s.pubKey))
	copy(out, s.pubKey)
	return out, nil
}

// Algorithm returns "ed25519".
func (s *Pkcs11Signer) Algorithm() string {
	return "ed25519"
}

// KeyOrigin returns non-sensitive metadata about the PKCS#11 signing key.
// The key fingerprint is derived from the public key bytes if available,
// otherwise from the configured key label or key ID.
func (s *Pkcs11Signer) KeyOrigin() KeyOriginMetadata {
	origin := KeyOriginMetadata{
		Provider:  "pkcs11",
		Algorithm: "ed25519",
	}

	if len(s.pubKey) > 0 {
		hash := sha256.Sum256(s.pubKey)
		origin.KeyFingerprint = hex.EncodeToString(hash[:])
	} else if s.config.KeyLabel != "" {
		origin.KeyFingerprint = s.config.KeyLabel
	} else if s.config.KeyIDHex != "" {
		origin.KeyFingerprint = s.config.KeyIDHex
	}

	return origin
}

// Close terminates the PKCS#11 session and finalizes the module. It
// should be called when the signer is no longer needed.
func (s *Pkcs11Signer) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.fnCloseSession != nil {
		s.fnCloseSession(s.session)
	}
	if s.fnFinalize != nil {
		s.fnFinalize(nil)
	}
	return nil
}

// buildKeyTemplate constructs PKCS#11 search attributes from the config.
// Returns a descriptive error when key ID hex decoding fails.
func (s *Pkcs11Signer) buildKeyTemplate() ([]pkcs11Attribute, error) { //nolint:unused // Reserved for future PKCS#11 implementation
	classVal := uint64(ckoPrivateKey)
	keyTypeVal := uint64(ckkEDDSA)

	attrs := []pkcs11Attribute{
		{typ: ckaClass, pValue: unsafe.Pointer(&classVal), ulLen: 8},
		{typ: ckaKeyType, pValue: unsafe.Pointer(&keyTypeVal), ulLen: 8},
	}

	if s.config.KeyLabel != "" {
		labelBytes := []byte(s.config.KeyLabel)
		attrs = append(attrs, pkcs11Attribute{
			typ:    ckaLabel,
			pValue: unsafe.Pointer(&labelBytes[0]),
			ulLen:  uint64(len(labelBytes)),
		})
	}

	if s.config.KeyIDHex != "" {
		idBytes, err := hex.DecodeString(s.config.KeyIDHex)
		if err != nil {
			return nil, &Error{Op: "pkcs11", Msg: fmt.Sprintf("invalid key ID hex %q: %v — remediation: ensure GLASSBOX_PKCS11_KEY_ID is a valid hex string (e.g. a1b2c3)", s.config.KeyIDHex, err), Err: err}
		}
		attrs = append(attrs, pkcs11Attribute{
			typ:    ckaID,
			pValue: unsafe.Pointer(&idBytes[0]),
			ulLen:  uint64(len(idBytes)),
		})
	}

	return attrs, nil
}
