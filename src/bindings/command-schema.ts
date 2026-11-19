// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0
//
// THIS FILE IS AUTO-GENERATED — do not edit manually.
// Regenerate with: glassbox generate-schema --output src/bindings
// Schema version: 1.0.0  |  Glassbox: 0.0.0-dev
// Source: internal/bindings/schema.go

/** The type of a command flag value. */
export type FieldType =
  | 'string'
  | 'boolean'
  | 'integer'
  | 'number'
  | 'enum'
  | 'array'
  | 'object'
  | 'unknown';

/** Describes a single CLI flag accepted by a command. */
export interface FlagDefinition {
  name: string;
  short?: string;
  type: FieldType;
  default?: string;
  required?: boolean;
  enumValues?: string[];
  description: string;
  deprecated?: string;
}

/** Describes a field in the JSON data payload returned by a command. */
export interface OutputFieldDefinition {
  name: string;
  type: FieldType;
  itemType?: FieldType;
  optional?: boolean;
  description: string;
}

/** Documents a set of flags that cannot be used together. */
export interface MutualExclusionGroup {
  flags: string[];
  description: string;
}

/** Canonical descriptor for a public Glassbox command. */
export interface CommandDefinition {
  name: string;
  short: string;
  flags: FlagDefinition[];
  output: OutputFieldDefinition[];
  mutualExclusions?: MutualExclusionGroup[];
  stable: boolean;
}

/** Versioned container for all command definitions. */
export interface CommandSchema {
  schemaVersion: string;
  glassboxVersion: string;
  commands: CommandDefinition[];
}

