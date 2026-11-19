// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package ui

import (
	"fmt"
	"strings"

	"github.com/dotandev/glassbox/internal/trace"
)

// StateRow is a single key-value pair shown in the right pane.
type StateRow struct {
	Key   string
	Value string
}

// TraceView is the split-screen controller that wires a trace.TreeRenderer
// (left pane) to a state table (right pane).
//
// Search is integrated directly: pressing / enters search-input mode, typing
// builds the query, Enter commits it, n/N navigate matches, and Ctrl-F
// toggles filter mode. The match counter is shown in the status bar.
//
// Typical lifecycle:
//
//	tv := ui.NewTraceView(root)
//	layout := ui.NewSplitLayout()
//	resize := layout.ListenResize()
//	kr := ui.NewKeyReader()
//
//	for {
//	    tv.Render(layout)
//	    key, _ := kr.Read()
//	    if done := tv.HandleKey(key, layout); done { break }
//	    select {
//	    case <-resize:
//	        tv.Resize(layout.Width, layout.Height)
//	    default:
//	    }
//	}
type TraceView struct {
	tree        *trace.TreeRenderer
	root        *trace.TraceNode
	stateRows   []StateRow
	stateScroll int
	stateSel    int
	search      *trace.SearchState
}

func NewTraceView(root *trace.TraceNode) *TraceView {
	w, h := TermSize()
	tv := &TraceView{
		tree:   trace.NewTreeRenderer(w/2, h-3),
		root:   root,
		search: trace.NewSearchState(),
	}
	tv.tree.RenderTree(root)
	// Index all nodes for search.
	tv.search.IndexNodes(root.FlattenAll())
	tv.refreshState()
	return tv
}

func (tv *TraceView) Resize(w, h int) {
	tv.tree = trace.NewTreeRenderer(w/2, h-3)
}

func (tv *TraceView) HandleKey(k Key, layout *SplitLayout) (done bool) {
	// While in search-input mode, most keys feed the query buffer.
	if tv.search.Mode() == trace.SearchModeInput {
		return tv.handleSearchInputKey(k, layout)
	}

	switch k {
	case KeyQuit:
		return true

	case KeyTab:
		layout.ToggleFocus()

	case KeyLeft:
		layout.SetFocus(PaneTrace)

	case KeyRight:
		layout.SetFocus(PaneState)

	case KeyUp:
		if layout.Focus == PaneTrace {
			tv.tree.SelectUp()
			tv.refreshState()
		} else {
			tv.stateScrollUp()
		}

	case KeyDown:
		if layout.Focus == PaneTrace {
			tv.tree.SelectDown()
			tv.refreshState()
		} else {
			tv.stateScrollDown()
		}

	case KeyEnter:
		if layout.Focus == PaneTrace {
			if node := tv.tree.GetSelectedNode(); node != nil {
				node.ToggleExpanded()
				tv.tree.RenderTree(tv.root)
				tv.refreshState()
			}
		}

	case KeySlash:
		// Enter search-input mode.
		tv.search.BeginInput()

	case KeyEscape:
		// Clear any active search.
		tv.search.ClearSearch()
		tv.applyFilter()

	case KeyNextMatch:
		tv.navigateNextMatch()

	case KeyPrevMatch:
		tv.navigatePrevMatch()

	case KeyFilterToggle:
		tv.search.ToggleFilterMode()
		tv.applyFilter()
	}

	return false
}

// handleSearchInputKey handles key presses while the user is typing a query.
func (tv *TraceView) handleSearchInputKey(k Key, layout *SplitLayout) bool {
	switch k {
	case KeyEnter:
		// Commit the query.
		count := tv.search.CommitQuery()
		if count > 0 {
			// Jump to first match in the tree.
			if m := tv.search.CurrentMatch(); m != nil && m.NodeData != nil {
				tv.tree.SelectNodeByID(m.NodeData.ID)
				tv.refreshState()
			}
		}
		tv.applyFilter()
	case KeyEscape:
		tv.search.ClearSearch()
		tv.applyFilter()
	case KeyBackspace:
		tv.search.Backspace()
	case KeyQuit:
		// 'q' while typing should append to buffer, not quit.
		tv.search.AppendChar('q')
	default:
		// All other keys append their rune to the buffer.
		if r := keyToRune(k); r != 0 {
			tv.search.AppendChar(r)
		}
	}
	return false
}

