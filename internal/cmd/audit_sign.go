// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/dotandev/glassbox/internal/errors"
	"github.com/dotandev/glassbox/internal/plan"
	"github.com/dotandev/glassbox/internal/signer"
	"github.com/spf13/cobra"
)

// auditSign flag variables — package-level so tests can set them directly.
var (
	auditSignPayload      string
	auditSignPayloadFile  string
	auditSignValidateOnly bool

	// auditSignJSONFlag wraps the signed audit log output in a schema-versioned
	// JSON envelope (currently a no-op pass-through; reserved for future envelope).
	auditSignJSONFlag bool

	// auditSignPlanFlag prints the execution plan without performing any signing.
	auditSignPlanFlag bool

	// auditSignSoftwareKey accepts a PKCS#8 PEM Ed25519 private key (literal
	// PEM text or a file path). Equivalent to GLASSBOX_AUDIT_PRIVATE_KEY_PEM.
	auditSignSoftwareKey string

	// auditSignHSMProvider is kept for backward compatibility. When set to
	// "pkcs11" it is equivalent to --signing-provider pkcs11.
	// Deprecated: prefer --signing-provider.
	auditSignHSMProvider string

	// auditSignProvider selects the signing backend by name.
	// Supported values: "software" (default), "pkcs11".
	// Additional providers can be registered via signer.DefaultRegistry.
	auditSignProvider string

	// PKCS#11 flag overrides — these take precedence over the corresponding
	// GLASSBOX_PKCS11_* environment variables.
	auditSignPKCS11Module     string
	auditSignPKCS11PIN        string
	auditSignPKCS11TokenLabel string
	auditSignPKCS11KeyLabel   string
	auditSignPKCS11KeyIDHex   string

	// Provenance flags — optional metadata attached to the signed audit log.
	auditSignSignerIdentity        string
	auditSignKeyID                 string
	auditSignCertChainFile         string
	auditSignPreviousSignatureHash string
)

// SignatureProvenance carries metadata about the signing key and identity,
// enabling independent verification of who signed an audit log and with what key.
type SignatureProvenance struct {
	// SignerIdentity is a human-readable identifier for the signing entity
	// (e.g. "ops-team@example.com", "ci-pipeline", "hsm-slot-0").
	SignerIdentity string `json:"signer_identity,omitempty"`

	// KeyID is an opaque identifier for the specific key used to sign
	// (e.g. PKCS#11 CKA_LABEL, KMS key ARN, or a fingerprint).
	KeyID string `json:"key_id,omitempty"`

	// Algorithm describes the signing algorithm (e.g. "Ed25519", "ECDSA-P256").
	Algorithm string `json:"algorithm,omitempty"`

	// CertificateChain holds PEM-encoded certificates in order from leaf to
	// root, enabling chain-of-trust verification when an HSM or PKI is used.
	// May be empty for software/bare-key signing.
	CertificateChain []string `json:"certificate_chain,omitempty"`

	// PreviousSignatureHash is the hex-encoded SHA-256 of the immediately
	// preceding signed audit log, forming a tamper-evident verification chain.
	// Empty for the first entry in a chain.
	PreviousSignatureHash string `json:"previous_signature_hash,omitempty"`
}

// SignedAuditLog is the JSON output produced by audit:sign.
type SignedAuditLog struct {
	Version    string                    `json:"version"`
	Timestamp  time.Time                 `json:"timestamp"`
	TraceHash  string                    `json:"trace_hash"`
	Signature  string                    `json:"signature"`
	PublicKey  string                    `json:"public_key"`
	Provider   string                    `json:"provider"`
	KeyOrigin  *signer.KeyOriginMetadata `json:"key_origin,omitempty"`
	Provenance *SignatureProvenance       `json:"provenance,omitempty"`
	Payload    json.RawMessage           `json:"payload"`
	// SigningAttempts records every KMS API call made during the retry loop.
	// Present only when the KMS provider was used. Empty for idempotency hits
	// and for non-KMS providers.  Included in the canonical hash so that
	// post-signing tampering with attempt records invalidates the signature.
	SigningAttempts []signer.SigningAttempt `json:"signing_attempts,omitempty"`
}

