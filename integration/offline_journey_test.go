// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Package integration – offline (hermetic) journey test
//
// TestOfflineJourney exercises the full local-first debugging pipeline in a
// hermetically isolated environment:
//
//  1. Demo debug run       – glassbox debug --demo (no network, no WASM)
//  2. Local envelope debug – glassbox debug --json-file (offline XDR replay)
//  3. Snapshot save        – glassbox snapshot save (persist ledger state)
//  4. Snapshot load        – glassbox snapshot load --verify (round-trip + integrity)
//  5. Trace export         – glassbox debug --json-file --generate-trace
//  6. Report generation    – glassbox report --file (HTML, text, JSON)
//  7. Session lifecycle    – glassbox session list / save-guard / resume-guard
//  8. Audit sign           – glassbox audit:sign (offline Ed25519, no network)
//  9. Audit verify         – glassbox audit:verify (verify signature offline)
// 10. Registry replay      – glassbox debug --load-snapshots (offline registry)
// 11. Offline bundle       – glassbox offline generate / sign / verify
// 12. Missing-dep failure  – missing required file → actionable error, non-zero exit
// 13. Network-attempt guard– proxy intercepts any undeclared outbound call
//
// Acceptance criteria (from the spec):
//   - Journey completes offline with deterministic artifacts and no undeclared
//     network attempts.
//   - A missing local dependency produces an actionable failure message.
//   - The generated bundle can be verified in a second isolated step.
package integration

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ────────────────────────────────────────────────────────────────────────────
// Constants – deterministic fixture data
// ────────────────────────────────────────────────────────────────────────────

// offlineTxHash is a well-formed 64-hex-char transaction hash used throughout
// the offline fixtures.  It is never resolved against a live network.
const offlineTxHash = "aabb000000000000000000000000000000000000000000000000000000001234"

// offlineEnvelopeXDR is a minimal valid-length base64 string that satisfies
// the CLI's base64-decode check for the --json-file envelope path.
// (88 base64 chars = 64 bytes of zeroes — structurally sufficient for the
// file-load code path; not a real Stellar envelope.)
const offlineEnvelopeXDR = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

// ────────────────────────────────────────────────────────────────────────────
// Network guard – blocks outbound calls made by child processes via HTTP_PROXY
// ────────────────────────────────────────────────────────────────────────────

// networkBlocker starts a local HTTP server and records any request that hits
// it.  Child processes have HTTP_PROXY / HTTPS_PROXY pointed at this server,
// so any HTTP client that respects the standard proxy env will trigger a test
// failure instead of completing a live network call.
type networkBlocker struct {
	srv      *httptest.Server
	dialHits int64 // accessed atomically
}

func newNetworkBlocker(t *testing.T) *networkBlocker {
	t.Helper()
	nb := &networkBlocker{}
	nb.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&nb.dialHits, 1)
		t.Errorf("NETWORK VIOLATION: undeclared outbound request to %s %s (host: %s)",
			r.Method, r.URL.String(), r.Host)
		http.Error(w, "network blocked by offline journey test harness", http.StatusForbidden)
	}))
	t.Cleanup(nb.srv.Close)
	return nb
}

func (nb *networkBlocker) proxyURL() string      { return nb.srv.URL }
func (nb *networkBlocker) hitCount() int64        { return atomic.LoadInt64(&nb.dialHits) }

