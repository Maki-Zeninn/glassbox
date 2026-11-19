// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// schema.go defines the canonical command-and-output schema for every public
// Glassbox CLI command.  It is the single source of truth from which
// TypeScript types, validation helpers, and documentation fragments are
// generated.
//
// The schema encodes:
//   - Each command's name and subcommand path
//   - Input flags with their types, default values, and constraints
//   - Output field descriptors for the JSON envelope data payload
//   - Documented mutual-exclusion groups (invalid option combinations)
//
// This file is read by GenerateCommandSchema (below) and by
// scripts/generate-ts-bindings.sh to produce the versioned TypeScript
// artifacts under src/bindings/.
package bindings

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/dotandev/glassbox/internal/version"
)

// FieldType enumerates the primitive types that a command flag or output field
// may carry.  The TypeScript generator maps each FieldType to a TypeScript type
// annotation.
type FieldType string

const (
	FieldTypeString  FieldType = "string"
	FieldTypeBool    FieldType = "boolean"
	FieldTypeInt     FieldType = "integer"
	FieldTypeFloat   FieldType = "number"
	FieldTypeEnum    FieldType = "enum"
	FieldTypeArray   FieldType = "array"
	FieldTypeObject  FieldType = "object"
	FieldTypeUnknown FieldType = "unknown"
)

// FlagDefinition describes a single CLI flag accepted by a command.
type FlagDefinition struct {
	// Name is the flag name without the leading dashes, e.g. "network".
	Name string `json:"name"`
	// Short is the optional single-character shorthand, e.g. "n".
	Short string `json:"short,omitempty"`
	// Type is the value type the flag accepts.
	Type FieldType `json:"type"`
	// Default is the string representation of the default value.  Empty when
	// there is no default.
	Default string `json:"default,omitempty"`
	// Required is true when the flag must be supplied.
	Required bool `json:"required,omitempty"`
	// EnumValues lists the allowed values when Type is FieldTypeEnum.
	EnumValues []string `json:"enumValues,omitempty"`
	// Description is a one-line human-readable explanation.
	Description string `json:"description"`
	// Deprecated is non-empty when the flag has been deprecated; the value
	// describes what to use instead.
	Deprecated string `json:"deprecated,omitempty"`
}

// OutputFieldDefinition describes a field in the JSON data payload returned by
// a command when run with --json / --format json.
type OutputFieldDefinition struct {
	// Name is the JSON key, e.g. "status".
	Name string `json:"name"`
	// Type is the value type for this output field.
	Type FieldType `json:"type"`
	// ItemType is the element type when Type is FieldTypeArray.
	ItemType FieldType `json:"itemType,omitempty"`
	// Optional is true when the field may be absent from the output.
	Optional bool `json:"optional,omitempty"`
	// Description is a one-line human-readable explanation.
	Description string `json:"description"`
}

// MutualExclusionGroup documents a set of flags that cannot be used together.
// Both generation and runtime validation use this to surface invalid option
// combinations early.
type MutualExclusionGroup struct {
	// Flags is the set of mutually exclusive flag names.
	Flags []string `json:"flags"`
	// Description explains why the combination is invalid.
	Description string `json:"description"`
}

// CommandDefinition is the canonical descriptor for a single public command
// (or subcommand).
type CommandDefinition struct {
	// Name is the full invocation path, e.g. "debug" or "audit:sign".
	Name string `json:"name"`
	// Short is the one-line help text.
	Short string `json:"short"`
	// Flags contains all accepted input flags.
	Flags []FlagDefinition `json:"flags"`
	// Output contains the fields emitted in the JSON data payload.
	Output []OutputFieldDefinition `json:"output"`
	// MutualExclusions documents invalid option combinations.
	MutualExclusions []MutualExclusionGroup `json:"mutualExclusions,omitempty"`
	// Stable is false when the command is still experimental and its schema
	// may change without a semver bump.
	Stable bool `json:"stable"`
}