var auditSignCmd = &cobra.Command{
	Use:     "audit:sign",
	GroupID: "utility",
	Short:   "Generate a deterministic signed audit log from a JSON payload",
	Long: `Generate a deterministic signed audit log from a JSON payload.

The payload can be supplied as a string via --payload, as a file via
--payload-file, or piped on stdin.

SIGNING PROVIDERS
  software  (default) — Ed25519 in-process signing using a PEM or hex key.
            Required: --software-private-key or GLASSBOX_AUDIT_PRIVATE_KEY_PEM
                      or GLASSBOX_SOFTWARE_PRIVATE_KEY_HEX

  pkcs11    — Hardware security module signing via PKCS#11 Cryptoki.
            Required: --pkcs11-module / GLASSBOX_PKCS11_MODULE
                      --pkcs11-pin    / GLASSBOX_PKCS11_PIN
            Optional: --pkcs11-token-label / GLASSBOX_PKCS11_TOKEN_LABEL
                      --pkcs11-key-label   / GLASSBOX_PKCS11_KEY_LABEL
                      --pkcs11-key-id      / GLASSBOX_PKCS11_KEY_ID
                      GLASSBOX_PKCS11_SLOT (non-negative integer, default 0)
            Flags take precedence over the matching environment variable. The
            required inputs are validated up front; use --validate-only to run a
            full preflight (module load, slot, PIN, key, test-sign) without
            signing.

PROVIDER SELECTION ORDER
  1. --signing-provider flag
  2. --hsm-provider flag (deprecated alias; "pkcs11" maps to the pkcs11 provider)
  3. GLASSBOX_SIGNING_PROVIDER environment variable
  4. GLASSBOX_SIGNER_TYPE environment variable (legacy)
  5. "software" (default)

EXAMPLES
  # Software signing with an inline PEM key
  glassbox audit:sign \
    --payload '{"input":{},"state":{},"events":[],"timestamp":"2026-01-01T00:00:00.000Z"}' \
    --software-private-key "$(cat ./ed25519-private-key.pem)"

  # Software signing with a key file
  glassbox audit:sign --payload-file payload.json \
    --software-private-key ./ed25519-private-key.pem

  # PKCS#11 HSM signing
  glassbox audit:sign --payload-file payload.json \
    --signing-provider pkcs11 \
    --pkcs11-module /usr/lib/softhsm/libsofthsm2.so \
    --pkcs11-pin 1234

  # Validate PKCS#11 configuration without signing (preflight)
  glassbox audit:sign --validate-only --signing-provider pkcs11 \
    --pkcs11-module /usr/lib/softhsm/libsofthsm2.so --pkcs11-pin 1234`,
	Args:    cobra.NoArgs,
	PreRunE: auditSignPreRunE,
	RunE:    runAuditSign,
}

// auditSignPreRunE validates inputs before signing. In addition to the shared
// argument checks, when the effective provider is pkcs11 (and we are not running
// a preflight-only pass) it verifies the required PKCS#11 inputs are present so
// failures surface as clear messages instead of low-level errors deep in the
// signing path.
//
// Provenance flags are also validated here so problems with --previous-signature-hash
// format or a missing --cert-chain file are caught before any signing work begins.
func auditSignPreRunE(cmd *cobra.Command, _ []string) error {
	if err := validateAuditSignArgs(auditSignPayload, auditSignPayloadFile, auditSignProvider); err != nil {
		return err
	}

	// Validate provenance flags up-front so the user gets actionable errors
	// before any signing work happens.
	if err := validateAuditSignProvenanceFlags(auditSignPreviousSignatureHash, auditSignCertChainFile); err != nil {
		return err
	}

	name, _ := resolveProviderAndConfig()
	if strings.EqualFold(name, "pkcs11") {
		cfg, err := effectivePKCS11Config()
		if err != nil {
			return err
		}
		if !auditSignValidateOnly {
			return validatePKCS11SignInputs(cfg)
		}
	}
	return nil
}

