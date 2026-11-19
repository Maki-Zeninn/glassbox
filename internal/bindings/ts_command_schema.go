// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// ts_command_schema.go generates versioned TypeScript source files from the
// canonical CommandSchema.  The generated output lives under src/bindings/ and
// is committed to the repository so that TypeScript consumers never drift from
// the authoritative Go definition.
//
// The generator emits three files:
//
//	command-schema.ts       – Runtime-accessible mirror of the full schema
//	command-types.ts        – TypeScript interfaces and union types per command
//	command-validators.ts   – Validation helpers (strict and permissive modes)
//	index.ts                – Barrel export
package bindings

import (
	"fmt"
	"strings"
)

// GenerateCommandSchemaTS generates the TypeScript source for the command
// schema mirror.  The file exports the schema as a typed const so that
// TypeScript consumers can inspect flag definitions and output shapes at
// runtime without hard-coding them.
func GenerateCommandSchemaTS(schema *CommandSchema) string {
	schemaJSON, _ := MarshalSchema(schema)

	var b strings.Builder
	b.WriteString(tsFileHeader(schema.GlassboxVersion))

	b.WriteString("/** The type of a command flag value. */\n")
	b.WriteString("export type FieldType =\n")
	b.WriteString("  | 'string'\n  | 'boolean'\n  | 'integer'\n  | 'number'\n")
	b.WriteString("  | 'enum'\n  | 'array'\n  | 'object'\n  | 'unknown';\n\n")

	b.WriteString("/** Describes a single CLI flag accepted by a command. */\n")
	b.WriteString("export interface FlagDefinition {\n")
	b.WriteString("  name: string;\n  short?: string;\n  type: FieldType;\n")
	b.WriteString("  default?: string;\n  required?: boolean;\n  enumValues?: string[];\n")
	b.WriteString("  description: string;\n  deprecated?: string;\n}\n\n")

	b.WriteString("/** Describes a field in the JSON data payload returned by a command. */\n")
	b.WriteString("export interface OutputFieldDefinition {\n")
	b.WriteString("  name: string;\n  type: FieldType;\n  itemType?: FieldType;\n")
	b.WriteString("  optional?: boolean;\n  description: string;\n}\n\n")

	b.WriteString("/** Documents a set of flags that cannot be used together. */\n")
	b.WriteString("export interface MutualExclusionGroup {\n")
	b.WriteString("  flags: string[];\n  description: string;\n}\n\n")

	b.WriteString("/** Canonical descriptor for a public Glassbox command. */\n")
	b.WriteString("export interface CommandDefinition {\n")
	b.WriteString("  name: string;\n  short: string;\n  flags: FlagDefinition[];\n")
	b.WriteString("  output: OutputFieldDefinition[];\n  mutualExclusions?: MutualExclusionGroup[];\n")
	b.WriteString("  stable: boolean;\n}\n\n")

	b.WriteString("/** Versioned container for all command definitions. */\n")
	b.WriteString("export interface CommandSchema {\n")
	b.WriteString("  schemaVersion: string;\n  glassboxVersion: string;\n  commands: CommandDefinition[];\n}\n\n")

	b.WriteString("/** Canonical command schema — do not edit manually. */\n")
	b.WriteString("export const GLASSBOX_COMMAND_SCHEMA: CommandSchema = ")
	b.Write(schemaJSON)
	b.WriteString(" as const;\n\n")

	b.WriteString("/**\n * Look up a command definition by its full invocation path.\n")
	b.WriteString(" * Returns undefined when the command is not registered.\n */\n")
	b.WriteString("export function getCommandDefinition(name: string): CommandDefinition | undefined {\n")
	b.WriteString("  return GLASSBOX_COMMAND_SCHEMA.commands.find((c) => c.name === name);\n}\n\n")

	b.WriteString("/**\n * Return all flag definitions for a command.\n")
	b.WriteString(" * Returns an empty array when the command is not registered.\n */\n")
	b.WriteString("export function getCommandFlags(name: string): FlagDefinition[] {\n")
	b.WriteString("  return getCommandDefinition(name)?.flags ?? [];\n}\n\n")

	b.WriteString("/**\n * Return all output field definitions for a command.\n")
	b.WriteString(" * Returns an empty array when the command is not registered.\n */\n")
	b.WriteString("export function getCommandOutput(name: string): OutputFieldDefinition[] {\n")
	b.WriteString("  return getCommandDefinition(name)?.output ?? [];\n}\n")

	return b.String()
}

