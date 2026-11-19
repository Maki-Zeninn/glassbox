// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package session

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/dotandev/glassbox/internal/simulator"
)

// RedactionProfileName selects a predefined redaction policy for sharing a
// session outside its original owner.
type RedactionProfileName string

const (
	// RedactionStrict removes contract arguments and environment metadata
	// outright and pseudonymizes account/contract identifiers. Use this for
	// sharing with parties outside the team.
	RedactionStrict RedactionProfileName = "strict"
	// RedactionBalanced keeps contract arguments and environment metadata
	// but pseudonymizes account/contract identifiers. Use this for sharing
	// within a trusted team that still shouldn't see raw addresses.
	RedactionBalanced RedactionProfileName = "balanced"
	// RedactionFull applies no redaction — the historical, backward
	// compatible behavior of 'glassbox session share'.
	RedactionFull RedactionProfileName = "full"
)

// FieldPolicy is the treatment applied to one logical field class.
type FieldPolicy string

const (
	// PolicyKeep leaves the field class untouched.
	PolicyKeep FieldPolicy = "keep"
	// PolicyRedact removes the field class outright, replacing it with
	// nothing (an empty/omitted value).
	PolicyRedact FieldPolicy = "redact"
	// PolicyPseudonymize replaces identifying values with a deterministic
	// pseudonym, preserving the relationships between repeated identifiers
	// without revealing the original value.
	PolicyPseudonymize FieldPolicy = "pseudonymize"
)

// Logical field classes a RedactionProfile assigns a policy to. These are
// deliberately coarser than individual Data struct fields — a "class"
// groups every field carrying the same kind of sensitive information so a
// profile reads as a short, auditable table.
const (
	// FieldContractArgs covers custom auth config, mock args, and restore
	// preamble embedded in SimRequestJSON — the contract call arguments and
	// mocked execution inputs an issue reporter may not want to disclose.
	FieldContractArgs = "contract_args"
	// FieldEnvMetadata covers EnvFingerprint, HorizonURL, and
	// PinnedEndpoint — details about the environment the session was
	// captured in.
	FieldEnvMetadata = "env_metadata"
	// FieldAccountIdentifiers covers Stellar account (G...) and contract
	// (C...) addresses appearing as literal text inside the session's JSON
	// fields (SimRequestJSON, SimResponseJSON, TraceJSON, BundleJSON,
	// AnnotationsJSON). Stellar secret seeds (S...) are always hard-redacted
	// regardless of this field's policy — a seed must never be shared.
	FieldAccountIdentifiers = "account_identifiers"
)

// RedactionProfile is a named table of field-class policies.
type RedactionProfile struct {
	Name   RedactionProfileName
	Fields map[string]FieldPolicy
}

// FullProfile applies no redaction: every field class is kept. This is the
// default, matching 'glassbox session share' behavior before Issue #561.
func FullProfile() RedactionProfile {
	return RedactionProfile{
		Name: RedactionFull,
		Fields: map[string]FieldPolicy{
			FieldContractArgs:       PolicyKeep,
			FieldEnvMetadata:        PolicyKeep,
			FieldAccountIdentifiers: PolicyKeep,
		},
	}
}

// BalancedProfile pseudonymizes account/contract identifiers but keeps
// contract arguments and environment metadata intact.
func BalancedProfile() RedactionProfile {
	return RedactionProfile{
		Name: RedactionBalanced,
		Fields: map[string]FieldPolicy{
			FieldContractArgs:       PolicyKeep,
			FieldEnvMetadata:        PolicyKeep,
			FieldAccountIdentifiers: PolicyPseudonymize,
		},
	}
}

// StrictProfile removes contract arguments and environment metadata
// outright and pseudonymizes account/contract identifiers.
func StrictProfile() RedactionProfile {
	return RedactionProfile{
		Name: RedactionStrict,
		Fields: map[string]FieldPolicy{
			FieldContractArgs:       PolicyRedact,
			FieldEnvMetadata:        PolicyRedact,
			FieldAccountIdentifiers: PolicyPseudonymize,
		},
	}
}

// ParseRedactionProfile resolves a user-supplied profile name (e.g. from a
// --redact CLI flag). An empty string resolves to RedactionFull so existing
// scripts that don't pass --redact keep today's unredacted behavior.
func ParseRedactionProfile(name string) (RedactionProfile, error) {
	switch RedactionProfileName(strings.ToLower(strings.TrimSpace(name))) {
	case "", RedactionFull:
		return FullProfile(), nil
	case RedactionBalanced:
		return BalancedProfile(), nil
	case RedactionStrict:
		return StrictProfile(), nil
	default:
		return RedactionProfile{}, fmt.Errorf(
			"unknown redaction profile %q — must be one of: strict, balanced, full",
			name,
		)
	}
}

// RedactionFieldReport describes what happened to one field class during a
// RedactSession call.
type RedactionFieldReport struct {
	Field   string
	Policy  FieldPolicy
	Applied bool // true when this field class actually contained something the policy changed
	Sample  string
}

// RedactionReport previews or summarizes a RedactSession call. Rendering it
// to the user before writing an archive satisfies the requirement that a
// redacted export be reviewable before it leaves the machine.
type RedactionReport struct {
	Profile                  RedactionProfileName
	Fields                   []RedactionFieldReport
	IdentifiersPseudonymized int
}

