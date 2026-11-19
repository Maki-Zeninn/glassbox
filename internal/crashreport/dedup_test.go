// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package crashreport

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// isolate overrides the HOME directory so each test gets its own suppress file.
func isolate(t *testing.T) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
}

// ── ErrorClass ────────────────────────────────────────────────────────────────

func TestErrorClass_BasicMessage(t *testing.T) {
	class := ErrorClass("open /home/user/secret.pem: no such file or directory")
	// Should contain the verb but not the path.
	assert.NotContains(t, class, "/home")
	assert.NotContains(t, class, "user")
	assert.Contains(t, class, "open")
}

func TestErrorClass_MemoryAddress(t *testing.T) {
	class := ErrorClass("runtime error: invalid memory address or nil pointer dereference at 0xc000123456")
	assert.NotContains(t, class, "0xc000123456")
}

func TestErrorClass_Empty(t *testing.T) {
	assert.Equal(t, "unknown", ErrorClass(""))
}

func TestErrorClass_LongMessage(t *testing.T) {
	long := strings.Repeat("a", 300)
	class := ErrorClass(long)
	assert.LessOrEqual(t, len(class), 256)
}

// ── NormalisedStack ───────────────────────────────────────────────────────────

func TestNormalisedStack_RemovesGoroutineHeader(t *testing.T) {
	stack := "goroutine 1 [running]:\nfoo.Bar()\n\tfoo/bar.go:42\n"
	norm := NormalisedStack(stack)
	assert.NotContains(t, norm, "goroutine 1")
}

func TestNormalisedStack_RemovesLineNumbers(t *testing.T) {
	stack := "foo.Bar()\n\t/home/user/go/src/foo/bar.go:42\n"
	norm := NormalisedStack(stack)
	assert.NotContains(t, norm, ":42")
}

func TestNormalisedStack_ReducesPathToBasename(t *testing.T) {
	stack := "foo.Bar()\n\t/home/user/project/internal/foo/bar.go:10\n"
	norm := NormalisedStack(stack)
	assert.NotContains(t, norm, "/home/user")
	assert.Contains(t, norm, "bar.go")
}

func TestNormalisedStack_RemovesAddresses(t *testing.T) {
	stack := "main.main()\n\t/app/main.go:20 +0x7fa3\n"
	norm := NormalisedStack(stack)
	assert.NotContains(t, norm, "0x7fa3")
}

func TestNormalisedStack_EmptyInput(t *testing.T) {
	assert.Equal(t, "", NormalisedStack(""))
}

// ── Fingerprint ───────────────────────────────────────────────────────────────

func TestFingerprint_SameLogicalCrash_SameFingerprint(t *testing.T) {
	// Two equivalent stack traces that differ only in goroutine ID, line number,
	// and memory addresses.
	stack1 := "goroutine 12 [running]:\nmain.run()\n\t/home/alice/project/main.go:42 +0xabc\n"
	stack2 := "goroutine 99 [running]:\nmain.run()\n\t/home/bob/project/main.go:42 +0xdef\n"

	fp1 := Fingerprint("rpc timeout", stack1)
	fp2 := Fingerprint("rpc timeout", stack2)

	assert.Equal(t, fp1, fp2, "equivalent crashes should produce the same fingerprint")
}

func TestFingerprint_DistinctCrashes_DifferentFingerprints(t *testing.T) {
	stack := "goroutine 1 [running]:\nmain.run()\n\t/app/main.go:10\n"

	fp1 := Fingerprint("rpc timeout", stack)
	fp2 := Fingerprint("nil pointer dereference", stack)
	fp3 := Fingerprint("rpc timeout", "goroutine 1 [running]:\nmain.other()\n\t/app/other.go:20\n")

	assert.NotEqual(t, fp1, fp2, "different errors should have different fingerprints")
	assert.NotEqual(t, fp1, fp3, "different stack frames should have different fingerprints")
}

func TestFingerprint_Length(t *testing.T) {
	fp := Fingerprint("some error", "some stack")
	// Should be 16 hex characters (8 bytes × 2 chars).
	assert.Len(t, fp, 16, "fingerprint should be 16 hex characters")
}

func TestFingerprint_NoRawSecrets(t *testing.T) {
	// Error message that might contain a path or key.
	errMsg := "open /etc/ssl/private/secret.key: permission denied"
	stack := "goroutine 1 [running]:\nreadKey()\n\t/app/tls/reader.go:55\n"

	fp := Fingerprint(errMsg, stack)
	// The fingerprint is an opaque hex string — it must not contain the raw path.
	assert.NotContains(t, fp, "secret.key")
	assert.NotContains(t, fp, "/etc")
}

// ── IsSuppressed ─────────────────────────────────────────────────────────────

func TestIsSuppressed_FirstReport_NotSuppressed(t *testing.T) {
	isolate(t)
	require.NoError(t, PurgeSuppressStore())

	assert.False(t, IsSuppressed("test error", "goroutine 1\nmain.run()\n\tmain.go"),
		"first report should not be suppressed")
}

