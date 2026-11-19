// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

//go:build ignore

// generate_schema_main.go is a standalone generator that writes the versioned
// TypeScript command-schema artifacts to disk.
//
// Usage:
//
//	go run internal/bindings/cmd/generate_schema_main.go
//	go run internal/bindings/cmd/generate_schema_main.go --output ./src/bindings
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dotandev/glassbox/internal/bindings"
)

func main() {
	outputDir := flag.String("output", "src/bindings", "Output directory for generated TypeScript files")
	flag.Parse()

	schema := bindings.GenerateCommandSchema()
	files := map[string]string{
		"command-schema.ts":     bindings.GenerateCommandSchemaTS(schema),
		"command-types.ts":      bindings.GenerateCommandTypesTS(schema),
		"command-validators.ts": bindings.GenerateCommandValidatorsTS(schema),
		"index.ts":              bindings.GenerateCommandSchemaIndexTS(schema),
	}

	if err := os.MkdirAll(*outputDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir %s: %v\n", *outputDir, err)
		os.Exit(1)
	}

	for name, content := range files {
		path := filepath.Join(*outputDir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "write %s: %v\n", path, err)
			os.Exit(1)
		}
		fmt.Printf("wrote %s\n", path)
	}
	fmt.Printf("schema version: %s  (glassbox %s)\n", schema.SchemaVersion, schema.GlassboxVersion)
}
