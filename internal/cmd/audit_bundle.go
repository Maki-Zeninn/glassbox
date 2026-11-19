// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dotandev/glassbox/internal/auditbundle"
	"github.com/dotandev/glassbox/internal/errors"
	"github.com/dotandev/glassbox/internal/version"
	"github.com/spf13/cobra"
)

// ── bundle:pack ──────────────────────────────────────────────────────────────

var (
	bundlePackOutputFlag      string
	bundlePackDescriptionFlag string
	bundlePackKeyFileFlag     string
)

var bundlePackCmd = &cobra.Command{
	Use:     "bundle:pack",
	GroupID: "utility",
	Short:   "Pack signed audit logs into a portable offline verification bundle",
	Long: `Pack one or more signed audit log files (produced by audit:sign) into a
portable bundle that can be verified on an isolated machine with no network
access, no KMS, no HSM, and no RPC services.

The bundle contains:
  - All signed audit log entries
  - Public key catalog (keys referenced by the log entries, plus any extras
    provided via --public-keys)
  - Per-member SHA-256 integrity manifest
  - Bundle metadata (verifier version, Glassbox version, creation time)

Private keys are never included.  The pack command validates each entry to
ensure no private key fields are present before writing.

EXAMPLES
  # Pack a single signed audit log
  glassbox bundle:pack signed-audit.json -o incident-2026.auditbundle

  # Pack multiple logs with a description
  glassbox bundle:pack *.json -o bundle.auditbundle --desc "Q2 incident review"

  # Pack with an explicit public key catalog file
  glassbox bundle:pack signed-audit.json --public-keys keys.json -o bundle.auditbundle`,
	Args:    cobra.MinimumNArgs(1),
	PreRunE: bundlePackPreRunE,
	RunE:    runBundlePack,
}

func bundlePackPreRunE(_ *cobra.Command, args []string) error {
	for _, arg := range args {
		if strings.ContainsRune(arg, 0) {
			return errors.WrapValidationError(fmt.Sprintf("input path contains null bytes: %q", arg))
		}
		if _, err := os.Stat(arg); err != nil {
			return errors.WrapValidationError(fmt.Sprintf("input file not found or unreadable: %q: %v", arg, err))
		}
	}
	if bundlePackOutputFlag == "" {
		return errors.WrapCliArgumentRequired("output")
	}
	return nil
}

func runBundlePack(cmd *cobra.Command, args []string) error {
	// Load all signed audit log entries from the input files.
	var entries []auditbundle.SignedLogEntry
	for _, path := range args {
		loaded, err := loadSignedLogEntries(path)
		if err != nil {
			return fmt.Errorf("failed to load entries from %q: %w", path, err)
		}
		entries = append(entries, loaded...)
	}
	if len(entries) == 0 {
		return errors.WrapValidationError("no signed audit log entries found in the provided files")
	}

	// Optionally load an explicit public key catalog.
	var extraKeys []auditbundle.PublicKeyEntry
	if bundlePackKeyFileFlag != "" {
		catalog, err := loadPublicKeyCatalog(bundlePackKeyFileFlag)
		if err != nil {
			return fmt.Errorf("failed to load public keys from %q: %w", bundlePackKeyFileFlag, err)
		}
		extraKeys = catalog.Keys
	}

	opts := auditbundle.PackOptions{
		GlassboxVersion: version.Version,
		Description:     bundlePackDescriptionFlag,
		ExtraPublicKeys: extraKeys,
	}

	dest := bundlePackOutputFlag
	if filepath.Ext(dest) == "" {
		dest += auditbundle.BundleExtension
	}

	if err := auditbundle.Pack(dest, entries, nil, opts); err != nil {
		return fmt.Errorf("bundle:pack failed: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Bundle created: %s\n", dest)
	fmt.Fprintf(cmd.OutOrStdout(), "  Entries: %d\n", len(entries))
	if len(extraKeys) > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "  Extra keys: %d\n", len(extraKeys))
	}
	fmt.Fprintf(cmd.OutOrStdout(), "\nVerify offline with:\n")
	fmt.Fprintf(cmd.OutOrStdout(), "  glassbox bundle:verify %s\n", dest)
	return nil
}

// ── bundle:verify ────────────────────────────────────────────────────────────

var (
	bundleVerifyJSONFlag         bool
	bundleVerifyAllowedKeysFlag  string
	bundleVerifyRequireAllFlag   bool
	bundleVerifyStrictFlag       bool
)