// navigateNextMatch moves to the next search match in the tree.
func (tv *TraceView) navigateNextMatch() {
	m := tv.search.NextMatch()
	if m == nil || m.NodeData == nil {
		return
	}
	tv.tree.SelectNodeByID(m.NodeData.ID)
	tv.refreshState()
}

// navigatePrevMatch moves to the previous search match in the tree.
func (tv *TraceView) navigatePrevMatch() {
	m := tv.search.PrevMatch()
	if m == nil || m.NodeData == nil {
		return
	}
	tv.tree.SelectNodeByID(m.NodeData.ID)
	tv.refreshState()
}

// applyFilter re-renders the tree respecting the current filter mode.
func (tv *TraceView) applyFilter() {
	if tv.search.IsFiltering() {
		// Build a filtered root: only nodes visible under the current query.
		filtered := buildFilteredRoot(tv.root, tv.search)
		if filtered != nil {
			tv.tree.RenderTree(filtered)
		}
	} else {
		tv.tree.RenderTree(tv.root)
	}
	tv.refreshState()
}

// buildFilteredRoot returns a shallow copy of the tree containing only nodes
// that are visible under the current search filter (matched nodes + their
// ancestor branches for context). Returns nil when root is nil.
func buildFilteredRoot(root *trace.TraceNode, s *trace.SearchState) *trace.TraceNode {
	if root == nil {
		return nil
	}
	if !s.IsNodeVisible(root) {
		return nil
	}
	// Clone the node without children, then recursively add visible children.
	clone := trace.NewTraceNode(root.ID, root.Type)
	clone.ContractID = root.ContractID
	clone.Function = root.Function
	clone.Error = root.Error
	clone.EventData = root.EventData
	clone.Depth = root.Depth
	clone.Expanded = root.Expanded
	clone.SourceRef = root.SourceRef
	clone.CPUDelta = root.CPUDelta
	clone.MemoryDelta = root.MemoryDelta

	for _, child := range root.Children {
		if filtered := buildFilteredRoot(child, s); filtered != nil {
			clone.AddChild(filtered)
		}
	}
	return clone
}

func (tv *TraceView) Render(layout *SplitLayout) {
	lw := layout.LeftWidth()
	rw := layout.RightWidth()
	contentRows := layout.Height - 3

	leftLines := tv.renderTraceLines(lw, contentRows)
	rightLines := tv.renderStateLines(rw, contentRows)

	layout.RenderWithSearch(leftLines, rightLines, tv.search.MatchCounterLine())
}

// ──────────────────────────────────────────────────────────────────────────────
// Left pane — Trace tree
// ──────────────────────────────────────────────────────────────────────────────

func (tv *TraceView) renderTraceLines(width, maxRows int) []string {
	raw := tv.tree.Render()
	all := strings.Split(raw, "\n")

	lines := make([]string, maxRows)
	for i := 0; i < maxRows; i++ {
		text := ""
		if i < len(all) {
			text = all[i]
		}
		lines[i] = padOrClip(text, width)
	}
	return lines
}

// ──────────────────────────────────────────────────────────────────────────────
// Right pane — State table
// ──────────────────────────────────────────────────────────────────────────────

func (tv *TraceView) refreshState() {
	node := tv.tree.GetSelectedNode()
	tv.stateRows = nodeToStateRows(node)
	if tv.stateSel >= len(tv.stateRows) {
		tv.stateSel = len(tv.stateRows) - 1
	}
	if tv.stateSel < 0 {
		tv.stateSel = 0
	}
	tv.stateScroll = 0
}

func (tv *TraceView) stateScrollUp() {
	if tv.stateSel > 0 {
		tv.stateSel--
	}
	if tv.stateSel < tv.stateScroll {
		tv.stateScroll = tv.stateSel
	}
}