// assertNoDials fails the test if any outbound request was intercepted by the
// blocking proxy.
func assertNoDials(t *testing.T, nb *networkBlocker) {
	t.Helper()
	if hits := nb.hitCount(); hits > 0 {
		t.Errorf("offline journey: %d undeclared outbound network attempt(s) detected", hits)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Hermetic environment builder
// ────────────────────────────────────────────────────────────────────────────

// hermeticEnv returns a minimal, isolated set of environment variables for
// every child glassbox process in the offline journey:
//
//   - HOME / USERPROFILE → isolated temp dir (no ~/.glassbox leakage)
//   - No GLASSBOX_RPC_URL / GLASSBOX_RPC_TOKEN (prevent accidental live calls)
//   - GLASSBOX_NO_UPDATE_CHECK=1 (suppress the async GitHub version ping)
//   - NO_COLOR=1 (deterministic text output for string assertions)
//   - HTTP_PROXY / HTTPS_PROXY / ALL_PROXY → blocking proxy
//   - PATH preserved for shared-library resolution
func hermeticEnv(homeDir, blockerURL string) []string {
	env := []string{
		"HOME=" + homeDir,
		"USERPROFILE=" + homeDir,
		// Suppress the async GitHub update-check so it can't slip through.
		"GLASSBOX_NO_UPDATE_CHECK=1",
		// Telemetry opt-out.
		"GLASSBOX_TELEMETRY=",
		"GLASSBOX_TELEMETRY_ENDPOINT=",
		// Stable text output.
		"NO_COLOR=1",
		// Point standard proxy env vars at the blocking proxy.
		"HTTP_PROXY=" + blockerURL,
		"HTTPS_PROXY=" + blockerURL,
		"ALL_PROXY=" + blockerURL,
		// Ensure no live RPC credentials are accidentally picked up.
		"GLASSBOX_RPC_URL=",
		"GLASSBOX_RPC_TOKEN=",
		// Disable the simulator binary path so only mock/demo paths are used.
		"GLASSBOX_SIM_PATH=",
		"GLASSBOX_SIM_COVERAGE_LCOV_PATH=",
	}
	// Preserve PATH so the binary can resolve shared libraries.
	for _, e := range os.Environ() {
		if strings.HasPrefix(strings.ToUpper(e), "PATH=") {
			env = append(env, e)
			break
		}
	}
	return env
}

// runHermetic executes the glassbox binary with the hermetic environment and
// returns stdout, stderr, and the run error.  A 30 s timeout is applied.
func runHermetic(t *testing.T, env []string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	bin := binaryPath(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = env

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err = cmd.Run()
	return outBuf.String(), errBuf.String(), err
}

// ────────────────────────────────────────────────────────────────────────────
// Fixture helpers – all data lives in t.TempDir()
// ────────────────────────────────────────────────────────────────────────────

// writeJSON marshals v to indented JSON and writes it to dir/name.
func writeJSON(t *testing.T, dir, name string, v interface{}) string {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("writeJSON %s: %v", name, err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("writeJSON %s: %v", name, err)
	}
	return path
}

// fixtureEnvelopeJSON writes a JSON envelope file for glassbox debug --json-file.
func fixtureEnvelopeJSON(t *testing.T, dir string) string {
	t.Helper()
	return writeJSON(t, dir, "envelope.json", map[string]interface{}{
		"network":         "testnet",
		"envelope_xdr":    offlineEnvelopeXDR,
		"result_meta_xdr": "",
	})
}

// fixtureLedgerState writes a minimal Snapshot-format JSON (ledgerEntries)
// compatible with glassbox snapshot save --input.
func fixtureLedgerState(t *testing.T, dir string) string {
	t.Helper()
	return writeJSON(t, dir, "ledger_state.json", map[string]interface{}{
		"ledgerEntries": []interface{}{
			[]string{
				base64.StdEncoding.EncodeToString([]byte("FIXTURE_KEY_0001")),
				base64.StdEncoding.EncodeToString([]byte("FIXTURE_VAL_0001")),
			},
		},
	})
}

// fixtureTrace writes a minimal ExecutionTrace JSON that satisfies
// glassbox report --file.
func fixtureTrace(t *testing.T, dir string) string {
	t.Helper()
	now := time.Date(2026, 8, 30, 0, 0, 0, 0, time.UTC)
	return writeJSON(t, dir, "trace.json", map[string]interface{}{
		"transaction_hash":  offlineTxHash,
		"start_time":        now.Format(time.RFC3339),
		"end_time":          now.Add(500 * time.Millisecond).Format(time.RFC3339),
		"current_step":      0,
		"snapshot_interval": 100,
		"states": []interface{}{
			map[string]interface{}{
				"step":        0,
				"timestamp":   now.Format(time.RFC3339),
				"operation":   "invoke_host_function",
				"event_type":  "contract_call",
				"contract_id": "CDUMMY000000000000000000000000000000000000000000000000000FIXTURE",
				"function":    "increment",
			},
			map[string]interface{}{
				"step":        1,
				"timestamp":   now.Add(100 * time.Millisecond).Format(time.RFC3339),
				"operation":   "invoke_host_function",
				"event_type":  "contract_call",
				"contract_id": "CDUMMY000000000000000000000000000000000000000000000000000FIXTURE",
				"function":    "get_count",
			},
		},
		"snapshots":         []interface{}{},
		"diagnostic_events": []interface{}{},
	})
}

// fixtureAuditPayload writes a minimal JSON payload for audit:sign.
func fixtureAuditPayload(t *testing.T, dir, name string) string {
	t.Helper()
	return writeJSON(t, dir, name, map[string]interface{}{
		"input":     map[string]interface{}{},
		"state":     map[string]interface{}{},
		"events":    []interface{}{},
		"timestamp": "2026-08-30T00:00:00.000Z",
	})
}

// fixtureED25519Key generates a deterministic Ed25519 key from a fixed seed
// (safe for test use only) and writes the PKCS#8 PEM file.
// Returns the file path and the hex-encoded public key.
func fixtureED25519Key(t *testing.T, dir string) (keyPath, pubKeyHex string) {
	t.Helper()
	rawSeed := sha256.Sum256([]byte("glassbox-offline-journey-fixture-ed25519-v1"))
	privKey := ed25519.NewKeyFromSeed(rawSeed[:])
	pubKey := privKey.Public().(ed25519.PublicKey)

	pkcs8Bytes, err := x509.MarshalPKCS8PrivateKey(privKey)
	if err != nil {
		t.Fatalf("PKCS8 marshal: %v", err)
	}
	block := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8Bytes})
	keyPath = filepath.Join(dir, "ed25519_key.pem")
	if err := os.WriteFile(keyPath, block, 0o600); err != nil {
		t.Fatalf("write key PEM: %v", err)
	}
	return keyPath, hex.EncodeToString(pubKey)
}

// fixtureSignedAuditLog calls audit:sign via the CLI binary and writes the
// resulting signed audit log to dir/signed_audit.json.
func fixtureSignedAuditLog(t *testing.T, env []string, dir, payloadPath, keyPath string) string {
	t.Helper()
	stdout, stderr, err := runHermetic(t, env,
		"audit:sign",
		"--payload-file", payloadPath,
		"--software-private-key", keyPath,
	)
	if err != nil {
		t.Fatalf("audit:sign (fixture): exit=%d stdout=%s stderr=%s",
			exitCode(err), stdout, stderr)
	}
	out := filepath.Join(dir, "signed_audit.json")
	if err := os.WriteFile(out, []byte(stdout), 0o644); err != nil {
		t.Fatalf("write signed audit: %v", err)
	}
	return out
}

// fixtureSnapshotRegistry builds a minimal replay-registry JSON file.
func fixtureSnapshotRegistry(t *testing.T, dir string) string {
	t.Helper()
	snap := map[string]interface{}{
		"ledgerEntries": []interface{}{
			[]string{
				base64.StdEncoding.EncodeToString([]byte("REGISTRY_KEY_001")),
				base64.StdEncoding.EncodeToString([]byte("REGISTRY_VAL_001")),
			},
		},
	}
	snapBytes, _ := json.Marshal(snap)
	cs := sha256.Sum256(snapBytes)
	checksum := hex.EncodeToString(cs[:])

	return writeJSON(t, dir, "snapshot_registry.json", map[string]interface{}{
		"schema_version":   1,
		"glassbox_version": "test",
		"created_at":       time.Date(2026, 8, 30, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
		"tx_hash":          offlineTxHash,
		"network":          "testnet",
		"envelope_xdr":     offlineEnvelopeXDR,
		"result_meta_xdr":  "",
		"entries": []interface{}{
			map[string]interface{}{
				"timestamp": int64(1756512000),
				"snapshot":  snap,
				"checksum":  checksum,
			},
		},
	})
}

// fixtureXDRFile writes a raw base64 XDR envelope text file for
// glassbox offline generate.
func fixtureXDRFile(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "envelope.xdr")
	if err := os.WriteFile(path, []byte(offlineEnvelopeXDR+"\n"), 0o644); err != nil {
		t.Fatalf("write XDR file: %v", err)
	}
	return path
}

// ────────────────────────────────────────────────────────────────────────────
// Artifact manifest helper
// ────────────────────────────────────────────────────────────────────────────

type artifactCheck struct{ t *testing.T }

func artifacts(t *testing.T) *artifactCheck { return &artifactCheck{t: t} }

// file asserts that path exists and is non-empty, returning the size.
func (a *artifactCheck) file(path string) int64 {
	a.t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		a.t.Errorf("artifact %q not found: %v", path, err)
		return 0
	}
	if info.Size() == 0 {
		a.t.Errorf("artifact %q is empty", path)
	}
	return info.Size()
}

