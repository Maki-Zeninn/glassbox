// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package depcompat

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
)

// expectedOnlyKeys lists JSON top-level keys whose changes are always
// classified as DiffClassExpected. These are format/schema fields that change
// with a new schema version but do not affect serialization semantics.
var expectedOnlyKeys = map[string]bool{
	"schema_version": true,
	"schema":         true,
	"version":        true,
	"spec_version":   true,
	"format_version": true,
}

// nullableValuePatterns lists JSON path suffixes whose value diffs are
// classified as expected when the actual value is null, "", 0, or [].
var nullableValuePatterns = []string{
	"_url",
	"_uri",
	"description",
	"notes",
	"comment",
	"deprecated",
}

// CompareFiles loads golden and actual JSON files and produces an OutputResult.
// The caller provides the dep group, output kind, and both file paths.
func CompareFiles(group DepGroup, kind OutputKind, goldenPath, actualPath string) OutputResult {
	result := OutputResult{
		DepGroup:     group,
		OutputKind:   kind,
		GoldenFile:   goldenPath,
		CapturedFile: actualPath,
	}

	goldenBytes, err := os.ReadFile(goldenPath)
	if err != nil {
		result.Error = fmt.Sprintf("read golden file: %v", err)
		result.Class = DiffClassUnexpected
		return result
	}

	actualBytes, err := os.ReadFile(actualPath)
	if err != nil {
		result.Error = fmt.Sprintf("read actual file: %v", err)
		result.Class = DiffClassUnexpected
		return result
	}

	var golden, actual interface{}
	if err := json.Unmarshal(goldenBytes, &golden); err != nil {
		result.Error = fmt.Sprintf("parse golden JSON: %v", err)
		result.Class = DiffClassUnexpected
		return result
	}
	if err := json.Unmarshal(actualBytes, &actual); err != nil {
		result.Error = fmt.Sprintf("parse actual JSON: %v", err)
		result.Class = DiffClassUnexpected
		return result
	}

	diffs := diffValues("$", golden, actual)
	result.Diffs = diffs
	result.Class = aggregateClass(diffs)
	return result
}

// CompareBytes compares golden and actual JSON bytes directly (for tests).
func CompareBytes(group DepGroup, kind OutputKind, goldenBytes, actualBytes []byte) OutputResult {
	result := OutputResult{
		DepGroup:   group,
		OutputKind: kind,
	}

	var golden, actual interface{}
	if err := json.Unmarshal(goldenBytes, &golden); err != nil {
		result.Error = fmt.Sprintf("parse golden JSON: %v", err)
		result.Class = DiffClassUnexpected
		return result
	}
	if err := json.Unmarshal(actualBytes, &actual); err != nil {
		result.Error = fmt.Sprintf("parse actual JSON: %v", err)
		result.Class = DiffClassUnexpected
		return result
	}

	diffs := diffValues("$", golden, actual)
	result.Diffs = diffs
	result.Class = aggregateClass(diffs)
	return result
}

// diffValues recursively compares two decoded JSON values and returns field diffs.
func diffValues(path string, golden, actual interface{}) []FieldDiff {
	var diffs []FieldDiff

	switch g := golden.(type) {
	case map[string]interface{}:
		a, ok := actual.(map[string]interface{})
		if !ok {
			return []FieldDiff{typeMismatch(path, golden, actual)}
		}
		// Check all keys in golden exist in actual with the same value.
		for _, k := range sortedKeys(g) {
			childPath := path + "." + k
			gv := g[k]
			av, exists := a[k]
			if !exists {
				diffs = append(diffs, missingField(childPath, gv))
				continue
			}
			diffs = append(diffs, diffValues(childPath, gv, av)...)
		}
		// Check for new keys in actual that aren't in golden.
		for _, k := range sortedKeys(a) {
			childPath := path + "." + k
			if _, exists := g[k]; !exists {
				diffs = append(diffs, newField(childPath, a[k]))
			}
		}

	case []interface{}:
		a, ok := actual.([]interface{})
		if !ok {
			return []FieldDiff{typeMismatch(path, golden, actual)}
		}
		// Compare length.
		if len(g) != len(a) {
			diffs = append(diffs, arrayLengthDiff(path, len(g), len(a)))
		}
		// Compare elements up to the shorter length.
		n := len(g)
		if len(a) < n {
			n = len(a)
		}
		for i := 0; i < n; i++ {
			elemPath := fmt.Sprintf("%s[%d]", path, i)
			diffs = append(diffs, diffValues(elemPath, g[i], a[i])...)
		}

	default:
		// Scalar comparison: check for type change first, then value change.
		gStr := jsonEncode(golden)
		aStr := jsonEncode(actual)
		if isTypeMismatch(golden, actual) {
			diffs = append(diffs, typeMismatch(path, golden, actual))
		} else if !reflect.DeepEqual(golden, actual) {
			class, reason := classifyScalarDiff(path, gStr, aStr)
			diffs = append(diffs, FieldDiff{
				JSONPath:    path,
				GoldenValue: gStr,
				ActualValue: aStr,
				Class:       class,
				Reason:      reason,
			})
		}
	}
	return diffs
}

