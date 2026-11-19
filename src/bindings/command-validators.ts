// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0
//
// THIS FILE IS AUTO-GENERATED — do not edit manually.
// Regenerate with: glassbox generate-schema --output src/bindings
// Schema version: 1.0.0  |  Glassbox: 0.0.0-dev
// Source: internal/bindings/schema.go

import type { FlagDefinition } from './command-schema';
import { getCommandDefinition } from './command-schema';

/** A single field-level validation error. */
export interface ValidationError {
  /** Dot-separated path to the failing field, e.g. "options.network". */
  path: string;
  /** Human-readable explanation of the failure. */
  message: string;
  /** Stable error code that automation can key on. */
  code: ValidationErrorCode;
}

/** Stable codes for validation failures. */
export type ValidationErrorCode =
  | 'REQUIRED_FIELD_MISSING'
  | 'INVALID_ENUM_VALUE'
  | 'WRONG_TYPE'
  | 'MUTUAL_EXCLUSION_VIOLATED'
  | 'UNKNOWN_FIELD'
  | 'DEPRECATED_FLAG';

/** Result of a validation operation. */
export interface ValidationResult {
  /** True when all checks passed. */
  valid: boolean;
  /** Array of field-level errors (empty when valid is true). */
  errors: ValidationError[];
}

/** Options controlling the strictness of validation. */
export interface ValidationOptions {
  /**
   * When true, unknown fields are rejected.
   * When false (permissive mode), unknown fields are silently ignored.
   * Defaults to true.
   */
  strict?: boolean;
  /**
   * When true, deprecated flags produce ValidationErrors in addition to
   * being accepted.  Defaults to false.
   */
  warnDeprecated?: boolean;
}

const DEFAULT_OPTIONS: Required<ValidationOptions> = {
  strict: true,
  warnDeprecated: false,
};

/**
 * Validate command options against the schema for the named command.
 * Returns a ValidationResult with field-path diagnostics.
 */
export function validateCommandOptions(
  commandName: string,
  options: Record<string, unknown>,
  opts: ValidationOptions = {},
): ValidationResult {
  const { strict, warnDeprecated } = { ...DEFAULT_OPTIONS, ...opts };
  const errors: ValidationError[] = [];

  const def = getCommandDefinition(commandName);
  if (!def) {
    errors.push({ path: '', message: 'Unknown command: ' + commandName, code: 'UNKNOWN_FIELD' });
    return { valid: false, errors };
  }

  const flagsByName = new Map(def.flags.map((f) => [f.name, f]));

  for (const [key, value] of Object.entries(options)) {
    const flag = flagsByName.get(key);
    if (!flag) {
      if (strict) {
        errors.push({ path: 'options.' + key, message: 'Unknown flag ' + key + ' for command ' + commandName, code: 'UNKNOWN_FIELD' });
      }
      continue;
    }
    if (warnDeprecated && flag.deprecated) {
      errors.push({ path: 'options.' + key, message: 'Flag ' + key + ' is deprecated: ' + flag.deprecated, code: 'DEPRECATED_FLAG' });
    }
    validateFlagValue(flag, key, value, errors);
  }

  for (const flag of def.flags) {
    if (flag.required && !(flag.name in options)) {
      errors.push({ path: 'options.' + flag.name, message: 'Required flag ' + flag.name + ' is missing', code: 'REQUIRED_FIELD_MISSING' });
    }
  }

  for (const group of (def.mutualExclusions ?? [])) {
    const present = group.flags.filter((f) => f in options);
    if (present.length > 1) {
      errors.push({ path: 'options.[' + present.join(', ') + ']', message: 'Flags [' + present.join(', ') + '] are mutually exclusive: ' + group.description, code: 'MUTUAL_EXCLUSION_VIOLATED' });
    }
  }

  return { valid: errors.length === 0, errors };
}

/**
 * Validate a command output payload against the schema for the named command.
 * In permissive mode, additive fields are silently accepted.
 */
