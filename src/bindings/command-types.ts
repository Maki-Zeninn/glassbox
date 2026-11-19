// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0
//
// THIS FILE IS AUTO-GENERATED — do not edit manually.
// Regenerate with: glassbox generate-schema --output src/bindings
// Schema version: 1.0.0  |  Glassbox: 0.0.0-dev
// Source: internal/bindings/schema.go

import type { CommandDefinition } from './command-schema';

// Re-export the schema type for consumers that only need types.
export type { CommandDefinition };

// ── audit:sign ─────────────────────────────────────────────

/** Input options for the `audit:sign` command. */
export interface AuditSignOptions {
  /** Inline JSON payload string */
  payload?: string;
  /** Path to a JSON payload file */
  payloadFile?: string;
  /** Signing backend to use */
  signingProvider?: "software" | "pkcs11" | "kms";
  /** PEM Ed25519 private key or path (software provider) */
  softwarePrivateKey?: string;
  /** Path to the PKCS#11 shared library */
  pkcs11Module?: string;
  /** PKCS#11 user PIN */
  pkcs11Pin?: string;
  /** PKCS#11 key label */
  pkcs11KeyLabel?: string;
  /** PKCS#11 key ID (hex) */
  pkcs11KeyId?: string;
  /** Run preflight checks without signing */
  validateOnly?: boolean;
  /** Emit machine-readable JSON output */
  json?: boolean;
}

/** JSON data payload returned by the `audit:sign` command. */
export interface AuditSignOutput {
  /** SHA-256 hex digest of the canonical payload */
  hash: string;
  /** Base64-encoded Ed25519 signature */
  signature: string;
  /** Base64-encoded Ed25519 public key */
  publicKey: string;
  /** RFC 3339 timestamp of the signing operation */
  timestamp: string;
  /** Signing provider used */
  provider: string;
}

// ── audit:verify ─────────────────────────────────────────────

/** Input options for the `audit:verify` command. */
export interface AuditVerifyOptions {
  /** Path to the signed audit log JSON file */
  input: string;
  /** Override the public key used for verification */
  publicKey?: string;
  /** Emit machine-readable JSON output */
  json?: boolean;
}

/** JSON data payload returned by the `audit:verify` command. */
export interface AuditVerifyOutput {
  /** True when signature and hash both verify */
  valid: boolean;
  /** True when the stored hash matches the recomputed hash */
  hash_match: boolean;
  /** True when the signature verifies against the public key */
  signature_valid: boolean;
  /** True when the audit chain is intact (multi-entry logs) */
  chain_valid?: boolean;
}

// ── cache status ─────────────────────────────────────────────

/** Input options for the `cache status` command. */
export interface CacheStatusOptions {
  /** Include RPC cache statistics */
  rpc?: boolean;
  /** Emit machine-readable JSON output */
  json?: boolean;
}

/** JSON data payload returned by the `cache status` command. */
export interface CacheStatusOutput {
  /** Total cached entries */
  total_entries: number;
  /** Total cache size in bytes */
  total_bytes: number;
  /** RPC cache stats when --rpc is set */
  rpc?: Record<string, unknown>;
}

// ── check-bindings ─────────────────────────────────────────────

/** Input options for the `check-bindings` command. */
export interface CheckBindingsOptions {
  /** Binding directory to validate */
  output: string;
  /** Load spec from a JSON or XDR ABI file */
  specFile?: string;
  /** Format of --spec-file */
  specFormat?: "json" | "xdr";
  /** Regenerate stale or missing files automatically */
  regenerate?: boolean;
  /** Emit machine-readable JSON output */
  json?: boolean;
}

/** JSON data payload returned by the `check-bindings` command. */
export interface CheckBindingsOutput {
  /** Validated binding directory */
  outputDir: string;
  /** Current ABI hash */
  sourceABIHash: string;
  /** True when any file is stale or missing */
  isStale: boolean;
  /** Number of stale or missing files */
  staleCount: number;
  /** Per-file validation results */
  files: Record<string, unknown>[];
}

// ── debug ─────────────────────────────────────────────

/** Input options for the `debug` command. */
export interface DebugOptions {
  /** Stellar network to query */
  network?: "mainnet" | "testnet" | "futurenet";
  /** Custom Soroban RPC endpoint */
  rpcUrl?: string;
  /** Path to an offline XDR envelope file */
  xdrFile?: string;
  /** Path to an offline JSON envelope file */
  jsonFile?: string;
  /** Path to a local WASM binary for replay */
  wasm?: string;
  /** Arguments to pass to the WASM replay */
  args?: string[];
  /** Re-run automatically when the WASM binary changes */
  hotReload?: boolean;
  /** Print sample output without network or WASM */
  demo?: boolean;
  /** Validate inputs without running a simulation */
  dryRun?: boolean;
  /** Compare results against a second network */
  compareNetwork?: "mainnet" | "testnet" | "futurenet";
  /** Poll for the transaction and debug when it lands */
  watch?: boolean;
  /** Watch mode poll timeout in seconds */
  watchTimeout?: number;
  /** Path to save ledger snapshot registry */
  saveSnapshots?: string;
  /** Path to load a previously saved snapshot registry */
  loadSnapshots?: string;
  /** Emit machine-readable JSON output */
  json?: boolean;
}