func init() {
	// Payload source flags
	auditSignCmd.Flags().StringVar(&auditSignPayload, "payload", "", "JSON payload to sign")
	auditSignCmd.Flags().StringVar(&auditSignPayloadFile, "payload-file", "", "Path to JSON payload file")

	// Provider selection
	auditSignCmd.Flags().StringVar(&auditSignProvider, "signing-provider", "",
		fmt.Sprintf("Signing provider to use (%s)", strings.Join(signer.DefaultRegistry.Names(), ", ")))
	auditSignCmd.Flags().StringVar(&auditSignHSMProvider, "hsm-provider", "",
		"Deprecated: use --signing-provider instead. HSM provider (pkcs11)")
	_ = auditSignCmd.Flags().MarkDeprecated("hsm-provider", "use --signing-provider instead")

	// Software provider flags
	auditSignCmd.Flags().StringVar(&auditSignSoftwareKey, "software-private-key", "",
		"PKCS#8 PEM Ed25519 private key for software signing (literal PEM or file path)")

	// PKCS#11 provider flags
	auditSignCmd.Flags().StringVar(&auditSignPKCS11Module, "pkcs11-module", "",
		"Path to PKCS#11 shared library (overrides GLASSBOX_PKCS11_MODULE)")
	auditSignCmd.Flags().StringVar(&auditSignPKCS11PIN, "pkcs11-pin", "",
		"PKCS#11 user PIN (overrides GLASSBOX_PKCS11_PIN)")
	auditSignCmd.Flags().StringVar(&auditSignPKCS11TokenLabel, "pkcs11-token-label", "",
		"PKCS#11 token label (overrides GLASSBOX_PKCS11_TOKEN_LABEL)")
	auditSignCmd.Flags().StringVar(&auditSignPKCS11KeyLabel, "pkcs11-key-label", "",
		"PKCS#11 key CKA_LABEL (overrides GLASSBOX_PKCS11_KEY_LABEL)")
	auditSignCmd.Flags().StringVar(&auditSignPKCS11KeyIDHex, "pkcs11-key-id", "",
		"PKCS#11 key CKA_ID in hex (overrides GLASSBOX_PKCS11_KEY_ID)")
	auditSignCmd.Flags().BoolVar(&auditSignValidateOnly, "validate-only", false,
		"Run PKCS#11 preflight checks and exit without signing")
	auditSignCmd.Flags().BoolVar(&auditSignJSONFlag, "json", false,
		"Wrap signed audit log output in a schema-versioned JSON envelope")
	auditSignCmd.Flags().BoolVar(&auditSignPlanFlag, "plan", false,
		"Print the execution plan (files, signing provider, outputs) without performing any signing")

	// Provenance flags
	auditSignCmd.Flags().StringVar(&auditSignSignerIdentity, "signer-identity", "",
		"Human-readable signer identity (e.g. email, team name) stored in provenance metadata")
	auditSignCmd.Flags().StringVar(&auditSignKeyID, "key-id", "",
		"Opaque key identifier stored in provenance metadata (e.g. PKCS#11 label, KMS ARN)")
	auditSignCmd.Flags().StringVar(&auditSignCertChainFile, "cert-chain", "",
		"Path to a PEM file containing the certificate chain (leaf first) for provenance")
	auditSignCmd.Flags().StringVar(&auditSignPreviousSignatureHash, "previous-signature-hash", "",
		"Hex-encoded SHA-256 of the previous signed audit log to form a verification chain")

	rootCmd.AddCommand(auditSignCmd)
}