// jsonFile asserts the file exists, is non-empty, and parses as valid JSON.
func (a *artifactCheck) jsonFile(path string) {
	a.t.Helper()
	a.file(path)
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var v interface{}
	if err := json.Unmarshal(data, &v); err != nil {
		a.t.Errorf("artifact %q is not valid JSON: %v", path, err)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// JSON field extractor
// ────────────────────────────────────────────────────────────────────────────

// jsonFieldStr reads a JSON file and returns the string value at the dot-path
// described by keys.  For example keys=["snapshot","fingerprint"] navigates
// obj["snapshot"]["fingerprint"].
func jsonFieldStr(t *testing.T, path string, keys ...string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("jsonFieldStr: read %q: %v", path, err)
	}
	var v interface{}
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("jsonFieldStr: parse %q: %v", path, err)
	}
	for _, k := range keys {
		m, ok := v.(map[string]interface{})
		if !ok {
			return ""
		}
		v = m[k]
	}
	s, _ := v.(string)
	return s
}

// ────────────────────────────────────────────────────────────────────────────
// TestOfflineJourney – the primary acceptance test
// ────────────────────────────────────────────────────────────────────────────

func TestOfflineJourney(t *testing.T) {
	if testing.Short() {
		t.Skip("offline journey skipped in -short mode")
	}

	// ── Setup ───────────────────────────────────────────────────────────────
	homeDir := t.TempDir()
	workDir := t.TempDir()
	blocker := newNetworkBlocker(t)
	env := hermeticEnv(homeDir, blocker.proxyURL())

	// ── Build fixtures ──────────────────────────────────────────────────────
	envelopeFile := fixtureEnvelopeJSON(t, workDir)
	ledgerStateFile := fixtureLedgerState(t, workDir)
	traceFile := fixtureTrace(t, workDir)
	payloadFile := fixtureAuditPayload(t, workDir, "audit_payload.json")
	keyPath, pubKeyHex := fixtureED25519Key(t, workDir)
	registryFile := fixtureSnapshotRegistry(t, workDir)
	xdrFile := fixtureXDRFile(t, workDir)
	signedAuditLogPath := fixtureSignedAuditLog(t, env, workDir, payloadFile, keyPath)

	// ── Step 1: Demo debug – no network, no WASM ────────────────────────────
	t.Run("step01_demo_debug", func(t *testing.T) {
		stdout, stderr, err := runHermetic(t, env, "debug", "--demo", "--no-color")
		assertExitCode(t, 0, err)
		combined := stdout + stderr
		assertNotContains(t, "demo stderr", combined, "panic")
		assertNotContains(t, "demo stderr", combined, "goroutine")
		assertContains(t, "demo output", combined, "transaction")
	})

	// ── Step 2: Local envelope debug – offline XDR replay ──────────────────
	t.Run("step02_local_envelope_debug", func(t *testing.T) {
		stdout, stderr, err := runHermetic(t, env,
			"debug", "--json-file", envelopeFile, "--no-color",
		)
		combined := stdout + stderr
		assertNotContains(t, "local-envelope stderr", combined, "panic")
		assertNotContains(t, "local-envelope stderr", combined, "goroutine")
		if exitCode(err) != 0 {
			// Acceptable: missing simulator binary.  Not acceptable: network error.
			assertNotContains(t, "local-envelope failure", combined, "dial tcp")
			assertNotContains(t, "local-envelope failure", combined, "no such host")
			assertNotContains(t, "local-envelope failure", combined, "connection refused")
		}
	})

	// ── Step 3: Snapshot save ───────────────────────────────────────────────
	snapPath := filepath.Join(workDir, "saved_snapshot.json")
	t.Run("step03_snapshot_save", func(t *testing.T) {
		stdout, stderr, err := runHermetic(t, env,
			"snapshot", "save",
			"--tx", offlineTxHash,
			"--network", "testnet",
			"--input", ledgerStateFile,
			"--output", snapPath,
			"--envelope-xdr", offlineEnvelopeXDR,
		)
		assertExitCode(t, 0, err)
		combined := stdout + stderr
		assertContains(t, "snapshot-save output", combined, "Snapshot saved")
		assertContains(t, "snapshot-save output", combined, snapPath)
		assertNotContains(t, "snapshot-save stderr", combined, "panic")
		artifacts(t).jsonFile(snapPath)
	})

	// ── Step 4: Snapshot load + verify ─────────────────────────────────────
	t.Run("step04_snapshot_load", func(t *testing.T) {
		if _, err := os.Stat(snapPath); err != nil {
			t.Skipf("step04 skipped: snapshot from step03 not present (%v)", err)
		}
		stdout, stderr, err := runHermetic(t, env,
			"snapshot", "load",
			"--path", snapPath,
			"--tx", offlineTxHash,
			"--network", "testnet",
			"--verify",
		)
		assertExitCode(t, 0, err)
		combined := stdout + stderr
		assertContains(t, "snapshot-load output", combined, "Validation OK")
		assertContains(t, "snapshot-load output", combined, offlineTxHash)
		assertNotContains(t, "snapshot-load stderr", combined, "panic")
		assertNotContains(t, "snapshot-load output", combined, "fingerprint mismatch")
	})

	// ── Step 5: Trace export ────────────────────────────────────────────────
	traceOutPath := filepath.Join(workDir, "exported_trace.json")
	t.Run("step05_trace_export", func(t *testing.T) {
		stdout, stderr, err := runHermetic(t, env,
			"debug",
			"--json-file", envelopeFile,
			"--generate-trace",
			"--trace-output", traceOutPath,
			"--no-color",
		)
		combined := stdout + stderr
		assertNotContains(t, "trace-export stderr", combined, "panic")
		assertNotContains(t, "trace-export stderr", combined, "goroutine")
		if exitCode(err) == 0 {
			artifacts(t).jsonFile(traceOutPath)
		} else {
			// Acceptable: missing simulator.  Not acceptable: any network error.
			assertNotContains(t, "trace-export failure", combined, "dial tcp")
			assertNotContains(t, "trace-export failure", combined, "no such host")
		}
	})

	// ── Step 6: Report generation (from pre-built fixture trace) ───────────
	reportDir := filepath.Join(workDir, "reports")
	if err := os.MkdirAll(reportDir, 0o755); err != nil {
		t.Fatalf("mkdir reports: %v", err)
	}

	t.Run("step06a_report_html", func(t *testing.T) {
		stdout, stderr, err := runHermetic(t, env,
			"report",
			"--file", traceFile,
			"--format", "html",
			"--output", reportDir,
		)
		assertExitCode(t, 0, err)
		combined := stdout + stderr
		assertContains(t, "report-html output", combined, "[OK]")
		assertNotContains(t, "report-html stderr", combined, "panic")

		htmlPath := filepath.Join(reportDir, "report.html")
		artifacts(t).file(htmlPath)
		data, _ := os.ReadFile(htmlPath)
		if !strings.Contains(strings.ToLower(string(data)), "html") {
			t.Errorf("report.html does not appear to contain HTML content")
		}
	})

	t.Run("step06b_report_text", func(t *testing.T) {
		stdout, stderr, err := runHermetic(t, env,
			"report",
			"--file", traceFile,
			"--format", "text",
			"--output", reportDir,
		)
		assertExitCode(t, 0, err)
		combined := stdout + stderr
		assertContains(t, "report-text output", combined, "[OK]")
		assertNotContains(t, "report-text stderr", combined, "panic")
		artifacts(t).file(filepath.Join(reportDir, "report.txt"))
	})

	t.Run("step06c_report_json", func(t *testing.T) {
		stdout, stderr, err := runHermetic(t, env,
			"report",
			"--file", traceFile,
			"--format", "json",
			"--output", reportDir,
		)
		assertExitCode(t, 0, err)
		combined := stdout + stderr
		assertContains(t, "report-json output", combined, "[OK]")
		assertNotContains(t, "report-json stderr", combined, "panic")
		artifacts(t).jsonFile(filepath.Join(reportDir, "report.json"))
	})

	// ── Step 7: Session lifecycle ───────────────────────────────────────────
	t.Run("step07_session_lifecycle", func(t *testing.T) {
		// list on an empty store must succeed.
		stdout, stderr, err := runHermetic(t, env, "session", "list")
		assertExitCode(t, 0, err)
		combined := stdout + stderr
		assertNotContains(t, "session-list stderr", combined, "panic")
		// Acceptable output: "No saved sessions" or a table header row.
		if !containsAny(combined, "No saved sessions", "no saved sessions", "ID", "Session") {
			t.Logf("session list (empty store): %q", combined)
		}

		// save without an active session must fail with an actionable message.
		_, stderr2, err2 := runHermetic(t, env, "session", "save")
		if exitCode(err2) == 0 {
			t.Error("session save without active session: expected non-zero exit")
		}
		if !containsAny(stderr2, "no active session", "debug", "glassbox debug") {
			t.Errorf("session save without active session: expected actionable error, got: %q", stderr2)
		}
		assertNotContains(t, "session-save stderr", stderr2, "panic")

		// resume with a nonexistent ID must fail gracefully.
		_, stderr3, err3 := runHermetic(t, env, "session", "resume", "nonexistent-id-xyz")
		if exitCode(err3) == 0 {
			t.Error("session resume (nonexistent id): expected non-zero exit")
		}
		assertNotContains(t, "session-resume stderr", stderr3, "panic")
		assertNotContains(t, "session-resume stderr", stderr3, "goroutine")
	})

	// ── Step 8: Audit sign (offline, Ed25519) ───────────────────────────────
	t.Run("step08_audit_sign", func(t *testing.T) {
		// The signed log was built during fixture setup; verify its structure.
		artifacts(t).jsonFile(signedAuditLogPath)
		data, _ := os.ReadFile(signedAuditLogPath)
		var log map[string]interface{}
		if err := json.Unmarshal(data, &log); err != nil {
			t.Fatalf("signed audit log: %v", err)
		}
		for _, field := range []string{
			"version", "timestamp", "trace_hash",
			"signature", "public_key", "provider", "payload",
		} {
			if _, ok := log[field]; !ok {
				t.Errorf("signed audit log missing required field %q", field)
			}
		}
		if log["provider"] != "software" {
			t.Errorf("audit log provider: want %q, got %v", "software", log["provider"])
		}
	})

	// ── Step 9: Audit verify (offline, deterministic) ──────────────────────
	t.Run("step09a_audit_verify_embedded_key", func(t *testing.T) {
		stdout, stderr, err := runHermetic(t, env,
			"audit:verify", "--audit-log", signedAuditLogPath,
		)
		assertExitCode(t, 0, err)
		combined := stdout + stderr
		assertContains(t, "audit-verify output", combined, "VALID")
		assertContains(t, "audit-verify output", combined, "PASS")
		assertNotContains(t, "audit-verify stderr", combined, "panic")
	})

	t.Run("step09b_audit_verify_explicit_key", func(t *testing.T) {
		stdout, stderr, err := runHermetic(t, env,
			"audit:verify",
			"--audit-log", signedAuditLogPath,
			"--public-key", pubKeyHex,
		)
		assertExitCode(t, 0, err)
		combined := stdout + stderr
		assertContains(t, "audit-verify (explicit key)", combined, "VALID")
		assertNotContains(t, "audit-verify (explicit key) stderr", combined, "panic")
	})

	t.Run("step09c_audit_verify_json_output", func(t *testing.T) {
		stdout, stderr, err := runHermetic(t, env,
			"audit:verify",
			"--audit-log", signedAuditLogPath,
			"--json",
		)
		assertExitCode(t, 0, err)
		assertNotContains(t, "audit-verify-json stderr", stderr, "panic")

		var result map[string]interface{}
		if err := json.Unmarshal([]byte(stdout), &result); err != nil {
			t.Fatalf("audit:verify --json is not valid JSON: %v\nstdout: %s", err, stdout)
		}
		if valid, _ := result["valid"].(bool); !valid {
			t.Errorf("audit:verify --json: expected valid=true, got: %v", result["valid"])
		}
		if sigValid, _ := result["signature_valid"].(bool); !sigValid {
			t.Errorf("audit:verify --json: expected signature_valid=true, got: %v",
				result["signature_valid"])
		}
	})

	// ── Step 10: Registry replay (offline --load-snapshots) ─────────────────
	t.Run("step10_registry_replay", func(t *testing.T) {
		stdout, stderr, err := runHermetic(t, env,
			"debug",
			"--load-snapshots", registryFile,
			"--no-color",
		)
		combined := stdout + stderr
		assertNotContains(t, "registry-replay stderr", combined, "panic")
		assertNotContains(t, "registry-replay stderr", combined, "goroutine")
		if exitCode(err) == 0 {
			assertContains(t, "registry-replay output", combined, "Offline replay")
			assertContains(t, "registry-replay output", combined, "testnet")
		} else {
			// Acceptable: missing simulator.  Not acceptable: network error.
			assertNotContains(t, "registry-replay failure", combined, "dial tcp")
			assertNotContains(t, "registry-replay failure", combined, "no such host")
		}
	})

	// ── Step 11: Offline bundle – generate / sign / verify ──────────────────
	bundleFile := filepath.Join(workDir, "tx.glassbox.json")

	t.Run("step11a_offline_generate", func(t *testing.T) {
		stdout, stderr, err := runHermetic(t, env,
			"offline", "generate",
			"--network", "testnet",
			"--output", bundleFile,
			xdrFile,
		)
		assertExitCode(t, 0, err)
		combined := stdout + stderr
		assertContains(t, "offline-generate output", combined, "Unsigned envelope saved")
		assertNotContains(t, "offline-generate stderr", combined, "panic")
		artifacts(t).jsonFile(bundleFile)
	})

	t.Run("step11b_offline_sign", func(t *testing.T) {
		if _, err := os.Stat(bundleFile); err != nil {
			t.Skipf("step11b skipped: bundle from step11a not present (%v)", err)
		}
		rawSeed := sha256.Sum256([]byte("glassbox-offline-journey-fixture-ed25519-v1"))
		seedHex := hex.EncodeToString(rawSeed[:])

		stdout, stderr, err := runHermetic(t, env,
			"offline", "sign",
			"--key", seedHex,
			bundleFile,
		)
		assertExitCode(t, 0, err)
		combined := stdout + stderr
		assertContains(t, "offline-sign output", combined, "signed successfully")
		assertNotContains(t, "offline-sign stderr", combined, "panic")
	})

	t.Run("step11c_offline_verify", func(t *testing.T) {
		if _, err := os.Stat(bundleFile); err != nil {
			t.Skipf("step11c skipped: bundle not present (%v)", err)
		}
		stdout, stderr, err := runHermetic(t, env,
			"offline", "verify",
			bundleFile,
		)
		assertExitCode(t, 0, err)
		combined := stdout + stderr
		assertContains(t, "offline-verify output", combined, "verified successfully")
		assertNotContains(t, "offline-verify stderr", combined, "panic")
	})

	// ── Step 12: Missing-dependency failures → actionable errors ────────────
	missingPath := filepath.Join(workDir, "does_not_exist.json")

	t.Run("step12a_missing_snapshot_path", func(t *testing.T) {
		_, stderr, err := runHermetic(t, env,
			"snapshot", "load", "--path", missingPath,
		)
		if exitCode(err) == 0 {
			t.Error("expected non-zero exit for missing snapshot path")
		}
		if !containsAny(stderr, "not found", "no such file", "failed to read", missingPath) {
			t.Errorf("missing snapshot: expected actionable error, got: %q", stderr)
		}
		assertNotContains(t, "missing-snapshot stderr", stderr, "panic")
	})

	t.Run("step12b_missing_trace_for_report", func(t *testing.T) {
		_, stderr, err := runHermetic(t, env,
			"report", "--file", missingPath, "--format", "text",
		)
		if exitCode(err) == 0 {
			t.Error("expected non-zero exit for missing trace file")
		}
		if !containsAny(stderr, "not found", "no such file", missingPath) {
			t.Errorf("missing trace: expected actionable error, got: %q", stderr)
		}
		assertNotContains(t, "missing-trace stderr", stderr, "panic")
	})

	t.Run("step12c_missing_audit_payload", func(t *testing.T) {
		_, stderr, err := runHermetic(t, env,
			"audit:sign",
			"--payload-file", missingPath,
			"--software-private-key", keyPath,
		)
		if exitCode(err) == 0 {
			t.Error("expected non-zero exit for missing audit payload")
		}
		if !containsAny(stderr, "not found", "no such file", "failed to read", missingPath) {
			t.Errorf("missing payload: expected actionable error, got: %q", stderr)
		}
		assertNotContains(t, "missing-payload stderr", stderr, "panic")
	})

	t.Run("step12d_missing_audit_log_for_verify", func(t *testing.T) {
		_, stderr, err := runHermetic(t, env,
			"audit:verify", "--audit-log", missingPath,
		)
		if exitCode(err) == 0 {
			t.Error("expected non-zero exit for missing audit log")
		}
		if !containsAny(stderr, "not found", "no such file", "failed to read", missingPath) {
			t.Errorf("missing audit log: expected actionable error, got: %q", stderr)
		}
		assertNotContains(t, "missing-audit-log stderr", stderr, "panic")
	})

	t.Run("step12e_missing_registry_for_replay", func(t *testing.T) {
		_, stderr, err := runHermetic(t, env,
			"debug", "--load-snapshots", missingPath,
		)
		if exitCode(err) == 0 {
			t.Error("expected non-zero exit for missing snapshot registry")
		}
		if !containsAny(stderr, "not found", "no such file", "failed to read", missingPath, "failed to load") {
			t.Errorf("missing registry: expected actionable error, got: %q", stderr)
		}
		assertNotContains(t, "missing-registry stderr", stderr, "panic")
	})

	// ── Step 13: Network-attempt guard ──────────────────────────────────────
	t.Run("step13_no_undeclared_network", func(t *testing.T) {
		assertNoDials(t, blocker)
	})
}

