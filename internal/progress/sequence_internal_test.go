// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package progress

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAllPhasesInPhaseOrder(t *testing.T) {
	phases := []Phase{
		PhaseInit,
		PhaseFetch,
		PhaseSimulate,
		PhaseAnalyze,
		PhaseExport,
		PhaseDone,
	}

	seenIndexes := make(map[int]Phase, len(phases))
	for _, phase := range phases {
		index, ok := phaseOrder[phase]
		require.True(t, ok, "phase %q missing from phaseOrder", phase)

		previous, duplicate := seenIndexes[index]
		assert.False(t, duplicate, "phase %q and %q share phaseOrder index %d", previous, phase, index)
		seenIndexes[index] = phase
	}
}
