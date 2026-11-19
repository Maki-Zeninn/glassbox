// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// tags.go — Structured, filterable session tags [Issue #1060].
//
// Tags are short, operator-assigned labels stored in Data.TagsJSON as a JSON
// array of strings. They are distinct from free-text analyst notes
// (AnnotationsJSON) and are designed for programmatic filtering:
//
//	glassbox session list --tag auth-bug
//	glassbox session list --tag mainnet --tag priority-high
//
// # Constraints
//
//   - Each tag is 1–64 characters, lowercase alphanumeric, hyphens, underscores,
//     and forward slashes (for namespace prefixes like "team/backend").
//   - A session may carry at most MaxTags (32) tags.
//   - Duplicate tags are silently deduplicated; order is preserved on first add.
//
// Tags do NOT participate in the session's AuditHash — they are operational
// metadata and must not alter cryptographic evidence of the simulation result.
package session

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

const (
	// MaxTags is the maximum number of tags a single session may carry.
	MaxTags = 32
	// MaxTagLength is the maximum byte length of a single tag.
	MaxTagLength = 64
)

// tagPattern accepts lowercase alphanumeric, hyphens, underscores, and a
// single forward slash for namespace prefixes (e.g. "team/backend").
// A tag must not be empty, must not start or end with a separator, and must
// not contain consecutive separators.
var tagPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9\-_/]*[a-z0-9]$|^[a-z0-9]$`)

// ValidateTag returns an error if tag does not conform to the tag format rules.
func ValidateTag(tag string) error {
	if tag == "" {
		return fmt.Errorf("tag must not be empty")
	}
	if len(tag) > MaxTagLength {
		return fmt.Errorf("tag %q is too long (%d bytes, max %d)", tag, len(tag), MaxTagLength)
	}
	if !tagPattern.MatchString(tag) {
		return fmt.Errorf(
			"tag %q contains invalid characters — tags must be lowercase alphanumeric "+
				"with hyphens, underscores, or a single namespace prefix (e.g. \"team/backend\")",
			tag,
		)
	}
	return nil
}

// Tags returns the tag slice stored in data.TagsJSON.
// Never returns nil; returns an empty slice for sessions with no tags.
func Tags(data *Data) ([]string, error) {
	if data == nil {
		return []string{}, nil
	}
	raw := data.TagsJSON
	if raw == "" {
		return []string{}, nil
	}
	var tags []string
	if err := json.Unmarshal([]byte(raw), &tags); err != nil {
		return nil, fmt.Errorf("failed to parse session tags: %w\n"+
			"  The tags_json field may be corrupt. "+
			"Run 'glassbox session doctor' for diagnostics.", err)
	}
	if tags == nil {
		return []string{}, nil
	}
	return tags, nil
}

// SetTags replaces the tag list on data with tags, after validating every
// entry and deduplicating (preserving first-occurrence order).
//
// An empty slice or nil clears all tags. Returns a validation error when any
// tag is malformed or the deduplicated count exceeds MaxTags.
func SetTags(data *Data, tags []string) error {
	if data == nil {
		return fmt.Errorf("cannot set tags on nil session data")
	}
	if len(tags) == 0 {
		data.TagsJSON = "[]"
		return nil
	}

	// Validate and deduplicate, preserving first-occurrence order.
	seen := make(map[string]struct{}, len(tags))
	deduped := make([]string, 0, len(tags))
	for _, tag := range tags {
		normalized := strings.ToLower(strings.TrimSpace(tag))
		if err := ValidateTag(normalized); err != nil {
			return err
		}
		if _, exists := seen[normalized]; !exists {
			seen[normalized] = struct{}{}
			deduped = append(deduped, normalized)
		}
	}

	if len(deduped) > MaxTags {
		return fmt.Errorf(
			"too many tags (%d, max %d)\n"+
				"  Fix: remove some tags before adding new ones with "+
				"'glassbox session tag remove --id <session-id> --tag <tag>'",
			len(deduped), MaxTags,
		)
	}

	b, err := json.Marshal(deduped)
	if err != nil {
		return fmt.Errorf("failed to marshal tags: %w", err)
	}
	data.TagsJSON = string(b)
	return nil
}

// AddTag appends tag to the session's tag list if it is not already present.
// Returns an error when the tag is invalid or the limit would be exceeded.
func AddTag(data *Data, tag string) error {
	if data == nil {
		return fmt.Errorf("cannot add tag to nil session data")
	}
	normalized := strings.ToLower(strings.TrimSpace(tag))
	if err := ValidateTag(normalized); err != nil {
		return err
	}

	existing, err := Tags(data)
	if err != nil {
		return err
	}

	// Idempotent: already present.
	for _, t := range existing {
		if t == normalized {
			return nil
		}
	}

	if len(existing) >= MaxTags {
		return fmt.Errorf(
			"session already has %d tags (max %d)\n"+
				"  Fix: remove an existing tag first with "+
				"'glassbox session tag remove --id <session-id> --tag <tag>'",
			len(existing), MaxTags,
		)
	}

	return SetTags(data, append(existing, normalized))
}

// RemoveTag removes tag from the session's tag list.
// Returns an error when the tag is not present.
func RemoveTag(data *Data, tag string) error {
	if data == nil {
		return fmt.Errorf("cannot remove tag from nil session data")
	}
	normalized := strings.ToLower(strings.TrimSpace(tag))

	existing, err := Tags(data)
	if err != nil {
		return err
	}

	filtered := existing[:0]
	found := false
	for _, t := range existing {
		if t == normalized {
			found = true
			continue
		}
		filtered = append(filtered, t)
	}

	if !found {
		return fmt.Errorf(
			"tag %q not found on this session\n"+
				"  Run 'glassbox session tag list --id <session-id>' to see current tags",
			normalized,
		)
	}

	return SetTags(data, filtered)
}

// HasTag reports whether the session carries tag (case-insensitive).
func HasTag(data *Data, tag string) bool {
	if data == nil {
		return false
	}
	normalized := strings.ToLower(strings.TrimSpace(tag))
	tags, err := Tags(data)
	if err != nil {
		return false
	}
	for _, t := range tags {
		if t == normalized {
			return true
		}
	}
	return false
}