func (tv *TraceView) stateScrollDown() {
	if tv.stateSel < len(tv.stateRows)-1 {
		tv.stateSel++
	}
}

func (tv *TraceView) renderStateLines(width, maxRows int) []string {
	lines := make([]string, maxRows)

	keyW := width / 3
	valW := width - keyW - 3
	if keyW < 4 {
		keyW = 4
	}
	if valW < 4 {
		valW = 4
	}
	header := fmt.Sprintf("  %-*s  %s", keyW, "Key", "Value")
	lines[0] = padOrClip(header, width)

	divider := "  " + strings.Repeat("─", width-2)
	lines[1] = padOrClip(divider, width)

	visStart := tv.stateScroll
	row := 2
	for i := visStart; i < len(tv.stateRows) && row < maxRows; i++ {
		sr := tv.stateRows[i]
		prefix := "  "
		if i == tv.stateSel {
			prefix = "▸ "
		}
		key := padOrClip(sr.Key, keyW)
		val := padOrClip(sr.Value, valW)
		line := fmt.Sprintf("%s%-*s  %s", prefix, keyW, key, val)
		lines[row] = padOrClip(line, width)
		row++
	}

	for ; row < maxRows; row++ {
		lines[row] = strings.Repeat(" ", width)
	}

	if len(tv.stateRows) == 0 {
		msg := "  (no state for selected node)"
		lines[2] = padOrClip(msg, width)
	}

	return lines
}

// nodeToStateRows converts a TraceNode into display rows for the state table.
func nodeToStateRows(node *trace.TraceNode) []StateRow {
	if node == nil {
		return nil
	}
	var rows []StateRow

	add := func(k, v string) {
		rows = append(rows, StateRow{Key: k, Value: v})
	}

	add("type", node.Type)
	if node.ContractID != "" {
		add("contract_id", node.ContractID)
	}
	if node.Function != "" {
		add("function", node.Function)
	}
	add("depth", fmt.Sprintf("%d", node.Depth))
	if node.EventData != "" {
		add("event_data", node.EventData)
	}
	if node.Error != "" {
		add("error", node.Error)
	}
	if node.CPUDelta != nil {
		add("cpu_delta", fmt.Sprintf("%d instructions", *node.CPUDelta))
	}
	if node.MemoryDelta != nil {
		add("mem_delta", fmt.Sprintf("%d bytes", *node.MemoryDelta))
	}
	if node.SourceRef != nil {
		ref := node.SourceRef
		loc := fmt.Sprintf("%s:%d", ref.File, ref.Line)
		if ref.Column > 0 {
			loc = fmt.Sprintf("%s:%d", loc, ref.Column)
		}
		// Append the origin class label for non-user frames so the detail
		// panel immediately shows whether the source is generated or external.
		if label := sourceOriginLabel(ref.OriginClass); label != "" {
			loc = loc + "  " + label
		}
		add("source", loc)
		if ref.Function != "" {
			add("src_function", ref.Function)
		}
	}
	add("children", fmt.Sprintf("%d", len(node.Children)))
	if node.IsLeaf() {
		add("leaf", "true")
	}
	if node.IsCrossContractCall() {
		add("cross_contract", "true")
	}

	return rows
}

// ──────────────────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────────────────

// sourceOriginLabel returns the display label for a SourceRef.OriginClass
// string. It mirrors the logic in internal/trace/formatter.go without
// importing the sourcemap package, avoiding an import cycle.
func sourceOriginLabel(class string) string {
	switch class {
	case "generated":
		return "[generated]"
	case "external":
		return "[external]"
	case "unknown":
		return "[unknown origin]"
	default:
		return ""
	}
}

func padOrClip(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if len(s) >= width {
		return s[:width]
	}
	return s + strings.Repeat(" ", width-len(s))
}

func treeRoot(n *trace.TraceNode) *trace.TraceNode {
	for n.Parent != nil {
		n = n.Parent
	}
	return n
}

// keyToRune maps a Key constant to its rune for search input buffering.
// Returns 0 for keys that have no printable representation.
func keyToRune(k Key) rune {
	switch k {
	case KeySlash:
		return '/'
	default:
		return 0
	}
}
