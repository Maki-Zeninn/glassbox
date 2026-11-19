// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package abi_test

import (
	"testing"

	"github.com/dotandev/glassbox/internal/abi"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── builder helpers ───────────────────────────────────────────────────────────
// Field names match what printer_test.go shows for the actual XDR codegen:
//   ScSpecFunctionV0.Name        → xdr.ScSymbol
//   ScSpecFunctionInputV0.Name   → string
//   ScSpecFunctionInputV0.Type   → xdr.ScSpecTypeDef
//   ScSpecFunctionV0.Outputs     → []xdr.ScSpecTypeDef
//   ScSpecUdtStructV0.Name       → string
//   ScSpecUdtStructFieldV0.Name  → string
//   ScSpecUdtEnumV0.Name         → string
//   ScSpecUdtEnumCaseV0.Name     → string
//   ScSpecUdtEnumCaseV0.Value    → xdr.Uint32
//   ScSpecUdtUnionV0.Name        → string

func makeFunc(name string, inputs []xdr.ScSpecFunctionInputV0, outputs []xdr.ScSpecTypeDef) xdr.ScSpecFunctionV0 {
	return xdr.ScSpecFunctionV0{
		Name:    xdr.ScSymbol(name),
		Inputs:  inputs,
		Outputs: outputs,
	}
}

func makeInput(name string, t xdr.ScSpecTypeDef) xdr.ScSpecFunctionInputV0 {
	return xdr.ScSpecFunctionInputV0{Name: name, Type: t}
}

func i128Type() xdr.ScSpecTypeDef {
	return xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeI128}
}

func u32Type() xdr.ScSpecTypeDef {
	return xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeU32}
}

func boolType() xdr.ScSpecTypeDef {
	return xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeBool}
}

func addressType() xdr.ScSpecTypeDef {
	return xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeAddress}
}

func makeStruct(name string, fields []xdr.ScSpecUdtStructFieldV0) xdr.ScSpecUdtStructV0 {
	return xdr.ScSpecUdtStructV0{Name: name, Fields: fields}
}

func makeField(name string, t xdr.ScSpecTypeDef) xdr.ScSpecUdtStructFieldV0 {
	return xdr.ScSpecUdtStructFieldV0{Name: name, Type: t}
}

func makeEnum(name string, cases []xdr.ScSpecUdtEnumCaseV0) xdr.ScSpecUdtEnumV0 {
	return xdr.ScSpecUdtEnumV0{Name: name, Cases: cases}
}

func makeEnumCase(name string, value uint32) xdr.ScSpecUdtEnumCaseV0 {
	return xdr.ScSpecUdtEnumCaseV0{Name: name, Value: xdr.Uint32(value)}
}

func makeUnion(name string, cases []xdr.ScSpecUdtUnionCaseV0) xdr.ScSpecUdtUnionV0 {
	return xdr.ScSpecUdtUnionV0{Name: name, Cases: cases}
}

func makeVoidCase(name string) xdr.ScSpecUdtUnionCaseV0 {
	return xdr.ScSpecUdtUnionCaseV0{
		Kind:     xdr.ScSpecUdtUnionCaseV0KindScSpecUdtUnionCaseVoidV0,
		VoidCase: &xdr.ScSpecUdtUnionCaseVoidV0{Name: name},
	}
}

// ── identical specs ───────────────────────────────────────────────────────────

func TestCompareSpecs_Identical(t *testing.T) {
	spec := &abi.ContractSpec{
		Functions: []xdr.ScSpecFunctionV0{
			makeFunc("transfer",
				[]xdr.ScSpecFunctionInputV0{
					makeInput("from", addressType()),
					makeInput("to", addressType()),
					makeInput("amount", i128Type()),
				},
				[]xdr.ScSpecTypeDef{boolType()},
			),
		},
	}
	report := abi.CompareSpecs(spec, spec)
	assert.Equal(t, abi.CompatOK, report.OverallStatus)
	assert.Empty(t, report.Changes)
	assert.Equal(t, 0, report.BreakingCount)
}

func TestCompareSpecs_BothNil(t *testing.T) {
	report := abi.CompareSpecs(nil, nil)
	assert.Equal(t, abi.CompatOK, report.OverallStatus)
	assert.Empty(t, report.Changes)
}

