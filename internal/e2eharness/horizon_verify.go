// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package e2eharness

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/dotandev/glassbox/internal/sarif"
)

// HorizonVerifier fetches historical transactions from a Horizon archive and
// bit-exact-compares locally re-simulated results against the canonical
// Horizon TransactionResultMeta [Issue #1116].
type HorizonVerifier struct {
	BaseURL      string
	HTTPClient   *http.Client
	LedgerStart  uint32
	LedgerEnd    uint32
	Limit        int
	Simulate     SimulateFunc
	ResourceTol  float64 // relative tolerance for ResourceUsage warnings
}

// SimulateFunc re-simulates a transaction locally. Tests inject a mock.
type SimulateFunc func(ctx context.Context, entry CorpusEntry) (*LocalSimResult, error)

// CorpusEntry is one Horizon transaction used as a verification input.
type CorpusEntry struct {
	Hash              string
	Ledger            uint32
	EnvelopeXDR       string
	ResultXDR         string
	ResultMetaXDR     string
	PagingToken       string
}

// LocalSimResult is the locally re-simulated outcome to compare.
type LocalSimResult struct {
	ReturnValue   []byte
	Events        [][]byte
	ResourceUsage ResourceUsage
	ResultMetaXDR string
}

// ResourceUsage captures CPU/memory accounting for tolerant comparison.
type ResourceUsage struct {
	CPUInstructions uint64
	MemoryBytes     uint64
}

// FieldDivergence describes a single mismatched field.
type FieldDivergence struct {
	Field    string `json:"field"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
	Severity string `json:"severity"` // "error" or "warning"
}

// VerificationResult is the outcome of Verify for one corpus entry.
type VerificationResult struct {
	Match       bool
	Divergences []FieldDivergence
	SARIFReport *sarif.Run
	TxHash      string
}

// NewHorizonVerifier constructs a verifier with sensible defaults.
func NewHorizonVerifier(baseURL string, simulate SimulateFunc) *HorizonVerifier {
	return &HorizonVerifier{
		BaseURL:     strings.TrimRight(baseURL, "/"),
		HTTPClient:  &http.Client{Timeout: 30 * time.Second},
		Limit:       200,
		Simulate:    simulate,
		ResourceTol: 0.05,
	}
}

// FetchCorpus paginates Horizon's /transactions endpoint between startLedger
// and endLedger, collecting up to Limit entries.
func (v *HorizonVerifier) FetchCorpus(ctx context.Context, startLedger, endLedger uint32) ([]CorpusEntry, error) {
	if v.HTTPClient == nil {
		v.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	limit := v.Limit
	if limit <= 0 {
		limit = 200
	}
	var out []CorpusEntry
	cursor := ""
	for {
		u, err := url.Parse(v.BaseURL + "/transactions")
		if err != nil {
			return nil, err
		}
		q := u.Query()
		q.Set("order", "asc")
		q.Set("limit", strconv.Itoa(limit))
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		u.RawQuery = q.Encode()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		resp, err := v.HTTPClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("horizon fetch: %w", err)
		}
		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if resp.StatusCode >= 300 {
			return nil, fmt.Errorf("horizon status %d: %s", resp.StatusCode, truncate(string(body), 200))
		}

		page, err := parseHorizonTxPage(body)
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			break
		}
		for _, e := range page {
			if e.Ledger < startLedger {
				continue
			}
			if endLedger > 0 && e.Ledger > endLedger {
				return out, nil
			}
			out = append(out, e)
			if len(out) >= limit {
				return out, nil
			}
			cursor = e.PagingToken
		}
		if len(page) < limit {
			break
		}
	}
	return out, nil
}

type horizonTxPage struct {
	Embedded struct {
		Records []horizonTx `json:"records"`
	} `json:"_embedded"`
}

type horizonTx struct {
	Hash          string `json:"hash"`
	Ledger        uint32 `json:"ledger"`
	PagingToken   string `json:"paging_token"`
	EnvelopeXDR   string `json:"envelope_xdr"`
	ResultXDR     string `json:"result_xdr"`
	ResultMetaXDR string `json:"result_meta_xdr"`
}

func parseHorizonTxPage(body []byte) ([]CorpusEntry, error) {
	var page horizonTxPage
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, fmt.Errorf("decode horizon page: %w", err)
	}
	out := make([]CorpusEntry, 0, len(page.Embedded.Records))
	for _, r := range page.Embedded.Records {
		out = append(out, CorpusEntry{
			Hash:          r.Hash,
			Ledger:        r.Ledger,
			EnvelopeXDR:   r.EnvelopeXDR,
			ResultXDR:     r.ResultXDR,
			ResultMetaXDR: r.ResultMetaXDR,
			PagingToken:   r.PagingToken,
		})
	}
	return out, nil
}

// Verify re-simulates entry and bit-exact-compares canonicalised fields.
func (v *HorizonVerifier) Verify(ctx context.Context, entry CorpusEntry) (VerificationResult, error) {
	res := VerificationResult{TxHash: entry.Hash, Match: true}
	if v.Simulate == nil {
		return res, fmt.Errorf("SimulateFunc not configured")
	}
	local, err := v.Simulate(ctx, entry)
	if err != nil {
		return res, err
	}
	divs := CompareResults(entry, local, v.ResourceTol)
	res.Divergences = divs
	for _, d := range divs {
		if d.Severity == "error" {
			res.Match = false
			break
		}
	}
	res.SARIFReport = divergencesToSARIF(entry.Hash, divs)
	return res, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