/** Canonical command schema — do not edit manually. */
export const GLASSBOX_COMMAND_SCHEMA: CommandSchema = {
  "schemaVersion": "1.0.0",
  "glassboxVersion": "0.0.0-dev",
  "commands": [
    {
      "name": "audit:sign",
      "short": "Generate a signed audit log from a JSON payload",
      "flags": [
        {
          "name": "payload",
          "type": "string",
          "description": "Inline JSON payload string"
        },
        {
          "name": "payload-file",
          "type": "string",
          "description": "Path to a JSON payload file"
        },
        {
          "name": "signing-provider",
          "type": "enum",
          "default": "software",
          "enumValues": [
            "software",
            "pkcs11",
            "kms"
          ],
          "description": "Signing backend to use"
        },
        {
          "name": "software-private-key",
          "type": "string",
          "description": "PEM Ed25519 private key or path (software provider)"
        },
        {
          "name": "pkcs11-module",
          "type": "string",
          "description": "Path to the PKCS#11 shared library"
        },
        {
          "name": "pkcs11-pin",
          "type": "string",
          "description": "PKCS#11 user PIN"
        },
        {
          "name": "pkcs11-key-label",
          "type": "string",
          "description": "PKCS#11 key label"
        },
        {
          "name": "pkcs11-key-id",
          "type": "string",
          "description": "PKCS#11 key ID (hex)"
        },
        {
          "name": "validate-only",
          "type": "boolean",
          "description": "Run preflight checks without signing"
        },
        {
          "name": "json",
          "type": "boolean",
          "description": "Emit machine-readable JSON output"
        }
      ],
      "output": [
        {
          "name": "hash",
          "type": "string",
          "description": "SHA-256 hex digest of the canonical payload"
        },
        {
          "name": "signature",
          "type": "string",
          "description": "Base64-encoded Ed25519 signature"
        },
        {
          "name": "publicKey",
          "type": "string",
          "description": "Base64-encoded Ed25519 public key"
        },
        {
          "name": "timestamp",
          "type": "string",
          "description": "RFC 3339 timestamp of the signing operation"
        },
        {
          "name": "provider",
          "type": "string",
          "description": "Signing provider used"
        }
      ],
      "mutualExclusions": [
        {
          "flags": [
            "payload",
            "payload-file"
          ],
          "description": "Provide the payload inline or via file, not both"
        }
      ],
      "stable": true
    },
    {
      "name": "audit:verify",
      "short": "Verify a signed audit log",
      "flags": [
        {
          "name": "input",
          "type": "string",
          "required": true,
          "description": "Path to the signed audit log JSON file"
        },
        {
          "name": "public-key",
          "type": "string",
          "description": "Override the public key used for verification"
        },
        {
          "name": "json",
          "type": "boolean",
          "description": "Emit machine-readable JSON output"
        }
      ],
      "output": [
        {
          "name": "valid",
          "type": "boolean",
          "description": "True when signature and hash both verify"
        },
        {
          "name": "hash_match",
          "type": "boolean",
          "description": "True when the stored hash matches the recomputed hash"
        },
        {
          "name": "signature_valid",
          "type": "boolean",
          "description": "True when the signature verifies against the public key"
        },
        {
          "name": "chain_valid",
          "type": "boolean",
          "optional": true,
          "description": "True when the audit chain is intact (multi-entry logs)"
        }
      ],
      "stable": true
    },
    {
      "name": "cache status",
      "short": "Show cache usage statistics",
      "flags": [
        {
          "name": "rpc",
          "type": "boolean",
          "description": "Include RPC cache statistics"
        },
        {
          "name": "json",
          "type": "boolean",
          "description": "Emit machine-readable JSON output"
        }
      ],
      "output": [
        {
          "name": "total_entries",
          "type": "integer",
          "description": "Total cached entries"
        },
        {
          "name": "total_bytes",
          "type": "integer",
          "description": "Total cache size in bytes"
        },
        {
          "name": "rpc",
          "type": "object",
          "optional": true,
          "description": "RPC cache stats when --rpc is set"
        }
      ],
      "stable": true
    },
    {
      "name": "check-bindings",
      "short": "Check whether generated TypeScript bindings are up-to-date",
      "flags": [
        {
          "name": "output",
          "short": "o",
          "type": "string",
          "required": true,
          "description": "Binding directory to validate"
        },
        {
          "name": "spec-file",
          "type": "string",
          "description": "Load spec from a JSON or XDR ABI file"
        },
        {
          "name": "spec-format",
          "type": "enum",
          "enumValues": [
            "json",
            "xdr"
          ],
          "description": "Format of --spec-file"
        },
        {
          "name": "regenerate",
          "type": "boolean",
          "description": "Regenerate stale or missing files automatically"
        },
        {
          "name": "json",
          "type": "boolean",
          "description": "Emit machine-readable JSON output"
        }
      ],
      "output": [
        {
          "name": "outputDir",
          "type": "string",
          "description": "Validated binding directory"
        },
        {
          "name": "sourceABIHash",
          "type": "string",
          "description": "Current ABI hash"
        },
        {
          "name": "isStale",
          "type": "boolean",
          "description": "True when any file is stale or missing"
        },
        {
          "name": "staleCount",
          "type": "integer",
          "description": "Number of stale or missing files"
        },
        {
          "name": "files",
          "type": "array",
          "itemType": "object",
          "description": "Per-file validation results"
        }
      ],
      "stable": true
    },
    {
      "name": "debug",
      "short": "Fetch and simulate a Stellar transaction locally",
      "flags": [
        {
          "name": "network",
          "short": "n",
          "type": "enum",
          "default": "mainnet",
          "enumValues": [
            "mainnet",
            "testnet",
            "futurenet"
          ],
          "description": "Stellar network to query"
        },
        {
          "name": "rpc-url",
          "type": "string",
          "description": "Custom Soroban RPC endpoint"
        },
        {
          "name": "xdr-file",
          "type": "string",
          "description": "Path to an offline XDR envelope file"
        },
        {
          "name": "json-file",
          "type": "string",
          "description": "Path to an offline JSON envelope file"
        },
        {
          "name": "wasm",
          "type": "string",
          "description": "Path to a local WASM binary for replay"
        },
        {
          "name": "args",
          "type": "array",
          "description": "Arguments to pass to the WASM replay"
        },
        {
          "name": "hot-reload",
          "type": "boolean",
          "description": "Re-run automatically when the WASM binary changes"
        },
        {
          "name": "demo",
          "type": "boolean",
          "description": "Print sample output without network or WASM"
        },
        {
          "name": "dry-run",
          "type": "boolean",
          "description": "Validate inputs without running a simulation"
        },
        {
          "name": "compare-network",
          "type": "enum",
          "enumValues": [
            "mainnet",
            "testnet",
            "futurenet"
          ],
          "description": "Compare results against a second network"
        },
        {
          "name": "watch",
          "type": "boolean",
          "description": "Poll for the transaction and debug when it lands"
        },
        {
          "name": "watch-timeout",
          "type": "integer",
          "default": "60",
          "description": "Watch mode poll timeout in seconds"
        },
        {
          "name": "save-snapshots",
          "type": "string",
          "description": "Path to save ledger snapshot registry"
        },
        {
          "name": "load-snapshots",
          "type": "string",
          "description": "Path to load a previously saved snapshot registry"
        },
        {
          "name": "json",
          "type": "boolean",
          "description": "Emit machine-readable JSON output"
        }
      ],
      "output": [
        {
          "name": "status",
          "type": "string",
          "description": "Transaction status: success | failed"
        },
        {
          "name": "network",
          "type": "string",
          "description": "Network used for the simulation"
        },
        {
          "name": "snapshot",
          "type": "string",
          "description": "Snapshot completeness: complete | partial | none"
        },
        {
          "name": "cpu_instructions",
          "type": "integer",
          "description": "CPU instructions consumed"
        },
        {
          "name": "cpu_limit",
          "type": "integer",
          "description": "CPU instruction budget"
        },
        {
          "name": "memory_bytes",
          "type": "integer",
          "description": "Memory bytes consumed"
        },
        {
          "name": "memory_limit",
          "type": "integer",
          "description": "Memory byte budget"
        },
        {
          "name": "operations",
          "type": "integer",
          "description": "Number of operations in the transaction"
        },
        {
          "name": "session_id",
          "type": "string",
          "description": "Created session identifier"
        },
        {
          "name": "events",
          "type": "array",
          "itemType": "object",
          "optional": true,
          "description": "Diagnostic events emitted during execution"
        },
        {
          "name": "error",
          "type": "object",
          "optional": true,
          "description": "Structured error when status is failed"
        }
      ],
      "mutualExclusions": [
        {
          "flags": [
            "xdr-file",
            "json-file",
            "wasm"
          ],
          "description": "Only one offline input source may be specified at a time"
        },
        {
          "flags": [
            "xdr-file",
            "network"
          ],
          "description": "--xdr-file is an offline mode; --network is ignored"
        },
        {
          "flags": [
            "dry-run",
            "watch"
          ],
          "description": "Cannot dry-run and watch simultaneously"
        }
      ],
      "stable": true
    },
    {
      "name": "generate-bindings",
      "short": "Generate TypeScript bindings from a Soroban contract ABI",
      "flags": [
        {
          "name": "output",
          "short": "o",
          "type": "string",
          "required": true,
          "description": "Output directory for generated files"
        },
        {
          "name": "package",
          "type": "string",
          "description": "npm package name for the generated bindings"
        },
        {
          "name": "contract-id",
          "type": "string",
          "description": "Stellar contract ID to embed in metadata"
        },
        {
          "name": "network",
          "type": "enum",
          "default": "testnet",
          "enumValues": [
            "mainnet",
            "testnet",
            "futurenet"
          ],
          "description": "Stellar network"
        },
        {
          "name": "runtime",
          "type": "enum",
          "default": "node",
          "enumValues": [
            "node",
            "browser",
            "universal"
          ],
          "description": "Target runtime environment"
        },
        {
          "name": "spec-file",
          "type": "string",
          "description": "Load spec from a JSON or XDR ABI file instead of WASM"
        },
        {
          "name": "spec-format",
          "type": "enum",
          "enumValues": [
            "json",
            "xdr"
          ],
          "description": "Format of --spec-file (auto-detected when omitted)"
        },
        {
          "name": "debug-metadata",
          "type": "boolean",
          "description": "Include ABI debug metadata wrappers"
        },
        {
          "name": "no-embed-metadata",
          "type": "boolean",
          "description": "Omit provenance headers from generated files"
        },
        {
          "name": "json",
          "type": "boolean",
          "description": "Emit machine-readable JSON output"
        }
      ],
      "output": [
        {
          "name": "package",
          "type": "string",
          "description": "Generated package name"
        },
        {
          "name": "output",
          "type": "string",
          "description": "Output directory path"
        },
        {
          "name": "runtime",
          "type": "string",
          "description": "Target runtime"
        },
        {
          "name": "files",
          "type": "array",
          "itemType": "string",
          "description": "Paths of all generated files"
        },
        {
          "name": "debug_metadata",
          "type": "boolean",
          "description": "Whether debug metadata wrappers were included"
        }
      ],
      "mutualExclusions": [
        {
          "flags": [
            "spec-file",
            "wasm-file"
          ],
          "description": "Provide either a WASM binary or a spec file, not both"
        }
      ],
      "stable": true
    },
    {
      "name": "session list",
      "short": "List all saved sessions",
      "flags": [
        {
          "name": "json",
          "type": "boolean",
          "description": "Emit machine-readable JSON output"
        }
      ],
      "output": [
        {
          "name": "sessions",
          "type": "array",
          "itemType": "object",
          "description": "Array of session summary objects"
        }
      ],
      "stable": true
    },
    {
      "name": "session save",
      "short": "Persist the current debug session to disk",
      "flags": [
        {
          "name": "name",
          "type": "string",
          "description": "Human-readable name for the session"
        },
        {
          "name": "json",
          "type": "boolean",
          "description": "Emit machine-readable JSON output"
        }
      ],
      "output": [
        {
          "name": "session_id",
          "type": "string",
          "description": "Persisted session identifier"
        },
        {
          "name": "path",
          "type": "string",
          "description": "Path to the saved session directory"
        }
      ],
      "stable": true
    },
    {
      "name": "telemetry",
      "short": "Show current telemetry state and opt-in/out instructions",
      "flags": [
        {
          "name": "json",
          "type": "boolean",
          "description": "Emit machine-readable JSON output"
        }
      ],
      "output": [
        {
          "name": "enabled",
          "type": "boolean",
          "description": "Whether telemetry is currently enabled"
        },
        {
          "name": "endpoint",
          "type": "string",
          "optional": true,
          "description": "Active OTLP endpoint when enabled"
        },
        {
          "name": "anonymized",
          "type": "boolean",
          "description": "Whether anonymized mode is active"
        }
      ],
      "stable": true
    },
    {
      "name": "version",
      "short": "Print version information",
      "flags": [
        {
          "name": "json",
          "type": "boolean",
          "description": "Emit machine-readable JSON output"
        }
      ],
      "output": [
        {
          "name": "version",
          "type": "string",
          "description": "CLI semantic version"
        },
        {
          "name": "commit",
          "type": "string",
          "optional": true,
          "description": "Git commit hash"
        },
        {
          "name": "build_date",
          "type": "string",
          "optional": true,
          "description": "Build date (RFC 3339)"
        },
        {
          "name": "go_version",
          "type": "string",
          "description": "Go toolchain version used to build"
        },
        {
          "name": "platform",
          "type": "string",
          "description": "Target OS/arch"
        }
      ],
      "stable": true
    }
  ]
} as const;

/**
 * Look up a command definition by its full invocation path.
 * Returns undefined when the command is not registered.
 */
export function getCommandDefinition(name: string): CommandDefinition | undefined {
  return GLASSBOX_COMMAND_SCHEMA.commands.find((c) => c.name === name);
}

/**
 * Return all flag definitions for a command.
 * Returns an empty array when the command is not registered.
 */
export function getCommandFlags(name: string): FlagDefinition[] {
  return getCommandDefinition(name)?.flags ?? [];
}

/**
 * Return all output field definitions for a command.
 * Returns an empty array when the command is not registered.
 */
export function getCommandOutput(name: string): OutputFieldDefinition[] {
  return getCommandDefinition(name)?.output ?? [];
}
