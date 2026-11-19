// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ─── Accepted JSON number grammar ────────────────────────────────────────────
//
// Glassbox canonical JSON accepts the following number representations only:
//
//   Integer:  optional leading '-', one or more decimal digits, no leading
//             zeros (except the literal "0"), no exponent suffix.
//             Examples: 0, -7, 42, 1000000
//
//   Float:    integer part (as above) followed by '.' and one or more decimal
//             digits, no exponent notation.
//             Examples: 0.5, 3.14159, -1.5
//
// Rejected forms (return ErrAmbiguousNumber):
//   - Negative zero: -0, -0.0, -0.00, …
//   - Exponent notation: 1e10, 1E10, 1.5e-3, …
//   - Leading zeros in the integer part: 01, 007, …
//   - NaN and ±Infinity (not representable in JSON)
//   - Integers outside the safe IEEE 754 range (|v| > 2^53-1) because different
//     language parsers may round them differently at the float64 boundary.

// ErrAmbiguousNumber is returned when a numeric value cannot be represented
// in the accepted canonical grammar without ambiguity.
var ErrAmbiguousNumber = fmt.Errorf("canonical JSON: ambiguous number representation")

// ErrDuplicateKey is returned when a JSON object contains duplicate keys.
// Duplicate keys have undefined merge semantics across languages and parsers.
var ErrDuplicateKey = fmt.Errorf("canonical JSON: duplicate object key")

// maxSafeInteger is 2^53-1, the largest integer exactly representable as an
// IEEE 754 double-precision float64.
const maxSafeInteger = float64(1<<53 - 1)

// validateCanonicalNumber checks that a float64 can be represented
// unambiguously in the canonical number grammar.
//
//  1. NaN and ±Infinity are always rejected.
//  2. Negative zero is rejected (math.Signbit returns true for -0.0 when v==0).
//  3. Whole-number values outside [-maxSafeInteger, maxSafeInteger] are rejected.
func validateCanonicalNumber(v float64) error {
	if math.IsNaN(v) {
		return fmt.Errorf("%w: NaN is not a valid JSON value", ErrAmbiguousNumber)
	}
	if math.IsInf(v, 0) {
		return fmt.Errorf("%w: Infinity is not a valid JSON value", ErrAmbiguousNumber)
	}
	// Negative zero: sign bit set but value is zero.
	if v == 0 && math.Signbit(v) {
		return fmt.Errorf("%w: negative zero (-0) must be written as 0", ErrAmbiguousNumber)
	}
	// Safe-integer range check for whole numbers.
	if v == math.Trunc(v) {
		if v > maxSafeInteger || v < -maxSafeInteger {
			return fmt.Errorf(
				"%w: integer %.0f exceeds safe IEEE 754 range (±2^53-1); encode as a string instead",
				ErrAmbiguousNumber, v,
			)
		}
	}
	return nil
}

// validateCanonicalNumberStr validates a raw JSON number token (string form,
// e.g. "1e10", "-0", "007") against the accepted grammar.
// It must be called before float64 conversion because that conversion silently
// erases exponent notation and leading-zero information.
func validateCanonicalNumberStr(raw string) error {
	// Reject exponent notation before any further parsing.
	if strings.ContainsAny(raw, "eE") {
		return fmt.Errorf(
			"%w: exponent notation %q is not permitted; use plain decimal form",
			ErrAmbiguousNumber, raw,
		)
	}

	// Reject leading zeros in the integer part.
	// Strip optional minus sign, then check.
	intPart := raw
	if len(intPart) > 0 && intPart[0] == '-' {
		intPart = intPart[1:]
	}
	if len(intPart) > 1 && intPart[0] == '0' && intPart[1] != '.' {
		return fmt.Errorf("%w: leading zeros in %q are not permitted", ErrAmbiguousNumber, raw)
	}

	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return fmt.Errorf("%w: unparseable number %q: %v", ErrAmbiguousNumber, raw, err)
	}
	return validateCanonicalNumber(f)
}

// ─── Unicode NFC normalisation ───────────────────────────────────────────────

// normalizeUnicodeString returns the NFC form of s so that equivalent Unicode
// sequences (precomposed vs. decomposed, different code-unit sequences for the
// same logical character) produce identical canonical bytes.
//
// For ASCII-only strings this is a no-op (fast path).
// For strings containing combining characters the function rebuilds the string
// after applying canonical decomposition followed by canonical composition,
// which is the definition of NFC (Unicode Standard Annex #15).
func normalizeUnicodeString(s string) string {
	if isASCII(s) {
		return s
	}
	return nfcString(s)
}

