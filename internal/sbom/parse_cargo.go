// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package sbom

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// ParseCargoLock reads a Cargo.lock file (v1, v2, or v3 / v4 format) and
// returns a Component for every [[package]] entry that is not the workspace
// root.
//
// Cargo.lock is a subset of TOML, but the [[package]] tables it uses are
// simple enough to parse with a hand-written line scanner without pulling in a
// full TOML library — keeping this package dependency-free.
//
// Supported fields extracted per package:
//
//	name        → Component.Name
//	version     → Component.Version
//	checksum    → Component.Checksum  (hex portion after "sha256:")
//
// Packages with no "source" field are local workspace members and are skipped.
func ParseCargoLock(r io.Reader) ([]Component, error) {
	scanner := bufio.NewScanner(r)

	type record struct {
		name     string
		version  string
		source   string
		checksum string
	}

	var (
		records  []record
		current  *record
		inPkg    bool
		lineNum  int
	)

	flush := func() {
		if current != nil && current.name != "" && current.version != "" {
			// Skip local workspace members (no source = not from crates.io or git).
			if current.source != "" {
				records = append(records, *current)
			}
			current = nil
		}
		inPkg = false
	}

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		if line == "[[package]]" {
			flush()
			current = &record{}
			inPkg = true
			continue
		}

		// A blank line or a new section header ends the current package block.
		if inPkg && (line == "" || (strings.HasPrefix(line, "[") && !strings.HasPrefix(line, "[[package]]"))) {
			flush()
			continue
		}

		if !inPkg || current == nil {
			continue
		}

		key, value, ok := parseTomlKV(line)
		if !ok {
			continue
		}

		switch key {
		case "name":
			current.name = value
		case "version":
			current.version = value
		case "source":
			current.source = value
		case "checksum":
			// Cargo stores checksums as "sha256:<hex>"; strip the prefix.
			if strings.HasPrefix(value, "sha256:") {
				current.checksum = value[7:]
			} else {
				current.checksum = value
			}
		}
	}
	flush() // flush the last package

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading Cargo.lock: %w", err)
	}

	components := make([]Component, 0, len(records))
	for _, rec := range records {
		name := rec.name
		version := rec.version
		components = append(components, Component{
			Ecosystem: EcosystemCargo,
			Name:      name,
			Version:   version,
			PURL:      BuildPURL(EcosystemCargo, name, version),
			SPDXID:    BuildSPDXID(EcosystemCargo, name, version),
			Checksum:  rec.checksum,
		})
	}
	return components, nil
}

// parseTomlKV parses a single TOML key = "value" or key = value line,
// returning the unquoted key and unquoted string value. It handles the single
// subset of TOML that Cargo.lock actually uses: bare keys, string values in
// double quotes, and bare version/source strings.
//
// Returns ok=false for comments, section headers, blank lines, or anything
// that doesn't match the expected form.
func parseTomlKV(line string) (key, value string, ok bool) {
	// Skip comments and blank lines.
	if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
		return "", "", false
	}

	idx := strings.IndexByte(line, '=')
	if idx < 0 {
		return "", "", false
	}

	key = strings.TrimSpace(line[:idx])
	raw := strings.TrimSpace(line[idx+1:])

	// Unquote double-quoted string.
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		value = raw[1 : len(raw)-1]
	} else {
		value = raw
	}

	return key, value, key != ""
}
