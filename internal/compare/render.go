// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package compare

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/dotandev/glassbox/internal/simulator"
	"github.com/dotandev/glassbox/internal/visualizer"
)

const (
	colWidth  = 52 // width of each column in the side-by-side table
	columnSep = " | "
)

// Render prints a human-readable side-by-side diff of a DiffResult to stdout.
// It uses the visualizer package for theme-aware colours.
func Render(result *DiffResult) {
	// Use a discard writer to effectively capture output and then print it.
	// For actual stdout output, this function calls RenderTo with nil.
	var buf bytes.Buffer
	RenderTo(result, &buf)
	fmt.Print(buf.String())
}

// RenderTo prints a human-readable side-by-side diff of a DiffResult to w.
// If w is nil, output goes to stdout.
func RenderTo(result *DiffResult, w io.Writer) {
	if result == nil {
		return
	}

	if w == nil {
		w = io.Discard
	}

	printHeader(w)

	// ── Status ────────────────────────────────────────────────────────────────
	fmt.Fprintln(w, sectionTitle("Execution Status"))
	renderStatus(w, result.StatusDiff)

	// ── Budget / Resource Usage ───────────────────────────────────────────────
	if result.BudgetDiff != nil {
		fmt.Fprintln(w)
		fmt.Fprintln(w, sectionTitle("Resource Usage (Local vs On-Chain)"))
		renderBudget(w, result.BudgetDiff)
	}

	// ── Raw Event Diff ────────────────────────────────────────────────────────
	if len(result.EventDiffs) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, sectionTitle("Event Log Diff"))
		renderEventDiffs(w, result.EventDiffs)
	}

	// ── Diagnostic Event Diff ─────────────────────────────────────────────────
	if len(result.DiagnosticDiffs) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, sectionTitle("Diagnostic Event Diff"))
		renderDiagnosticDiffs(w, result.DiagnosticDiffs)
	}

	// ── Divergent Call Paths ──────────────────────────────────────────────────
	if len(result.CallPathDivergences) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, sectionTitle("Divergent Call Paths"))
		renderCallPaths(w, result.CallPathDivergences)
	}

	// ── Summary ───────────────────────────────────────────────────────────────
	fmt.Fprintln(w)
	renderSummary(w, result)
}

// ─── internal renderers ───────────────────────────────────────────────────────

func printHeader(w io.Writer) {
	sep := strings.Repeat("─", colWidth*2+len(columnSep))
	fmt.Fprintln(w)
	fmt.Fprintln(w, visualizer.Colorize("╔"+strings.Repeat("═", len(sep))+"╗", "cyan"))
	title := "  COMPARE REPLAY  ─  Local WASM  vs  On-Chain WASM  "
	pad := len(sep) - len(title)
	if pad < 0 {
		pad = 0
	}
	fmt.Fprintf(w, visualizer.Colorize("║", "cyan")+"%s"+strings.Repeat(" ", pad)+visualizer.Colorize("║", "cyan")+"\n", title)
	fmt.Fprintln(w, visualizer.Colorize("╚"+strings.Repeat("═", len(sep))+"╝", "cyan"))
	fmt.Fprintln(w)
}

func sectionTitle(title string) string {
	line := "── " + title + " " + strings.Repeat("─", max(0, 60-len(title)))
	return visualizer.Colorize(line, "bold")
}

func renderStatus(w io.Writer, sd StatusDiff) {
	leftLabel := "LOCAL"
	rightLabel := "ON-CHAIN"
	fmt.Fprintf(w, "  %-*s%s%-*s\n", colWidth, leftLabel, columnSep, colWidth, rightLabel)
	fmt.Fprintf(w, "  %s\n", strings.Repeat("-", colWidth*2+len(columnSep)))

	localStatus := statusLine(sd.LocalStatus, sd.LocalError)
	onChainStatus := statusLine(sd.OnChainStatus, sd.OnChainError)

	if sd.Match {
		fmt.Fprintf(w, "  %-*s%s%-*s  %s\n",
			colWidth, localStatus, columnSep, colWidth, onChainStatus,
			visualizer.Colorize("[MATCH]", "green"))
	} else {
		fmt.Fprintf(w, "  %-*s%s%-*s  %s\n",
			colWidth, localStatus, columnSep, colWidth, onChainStatus,
			visualizer.Colorize("[DIFF]", "red"))
	}
}

func statusLine(status, errMsg string) string {
	s := status
	if errMsg != "" {
		s += " – " + truncate(errMsg, 30)
	}
	return s
}

func renderBudget(w io.Writer, bd *BudgetDiff) {
	fmt.Fprintf(w, "  %-22s  %-15s  %-15s  %s\n", "Metric", "Local", "On-Chain", "Delta")
	fmt.Fprintf(w, "  %s\n", strings.Repeat("-", 70))

	cpuDeltaStr := formatDelta(bd.CPUDelta)
	memDeltaStr := formatDelta(bd.MemoryDelta)
	opsDeltaStr := formatDeltaInt(bd.OpsDelta)

	fmt.Fprintf(w, "  %-22s  %-15d  %-15d  %s\n",
		"CPU Instructions", bd.LocalCPU, bd.OnChainCPU, colorizeDelta(cpuDeltaStr, bd.CPUDelta))
	fmt.Fprintf(w, "  %-22s  %-15d  %-15d  %s\n",
		"Memory Bytes", bd.LocalMem, bd.OnChainMem, colorizeDelta(memDeltaStr, bd.MemoryDelta))
	fmt.Fprintf(w, "  %-22s  %-15d  %-15d  %s\n",
		"Operations", bd.LocalOps, bd.OnChainOps, colorizeDelta(opsDeltaStr, int64(bd.OpsDelta)))
}

