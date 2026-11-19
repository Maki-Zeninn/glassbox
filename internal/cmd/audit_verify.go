// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/dotandev/glassbox/internal/errors"
	"github.com/spf13/cobra"
)

var (
	auditVerifyFile            string
	auditVerifyPublicKey       string
	auditVerifySchema          string
	auditVerifyJSON            bool
	auditVerifyPreviousHash    string
	auditVerifyTrustRoots      string
	auditVerifyAllowedIssuers  string
	auditVerifyCheckValidity   bool
	auditVerifyRevokedCerts    string
	auditVerifyPolicyConfig    string
	auditVerifyRevocationMode  string
)

// TrustPolicyConfig holds configurable trust policy for certificate/attestation evaluation.
// It is intentionally separate from cryptographic verification so that changing a policy
// affects only the policy outcome, not the underlying signature check.
type TrustPolicyConfig struct {
	// TrustRoots holds PEM-encoded root CA certificates that are considered trusted anchors.
	// Default (empty): no root check is performed.
	TrustRoots []string `json:"trust_roots,omitempty"`
	// AllowedIssuers is an allowlist of issuer Distinguished Names (or substrings).
	// Certificates whose issuer is not in this list are rejected with an explicit message.
	// Default (empty): all issuers are accepted.
	AllowedIssuers []string `json:"allowed_issuers,omitempty"`
	// CheckValidity enforces the certificate notBefore/notAfter validity window.
	// Default: false (validity is not checked).
	CheckValidity bool `json:"check_validity,omitempty"`
	// RevokedCertificates is a list of revoked serial numbers (hex strings).
	// Default (empty): no revocation check.
	RevokedCertificates []string `json:"revoked_certificates,omitempty"`
	// RevocationPolicy controls how revocation failures are handled.
	// "strict" (default when revoked_certificates is set): revoked certs fail the policy.
	// "none": revocation list is informational only.
	RevocationPolicy string `json:"revocation_policy,omitempty"`
}

// policyVerificationResult is the structured result of trust policy evaluation.
type policyVerificationResult struct {
	Valid             bool     `json:"valid"`
	TrustRootValid    bool     `json:"trust_root_valid"`
	IssuerAllowed     bool     `json:"issuer_allowed"`
	ValidityValid     bool     `json:"validity_valid"`
	RevocationValid   bool     `json:"revocation_valid"`
	Issues            []string `json:"issues,omitempty"`
	UntrustedIssuers  []string `json:"untrusted_issuers,omitempty"`
}

// auditVerifyResult is the structured output produced when --json is requested.
type auditVerifyResult struct {
	Valid           bool                      `json:"valid"`
	Version         string                    `json:"version,omitempty"`
	Timestamp       string                    `json:"timestamp,omitempty"`
	TraceHash       string                    `json:"trace_hash,omitempty"`
	PublicKey       string                    `json:"public_key,omitempty"`
	Provider        string                    `json:"provider,omitempty"`
	SignatureValid  bool                      `json:"signature_valid"`
	HashValid       bool                      `json:"hash_valid"`
	SchemaValid     *bool                     `json:"schema_valid,omitempty"`
	ProvenanceValid *bool                     `json:"provenance_valid,omitempty"`
	ChainLinkValid  *bool                     `json:"chain_link_valid,omitempty"`
	ChainNote       string                    `json:"chain_note,omitempty"`
	Policy          *policyVerificationResult `json:"policy,omitempty"`
	Error           string                    `json:"error,omitempty"`
}

