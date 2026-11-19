// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package telemetry

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

func capitalizeFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(string(s[0])) + string(s[1:])
}

// GenerateDocs generates a markdown documentation table from the registry.
func GenerateDocs() string {
	defs := List()
	
	// Sort by name for consistent output
	sort.Slice(defs, func(i, j int) bool {
		return defs[i].Name < defs[j].Name
	})
	
	var builder strings.Builder
	
	builder.WriteString("# Event Registry Documentation\n\n")
	builder.WriteString("This document is auto-generated from the telemetry event registry.\n")
	builder.WriteString("Do not edit manually; regenerate by running `go run ./internal/telemetry/cmd/gendocs/main.go`.\n\n")
	builder.WriteString("## Event Definitions\n\n")
	
	for _, def := range defs {
		builder.WriteString(fmt.Sprintf("### %s (v%d)\n\n", def.Name, def.Version))
		builder.WriteString(fmt.Sprintf("**Owner:** %s  \n", def.Owner))
		builder.WriteString(fmt.Sprintf("**Stability:** %s  \n", capitalizeFirst(string(def.Stability))))
		builder.WriteString(fmt.Sprintf("**Sensitivity:** %s  \n", capitalizeFirst(string(def.Sensitivity))))
		builder.WriteString(fmt.Sprintf("**Retention:** %s  \n\n", capitalizeFirst(string(def.Retention))))
		
		if def.Description != "" {
			builder.WriteString(def.Description + "\n\n")
		}
		
		if def.DeprecatedVersion > 0 {
			builder.WriteString(fmt.Sprintf("**Deprecated:** Version %d", def.DeprecatedVersion))
			if def.DeprecatedBy != "" {
				builder.WriteString(fmt.Sprintf(" (replaced by `%s`)", def.DeprecatedBy))
			}
			builder.WriteString("\n\n")
		}
		
		builder.WriteString("#### Fields\n\n")
		builder.WriteString("| Name | Type | Required | Sensitive | Enum |\n")
		builder.WriteString("|------|------|----------|-----------|------|\n")
		
		for _, field := range def.Fields {
			sensitive := "No"
			if field.Sensitive {
				sensitive = "Yes"
			}
			enumVal := ""
			if len(field.Enum) > 0 {
				enumVal = strings.Join(field.Enum, ", ")
			}
			required := "No"
			if field.Required {
				required = "Yes"
			}
			builder.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s |\n",
				field.Name, field.Type, required, sensitive, enumVal))
		}
		
		builder.WriteString("\n")
		
		if len(def.MigrationRules) > 0 {
			builder.WriteString("#### Migration Rules\n\n")
			for _, rule := range def.MigrationRules {
				builder.WriteString(fmt.Sprintf("- **v%d → v%d:** %s\n",
					rule.FromVersion, rule.ToVersion, rule.Transform))
				if len(rule.FieldMap) > 0 {
					builder.WriteString("  Field mappings:\n")
					for old, new := range rule.FieldMap {
						builder.WriteString(fmt.Sprintf("  - `%s` → `%s`\n", old, new))
					}
				}
			}
			builder.WriteString("\n")
		}
		
		builder.WriteString("---\n\n")
	}
	
	return builder.String()
}

// WriteDocsToFile writes the generated documentation to a file.
func WriteDocsToFile(path string) error {
	docs := GenerateDocs()
	return os.WriteFile(path, []byte(docs), 0644)
}