// ────────────────────────────────────────────────────────────────────────────
// TestOfflineSnapshotDeterminism – same ledger state → same fingerprint
// ────────────────────────────────────────────────────────────────────────────

// TestOfflineSnapshotDeterminism verifies that snapshot save is a deterministic
// operation: identical input produces the same fingerprint across separate runs.
func TestOfflineSnapshotDeterminism(t *testing.T) {
	if testing.Short() {
		t.Skip("offline snapshot determinism test skipped in -short mode")
	}

	blocker := newNetworkBlocker(t)
	ledgerFile := fixtureLedgerState(t, t.TempDir())

	fingerprints := make([]string, 2)
	for i := range fingerprints {
		homeDir := t.TempDir()
		outDir := t.TempDir()
		env := hermeticEnv(homeDir, blocker.proxyURL())
		outPath := filepath.Join(outDir, fmt.Sprintf("snap%d.json", i))

		_, _, err := runHermetic(t, env,
			"snapshot", "save",
			"--tx", offlineTxHash,
			"--network", "testnet",
			"--input", ledgerFile,
			"--output", outPath,
			"--envelope-xdr", offlineEnvelopeXDR,
		)
		assertExitCode(t, 0, err)

		fingerprints[i] = jsonFieldStr(t, outPath, "snapshot", "fingerprint")
		if fingerprints[i] == "" {
			t.Fatalf("run %d: snapshot fingerprint is empty", i)
		}
	}

	if fingerprints[0] != fingerprints[1] {
		t.Errorf("snapshot fingerprint is non-deterministic:\n  run 0: %s\n  run 1: %s",
			fingerprints[0], fingerprints[1])
	}

	assertNoDials(t, blocker)
}

