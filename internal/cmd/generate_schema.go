// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/dotandev/glassbox/internal/bindings"
	"github.com/dotandev/glassbox/internal/clioutput"
	"github.com/spf13/cobra"
)

var generateSchemaOutputDir string
var generateSchemaJSONFlag bool

// generateSchemaCmd writes the versioned TypeScript command-schema artifacts
// to an output directory.  It is registered as `glassbox generate-schema`.
var generateSchemaCmd = &cobra.Command{
	Use:   "generate-schema",
	Short: "Write versioned TypeScript command-schema bindings to disk",
	Long: `Write canonical TypeScript command-schema artifacts to an output directory.

Generated files:
  command-schema.ts     – Full schema as a typed const (interfaces + runtime data)
  command-types.ts      – Per-command input/output TypeScript interfaces
  command-validators.ts – Validation helpers with field-path diagnostics
  index.ts              – Barrel export

These files are the single source of truth for TypeScript consumers.
Commit them to the repository and detect manual drift in CI with:

  git diff --exit-code src/bindings/

Examples:
  glassbox generate-schema --output ./src/bindings
  glassbox generate-schema --output ./src/bindings --json`,
	RunE: runGenerateSchema,
}

func init() {
	generateSchemaCmd.Flags().StringVarP(
		&generateSchemaOutputDir, "output", "o", "./src/bindings",
		"Directory to write generated TypeScript schema files",
	)
	generateSchemaCmd.Flags().BoolVar(
		&generateSchemaJSONFlag, "json", false,
		"Emit machine-readable JSON output",
	)
}

type generateSchemaOutput struct {
	Output        string   `json:"output"`
	Files         []string `json:"files"`
	SchemaVersion string   `json:"schema_version"`
	GlassboxVersion string `json:"glassbox_version"`
}

func runGenerateSchema(cmd *cobra.Command, _ []string) error {
	schema := bindings.GenerateCommandSchema()

	files := map[string]string{
		"command-schema.ts":     bindings.GenerateCommandSchemaTS(schema),
		"command-types.ts":      bindings.GenerateCommandTypesTS(schema),
		"command-validators.ts": bindings.GenerateCommandValidatorsTS(schema),
		"index.ts":              bindings.GenerateCommandSchemaIndexTS(schema),
	}

	if err := os.MkdirAll(generateSchemaOutputDir, 0o755); err != nil {
		return fmt.Errorf("creating output directory %q: %w", generateSchemaOutputDir, err)
	}

	written := make([]string, 0, len(files))
	for name, content := range files {
		path := filepath.Join(generateSchemaOutputDir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", name, err)
		}
		written = append(written, path)
	}

	if generateSchemaJSONFlag {
		out := generateSchemaOutput{
			Output:          generateSchemaOutputDir,
			Files:           written,
			SchemaVersion:   schema.SchemaVersion,
			GlassboxVersion: schema.GlassboxVersion,
		}
		return clioutput.Write(cmd.OutOrStdout(), "generate-schema", out)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Schema version: %s  (glassbox %s)\n",
		schema.SchemaVersion, schema.GlassboxVersion)
	for _, f := range written {
		fmt.Fprintf(cmd.OutOrStdout(), "  wrote  %s\n", f)
	}
	return nil
}