var auditVerifyCmd = &cobra.Command{
	Use:     "audit:verify",
	GroupID: "utility",
	Short:   "Verify the signature and payload of a signed audit log",
	Long: `Verify the integrity and authenticity of a signed audit log produced by
audit:sign. The command re-derives the payload hash, checks it against the
embedded trace_hash field, and verifies the Ed25519 signature.

All inputs (--public-key, --schema, --previous-signature-hash) are validated
before the audit log is read, and required log fields are checked before any
cryptographic work, so malformed input fails fast with an explicit message.

TRUST POLICY
  Certificate and attestation verification can be controlled through an explicit
  trust policy rather than relying on ambient machine state:

  --trust-roots <pem-file>     Only accept certificates rooted in this CA.
  --allowed-issuers <csv>      Allowlist of certificate issuer DN substrings.
                               Unrecognised issuers are clearly identified.
  --check-validity             Enforce notBefore/notAfter windows.
  --revoked-certs <csv|file>   Hex serials of revoked certificates.
  --revocation-mode strict     Fail on revoked certs (default when list given).
  --policy-config <json-file>  Load full TrustPolicyConfig from a JSON file.
                               CLI flags take precedence over file values.

  When no trust policy flags are set the command performs cryptographic
  verification only (signature + hash) — this is the documented default.
  Policy evaluation is always a separate step and its result is shown
  distinctly from the cryptographic result.

CHAIN INTEGRITY
  Signed logs may carry a provenance.previous_signature_hash that links each log
  to its predecessor, forming a tamper-evident chain. Pass the expected
  predecessor hash with --previous-signature-hash to verify that this log links
  to it. Without that flag the chain hash is only format-checked, not verified —
  the command says so explicitly so a passing report is not mistaken for a
  verified chain.

EXAMPLES
  # Verify with the public key embedded in the file
  glassbox audit:verify --audit-log signed-audit.json

  # Override the public key for independent verification
  glassbox audit:verify --audit-log signed-audit.json --public-key <hex>

  # Validate payload structure against a JSON schema
  glassbox audit:verify --audit-log signed-audit.json --schema payload-schema.json

  # Verify with explicit trust roots and issuer allowlist
  glassbox audit:verify --audit-log signed-audit.json \
    --trust-roots /etc/glassbox/trusted-ca.pem \
    --allowed-issuers "CN=My HSM CA,OU=Security"

  # Verify enforcing validity windows and checking revocation list
  glassbox audit:verify --audit-log signed-audit.json \
    --check-validity --revoked-certs deadbeef01,cafebabe02

  # Load policy from config file
  glassbox audit:verify --audit-log signed-audit.json --policy-config trust-policy.json

  # Verify this log links to the expected previous log in the chain
  glassbox audit:verify --audit-log signed-audit.json --previous-signature-hash <hex>

  # Machine-readable JSON output
  glassbox audit:verify --audit-log signed-audit.json --json`,
	Args:    cobra.NoArgs,
	PreRunE: auditVerifyPreRunE,
	RunE:    runAuditVerify,
}

// auditVerifyPreRunE validates all inputs before the audit log is read.
func auditVerifyPreRunE(cmd *cobra.Command, args []string) error {
	if auditVerifyFile == "" {
		return errors.WrapCliArgumentRequired("audit-log")
	}
	if err := validateFilePath("audit-log", auditVerifyFile); err != nil {
		return err
	}
	if auditVerifyTrustRoots != "" {
		if err := validateFilePath("trust-roots", auditVerifyTrustRoots); err != nil {
			// Allow raw PEM strings too — only reject if it looks like a path but doesn't exist.
			if !strings.HasPrefix(auditVerifyTrustRoots, "-----BEGIN") {
				return err
			}
		}
	}
	if auditVerifyPolicyConfig != "" {
		if err := validateFilePath("policy-config", auditVerifyPolicyConfig); err != nil {
			return err
		}
	}
	if auditVerifyRevocationMode != "" && auditVerifyRevocationMode != "none" && auditVerifyRevocationMode != "strict" {
		return errors.WrapValidationError("--revocation-mode must be \"none\" or \"strict\"")
	}
	return validateAuditVerifyInputs(auditVerifyPublicKey, auditVerifySchema, auditVerifyPreviousHash)
}

func init() {
	auditVerifyCmd.Flags().StringVar(&auditVerifyFile, "audit-log", "", "Path to signed audit log JSON file (required)")
	auditVerifyCmd.Flags().StringVar(&auditVerifyPublicKey, "public-key", "", "Hex-encoded Ed25519 public key (overrides key embedded in the log)")
	auditVerifyCmd.Flags().StringVar(&auditVerifySchema, "schema", "", "Path to a JSON schema file to validate the payload against")
	auditVerifyCmd.Flags().StringVar(&auditVerifyPreviousHash, "previous-signature-hash", "", "Expected hex SHA-256 of the previous log; verifies this log's chain link")
	auditVerifyCmd.Flags().BoolVar(&auditVerifyJSON, "json", false, "Output verification result as JSON")

	// Trust policy flags — each is optional; combining them composes the active policy.
	auditVerifyCmd.Flags().StringVar(&auditVerifyTrustRoots, "trust-roots", "", "Path to a PEM file containing trusted root CA certificates, or an inline PEM string")
	auditVerifyCmd.Flags().StringVar(&auditVerifyAllowedIssuers, "allowed-issuers", "", "Comma-separated list of allowed certificate issuer DN substrings")
	auditVerifyCmd.Flags().BoolVar(&auditVerifyCheckValidity, "check-validity", false, "Enforce certificate notBefore/notAfter validity window")
	auditVerifyCmd.Flags().StringVar(&auditVerifyRevokedCerts, "revoked-certs", "", "Comma-separated list or path to file of revoked certificate serial numbers (hex)")
	auditVerifyCmd.Flags().StringVar(&auditVerifyRevocationMode, "revocation-mode", "", "Revocation policy mode: \"strict\" (fail on revoked) or \"none\" (informational only)")
	auditVerifyCmd.Flags().StringVar(&auditVerifyPolicyConfig, "policy-config", "", "Path to a JSON file with TrustPolicyConfig; CLI flags override file values")

	rootCmd.AddCommand(auditVerifyCmd)
}