func runAuditSign(cmd *cobra.Command, args []string) error {
	// --validate-only: run PKCS#11 preflight checks and exit without signing.
	if auditSignValidateOnly {
		return runPkcs11Preflight(cmd)
	}

	// --plan: print execution plan without signing.
	if auditSignPlanFlag {
		providerName, cfg := resolveProviderAndConfig()
		keyID := auditSignKeyID
		if keyID == "" {
			// Use a provider-appropriate placeholder.
			switch providerName {
			case "pkcs11":
				keyID = firstNonEmpty(cfg.PKCS11KeyLabel, cfg.PKCS11KeyIDHex, "pkcs11-key")
			default:
				keyID = "(software key)"
			}
		}
		execPlan := plan.BuildAuditPlan(plan.AuditPlanOptions{
			PayloadFile:   auditSignPayloadFile,
			ProviderName:  providerName,
			KeyIdentifier: keyID,
			Algorithm:     "ed25519",
			CertChainFile: auditSignCertChainFile,
		})
		if auditSignJSONFlag {
			jsonOut, jsonErr := execPlan.RenderJSON()
			if jsonErr != nil {
				return fmt.Errorf("failed to render plan: %w", jsonErr)
			}
			fmt.Fprintln(cmd.OutOrStdout(), jsonOut)
		} else {
			fmt.Fprint(cmd.OutOrStdout(), execPlan.RenderText())
		}
		return nil
	}

	if auditSignPayload != "" && auditSignPayloadFile != "" {
		return errors.WrapValidationError("only one of --payload or --payload-file may be provided")
	}

	payloadBytes, err := readAuditPayload(auditSignPayload, auditSignPayloadFile)
	if err != nil {
		return err
	}

	if len(strings.TrimSpace(string(payloadBytes))) == 0 {
		return errors.WrapValidationError("payload is required")
	}

	var payload interface{}
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return errors.WrapValidationError(fmt.Sprintf("invalid JSON payload: %v", err))
	}

	canonicalPayload, err := marshalCanonical(payload)
	if err != nil {
		return errors.WrapMarshalFailed(err)
	}

	providerName, cfg := resolveProviderAndConfig()

	signerImpl, err := signer.DefaultRegistry.CreateSigner(providerName, cfg)
	if err != nil {
		return err
	}
	defer func() {
		if closer, ok := signerImpl.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
	}()

	// Retrieve key-origin metadata before signing so it can be covered by the
	// signature.  The metadata is non-sensitive (no PINs, no private key material).
	keyOrigin := signerImpl.KeyOrigin()
	keyOrigin.Provider = providerName

	// Build the hash input: canonical payload + provider + key origin metadata.
	// Including provider and key-origin in the hash ensures that any tampering
	// with these fields after signing invalidates the signature.
	type signedInput struct {
		Payload   json.RawMessage          `json:"payload"`
		Provider  string                   `json:"provider"`
		KeyOrigin signer.KeyOriginMetadata `json:"key_origin"`
	}
	si := signedInput{
		Payload:   json.RawMessage(canonicalPayload),
		Provider:  providerName,
		KeyOrigin: keyOrigin,
	}
	hashInputBytes, err := marshalCanonical(si)
	if err != nil {
		return errors.WrapMarshalFailed(err)
	}

	hash := sha256.Sum256(hashInputBytes)
	// ── Signing ───────────────────────────────────────────────────────────────
	// When the signer is a KMSSigner, use SignWithMetadata so per-attempt audit
	// records are captured and included in the canonical hash.  For all other
	// providers the existing Sign() path is used unchanged.
	var signature []byte
	var signingAttempts []signer.SigningAttempt

	if kmsSgn, ok := signerImpl.(*signer.KMSSigner); ok {
		// Use the correlation ID from the key-id flag when available so the
		// signing session can be traced across KMS CloudTrail and Glassbox logs.
		corrID := auditSignKeyID
		kmsMeta, kmsErr := kmsSgn.SignWithMetadata(cmd.Context(), hash[:], corrID)
		signingAttempts = kmsMeta.SigningAttempts

		if kmsErr != nil {
			// Write the unsigned payload and attempt history to the dead-letter
			// queue so operators can re-sign after the KMS issue is resolved.
			dlw := signer.NewDeadLetterWriter()
			dlEntry := signer.DeadLetterEntry{
				SessionID:      corrID,
				IdempotencyKey: kmsSgn.IdempotencyKeyFor(hash[:]),
				Provider:       providerName,
				ErrorCode:      kmsMeta.ErrorCode,
				ErrorClass:     kmsMeta.ErrorClass,
				SigningAttempts: signingAttempts,
				Payload:        json.RawMessage(payloadBytes),
			}
			if writeErr := dlw.Write(dlEntry); writeErr != nil {
				// Writing to the dead-letter queue failed — surface both errors
				// so the operator knows the payload was lost.
				return errors.WrapValidationError(fmt.Sprintf(
					"signing failed: %v\n"+
						"  Additionally, failed to write dead-letter entry: %v\n"+
						"  The unsigned payload has NOT been preserved.",
					kmsErr, writeErr,
				))
			}
			dlDir, _ := signer.DefaultDeadLetterDir()
			return errors.WrapValidationError(fmt.Sprintf(
				"signing failed after %d attempt(s): %v\n"+
					"  The unsigned payload has been written to the dead-letter queue at:\n"+
					"    %s\n"+
					"  Run 'glassbox audit:redeliver' to re-sign it once the KMS issue is resolved.",
				kmsMeta.Attempts, kmsErr, dlDir,
			))
		}
		signature = kmsMeta.Signature
	} else {
		var signErr error
		signature, signErr = signerImpl.Sign(hash[:])
		if signErr != nil {
			return errors.WrapValidationError(fmt.Sprintf("signing failed: %v", signErr))
		}
	}

	publicKey, err := signerImpl.PublicKey()
	if err != nil {
		return errors.WrapValidationError(fmt.Sprintf("failed to retrieve public key: %v", err))
	}

	auditLog := SignedAuditLog{
		Version:         "1.0.0",
		Timestamp:       time.Now().UTC(),
		TraceHash:       hex.EncodeToString(hash[:]),
		Signature:       hex.EncodeToString(signature),
		PublicKey:       hex.EncodeToString(publicKey),
		Provider:        providerName,
		KeyOrigin:       &keyOrigin,
		Payload:         json.RawMessage(payloadBytes),
		SigningAttempts:  signingAttempts,
	}

	// Attach provenance metadata when any provenance flag is set.
	prov, err := buildProvenance(providerName, hex.EncodeToString(publicKey), signerImpl.Algorithm())
	if err != nil {
		return err
	}
	if prov != nil {
		auditLog.Provenance = prov
	}

	output, err := json.MarshalIndent(auditLog, "", "  ")
	if err != nil {
		return errors.WrapMarshalFailed(err)
	}

	fmt.Fprintln(cmd.OutOrStdout(), string(output))
	return nil
}

