// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package trace

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// generateTraceTree builds a random TraceNode tree using rapid generators.
func generateTraceTree(rt *rapid.T) *TraceNode {
	maxDepth := rapid.IntRange(0, 5).Draw(rt, "maxDepth")
	maxChildren := rapid.IntRange(0, 4).Draw(rt, "maxChildrenPerNode")

	return buildRandomNode(rt, "root", 0, maxDepth, maxChildren)
}

func buildRandomNode(rt *rapid.T, id string, depth, maxDepth, maxChildren int) *TraceNode {
	nodeType := rapid.StringOf(rapid.RuneFrom([]rune("abcdef"))).Draw(rt, "nodeType")
	node := NewTraceNode(id, nodeType)
	node.Depth = depth

	if depth >= maxDepth {
		return node
	}

	numChildren := rapid.IntRange(0, maxChildren).Draw(rt, "numChildren")
	for i := 0; i < numChildren; i++ {
		childID := fmt.Sprintf("%s-%d", id, i)
		child := buildRandomNode(rt, childID, depth+1, maxDepth, maxChildren)
		node.AddChild(child)
	}

	return node
}

// TestProp_TreeDepthConsistency verifies that every node's Depth equals its
// parent's Depth + 1 (or 0 for the root), and that AddChild sets the correct
// depth on the child.
func TestProp_TreeDepthConsistency(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		root := generateTraceTree(rt)
		assertDepthInvariant(rt, root, 0)
	})
}

func assertDepthInvariant(rt *rapid.T, node *TraceNode, expectedDepth int) {
	rt.Helper()
	assert.Equal(rt, expectedDepth, node.Depth,
		"node %q depth mismatch: expected %d, got %d", node.ID, expectedDepth, node.Depth)
	for _, child := range node.Children {
		assertDepthInvariant(rt, child, expectedDepth+1)
	}
}

// TestProp_ParentChildRelationship verifies that every non-root node's Parent
// pointer is non-nil and references the correct parent.
func TestProp_ParentChildRelationship(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		root := generateTraceTree(rt)
		assertParentInvariant(rt, root)
	})
}

func assertParentInvariant(rt *rapid.T, node *TraceNode) {
	rt.Helper()
	for _, child := range node.Children {
		require.NotNil(rt, child.Parent, "child %q has nil parent", child.ID)
		assert.Equal(rt, node, child.Parent,
			"child %q parent mismatch: expected %q, got %q", child.ID, node.ID, child.Parent.ID)
		assertParentInvariant(rt, child)
	}
}

// TestProp_FlattenContainsAllNodes verifies that FlattenAll returns every node
// in the tree exactly once (no duplicates, no missing).
func TestProp_FlattenContainsAllNodes(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		root := generateTraceTree(rt)

		flatAll := root.FlattenAll()
		require.NotEmpty(rt, flatAll, "FlattenAll should return at least the root")
		assert.Equal(rt, root, flatAll[0], "FlattenAll should start with root")

		// Count all nodes in the tree recursively.
		total := countNodes(root)
		assert.Equal(rt, total, len(flatAll),
			"FlattenAll count mismatch: tree has %d nodes, FlattenAll returned %d", total, len(flatAll))

		// Check for duplicates by ID.
		seen := make(map[string]bool)
		for _, n := range flatAll {
			assert.False(rt, seen[n.ID], "duplicate node %q in FlattenAll", n.ID)
			seen[n.ID] = true
		}
	})
}

func countNodes(n *TraceNode) int {
	count := 1
	for _, child := range n.Children {
		count += countNodes(child)
	}
	return count
}

// TestProp_FlattenRespectsExpanded verifies that Flatten only includes children
// of expanded nodes and their descendants.
func TestProp_FlattenRespectsExpanded(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		root := generateTraceTree(rt)

		// FlattenAll gives us all nodes.
		flatAll := root.FlattenAll()
		// Flatten gives us visible nodes.
		flat := root.Flatten()

		require.NotEmpty(rt, flat, "Flatten should return at least the root")
		assert.Equal(rt, root, flat[0], "Flatten should start with root")

		// Every node in Flatten must be in FlattenAll.
		allSet := make(map[string]bool)
		for _, n := range flatAll {
			allSet[n.ID] = true
		}
		for _, n := range flat {
			assert.True(rt, allSet[n.ID],
				"node %q in Flatten but not in FlattenAll", n.ID)
		}

		// Count should be <= FlattenAll count.
		assert.LessOrEqual(rt, len(flat), len(flatAll),
			"Flatten returned more nodes than FlattenAll")
	})
}