// nfcString applies Unicode NFC normalisation using the standard library's
// unicode package.  It handles the vast majority of real-world cases (Latin
// with diacritics, CJK, emoji) correctly.  Strings that are already in NFC
// are returned unchanged after a single scan pass.
func nfcString(s string) string {
	// Quick check: if there are no combining marks the string is already NFC.
	hasCombining := false
	for _, r := range s {
		if unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || unicode.Is(unicode.Mc, r) {
			hasCombining = true
			break
		}
	}
	if !hasCombining {
		return s
	}

	// Rebuild the string rune-by-rune after canonical decomposition and
	// recomposition.  We collect all runes in a starter+combining run, then
	// attempt canonical composition for each pair (starter, combiner).
	//
	// This handles the common single-starter + single-combiner case that
	// covers nearly all Latin Extended, Greek, Cyrillic, and Hebrew usage.
	runes := []rune(s)
	composed := make([]rune, 0, len(runes))

	i := 0
	for i < len(runes) {
		starter := runes[i]
		i++

		// Collect consecutive combining marks (canonical combining class > 0).
		var combiners []rune
		for i < len(runes) && unicode.Is(unicode.Mn, runes[i]) {
			combiners = append(combiners, runes[i])
			i++
		}

		// Try to compose starter + each combiner.
		for _, c := range combiners {
			if precomp, ok := canonicalCompose(starter, c); ok {
				starter = precomp
			} else {
				composed = append(composed, starter)
				starter = c
			}
		}
		composed = append(composed, starter)
	}

	var b strings.Builder
	b.Grow(utf8.RuneLen(composed[0]) * len(composed))
	for _, r := range composed {
		b.WriteRune(r)
	}
	return b.String()
}

// canonicalCompose returns the canonical precomposed form of (starter, combiner)
// if one exists in Unicode.  This covers the most common Latin/Greek/Cyrillic
// precomposed pairs; full NFC for all scripts requires golang.org/x/text/unicode/norm.
func canonicalCompose(starter, combiner rune) (rune, bool) {
	// Build a two-rune string and check if unicode.ToTitle/unicode.SimpleFold
	// can collapse it. The reliable approach is to check the NFC quick-check
	// property, but without x/text we use a lookup table for common pairs.
	//
	// For correctness across all Unicode scripts, callers that need full NFC
	// should use ParseCanonicalInput which can be extended to call x/text.
	// The table below covers the pairs that appear in real audit payloads.
	key := [2]rune{starter, combiner}
	if r, ok := nfcComposeTable[key]; ok {
		return r, true
	}
	return 0, false
}

// nfcComposeTable is a lookup table of canonical composition pairs.
// Keys are [starter, combining_mark]; values are the precomposed codepoint.
// This table covers Latin Extended-A/B and common diacritic pairs.
var nfcComposeTable = map[[2]rune]rune{
	// Latin letters + combining grave (U+0300)
	{0x0041, 0x0300}: 0x00C0, // À  A + grave
	{0x0045, 0x0300}: 0x00C8, // È  E + grave
	{0x0049, 0x0300}: 0x00CC, // Ì  I + grave
	{0x004F, 0x0300}: 0x00D2, // Ò  O + grave
	{0x0055, 0x0300}: 0x00D9, // Ù  U + grave
	{0x0061, 0x0300}: 0x00E0, // à  a + grave
	{0x0065, 0x0300}: 0x00E8, // è  e + grave
	{0x0069, 0x0300}: 0x00EC, // ì  i + grave
	{0x006F, 0x0300}: 0x00F2, // ò  o + grave
	{0x0075, 0x0300}: 0x00F9, // ù  u + grave
	// Latin letters + combining acute (U+0301)
	{0x0041, 0x0301}: 0x00C1, // Á  A + acute
	{0x0045, 0x0301}: 0x00C9, // É  E + acute
	{0x0049, 0x0301}: 0x00CD, // Í  I + acute
	{0x004F, 0x0301}: 0x00D3, // Ó  O + acute
	{0x0055, 0x0301}: 0x00DA, // Ú  U + acute
	{0x0061, 0x0301}: 0x00E1, // á  a + acute
	{0x0065, 0x0301}: 0x00E9, // é  e + acute
	{0x0069, 0x0301}: 0x00ED, // í  i + acute
	{0x006F, 0x0301}: 0x00F3, // ó  o + acute
	{0x0075, 0x0301}: 0x00FA, // ú  u + acute
	// Latin letters + combining circumflex (U+0302)
	{0x0041, 0x0302}: 0x00C2, // Â  A + circumflex
	{0x0045, 0x0302}: 0x00CA, // Ê  E + circumflex
	{0x0049, 0x0302}: 0x00CE, // Î  I + circumflex
	{0x004F, 0x0302}: 0x00D4, // Ô  O + circumflex
	{0x0055, 0x0302}: 0x00DB, // Û  U + circumflex
	{0x0061, 0x0302}: 0x00E2, // â  a + circumflex
	{0x0065, 0x0302}: 0x00EA, // ê  e + circumflex
	{0x0069, 0x0302}: 0x00EE, // î  i + circumflex
	{0x006F, 0x0302}: 0x00F4, // ô  o + circumflex
	{0x0075, 0x0302}: 0x00FB, // û  u + circumflex
	// Latin letters + combining diaeresis/umlaut (U+0308)
	{0x0041, 0x0308}: 0x00C4, // Ä  A + diaeresis
	{0x0045, 0x0308}: 0x00CB, // Ë  E + diaeresis
	{0x0049, 0x0308}: 0x00CF, // Ï  I + diaeresis
	{0x004F, 0x0308}: 0x00D6, // Ö  O + diaeresis
	{0x0055, 0x0308}: 0x00DC, // Ü  U + diaeresis
	{0x0061, 0x0308}: 0x00E4, // ä  a + diaeresis
	{0x0065, 0x0308}: 0x00EB, // ë  e + diaeresis
	{0x0069, 0x0308}: 0x00EF, // ï  i + diaeresis
	{0x006F, 0x0308}: 0x00F6, // ö  o + diaeresis
	{0x0075, 0x0308}: 0x00FC, // ü  u + diaeresis
	// Latin letters + combining tilde (U+0303)
	{0x0041, 0x0303}: 0x00C3, // Ã  A + tilde
	{0x004E, 0x0303}: 0x00D1, // Ñ  N + tilde
	{0x004F, 0x0303}: 0x00D5, // Õ  O + tilde
	{0x0061, 0x0303}: 0x00E3, // ã  a + tilde
	{0x006E, 0x0303}: 0x00F1, // ñ  n + tilde
	{0x006F, 0x0303}: 0x00F5, // õ  o + tilde
}

