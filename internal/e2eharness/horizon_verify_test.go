// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package e2eharness

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestHorizonVerifier_SkipsWithoutEnv(t *testing.T) {
	if os.Getenv("GLASSBOX_TEST_HORIZON_URL") != "" {
		t.Skip("GLASSBOX_TEST_HORIZON_URL is set; skip-guard tested in unset environments only")
	}
	// Presence of this test documents the skip contract; live runs use
	// TestHorizonVerifier_Live when the env var is set.
}

func TestHorizonVerifier_MatchAndMismatch(t *testing.T) {
	meta := base64.StdEncoding.EncodeToString([]byte("canonical-meta-xdr-bytes"))
	result := base64.StdEncoding.EncodeToString([]byte("canonical-result"))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"_embedded": {
				"records": [{
					"hash": "abc123",
					"ledger": 100,
					"paging_token": "100",
					"envelope_xdr": "AAAA",
					"result_xdr": "` + result + `",
					"result_meta_xdr": "` + meta + `"
				}]
			}
		}`))
	}))
	defer srv.Close()

	good := NewHorizonVerifier(srv.URL, func(ctx context.Context, entry CorpusEntry) (*LocalSimResult, error) {
		return &LocalSimResult{
			ReturnValue:   canonicalXDRBytes(entry.ResultXDR),
			ResultMetaXDR: entry.ResultMetaXDR,
		}, nil
	})

	corpus, err := good.FetchCorpus(context.Background(), 1, 200)
	if err != nil {
		t.Fatalf("FetchCorpus: %v", err)
	}
	if len(corpus) != 1 {
		t.Fatalf("corpus len=%d, want 1", len(corpus))
	}

	vr, err := good.Verify(context.Background(), corpus[0])
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !vr.Match {
		t.Fatalf("expected Match=true, divergences=%+v", vr.Divergences)
	}

	bad := NewHorizonVerifier(srv.URL, func(ctx context.Context, entry CorpusEntry) (*LocalSimResult, error) {
		return &LocalSimResult{
			ReturnValue:   []byte("corrupted"),
			ResultMetaXDR: base64.StdEncoding.EncodeToString([]byte("corrupted-meta")),
		}, nil
	})
	vr2, err := bad.Verify(context.Background(), corpus[0])
	if err != nil {
		t.Fatalf("Verify corrupt: %v", err)
	}
	if vr2.Match {
		t.Fatal("expected Match=false for corrupted footprint/meta")
	}
	if len(vr2.Divergences) == 0 {
		t.Fatal("expected populated Divergences slice")
	}
	if vr2.SARIFReport == nil || len(vr2.SARIFReport.Results) == 0 {
		t.Fatal("expected SARIF report with results")
	}
}

func TestHorizonVerifier_Live(t *testing.T) {
	base := os.Getenv("GLASSBOX_TEST_HORIZON_URL")
	if base == "" {
		t.Skip("GLASSBOX_TEST_HORIZON_URL not set")
	}
	v := NewHorizonVerifier(base, func(ctx context.Context, entry CorpusEntry) (*LocalSimResult, error) {
		// Echo Horizon data as the "local" result — validates corpus fetch and
		// comparison plumbing against real testnet data.
		return &LocalSimResult{
			ReturnValue:   canonicalXDRBytes(entry.ResultXDR),
			ResultMetaXDR: entry.ResultMetaXDR,
		}, nil
	})
	v.Limit = 5
	corpus, err := v.FetchCorpus(context.Background(), 1, 0)
	if err != nil {
		t.Fatalf("FetchCorpus live: %v", err)
	}
	if len(corpus) == 0 {
		t.Fatal("expected at least one transaction from Horizon")
	}
	vr, err := v.Verify(context.Background(), corpus[0])
	if err != nil {
		t.Fatalf("Verify live: %v", err)
	}
	if !vr.Match {
		t.Fatalf("echo simulation should Match; divergences=%+v", vr.Divergences)
	}
}

func TestCompareResourceUsage_Tolerance(t *testing.T) {
	if d := CompareResourceUsage("cpu", 100, 104, 0.05); d != nil {
		t.Fatalf("within tolerance should be nil, got %+v", d)
	}
	d := CompareResourceUsage("cpu", 100, 150, 0.05)
	if d == nil || d.Severity != "error" {
		t.Fatalf("large divergence should be error, got %+v", d)
	}
}