func runAuditVerify(cmd *cobra.Command, args []string) error {
	data, err := os.ReadFile(auditVerifyFile)
	if err != nil {
		return errors.WrapValidationError(fmt.Sprintf("failed to read audit log: %v", err))
	}

	var log SignedAuditLog
	if err := json.Unmarshal(data, &log); err != nil {
		return errors.WrapValidationError(fmt.Sprintf("failed to parse audit log JSON: %v", err))
	}

	// Reject structurally incomplete logs before any cryptographic work, so a
	// missing field produces an explicit message rather than an opaque failure.
	if err := validateAuditLogFields(&log, auditVerifyPublicKey != ""); err != nil {
		return err
	}

	result := auditVerifyResult{
		Version:   log.Version,
		Timestamp: log.Timestamp.Format(time.RFC3339),
		TraceHash: log.TraceHash,
		PublicKey: log.PublicKey,
		Provider:  log.Provider,
	}

	// Resolve public key: flag takes precedence over embedded key.
	pubKeyHex := log.PublicKey
	if auditVerifyPublicKey != "" {
		pubKeyHex = auditVerifyPublicKey
	}

	// Step 1: Re-derive payload hash.
	var payload interface{}
	if err := json.Unmarshal(log.Payload, &payload); err != nil {
		return errors.WrapValidationError(fmt.Sprintf("failed to parse payload JSON: %v", err))
	}
	canonicalBytes, err := marshalCanonical(payload)
	if err != nil {
		return errors.WrapMarshalFailed(err)
	}
	derivedHash := sha256.Sum256(canonicalBytes)
	derivedHashHex := hex.EncodeToString(derivedHash[:])

	result.HashValid = derivedHashHex == log.TraceHash

	// Step 2: Verify Ed25519 signature.
	pubKeyBytes, err := hex.DecodeString(pubKeyHex)
	if err != nil || len(pubKeyBytes) != ed25519.PublicKeySize {
		msg := "invalid public key"
		if err != nil {
			msg = fmt.Sprintf("invalid public key hex: %v", err)
		}
		result.Error = msg
		return outputVerifyResult(cmd, result, false)
	}

	sigBytes, err := hex.DecodeString(log.Signature)
	if err != nil {
		result.Error = fmt.Sprintf("invalid signature hex: %v", err)
		return outputVerifyResult(cmd, result, false)
	}

	result.SignatureValid = ed25519.Verify(ed25519.PublicKey(pubKeyBytes), derivedHash[:], sigBytes)

	// Step 3: Optional schema validation.
	if auditVerifySchema != "" {
		schemaValid, schemaErr := validatePayloadAgainstSchema(log.Payload, auditVerifySchema)
		result.SchemaValid = &schemaValid
		if schemaErr != nil && result.Error == "" {
			result.Error = fmt.Sprintf("schema validation: %v", schemaErr)
		}
	}

	// Step 4: Validate provenance fields when present.
	if log.Provenance != nil {
		provValid, provErr := validateProvenance(log.Provenance)
		result.ProvenanceValid = &provValid
		if provErr != nil {
			result.Error = appendErr(result.Error, fmt.Sprintf("provenance validation: %v", provErr))
		}
	}

	// Step 5: Chain-link verification.
	// When --previous-signature-hash is supplied, verify this log actually links
	// to the expected predecessor. Otherwise, if the log carries a chain hash,
	// make clear it was only format-checked — not verified against a prior log.
	if auditVerifyPreviousHash != "" {
		linkValid, linkErr := verifyChainLink(&log, auditVerifyPreviousHash)
		result.ChainLinkValid = &linkValid
		if linkErr != nil {
			result.Error = appendErr(result.Error, fmt.Sprintf("chain link: %v", linkErr))
		}
	} else if log.Provenance != nil && log.Provenance.PreviousSignatureHash != "" {
		// Format-check the embedded hash so a malformed value is caught even
		// when the caller does not supply --previous-signature-hash for full
		// chain verification. A structurally invalid hash cannot link to any
		// predecessor, so we surface a warning rather than silently pass.
		if fmtErr := validateSHA256HexHash("previous_signature_hash", log.Provenance.PreviousSignatureHash); fmtErr != nil {
			result.ChainNote = fmt.Sprintf(
				"previous_signature_hash is present but its format is invalid (%v); "+
					"the log may be corrupted or tampered — "+
					"pass --previous-signature-hash <hex> to attempt full chain verification",
				fmtErr,
			)
		} else {
			result.ChainNote = "previous_signature_hash is present but chain linkage was not verified; " +
				"pass --previous-signature-hash <hex> to confirm this log links to the expected predecessor"
		}
	}

	// Step 6: Trust policy evaluation (separate from cryptographic checks).
	policy, policyErr := buildTrustPolicy()
	if policyErr != nil {
		return policyErr
	}
	if policy != nil {
		policyResult := evaluateTrustPolicy(&log, policy)
		result.Policy = &policyResult
	}

	result.Valid = result.HashValid && result.SignatureValid &&
		(result.SchemaValid == nil || *result.SchemaValid) &&
		(result.ProvenanceValid == nil || *result.ProvenanceValid) &&
		(result.ChainLinkValid == nil || *result.ChainLinkValid) &&
		(result.Policy == nil || result.Policy.Valid)

	return outputVerifyResult(cmd, result, result.Valid)
}

