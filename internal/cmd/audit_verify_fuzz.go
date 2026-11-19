// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

//go:build go1.18
// +build go1.18

package cmd

import (
	"crypto/x509"
	"encoding/pem"
	"testing"
)

// FuzzParseCertificate tests certificate parsing with malformed PEM data.
// This targets x509.ParseCertificate used in audit verification.
func FuzzParseCertificate(f *testing.F) {
	// Seed corpus with valid and edge-case inputs
	f.Add([]byte("-----BEGIN CERTIFICATE-----\nMIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEA\n-----END CERTIFICATE-----")) // Valid PEM structure
	f.Add([]byte{}) // Empty
	f.Add([]byte("invalid pem data")) // Invalid PEM
	f.Add([]byte("-----BEGIN CERTIFICATE-----")) // Incomplete PEM
	f.Add([]byte("-----BEGIN CERTIFICATE-----\ninvalid base64\n-----END CERTIFICATE-----")) // Invalid base64
	f.Add(make([]byte, 1000)) // Large input

	f.Fuzz(func(t *testing.T, data []byte) {
		block, _ := pem.Decode(data)
		if block == nil {
			return // Not valid PEM, skip
		}
		
		_, err := x509.ParseCertificate(block.Bytes)
		_ = err // Expect errors for malformed certificates, but no panics
	})
}

// FuzzParseTrustPolicyConfig tests trust policy configuration parsing.
// This targets the JSON unmarshaling of TrustPolicyConfig.
func FuzzParseTrustPolicyConfig(f *testing.F) {
	f.Add([]byte(`{"trust_roots":["cert1"],"allowed_issuers":["issuer1"],"check_validity":true}`)) // Valid config
	f.Add([]byte(`{}`)) // Empty config
	f.Add([]byte(`invalid`)) // Invalid JSON
	f.Add([]byte{}) // Empty

	f.Fuzz(func(t *testing.T, data []byte) {
		var config TrustPolicyConfig
		err := config.UnmarshalJSON(data)
		_ = err // Expect errors for malformed JSON, but no panics
	})
}

// FuzzParsePEMCertificates tests parsing multiple PEM-encoded certificates.
func FuzzParsePEMCertificates(f *testing.F) {
	// Seed with multiple certificates
	f.Add([]byte("-----BEGIN CERTIFICATE-----\nMIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEA\n-----END CERTIFICATE-----\n-----BEGIN CERTIFICATE-----\nMIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEA\n-----END CERTIFICATE-----"))
	f.Add([]byte("-----BEGIN CERTIFICATE-----\nMIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEA\n-----END CERTIFICATE-----")) // Single cert
	f.Add([]byte{}) // Empty

	f.Fuzz(func(t *testing.T, data []byte) {
		certs := parsePEMCertificates(data)
		_ = certs // Should not panic even with malformed input
	})
}

// parsePEMCertificates is extracted from audit_verify.go for fuzzing
func parsePEMCertificates(data []byte) []*x509.Certificate {
	var certs []*x509.Certificate
	for len(data) > 0 {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err == nil {
			certs = append(certs, cert)
		}
	}
	return certs
}