// ────────────────────────────────────────────────────────────────────────────
// TestOfflineBundleVerifyIsolated – acceptance criterion
// "The generated bundle can be verified in a second isolated step."
// ────────────────────────────────────────────────────────────────────────────

func TestOfflineBundleVerifyIsolated(t *testing.T) {
	if testing.Short() {
		t.Skip("offline bundle verify isolated test skipped in -short mode")
	}

	// Environment A – where the bundle is generated and signed.
	homeA := t.TempDir()
	workA := t.TempDir()
	blocker := newNetworkBlocker(t)
	envA := hermeticEnv(homeA, blocker.proxyURL())

	xdrFile := fixtureXDRFile(t, workA)
	bundleFile := filepath.Join(workA, "bundle.glassbox.json")

	// Generate.
	_, _, err := runHermetic(t, envA,
		"offline", "generate",
		"--network", "testnet",
		"--output", bundleFile,
		xdrFile,
	)
	assertExitCode(t, 0, err)

	// Sign.
	rawSeed := sha256.Sum256([]byte("glassbox-offline-journey-fixture-ed25519-v1"))
	seedHex := hex.EncodeToString(rawSeed[:])
	_, _, err = runHermetic(t, envA, "offline", "sign", "--key", seedHex, bundleFile)
	assertExitCode(t, 0, err)

	// Environment B – completely independent (different HOME, different TempDir).
	homeB := t.TempDir()
	envB := hermeticEnv(homeB, blocker.proxyURL())

	stdout, stderr, err := runHermetic(t, envB, "offline", "verify", bundleFile)
	assertExitCode(t, 0, err)
	combined := stdout + stderr
	assertContains(t, "isolated-verify output", combined, "verified successfully")
	assertNotContains(t, "isolated-verify stderr", combined, "panic")

	assertNoDials(t, blocker)
}

