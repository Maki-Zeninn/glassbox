// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

// audit_redeliver.go implements `glassbox audit:redeliver`.
//
// The command reads dead-letter files written by audit:sign when KMS signing
// exhausted all retries.  For each file (oldest first) it:
//
//  1. Re-signs the stored payload using the currently configured signing
//     provider.
//  2. Writes the signed record to stdout or the output directory.
//  3. Removes the dead-letter file on success.
//
// A single failed re-sign does not abort the batch — the file is left in
// the dead-letter queue and the next file is attempted.  A summary is
// printed at the end so operators know what succeeded and what still needs
// attention.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dotandev/glassbox/internal/errors"
	"github.com/dotandev/glassbox/internal/signer"
	"github.com/spf13/cobra"
)

var (
	// auditRedeliverDir overrides the default dead-letter directory.
	auditRedeliverDir string
	// auditRedeliverOutDir, when set, writes signed records as individual
	// JSON files in this directory instead of printing to stdout.
	auditRedeliverOutDir string
	// auditRedeliverDryRun prints what would be re-signed without doing it.
	auditRedeliverDryRun bool
	// auditRedeliverMaxFiles limits how many files are processed in one run.
	auditRedeliverMaxFiles int
)

var auditRedeliverCmd = &cobra.Command{
	Use:     "audit:redeliver",
	GroupID: "utility",
	Short:   "Re-sign dead-letter payloads left by a failed KMS signing operation",
	Long: `Re-sign unsigned audit payloads that were preserved in the dead-letter
queue after KMS signing exhausted all retries.

Files are processed oldest-first. For each file the command:

  1. Re-signs the stored payload using the currently configured signing
     provider (same provider selection logic as audit:sign).
  2. Prints the signed record to stdout, or writes it to --output-dir.
  3. Removes the dead-letter file on success.

A failed re-sign leaves the file in the queue and continues with the
next file. The exit code is non-zero if any file could not be re-signed.

Dead-letter directory: ~/.Glassbox/dead-letter/kms/

EXAMPLES

  # Re-sign all dead-letter files using the current KMS provider
  glassbox audit:redeliver \
    --signing-provider aws-kms

  # Dry-run: show what would be re-signed without doing it
  glassbox audit:redeliver --dry-run

  # Write re-signed records to a directory instead of stdout
  glassbox audit:redeliver --output-dir ./re-signed/

  # Use a custom dead-letter directory
  glassbox audit:redeliver --dead-letter-dir /mnt/audit/dead-letter/kms`,
	Args: cobra.NoArgs,
	RunE: runAuditRedeliver,
}

func init() {
	auditRedeliverCmd.Flags().StringVar(&auditRedeliverDir, "dead-letter-dir", "",
		"Dead-letter directory to scan (default: ~/.Glassbox/dead-letter/kms)")
	auditRedeliverCmd.Flags().StringVar(&auditRedeliverOutDir, "output-dir", "",
		"Write re-signed records to this directory instead of stdout")
	auditRedeliverCmd.Flags().BoolVar(&auditRedeliverDryRun, "dry-run", false,
		"Print what would be re-signed without modifying any files")
	auditRedeliverCmd.Flags().IntVar(&auditRedeliverMaxFiles, "max-files", 0,
		"Maximum number of dead-letter files to process in one run (0 = unlimited)")

	// Re-use the provider-selection flags already declared on auditSignCmd
	// by binding to the same package-level variables.  The persistent flags
	// are not shared; we redeclare only the ones needed for provider selection.
	auditRedeliverCmd.Flags().StringVar(&auditSignProvider, "signing-provider", "",
		fmt.Sprintf("Signing provider (%s)", strings.Join(signer.DefaultRegistry.Names(), ", ")))
	auditRedeliverCmd.Flags().StringVar(&auditSignSoftwareKey, "software-private-key", "",
		"PKCS#8 PEM Ed25519 private key (literal PEM or file path)")
	auditRedeliverCmd.Flags().StringVar(&auditSignPKCS11Module, "pkcs11-module", "",
		"Path to PKCS#11 shared library")
	auditRedeliverCmd.Flags().StringVar(&auditSignPKCS11PIN, "pkcs11-pin", "",
		"PKCS#11 user PIN")

	rootCmd.AddCommand(auditRedeliverCmd)
}