// resolveProviderAndConfig determines the active provider name and builds a
// ProviderConfig from CLI flags and environment variables.
//
// Provider selection order:
//  1. --signing-provider flag
//  2. --hsm-provider flag (deprecated; "pkcs11" maps to "pkcs11")
//  3. GLASSBOX_SIGNING_PROVIDER environment variable
//  4. GLASSBOX_SIGNER_TYPE environment variable (legacy)
//  5. "software" (default)
func resolveProviderAndConfig() (string, signer.ProviderConfig) {
	cfg := signer.ProviderConfig{
		SoftwareKeyPEM:   auditSignSoftwareKey,
		PKCS11ModulePath: auditSignPKCS11Module,
		PKCS11PIN:        auditSignPKCS11PIN,
		PKCS11TokenLabel: auditSignPKCS11TokenLabel,
		PKCS11KeyLabel:   auditSignPKCS11KeyLabel,
		PKCS11KeyIDHex:   auditSignPKCS11KeyIDHex,
	}
	if slot, err := resolvePKCS11SlotIndex(); err == nil {
		cfg.PKCS11SlotIndex = slot
	}

	// Determine provider name
	name := auditSignProvider
	if name == "" && auditSignHSMProvider != "" {
		// Legacy --hsm-provider flag: map "pkcs11" → "pkcs11"
		name = strings.ToLower(auditSignHSMProvider)
	}
	if name == "" {
		name = os.Getenv("GLASSBOX_SIGNING_PROVIDER")
	}
	if name == "" {
		// Legacy GLASSBOX_SIGNER_TYPE support
		if legacy := os.Getenv("GLASSBOX_SIGNER_TYPE"); legacy != "" {
			name = strings.ToLower(legacy)
		}
	}
	if name == "" {
		name = "software"
	}

	return name, cfg
}