// accountIdentifierPattern matches Stellar strkey-encoded account addresses
// (G...), contract addresses (C...), and secret seeds (S...) appearing as
// literal text — this only ever matches inside JSON text fields
// (SimRequestJSON, SimResponseJSON, TraceJSON, BundleJSON, AnnotationsJSON).
// XDR fields are binary-then-base64, so an address embedded there is never
// present as this literal substring and is intentionally not scanned.
var accountIdentifierPattern = regexp.MustCompile(`\b([GCS])[A-Z2-7]{55}\b`)

// pseudonymMapper assigns deterministic pseudonyms to identifiers: the same
// identifier always maps to the same pseudonym within one RedactSession
// call, and — since the mapping is a pure function of the session ID and
// the identifier, with no state to persist — across repeated exports of the
// same session too.
type pseudonymMapper struct {
	key   []byte
	cache map[string]string
	count int
}

func newPseudonymMapper(sessionID string) *pseudonymMapper {
	sum := sha256.Sum256([]byte(sessionID))
	return &pseudonymMapper{key: sum[:], cache: make(map[string]string)}
}

func (m *pseudonymMapper) pseudonym(prefix, identifier string) string {
	if p, ok := m.cache[identifier]; ok {
		return p
	}
	mac := hmac.New(sha256.New, m.key)
	mac.Write([]byte(identifier))
	p := fmt.Sprintf("%s_%s", prefix, hex.EncodeToString(mac.Sum(nil))[:8])
	m.cache[identifier] = p
	m.count++
	return p
}

// transform replaces every account/contract identifier in s with its
// pseudonym. Secret seeds are always hard-redacted rather than
// pseudonymized: a seed grants signing authority, so even a stable
// pseudonym for it would be a liability if the mapping were ever recovered.
func (m *pseudonymMapper) transform(s string) string {
	if s == "" {
		return s
	}
	return accountIdentifierPattern.ReplaceAllStringFunc(s, func(match string) string {
		switch match[0] {
		case 'S':
			return "[REDACTED]"
		case 'G':
			return m.pseudonym("ACCOUNT", match)
		case 'C':
			return m.pseudonym("CONTRACT", match)
		default:
			return match
		}
	})
}

// jsonTextFields are the Data fields account-identifier scanning applies to.
func jsonTextFieldsOf(d *Data) []*string {
	return []*string{
		&d.SimRequestJSON, &d.SimResponseJSON,
		&d.TraceJSON, &d.BundleJSON, &d.AnnotationsJSON,
	}
}

// RedactSession applies profile to a copy of data and returns the redacted
// copy together with a report describing what changed. data is never
// mutated. TxHash, Network, Status, and SchemaVersion are never touched —
// they are structural fields ValidateIntegrity requires and are not
// considered sensitive in the sense this function addresses.
//
// If data is encrypted (EncryptedPayload != nil), the sensitive fields
// RedactSession inspects are already empty on data — decrypt with
// DecryptSessionPayload first so there is plaintext to redact.
func RedactSession(data *Data, profile RedactionProfile) (*Data, *RedactionReport, error) {
	if data == nil {
		return nil, nil, fmt.Errorf("session data is nil")
	}

	redacted := *data
	report := &RedactionReport{Profile: profile.Name}

	// Environment metadata.
	envPolicy := profile.Fields[FieldEnvMetadata]
	envField := RedactionFieldReport{Field: FieldEnvMetadata, Policy: envPolicy}
	if envPolicy == PolicyRedact {
		if redacted.EnvFingerprint != "" || redacted.HorizonURL != "" || redacted.PinnedEndpoint != "" {
			envField.Applied = true
			envField.Sample = firstNonEmpty(redacted.HorizonURL, redacted.PinnedEndpoint, redacted.EnvFingerprint)
		}
		redacted.EnvFingerprint = ""
		redacted.HorizonURL = ""
		redacted.PinnedEndpoint = ""
	}
	report.Fields = append(report.Fields, envField)

	// Contract arguments embedded in SimRequestJSON.
	caPolicy := profile.Fields[FieldContractArgs]
	caField := RedactionFieldReport{Field: FieldContractArgs, Policy: caPolicy}
	if caPolicy == PolicyRedact && redacted.SimRequestJSON != "" {
		var req simulator.SimulationRequest
		if err := json.Unmarshal([]byte(redacted.SimRequestJSON), &req); err != nil {
			return nil, nil, fmt.Errorf("redact: sim_request_json is not valid JSON: %w", err)
		}
		if len(req.CustomAuthCfg) > 0 || (req.MockArgs != nil && len(*req.MockArgs) > 0) || len(req.RestorePreamble) > 0 {
			caField.Applied = true
			caField.Sample = "custom_auth_config, mock_args, restore_preamble"
		}
		req.CustomAuthCfg = nil
		req.MockArgs = nil
		req.RestorePreamble = nil
		out, err := json.Marshal(req)
		if err != nil {
			return nil, nil, fmt.Errorf("redact: failed to re-marshal sim_request_json: %w", err)
		}
		redacted.SimRequestJSON = string(out)
	}
	report.Fields = append(report.Fields, caField)

	// Account/contract identifiers.
	aiPolicy := profile.Fields[FieldAccountIdentifiers]
	aiField := RedactionFieldReport{Field: FieldAccountIdentifiers, Policy: aiPolicy}
	if aiPolicy == PolicyPseudonymize {
		mapper := newPseudonymMapper(data.ID)
		for _, f := range jsonTextFieldsOf(&redacted) {
			*f = mapper.transform(*f)
		}
		if mapper.count > 0 {
			aiField.Applied = true
			aiField.Sample = fmt.Sprintf("%d identifier(s) pseudonymized", mapper.count)
		}
		report.IdentifiersPseudonymized = mapper.count
	}
	report.Fields = append(report.Fields, aiField)

	return &redacted, report, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
