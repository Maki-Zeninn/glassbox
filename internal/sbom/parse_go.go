// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package sbom

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// goModule is the subset of fields we use from `go list -m -json all` output.
// Each invocation of `go list -m -json all` produces a stream of JSON objects
// (one per module), not a JSON array, so we must decode them incrementally.
type goModule struct {
	Path    string `json:"Path"`
	Version string `json:"Version"`
	Main    bool   `json:"Main"`
	// GoMod is the absolute path to the go.mod file; unused here but kept for
	// completeness.
	GoMod string `json:"GoMod,omitempty"`
}

// ParseGoModules reads the JSON stream produced by `go list -m -json all` from
// r and returns a Component for every non-main module.
//
// The input format is a sequence of JSON objects (not an array):
//
//	{ "Path": "github.com/foo/bar", "Version": "v1.2.3", ... }
//	{ "Path": "github.com/baz/qux", "Version": "v0.1.0", ... }
//
// Modules with Main=true (the root module) are excluded because they are not
// transitive dependencies.
//
// Returns an error when the input is malformed, with the byte offset of the
// first bad token included in the message.
func ParseGoModules(r io.Reader) ([]Component, error) {
	dec := json.NewDecoder(bufio.NewReader(r))
	var components []Component

	for {
		var m goModule
		if err := dec.Decode(&m); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("go module JSON parse error at byte %d: %w", dec.InputOffset(), err)
		}

		// Skip the root module — it is the subject of the SBOM, not a dependency.
		if m.Main {
			continue
		}
		// Skip pseudo-versions that carry no release tag (they will have an
		// empty Version field when the module is replaced by a local path).
		if m.Version == "" {
			continue
		}

		name := strings.TrimSpace(m.Path)
		version := strings.TrimSpace(m.Version)

		components = append(components, Component{
			Ecosystem: EcosystemGo,
			Name:      name,
			Version:   version,
			PURL:      BuildPURL(EcosystemGo, name, version),
			SPDXID:    BuildSPDXID(EcosystemGo, name, version),
		})
	}

	return components, nil
}