func runAuditRedeliver(cmd *cobra.Command, _ []string) error {
	// Resolve dead-letter directory.
	dlDir := auditRedeliverDir
	if dlDir == "" {
		var err error
		dlDir, err = signer.DefaultDeadLetterDir()
		if err != nil {
			return errors.WrapValidationError(fmt.Sprintf(
				"cannot determine dead-letter directory: %v", err))
		}
	}

	// Read all pending dead-letter files.
	files, err := signer.ReadDeadLetterDir(dlDir)
	if err != nil {
		return errors.WrapValidationError(fmt.Sprintf(
			"failed to read dead-letter directory %q: %v", dlDir, err))
	}

	if len(files) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No unsigned dead-letter files found. Nothing to redeliver.")
		return nil
	}

	// Apply max-files cap.
	if auditRedeliverMaxFiles > 0 && len(files) > auditRedeliverMaxFiles {
		files = files[:auditRedeliverMaxFiles]
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Found %d dead-letter file(s) in %s\n\n", len(files), dlDir)

	if auditRedeliverDryRun {
		for i, f := range files {
			fmt.Fprintf(cmd.OutOrStdout(), "  [dry-run] %d. %s\n", i+1, filepath.Base(f.Path))
			fmt.Fprintf(cmd.OutOrStdout(), "            failed_at=%s provider=%s attempts=%d\n\n",
				f.Entry.FailedAt.Format(time.RFC3339),
				f.Entry.Provider,
				len(f.Entry.SigningAttempts),
			)
		}
		return nil
	}

	// Create output directory if requested.
	if auditRedeliverOutDir != "" {
		if mkErr := os.MkdirAll(auditRedeliverOutDir, 0o755); mkErr != nil {
			return errors.WrapValidationError(fmt.Sprintf(
				"failed to create output directory %q: %v", auditRedeliverOutDir, mkErr))
		}
	}

	// Resolve signing provider — reuse the same resolution logic as audit:sign.
	providerName, providerCfg := resolveProviderAndConfig()
	signerImpl, createErr := signer.DefaultRegistry.CreateSigner(providerName, providerCfg)
	if createErr != nil {
		return fmt.Errorf("failed to create signer %q: %w", providerName, createErr)
	}
	defer func() {
		if closer, ok := signerImpl.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
	}()

	keyOrigin := signerImpl.KeyOrigin()
	keyOrigin.Provider = providerName

	successes, failures := 0, 0
	var failedPaths []string

	for i, f := range files {
		fmt.Fprintf(cmd.OutOrStdout(), "[%d/%d] %s ... ",
			i+1, len(files), filepath.Base(f.Path))

		signed, reErr := redeliverOne(cmd, f.Entry, signerImpl, providerName, keyOrigin)
		if reErr != nil {
			failures++
			failedPaths = append(failedPaths, f.Path)
			fmt.Fprintf(cmd.OutOrStdout(), "FAILED: %v\n", reErr)
			continue
		}

		// Write or print the signed record.
		if auditRedeliverOutDir != "" {
			outName := strings.TrimSuffix(filepath.Base(f.Path), ".json") + "-signed.json"
			outPath := filepath.Join(auditRedeliverOutDir, outName)
			if writeErr := os.WriteFile(outPath, signed, 0o600); writeErr != nil {
				failures++
				failedPaths = append(failedPaths, f.Path)
				fmt.Fprintf(cmd.OutOrStdout(), "FAILED (write output): %v\n", writeErr)
				continue
			}
			fmt.Fprintf(cmd.OutOrStdout(), "OK → %s\n", outPath)
		} else {
			fmt.Fprintln(cmd.OutOrStdout(), "OK")
			fmt.Fprintln(cmd.OutOrStdout(), string(signed))
		}

		// Remove the dead-letter file — re-sign succeeded.
		if rmErr := os.Remove(f.Path); rmErr != nil {
			fmt.Fprintf(cmd.OutOrStdout(),
				"  Warning: signed successfully but failed to remove dead-letter file %q: %v\n",
				f.Path, rmErr)
		}
		successes++
	}

	// Summary.
	fmt.Fprintln(cmd.OutOrStdout())
	fmt.Fprintf(cmd.OutOrStdout(), "Redeliver summary: %d succeeded, %d failed\n", successes, failures)
	if len(failedPaths) > 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "Failed files (still in dead-letter queue):")
		for _, p := range failedPaths {
			fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", p)
		}
		return fmt.Errorf("%d dead-letter file(s) could not be re-signed", failures)
	}
	return nil
}

// redeliverOne re-signs a single dead-letter entry and returns the signed
// JSON bytes.  It reuses exactly the same hash-input construction as
// audit:sign so the resulting SignedAuditLog is structurally identical.
func redeliverOne(
	cmd *cobra.Command,
	entry signer.DeadLetterEntry,
	signerImpl signer.Signer,
	providerName string,
	keyOrigin signer.KeyOriginMetadata,
) ([]byte, error) {
	// Validate the payload is parseable before attempting to sign.
	if len(entry.Payload) == 0 {
		return nil, fmt.Errorf("dead-letter entry has empty payload")
	}
	var rawPayload interface{}
	if err := json.Unmarshal(entry.Payload, &rawPayload); err != nil {
		return nil, fmt.Errorf("dead-letter payload is not valid JSON: %w", err)
	}

	canonicalPayload, err := marshalCanonical(rawPayload)
	if err != nil {
		return nil, fmt.Errorf("failed to canonicalise payload: %w", err)
	}

	// Build hash input — same structure as runAuditSign.
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
		return nil, fmt.Errorf("failed to marshal hash input: %w", err)
	}

	hash := sha256.Sum256(hashInputBytes)

	// Sign — use SignWithMetadata for KMS so the new attempt is captured.
	var signature []byte
	var newAttempts []signer.SigningAttempt

	if kmsSgn, ok := signerImpl.(*signer.KMSSigner); ok {
		kmsMeta, kmsErr := kmsSgn.SignWithMetadata(cmd.Context(), hash[:], entry.SessionID)
		newAttempts = kmsMeta.SigningAttempts
		if kmsErr != nil {
			return nil, fmt.Errorf("KMS re-sign failed: %w", kmsErr)
		}
		signature = kmsMeta.Signature
	} else {
		var signErr error
		signature, signErr = signerImpl.Sign(hash[:])
		if signErr != nil {
			return nil, fmt.Errorf("re-sign failed: %w", signErr)
		}
	}

	publicKey, err := signerImpl.PublicKey()
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve public key: %w", err)
	}

	// Merge original attempts + new redeliver attempts so the full history is preserved.
	allAttempts := append(entry.SigningAttempts, newAttempts...)

	auditLog := SignedAuditLog{
		Version:         "1.0.0",
		Timestamp:       time.Now().UTC(),
		TraceHash:       hex.EncodeToString(hash[:]),
		Signature:       hex.EncodeToString(signature),
		PublicKey:       hex.EncodeToString(publicKey),
		Provider:        providerName,
		KeyOrigin:       &keyOrigin,
		Payload:         json.RawMessage(entry.Payload),
		SigningAttempts:  allAttempts,
	}

	return json.MarshalIndent(auditLog, "", "  ")
}