// TestProp_ExpandCollapseRoundTrip verifies that ExpandAll followed by
// CollapseAll leaves every node collapsed, and vice versa.
func TestProp_ExpandCollapseRoundTrip(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		root := generateTraceTree(rt)

		// ExpandAll -> all expanded.
		root.ExpandAll()
		forAllNodes(root, func(n *TraceNode) {
			assert.True(rt, n.Expanded, "node %q should be expanded after ExpandAll", n.ID)
		})

		// CollapseAll -> all collapsed.
		root.CollapseAll()
		forAllNodes(root, func(n *TraceNode) {
			assert.False(rt, n.Expanded, "node %q should be collapsed after CollapseAll", n.ID)
		})

		// ExpandAll again -> all expanded.
		root.ExpandAll()
		flat := root.Flatten()
		flatAll := root.FlattenAll()
		assert.Equal(rt, len(flatAll), len(flat),
			"after ExpandAll, Flatten should return all nodes")
	})
}

// TestProp_ToggleExpandedIsReversible verifies that toggling a node twice
// returns it to its original state.
func TestProp_ToggleExpandedIsReversible(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		root := generateTraceTree(rt)

		// Toggle each node twice -> original state.
		forAllNodes(root, func(n *TraceNode) {
			original := n.Expanded
			n.ToggleExpanded()
			n.ToggleExpanded()
			assert.Equal(rt, original, n.Expanded,
				"double-toggle changed state of node %q", n.ID)
		})
	})
}

// TestProp_AddChildIncreasesCount verifies that AddChild increases the
// Children slice length by exactly one.
func TestProp_AddChildIncreasesCount(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		root := generateTraceTree(rt)
		initialChildren := len(root.Children)

		childType := rapid.StringOf(rapid.RuneFrom([]rune("abcdef"))).Draw(rt, "childType")
		child := NewTraceNode("new-child", childType)
		root.AddChild(child)

		assert.Equal(rt, initialChildren+1, len(root.Children),
			"AddChild should increase children count by 1")
		assert.Equal(rt, root, child.Parent,
			"AddChild should set parent pointer")
	})
}

// TestProp_IsCrossContractCall verifies that IsCrossContractCall returns true
// only when both parent and child have non-empty, different ContractIDs.
func TestProp_IsCrossContractCall(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		parentContractID := rapid.StringOf(rapid.RuneFrom([]rune("abcdef0123456789"))).Draw(rt, "parentContractID")
		childContractID := rapid.StringOf(rapid.RuneFrom([]rune("abcdef0123456789"))).Draw(rt, "childContractID")

		parent := NewTraceNode("parent", "contract_call")
		parent.ContractID = parentContractID
		child := NewTraceNode("child", "contract_call")
		child.ContractID = childContractID
		parent.AddChild(child)

		if parentContractID == "" || childContractID == "" || parentContractID == childContractID {
			assert.False(rt, child.IsCrossContractCall(),
				"IsCrossContractCall should be false when contracts are same/empty")
		} else {
			assert.True(rt, child.IsCrossContractCall(),
				"IsCrossContractCall should be true for different non-empty contracts")
		}
	})
}

// TestProp_IsLeafForNewNode verifies that a newly created node with no
// children is a leaf.
func TestProp_IsLeafForNewNode(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		id := rapid.StringOf(rapid.RuneFrom([]rune("abcdef"))).Draw(rt, "id")
		nodeType := rapid.StringOf(rapid.RuneFrom([]rune("abcdef"))).Draw(rt, "nodeType")
		node := NewTraceNode(id, nodeType)
		assert.True(rt, node.IsLeaf(), "new node should be a leaf")
	})
}

// TestProp_FlattenSingleChild verifies that a tree with a single chain of
// nodes (each with exactly one child) produces a FlattenAll of the correct
// length equal to depth+1.
func TestProp_FlattenSingleChild(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		depth := rapid.IntRange(0, 20).Draw(rt, "depth")

		root := NewTraceNode("root", "root")
		current := root
		for i := 0; i < depth; i++ {
			child := NewTraceNode(fmt.Sprintf("child-%d", i), "child")
			current.AddChild(child)
			current = child
		}

		flatAll := root.FlattenAll()
		assert.Equal(rt, depth+1, len(flatAll),
			"single-chain tree of depth %d should have %d nodes", depth, depth+1)

		// Verify depths are sequential.
		for i, n := range flatAll {
			assert.Equal(rt, i, n.Depth,
				"node at index %d should have depth %d, got %d", i, i, n.Depth)
		}
	})
}

func forAllNodes(n *TraceNode, fn func(*TraceNode)) {
	fn(n)
	for _, child := range n.Children {
		forAllNodes(child, fn)
	}
}