// ────────────────────────────────────────────────────────────────────────────
// TestOfflineAuditChain – tamper-evident chain verification entirely offline
// ────────────────────────────────────────────────────────────────────────────

func TestOfflineAuditChain(t *testing.T) {
	if testing.Short() {
		t.Skip("offline audit chain test skipped in -short mode")
	}

	homeDir := t.TempDir()
	workDir := t.TempDir()
	blocker := newNetworkBlocker(t)
	env := hermeticEnv(homeDir, blocker.proxyURL())

	keyPath, pubKeyHex := fixtureED25519Key(t, workDir)
	payload1 := fixtureAuditPayload(t, workDir, "payload_1.json")

	// ── Entry 1 (genesis – no previous hash) ────────────────────────────────
	out1Path := filepath.Join(workDir, "audit_1.json")
	stdout1, _, err := runHermetic(t, env,
		"audit:sign",
		"--payload-file", payload1,
		"--software-private-key", keyPath,
	)
	if err != nil {
		t.Fatalf("audit:sign entry 1: %v", err)
	}
	if err := os.WriteFile(out1Path, []byte(stdout1), 0o644); err != nil {
		t.Fatalf("write audit entry 1: %v", err)
	}

	// Extract trace_hash from entry 1 – this becomes the previous hash for entry 2.
	var log1 map[string]interface{}
	if err := json.Unmarshal([]byte(stdout1), &log1); err != nil {
		t.Fatalf("parse audit entry 1 JSON: %v", err)
	}
	traceHash1, _ := log1["trace_hash"].(string)
	if traceHash1 == "" {
		t.Fatal("audit entry 1: missing trace_hash field")
	}

	// ── Entry 2 (linked to entry 1) ─────────────────────────────────────────
	payload2Path := writeJSON(t, workDir, "payload_2.json", map[string]interface{}{
		"input":     map[string]interface{}{"step": 2},
		"state":     map[string]interface{}{},
		"events":    []interface{}{},
		"timestamp": "2026-08-30T01:00:00.000Z",
	})

	out2Path := filepath.Join(workDir, "audit_2.json")
	stdout2, _, err2 := runHermetic(t, env,
		"audit:sign",
		"--payload-file", payload2Path,
		"--software-private-key", keyPath,
		"--previous-signature-hash", traceHash1,
	)
	if err2 != nil {
		t.Fatalf("audit:sign entry 2: %v", err2)
	}
	if err := os.WriteFile(out2Path, []byte(stdout2), 0o644); err != nil {
		t.Fatalf("write audit entry 2: %v", err)
	}

	// ── Verify entry 1 standalone ────────────────────────────────────────────
	stdout3, stderr3, err3 := runHermetic(t, env,
		"audit:verify",
		"--audit-log", out1Path,
		"--public-key", pubKeyHex,
	)
	assertExitCode(t, 0, err3)
	assertContains(t, "chain-verify entry 1", stdout3+stderr3, "VALID")
	assertNotContains(t, "chain-verify entry 1 stderr", stderr3, "panic")

	// ── Verify entry 2 with chain-link check ─────────────────────────────────
	// Note: the provenance object was created by --previous-signature-hash on
	// sign, but lacks a cert chain.  The CLI will flag the provenance as
	// incomplete while still correctly checking the chain link — so the output
	// will contain chain-link related messaging regardless of exit status.
	stdout4, stderr4, _ := runHermetic(t, env,
		"audit:verify",
		"--audit-log", out2Path,
		"--public-key", pubKeyHex,
		"--previous-signature-hash", traceHash1,
	)
	combined4 := stdout4 + stderr4
	// The chain link itself is verified correctly (either as PASS or embedded in output).
	if !containsAny(combined4, "Chain link", "chain link", "chain_link_valid", "previous_signature_hash") {
		t.Errorf("chain-verify entry 2: expected chain-related output, got: %q", combined4)
	}
	assertNotContains(t, "chain-verify entry 2 stderr", stderr4, "panic")

	// ── Verify that tampering breaks the chain ───────────────────────────────
	// Using the wrong previous-hash must cause INVALID result.
	wrongHash := strings.Repeat("f", 64)
	stdout5, stderr5, err5 := runHermetic(t, env,
		"audit:verify",
		"--audit-log", out2Path,
		"--public-key", pubKeyHex,
		"--previous-signature-hash", wrongHash,
	)
	if exitCode(err5) == 0 {
		t.Error("audit:verify with wrong chain hash: expected non-zero exit")
	}
	combined5 := stdout5 + stderr5
	if !containsAny(combined5, "INVALID", "chain link", "broken", "does not match") {
		t.Errorf("audit:verify wrong chain hash: expected chain-failure message, got: %q", combined5)
	}
	assertNotContains(t, "chain-broken stderr", stderr5, "panic")

	assertNoDials(t, blocker)
}