// buildTrustPolicy assembles a TrustPolicyConfig from CLI flags and an optional
// --policy-config JSON file. CLI flags always override file values.
// Returns nil when no trust policy is configured (default: crypto-only mode).
func buildTrustPolicy() (*TrustPolicyConfig, error) {
	hasPolicy := auditVerifyTrustRoots != "" ||
		auditVerifyAllowedIssuers != "" ||
		auditVerifyCheckValidity ||
		auditVerifyRevokedCerts != "" ||
		auditVerifyRevocationMode != "" ||
		auditVerifyPolicyConfig != ""

	if !hasPolicy {
		return nil, nil
	}

	var policy TrustPolicyConfig

	// Load base policy from file, if provided.
	if auditVerifyPolicyConfig != "" {
		data, err := os.ReadFile(auditVerifyPolicyConfig)
		if err != nil {
			return nil, errors.WrapValidationError(fmt.Sprintf("failed to read policy-config: %v", err))
		}
		if err := json.Unmarshal(data, &policy); err != nil {
			return nil, errors.WrapValidationError(fmt.Sprintf("failed to parse policy-config JSON: %v", err))
		}
	}

	// CLI flags override file values.
	if auditVerifyTrustRoots != "" {
		var rootPEM string
		if strings.HasPrefix(auditVerifyTrustRoots, "-----BEGIN") {
			rootPEM = auditVerifyTrustRoots
		} else {
			data, err := os.ReadFile(auditVerifyTrustRoots)
			if err != nil {
				return nil, errors.WrapValidationError(fmt.Sprintf("failed to read trust-roots file: %v", err))
			}
			rootPEM = string(data)
		}
		policy.TrustRoots = []string{rootPEM}
	}
	if auditVerifyAllowedIssuers != "" {
		parts := strings.Split(auditVerifyAllowedIssuers, ",")
		for i, p := range parts {
			parts[i] = strings.TrimSpace(p)
		}
		policy.AllowedIssuers = parts
	}
	if auditVerifyCheckValidity {
		policy.CheckValidity = true
	}
	if auditVerifyRevokedCerts != "" {
		// Accept either a comma-separated list or a file path.
		if _, err := os.Stat(auditVerifyRevokedCerts); err == nil {
			data, err := os.ReadFile(auditVerifyRevokedCerts)
			if err != nil {
				return nil, errors.WrapValidationError(fmt.Sprintf("failed to read revoked-certs file: %v", err))
			}
			for _, line := range strings.Split(string(data), "\n") {
				if s := strings.TrimSpace(line); s != "" {
					policy.RevokedCertificates = append(policy.RevokedCertificates, s)
				}
			}
		} else {
			for _, s := range strings.Split(auditVerifyRevokedCerts, ",") {
				if t := strings.TrimSpace(s); t != "" {
					policy.RevokedCertificates = append(policy.RevokedCertificates, t)
				}
			}
		}
	}
	if auditVerifyRevocationMode != "" {
		policy.RevocationPolicy = auditVerifyRevocationMode
	}

	return &policy, nil
}