// GenerateCommandTypesTS generates per-command TypeScript interfaces for both
// the input options (flags) and the structured output (JSON data payload).
func GenerateCommandTypesTS(schema *CommandSchema) string {
	var b strings.Builder
	b.WriteString(tsFileHeader(schema.GlassboxVersion))
	b.WriteString("import type { CommandDefinition } from './command-schema';\n\n")
	b.WriteString("// Re-export the schema type for consumers that only need types.\n")
	b.WriteString("export type { CommandDefinition };\n\n")

	for _, cmd := range schema.Commands {
		typeName := commandToTypeName(cmd.Name)
		b.WriteString(fmt.Sprintf("// ── %s ─────────────────────────────────────────────\n\n", cmd.Name))

		// Input options interface.
		b.WriteString(fmt.Sprintf("/** Input options for the `%s` command. */\n", cmd.Name))
		b.WriteString(fmt.Sprintf("export interface %sOptions {\n", typeName))
		for _, flag := range cmd.Flags {
			comment := flag.Description
			if flag.Deprecated != "" {
				comment += fmt.Sprintf(" @deprecated %s", flag.Deprecated)
			}
			optMarker := "?"
			if flag.Required {
				optMarker = ""
			}
			b.WriteString(fmt.Sprintf("  /** %s */\n", comment))
			b.WriteString(fmt.Sprintf("  %s%s: %s;\n", sanitizeFlagName(flag.Name), optMarker, fieldTypeToTS(flag)))
		}
		b.WriteString("}\n\n")

		// Output interface.
		b.WriteString(fmt.Sprintf("/** JSON data payload returned by the `%s` command. */\n", cmd.Name))
		b.WriteString(fmt.Sprintf("export interface %sOutput {\n", typeName))
		for _, field := range cmd.Output {
			optMarker := "?"
			if !field.Optional {
				optMarker = ""
			}
			b.WriteString(fmt.Sprintf("  /** %s */\n", field.Description))
			b.WriteString(fmt.Sprintf("  %s%s: %s;\n", field.Name, optMarker, outputFieldTypeToTS(field)))
		}
		b.WriteString("}\n\n")
	}

	// Union of all command names.
	b.WriteString("/** Union of all public command names. */\n")
	b.WriteString("export type CommandName =\n")
	for _, cmd := range schema.Commands {
		b.WriteString(fmt.Sprintf("  | %q\n", cmd.Name))
	}
	b.WriteString(";\n")

	return b.String()
}

