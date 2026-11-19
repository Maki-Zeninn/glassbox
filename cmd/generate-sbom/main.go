// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// generate-sbom produces a versioned SPDX 2.3 JSON Software Bill of Materials
// for a Glassbox release by merging dependency information from all three
// ecosystems used in the repository (Go modules, Cargo crates, npm packages).
//
// Usage:
//
//	generate-sbom \
//	  --version      v1.2.3 \
//	  --commit       <full-git-sha> \
//	  --go-modules   go-modules.json \
//	  --cargo-lock   simulator/Cargo.lock \
//	  --package-lock package-lock.json \
//	  --output       dist/release/glassbox-v1.2.3.spdx.json
//
// The go-modules.json file must be produced beforehand:
//
//	go list -m -json all > go-modules.json
//
// All three ecosystem inputs are optional — the tool will warn and skip
// any that are missing, as long as at least one is provided.
//
// The output filename is printed to stdout on success so it can be captured
// in shell scripts and forwarded as --sbom-ref to generate-release-manifest.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dotandev/glassbox/internal/sbom"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

type flags struct {
	version        string
	commit         string
	toolVersion    string
	goModulesPath  string
	cargoLockPath  string
	packageLockPath string
	output         string
	verify         bool
	jsonOnly       bool
}

func run(args []string) error {
	fs := flag.NewFlagSet("generate-sbom", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var f flags
	fs.StringVar(&f.version, "version", "", "Release version string (e.g. v1.2.3) [required]")
	fs.StringVar(&f.commit, "commit", "", "Full git commit SHA [required]")
	fs.StringVar(&f.toolVersion, "tool-version", "", "Glassbox tool version embedded in SBOM creator field (defaults to --version)")
	fs.StringVar(&f.goModulesPath, "go-modules", "", "Path to go list -m -json all output file (optional)")
	fs.StringVar(&f.cargoLockPath, "cargo-lock", "", "Path to Cargo.lock (optional)")
	fs.StringVar(&f.packageLockPath, "package-lock", "", "Path to package-lock.json (optional)")
	fs.StringVar(&f.output, "output", "", "Write SBOM to this file (stdout when omitted)")
	fs.BoolVar(&f.verify, "verify", false, "Validate the SBOM after generation (default: true when --output is set)")
	fs.BoolVar(&f.jsonOnly, "json-only", false, "Suppress informational messages; write only the SPDX JSON")

	if err := fs.Parse(args); err != nil {
		return err
	}

	// Validate required flags.
	var missing []string
	if f.version == "" {
		missing = append(missing, "--version")
	}
	if f.commit == "" {
		missing = append(missing, "--commit")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required flag(s): %s", strings.Join(missing, ", "))
	}

	// At least one ecosystem input must be provided.
	if f.goModulesPath == "" && f.cargoLockPath == "" && f.packageLockPath == "" {
		return errors.New("at least one of --go-modules, --cargo-lock, or --package-lock must be provided")
	}

	toolVer := f.toolVersion
	if toolVer == "" {
		toolVer = f.version
	}

	logf := func(format string, a ...interface{}) {
		if !f.jsonOnly {
			fmt.Fprintf(os.Stderr, format+"\n", a...)
		}
	}

	logf("Generating SBOM for %s (commit %s) ...", f.version, shortSHA(f.commit))
	if f.goModulesPath != "" {
		logf("  Go modules:    %s", f.goModulesPath)
	}
	if f.cargoLockPath != "" {
		logf("  Cargo.lock:    %s", f.cargoLockPath)
	}
	if f.packageLockPath != "" {
		logf("  package-lock:  %s", f.packageLockPath)
	}

	result, err := sbom.GenerateFromFiles(sbom.GenerateOptions{
		GoModulesJSON:   f.goModulesPath,
		CargoLockPath:   f.cargoLockPath,
		PackageLockPath: f.packageLockPath,
		Context: sbom.BuildContext{
			GeneratedAt:     time.Now().UTC(),
			ToolVersion:     toolVer,
			ReleaseVersion:  f.version,
			Commit:          f.commit,
			GoModPath:       f.goModulesPath,
			CargoLockPath:   f.cargoLockPath,
			PackageLockPath: f.packageLockPath,
		},
	})
	if err != nil {
		return fmt.Errorf("generating SBOM: %w", err)
	}

	// Report any non-fatal warnings.
	for _, w := range result.Warnings {
		logf("  [WARN] %s", w)
	}

	// Summary of component counts.
	total := 0
	for _, n := range result.ComponentCounts {
		total += n
	}
	logf("  Components: %d total (go=%d cargo=%d npm=%d)",
		total,
		result.ComponentCounts[sbom.EcosystemGo],
		result.ComponentCounts[sbom.EcosystemCargo],
		result.ComponentCounts[sbom.EcosystemNPM],
	)
	logf("  Document hash: %s", shortHex(result.Document.DocumentHash))

	// Validate when requested (default on when writing to a file).
	if f.verify || f.output != "" {
		if err := sbom.Validate(result.Document); err != nil {
			return fmt.Errorf("SBOM validation failed: %w", err)
		}
		logf("  [PASS] SPDX document validated")
	}

	// Serialize.
	data, err := sbom.Marshal(result.Document)
	if err != nil {
		return fmt.Errorf("serialising SBOM: %w", err)
	}

	// Write output.
	if f.output != "" {
		// Ensure the output directory exists.
		if dir := filepath.Dir(f.output); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return fmt.Errorf("creating output directory %s: %w", dir, err)
			}
		}
		if err := os.WriteFile(f.output, data, 0644); err != nil {
			return fmt.Errorf("writing SBOM to %s: %w", f.output, err)
		}
		logf("SBOM written to %s", f.output)

		// Verify the written file is valid JSON and re-parses correctly.
		if f.verify || true {
			raw, readErr := os.ReadFile(f.output)
			if readErr != nil {
				return fmt.Errorf("re-reading SBOM for verification: %w", readErr)
			}
			var check sbom.SPDXDocument
			if jsonErr := json.Unmarshal(raw, &check); jsonErr != nil {
				return fmt.Errorf("written SBOM is not valid JSON: %w", jsonErr)
			}
			if err := sbom.Validate(&check); err != nil {
				return fmt.Errorf("re-validation of written SBOM failed: %w", err)
			}
			logf("  [PASS] Written SBOM re-validated successfully")
		}

		// Print just the output filename to stdout so shell scripts can capture it.
		fmt.Println(f.output)
	} else {
		if _, err := os.Stdout.Write(data); err != nil {
			return fmt.Errorf("writing SBOM to stdout: %w", err)
		}
	}

	return nil
}

// shortSHA returns the first 8 characters of a SHA, or the full string if shorter.
func shortSHA(sha string) string {
	if len(sha) > 8 {
		return sha[:8] + "…"
	}
	return sha
}

// shortHex returns first 8 + "…" + last 8 of a hex string.
func shortHex(h string) string {
	if len(h) <= 16 {
		return h
	}
	return h[:8] + "…" + h[len(h)-8:]
}