export function validateCommandOutput(
  commandName: string,
  payload: Record<string, unknown>,
  opts: ValidationOptions = {},
): ValidationResult {
  const { strict } = { ...DEFAULT_OPTIONS, ...opts };
  const errors: ValidationError[] = [];

  const def = getCommandDefinition(commandName);
  if (!def) {
    errors.push({ path: '', message: 'Unknown command: ' + commandName, code: 'UNKNOWN_FIELD' });
    return { valid: false, errors };
  }

  const outputFields = new Map(def.output.map((f) => [f.name, f]));

  for (const field of def.output) {
    if (!field.optional && !(field.name in payload)) {
      errors.push({ path: 'output.' + field.name, message: 'Required output field ' + field.name + ' is missing', code: 'REQUIRED_FIELD_MISSING' });
    }
  }

  for (const [key, value] of Object.entries(payload)) {
    const field = outputFields.get(key);
    if (!field) {
      if (strict) {
        errors.push({ path: 'output.' + key, message: 'Unknown output field ' + key + ' for command ' + commandName, code: 'UNKNOWN_FIELD' });
      }
      continue;
    }
    validateOutputFieldValue(field, key, value, errors);
  }

  return { valid: errors.length === 0, errors };
}

// ─── Internal helpers ──────────────────────────────────────────────

function validateFlagValue(
  flag: FlagDefinition,
  key: string,
  value: unknown,
  errors: ValidationError[],
): void {
  switch (flag.type) {
    case 'boolean':
      if (typeof value !== 'boolean') errors.push({ path: 'options.' + key, message: 'Flag ' + key + ' must be a boolean, got ' + typeof value, code: 'WRONG_TYPE' });
      break;
    case 'integer':
      if (typeof value !== 'number' || !Number.isInteger(value)) errors.push({ path: 'options.' + key, message: 'Flag ' + key + ' must be an integer', code: 'WRONG_TYPE' });
      break;
    case 'number':
      if (typeof value !== 'number') errors.push({ path: 'options.' + key, message: 'Flag ' + key + ' must be a number', code: 'WRONG_TYPE' });
      break;
    case 'enum':
      if (typeof value !== 'string') {
        errors.push({ path: 'options.' + key, message: 'Flag ' + key + ' must be a string (enum)', code: 'WRONG_TYPE' });
      } else if (flag.enumValues && !flag.enumValues.includes(value)) {
        errors.push({ path: 'options.' + key, message: 'Flag ' + key + ' must be one of [' + (flag.enumValues ?? []).join(', ') + '], got ' + value, code: 'INVALID_ENUM_VALUE' });
      }
      break;
    case 'array':
      if (!Array.isArray(value)) errors.push({ path: 'options.' + key, message: 'Flag ' + key + ' must be an array', code: 'WRONG_TYPE' });
      break;
    default:
      if (typeof value !== 'string') errors.push({ path: 'options.' + key, message: 'Flag ' + key + ' must be a string, got ' + typeof value, code: 'WRONG_TYPE' });
  }
}

function validateOutputFieldValue(
  field: { name: string; type: string; optional?: boolean },
  key: string,
  value: unknown,
  errors: ValidationError[],
): void {
  if (value === null || value === undefined) {
    if (!field.optional) errors.push({ path: 'output.' + key, message: 'Required output field ' + key + ' is null or undefined', code: 'REQUIRED_FIELD_MISSING' });
    return;
  }
  switch (field.type) {
    case 'boolean': if (typeof value !== 'boolean') errors.push({ path: 'output.' + key, message: 'Field ' + key + ' must be boolean', code: 'WRONG_TYPE' }); break;
    case 'integer': if (typeof value !== 'number') errors.push({ path: 'output.' + key, message: 'Field ' + key + ' must be a number', code: 'WRONG_TYPE' }); break;
    case 'array': if (!Array.isArray(value)) errors.push({ path: 'output.' + key, message: 'Field ' + key + ' must be an array', code: 'WRONG_TYPE' }); break;
    case 'object': if (typeof value !== 'object' || Array.isArray(value)) errors.push({ path: 'output.' + key, message: 'Field ' + key + ' must be an object', code: 'WRONG_TYPE' }); break;
    default: if (typeof value !== 'string') errors.push({ path: 'output.' + key, message: 'Field ' + key + ' must be a string', code: 'WRONG_TYPE' });
  }
}