func TestCompareSpecs_OldNil_TreatedAsEmpty(t *testing.T) {
	new_ := &abi.ContractSpec{
		Functions: []xdr.ScSpecFunctionV0{makeFunc("mint", nil, nil)},
	}
	report := abi.CompareSpecs(nil, new_)
	assert.Equal(t, abi.CompatAdditive, report.OverallStatus)
	assert.Equal(t, 1, report.AdditiveCount)
}

// ── function changes ──────────────────────────────────────────────────────────

func TestCompareSpecs_FunctionRemoved_IsBreaking(t *testing.T) {
	old := &abi.ContractSpec{
		Functions: []xdr.ScSpecFunctionV0{
			makeFunc("transfer", nil, nil),
			makeFunc("approve", nil, nil),
		},
	}
	new_ := &abi.ContractSpec{
		Functions: []xdr.ScSpecFunctionV0{
			makeFunc("transfer", nil, nil),
		},
	}
	report := abi.CompareSpecs(old, new_)
	assert.Equal(t, abi.CompatBreaking, report.OverallStatus)
	assert.Equal(t, 1, report.BreakingCount)
	require.Len(t, report.Changes, 1)
	assert.Equal(t, abi.ChangeFunctionRemoved, report.Changes[0].Kind)
	assert.Equal(t, "functions.approve", report.Changes[0].Path)
	assert.NotEmpty(t, report.Changes[0].Remediation)
}

func TestCompareSpecs_FunctionAdded_IsAdditive(t *testing.T) {
	old := &abi.ContractSpec{
		Functions: []xdr.ScSpecFunctionV0{makeFunc("transfer", nil, nil)},
	}
	new_ := &abi.ContractSpec{
		Functions: []xdr.ScSpecFunctionV0{
			makeFunc("transfer", nil, nil),
			makeFunc("burn", nil, nil),
		},
	}
	report := abi.CompareSpecs(old, new_)
	assert.Equal(t, abi.CompatAdditive, report.OverallStatus)
	assert.Equal(t, 1, report.AdditiveCount)
	assert.Equal(t, 0, report.BreakingCount)
}

func TestCompareSpecs_ParamTypeChanged_IsBreaking(t *testing.T) {
	old := &abi.ContractSpec{
		Functions: []xdr.ScSpecFunctionV0{
			makeFunc("transfer",
				[]xdr.ScSpecFunctionInputV0{makeInput("amount", i128Type())},
				nil,
			),
		},
	}
	new_ := &abi.ContractSpec{
		Functions: []xdr.ScSpecFunctionV0{
			makeFunc("transfer",
				[]xdr.ScSpecFunctionInputV0{makeInput("amount", u32Type())}, // type changed
				nil,
			),
		},
	}
	report := abi.CompareSpecs(old, new_)
	assert.Equal(t, abi.CompatBreaking, report.OverallStatus)
	found := false
	for _, c := range report.Changes {
		if c.Kind == abi.ChangeFunctionParamType && c.Path == "functions.transfer.params.amount" {
			found = true
			assert.NotEmpty(t, c.OldValue)
			assert.NotEmpty(t, c.NewValue)
		}
	}
	assert.True(t, found, "expected param type change in report")
}

func TestCompareSpecs_ParamRemoved_IsBreaking(t *testing.T) {
	old := &abi.ContractSpec{
		Functions: []xdr.ScSpecFunctionV0{
			makeFunc("transfer",
				[]xdr.ScSpecFunctionInputV0{
					makeInput("from", addressType()),
					makeInput("to", addressType()),
					makeInput("amount", i128Type()),
				},
				nil,
			),
		},
	}
	new_ := &abi.ContractSpec{
		Functions: []xdr.ScSpecFunctionV0{
			makeFunc("transfer",
				[]xdr.ScSpecFunctionInputV0{
					makeInput("from", addressType()),
					makeInput("to", addressType()),
					// "amount" removed
				},
				nil,
			),
		},
	}
	report := abi.CompareSpecs(old, new_)
	assert.Equal(t, abi.CompatBreaking, report.OverallStatus)
	found := false
	for _, c := range report.Changes {
		if c.Kind == abi.ChangeFunctionParamRemoved {
			found = true
		}
	}
	assert.True(t, found, "expected function_param_removed change")
}