var bundleVerifyCmd = &cobra.Command{
	Use:     "bundle:verify <bundle-file>",
	GroupID: "utility",
	Short:   "Verify a portable audit bundle without any network access",
	Long: `Verify the integrity and authenticity of a portable audit bundle produced by
bundle:pack.  The verification is fully offline — no network, KMS, HSM, or RPC
calls are made at any point.

THREE-PHASE VERIFICATION
  1. Bundle integrity   — re-hash every ZIP member and compare to the manifest.
                         A missing or mismatched hash fails before signature
                         checking so tampered bundles surface clearly.
  2. Signature validity — verify each log entry's Ed25519 signature against the
                         public key embedded in the bundle's key catalog.
  3. Trust policy       — check that each signing key is in the --allowed-keys
                         list when one is provided.  Without this flag, any key
                         in the catalog is implicitly trusted.

The result distinguishes all three outcomes so you can tell the difference
between a tampered bundle, a valid-but-untrusted signer, and a fully verified log.

EXIT CODES
  0  all phases passed
  1  verification failed (see output for which phase)

EXAMPLES
  # Basic offline verification
  glassbox bundle:verify incident-2026.auditbundle

  # Restrict trust to a specific key ID
  glassbox bundle:verify bundle.auditbundle --allowed-keys ops-key-2026

  # Require every log entry to carry a signature
  glassbox bundle:verify bundle.auditbundle --require-all-signed

  # Machine-readable JSON output
  glassbox bundle:verify bundle.auditbundle --json`,
	Args:    cobra.ExactArgs(1),
	PreRunE: bundleVerifyPreRunE,
	RunE:    runBundleVerify,
}

func bundleVerifyPreRunE(_ *cobra.Command, args []string) error {
	if strings.TrimSpace(args[0]) == "" {
		return errors.WrapCliArgumentRequired("bundle-file")
	}
	if strings.ContainsRune(args[0], 0) {
		return errors.WrapValidationError(fmt.Sprintf("bundle path contains null bytes: %q", args[0]))
	}
	if _, err := os.Stat(args[0]); err != nil {
		return errors.WrapValidationError(fmt.Sprintf("bundle file not found or unreadable: %q: %v", args[0], err))
	}
	return nil
}

func runBundleVerify(cmd *cobra.Command, args []string) error {
	bundlePath := args[0]

	var allowedKeyIDs []string
	if bundleVerifyAllowedKeysFlag != "" {
		for _, id := range strings.Split(bundleVerifyAllowedKeysFlag, ",") {
			if t := strings.TrimSpace(id); t != "" {
				allowedKeyIDs = append(allowedKeyIDs, t)
			}
		}
	}

	opts := auditbundle.VerifyOptions{
		AllowedKeyIDs:    allowedKeyIDs,
		RequireAllSigned: bundleVerifyRequireAllFlag,
		StrictIntegrity:  bundleVerifyStrictFlag,
	}

	result, err := auditbundle.VerifyBundle(bundlePath, opts)
	if err != nil {
		return fmt.Errorf("bundle:verify failed: %w", err)
	}

	if bundleVerifyJSONFlag {
		b, jsonErr := json.MarshalIndent(result, "", "  ")
		if jsonErr != nil {
			return fmt.Errorf("failed to encode verification result: %w", jsonErr)
		}
		fmt.Fprintln(cmd.OutOrStdout(), string(b))
		if !result.OK {
			return errors.WrapAuditLogInvalid("bundle verification failed")
		}
		return nil
	}

	return printBundleVerifyResult(cmd, result, bundlePath)
}