// evaluateTrustPolicy checks the certificate chain in log against the given
// TrustPolicyConfig. It always produces a complete policyVerificationResult so
// the caller can display each dimension (root, issuer, validity, revocation)
// separately from the underlying cryptographic result.
func evaluateTrustPolicy(log *SignedAuditLog, policy *TrustPolicyConfig) policyVerificationResult {
	result := policyVerificationResult{
		TrustRootValid:  true,
		IssuerAllowed:   true,
		ValidityValid:   true,
		RevocationValid: true,
	}

	// Collect certificate chain PEM blocks from provenance, if present.
	var chainPEMs []string
	if log.Provenance != nil {
		chainPEMs = log.Provenance.CertificateChain
	}

	// Parse all PEM blocks into *x509.Certificate values.
	parsedCerts := parseCertChain(chainPEMs)

	// 1. Trust root check.
	if len(policy.TrustRoots) > 0 {
		if len(parsedCerts) == 0 {
			result.TrustRootValid = false
			result.Issues = append(result.Issues, "no certificates present to verify against configured trust roots")
		} else {
			trustedPool := x509.NewCertPool()
			for _, rootPEM := range policy.TrustRoots {
				trustedPool.AppendCertsFromPEM([]byte(rootPEM))
			}
			leaf := parsedCerts[0]
			opts := x509.VerifyOptions{Roots: trustedPool}
			if len(parsedCerts) > 1 {
				intermediate := x509.NewCertPool()
				for _, ic := range parsedCerts[1:] {
					intermediate.AddCert(ic)
				}
				opts.Intermediates = intermediate
			}
			if _, err := leaf.Verify(opts); err != nil {
				result.TrustRootValid = false
				result.Issues = append(result.Issues, fmt.Sprintf("chain root is not in configured trust roots: %v", err))
			}
		}
	}

	// 2. Issuer allowlist check.
	if len(policy.AllowedIssuers) > 0 {
		if len(parsedCerts) == 0 {
			result.IssuerAllowed = false
			result.Issues = append(result.Issues, "no certificates present to evaluate issuer allowlist")
		} else {
			for _, cert := range parsedCerts {
				issuerDN := cert.Issuer.String()
				allowed := false
				for _, allowedIssuer := range policy.AllowedIssuers {
					if strings.Contains(issuerDN, strings.TrimSpace(allowedIssuer)) {
						allowed = true
						break
					}
				}
				if !allowed {
					result.IssuerAllowed = false
					if !containsStr(result.UntrustedIssuers, issuerDN) {
						result.UntrustedIssuers = append(result.UntrustedIssuers, issuerDN)
					}
					result.Issues = append(result.Issues,
						fmt.Sprintf("untrusted issuer %q is not in allowed issuers list", issuerDN))
				}
			}
		}
	}

	// 3. Validity window check.
	if policy.CheckValidity {
		refTime := log.Timestamp
		if refTime.IsZero() {
			refTime = time.Now().UTC()
		}
		if len(parsedCerts) == 0 {
			result.ValidityValid = false
			result.Issues = append(result.Issues, "no certificates present to check validity window")
		} else {
			for _, cert := range parsedCerts {
				if refTime.Before(cert.NotBefore) {
					result.ValidityValid = false
					result.Issues = append(result.Issues, fmt.Sprintf(
						"certificate %q is not yet valid (notBefore: %s, reference time: %s)",
						cert.Subject.String(),
						cert.NotBefore.Format(time.RFC3339),
						refTime.Format(time.RFC3339),
					))
				}
				if refTime.After(cert.NotAfter) {
					result.ValidityValid = false
					result.Issues = append(result.Issues, fmt.Sprintf(
						"certificate %q has expired (notAfter: %s, reference time: %s)",
						cert.Subject.String(),
						cert.NotAfter.Format(time.RFC3339),
						refTime.Format(time.RFC3339),
					))
				}
			}
		}
	}

	// 4. Revocation check.
	mode := strings.ToLower(strings.TrimSpace(policy.RevocationPolicy))
	if mode == "" && len(policy.RevokedCertificates) > 0 {
		mode = "strict" // default when a revocation list is present
	}
	if mode == "strict" && len(policy.RevokedCertificates) > 0 {
		for _, cert := range parsedCerts {
			serial := strings.ToLower(fmt.Sprintf("%x", cert.SerialNumber))
			for _, revoked := range policy.RevokedCertificates {
				if strings.ToLower(strings.TrimSpace(revoked)) == serial {
					result.RevocationValid = false
					result.Issues = append(result.Issues, fmt.Sprintf(
						"certificate %q is revoked (serial: %s)",
						cert.Subject.String(), serial,
					))
				}
			}
		}
	}

	result.Valid = result.TrustRootValid && result.IssuerAllowed &&
		result.ValidityValid && result.RevocationValid

	return result
}