func TestCompareSpecs_ParamAdded_IsConditional(t *testing.T) {
	old := &abi.ContractSpec{
		Functions: []xdr.ScSpecFunctionV0{
			makeFunc("transfer",
				[]xdr.ScSpecFunctionInputV0{makeInput("amount", i128Type())},
				nil,
			),
		},
	}
	new_ := &abi.ContractSpec{
		Functions: []xdr.ScSpecFunctionV0{
			makeFunc("transfer",
				[]xdr.ScSpecFunctionInputV0{
					makeInput("amount", i128Type()),
					makeInput("memo", u32Type()),
				},
				nil,
			),
		},
	}
	report := abi.CompareSpecs(old, new_)
	// A new required parameter breaks existing callers → conditional.
	assert.Equal(t, abi.CompatConditional, report.OverallStatus)
	assert.Greater(t, report.ConditionalCount, 0)
}

func TestCompareSpecs_ParamReordered_IsBreaking(t *testing.T) {
	old := &abi.ContractSpec{
		Functions: []xdr.ScSpecFunctionV0{
			makeFunc("transfer",
				[]xdr.ScSpecFunctionInputV0{
					makeInput("from", addressType()),
					makeInput("to", addressType()),
				},
				nil,
			),
		},
	}
	new_ := &abi.ContractSpec{
		Functions: []xdr.ScSpecFunctionV0{
			makeFunc("transfer",
				[]xdr.ScSpecFunctionInputV0{
					makeInput("to", addressType()),
					makeInput("from", addressType()),
				},
				nil,
			),
		},
	}
	report := abi.CompareSpecs(old, new_)
	assert.Equal(t, abi.CompatBreaking, report.OverallStatus)
	found := false
	for _, c := range report.Changes {
		if c.Kind == abi.ChangeFunctionParamReorder {
			found = true
			assert.NotEmpty(t, c.Remediation)
		}
	}
	assert.True(t, found, "expected function_param_reordered change")
}

func TestCompareSpecs_ReturnTypeChanged_IsConditional(t *testing.T) {
	old := &abi.ContractSpec{
		Functions: []xdr.ScSpecFunctionV0{
			makeFunc("get_balance", nil, []xdr.ScSpecTypeDef{i128Type()}),
		},
	}
	new_ := &abi.ContractSpec{
		Functions: []xdr.ScSpecFunctionV0{
			makeFunc("get_balance", nil, []xdr.ScSpecTypeDef{u32Type()}),
		},
	}
	report := abi.CompareSpecs(old, new_)
	assert.Equal(t, abi.CompatConditional, report.OverallStatus)
	found := false
	for _, c := range report.Changes {
		if c.Kind == abi.ChangeFunctionReturnType {
			found = true
		}
	}
	assert.True(t, found, "expected function_return_type_changed")
}

// ── struct changes ────────────────────────────────────────────────────────────

func TestCompareSpecs_StructRemoved_IsBreaking(t *testing.T) {
	old := &abi.ContractSpec{
		Structs: []xdr.ScSpecUdtStructV0{makeStruct("TokenInfo", nil)},
	}
	new_ := &abi.ContractSpec{}
	report := abi.CompareSpecs(old, new_)
	assert.Equal(t, abi.CompatBreaking, report.OverallStatus)
	require.NotEmpty(t, report.Changes)
	assert.Equal(t, abi.ChangeStructRemoved, report.Changes[0].Kind)
}

func TestCompareSpecs_StructAdded_IsAdditive(t *testing.T) {
	old := &abi.ContractSpec{}
	new_ := &abi.ContractSpec{
		Structs: []xdr.ScSpecUdtStructV0{makeStruct("TokenInfo", nil)},
	}
	report := abi.CompareSpecs(old, new_)
	assert.Equal(t, abi.CompatAdditive, report.OverallStatus)
}

func TestCompareSpecs_StructFieldRemoved_IsBreaking(t *testing.T) {
	old := &abi.ContractSpec{
		Structs: []xdr.ScSpecUdtStructV0{
			makeStruct("Token", []xdr.ScSpecUdtStructFieldV0{
				makeField("name", u32Type()),
				makeField("symbol", u32Type()),
			}),
		},
	}
	new_ := &abi.ContractSpec{
		Structs: []xdr.ScSpecUdtStructV0{
			makeStruct("Token", []xdr.ScSpecUdtStructFieldV0{
				makeField("name", u32Type()),
				// "symbol" removed
			}),
		},
	}
	report := abi.CompareSpecs(old, new_)
	assert.Equal(t, abi.CompatBreaking, report.OverallStatus)
	found := false
	for _, c := range report.Changes {
		if c.Kind == abi.ChangeStructFieldRemoved {
			found = true
		}
	}
	assert.True(t, found)
}

