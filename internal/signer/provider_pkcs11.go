// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package signer

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// wellKnownPKCS11Modules lists common PKCS#11 shared library paths per OS.
// Used by DiscoverPKCS11Modules for dynamic module discovery.
var wellKnownPKCS11Modules = map[string][]string{
	"linux": {
		"/usr/lib/softhsm/libsofthsm2.so",
		"/usr/lib/x86_64-linux-gnu/softhsm/libsofthsm2.so",
		"/usr/lib/aarch64-linux-gnu/softhsm/libsofthsm2.so",
		"/usr/local/lib/softhsm/libsofthsm2.so",
		"/usr/lib/opensc-pkcs11.so",
		"/usr/lib/x86_64-linux-gnu/opensc-pkcs11.so",
		"/usr/lib/libeTPkcs11.so",
		"/usr/local/lib/libeTPkcs11.so",
		"/usr/lib/libykcs11.so",
		"/usr/lib/x86_64-linux-gnu/libykcs11.so",
	},
	"darwin": {
		"/usr/local/lib/softhsm/libsofthsm2.so",
		"/opt/homebrew/lib/softhsm/libsofthsm2.so",
		"/opt/homebrew/lib/libsofthsm2.so",
		"/usr/local/lib/libykcs11.dylib",
		"/opt/homebrew/lib/libykcs11.dylib",
		"/Library/OpenSC/lib/opensc-pkcs11.so",
		"/usr/local/lib/opensc-pkcs11.so",
	},
	"windows": {
		`C:\Program Files\SoftHSM2\lib\softhsm2-x64.dll`,
		`C:\Program Files (x86)\SoftHSM2\lib\softhsm2.dll`,
		`C:\Program Files\OpenSC Project\OpenSC\pkcs11\opensc-pkcs11.dll`,
		`C:\Program Files\Yubico\Yubico PIV Tool\bin\libykcs11.dll`,
	},
}

// DiscoverPKCS11Modules returns the paths of PKCS#11 shared libraries that
// exist on the current system. It checks the well-known locations for the
// running OS and returns only those that are present on disk.
func DiscoverPKCS11Modules() []string {
	candidates, ok := wellKnownPKCS11Modules[runtime.GOOS]
	if !ok {
		return nil
	}

	var found []string
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			found = append(found, path)
		}
	}
	return found
}

// PKCS11Provider implements SignerProvider for hardware security module
// signing via the PKCS#11 Cryptoki interface.
//
// Required environment variables:
//   - GLASSBOX_PKCS11_MODULE — path to the PKCS#11 shared library
//   - GLASSBOX_PKCS11_PIN    — user PIN for the token
//
// Optional environment variables:
//   - GLASSBOX_PKCS11_TOKEN_LABEL — select token by label
//   - GLASSBOX_PKCS11_KEY_LABEL   — select key by CKA_LABEL
//   - GLASSBOX_PKCS11_KEY_ID      — select key by CKA_ID (hex)
type PKCS11Provider struct{}

// Name returns "pkcs11".
func (p *PKCS11Provider) Name() string { return "pkcs11" }

// Description returns a short human-readable description.
func (p *PKCS11Provider) Description() string {
	return "HSM signing via PKCS#11 Cryptoki interface (YubiKey, SoftHSM, etc.)"
}

// EnvVars documents the environment variables recognised by this provider.
func (p *PKCS11Provider) EnvVars() []EnvVarDoc {
	return []EnvVarDoc{
		{
			Name:        "GLASSBOX_PKCS11_MODULE",
			Required:    true,
			Description: "Filesystem path to the PKCS#11 shared library (.so/.dylib/.dll)",
		},
		{
			Name:        "GLASSBOX_PKCS11_PIN",
			Required:    true,
			Description: "User PIN for the PKCS#11 token",
		},
		{
			Name:        "GLASSBOX_PKCS11_TOKEN_LABEL",
			Required:    false,
			Description: "Select token by label (optional; uses slot 0 when omitted)",
		},
		{
			Name:        "GLASSBOX_PKCS11_KEY_LABEL",
			Required:    false,
			Description: "Select signing key by CKA_LABEL attribute",
		},
		{
			Name:        "GLASSBOX_PKCS11_KEY_ID",
			Required:    false,
			Description: "Select signing key by CKA_ID attribute (hex-encoded)",
		},
	}
}