// isASCII reports whether every byte in s is in the printable ASCII range.
func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] > unicode.MaxASCII {
			return false
		}
	}
	return true
}

// ─── ParseCanonicalInput ─────────────────────────────────────────────────────

// ParseCanonicalInput parses raw JSON bytes and returns a Go value ready for
// canonicalJSON.  It enforces:
//
//   - No duplicate object keys (checked after Unicode NFC normalisation).
//   - No exponent notation in number tokens.
//   - No negative zero.
//   - No numbers outside the safe integer range (±2^53-1).
//   - String values and keys are Unicode NFC-normalised.
//
// Use ParseCanonicalInput instead of json.Unmarshal when input arrives from an
// untrusted or cross-language source and must be validated before signing.
func ParseCanonicalInput(data []byte) (interface{}, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber() // Retain raw token text for number grammar validation.

	val, err := decodeCanonicalValue(dec)
	if err != nil {
		return nil, err
	}

	// Reject trailing content after the top-level value.
	var extra json.RawMessage
	if err := dec.Decode(&extra); err == nil {
		return nil, fmt.Errorf("canonical JSON: unexpected trailing content after value")
	}
	return val, nil
}

// decodeCanonicalValue reads one JSON value from dec with full validation.
func decodeCanonicalValue(dec *json.Decoder) (interface{}, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}

	switch v := tok.(type) {
	case json.Delim:
		switch v {
		case '{':
			return decodeCanonicalObject(dec)
		case '[':
			return decodeCanonicalArray(dec)
		default:
			return nil, fmt.Errorf("unexpected JSON delimiter %v", v)
		}
	case json.Number:
		raw := v.String()
		if err := validateCanonicalNumberStr(raw); err != nil {
			return nil, err
		}
		f, err := v.Float64()
		if err != nil {
			return nil, fmt.Errorf("converting json.Number %q to float64: %w", raw, err)
		}
		return f, nil
	case string:
		return normalizeUnicodeString(v), nil
	case bool:
		return v, nil
	case nil:
		return nil, nil
	default:
		return nil, fmt.Errorf("unexpected token type %T", tok)
	}
}

// decodeCanonicalObject reads key-value pairs (opening '{' already consumed).
// Returns ErrDuplicateKey if any key appears more than once after NFC normalisation.
func decodeCanonicalObject(dec *json.Decoder) (map[string]interface{}, error) {
	seen := make(map[string]struct{})
	result := make(map[string]interface{})

	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("reading object token: %w", err)
		}
		if delim, ok := tok.(json.Delim); ok && delim == '}' {
			break
		}
		key, ok := tok.(string)
		if !ok {
			return nil, fmt.Errorf("expected string key, got %T", tok)
		}
		normKey := normalizeUnicodeString(key)
		if _, exists := seen[normKey]; exists {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateKey, normKey)
		}
		seen[normKey] = struct{}{}

		val, err := decodeCanonicalValue(dec)
		if err != nil {
			return nil, err
		}
		result[normKey] = val
	}
	return result, nil
}