// GenerateCommandValidatorsTS generates TypeScript validator functions for
// command inputs and outputs.  Each validator returns a typed result with
// field-path diagnostics so callers know exactly which field failed and why.
func GenerateCommandValidatorsTS(schema *CommandSchema) string {
	var b strings.Builder
	b.WriteString(tsFileHeader(schema.GlassboxVersion))

	// Static preamble — uses only regular string concatenation; no backtick
	// template literals so the Go source stays compilable.
	lines := []string{
		"import type { FlagDefinition } from './command-schema';",
		"import { getCommandDefinition } from './command-schema';",
		"",
		"/** A single field-level validation error. */",
		"export interface ValidationError {",
		"  /** Dot-separated path to the failing field, e.g. \"options.network\". */",
		"  path: string;",
		"  /** Human-readable explanation of the failure. */",
		"  message: string;",
		"  /** Stable error code that automation can key on. */",
		"  code: ValidationErrorCode;",
		"}",
		"",
		"/** Stable codes for validation failures. */",
		"export type ValidationErrorCode =",
		"  | 'REQUIRED_FIELD_MISSING'",
		"  | 'INVALID_ENUM_VALUE'",
		"  | 'WRONG_TYPE'",
		"  | 'MUTUAL_EXCLUSION_VIOLATED'",
		"  | 'UNKNOWN_FIELD'",
		"  | 'DEPRECATED_FLAG';",
		"",
		"/** Result of a validation operation. */",
		"export interface ValidationResult {",
		"  /** True when all checks passed. */",
		"  valid: boolean;",
		"  /** Array of field-level errors (empty when valid is true). */",
		"  errors: ValidationError[];",
		"}",
		"",
		"/** Options controlling the strictness of validation. */",
		"export interface ValidationOptions {",
		"  /**",
		"   * When true, unknown fields are rejected.",
		"   * When false (permissive mode), unknown fields are silently ignored.",
		"   * Defaults to true.",
		"   */",
		"  strict?: boolean;",
		"  /**",
		"   * When true, deprecated flags produce ValidationErrors in addition to",
		"   * being accepted.  Defaults to false.",
		"   */",
		"  warnDeprecated?: boolean;",
		"}",
		"",
		"const DEFAULT_OPTIONS: Required<ValidationOptions> = {",
		"  strict: true,",
		"  warnDeprecated: false,",
		"};",
		"",
		"/**",
		" * Validate command options against the schema for the named command.",
		" * Returns a ValidationResult with field-path diagnostics.",
		" */",
		"export function validateCommandOptions(",
		"  commandName: string,",
		"  options: Record<string, unknown>,",
		"  opts: ValidationOptions = {},",
		"): ValidationResult {",
		"  const { strict, warnDeprecated } = { ...DEFAULT_OPTIONS, ...opts };",
		"  const errors: ValidationError[] = [];",
		"",
		"  const def = getCommandDefinition(commandName);",
		"  if (!def) {",
		"    errors.push({ path: '', message: 'Unknown command: ' + commandName, code: 'UNKNOWN_FIELD' });",
		"    return { valid: false, errors };",
		"  }",
		"",
		"  const flagsByName = new Map(def.flags.map((f) => [f.name, f]));",
		"",
		"  for (const [key, value] of Object.entries(options)) {",
		"    const flag = flagsByName.get(key);",
		"    if (!flag) {",
		"      if (strict) {",
		"        errors.push({ path: 'options.' + key, message: 'Unknown flag ' + key + ' for command ' + commandName, code: 'UNKNOWN_FIELD' });",
		"      }",
		"      continue;",
		"    }",
		"    if (warnDeprecated && flag.deprecated) {",
		"      errors.push({ path: 'options.' + key, message: 'Flag ' + key + ' is deprecated: ' + flag.deprecated, code: 'DEPRECATED_FLAG' });",
		"    }",
		"    validateFlagValue(flag, key, value, errors);",
		"  }",
		"",
		"  for (const flag of def.flags) {",
		"    if (flag.required && !(flag.name in options)) {",
		"      errors.push({ path: 'options.' + flag.name, message: 'Required flag ' + flag.name + ' is missing', code: 'REQUIRED_FIELD_MISSING' });",
		"    }",
		"  }",
		"",
		"  for (const group of (def.mutualExclusions ?? [])) {",
		"    const present = group.flags.filter((f) => f in options);",
		"    if (present.length > 1) {",
		"      errors.push({ path: 'options.[' + present.join(', ') + ']', message: 'Flags [' + present.join(', ') + '] are mutually exclusive: ' + group.description, code: 'MUTUAL_EXCLUSION_VIOLATED' });",
		"    }",
		"  }",
		"",
		"  return { valid: errors.length === 0, errors };",
		"}",
		"",
		"/**",
		" * Validate a command output payload against the schema for the named command.",
		" * In permissive mode, additive fields are silently accepted.",
		" */",
		"export function validateCommandOutput(",
		"  commandName: string,",
		"  payload: Record<string, unknown>,",
		"  opts: ValidationOptions = {},",
		"): ValidationResult {",
		"  const { strict } = { ...DEFAULT_OPTIONS, ...opts };",
		"  const errors: ValidationError[] = [];",
		"",
		"  const def = getCommandDefinition(commandName);",
		"  if (!def) {",
		"    errors.push({ path: '', message: 'Unknown command: ' + commandName, code: 'UNKNOWN_FIELD' });",
		"    return { valid: false, errors };",
		"  }",
		"",
		"  const outputFields = new Map(def.output.map((f) => [f.name, f]));",
		"",
		"  for (const field of def.output) {",
		"    if (!field.optional && !(field.name in payload)) {",
		"      errors.push({ path: 'output.' + field.name, message: 'Required output field ' + field.name + ' is missing', code: 'REQUIRED_FIELD_MISSING' });",
		"    }",
		"  }",
		"",
		"  for (const [key, value] of Object.entries(payload)) {",
		"    const field = outputFields.get(key);",
		"    if (!field) {",
		"      if (strict) {",
		"        errors.push({ path: 'output.' + key, message: 'Unknown output field ' + key + ' for command ' + commandName, code: 'UNKNOWN_FIELD' });",
		"      }",
		"      continue;",
		"    }",
		"    validateOutputFieldValue(field, key, value, errors);",
		"  }",
		"",
		"  return { valid: errors.length === 0, errors };",
		"}",
		"",
		"// ─── Internal helpers ──────────────────────────────────────────────",
		"",
		"function validateFlagValue(",
		"  flag: FlagDefinition,",
		"  key: string,",
		"  value: unknown,",
		"  errors: ValidationError[],",
		"): void {",
		"  switch (flag.type) {",
		"    case 'boolean':",
		"      if (typeof value !== 'boolean') errors.push({ path: 'options.' + key, message: 'Flag ' + key + ' must be a boolean, got ' + typeof value, code: 'WRONG_TYPE' });",
		"      break;",
		"    case 'integer':",
		"      if (typeof value !== 'number' || !Number.isInteger(value)) errors.push({ path: 'options.' + key, message: 'Flag ' + key + ' must be an integer', code: 'WRONG_TYPE' });",
		"      break;",
		"    case 'number':",
		"      if (typeof value !== 'number') errors.push({ path: 'options.' + key, message: 'Flag ' + key + ' must be a number', code: 'WRONG_TYPE' });",
		"      break;",
		"    case 'enum':",
		"      if (typeof value !== 'string') {",
		"        errors.push({ path: 'options.' + key, message: 'Flag ' + key + ' must be a string (enum)', code: 'WRONG_TYPE' });",
		"      } else if (flag.enumValues && !flag.enumValues.includes(value)) {",
		"        errors.push({ path: 'options.' + key, message: 'Flag ' + key + ' must be one of [' + (flag.enumValues ?? []).join(', ') + '], got ' + value, code: 'INVALID_ENUM_VALUE' });",
		"      }",
		"      break;",
		"    case 'array':",
		"      if (!Array.isArray(value)) errors.push({ path: 'options.' + key, message: 'Flag ' + key + ' must be an array', code: 'WRONG_TYPE' });",
		"      break;",
		"    default:",
		"      if (typeof value !== 'string') errors.push({ path: 'options.' + key, message: 'Flag ' + key + ' must be a string, got ' + typeof value, code: 'WRONG_TYPE' });",
		"  }",
		"}",
		"",
		"function validateOutputFieldValue(",
		"  field: { name: string; type: string; optional?: boolean },",
		"  key: string,",
		"  value: unknown,",
		"  errors: ValidationError[],",
		"): void {",
		"  if (value === null || value === undefined) {",
		"    if (!field.optional) errors.push({ path: 'output.' + key, message: 'Required output field ' + key + ' is null or undefined', code: 'REQUIRED_FIELD_MISSING' });",
		"    return;",
		"  }",
		"  switch (field.type) {",
		"    case 'boolean': if (typeof value !== 'boolean') errors.push({ path: 'output.' + key, message: 'Field ' + key + ' must be boolean', code: 'WRONG_TYPE' }); break;",
		"    case 'integer': if (typeof value !== 'number') errors.push({ path: 'output.' + key, message: 'Field ' + key + ' must be a number', code: 'WRONG_TYPE' }); break;",
		"    case 'array': if (!Array.isArray(value)) errors.push({ path: 'output.' + key, message: 'Field ' + key + ' must be an array', code: 'WRONG_TYPE' }); break;",
		"    case 'object': if (typeof value !== 'object' || Array.isArray(value)) errors.push({ path: 'output.' + key, message: 'Field ' + key + ' must be an object', code: 'WRONG_TYPE' }); break;",
		"    default: if (typeof value !== 'string') errors.push({ path: 'output.' + key, message: 'Field ' + key + ' must be a string', code: 'WRONG_TYPE' });",
		"  }",
		"}",
	}
	for _, line := range lines {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// GenerateCommandSchemaIndexTS generates the barrel export for the bindings
// schema package.
func GenerateCommandSchemaIndexTS(schema *CommandSchema) string {
	var b strings.Builder
	b.WriteString(tsFileHeader(schema.GlassboxVersion))
	lines := []string{
		"export {",
		"  GLASSBOX_COMMAND_SCHEMA,",
		"  getCommandDefinition,",
		"  getCommandFlags,",
		"  getCommandOutput,",
		"} from './command-schema';",
		"export type {",
		"  CommandSchema,",
		"  CommandDefinition,",
		"  FlagDefinition,",
		"  OutputFieldDefinition,",
		"  MutualExclusionGroup,",
		"  FieldType,",
		"} from './command-schema';",
		"",
		"export type { CommandName } from './command-types';",
		"",
		"export {",
		"  validateCommandOptions,",
		"  validateCommandOutput,",
		"} from './command-validators';",
		"export type {",
		"  ValidationResult,",
		"  ValidationError,",
		"  ValidationErrorCode,",
		"  ValidationOptions,",
		"} from './command-validators';",
	}
	for _, line := range lines {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// ─── Internal formatting helpers ─────────────────────────────────────────────

// tsFileHeader returns the standard file header comment for generated files.
func tsFileHeader(glassboxVersion string) string {
	return fmt.Sprintf(
		"// Copyright 2026 Glassbox Users\n"+
			"// SPDX-License-Identifier: Apache-2.0\n"+
			"//\n"+
			"// THIS FILE IS AUTO-GENERATED — do not edit manually.\n"+
			"// Regenerate with: glassbox generate-schema --output src/bindings\n"+
			"// Schema version: 1.0.0  |  Glassbox: %s\n"+
			"// Source: internal/bindings/schema.go\n\n",
		glassboxVersion,
	)
}

// commandToTypeName converts a command name like "audit:sign" to "AuditSign".
func commandToTypeName(name string) string {
	parts := strings.FieldsFunc(name, func(r rune) bool {
		return r == ':' || r == ' ' || r == '-' || r == '_'
	})
	var b strings.Builder
	for _, p := range parts {
		if len(p) > 0 {
			b.WriteString(strings.ToUpper(p[:1]) + p[1:])
		}
	}
	return b.String()
}

// sanitizeFlagName converts a flag name like "rpc-url" to a valid TypeScript
// identifier "rpcUrl".
func sanitizeFlagName(name string) string {
	parts := strings.Split(name, "-")
	if len(parts) == 1 {
		return name
	}
	var b strings.Builder
	b.WriteString(parts[0])
	for _, p := range parts[1:] {
		if len(p) > 0 {
			b.WriteString(strings.ToUpper(p[:1]) + p[1:])
		}
	}
	return b.String()
}

// fieldTypeToTS converts a FlagDefinition to its TypeScript type annotation.
func fieldTypeToTS(flag FlagDefinition) string {
	switch flag.Type {
	case FieldTypeString:
		return "string"
	case FieldTypeBool:
		return "boolean"
	case FieldTypeInt, FieldTypeFloat:
		return "number"
	case FieldTypeArray:
		return "string[]"
	case FieldTypeObject:
		return "Record<string, unknown>"
	case FieldTypeEnum:
		if len(flag.EnumValues) > 0 {
			quoted := make([]string, len(flag.EnumValues))
			for i, v := range flag.EnumValues {
				quoted[i] = fmt.Sprintf("%q", v)
			}
			return strings.Join(quoted, " | ")
		}
		return "string"
	default:
		return "unknown"
	}
}

// outputFieldTypeToTS converts an OutputFieldDefinition to its TypeScript type
// annotation.
func outputFieldTypeToTS(field OutputFieldDefinition) string {
	switch field.Type {
	case FieldTypeString, FieldTypeEnum:
		return "string"
	case FieldTypeBool:
		return "boolean"
	case FieldTypeInt, FieldTypeFloat:
		return "number"
	case FieldTypeArray:
		switch field.ItemType {
		case FieldTypeString, FieldTypeEnum:
			return "string[]"
		case FieldTypeObject:
			return "Record<string, unknown>[]"
		default:
			return "unknown[]"
		}
	case FieldTypeObject:
		return "Record<string, unknown>"
	default:
		return "unknown"
	}
}