// CommandSchema is the top-level versioned container for all command
// definitions.
type CommandSchema struct {
	// SchemaVersion is the version of this schema format (not the CLI version).
	// Increment the major component when a breaking field is removed or renamed.
	SchemaVersion string `json:"schemaVersion"`
	// GlassboxVersion is the CLI version that produced this schema.
	GlassboxVersion string `json:"glassboxVersion"`
	// Commands is the ordered list of all public command definitions.
	Commands []CommandDefinition `json:"commands"`
}

// canonicalCommandDefinitions is the single source of truth for all public
// Glassbox commands.  TypeScript consumers should never hard-code flag names or
// output shapes independently of this registry.
var canonicalCommandDefinitions = []CommandDefinition{
	{
		Name:  "debug",
		Short: "Fetch and simulate a Stellar transaction locally",
		Flags: []FlagDefinition{
			{Name: "network", Short: "n", Type: FieldTypeEnum, Default: "mainnet",
				EnumValues:  []string{"mainnet", "testnet", "futurenet"},
				Description: "Stellar network to query"},
			{Name: "rpc-url", Type: FieldTypeString, Description: "Custom Soroban RPC endpoint"},
			{Name: "xdr-file", Type: FieldTypeString, Description: "Path to an offline XDR envelope file"},
			{Name: "json-file", Type: FieldTypeString, Description: "Path to an offline JSON envelope file"},
			{Name: "wasm", Type: FieldTypeString, Description: "Path to a local WASM binary for replay"},
			{Name: "args", Type: FieldTypeArray, Description: "Arguments to pass to the WASM replay"},
			{Name: "hot-reload", Type: FieldTypeBool, Description: "Re-run automatically when the WASM binary changes"},
			{Name: "demo", Type: FieldTypeBool, Description: "Print sample output without network or WASM"},
			{Name: "dry-run", Type: FieldTypeBool, Description: "Validate inputs without running a simulation"},
			{Name: "compare-network", Type: FieldTypeEnum, EnumValues: []string{"mainnet", "testnet", "futurenet"},
				Description: "Compare results against a second network"},
			{Name: "watch", Type: FieldTypeBool, Description: "Poll for the transaction and debug when it lands"},
			{Name: "watch-timeout", Type: FieldTypeInt, Default: "60", Description: "Watch mode poll timeout in seconds"},
			{Name: "save-snapshots", Type: FieldTypeString, Description: "Path to save ledger snapshot registry"},
			{Name: "load-snapshots", Type: FieldTypeString, Description: "Path to load a previously saved snapshot registry"},
			{Name: "json", Type: FieldTypeBool, Description: "Emit machine-readable JSON output"},
		},
		Output: []OutputFieldDefinition{
			{Name: "status", Type: FieldTypeString, Description: "Transaction status: success | failed"},
			{Name: "network", Type: FieldTypeString, Description: "Network used for the simulation"},
			{Name: "snapshot", Type: FieldTypeString, Description: "Snapshot completeness: complete | partial | none"},
			{Name: "cpu_instructions", Type: FieldTypeInt, Description: "CPU instructions consumed"},
			{Name: "cpu_limit", Type: FieldTypeInt, Description: "CPU instruction budget"},
			{Name: "memory_bytes", Type: FieldTypeInt, Description: "Memory bytes consumed"},
			{Name: "memory_limit", Type: FieldTypeInt, Description: "Memory byte budget"},
			{Name: "operations", Type: FieldTypeInt, Description: "Number of operations in the transaction"},
			{Name: "session_id", Type: FieldTypeString, Description: "Created session identifier"},
			{Name: "events", Type: FieldTypeArray, ItemType: FieldTypeObject, Optional: true, Description: "Diagnostic events emitted during execution"},
			{Name: "error", Type: FieldTypeObject, Optional: true, Description: "Structured error when status is failed"},
		},
		MutualExclusions: []MutualExclusionGroup{
			{Flags: []string{"xdr-file", "json-file", "wasm"}, Description: "Only one offline input source may be specified at a time"},
			{Flags: []string{"xdr-file", "network"}, Description: "--xdr-file is an offline mode; --network is ignored"},
			{Flags: []string{"dry-run", "watch"}, Description: "Cannot dry-run and watch simultaneously"},
		},
		Stable: true,
	},
	{
		Name:  "audit:sign",
		Short: "Generate a signed audit log from a JSON payload",
		Flags: []FlagDefinition{
			{Name: "payload", Type: FieldTypeString, Description: "Inline JSON payload string"},
			{Name: "payload-file", Type: FieldTypeString, Description: "Path to a JSON payload file"},
			{Name: "signing-provider", Type: FieldTypeEnum, Default: "software",
				EnumValues:  []string{"software", "pkcs11", "kms"},
				Description: "Signing backend to use"},
			{Name: "software-private-key", Type: FieldTypeString, Description: "PEM Ed25519 private key or path (software provider)"},
			{Name: "pkcs11-module", Type: FieldTypeString, Description: "Path to the PKCS#11 shared library"},
			{Name: "pkcs11-pin", Type: FieldTypeString, Description: "PKCS#11 user PIN"},
			{Name: "pkcs11-key-label", Type: FieldTypeString, Description: "PKCS#11 key label"},
			{Name: "pkcs11-key-id", Type: FieldTypeString, Description: "PKCS#11 key ID (hex)"},
			{Name: "validate-only", Type: FieldTypeBool, Description: "Run preflight checks without signing"},
			{Name: "json", Type: FieldTypeBool, Description: "Emit machine-readable JSON output"},
		},
		Output: []OutputFieldDefinition{
			{Name: "hash", Type: FieldTypeString, Description: "SHA-256 hex digest of the canonical payload"},
			{Name: "signature", Type: FieldTypeString, Description: "Base64-encoded Ed25519 signature"},
			{Name: "publicKey", Type: FieldTypeString, Description: "Base64-encoded Ed25519 public key"},
			{Name: "timestamp", Type: FieldTypeString, Description: "RFC 3339 timestamp of the signing operation"},
			{Name: "provider", Type: FieldTypeString, Description: "Signing provider used"},
		},
		MutualExclusions: []MutualExclusionGroup{
			{Flags: []string{"payload", "payload-file"}, Description: "Provide the payload inline or via file, not both"},
		},
		Stable: true,
	},
	{
		Name:  "audit:verify",
		Short: "Verify a signed audit log",
		Flags: []FlagDefinition{
			{Name: "input", Type: FieldTypeString, Required: true, Description: "Path to the signed audit log JSON file"},
			{Name: "public-key", Type: FieldTypeString, Description: "Override the public key used for verification"},
			{Name: "json", Type: FieldTypeBool, Description: "Emit machine-readable JSON output"},
		},
		Output: []OutputFieldDefinition{
			{Name: "valid", Type: FieldTypeBool, Description: "True when signature and hash both verify"},
			{Name: "hash_match", Type: FieldTypeBool, Description: "True when the stored hash matches the recomputed hash"},
			{Name: "signature_valid", Type: FieldTypeBool, Description: "True when the signature verifies against the public key"},
			{Name: "chain_valid", Type: FieldTypeBool, Optional: true, Description: "True when the audit chain is intact (multi-entry logs)"},
		},
		Stable: true,
	},
	{
		Name:  "generate-bindings",
		Short: "Generate TypeScript bindings from a Soroban contract ABI",
		Flags: []FlagDefinition{
			{Name: "output", Short: "o", Type: FieldTypeString, Required: true, Description: "Output directory for generated files"},
			{Name: "package", Type: FieldTypeString, Description: "npm package name for the generated bindings"},
			{Name: "contract-id", Type: FieldTypeString, Description: "Stellar contract ID to embed in metadata"},
			{Name: "network", Type: FieldTypeEnum, Default: "testnet",
				EnumValues:  []string{"mainnet", "testnet", "futurenet"},
				Description: "Stellar network"},
			{Name: "runtime", Type: FieldTypeEnum, Default: "node",
				EnumValues:  []string{"node", "browser", "universal"},
				Description: "Target runtime environment"},
			{Name: "spec-file", Type: FieldTypeString, Description: "Load spec from a JSON or XDR ABI file instead of WASM"},
			{Name: "spec-format", Type: FieldTypeEnum, EnumValues: []string{"json", "xdr"},
				Description: "Format of --spec-file (auto-detected when omitted)"},
			{Name: "debug-metadata", Type: FieldTypeBool, Description: "Include ABI debug metadata wrappers"},
			{Name: "no-embed-metadata", Type: FieldTypeBool, Description: "Omit provenance headers from generated files"},
			{Name: "json", Type: FieldTypeBool, Description: "Emit machine-readable JSON output"},
		},
		Output: []OutputFieldDefinition{
			{Name: "package", Type: FieldTypeString, Description: "Generated package name"},
			{Name: "output", Type: FieldTypeString, Description: "Output directory path"},
			{Name: "runtime", Type: FieldTypeString, Description: "Target runtime"},
			{Name: "files", Type: FieldTypeArray, ItemType: FieldTypeString, Description: "Paths of all generated files"},
			{Name: "debug_metadata", Type: FieldTypeBool, Description: "Whether debug metadata wrappers were included"},
		},
		MutualExclusions: []MutualExclusionGroup{
			{Flags: []string{"spec-file", "wasm-file"}, Description: "Provide either a WASM binary or a spec file, not both"},
		},
		Stable: true,
	},
	{
		Name:  "check-bindings",
		Short: "Check whether generated TypeScript bindings are up-to-date",
		Flags: []FlagDefinition{
			{Name: "output", Short: "o", Type: FieldTypeString, Required: true, Description: "Binding directory to validate"},
			{Name: "spec-file", Type: FieldTypeString, Description: "Load spec from a JSON or XDR ABI file"},
			{Name: "spec-format", Type: FieldTypeEnum, EnumValues: []string{"json", "xdr"}, Description: "Format of --spec-file"},
			{Name: "regenerate", Type: FieldTypeBool, Description: "Regenerate stale or missing files automatically"},
			{Name: "json", Type: FieldTypeBool, Description: "Emit machine-readable JSON output"},
		},
		Output: []OutputFieldDefinition{
			{Name: "outputDir", Type: FieldTypeString, Description: "Validated binding directory"},
			{Name: "sourceABIHash", Type: FieldTypeString, Description: "Current ABI hash"},
			{Name: "isStale", Type: FieldTypeBool, Description: "True when any file is stale or missing"},
			{Name: "staleCount", Type: FieldTypeInt, Description: "Number of stale or missing files"},
			{Name: "files", Type: FieldTypeArray, ItemType: FieldTypeObject, Description: "Per-file validation results"},
		},
		Stable: true,
	},
	{
		Name:  "session save",
		Short: "Persist the current debug session to disk",
		Flags: []FlagDefinition{
			{Name: "name", Type: FieldTypeString, Description: "Human-readable name for the session"},
			{Name: "json", Type: FieldTypeBool, Description: "Emit machine-readable JSON output"},
		},
		Output: []OutputFieldDefinition{
			{Name: "session_id", Type: FieldTypeString, Description: "Persisted session identifier"},
			{Name: "path", Type: FieldTypeString, Description: "Path to the saved session directory"},
		},
		Stable: true,
	},
	{
		Name:  "session list",
		Short: "List all saved sessions",
		Flags: []FlagDefinition{
			{Name: "json", Type: FieldTypeBool, Description: "Emit machine-readable JSON output"},
		},
		Output: []OutputFieldDefinition{
			{Name: "sessions", Type: FieldTypeArray, ItemType: FieldTypeObject, Description: "Array of session summary objects"},
		},
		Stable: true,
	},
	{
		Name:  "version",
		Short: "Print version information",
		Flags: []FlagDefinition{
			{Name: "json", Type: FieldTypeBool, Description: "Emit machine-readable JSON output"},
		},
		Output: []OutputFieldDefinition{
			{Name: "version", Type: FieldTypeString, Description: "CLI semantic version"},
			{Name: "commit", Type: FieldTypeString, Optional: true, Description: "Git commit hash"},
			{Name: "build_date", Type: FieldTypeString, Optional: true, Description: "Build date (RFC 3339)"},
			{Name: "go_version", Type: FieldTypeString, Description: "Go toolchain version used to build"},
			{Name: "platform", Type: FieldTypeString, Description: "Target OS/arch"},
		},
		Stable: true,
	},
	{
		Name:  "telemetry",
		Short: "Show current telemetry state and opt-in/out instructions",
		Flags: []FlagDefinition{
			{Name: "json", Type: FieldTypeBool, Description: "Emit machine-readable JSON output"},
		},
		Output: []OutputFieldDefinition{
			{Name: "enabled", Type: FieldTypeBool, Description: "Whether telemetry is currently enabled"},
			{Name: "endpoint", Type: FieldTypeString, Optional: true, Description: "Active OTLP endpoint when enabled"},
			{Name: "anonymized", Type: FieldTypeBool, Description: "Whether anonymized mode is active"},
		},
		Stable: true,
	},
	{
		Name:  "cache status",
		Short: "Show cache usage statistics",
		Flags: []FlagDefinition{
			{Name: "rpc", Type: FieldTypeBool, Description: "Include RPC cache statistics"},
			{Name: "json", Type: FieldTypeBool, Description: "Emit machine-readable JSON output"},
		},
		Output: []OutputFieldDefinition{
			{Name: "total_entries", Type: FieldTypeInt, Description: "Total cached entries"},
			{Name: "total_bytes", Type: FieldTypeInt, Description: "Total cache size in bytes"},
			{Name: "rpc", Type: FieldTypeObject, Optional: true, Description: "RPC cache stats when --rpc is set"},
		},
		Stable: true,
	},
}