func TestCompareSpecs_StructFieldAdded_IsAdditive(t *testing.T) {
	old := &abi.ContractSpec{
		Structs: []xdr.ScSpecUdtStructV0{
			makeStruct("Token", []xdr.ScSpecUdtStructFieldV0{makeField("name", u32Type())}),
		},
	}
	new_ := &abi.ContractSpec{
		Structs: []xdr.ScSpecUdtStructV0{
			makeStruct("Token", []xdr.ScSpecUdtStructFieldV0{
				makeField("name", u32Type()),
				makeField("decimals", u32Type()),
			}),
		},
	}
	report := abi.CompareSpecs(old, new_)
	assert.Equal(t, abi.CompatAdditive, report.OverallStatus)
	assert.Equal(t, 0, report.BreakingCount)
}

func TestCompareSpecs_StructFieldTypeChanged_IsBreaking(t *testing.T) {
	old := &abi.ContractSpec{
		Structs: []xdr.ScSpecUdtStructV0{
			makeStruct("Token", []xdr.ScSpecUdtStructFieldV0{makeField("amount", i128Type())}),
		},
	}
	new_ := &abi.ContractSpec{
		Structs: []xdr.ScSpecUdtStructV0{
			makeStruct("Token", []xdr.ScSpecUdtStructFieldV0{makeField("amount", u32Type())}),
		},
	}
	report := abi.CompareSpecs(old, new_)
	assert.Equal(t, abi.CompatBreaking, report.OverallStatus)
}

// ── enum changes ──────────────────────────────────────────────────────────────

func TestCompareSpecs_EnumCaseRemoved_IsBreaking(t *testing.T) {
	old := &abi.ContractSpec{
		Enums: []xdr.ScSpecUdtEnumV0{
			makeEnum("Status", []xdr.ScSpecUdtEnumCaseV0{
				makeEnumCase("Active", 0),
				makeEnumCase("Inactive", 1),
			}),
		},
	}
	new_ := &abi.ContractSpec{
		Enums: []xdr.ScSpecUdtEnumV0{
			makeEnum("Status", []xdr.ScSpecUdtEnumCaseV0{
				makeEnumCase("Active", 0),
				// "Inactive" removed
			}),
		},
	}
	report := abi.CompareSpecs(old, new_)
	assert.Equal(t, abi.CompatBreaking, report.OverallStatus)
	require.NotEmpty(t, report.Changes)
	var found bool
	for _, c := range report.Changes {
		if c.Kind == abi.ChangeEnumCaseRemoved && c.Path == "enums.Status.cases.Inactive" {
			found = true
		}
	}
	assert.True(t, found, "expected enums.Status.cases.Inactive to be removed")
}

func TestCompareSpecs_EnumCaseAdded_IsAdditive(t *testing.T) {
	old := &abi.ContractSpec{
		Enums: []xdr.ScSpecUdtEnumV0{
			makeEnum("Status", []xdr.ScSpecUdtEnumCaseV0{makeEnumCase("Active", 0)}),
		},
	}
	new_ := &abi.ContractSpec{
		Enums: []xdr.ScSpecUdtEnumV0{
			makeEnum("Status", []xdr.ScSpecUdtEnumCaseV0{
				makeEnumCase("Active", 0),
				makeEnumCase("Pending", 2),
			}),
		},
	}
	report := abi.CompareSpecs(old, new_)
	assert.Equal(t, abi.CompatAdditive, report.OverallStatus)
	assert.Equal(t, 0, report.BreakingCount)
}