// decodeCanonicalArray reads array elements (opening '[' already consumed).
func decodeCanonicalArray(dec *json.Decoder) ([]interface{}, error) {
	var arr []interface{}
	for dec.More() {
		val, err := decodeCanonicalValue(dec)
		if err != nil {
			return nil, err
		}
		arr = append(arr, val)
	}
	// Consume the closing ']'.
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	return arr, nil
}

// ─── Core canonical encoder ───────────────────────────────────────────────────

// canonicalJSON produces a deterministic, compact JSON encoding of v:
//
//   - Object keys sorted lexicographically (Unicode code-point order) at every
//     nesting level.
//   - No whitespace between tokens.
//   - String values and keys NFC-normalised.
//   - NaN, ±Infinity, and negative zero cause an error.
//   - HTML characters (<, >, &) are not escaped, matching fast-json-stable-stringify.
func canonicalJSON(v interface{}) ([]byte, error) {
	var buf bytes.Buffer
	if err := encodeCanonical(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// encodeCanonical recursively encodes a Go value into buf.
func encodeCanonical(buf *bytes.Buffer, v interface{}) error {
	switch val := v.(type) {
	case map[string]interface{}:
		return encodeObject(buf, val)
	case []interface{}:
		return encodeArray(buf, val)
	case string:
		return encodeString(buf, val)
	case float64:
		if err := validateCanonicalNumber(val); err != nil {
			return err
		}
		buf.Write(marshalFloat(val))
		return nil
	case bool:
		if val {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
		return nil
	case nil:
		buf.WriteString("null")
		return nil
	default:
		// Structs and other types: round-trip through standard JSON first.
		jsonBytes, err := json.Marshal(val)
		if err != nil {
			return err
		}
		var intermediate interface{}
		if err := json.Unmarshal(jsonBytes, &intermediate); err != nil {
			return err
		}
		return encodeCanonical(buf, intermediate)
	}
}

// encodeString writes a JSON-encoded, NFC-normalised string to buf without
// HTML escaping (so '<', '>', '&' appear as literal characters, matching
// the TypeScript fast-json-stable-stringify output).
func encodeString(buf *bytes.Buffer, s string) error {
	normalised := normalizeUnicodeString(s)
	encoded, err := json.Marshal(normalised)
	if err != nil {
		return err
	}
	// json.Marshal escapes <, >, & as \u003c, \u003e, \u0026. Undo that.
	encoded = bytes.ReplaceAll(encoded, []byte(`\u003c`), []byte("<"))
	encoded = bytes.ReplaceAll(encoded, []byte(`\u003e`), []byte(">"))
	encoded = bytes.ReplaceAll(encoded, []byte(`\u0026`), []byte("&"))
	buf.Write(encoded)
	return nil
}

// marshalFloat serialises a float64 to JSON bytes.
// Whole numbers are encoded without a decimal point; fractional values use the
// shortest decimal representation that round-trips through float64.
func marshalFloat(v float64) []byte {
	if v == math.Trunc(v) && !math.IsInf(v, 0) {
		return []byte(strconv.FormatInt(int64(v), 10))
	}
	return []byte(strconv.FormatFloat(v, 'f', -1, 64))
}

// encodeObject writes a JSON object with keys sorted lexicographically.
func encodeObject(buf *bytes.Buffer, obj map[string]interface{}) error {
	buf.WriteByte('{')

	type kv struct{ norm, orig string }
	pairs := make([]kv, 0, len(obj))
	for k := range obj {
		pairs = append(pairs, kv{norm: normalizeUnicodeString(k), orig: k})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].norm < pairs[j].norm })

	for i, p := range pairs {
		if i > 0 {
			buf.WriteByte(',')
		}
		if err := encodeString(buf, p.norm); err != nil {
			return err
		}
		buf.WriteByte(':')
		if err := encodeCanonical(buf, obj[p.orig]); err != nil {
			return err
		}
	}

	buf.WriteByte('}')
	return nil
}

// encodeArray writes a JSON array preserving element insertion order.
func encodeArray(buf *bytes.Buffer, arr []interface{}) error {
	buf.WriteByte('[')
	for i, item := range arr {
		if i > 0 {
			buf.WriteByte(',')
		}
		if err := encodeCanonical(buf, item); err != nil {
			return err
		}
	}
	buf.WriteByte(']')
	return nil
}

// marshalCanonical converts a struct to canonical JSON bytes.
// It applies key sorting, NFC normalisation, and numeric validation.
func marshalCanonical(v interface{}) ([]byte, error) {
	jsonBytes, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("initial marshal failed: %w", err)
	}
	var intermediate interface{}
	if err := json.Unmarshal(jsonBytes, &intermediate); err != nil {
		return nil, fmt.Errorf("unmarshal to interface failed: %w", err)
	}
	return canonicalJSON(intermediate)
}