// readAuditPayload reads the payload from the provided string, file, or stdin.
func readAuditPayload(payload, payloadFile string) ([]byte, error) {
	if payloadFile != "" {
		b, err := os.ReadFile(payloadFile)
		if err != nil {
			return nil, errors.WrapValidationError(fmt.Sprintf("failed to read payload file: %v", err))
		}
		return b, nil
	}

	if payload != "" {
		return []byte(payload), nil
	}

	stat, err := os.Stdin.Stat()
	if err != nil {
		return nil, errors.WrapValidationError(fmt.Sprintf("failed to inspect stdin: %v", err))
	}

	if stat.Mode()&os.ModeCharDevice == 0 {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, errors.WrapValidationError(fmt.Sprintf("failed to read payload from stdin: %v", err))
		}
		return b, nil
	}

	return nil, nil
}

// resolveAuditSigner is kept for backward compatibility with existing tests
// and callers that have not yet migrated to the registry-based API.
//
// Deprecated: use signer.DefaultRegistry.CreateSigner instead.
func resolveAuditSigner() (signer.Signer, error) {
	name, cfg := resolveProviderAndConfig()
	return signer.DefaultRegistry.CreateSigner(name, cfg)
}

// runPkcs11Preflight executes the PKCS#11 preflight validator and prints a
// human-readable report. It exits with a non-zero status if any check fails.
func runPkcs11Preflight(cmd *cobra.Command) error {
	// Accept the provider from any selection path (--signing-provider,
	// --hsm-provider, or environment), not just the deprecated --hsm-provider.
	name, _ := resolveProviderAndConfig()
	if !strings.EqualFold(name, "pkcs11") {
		return errors.WrapValidationError(
			"--validate-only applies only to the pkcs11 provider; select it with --signing-provider pkcs11")
	}

	// Build the config from flags merged over environment so --pkcs11-* overrides
	// are honored (the validator itself reports any still-missing values).
	cfg, err := effectivePKCS11Config()
	if err != nil {
		return err
	}
	vcfg := signer.DefaultValidatorConfig()
	validator := signer.NewPkcs11Validator(cfg, vcfg, &signer.OsPkcs11Provider{})

	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "Running PKCS#11 preflight checks...")
	fmt.Fprintln(out)

	report := validator.Validate(context.Background())

	for _, r := range report.Results {
		if r.OK {
			fmt.Fprintf(out, "  [PASS] %-14s %s\n", r.Step, r.Message)
		} else {
			fmt.Fprintf(out, "  [FAIL] %-14s %s\n", r.Step, r.Message)
			fmt.Fprintf(out, "         %-14s Remediation: %s\n", "", r.Remediation)
		}
	}

	fmt.Fprintln(out)
	if report.Ready {
		fmt.Fprintln(out, "Result: PKCS#11 configuration is valid and ready for signing.")
		return nil
	}
	return errors.WrapValidationError("PKCS#11 preflight checks failed; review the output above for remediation steps")
}

// effectivePKCS11Config builds the PKCS#11 configuration the command will use,
// applying CLI flags over the corresponding GLASSBOX_PKCS11_* environment
// variables (flags take precedence). It is the single source of truth for both
// preflight validation and the early required-input checks.
func effectivePKCS11Config() (signer.Pkcs11Config, error) {
	cfg := signer.Pkcs11Config{
		ModulePath: firstNonEmpty(auditSignPKCS11Module, os.Getenv("GLASSBOX_PKCS11_MODULE")),
		PIN:        firstNonEmpty(auditSignPKCS11PIN, os.Getenv("GLASSBOX_PKCS11_PIN")),
		TokenLabel: firstNonEmpty(auditSignPKCS11TokenLabel, os.Getenv("GLASSBOX_PKCS11_TOKEN_LABEL")),
		KeyLabel:   firstNonEmpty(auditSignPKCS11KeyLabel, os.Getenv("GLASSBOX_PKCS11_KEY_LABEL")),
		KeyIDHex:   firstNonEmpty(auditSignPKCS11KeyIDHex, os.Getenv("GLASSBOX_PKCS11_KEY_ID")),
	}
	slotIndex, err := resolvePKCS11SlotIndex()
	if err != nil {
		return signer.Pkcs11Config{}, err
	}
	cfg.SlotIndex = slotIndex
	return cfg, nil
}

