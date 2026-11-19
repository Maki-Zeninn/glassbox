// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"errors"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── Number grammar rejection ────────────────────────────────────────────────

func TestValidateCanonicalNumber_Rejects(t *testing.T) {
	cases := []struct {
		name string
		val  float64
	}{
		{"NaN", math.NaN()},
		{"positive Infinity", math.Inf(1)},
		{"negative Infinity", math.Inf(-1)},
		{"negative zero", math.Copysign(0, -1)},
		{"integer exceeds safe range +", maxSafeInteger + 1},
		{"integer exceeds safe range -", -(maxSafeInteger + 1)},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := validateCanonicalNumber(tc.val)
			require.Error(t, err)
			assert.True(t, errors.Is(err, ErrAmbiguousNumber), "expected ErrAmbiguousNumber, got: %v", err)
		})
	}
}

func TestValidateCanonicalNumber_Accepts(t *testing.T) {
	cases := []struct {
		name string
		val  float64
	}{
		{"zero", 0},
		{"positive integer", 42},
		{"negative integer", -7},
		{"max safe integer", maxSafeInteger},
		{"min safe integer", -maxSafeInteger},
		{"float 0.5", 0.5},
		{"float 3.14159", 3.14159},
		{"negative float", -1.5},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, validateCanonicalNumber(tc.val))
		})
	}
}

func TestValidateCanonicalNumberStr_Rejects(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"exponent lowercase e", "1e10"},
		{"exponent uppercase E", "1E10"},
		{"exponent with fraction", "1.5e-3"},
		{"exponent positive sign", "2e+4"},
		{"leading zeros", "007"},
		{"leading zero integer", "01"},
		{"leading zero negative", "-007"},
		{"negative zero integer", "-0"},
		{"negative zero float", "-0.0"},
		{"negative zero multi decimal", "-0.00"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := validateCanonicalNumberStr(tc.raw)
			require.Error(t, err, "expected error for raw=%q", tc.raw)
			assert.True(t, errors.Is(err, ErrAmbiguousNumber), "expected ErrAmbiguousNumber, got: %v", err)
		})
	}
}

func TestValidateCanonicalNumberStr_Accepts(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"zero", "0"},
		{"negative seven", "-7"},
		{"integer 42", "42"},
		{"float 0.5", "0.5"},
		{"float 3.14159", "3.14159"},
		{"negative float", "-1.5"},
		{"large safe integer", "9007199254740991"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, validateCanonicalNumberStr(tc.raw), "raw=%q", tc.raw)
		})
	}
}

// ─── canonicalJSON number encoding ───────────────────────────────────────────

func TestCanonicalJSON_RejectsNegativeZero(t *testing.T) {
	negZero := math.Copysign(0, -1)
	input := map[string]interface{}{"val": negZero}
	_, err := canonicalJSON(input)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAmbiguousNumber))
}

func TestCanonicalJSON_RejectsNaN(t *testing.T) {
	input := map[string]interface{}{"val": math.NaN()}
	_, err := canonicalJSON(input)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAmbiguousNumber))
}

func TestCanonicalJSON_RejectsInfinity(t *testing.T) {
	for _, v := range []float64{math.Inf(1), math.Inf(-1)} {
		input := map[string]interface{}{"val": v}
		_, err := canonicalJSON(input)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrAmbiguousNumber))
	}
}

func TestCanonicalJSON_IntegerNoDecimalPoint(t *testing.T) {
	// Whole-number floats must be encoded without a trailing ".0".
	input := map[string]interface{}{"n": float64(42)}
	got, err := canonicalJSON(input)
	require.NoError(t, err)
	assert.Equal(t, `{"n":42}`, string(got))
}

func TestCanonicalJSON_FloatPreservesDecimal(t *testing.T) {
	input := map[string]interface{}{"pi": float64(3.14159)}
	got, err := canonicalJSON(input)
	require.NoError(t, err)
	assert.Equal(t, `{"pi":3.14159}`, string(got))
}

// ─── Duplicate-key detection ─────────────────────────────────────────────────

func TestParseCanonicalInput_RejectsDuplicateKeys(t *testing.T) {
	// JSON with duplicate "a" key — last-writer-wins in most parsers,
	// which is ambiguous.
	raw := []byte(`{"a":1,"b":2,"a":3}`)
	_, err := ParseCanonicalInput(raw)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrDuplicateKey), "expected ErrDuplicateKey, got: %v", err)
}

func TestParseCanonicalInput_RejectsDuplicateKeysNested(t *testing.T) {
	raw := []byte(`{"outer":{"x":1,"x":2}}`)
	_, err := ParseCanonicalInput(raw)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrDuplicateKey))
}

func TestParseCanonicalInput_AcceptsUniqueKeys(t *testing.T) {
	raw := []byte(`{"a":1,"b":2,"c":3}`)
	val, err := ParseCanonicalInput(raw)
	require.NoError(t, err)
	m, ok := val.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, 3, len(m))
}

