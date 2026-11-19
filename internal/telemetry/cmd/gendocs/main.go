// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"log"
	"os"

	"github.com/dotandev/glassbox/internal/telemetry"
)

func main() {
	// Import the telemetry package to trigger init() and register all events
	_ = telemetry.List()

	// Generate documentation
	docsPath := "docs/telemetry-events.md"
	if len(os.Args) > 1 {
		docsPath = os.Args[1]
	}

	if err := telemetry.WriteDocsToFile(docsPath); err != nil {
		log.Fatalf("Failed to write docs: %v", err)
	}

	fmt.Printf("Documentation generated successfully: %s\n", docsPath)
}
