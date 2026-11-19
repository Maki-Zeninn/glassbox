// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// session_contract_test.go is the CLI-layer half of the Issue #567 fixture
// contract suite (see internal/session/contract_test.go for the library
// layer). It drives the same testhelpers.SessionFixture classes through the
// session CLI commands (doctor, resume, save) and asserts that the Field
// names the CLI surfaces to the user are exactly the ones
// session.ValidateIntegrity itself reports for the same fixture — proving
// the "CLI and library behavior agree" acceptance criterion rather than
// just checking a hardcoded substring.
package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/dotandev/glassbox/internal/session"
	"github.com/dotandev/glassbox/internal/testhelpers"
)

// wantIssueFields returns the distinct set of IntegrityIssue.Field values
// session.ValidateIntegrity reports for data.
func wantIssueFields(data *session.Data) []string {
	report := session.ValidateIntegrity(data)
	fields := make([]string, 0, len(report.Issues))
	seen := map[string]bool{}
	for _, issue := range report.Issues {
		if !seen[issue.Field] {
			seen[issue.Field] = true
			fields = append(fields, issue.Field)
		}
	}
	return fields
}

func TestContractCLI_Doctor_ReportsSameFieldsAsLibrary(t *testing.T) {
	// Failure class: 'glassbox session doctor' must surface the exact same
	// IntegrityIssue.Field names that session.ValidateIntegrity computes for
	// a corrupt fixture — not a generic "session is broken" message.
	overrideHome(t)

	data := testhelpers.NewSessionFixture().WithID("contract-cli-doctor-1").CorruptAuditHash().Build()
	wantFields := wantIssueFields(data)
	if len(wantFields) == 0 {
		t.Fatal("test fixture must fail integrity for this test to be meaningful")
	}

	store, err := session.NewStore()
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()
	if err := store.Save(context.Background(), data); err != nil {
		t.Fatalf("save: %v", err)
	}

	var out bytes.Buffer
	sessionDoctorCmd.SetOut(&out)
	sessionDoctorCmd.SetErr(&out)

	runErr := sessionDoctorCmd.RunE(sessionDoctorCmd, nil)
	if runErr == nil {
		t.Fatal("expected 'session doctor' to return an error for a degraded session")
	}

	rendered := out.String()
	for _, field := range wantFields {
		if !strings.Contains(rendered, "["+field+"]") {
			t.Errorf("doctor output missing field %q (library reports it); output:\n%s", field, rendered)
		}
	}
}

func TestContractCLI_Save_RejectsWithLibraryFieldName(t *testing.T) {
	// Failure class: 'glassbox session save' must reject an invalid
	// audit-chain session using the same Field name ValidateIntegrity would
	// report, not a raw SQL error or a differently-worded message.
	overrideHome(t)
	prevID, prevName, prevPin := sessionIDFlag, sessionNameFlag, sessionPinEndpointFlag
	prevCurrent := GetCurrentSession()
	t.Cleanup(func() {
		sessionIDFlag = prevID
		sessionNameFlag = prevName
		sessionPinEndpointFlag = prevPin
		SetCurrentSession(prevCurrent)
	})

	data := testhelpers.NewSessionFixture().OrphanedAuditSignature().Build()
	wantFields := wantIssueFields(data)
	if len(wantFields) == 0 {
		t.Fatal("test fixture must fail integrity for this test to be meaningful")
	}
	SetCurrentSession(data)

	var out bytes.Buffer
	sessionSaveCmd.SetOut(&out)
	sessionSaveCmd.SetErr(&out)

	err := sessionSaveCmd.RunE(sessionSaveCmd, []string{})
	if err == nil {
		t.Fatal("expected session save to reject an invalid audit-chain session")
	}
	msg := err.Error()
	for _, field := range wantFields {
		if !strings.Contains(msg, field) {
			t.Errorf("save error missing field %q (library reports it); error: %v", field, err)
		}
	}
}

func TestContractCLI_Doctor_ValidSession_NoIssuesReported(t *testing.T) {
	// Failure class: a healthy session must not be reported as degraded by
	// 'glassbox session doctor' — this pins the happy path of the contract
	// so a future change can't make doctor over-eager.
	overrideHome(t)

	data := testhelpers.NewSessionFixture().WithID("contract-cli-doctor-valid-1").Build()
	if report := session.ValidateIntegrity(data); !report.OK {
		t.Fatalf("test fixture must be valid for this test to be meaningful, got: %+v", report.Issues)
	}

	store, err := session.NewStore()
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()
	if err := store.Save(context.Background(), data); err != nil {
		t.Fatalf("save: %v", err)
	}

	var out bytes.Buffer
	sessionDoctorCmd.SetOut(&out)
	sessionDoctorCmd.SetErr(&out)

	if runErr := sessionDoctorCmd.RunE(sessionDoctorCmd, nil); runErr != nil {
		t.Errorf("expected no error for an all-healthy store, got: %v (output: %s)", runErr, out.String())
	}
}