func resolvePKCS11SlotIndex() (int, error) {
	raw := strings.TrimSpace(os.Getenv("GLASSBOX_PKCS11_SLOT"))
	if raw == "" {
		return 0, nil
	}

	idx, err := strconv.Atoi(raw)
	if err != nil {
		return 0, errors.WrapValidationError(fmt.Sprintf(
			"GLASSBOX_PKCS11_SLOT must be a non-negative integer, got %q\n"+
				"  Fix: set GLASSBOX_PKCS11_SLOT to a numeric slot index such as 0 or 1, or unset it to use the default slot\n"+
				"  Tip: run 'glassbox audit:sign --validate-only --signing-provider pkcs11' to inspect available slot diagnostics",
			raw,
		))
	}
	if idx < 0 {
		return 0, errors.WrapValidationError(fmt.Sprintf(
			"GLASSBOX_PKCS11_SLOT must be a non-negative integer, got %d\n"+
				"  Fix: set GLASSBOX_PKCS11_SLOT to 0 or a higher slot index, or unset it to use the default slot\n"+
				"  Tip: run 'glassbox audit:sign --validate-only --signing-provider pkcs11' to inspect available slot diagnostics",
			idx,
		))
	}
	return idx, nil
}

// validatePKCS11SignInputs rejects an incomplete PKCS#11 configuration before any
// module is loaded, so the user gets an explicit message instead of a low-level
// failure mid-signing. The module path and PIN are required; the key CKA_ID,
// when supplied, must be valid hex. Key-label and key-id are mutually exclusive.
func validatePKCS11SignInputs(cfg signer.Pkcs11Config) error {
	var missing []string
	if cfg.ModulePath == "" {
		missing = append(missing, "--pkcs11-module (or GLASSBOX_PKCS11_MODULE)")
	}
	if cfg.PIN == "" {
		missing = append(missing, "--pkcs11-pin (or GLASSBOX_PKCS11_PIN)")
	}
	if len(missing) > 0 {
		return errors.WrapValidationError(fmt.Sprintf(
			"pkcs11 signing requires %s\n"+
				"  Provide the missing value(s), or run 'glassbox audit:sign --validate-only --signing-provider pkcs11' "+
				"for a full PKCS#11 preflight report.",
			strings.Join(missing, " and ")))
	}

	// Check module file exists before attempting to load it.
	if info, err := os.Stat(cfg.ModulePath); err != nil {
		if os.IsNotExist(err) {
			return errors.WrapValidationError(fmt.Sprintf(
				"--pkcs11-module: module file not found: %q\n"+
					"  Fix: verify the path is correct for your OS and architecture\n"+
					"  Tip: run 'glassbox audit:sign --validate-only --signing-provider pkcs11' for a full preflight report",
				cfg.ModulePath,
			))
		}
		return errors.WrapValidationError(fmt.Sprintf(
			"--pkcs11-module: cannot access %q: %v", cfg.ModulePath, err,
		))
	} else if info.IsDir() {
		return errors.WrapValidationError(fmt.Sprintf(
			"--pkcs11-module: %q is a directory, not a shared library\n"+
				"  Fix: provide the full path to the .so/.dylib/.dll file",
			cfg.ModulePath,
		))
	}

	if cfg.KeyIDHex != "" {
		if _, err := hex.DecodeString(cfg.KeyIDHex); err != nil {
			return errors.WrapValidationError(fmt.Sprintf(
				"--pkcs11-key-id must be a hex-encoded CKA_ID (e.g. a1b2c3): %v", err))
		}
	}

	if cfg.KeyLabel != "" && cfg.KeyIDHex != "" {
		return errors.WrapValidationError(
			"--pkcs11-key-label and --pkcs11-key-id are mutually exclusive; provide only one identifier for the signing key\n" +
				"  Fix: use --pkcs11-key-label <CKA_LABEL> OR --pkcs11-key-id <hex CKA_ID>, not both")
	}

	if cfg.KeyLabel == "" && cfg.KeyIDHex == "" {
		return errors.WrapValidationError(
			"pkcs11 signing requires one of --pkcs11-key-label (CKA_LABEL) or --pkcs11-key-id (hex CKA_ID)\n" +
				"  Fix: set GLASSBOX_PKCS11_KEY_LABEL or GLASSBOX_PKCS11_KEY_ID, or use the corresponding --pkcs11-* flag")
	}

	return nil
}

