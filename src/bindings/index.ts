// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0
//
// THIS FILE IS AUTO-GENERATED — do not edit manually.
// Regenerate with: glassbox generate-schema --output src/bindings
// Schema version: 1.0.0  |  Glassbox: 0.0.0-dev
// Source: internal/bindings/schema.go

export {
  GLASSBOX_COMMAND_SCHEMA,
  getCommandDefinition,
  getCommandFlags,
  getCommandOutput,
} from './command-schema';
export type {
  CommandSchema,
  CommandDefinition,
  FlagDefinition,
  OutputFieldDefinition,
  MutualExclusionGroup,
  FieldType,
} from './command-schema';

export type { CommandName } from './command-types';

export {
  validateCommandOptions,
  validateCommandOutput,
} from './command-validators';
export type {
  ValidationResult,
  ValidationError,
  ValidationErrorCode,
  ValidationOptions,
} from './command-validators';