func TestCompareSpecs_EnumCaseValueChanged_IsBreaking(t *testing.T) {
	old := &abi.ContractSpec{
		Enums: []xdr.ScSpecUdtEnumV0{
			makeEnum("Status", []xdr.ScSpecUdtEnumCaseV0{makeEnumCase("Active", 0)}),
		},
	}
	new_ := &abi.ContractSpec{
		Enums: []xdr.ScSpecUdtEnumV0{
			makeEnum("Status", []xdr.ScSpecUdtEnumCaseV0{makeEnumCase("Active", 99)}), // discriminant changed
		},
	}
	report := abi.CompareSpecs(old, new_)
	assert.Equal(t, abi.CompatBreaking, report.OverallStatus)
	require.NotEmpty(t, report.Changes)
	assert.Equal(t, abi.ChangeEnumCaseValue, report.Changes[0].Kind)
}

func TestCompareSpecs_EnumRemoved_IsBreaking(t *testing.T) {
	old := &abi.ContractSpec{
		Enums: []xdr.ScSpecUdtEnumV0{makeEnum("OldEnum", nil)},
	}
	new_ := &abi.ContractSpec{}
	report := abi.CompareSpecs(old, new_)
	assert.Equal(t, abi.CompatBreaking, report.OverallStatus)
}

func TestCompareSpecs_EnumAdded_IsAdditive(t *testing.T) {
	old := &abi.ContractSpec{}
	new_ := &abi.ContractSpec{
		Enums: []xdr.ScSpecUdtEnumV0{makeEnum("NewEnum", nil)},
	}
	report := abi.CompareSpecs(old, new_)
	assert.Equal(t, abi.CompatAdditive, report.OverallStatus)
}

// ── union changes ─────────────────────────────────────────────────────────────

func TestCompareSpecs_UnionRemoved_IsBreaking(t *testing.T) {
	old := &abi.ContractSpec{
		Unions: []xdr.ScSpecUdtUnionV0{makeUnion("Result", nil)},
	}
	new_ := &abi.ContractSpec{}
	report := abi.CompareSpecs(old, new_)
	assert.Equal(t, abi.CompatBreaking, report.OverallStatus)
}

func TestCompareSpecs_UnionCaseAdded_IsAdditive(t *testing.T) {
	old := &abi.ContractSpec{
		Unions: []xdr.ScSpecUdtUnionV0{
			makeUnion("MyUnion", []xdr.ScSpecUdtUnionCaseV0{makeVoidCase("A")}),
		},
	}
	new_ := &abi.ContractSpec{
		Unions: []xdr.ScSpecUdtUnionV0{
			makeUnion("MyUnion", []xdr.ScSpecUdtUnionCaseV0{
				makeVoidCase("A"),
				makeVoidCase("B"),
			}),
		},
	}
	report := abi.CompareSpecs(old, new_)
	assert.Equal(t, abi.CompatAdditive, report.OverallStatus)
}

func TestCompareSpecs_UnionCaseRemoved_IsBreaking(t *testing.T) {
	old := &abi.ContractSpec{
		Unions: []xdr.ScSpecUdtUnionV0{
			makeUnion("MyUnion", []xdr.ScSpecUdtUnionCaseV0{
				makeVoidCase("A"),
				makeVoidCase("B"),
			}),
		},
	}
	new_ := &abi.ContractSpec{
		Unions: []xdr.ScSpecUdtUnionV0{
			makeUnion("MyUnion", []xdr.ScSpecUdtUnionCaseV0{makeVoidCase("A")}),
		},
	}
	report := abi.CompareSpecs(old, new_)
	assert.Equal(t, abi.CompatBreaking, report.OverallStatus)
}

// ── ordering / formatting independence ───────────────────────────────────────

func TestCompareSpecs_FunctionOrderingIndependent(t *testing.T) {
	// Same functions in different slice order — must not produce changes.
	f1 := makeFunc("transfer", nil, nil)
	f2 := makeFunc("approve", nil, nil)

	old := &abi.ContractSpec{Functions: []xdr.ScSpecFunctionV0{f1, f2}}
	new_ := &abi.ContractSpec{Functions: []xdr.ScSpecFunctionV0{f2, f1}}

	report := abi.CompareSpecs(old, new_)
	assert.Equal(t, abi.CompatOK, report.OverallStatus)
	assert.Empty(t, report.Changes)
}