// parseCertChain parses PEM-encoded certificates and returns the successfully
// decoded ones. Failures are silently skipped; the caller already validated PEM
// format through validateProvenance.
func parseCertChain(pemBlocks []string) []*x509.Certificate {
	var certs []*x509.Certificate
	for _, block := range pemBlocks {
		for rest := []byte(block); len(rest) > 0; {
			var b *pem.Block
			b, rest = pem.Decode(rest)
			if b == nil {
				break
			}
			if b.Type != "CERTIFICATE" {
				continue
			}
			cert, err := x509.ParseCertificate(b.Bytes)
			if err == nil {
				certs = append(certs, cert)
			}
		}
	}
	return certs
}

// containsStr returns true if ss contains the string s.
func containsStr(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

// resetAuditVerifyFlags resets all package-level flag variables to their zero
// values so that successive RunE calls in tests start from a clean state.
func resetAuditVerifyFlags() {
	auditVerifyFile = ""
	auditVerifyPublicKey = ""
	auditVerifySchema = ""
	auditVerifyJSON = false
	auditVerifyPreviousHash = ""
	auditVerifyTrustRoots = ""
	auditVerifyAllowedIssuers = ""
	auditVerifyCheckValidity = false
	auditVerifyRevokedCerts = ""
	auditVerifyRevocationMode = ""
	auditVerifyPolicyConfig = ""
}

// outputVerifyResult prints the verification result in human-readable or JSON format.
func outputVerifyResult(cmd *cobra.Command, r auditVerifyResult, valid bool) error {
	out := cmd.OutOrStdout()

	if auditVerifyJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	}

	fmt.Fprintln(out, "Audit Log Verification")
	fmt.Fprintln(out, strings.Repeat("─", 50))

	if r.Version != "" {
		fmt.Fprintf(out, "  Version:    %s\n", r.Version)
	}
	if r.Timestamp != "" {
		fmt.Fprintf(out, "  Timestamp:  %s\n", r.Timestamp)
	}
	if r.Provider != "" {
		fmt.Fprintf(out, "  Provider:   %s\n", r.Provider)
	}
	if r.PublicKey != "" {
		// Show a shortened key identifier: first 8 + last 8 hex chars.
		keyID := r.PublicKey
		if len(keyID) > 16 {
			keyID = keyID[:8] + "…" + keyID[len(keyID)-8:]
		}
		fmt.Fprintf(out, "  Key ID:     %s\n", keyID)
	}
	if r.TraceHash != "" {
		fmt.Fprintf(out, "  Trace Hash: %s\n", r.TraceHash)
	}

	fmt.Fprintln(out)
	fmt.Fprintln(out, "Cryptographic Verification")
	printCheck(out, "Payload hash", r.HashValid)
	printCheck(out, "Signature   ", r.SignatureValid)
	if r.SchemaValid != nil {
		printCheck(out, "Schema      ", *r.SchemaValid)
	}
	if r.ProvenanceValid != nil {
		printCheck(out, "Provenance  ", *r.ProvenanceValid)
	}
	if r.ChainLinkValid != nil {
		printCheck(out, "Chain link  ", *r.ChainLinkValid)
	}

	if r.ChainNote != "" {
		fmt.Fprintf(out, "\nNote: %s\n", r.ChainNote)
	}

	if r.Policy != nil {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Trust Policy")
		printCheck(out, "Trust root  ", r.Policy.TrustRootValid)
		printCheck(out, "Issuer      ", r.Policy.IssuerAllowed)
		printCheck(out, "Validity    ", r.Policy.ValidityValid)
		printCheck(out, "Revocation  ", r.Policy.RevocationValid)
		if len(r.Policy.UntrustedIssuers) > 0 {
			fmt.Fprintf(out, "  Untrusted issuers: %s\n", strings.Join(r.Policy.UntrustedIssuers, "; "))
		}
		if len(r.Policy.Issues) > 0 {
			for _, issue := range r.Policy.Issues {
				fmt.Fprintf(out, "  Issue: %s\n", issue)
			}
		}
	}

	fmt.Fprintln(out)
	if valid {
		fmt.Fprintln(out, "Result: VALID — audit log integrity confirmed.")
		return nil
	}

	if r.Error != "" {
		fmt.Fprintf(out, "Error: %s\n", r.Error)
	}
	fmt.Fprintln(out, "Result: INVALID — audit log failed verification.")
	return errors.WrapAuditLogInvalid("audit log verification failed")
}