func TestIsSuppressed_BelowLimit_NotSuppressed(t *testing.T) {
	isolate(t)
	require.NoError(t, PurgeSuppressStore())

	errMsg := "repeated error below limit"
	stack := "goroutine 1\nmain.run()\n\tmain.go"

	for i := 0; i < MaxReportsPerFingerprint; i++ {
		assert.False(t, IsSuppressed(errMsg, stack),
			"report %d/%d should not be suppressed", i+1, MaxReportsPerFingerprint)
	}
}

func TestIsSuppressed_AtLimit_Suppressed(t *testing.T) {
	isolate(t)
	require.NoError(t, PurgeSuppressStore())

	errMsg := "error reaching limit"
	stack := "goroutine 1\nmain.atLimit()\n\tmain.go"

	// Exhaust the limit.
	for i := 0; i < MaxReportsPerFingerprint; i++ {
		IsSuppressed(errMsg, stack)
	}

	// Next call should be suppressed.
	assert.True(t, IsSuppressed(errMsg, stack),
		"report exceeding MaxReportsPerFingerprint should be suppressed")
}

func TestIsSuppressed_DifferentErrors_IndependentCounters(t *testing.T) {
	isolate(t)
	require.NoError(t, PurgeSuppressStore())

	stack := "goroutine 1\nmain.run()\n\tmain.go"

	// Exhaust limit for error A.
	for i := 0; i < MaxReportsPerFingerprint; i++ {
		IsSuppressed("error-A", stack)
	}
	assert.True(t, IsSuppressed("error-A", stack), "error-A should be suppressed")

	// Error B must not be affected by error A's counter.
	assert.False(t, IsSuppressed("error-B", stack), "error-B should not be suppressed")
}

func TestIsSuppressedReport_EquivalentReports(t *testing.T) {
	isolate(t)
	require.NoError(t, PurgeSuppressStore())

	r := Report{
		ErrorMessage: "test dedup",
		StackTrace:   "goroutine 1\nmain.run()\n\tmain.go",
	}

	for i := 0; i < MaxReportsPerFingerprint; i++ {
		IsSuppressedReport(r)
	}
	assert.True(t, IsSuppressedReport(r))
}

// ── PurgeSuppressStore ────────────────────────────────────────────────────────

func TestPurgeSuppressStore_ClearsCounters(t *testing.T) {
	isolate(t)

	errMsg := "purge test"
	stack := "goroutine 1\nmain.purge()\n\tmain.go"

	for i := 0; i < MaxReportsPerFingerprint+1; i++ {
		IsSuppressed(errMsg, stack)
	}

	require.NoError(t, PurgeSuppressStore())
	assert.False(t, IsSuppressed(errMsg, stack), "after purge counter should reset")
}

func TestPurgeSuppressStore_IdempotentWhenMissing(t *testing.T) {
	isolate(t)
	// File does not exist yet.
	assert.NoError(t, PurgeSuppressStore())
}

// ── GetDedupStats ─────────────────────────────────────────────────────────────

func TestGetDedupStats_ReflectsActiveSuppressions(t *testing.T) {
	isolate(t)
	require.NoError(t, PurgeSuppressStore())

	for i := 0; i < MaxReportsPerFingerprint+1; i++ {
		IsSuppressed("stats error", "goroutine 1\nmain.stats()\n\tmain.go")
	}

	stats := GetDedupStats()
	assert.GreaterOrEqual(t, stats.UniqueFingerprints, 1)
	assert.GreaterOrEqual(t, stats.ActiveSuppressed, 1)
}

// ── Thread safety ─────────────────────────────────────────────────────────────

func TestIsSuppressed_ConcurrentSafe(t *testing.T) {
	isolate(t)
	require.NoError(t, PurgeSuppressStore())

	const goroutines = 10
	var wg sync.WaitGroup
	errMsgs := make([]string, goroutines)
	for i := range errMsgs {
		errMsgs[i] = fmt.Sprintf("concurrent error %d", i)
	}

	for _, msg := range errMsgs {
		wg.Add(1)
		go func(m string) {
			defer wg.Done()
			for i := 0; i < MaxReportsPerFingerprint+2; i++ {
				IsSuppressed(m, "goroutine 1\nmain.concurrent()\n\tmain.go")
			}
		}(msg)
	}
	wg.Wait()

	stats := GetDedupStats()
	assert.Equal(t, goroutines, stats.UniqueFingerprints)
}

// ── Privacy: no raw secrets in suppress file ──────────────────────────────────

func TestSuppressFile_ContainsOnlyFingerprints(t *testing.T) {
	isolate(t)
	require.NoError(t, PurgeSuppressStore())

	sensitiveErrMsg := "open /home/user/.ssh/id_rsa: permission denied"
	stack := "goroutine 1\nmain.open()\n\tmain.go"

	IsSuppressed(sensitiveErrMsg, stack)

	path := SuppressFilePath()
	require.NotEmpty(t, path)

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	// The suppress file must not contain the raw sensitive strings.
	assert.NotContains(t, string(data), "/home/user/.ssh")
	assert.NotContains(t, string(data), "id_rsa")
	assert.NotContains(t, string(data), "permission denied")
}