func TestParseCanonicalInput_RejectsExponentNotation(t *testing.T) {
	raw := []byte(`{"n":1e10}`)
	_, err := ParseCanonicalInput(raw)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAmbiguousNumber))
}

func TestParseCanonicalInput_RejectsNegativeZeroRaw(t *testing.T) {
	raw := []byte(`{"n":-0}`)
	_, err := ParseCanonicalInput(raw)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAmbiguousNumber))
}

func TestParseCanonicalInput_RejectsLeadingZeros(t *testing.T) {
	raw := []byte(`{"n":007}`)
	_, err := ParseCanonicalInput(raw)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAmbiguousNumber))
}

func TestParseCanonicalInput_RejectsTrailingContent(t *testing.T) {
	raw := []byte(`{"a":1}{"b":2}`)
	_, err := ParseCanonicalInput(raw)
	require.Error(t, err)
}

// ─── Unicode NFC normalisation ───────────────────────────────────────────────

func TestNormalizeUnicodeString_ASCII(t *testing.T) {
	// ASCII strings must be returned unchanged.
	inputs := []string{"hello", "audit-log", "2026-01-01T00:00:00Z", ""}
	for _, s := range inputs {
		assert.Equal(t, s, normalizeUnicodeString(s), "ASCII string should be unchanged: %q", s)
	}
}

func TestNormalizeUnicodeString_PrecomposedAlreadyNFC(t *testing.T) {
	// Precomposed characters (already NFC) must be returned unchanged.
	// "café" with precomposed é (U+00E9)
	precomposed := "caf\u00E9"
	result := normalizeUnicodeString(precomposed)
	assert.Equal(t, precomposed, result)
}

func TestNormalizeUnicodeString_DecomposedToNFC(t *testing.T) {
	// NFD form: 'e' (U+0065) + combining acute (U+0301) → NFC: é (U+00E9)
	decomposed := "caf\u0065\u0301" // NFD "café"
	precomposed := "caf\u00E9"      // NFC "café"
	result := normalizeUnicodeString(decomposed)
	assert.Equal(t, precomposed, result,
		"decomposed NFD string should be composed to NFC precomposed form")
}

func TestNormalizeUnicodeString_NonLatinUnchanged(t *testing.T) {
	// CJK and emoji are already in NFC — must be returned unchanged.
	cjk := "日本語"
	emoji := "🔬"
	assert.Equal(t, cjk, normalizeUnicodeString(cjk))
	assert.Equal(t, emoji, normalizeUnicodeString(emoji))
}

func TestCanonicalJSON_KeysNFCNormalized(t *testing.T) {
	// Two maps: one with precomposed key, one with decomposed.
	// Both must produce identical canonical JSON.
	precomposedKey := "caf\u00E9" // NFC
	decomposedKey := "caf\u0065\u0301" // NFD

	mapPrecomposed := map[string]interface{}{precomposedKey: 1}
	mapDecomposed := map[string]interface{}{decomposedKey: 1}

	gotPre, err := canonicalJSON(mapPrecomposed)
	require.NoError(t, err)
	gotDec, err := canonicalJSON(mapDecomposed)
	require.NoError(t, err)

	assert.Equal(t, string(gotPre), string(gotDec),
		"NFC and NFD key variants must produce identical canonical output")
}

func TestCanonicalJSON_StringValuesNFCNormalized(t *testing.T) {
	// String value in NFD must be normalised to NFC in canonical output.
	decomposed := "caf\u0065\u0301" // NFD
	input := map[string]interface{}{"name": decomposed}
	got, err := canonicalJSON(input)
	require.NoError(t, err)
	// The canonical output must contain the NFC form.
	precomposedJSON := `{"name":"caf` + "\u00E9" + `"}`
	assert.Equal(t, precomposedJSON, string(got))
}

// ─── ParseCanonicalInput + canonicalJSON round-trip ──────────────────────────

func TestParseCanonicalInput_RoundTrip(t *testing.T) {
	// A well-formed payload should parse and re-encode to identical bytes.
	raw := `{"events":["E1","E2"],"input":{"a":1,"b":2},"state":{"x":8,"y":9},"timestamp":"2026-01-01T00:00:00.000Z"}`
	val, err := ParseCanonicalInput([]byte(raw))
	require.NoError(t, err)

	got, err := canonicalJSON(val)
	require.NoError(t, err)
	assert.Equal(t, raw, string(got))
}

func TestParseCanonicalInput_KeyOrderingNormalised(t *testing.T) {
	// Input with unsorted keys — after parse+encode, keys must be sorted.
	raw := []byte(`{"z":3,"a":1,"m":2}`)
	val, err := ParseCanonicalInput(raw)
	require.NoError(t, err)

	got, err := canonicalJSON(val)
	require.NoError(t, err)
	assert.Equal(t, `{"a":1,"m":2,"z":3}`, string(got))
}