func printCheck(out interface{ Write([]byte) (int, error) }, label string, ok bool) {
	status := "PASS"
	if !ok {
		status = "FAIL"
	}
	fmt.Fprintf(out, "  [%s] %s\n", status, label)
}

// validatePayloadAgainstSchema validates payload bytes against a JSON schema file.
// It performs structural validation: checks that the schema's required keys exist
// in the payload and that the payload is a valid JSON object.
func validatePayloadAgainstSchema(payload json.RawMessage, schemaPath string) (bool, error) {
	schemaData, err := os.ReadFile(schemaPath)
	if err != nil {
		return false, fmt.Errorf("failed to read schema file %q: %w", schemaPath, err)
	}

	var schema map[string]interface{}
	if err := json.Unmarshal(schemaData, &schema); err != nil {
		return false, fmt.Errorf("failed to parse schema JSON: %w", err)
	}

	var payloadMap map[string]interface{}
	if err := json.Unmarshal(payload, &payloadMap); err != nil {
		return false, fmt.Errorf("payload is not a JSON object: %w", err)
	}

	// Validate required fields declared in the schema.
	if required, ok := schema["required"]; ok {
		requiredList, ok := required.([]interface{})
		if !ok {
			return false, fmt.Errorf("schema 'required' field must be an array")
		}
		for _, field := range requiredList {
			fieldStr, ok := field.(string)
			if !ok {
				continue
			}
			if _, exists := payloadMap[fieldStr]; !exists {
				return false, fmt.Errorf("payload missing required field %q", fieldStr)
			}
		}
	}

	// Validate property types when a 'properties' section is present.
	if props, ok := schema["properties"]; ok {
		propsMap, ok := props.(map[string]interface{})
		if ok {
			for key, propDef := range propsMap {
				val, exists := payloadMap[key]
				if !exists {
					continue
				}
				propDefMap, ok := propDef.(map[string]interface{})
				if !ok {
					continue
				}
				expectedType, ok := propDefMap["type"].(string)
				if !ok {
					continue
				}
				if err := checkJSONType(key, val, expectedType); err != nil {
					return false, err
				}
			}
		}
	}

	return true, nil
}

// checkJSONType validates that val matches the expected JSON Schema type string.
func checkJSONType(field string, val interface{}, expected string) error {
	var actual string
	switch val.(type) {
	case string:
		actual = "string"
	case float64:
		actual = "number"
	case bool:
		actual = "boolean"
	case map[string]interface{}:
		actual = "object"
	case []interface{}:
		actual = "array"
	case nil:
		actual = "null"
	default:
		actual = "unknown"
	}

	if actual != expected {
		return fmt.Errorf("field %q has type %q, expected %q", field, actual, expected)
	}
	return nil
}

// validateProvenance checks that provenance metadata fields are well-formed.
// It validates the certificate chain PEM blocks and the previous-signature hash
// format when they are present.
func validateProvenance(p *SignatureProvenance) (bool, error) {
	if p == nil {
		return true, nil
	}

	var issues []string

	if len(p.CertificateChain) == 0 && (p.SignerIdentity != "" || p.KeyID != "" || p.PreviousSignatureHash != "") {
		issues = append(issues, "provenance has identity fields but no certificate chain — "+
			"provide --cert-chain for full chain-of-trust verification")
	}

	for i, certPEM := range p.CertificateChain {
		block, rest := pem.Decode([]byte(certPEM))
		if block == nil {
			issues = append(issues, fmt.Sprintf("certificate_chain[%d] is not valid PEM (decode failed)", i))
			continue
		}
		if block.Type != "CERTIFICATE" {
			issues = append(issues, fmt.Sprintf("certificate_chain[%d] has unexpected PEM type %q", i, block.Type))
		}
		if len(rest) != 0 && i < len(p.CertificateChain)-1 {
			issues = append(issues, fmt.Sprintf("certificate_chain[%d] contains trailing data after PEM block; "+
				"each certificate should be a single PEM block, not concatenated", i))
		}
	}

	if p.PreviousSignatureHash != "" {
		if err := validateSHA256HexHash("previous_signature_hash", p.PreviousSignatureHash); err != nil {
			issues = append(issues, fmt.Sprintf("previous_signature_hash format invalid: %v — "+
				"the chain link cannot be verified; pass --previous-signature-hash <hex> to attempt verification", err))
		}
	}

	if len(issues) > 0 {
		return false, fmt.Errorf("provenance validation failed:\n  - %s", strings.Join(issues, "\n  - "))
	}

	return true, nil
}

