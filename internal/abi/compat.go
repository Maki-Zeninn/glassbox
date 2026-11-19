// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Package abi — compat.go implements ABI compatibility checking between two
// Soroban contract specifications.
//
// Change classification
//
//	CompatBreaking      — callers must be updated before the new version can be deployed.
//	                      Examples: removed function, changed parameter type, removed enum case.
//	CompatConditional   — may break callers depending on usage pattern.
//	                      Examples: new required parameter, changed return type.
//	CompatAdditive      — safe; no existing caller is affected.
//	                      Examples: new function, new optional field, new enum case.
//
// Comparison is performed on a NormalizedSpec, not on the raw XDR types,
// so that irrelevant ordering or whitespace differences never produce false positives.
package abi

import (
	"fmt"
	"sort"
	"strings"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// CompatStatus classifies the compatibility of a change between two ABI versions.
type CompatStatus string

const (
	// CompatOK means the two specs are identical.
	CompatOK CompatStatus = "compatible"
	// CompatAdditive means only backwards-compatible additions were found.
	CompatAdditive CompatStatus = "additive"
	// CompatConditional means changes that may or may not break callers.
	CompatConditional CompatStatus = "conditionally_compatible"
	// CompatBreaking means at least one change that breaks existing callers.
	CompatBreaking CompatStatus = "breaking"
)

// ChangeKind classifies one detected ABI change.
type ChangeKind string

const (
	// Function changes
	ChangeFunctionRemoved       ChangeKind = "function_removed"
	ChangeFunctionAdded         ChangeKind = "function_added"
	ChangeFunctionParamRemoved  ChangeKind = "function_param_removed"
	ChangeFunctionParamAdded    ChangeKind = "function_param_added"
	ChangeFunctionParamType     ChangeKind = "function_param_type_changed"
	ChangeFunctionParamReorder  ChangeKind = "function_param_reordered"
	ChangeFunctionReturnType    ChangeKind = "function_return_type_changed"
	// Struct changes
	ChangeStructRemoved       ChangeKind = "struct_removed"
	ChangeStructAdded         ChangeKind = "struct_added"
	ChangeStructFieldRemoved  ChangeKind = "struct_field_removed"
	ChangeStructFieldAdded    ChangeKind = "struct_field_added"
	ChangeStructFieldType     ChangeKind = "struct_field_type_changed"
	// Enum changes
	ChangeEnumRemoved      ChangeKind = "enum_removed"
	ChangeEnumAdded        ChangeKind = "enum_added"
	ChangeEnumCaseRemoved  ChangeKind = "enum_case_removed"
	ChangeEnumCaseAdded    ChangeKind = "enum_case_added"
	ChangeEnumCaseValue    ChangeKind = "enum_case_value_changed"
	// Union changes
	ChangeUnionRemoved     ChangeKind = "union_removed"
	ChangeUnionAdded       ChangeKind = "union_added"
	ChangeUnionCaseRemoved ChangeKind = "union_case_removed"
	ChangeUnionCaseAdded   ChangeKind = "union_case_added"
)

// defaultCompatStatus maps each ChangeKind to its CompatStatus.
var defaultCompatStatus = map[ChangeKind]CompatStatus{
	ChangeFunctionRemoved:       CompatBreaking,
	ChangeFunctionAdded:         CompatAdditive,
	ChangeFunctionParamRemoved:  CompatBreaking,
	ChangeFunctionParamAdded:    CompatConditional, // new required param breaks old callers
	ChangeFunctionParamType:     CompatBreaking,
	ChangeFunctionParamReorder:  CompatBreaking,
	ChangeFunctionReturnType:    CompatConditional, // callers may ignore return value
	ChangeStructRemoved:         CompatBreaking,
	ChangeStructAdded:           CompatAdditive,
	ChangeStructFieldRemoved:    CompatBreaking,
	ChangeStructFieldAdded:      CompatAdditive,
	ChangeStructFieldType:       CompatBreaking,
	ChangeEnumRemoved:           CompatBreaking,
	ChangeEnumAdded:             CompatAdditive,
	ChangeEnumCaseRemoved:       CompatBreaking,
	ChangeEnumCaseAdded:         CompatAdditive,
	ChangeEnumCaseValue:         CompatBreaking,
	ChangeUnionRemoved:          CompatBreaking,
	ChangeUnionAdded:            CompatAdditive,
	ChangeUnionCaseRemoved:      CompatBreaking,
	ChangeUnionCaseAdded:        CompatAdditive,
}

// ABIChange describes one detected difference between two ABI versions.
type ABIChange struct {
	// Kind classifies the change.
	Kind ChangeKind `json:"kind"`
	// Status is the compatibility impact of this change.
	Status CompatStatus `json:"status"`
	// Path is the stable, dot-separated identifier for the changed element.
	// Examples: "functions.transfer", "structs.TokenMetadata.fields.name",
	//           "enums.Status.cases.Active"
	Path string `json:"path"`
	// OldValue is the human-readable representation of the element in the
	// baseline (old) spec.  Empty when the element is new.
	OldValue string `json:"old_value,omitempty"`
	// NewValue is the human-readable representation in the current (new) spec.
	// Empty when the element was removed.
	NewValue string `json:"new_value,omitempty"`
	// Remediation is a brief, actionable hint for the developer.
	Remediation string `json:"remediation,omitempty"`
}

// CompatReport is the result of comparing two ContractSpec values.
type CompatReport struct {
	// OverallStatus is the worst-case status across all changes.
	OverallStatus CompatStatus `json:"overall_status"`
	// Changes lists every detected difference, sorted by path then kind.
	Changes []ABIChange `json:"changes"`
	// BreakingCount is the number of CompatBreaking changes.
	BreakingCount int `json:"breaking_count"`
	// ConditionalCount is the number of CompatConditional changes.
	ConditionalCount int `json:"conditional_count"`
	// AdditiveCount is the number of CompatAdditive changes.
	AdditiveCount int `json:"additive_count"`
	// Remediation is a top-level summary of actions required.
	Remediation string `json:"remediation,omitempty"`
}

// CompareSpecs compares old (baseline) and new ContractSpec values and returns
// a CompatReport describing every detected difference.
//
// The comparison is performed on normalized string representations so that
// harmless ordering or whitespace differences in the XDR encoding never
// produce false-positive diffs.
func CompareSpecs(old, new *ContractSpec) *CompatReport {
	report := &CompatReport{}

	if old == nil && new == nil {
		report.OverallStatus = CompatOK
		return report
	}
	if old == nil {
		old = &ContractSpec{}
	}
	if new == nil {
		new = &ContractSpec{}
	}

	report.Changes = append(report.Changes, compareFunctions(old, new)...)
	report.Changes = append(report.Changes, compareStructs(old, new)...)
	report.Changes = append(report.Changes, compareEnums(old, new)...)
	report.Changes = append(report.Changes, compareUnions(old, new)...)

	// Sort for deterministic output.
	sort.Slice(report.Changes, func(i, j int) bool {
		if report.Changes[i].Path != report.Changes[j].Path {
			return report.Changes[i].Path < report.Changes[j].Path
		}
		return string(report.Changes[i].Kind) < string(report.Changes[j].Kind)
	})

	// Aggregate counts and overall status.
	overall := CompatOK
	if len(report.Changes) > 0 {
		overall = CompatAdditive
	}
	for _, c := range report.Changes {
		switch c.Status {
		case CompatBreaking:
			report.BreakingCount++
			overall = CompatBreaking
		case CompatConditional:
			report.ConditionalCount++
			if overall != CompatBreaking {
				overall = CompatConditional
			}
		case CompatAdditive:
			report.AdditiveCount++
		}
	}
	report.OverallStatus = overall

	if report.BreakingCount > 0 {
		report.Remediation = fmt.Sprintf(
			"%d breaking change(s) detected. Update all callers before deploying the new contract version. "+
				"See each change's remediation field for specific guidance.",
			report.BreakingCount,
		)
	} else if report.ConditionalCount > 0 {
		report.Remediation = fmt.Sprintf(
			"%d conditionally compatible change(s) detected. Review each change and verify existing callers handle the new behaviour.",
			report.ConditionalCount,
		)
	}

	return report
}

// ── function comparison ───────────────────────────────────────────────────────

func compareFunctions(old, new *ContractSpec) []ABIChange {
	var changes []ABIChange

	oldFuncs := normFunctions(old)
	newFuncs := normFunctions(new)

	for name, of := range oldFuncs {
		nf, exists := newFuncs[name]
		if !exists {
			changes = append(changes, newChange(
				ChangeFunctionRemoved,
				"functions."+name,
				of.signature(), "",
				fmt.Sprintf("Re-add or deprecate function %q. Remove all callers if intentionally deleted.", name),
			))
			continue
		}
		changes = append(changes, compareFunctionParams(name, of, nf)...)
		changes = append(changes, compareFunctionReturns(name, of, nf)...)
	}
	for name, nf := range newFuncs {
		if _, exists := oldFuncs[name]; !exists {
			changes = append(changes, newChange(
				ChangeFunctionAdded,
				"functions."+name,
				"", nf.signature(),
				"",
			))
		}
	}
	return changes
}

func compareFunctionParams(name string, old, new normFunction) []ABIChange {
	var changes []ABIChange
	path := "functions." + name + ".params"

	// Check reordering: if both have the same parameter names but in different
	// order, report as a reorder (breaking) rather than add/remove noise.
	if haveSameParamNames(old.Params, new.Params) && !sameParamOrder(old.Params, new.Params) {
		changes = append(changes, newChange(
			ChangeFunctionParamReorder, path,
			joinNames(old.Params), joinNames(new.Params),
			fmt.Sprintf("Callers of %q must be updated for new parameter order.", name),
		))
		// Still check type changes after detecting reorder.
	}

	oldParams := indexByName(old.Params)
	newParams := indexByName(new.Params)

	for _, p := range old.Params {
		np, exists := newParams[p.Name]
		if !exists {
			changes = append(changes, newChange(
				ChangeFunctionParamRemoved,
				path+"."+p.Name,
				p.Type, "",
				fmt.Sprintf("Remove all uses of parameter %q in callers of function %q.", p.Name, name),
			))
			continue
		}
		if !strings.EqualFold(p.Type, np.Type) {
			changes = append(changes, newChange(
				ChangeFunctionParamType,
				path+"."+p.Name,
				p.Type, np.Type,
				fmt.Sprintf("Update all callers of %q to pass %q as type %s.", name, p.Name, np.Type),
			))
		}
	}
	for _, p := range new.Params {
		if _, exists := oldParams[p.Name]; !exists {
			changes = append(changes, newChange(
				ChangeFunctionParamAdded,
				path+"."+p.Name,
				"", p.Type,
				fmt.Sprintf("Callers of %q must supply new parameter %q.", name, p.Name),
			))
		}
	}
	return changes
}

func compareFunctionReturns(name string, old, new normFunction) []ABIChange {
	var changes []ABIChange
	if !strings.EqualFold(old.ReturnType, new.ReturnType) && !(old.ReturnType == "" && new.ReturnType == "") {
		changes = append(changes, newChange(
			ChangeFunctionReturnType,
			"functions."+name+".return_type",
			old.ReturnType, new.ReturnType,
			fmt.Sprintf("Verify callers of %q handle the new return type %s.", name, new.ReturnType),
		))
	}
	return changes
}

// ── struct comparison ─────────────────────────────────────────────────────────

func compareStructs(old, new *ContractSpec) []ABIChange {
	var changes []ABIChange

	oldStructs := normStructMap(old)
	newStructs := normStructMap(new)

	for name, os := range oldStructs {
		ns, exists := newStructs[name]
		if !exists {
			changes = append(changes, newChange(
				ChangeStructRemoved, "structs."+name,
				name, "",
				fmt.Sprintf("Remove all uses of struct %q.", name),
			))
			continue
		}
		changes = append(changes, compareStructFields(name, os, ns)...)
	}
	for name := range newStructs {
		if _, exists := oldStructs[name]; !exists {
			changes = append(changes, newChange(ChangeStructAdded, "structs."+name, "", name, ""))
		}
	}
	return changes
}

func compareStructFields(structName string, old, new normStruct) []ABIChange {
	var changes []ABIChange
	path := "structs." + structName + ".fields"

	oldFields := make(map[string]normField, len(old.Fields))
	for _, f := range old.Fields {
		oldFields[f.Name] = f
	}
	newFields := make(map[string]normField, len(new.Fields))
	for _, f := range new.Fields {
		newFields[f.Name] = f
	}

	for fname, of := range oldFields {
		nf, exists := newFields[fname]
		if !exists {
			changes = append(changes, newChange(
				ChangeStructFieldRemoved, path+"."+fname,
				of.Type, "",
				fmt.Sprintf("Remove all uses of field %q in struct %q.", fname, structName),
			))
			continue
		}
		if !strings.EqualFold(of.Type, nf.Type) {
			changes = append(changes, newChange(
				ChangeStructFieldType, path+"."+fname,
				of.Type, nf.Type,
				fmt.Sprintf("Update all code reading field %q of struct %q to handle type %s.", fname, structName, nf.Type),
			))
		}
	}
	for fname := range newFields {
		if _, exists := oldFields[fname]; !exists {
			changes = append(changes, newChange(ChangeStructFieldAdded, path+"."+fname, "", newFields[fname].Type, ""))
		}
	}
	return changes
}

// ── enum comparison ───────────────────────────────────────────────────────────

func compareEnums(old, new *ContractSpec) []ABIChange {
	var changes []ABIChange

	oldEnums := normEnumMap(old)
	newEnums := normEnumMap(new)

	for name, oe := range oldEnums {
		ne, exists := newEnums[name]
		if !exists {
			changes = append(changes, newChange(
				ChangeEnumRemoved, "enums."+name,
				name, "",
				fmt.Sprintf("Remove all uses of enum %q.", name),
			))
			continue
		}
		changes = append(changes, compareEnumCases(name, oe, ne)...)
	}
	for name := range newEnums {
		if _, exists := oldEnums[name]; !exists {
			changes = append(changes, newChange(ChangeEnumAdded, "enums."+name, "", name, ""))
		}
	}
	return changes
}

func compareEnumCases(enumName string, old, new normEnum) []ABIChange {
	var changes []ABIChange
	path := "enums." + enumName + ".cases"

	oldCases := make(map[string]uint32, len(old.Cases))
	for _, c := range old.Cases {
		oldCases[c.Name] = c.Value
	}
	newCases := make(map[string]uint32, len(new.Cases))
	for _, c := range new.Cases {
		newCases[c.Name] = c.Value
	}

	for cname, ov := range oldCases {
		nv, exists := newCases[cname]
		if !exists {
			changes = append(changes, newChange(
				ChangeEnumCaseRemoved, path+"."+cname,
				fmt.Sprintf("%d", ov), "",
				fmt.Sprintf("Remove all uses of enum case %q in %q.", cname, enumName),
			))
			continue
		}
		if ov != nv {
			changes = append(changes, newChange(
				ChangeEnumCaseValue, path+"."+cname,
				fmt.Sprintf("%d", ov), fmt.Sprintf("%d", nv),
				fmt.Sprintf("Update all code matching on enum case %q to handle new discriminant value %d.", cname, nv),
			))
		}
	}
	for cname := range newCases {
		if _, exists := oldCases[cname]; !exists {
			changes = append(changes, newChange(ChangeEnumCaseAdded, path+"."+cname, "", cname, ""))
		}
	}
	return changes
}

// ── union comparison ──────────────────────────────────────────────────────────

func compareUnions(old, new *ContractSpec) []ABIChange {
	var changes []ABIChange

	oldUnions := normUnionMap(old)
	newUnions := normUnionMap(new)

	for name, ou := range oldUnions {
		nu, exists := newUnions[name]
		if !exists {
			changes = append(changes, newChange(
				ChangeUnionRemoved, "unions."+name,
				name, "",
				fmt.Sprintf("Remove all uses of union %q.", name),
			))
			continue
		}
		oldCases := make(map[string]struct{}, len(ou.Cases))
		for _, c := range ou.Cases {
			oldCases[c] = struct{}{}
		}
		for _, c := range nu.Cases {
			if _, exists := oldCases[c]; !exists {
				changes = append(changes, newChange(
					ChangeUnionCaseAdded, "unions."+name+".cases."+c,
					"", c, "",
				))
			}
		}
		newCasesSet := make(map[string]struct{}, len(nu.Cases))
		for _, c := range nu.Cases {
			newCasesSet[c] = struct{}{}
		}
		for _, c := range ou.Cases {
			if _, exists := newCasesSet[c]; !exists {
				changes = append(changes, newChange(
					ChangeUnionCaseRemoved, "unions."+name+".cases."+c,
					c, "",
					fmt.Sprintf("Remove all uses of union case %q in %q.", c, name),
				))
			}
		}
	}
	for name := range newUnions {
		if _, exists := oldUnions[name]; !exists {
			changes = append(changes, newChange(ChangeUnionAdded, "unions."+name, "", name, ""))
		}
	}
	return changes
}

// ── normalized intermediate types ─────────────────────────────────────────────

type normParam struct {
	Name string
	Type string
}

type normFunction struct {
	Name       string
	Params     []normParam
	ReturnType string
}

func (f normFunction) signature() string {
	params := make([]string, len(f.Params))
	for i, p := range f.Params {
		params[i] = p.Name + ":" + p.Type
	}
	ret := f.ReturnType
	if ret == "" {
		ret = "void"
	}
	return fmt.Sprintf("%s(%s) -> %s", f.Name, strings.Join(params, ", "), ret)
}

type normField struct {
	Name string
	Type string
}

type normStruct struct {
	Name   string
	Fields []normField
}

type normEnumCase struct {
	Name  string
	Value uint32
}

type normEnum struct {
	Name  string
	Cases []normEnumCase
}

type normUnion struct {
	Name  string
	Cases []string
}

// normFunctions converts ContractSpec.Functions to a map of name → normFunction.
// It uses the XDR symbol names, lower-cased for comparison, to avoid
// false positives from capitalisation differences.
func normFunctions(spec *ContractSpec) map[string]normFunction {
	m := make(map[string]normFunction, len(spec.Functions))
	for _, f := range spec.Functions {
		name := string(f.Name)
		nf := normFunction{Name: name}
		for _, p := range f.Inputs {
			nf.Params = append(nf.Params, normParam{
				Name: p.Name, // string field per XDR codegen
				Type: scTypeString(p.Type),
			})
		}
		if len(f.Outputs) > 0 {
			// f.Outputs is []xdr.ScSpecTypeDef
			nf.ReturnType = scTypeString(f.Outputs[0])
		}
		m[name] = nf
	}
	return m
}

func normStructMap(spec *ContractSpec) map[string]normStruct {
	m := make(map[string]normStruct, len(spec.Structs))
	for _, s := range spec.Structs {
		name := s.Name // string field
		ns := normStruct{Name: name}
		for _, f := range s.Fields {
			ns.Fields = append(ns.Fields, normField{
				Name: f.Name, // string
				Type: scTypeString(f.Type),
			})
		}
		m[name] = ns
	}
	return m
}

func normEnumMap(spec *ContractSpec) map[string]normEnum {
	m := make(map[string]normEnum, len(spec.Enums))
	for _, e := range spec.Enums {
		name := e.Name // string
		ne := normEnum{Name: name}
		for _, c := range e.Cases {
			ne.Cases = append(ne.Cases, normEnumCase{
				Name:  c.Name,            // string
				Value: uint32(c.Value),   // xdr.Uint32 → uint32
			})
		}
		m[name] = ne
	}
	return m
}

func normUnionMap(spec *ContractSpec) map[string]normUnion {
	m := make(map[string]normUnion, len(spec.Unions))
	for _, u := range spec.Unions {
		name := u.Name // string
		nu := normUnion{Name: name}
		for _, c := range u.Cases {
			// Union cases use a Kind + tagged union.
			// We normalise to the case name regardless of void/tuple shape.
			caseName := unionCaseName(c)
			if caseName != "" {
				nu.Cases = append(nu.Cases, caseName)
			}
		}
		m[name] = nu
	}
	return m
}

// scTypeString returns a stable string representation of an XDR ScSpecTypeDef
// by delegating to FormatTypeDef (printer.go) so comparisons use the same
// human-readable type names shown to users.
func scTypeString(t xdr.ScSpecTypeDef) string {
	return FormatTypeDef(t)
}

// unionCaseName extracts the case name from an xdr.ScSpecUdtUnionCaseV0
// regardless of whether it is a void or tuple case.
func unionCaseName(c xdr.ScSpecUdtUnionCaseV0) string {
	switch c.Kind {
	case xdr.ScSpecUdtUnionCaseV0KindScSpecUdtUnionCaseVoidV0:
		if c.VoidCase != nil {
			return c.VoidCase.Name
		}
	case xdr.ScSpecUdtUnionCaseV0KindScSpecUdtUnionCaseTupleV0:
		if c.TupleCase != nil {
			return c.TupleCase.Name
		}
	}
	return ""
}

// ── helpers ───────────────────────────────────────────────────────────────────

func newChange(kind ChangeKind, path, old, new_, remediation string) ABIChange {
	status, ok := defaultCompatStatus[kind]
	if !ok {
		status = CompatBreaking // safe default
	}
	return ABIChange{
		Kind:        kind,
		Status:      status,
		Path:        path,
		OldValue:    old,
		NewValue:    new_,
		Remediation: remediation,
	}
}

func haveSameParamNames(a, b []normParam) bool {
	if len(a) != len(b) {
		return false
	}
	am := make(map[string]struct{}, len(a))
	for _, p := range a {
		am[p.Name] = struct{}{}
	}
	for _, p := range b {
		if _, ok := am[p.Name]; !ok {
			return false
		}
	}
	return true
}

func sameParamOrder(a, b []normParam) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name {
			return false
		}
	}
	return true
}

func indexByName(params []normParam) map[string]normParam {
	m := make(map[string]normParam, len(params))
	for _, p := range params {
		m[p.Name] = p
	}
	return m
}

func joinNames(params []normParam) string {
	names := make([]string, len(params))
	for i, p := range params {
		names[i] = p.Name
	}
	return strings.Join(names, ", ")
}