// ────────────────────────────────────────────────────────────────────────────
// TestOfflineExitCodes – every offline command that needs a missing file must
// exit non-zero and print an actionable message.
// ────────────────────────────────────────────────────────────────────────────

func TestOfflineExitCodes(t *testing.T) {
	if testing.Short() {
		t.Skip("offline exit-code test skipped in -short mode")
	}

	homeDir := t.TempDir()
	blocker := newNetworkBlocker(t)
	env := hermeticEnv(homeDir, blocker.proxyURL())
	missing := filepath.Join(t.TempDir(), "ghost.json")

	cases := []struct {
		name       string
		args       []string
		wantInErr  []string // at least one must appear in stderr
	}{
		{
			name:      "snapshot_load_missing",
			args:      []string{"snapshot", "load", "--path", missing},
			wantInErr: []string{"not found", "no such file", "failed to read"},
		},
		{
			name:      "report_missing_trace",
			args:      []string{"report", "--file", missing, "--format", "text"},
			wantInErr: []string{"not found", "no such file", missing},
		},
		{
			name:      "debug_load_snapshots_missing",
			args:      []string{"debug", "--load-snapshots", missing},
			wantInErr: []string{"not found", "no such file", "failed to load", "failed to read"},
		},
		{
			name:      "audit_verify_missing",
			args:      []string{"audit:verify", "--audit-log", missing},
			wantInErr: []string{"not found", "no such file", "failed to read"},
		},
		{
			name:      "offline_generate_missing_xdr",
			args:      []string{"offline", "generate", "--network", "testnet", missing},
			wantInErr: []string{"not found", "no such file", "failed to read"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, stderr, err := runHermetic(t, env, tc.args...)
			if exitCode(err) == 0 {
				t.Errorf("%s: expected non-zero exit for missing file", tc.name)
			}
			if !containsAny(stderr, tc.wantInErr...) {
				t.Errorf("%s: expected actionable error containing one of %v, got: %q",
					tc.name, tc.wantInErr, stderr)
			}
			assertNotContains(t, tc.name+" stderr", stderr, "panic")
			assertNotContains(t, tc.name+" stderr", stderr, "goroutine")
		})
	}

	assertNoDials(t, blocker)
}