func renderEventDiffs(w io.Writer, diffs []EventDiff) {
	fmt.Fprintf(w, "  %-6s  %-*s%s%-*s\n", "#", colWidth, "LOCAL", columnSep, colWidth, "ON-CHAIN")
	fmt.Fprintf(w, "  %s\n", strings.Repeat("-", colWidth*2+len(columnSep)+8))

	for _, d := range diffs {
		localEvt := truncate(d.LocalEvent, colWidth)
		onChainEvt := truncate(d.OnChainEvent, colWidth)
		var marker string
		if d.Divergent {
			marker = visualizer.Colorize("[!]", "yellow") + " "
		} else {
			marker = visualizer.Colorize("[=]", "dim") + " "
		}
		fmt.Fprintf(w, "%s[%3d]  %-*s%s%-*s\n",
			marker, d.Index+1, colWidth, localEvt, columnSep, colWidth, onChainEvt)
	}
}

func renderDiagnosticDiffs(w io.Writer, diffs []DiagnosticDiff) {
	fmt.Fprintf(w, "  %-6s  %-*s%s%-*s\n", "#", colWidth, "LOCAL", columnSep, colWidth, "ON-CHAIN")
	fmt.Fprintf(w, "  %s\n", strings.Repeat("-", colWidth*2+len(columnSep)+8))

	for _, d := range diffs {
		localDesc := diagnosticSummary(d.Local)
		onChainDesc := diagnosticSummary(d.OnChain)

		var marker string
		switch {
		case d.DivergentPath:
			marker = visualizer.Colorize("[PATH]", "red") + " "
		case d.Divergent:
			marker = visualizer.Colorize("[DIFF]", "yellow") + " "
		default:
			marker = visualizer.Colorize("[=]   ", "dim") + " "
		}

		fmt.Fprintf(w, "%s[%3d]  %-*s%s%-*s\n",
			marker, d.Index+1, colWidth, truncate(localDesc, colWidth),
			columnSep, colWidth, truncate(onChainDesc, colWidth))

		// Show topic diff inline if both sides have the event but topics differ
		if d.Local != nil && d.OnChain != nil && d.Divergent && !d.DivergentPath {
			renderTopicDiff(w, d.Local.Topics, d.OnChain.Topics)
		}
	}
}

func renderTopicDiff(w io.Writer, local, onChain []string) {
	maxLen := len(local)
	if len(onChain) > maxLen {
		maxLen = len(onChain)
	}
	for i := 0; i < maxLen; i++ {
		var lt, ot string
		if i < len(local) {
			lt = local[i]
		}
		if i < len(onChain) {
			ot = onChain[i]
		}
		if lt != ot {
			fmt.Fprintf(w, "        %s topic[%d]: %q  →  %q\n",
				visualizer.Colorize("↳", "yellow"), i, lt, ot)
		}
	}
}

func renderCallPaths(w io.Writer, divs []CallPathDivergence) {
	for i, div := range divs {
		fmt.Fprintf(w, "  %s  Divergence #%d at event [%d]\n",
			visualizer.Colorize("[PATH]", "red"), i+1, div.EventIndex+1)
		fmt.Fprintf(w, "       Reason    : %s\n", div.Reason)
		fmt.Fprintf(w, "       Local     : %s\n", visualizer.Colorize(div.LocalSummary, "cyan"))
		fmt.Fprintf(w, "       On-Chain  : %s\n", visualizer.Colorize(div.OnChainSummary, "magenta"))
		fmt.Fprintln(w)
	}
}

func renderSummary(w io.Writer, result *DiffResult) {
	fmt.Fprintln(w, sectionTitle("Summary"))
	fmt.Fprintln(w)

	if !result.HasDivergence {
		fmt.Fprintf(w, "  %s  Local and on-chain execution are IDENTICAL\n", visualizer.Success())
	} else {
		fmt.Fprintf(w, "  %s  Divergence detected between local and on-chain execution\n", visualizer.Warning())
	}

	fmt.Fprintln(w)
	fmt.Fprintf(w, "  %-30s  %d\n", "Total events compared:", result.TotalEvents)
	fmt.Fprintf(w, "  %-30s  %s\n", "Identical events:",
		visualizer.Colorize(fmt.Sprintf("%d", result.IdenticalEvents), "green"))
	fmt.Fprintf(w, "  %-30s  %s\n", "Divergent events:",
		colorizeDivergentCount(result.DivergentEvents))
	fmt.Fprintf(w, "  %-30s  %s\n", "Call-path divergences:",
		colorizeDivergentCount(len(result.CallPathDivergences)))

	if result.BudgetDiff != nil {
		fmt.Fprintln(w)
		cpuPct := budgetDeltaPct(result.BudgetDiff.CPUDelta, result.BudgetDiff.OnChainCPU)
		memPct := budgetDeltaPct(result.BudgetDiff.MemoryDelta, result.BudgetDiff.OnChainMem)
		fmt.Fprintf(w, "  %-30s  %s\n", "CPU delta vs on-chain:", colorizePct(cpuPct))
		fmt.Fprintf(w, "  %-30s  %s\n", "Memory delta vs on-chain:", colorizePct(memPct))
	}

	fmt.Fprintln(w)
	sep := strings.Repeat("─", colWidth*2+len(columnSep))
	fmt.Fprintln(w, visualizer.Colorize(sep, "dim"))
}