// firstNonEmpty returns the first non-empty string of its arguments.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// validateAuditSignProvenanceFlags validates provenance-related flags before signing.
// It rejects a malformed --previous-signature-hash (must be 64 hex chars) and checks
// that --cert-chain points to an existing, readable file so failures surface as
// clear messages rather than deep I/O errors inside buildProvenance.
func validateAuditSignProvenanceFlags(previousHash, certChainFile string) error {
	if previousHash != "" {
		if err := validateSHA256HexHash("--previous-signature-hash", previousHash); err != nil {
			return errors.WrapValidationError(
				err.Error() + "\n" +
					"  --previous-signature-hash must be the hex-encoded SHA-256 of the preceding signed audit log\n" +
					"  Fix: provide the exact 64-character hex string from the prior log's trace_hash field\n" +
					"  Example: glassbox audit:sign --previous-signature-hash <64-hex-chars> --payload-file payload.json",
			)
		}
	}

	if certChainFile != "" {
		if err := validateFilePath("cert-chain", certChainFile); err != nil {
			return errors.WrapValidationError(
				fmt.Sprintf("--cert-chain: file not found or not readable: %q\n"+
					"  Fix: provide the path to a valid PEM file containing the certificate chain (leaf first)\n"+
					"  Example: glassbox audit:sign --cert-chain ./certs/chain.pem --payload-file payload.json",
					certChainFile),
			)
		}
	}

	return nil
}

// buildProvenance constructs a SignatureProvenance from CLI flags.
// algorithm is the signing algorithm string reported by the Signer implementation
// (e.g. "ed25519") — using the live value instead of a provider-name guess
// ensures the field is always accurate regardless of which key type the HSM uses.
// Returns nil when no provenance flags are set (backward-compatible behaviour).
func buildProvenance(providerName, publicKeyHex, algorithm string) (*SignatureProvenance, error) {
	hasAny := auditSignSignerIdentity != "" ||
		auditSignKeyID != "" ||
		auditSignCertChainFile != "" ||
		auditSignPreviousSignatureHash != ""

	if !hasAny {
		return nil, nil
	}

	prov := &SignatureProvenance{
		SignerIdentity:        auditSignSignerIdentity,
		KeyID:                 auditSignKeyID,
		PreviousSignatureHash: auditSignPreviousSignatureHash,
		Algorithm:             algorithm,
	}

	// Load certificate chain from file when provided.
	if auditSignCertChainFile != "" {
		chainPEM, err := os.ReadFile(auditSignCertChainFile)
		if err != nil {
			return nil, errors.WrapValidationError(fmt.Sprintf("failed to read cert chain file: %v", err))
		}
		prov.CertificateChain = parsePEMChain(chainPEM)
	}

	return prov, nil
}

// parsePEMChain splits a PEM file into individual certificate strings.
func parsePEMChain(data []byte) []string {
	var certs []string
	rest := data
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type == "CERTIFICATE" {
			certs = append(certs, string(pem.EncodeToMemory(block)))
		}
	}
	return certs
}