func TestCompareSpecs_EnumCaseOrderingIndependent(t *testing.T) {
	c1 := makeEnumCase("Active", 0)
	c2 := makeEnumCase("Inactive", 1)

	old := &abi.ContractSpec{
		Enums: []xdr.ScSpecUdtEnumV0{makeEnum("Status", []xdr.ScSpecUdtEnumCaseV0{c1, c2})},
	}
	new_ := &abi.ContractSpec{
		Enums: []xdr.ScSpecUdtEnumV0{makeEnum("Status", []xdr.ScSpecUdtEnumCaseV0{c2, c1})},
	}
	report := abi.CompareSpecs(old, new_)
	assert.Equal(t, abi.CompatOK, report.OverallStatus)
	assert.Empty(t, report.Changes)
}

func TestCompareSpecs_StructOrderingIndependent(t *testing.T) {
	s1 := makeStruct("Foo", nil)
	s2 := makeStruct("Bar", nil)

	old := &abi.ContractSpec{Structs: []xdr.ScSpecUdtStructV0{s1, s2}}
	new_ := &abi.ContractSpec{Structs: []xdr.ScSpecUdtStructV0{s2, s1}}

	report := abi.CompareSpecs(old, new_)
	assert.Equal(t, abi.CompatOK, report.OverallStatus)
	assert.Empty(t, report.Changes)
}

// ── CompatReport fields ───────────────────────────────────────────────────────

func TestCompatReport_RemediationPopulatedOnBreaking(t *testing.T) {
	old := &abi.ContractSpec{
		Functions: []xdr.ScSpecFunctionV0{makeFunc("mint", nil, nil)},
	}
	new_ := &abi.ContractSpec{}
	report := abi.CompareSpecs(old, new_)
	assert.NotEmpty(t, report.Remediation)
	assert.Contains(t, report.Remediation, "breaking change")
}

func TestCompatReport_RemediationPopulatedOnConditional(t *testing.T) {
	old := &abi.ContractSpec{
		Functions: []xdr.ScSpecFunctionV0{
			makeFunc("get_balance", nil, []xdr.ScSpecTypeDef{i128Type()}),
		},
	}
	new_ := &abi.ContractSpec{
		Functions: []xdr.ScSpecFunctionV0{
			makeFunc("get_balance", nil, []xdr.ScSpecTypeDef{u32Type()}),
		},
	}
	report := abi.CompareSpecs(old, new_)
	assert.NotEmpty(t, report.Remediation)
	assert.Contains(t, report.Remediation, "conditionally compatible")
}

func TestCompatReport_NoRemediationOnOK(t *testing.T) {
	f := makeFunc("transfer", nil, nil)
	old := &abi.ContractSpec{Functions: []xdr.ScSpecFunctionV0{f}}
	new_ := &abi.ContractSpec{Functions: []xdr.ScSpecFunctionV0{f}}
	report := abi.CompareSpecs(old, new_)
	assert.Empty(t, report.Remediation)
}

func TestCompatReport_ChangesAreSortedByPath(t *testing.T) {
	old := &abi.ContractSpec{
		Functions: []xdr.ScSpecFunctionV0{
			makeFunc("zzz_func", nil, nil),
			makeFunc("aaa_func", nil, nil),
		},
	}
	new_ := &abi.ContractSpec{}
	report := abi.CompareSpecs(old, new_)
	require.Len(t, report.Changes, 2)
	assert.Less(t, report.Changes[0].Path, report.Changes[1].Path,
		"changes must be sorted by path")
}

func TestCompatReport_CountsAreAccurate(t *testing.T) {
	// One breaking (function removed), one conditional (return type changed),
	// one additive (function added).
	old := &abi.ContractSpec{
		Functions: []xdr.ScSpecFunctionV0{
			makeFunc("removed_func", nil, nil),
			makeFunc("changed_return", nil, []xdr.ScSpecTypeDef{i128Type()}),
		},
	}
	new_ := &abi.ContractSpec{
		Functions: []xdr.ScSpecFunctionV0{
			makeFunc("changed_return", nil, []xdr.ScSpecTypeDef{u32Type()}),
			makeFunc("new_func", nil, nil),
		},
	}
	report := abi.CompareSpecs(old, new_)
	assert.Equal(t, abi.CompatBreaking, report.OverallStatus)
	assert.Equal(t, 1, report.BreakingCount)
	assert.Equal(t, 1, report.ConditionalCount)
	assert.Equal(t, 1, report.AdditiveCount)
}