// validateAuditVerifyInputs validates the command's flag inputs before the audit
// log is read, so malformed values fail fast with an explicit message. It checks
// the --public-key hex/length, the --schema file path, and the
// --previous-signature-hash format when each is provided.
func validateAuditVerifyInputs(pubKeyHex, schemaPath, previousHash string) error {
	if pubKeyHex != "" {
		b, err := hex.DecodeString(pubKeyHex)
		if err != nil {
			return errors.WrapValidationError(fmt.Sprintf("--public-key is not valid hex: %v", err))
		}
		if len(b) != ed25519.PublicKeySize {
			return errors.WrapValidationError(fmt.Sprintf(
				"--public-key must be %d bytes (%d hex characters), got %d bytes",
				ed25519.PublicKeySize, ed25519.PublicKeySize*2, len(b)))
		}
	}

	if schemaPath != "" {
		if err := validateFilePath("schema", schemaPath); err != nil {
			return err
		}
	}

	if previousHash != "" {
		if err := validateSHA256HexHash("--previous-signature-hash", previousHash); err != nil {
			return errors.WrapValidationError(err.Error())
		}
	}

	return nil
}

// validateAuditLogFields rejects a structurally incomplete signed audit log
// before any cryptographic work. When the public key is supplied separately via
// --public-key, an absent embedded public_key is allowed.
func validateAuditLogFields(log *SignedAuditLog, hasPubKeyOverride bool) error {
	var missing []string
	if strings.TrimSpace(log.Signature) == "" {
		missing = append(missing, "signature")
	}
	if strings.TrimSpace(log.TraceHash) == "" {
		missing = append(missing, "trace_hash")
	}
	if strings.TrimSpace(log.Provider) == "" {
		missing = append(missing, "provider")
	}
	if !hasPubKeyOverride && strings.TrimSpace(log.PublicKey) == "" {
		missing = append(missing, "public_key")
	}
	if len(log.Payload) == 0 {
		missing = append(missing, "payload")
	}

	if len(missing) > 0 {
		return errors.WrapValidationError(fmt.Sprintf(
			"audit log is missing required field(s): %s\n"+
				"  A valid signed audit log must contain signature, trace_hash, public_key, payload, and provider.\n"+
				"  If the public key is provided separately, pass it with --public-key.\n"+
				"  Run 'glassbox audit:sign --help' for the signing command usage.",
			strings.Join(missing, ", ")))
	}

	return nil
}

// verifyChainLink checks that the log's provenance.previous_signature_hash equals
// the expected predecessor hash, confirming this log's link in the audit chain.
// Comparison is case-insensitive. It returns an actionable error when the log
// carries no chain hash (e.g. a genesis entry or missing provenance) or when the
// hashes differ.
func verifyChainLink(log *SignedAuditLog, expectedPreviousHash string) (bool, error) {
	expected := strings.ToLower(strings.TrimSpace(expectedPreviousHash))

	if log.Provenance == nil || strings.TrimSpace(log.Provenance.PreviousSignatureHash) == "" {
		return false, fmt.Errorf(
			"this audit log has no previous_signature_hash to verify — it may be the genesis " +
				"entry of the chain, or it was signed without provenance; omit --previous-signature-hash " +
				"to verify it as a standalone log")
	}

	actual := strings.ToLower(strings.TrimSpace(log.Provenance.PreviousSignatureHash))
	if actual != expected {
		return false, fmt.Errorf(
			"chain link broken: log's previous_signature_hash %s does not match the expected predecessor %s",
			shortHash(actual), shortHash(expected))
	}

	return true, nil
}

// validateSHA256HexHash verifies that h is a 64-character hexadecimal string
// (the form of a hex-encoded SHA-256 digest). label names the value in errors.
func validateSHA256HexHash(label, h string) error {
	if len(h) != 64 {
		return fmt.Errorf("%s must be a 64-character hex string (SHA-256), got %d characters", label, len(h))
	}
	for _, c := range h {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return fmt.Errorf("%s contains non-hex character %q", label, c)
		}
	}
	return nil
}

// shortHash abbreviates a hash for readable diagnostics (first 8 + last 8 chars).
func shortHash(h string) string {
	if len(h) <= 16 {
		return h
	}
	return h[:8] + "…" + h[len(h)-8:]
}

// appendErr joins error messages so multiple failures are surfaced together
// instead of one masking another.
func appendErr(existing, msg string) string {
	if existing == "" {
		return msg
	}
	return existing + "; " + msg
}