func printBundleVerifyResult(cmd *cobra.Command, result *auditbundle.VerificationResult, bundlePath string) error {
	out := cmd.OutOrStdout()
	sep := strings.Repeat("─", 56)

	fmt.Fprintln(out, "Audit Bundle Verification")
	fmt.Fprintln(out, sep)
	fmt.Fprintf(out, "  Bundle:      %s\n", filepath.Base(bundlePath))
	if result.BundleCreatedAt != nil {
		fmt.Fprintf(out, "  Created:     %s\n", result.BundleCreatedAt.Format(time.RFC3339))
	}
	fmt.Fprintf(out, "  Logs:        %d\n", result.LogsChecked)
	fmt.Fprintf(out, "  Verifier:    %s\n", result.VerifierVersion)
	fmt.Fprintln(out)

	// Phase 1: integrity
	fmt.Fprintln(out, "Phase 1 — Bundle Integrity")
	integrityStatus := "PASS"
	if !result.BundleIntegrity.OK {
		integrityStatus = "FAIL"
	}
	fmt.Fprintf(out, "  [%s] %d member(s) OK, %d failed\n",
		integrityStatus, result.BundleIntegrity.MembersOK, result.BundleIntegrity.MembersFailed)
	for _, issue := range result.BundleIntegrity.Issues {
		fmt.Fprintf(out, "    Issue: %s\n", issue)
	}

	// Phase 2: signatures
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Phase 2 — Signature Validity")
	sigStatus := "PASS"
	if !result.SignatureValidity.OK {
		sigStatus = "FAIL"
	}
	fmt.Fprintf(out, "  [%s] %d valid, %d invalid\n",
		sigStatus, result.SignatureValidity.ValidCount, result.SignatureValidity.InvalidCount)
	for _, issue := range result.SignatureValidity.Issues {
		fmt.Fprintf(out, "    Issue: %s\n", issue)
	}

	// Phase 3: trust policy
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Phase 3 — Trust Policy")
	trustStatus := "PASS"
	if !result.TrustPolicy.OK {
		trustStatus = "FAIL"
	}
	fmt.Fprintf(out, "  [%s] trusted: %d, unknown: %d\n",
		trustStatus, len(result.TrustPolicy.TrustedSigners), len(result.TrustPolicy.UnknownSigners))
	for _, signer := range result.TrustPolicy.TrustedSigners {
		fmt.Fprintf(out, "    Trusted: %s\n", signer)
	}
	for _, signer := range result.TrustPolicy.UnknownSigners {
		fmt.Fprintf(out, "    Unknown: %s\n", signer)
	}
	for _, issue := range result.TrustPolicy.Issues {
		fmt.Fprintf(out, "    Issue: %s\n", issue)
	}

	fmt.Fprintln(out)
	fmt.Fprintln(out, sep)
	if result.OK {
		fmt.Fprintln(out, "Result: VALID — bundle integrity, signatures, and trust policy all confirmed.")
		return nil
	}
	fmt.Fprintln(out, "Result: INVALID — bundle verification failed.")
	if len(result.Issues) > 0 {
		fmt.Fprintf(out, "\nSummary of issues:\n")
		for _, issue := range result.Issues {
			fmt.Fprintf(out, "  • %s\n", issue)
		}
	}
	return errors.WrapAuditLogInvalid("bundle verification failed")
}

// ── helpers ───────────────────────────────────────────────────────────────────

// loadSignedLogEntries reads a JSON file that is either a single SignedLogEntry
// object or a JSON array of SignedLogEntry objects.
func loadSignedLogEntries(path string) ([]auditbundle.SignedLogEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// Try array first.
	var entries []auditbundle.SignedLogEntry
	if err := json.Unmarshal(data, &entries); err == nil {
		return entries, nil
	}
	// Try single object.
	var entry auditbundle.SignedLogEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, fmt.Errorf("file %q is not a valid signed audit log (neither an object nor an array): %w", path, err)
	}
	return []auditbundle.SignedLogEntry{entry}, nil
}

// loadPublicKeyCatalog reads a PublicKeyCatalog from a JSON file.
func loadPublicKeyCatalog(path string) (*auditbundle.PublicKeyCatalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var catalog auditbundle.PublicKeyCatalog
	if err := json.Unmarshal(data, &catalog); err != nil {
		return nil, fmt.Errorf("file %q is not a valid public key catalog: %w", path, err)
	}
	return &catalog, nil
}

func init() {
	bundlePackCmd.Flags().StringVarP(&bundlePackOutputFlag, "output", "o", "",
		"Destination path for the bundle file (required; .auditbundle extension auto-appended)")
	bundlePackCmd.Flags().StringVar(&bundlePackDescriptionFlag, "desc", "",
		"Optional human-readable description to embed in the bundle")
	bundlePackCmd.Flags().StringVar(&bundlePackKeyFileFlag, "public-keys", "",
		"Path to a JSON file containing additional public keys to include in the bundle")

	bundleVerifyCmd.Flags().BoolVar(&bundleVerifyJSONFlag, "json", false,
		"Emit the verification result as JSON")
	bundleVerifyCmd.Flags().StringVar(&bundleVerifyAllowedKeysFlag, "allowed-keys", "",
		"Comma-separated list of allowed key IDs; unlisted keys fail the trust policy check")
	bundleVerifyCmd.Flags().BoolVar(&bundleVerifyRequireAllFlag, "require-all-signed", false,
		"Fail if any log entry is missing a signature")
	bundleVerifyCmd.Flags().BoolVar(&bundleVerifyStrictFlag, "strict", false,
		"Fail if any ZIP member is present but not recorded in the bundle manifest")

	rootCmd.AddCommand(bundlePackCmd)
	rootCmd.AddCommand(bundleVerifyCmd)
}