// ─── formatting helpers ───────────────────────────────────────────────────────

func diagnosticSummary(e *simulator.DiagnosticEvent) string {
	if e == nil {
		return "<absent>"
	}
	cid := ""
	if e.ContractID != nil {
		cid = truncate(*e.ContractID, 12)
	}
	topics := ""
	if len(e.Topics) > 0 {
		topics = truncate(e.Topics[0], 16)
	}
	return fmt.Sprintf("%s/%s %s", e.EventType, cid, topics)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 3 {
		return s[:n]
	}
	return s[:n-3] + "..."
}

func formatDelta(v int64) string {
	if v > 0 {
		return fmt.Sprintf("+%d", v)
	}
	return fmt.Sprintf("%d", v)
}

func formatDeltaInt(v int) string {
	if v > 0 {
		return fmt.Sprintf("+%d", v)
	}
	return fmt.Sprintf("%d", v)
}

func colorizeDelta(s string, v int64) string {
	switch {
	case v > 0:
		return visualizer.Colorize(s, "yellow")
	case v < 0:
		return visualizer.Colorize(s, "green")
	default:
		return visualizer.Colorize(s, "dim")
	}
}

func colorizeDivergentCount(n int) string {
	if n == 0 {
		return visualizer.Colorize("0", "green")
	}
	return visualizer.Colorize(fmt.Sprintf("%d", n), "red")
}

func budgetDeltaPct(delta int64, base uint64) float64 {
	if base == 0 {
		return 0
	}
	return float64(delta) / float64(base) * 100.0
}

func colorizePct(pct float64) string {
	s := fmt.Sprintf("%.2f%%", pct)
	switch {
	case pct > 10:
		return visualizer.Colorize(s, "red")
	case pct > 0:
		return visualizer.Colorize(s, "yellow")
	case pct < 0:
		return visualizer.Colorize(s, "green")
	default:
		return visualizer.Colorize(s, "dim")
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// RenderCrossCheck prints a human-readable cross-check report to stdout.
// It highlights whether the local simulation result agrees with the network
// failure reason and surfaces any discrepancies.
func RenderCrossCheck(r *CrossCheckResult) {
	if r == nil {
		return
	}

	var buf bytes.Buffer
	RenderCrossCheckTo(r, &buf)
	fmt.Print(buf.String())
}

// RenderCrossCheckTo prints a human-readable cross-check report to w.
// If w is nil, output goes to stdout.
func RenderCrossCheckTo(r *CrossCheckResult, w io.Writer) {
	if r == nil {
		return
	}

	if w == nil {
		w = io.Discard
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, sectionTitle("Simulation vs Network Cross-Check"))
	fmt.Fprintln(w)

	if r.Match {
		fmt.Fprintf(w, "  %s  Categories agree: %s\n",
			visualizer.Success(), visualizer.Colorize(string(r.LocalCategory), "green"))
	} else {
		fmt.Fprintf(w, "  %s  Mismatch detected\n", visualizer.Warning())
		fmt.Fprintln(w)
		fmt.Fprintf(w, "  %-20s [%s]\n", "Local simulation:",
			visualizer.Colorize(string(r.LocalCategory), "cyan"))
		if r.LocalSummary != "" {
			fmt.Fprintf(w, "  %-20s %s\n", "", truncate(r.LocalSummary, colWidth))
		}
		fmt.Fprintln(w)
		fmt.Fprintf(w, "  %-20s [%s]\n", "Network report:",
			visualizer.Colorize(string(r.NetworkCategory), "magenta"))
		if r.NetworkReason != "" {
			fmt.Fprintf(w, "  %-20s %s\n", "", truncate(r.NetworkReason, colWidth))
		}

		if len(r.Discrepancies) > 0 {
			fmt.Fprintln(w)
			fmt.Fprintf(w, "  %s\n", visualizer.Colorize("Discrepancies:", "yellow"))
			for _, d := range r.Discrepancies {
				fmt.Fprintf(w, "    • %s\n", d)
			}
		}
	}

	fmt.Fprintln(w)
	fmt.Fprintf(w, "  %s\n", r.Explanation)
	fmt.Fprintln(w)
	sep := strings.Repeat("─", colWidth*2+len(columnSep))
	fmt.Fprintln(w, visualizer.Colorize(sep, "dim"))
}