// classifyScalarDiff decides if a scalar value change is expected or unexpected.
func classifyScalarDiff(path, golden, actual string) (DiffClass, string) {
	// Extract the last key segment from the path.
	segments := strings.Split(path, ".")
	lastKey := segments[len(segments)-1]
	// Strip array index suffix if present.
	if idx := strings.LastIndex(lastKey, "["); idx >= 0 {
		lastKey = lastKey[:idx]
	}
	lastKey = strings.ToLower(lastKey)

	// Top-level schema/format version fields.
	if expectedOnlyKeys[lastKey] {
		return DiffClassExpected, fmt.Sprintf("schema field %q changed: %s → %s", lastKey, golden, actual)
	}

	// Null/empty actual value for nullable-by-convention fields.
	if isNullOrEmpty(actual) {
		for _, suffix := range nullableValuePatterns {
			if strings.HasSuffix(lastKey, suffix) {
				return DiffClassExpected, fmt.Sprintf("nullable field %q is empty in actual output", lastKey)
			}
		}
	}

	// Timestamps are expected to differ between runs.
	if strings.Contains(lastKey, "timestamp") || strings.Contains(lastKey, "created_at") ||
		strings.Contains(lastKey, "updated_at") || strings.Contains(lastKey, "generated_at") {
		return DiffClassExpected, "timestamp field changes between runs"
	}

	return DiffClassUnexpected, fmt.Sprintf("value changed at %s: %s → %s", path, golden, actual)
}

// isNullOrEmpty returns true for JSON null, empty string, 0, and [].
func isNullOrEmpty(v string) bool {
	return v == "null" || v == `""` || v == "0" || v == "[]" || v == "{}"
}

// aggregateClass returns the worst-case diff class across all diffs.
func aggregateClass(diffs []FieldDiff) DiffClass {
	if len(diffs) == 0 {
		return DiffClassNone
	}
	for _, d := range diffs {
		if d.Class == DiffClassUnexpected {
			return DiffClassUnexpected
		}
	}
	return DiffClassExpected
}

// missingField returns a diff for a field present in golden but absent in actual.
func missingField(path string, goldenValue interface{}) FieldDiff {
	segments := strings.Split(path, ".")
	lastKey := segments[len(segments)-1]
	if idx := strings.LastIndex(lastKey, "["); idx >= 0 {
		lastKey = lastKey[:idx]
	}
	class := DiffClassUnexpected
	reason := fmt.Sprintf("required field %q is missing from actual output", path)
	// Fields that are new in golden but absent in actual (schema additions)
	// could be expected if they match known nullable patterns, but without
	// context we conservatively mark them unexpected.
	_ = lastKey
	return FieldDiff{
		JSONPath:    path,
		GoldenValue: jsonEncode(goldenValue),
		Class:       class,
		Reason:      reason,
	}
}

// newField returns a diff for a field present in actual but absent in golden.
// New optional fields are classified as expected schema additions.
func newField(path string, actualValue interface{}) FieldDiff {
	return FieldDiff{
		JSONPath:    path,
		ActualValue: jsonEncode(actualValue),
		Class:       DiffClassExpected,
		Reason:      fmt.Sprintf("new field %q in actual output (schema addition)", path),
	}
}

// typeMismatch returns a diff when the JSON type of a value changed.
func typeMismatch(path string, golden, actual interface{}) FieldDiff {
	return FieldDiff{
		JSONPath:    path,
		GoldenValue: jsonEncode(golden),
		ActualValue: jsonEncode(actual),
		Class:       DiffClassUnexpected,
		Reason:      fmt.Sprintf("type changed at %s: %T → %T", path, golden, actual),
	}
}

// isTypeMismatch returns true when golden and actual are different JSON scalar types.
// JSON scalars decoded by encoding/json are: float64, string, bool, nil.
func isTypeMismatch(golden, actual interface{}) bool {
	switch golden.(type) {
	case float64:
		_, ok := actual.(float64)
		return !ok
	case string:
		_, ok := actual.(string)
		return !ok
	case bool:
		_, ok := actual.(bool)
		return !ok
	case nil:
		return actual != nil
	}
	return false
}

// arrayLengthDiff returns a diff when array lengths differ.
func arrayLengthDiff(path string, goldenLen, actualLen int) FieldDiff {
	class := DiffClassUnexpected
	reason := fmt.Sprintf("array length changed at %s: %d → %d", path, goldenLen, actualLen)
	// A longer array in actual is a schema addition (expected if all new elements are valid).
	if actualLen > goldenLen {
		class = DiffClassExpected
		reason = fmt.Sprintf("array at %s grew from %d to %d elements (schema addition)", path, goldenLen, actualLen)
	}
	return FieldDiff{
		JSONPath:    path,
		GoldenValue: jsonEncode(goldenLen),
		ActualValue: jsonEncode(actualLen),
		Class:       class,
		Reason:      reason,
	}
}

// sortedKeys returns map keys in sorted order for deterministic diff output.
func sortedKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// jsonEncode serialises a value to a compact JSON string for display.
func jsonEncode(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("<encode error: %v>", err)
	}
	return string(b)
}