/** JSON data payload returned by the `debug` command. */
export interface DebugOutput {
  /** Transaction status: success | failed */
  status: string;
  /** Network used for the simulation */
  network: string;
  /** Snapshot completeness: complete | partial | none */
  snapshot: string;
  /** CPU instructions consumed */
  cpu_instructions: number;
  /** CPU instruction budget */
  cpu_limit: number;
  /** Memory bytes consumed */
  memory_bytes: number;
  /** Memory byte budget */
  memory_limit: number;
  /** Number of operations in the transaction */
  operations: number;
  /** Created session identifier */
  session_id: string;
  /** Diagnostic events emitted during execution */
  events?: Record<string, unknown>[];
  /** Structured error when status is failed */
  error?: Record<string, unknown>;
}

// ── generate-bindings ─────────────────────────────────────────────

/** Input options for the `generate-bindings` command. */
export interface GenerateBindingsOptions {
  /** Output directory for generated files */
  output: string;
  /** npm package name for the generated bindings */
  package?: string;
  /** Stellar contract ID to embed in metadata */
  contractId?: string;
  /** Stellar network */
  network?: "mainnet" | "testnet" | "futurenet";
  /** Target runtime environment */
  runtime?: "node" | "browser" | "universal";
  /** Load spec from a JSON or XDR ABI file instead of WASM */
  specFile?: string;
  /** Format of --spec-file (auto-detected when omitted) */
  specFormat?: "json" | "xdr";
  /** Include ABI debug metadata wrappers */
  debugMetadata?: boolean;
  /** Omit provenance headers from generated files */
  noEmbedMetadata?: boolean;
  /** Emit machine-readable JSON output */
  json?: boolean;
}

/** JSON data payload returned by the `generate-bindings` command. */
export interface GenerateBindingsOutput {
  /** Generated package name */
  package: string;
  /** Output directory path */
  output: string;
  /** Target runtime */
  runtime: string;
  /** Paths of all generated files */
  files: string[];
  /** Whether debug metadata wrappers were included */
  debug_metadata: boolean;
}

// ── session list ─────────────────────────────────────────────

/** Input options for the `session list` command. */
export interface SessionListOptions {
  /** Emit machine-readable JSON output */
  json?: boolean;
}

/** JSON data payload returned by the `session list` command. */
export interface SessionListOutput {
  /** Array of session summary objects */
  sessions: Record<string, unknown>[];
}

// ── session save ─────────────────────────────────────────────

/** Input options for the `session save` command. */
export interface SessionSaveOptions {
  /** Human-readable name for the session */
  name?: string;
  /** Emit machine-readable JSON output */
  json?: boolean;
}

/** JSON data payload returned by the `session save` command. */
export interface SessionSaveOutput {
  /** Persisted session identifier */
  session_id: string;
  /** Path to the saved session directory */
  path: string;
}

// ── telemetry ─────────────────────────────────────────────

/** Input options for the `telemetry` command. */
export interface TelemetryOptions {
  /** Emit machine-readable JSON output */
  json?: boolean;
}

/** JSON data payload returned by the `telemetry` command. */
export interface TelemetryOutput {
  /** Whether telemetry is currently enabled */
  enabled: boolean;
  /** Active OTLP endpoint when enabled */
  endpoint?: string;
  /** Whether anonymized mode is active */
  anonymized: boolean;
}

// ── version ─────────────────────────────────────────────

/** Input options for the `version` command. */
export interface VersionOptions {
  /** Emit machine-readable JSON output */
  json?: boolean;
}

/** JSON data payload returned by the `version` command. */
export interface VersionOutput {
  /** CLI semantic version */
  version: string;
  /** Git commit hash */
  commit?: string;
  /** Build date (RFC 3339) */
  build_date?: string;
  /** Go toolchain version used to build */
  go_version: string;
  /** Target OS/arch */
  platform: string;
}

/** Union of all public command names. */
export type CommandName =
  | "audit:sign"
  | "audit:verify"
  | "cache status"
  | "check-bindings"
  | "debug"
  | "generate-bindings"
  | "session list"
  | "session save"
  | "telemetry"
  | "version"
;