// GenerateCommandSchema returns the versioned CommandSchema populated from the
// canonical command definitions.  Commands are sorted by name for
// deterministic output.
func GenerateCommandSchema() *CommandSchema {
	cmds := make([]CommandDefinition, len(canonicalCommandDefinitions))
	copy(cmds, canonicalCommandDefinitions)
	sort.Slice(cmds, func(i, j int) bool {
		return cmds[i].Name < cmds[j].Name
	})

	v := version.Version
	if v == "" {
		v = "dev"
	}

	return &CommandSchema{
		SchemaVersion:   "1.0.0",
		GlassboxVersion: v,
		Commands:        cmds,
	}
}

// MarshalSchema serialises the CommandSchema to canonical JSON bytes.  The
// output is stable across runs: commands are sorted by name and all map keys
// are sorted by the standard encoding/json marshaler. Line endings are normalized to LF.
func MarshalSchema(schema *CommandSchema) ([]byte, error) {
	b, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshalling command schema: %w", err)
	}
	// Normalize line endings for cross-platform consistency
	normalized := strings.ReplaceAll(string(b), "\r\n", "\n")
	return []byte(normalized), nil
}

// CommandByName returns the CommandDefinition for the given command name,
// or (zero-value, false) when not found.
func CommandByName(name string) (CommandDefinition, bool) {
	for _, cmd := range canonicalCommandDefinitions {
		if cmd.Name == name {
			return cmd, true
		}
	}
	return CommandDefinition{}, false
}
