// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package sourcemap

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// RevisionProvenance records both the revision used in a source URL and where
// that revision came from. Keeping this alongside a link makes replay reports
// auditable: a branch name is not an immutable description of source code.
type RevisionProvenance struct {
	Revision string `json:"revision,omitempty"`
	Source   string `json:"source"` // manifest, session, repository, override, dirty, unknown
	Dirty    bool   `json:"dirty,omitempty"`
}

// Label is suitable for displaying beside a generated source link.
func (p RevisionProvenance) Label() string {
	if p.Dirty {
		return "dirty working tree"
	}
	if p.Revision == "" {
		return "revision unknown"
	}
	if p.Source == "" {
		return p.Revision
	}
	return p.Source + ": " + p.Revision
}

// Immutable reports whether Revision is a git object ID rather than a mutable
// branch or tag name. Both SHA-1 and SHA-256 git object IDs are supported.
func (p RevisionProvenance) Immutable() bool { return isCommitSHA(p.Revision) && !p.Dirty }

// RevisionOptions provides replay metadata in precedence order. A manifest is
// the artifact that was replayed, followed by the recorded session; an explicit
// override is deliberately last in this list because it is opt-in and should
// never be selected accidentally. Set AllowAmbiguous to intentionally permit a
// branch URL when no immutable revision can be established.
type RevisionOptions struct {
	ManifestRevision string
	SessionRevision  string
	OverrideRevision string
	AllowAmbiguous   bool
}

var commitSHA = regexp.MustCompile(`^[0-9a-fA-F]{40}([0-9a-fA-F]{24})?$`)

func isCommitSHA(revision string) bool { return commitSHA.MatchString(revision) }

func resolveRevision(repoRoot string, opts RevisionOptions) (RevisionProvenance, error) {
	for _, candidate := range []struct{ revision, source string }{
		{opts.ManifestRevision, "manifest"},
		{opts.SessionRevision, "session"},
		{opts.OverrideRevision, "override"},
	} {
		if candidate.revision == "" {
			continue
		}
		if !isCommitSHA(candidate.revision) && !opts.AllowAmbiguous {
			return RevisionProvenance{Revision: candidate.revision, Source: candidate.source}, fmt.Errorf("%s revision %q is not an immutable commit SHA", candidate.source, candidate.revision)
		}
		return RevisionProvenance{Revision: candidate.revision, Source: candidate.source}, nil
	}

	out, err := gitOutput(repoRoot, "rev-parse", "HEAD")
	if err != nil || !isCommitSHA(out) {
		return RevisionProvenance{Source: "unknown"}, fmt.Errorf("cannot determine repository commit: %v", err)
	}
	// A URL at HEAD is misleading when replay consumed uncommitted source. Include
	// untracked files because source maps can point at a newly-created source file.
	status, statusErr := gitOutput(repoRoot, "status", "--porcelain")
	if statusErr != nil || status != "" {
		return RevisionProvenance{Revision: out, Source: "dirty", Dirty: true}, fmt.Errorf("repository working tree is dirty")
	}
	return RevisionProvenance{Revision: out, Source: "repository"}, nil
}

func gitOutput(root string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
