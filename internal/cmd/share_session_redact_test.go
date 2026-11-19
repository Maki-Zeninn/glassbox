// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Tests for Issue #561: session sharing redaction CLI wiring.

package cmd

import (
	"bytes"
	"testing"

	"github.com/dotandev/glassbox/internal/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionShareCmd_HasRedactAndPreviewFlags(t *testing.T) {
	require.NotNil(t, sessionShareCmd.Flags().Lookup("redact"))
	require.NotNil(t, sessionShareCmd.Flags().Lookup("preview"))
	assert.Equal(t, "full", sessionShareCmd.Flags().Lookup("redact").DefValue,
		"default redact profile must be 'full' so existing scripts keep today's unredacted behavior")
}

func TestPrintRedactionReport_ListsAppliedAndUnappliedFields(t *testing.T) {
	report := &session.RedactionReport{
		Profile: session.RedactionStrict,
		Fields: []session.RedactionFieldReport{
			{Field: session.FieldEnvMetadata, Policy: session.PolicyRedact, Applied: true, Sample: "https://horizon-testnet.stellar.org"},
			{Field: session.FieldContractArgs, Policy: session.PolicyRedact, Applied: false},
			{Field: session.FieldAccountIdentifiers, Policy: session.PolicyPseudonymize, Applied: true, Sample: "2 identifier(s) pseudonymized"},
		},
		IdentifiersPseudonymized: 2,
	}

	var buf bytes.Buffer
	printRedactionReport(&buf, report)
	out := buf.String()

	assert.Contains(t, out, "strict")
	assert.Contains(t, out, session.FieldEnvMetadata)
	assert.Contains(t, out, "removed")
	assert.Contains(t, out, "nothing to change")
	assert.Contains(t, out, "pseudonymized")
	assert.Contains(t, out, "2 unique identifier(s) pseudonymized")
}