// Validate checks that the module path and PIN are present and that the
// module file exists on disk. It also validates KeyIDHex format and the
// SlotIndex range so failures surface before any module is loaded.
func (p *PKCS11Provider) Validate(cfg ProviderConfig) error {
	module := p.resolveModule(cfg)
	if module == "" {
		hint := p.discoveryHint()
		return &Error{
			Op:  "pkcs11",
			Msg: fmt.Sprintf("PKCS#11 module path is required; set --pkcs11-module or GLASSBOX_PKCS11_MODULE%s", hint),
		}
	}

	info, err := os.Stat(module)
	if err != nil {
		return &Error{
			Op:  "pkcs11",
			Msg: fmt.Sprintf("PKCS#11 module not found at %q: %v\n"+
				"  Fix: verify the path is correct for your OS/architecture; run --validate-only for a full preflight report", module, err),
		}
	}
	if info.IsDir() {
		return &Error{
			Op:  "pkcs11",
			Msg: fmt.Sprintf("PKCS#11 module path %q is a directory, not a shared library\n"+
				"  Fix: provide the full path to the .so/.dylib/.dll file, not its parent directory", module),
		}
	}

	if p.resolvePIN(cfg) == "" {
		return &Error{
			Op:  "pkcs11",
			Msg: "PKCS#11 PIN is required; set --pkcs11-pin or GLASSBOX_PKCS11_PIN",
		}
	}

	// Validate KeyIDHex format when provided so a typo is caught before
	// any module is loaded (mirrors the check in validatePKCS11SignInputs).
	keyIDHex := p.resolveKeyIDHex(cfg)
	if keyIDHex != "" {
		if _, hexErr := hex.DecodeString(keyIDHex); hexErr != nil {
			return &Error{
				Op:  "pkcs11",
				Msg: fmt.Sprintf("--pkcs11-key-id (or GLASSBOX_PKCS11_KEY_ID) must be a hex-encoded CKA_ID: %v\n"+
					"  Fix: provide a valid hex string, e.g. 'a1b2c3' (use pkcs11-tool --list-objects to find the CKA_ID)", hexErr),
			}
		}
	}

	// Validate slot index is non-negative.
	if cfg.PKCS11SlotIndex < 0 {
		return &Error{
			Op:  "pkcs11",
			Msg: fmt.Sprintf("PKCS#11 slot index must be >= 0, got %d; set GLASSBOX_PKCS11_SLOT to a valid non-negative integer", cfg.PKCS11SlotIndex),
		}
	}

	return nil
}

// Create builds a Pkcs11Config from cfg and returns a Pkcs11Signer.
func (p *PKCS11Provider) Create(cfg ProviderConfig) (Signer, error) {
	pkCfg := Pkcs11Config{
		ModulePath: p.resolveModule(cfg),
		PIN:        p.resolvePIN(cfg),
		TokenLabel: p.resolveTokenLabel(cfg),
		KeyLabel:   p.resolveKeyLabel(cfg),
		KeyIDHex:   p.resolveKeyIDHex(cfg),
		SlotIndex:  cfg.PKCS11SlotIndex,
	}
	return NewPkcs11Signer(pkCfg)
}

// resolveModule returns the module path from cfg or environment.
func (p *PKCS11Provider) resolveModule(cfg ProviderConfig) string {
	if cfg.PKCS11ModulePath != "" {
		return cfg.PKCS11ModulePath
	}
	return os.Getenv("GLASSBOX_PKCS11_MODULE")
}

// resolvePIN returns the PIN from cfg or environment.
func (p *PKCS11Provider) resolvePIN(cfg ProviderConfig) string {
	if cfg.PKCS11PIN != "" {
		return cfg.PKCS11PIN
	}
	return os.Getenv("GLASSBOX_PKCS11_PIN")
}

// resolveTokenLabel returns the token label from cfg or environment.
func (p *PKCS11Provider) resolveTokenLabel(cfg ProviderConfig) string {
	if cfg.PKCS11TokenLabel != "" {
		return cfg.PKCS11TokenLabel
	}
	return os.Getenv("GLASSBOX_PKCS11_TOKEN_LABEL")
}

// resolveKeyLabel returns the key label from cfg or environment.
func (p *PKCS11Provider) resolveKeyLabel(cfg ProviderConfig) string {
	if cfg.PKCS11KeyLabel != "" {
		return cfg.PKCS11KeyLabel
	}
	return os.Getenv("GLASSBOX_PKCS11_KEY_LABEL")
}

// resolveKeyIDHex returns the key ID hex from cfg or environment.
func (p *PKCS11Provider) resolveKeyIDHex(cfg ProviderConfig) string {
	if cfg.PKCS11KeyIDHex != "" {
		return cfg.PKCS11KeyIDHex
	}
	return os.Getenv("GLASSBOX_PKCS11_KEY_ID")
}

// discoveryHint returns a hint string listing discovered modules on this system,
// so callers can copy-paste the correct path instead of guessing.
func (p *PKCS11Provider) discoveryHint() string {
	discovered := DiscoverPKCS11Modules()
	if len(discovered) == 0 {
		return ""
	}
	var paths []string
	for _, m := range discovered {
		paths = append(paths, filepath.ToSlash(m))
	}
	sort.Slice(paths, func(i, j int) bool {
		return strings.ToLower(paths[i]) < strings.ToLower(paths[j])
	})
	return "; discovered modules available on this system:\n  " + strings.Join(paths, "\n  ")
}
