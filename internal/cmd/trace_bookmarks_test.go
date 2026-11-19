// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Tests for Issue #562: bookmark identity and merge behavior, CLI wiring.

package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dotandev/glassbox/internal/trace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeTestTraceFile(t *testing.T, tr *trace.ExecutionTrace) string {
	t.Helper()
	data, err := json.Marshal(tr)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "trace.json")
	require.NoError(t, os.WriteFile(path, data, 0o644))
	return path
}

func testTrace(txHash string, n int) *trace.ExecutionTrace {
	tr := trace.NewExecutionTrace(txHash, 100)
	tr.StartTime = time.Now()
	tr.EndTime = tr.StartTime.Add(time.Second)
	for i := 0; i < n; i++ {
		tr.AddState(trace.ExecutionState{
			Step:       i,
			Operation:  "invoke",
			EventType:  trace.EventTypeContractCall,
			ContractID: "CONTRACT" + string(rune('A'+i)),
			Function:   "fn",
		})
	}
	return tr
}

func TestTraceCmd_BookmarksOnConflictFlag_DefaultIsFail(t *testing.T) {
	flag := traceCmd.Flags().Lookup("bookmarks-on-conflict")
	require.NotNil(t, flag)
	assert.Equal(t, "fail", flag.DefValue)

	assert.NotNil(t, traceCmd.Flags().Lookup("bookmarks-preview"))
}

func TestTraceCmd_ImportBookmarks_NoConflict_MergesAndExports(t *testing.T) {
	t.Cleanup(resetTraceFlags)

	tr := testTrace("tx-bookmarks-1", 5)
	tracePath := writeTestTraceFile(t, tr)

	bm, err := trace.NewBookmark("bm1", "entrypoint", tr, 3)
	require.NoError(t, err)
	annotationsPath := filepath.Join(t.TempDir(), "in.json")
	require.NoError(t, (&trace.AnnotationFile{Bookmarks: []trace.Bookmark{bm}}).Save(annotationsPath))

	exportPath := filepath.Join(t.TempDir(), "out.json")

	traceAnnotationsFlag = annotationsPath
	traceAnnotationsExportPath = exportPath
	traceBookmarksOnConflict = "fail"

	var out bytes.Buffer
	traceCmd.SetOut(&out)
	traceCmd.SetErr(&out)
	err = traceCmd.RunE(traceCmd, []string{tracePath})
	require.NoError(t, err)

	exported, err := trace.LoadAnnotationFile(exportPath)
	require.NoError(t, err)
	require.Len(t, exported.Bookmarks, 1)
	assert.Equal(t, "entrypoint", exported.Bookmarks[0].Name)
}

func TestTraceCmd_ImportBookmarks_ConflictWithFailPolicy_Errors(t *testing.T) {
	t.Cleanup(resetTraceFlags)

	tr := testTrace("tx-bookmarks-2", 5)
	// The trace file itself already carries a bookmark at step 0.
	existing, err := trace.NewBookmark("bm1", "existing-entry", tr, 0)
	require.NoError(t, err)
	tr.Annotations.Bookmarks = []trace.Bookmark{existing}
	tracePath := writeTestTraceFile(t, tr)

	// Incoming file reuses the same ID but points at a different step.
	incoming, err := trace.NewBookmark("bm1", "colleagues-entry", tr, 4)
	require.NoError(t, err)
	annotationsPath := filepath.Join(t.TempDir(), "in.json")
	require.NoError(t, (&trace.AnnotationFile{Bookmarks: []trace.Bookmark{incoming}}).Save(annotationsPath))

	traceAnnotationsFlag = annotationsPath
	traceBookmarksOnConflict = "fail"

	err = traceCmd.RunE(traceCmd, []string{tracePath})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bookmark import conflict")
}

func TestTraceCmd_ImportBookmarks_ConflictWithRenamePolicy_KeepsBoth(t *testing.T) {
	t.Cleanup(resetTraceFlags)

	tr := testTrace("tx-bookmarks-3", 5)
	existing, err := trace.NewBookmark("bm1", "existing-entry", tr, 0)
	require.NoError(t, err)
	tr.Annotations.Bookmarks = []trace.Bookmark{existing}
	tracePath := writeTestTraceFile(t, tr)

	incoming, err := trace.NewBookmark("bm1", "colleagues-entry", tr, 4)
	require.NoError(t, err)
	annotationsPath := filepath.Join(t.TempDir(), "in.json")
	require.NoError(t, (&trace.AnnotationFile{Bookmarks: []trace.Bookmark{incoming}}).Save(annotationsPath))

	exportPath := filepath.Join(t.TempDir(), "out.json")

	traceAnnotationsFlag = annotationsPath
	traceAnnotationsExportPath = exportPath
	traceBookmarksOnConflict = "rename"

	err = traceCmd.RunE(traceCmd, []string{tracePath})
	require.NoError(t, err)

	exported, err := trace.LoadAnnotationFile(exportPath)
	require.NoError(t, err)
	require.Len(t, exported.Bookmarks, 2, "both the existing and renamed incoming bookmark must be present")
}

func TestTraceCmd_BookmarksPreview_DoesNotApplyOrExport(t *testing.T) {
	t.Cleanup(resetTraceFlags)

	tr := testTrace("tx-bookmarks-4", 5)
	existing, err := trace.NewBookmark("bm1", "existing-entry", tr, 0)
	require.NoError(t, err)
	tr.Annotations.Bookmarks = []trace.Bookmark{existing}
	tracePath := writeTestTraceFile(t, tr)

	incoming, err := trace.NewBookmark("bm1", "colleagues-entry", tr, 4)
	require.NoError(t, err)
	annotationsPath := filepath.Join(t.TempDir(), "in.json")
	require.NoError(t, (&trace.AnnotationFile{Bookmarks: []trace.Bookmark{incoming}}).Save(annotationsPath))

	exportPath := filepath.Join(t.TempDir(), "out.json")

	traceAnnotationsFlag = annotationsPath
	traceAnnotationsExportPath = exportPath
	traceBookmarksOnConflict = "fail"
	traceBookmarksPreview = true

	var out bytes.Buffer
	traceCmd.SetOut(&out)
	err = traceCmd.RunE(traceCmd, []string{tracePath})
	require.NoError(t, err)
	assert.Contains(t, out.String(), "conflict")

	_, statErr := os.Stat(exportPath)
	assert.True(t, os.IsNotExist(statErr), "--bookmarks-preview must not write the export file")
}
