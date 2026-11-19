// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package bindings

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dotandev/glassbox/internal/abi"
	"github.com/dotandev/glassbox/internal/endpoints"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// RuntimeTarget controls which runtime environment the generated bindings
// target. The generator uses this to decide which fetch/transport primitives
// to emit and whether to guard against Node-only globals.
type RuntimeTarget string

const (
	// RuntimeNode targets Node.js (default). Uses child_process.spawn and
	// Node's built-in fetch / https module.
	RuntimeNode RuntimeTarget = "node"
	// RuntimeBrowser targets browser environments. Uses the global fetch API
	// and avoids any Node-only imports (child_process, fs, etc.).
	RuntimeBrowser RuntimeTarget = "browser"
	// RuntimeUniversal emits environment-detection code that selects the
	// appropriate transport at runtime (works in Node, browser, and Electron).
	RuntimeUniversal RuntimeTarget = "universal"
)

// GeneratorConfig holds configuration for TypeScript bindings generation.
type GeneratorConfig struct {
	// WasmBytes is the raw WASM binary. Mutually exclusive with SpecBytes/SpecPath.
	WasmBytes []byte
	// SpecBytes is a pre-loaded ABI/spec file (JSON or XDR). When set, WASM
	// extraction is skipped entirely.
	SpecBytes []byte
	// SpecFormat is the format of SpecBytes. When empty, auto-detection is used.
	SpecFormat abi.ImportFormat

	OutputDir   string
	PackageName string
	ContractID  string
	Network     string

	// RuntimeTarget controls which runtime environment the generated code targets.
	// Defaults to RuntimeNode when empty.
	RuntimeTarget RuntimeTarget

	// IncludeDebugMeta controls whether withDebugMetadata() wrappers are emitted.
	IncludeDebugMeta bool
	// WasmSourcePath is an optional hint embedded in debug metadata.
	WasmSourcePath string

	// NoEmbedArtifactMetadata disables metadata header embedding.  Takes
	// precedence over the default-on behaviour.  Set this to true when you
	// need byte-for-byte reproducible output (e.g. snapshot tests that pin
	// timestamps).
	NoEmbedArtifactMetadata bool

	// fixedGenerationTime pins the generation timestamp used in artifact
	// metadata.  Zero value means use time.Now().  Only set in tests.
	fixedGenerationTime time.Time

	// artifactMeta is the pre-computed metadata to embed.  Populated by
	// Generate() when metadata embedding is enabled (the default).
	artifactMeta *ArtifactMetadata
}

// GeneratedFile represents a generated TypeScript file.
type GeneratedFile struct {
	Path    string
	Content string
}

// Generator generates TypeScript bindings from Soroban contract specs.
type Generator struct {
	config GeneratorConfig
	spec   *abi.ContractSpec
}

// NewGenerator creates a new bindings generator.
// Artifact metadata headers are embedded by default; set
// config.NoEmbedArtifactMetadata = true to disable them.
func NewGenerator(config GeneratorConfig) *Generator {
	return &Generator{config: config}
}

// shouldEmbedMetadata reports whether the generator should embed artifact
// metadata headers.  Metadata is on by default; NoEmbedArtifactMetadata
// overrides it.
func (g *Generator) shouldEmbedMetadata() bool {
	return !g.config.NoEmbedArtifactMetadata
}

// generationTime returns the timestamp to embed in artifact metadata.
// It is a method on GeneratorConfig so tests can override it by setting
// a fixed time via the internal fixedGenerationTime field.
func (c *GeneratorConfig) generationTime() time.Time {
	if !c.fixedGenerationTime.IsZero() {
		return c.fixedGenerationTime
	}
	return time.Now().UTC()
}

// Generate extracts the contract spec and generates TypeScript bindings.
// It accepts three mutually exclusive spec sources (checked in order):
//  1. config.SpecBytes – a pre-loaded JSON or XDR ABI file
//  2. config.WasmBytes – a compiled WASM binary (contractspecv0 section extracted)
//
// When NoEmbedArtifactMetadata is false (the default), every generated file is
// prefixed with a @glassbox-bindings-meta header that encodes the ABI hash,
// contract ID, and generation timestamp.  This header is used by the
// check-bindings command to detect stale artifacts.
func (g *Generator) Generate() ([]GeneratedFile, error) {
	var err error

	switch {
	case len(g.config.SpecBytes) > 0:
		// External ABI/spec file path – no WASM needed.
		format := g.config.SpecFormat
		if format == "" {
			format = abi.DetectFormat(g.config.SpecBytes)
		}
		switch format {
		case abi.ImportFormatJSON:
			g.spec, err = abi.ImportFromJSON(g.config.SpecBytes)
		case abi.ImportFormatXDR:
			g.spec, err = abi.ImportFromXDR(g.config.SpecBytes)
		default:
			return nil, fmt.Errorf("unsupported spec format: %q", format)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to load spec file: %w", err)
		}

	case len(g.config.WasmBytes) > 0:
		// Classic WASM extraction path.
		specBytes, extractErr := abi.ExtractCustomSection(g.config.WasmBytes, "contractspecv0")
		if extractErr != nil {
			return nil, fmt.Errorf("failed to extract contract spec: %w", extractErr)
		}
		if specBytes == nil {
			return nil, fmt.Errorf("contract spec not found in WASM file")
		}
		g.spec, err = abi.DecodeContractSpec(specBytes)
		if err != nil {
			return nil, fmt.Errorf("failed to decode contract spec: %w", err)
		}

	default:
		return nil, fmt.Errorf("no spec source provided: supply either WasmBytes or SpecBytes")
	}

	// Sort spec entries for deterministic output
	g.sortSpec()

	// Compute and store artifact metadata so that generateXxx() helpers can
	// prepend the header to each file.
	if g.shouldEmbedMetadata() {
		abiHash, hashErr := HashABI(g.spec)
		if hashErr != nil {
			return nil, fmt.Errorf("computing ABI hash: %w", hashErr)
		}
		g.config.artifactMeta = &ArtifactMetadata{
			ABIHash:         abiHash,
			ContractID:      g.config.ContractID,
			GeneratedAt:     g.config.generationTime(),
			GlassboxVersion: currentGlassboxVersion(),
		}
	}

	files := []GeneratedFile{
		{Path: "types.ts", Content: normalizeLineEndings(g.generateTypes())},
		{Path: "metadata.ts", Content: normalizeLineEndings(g.generateMetadata())},
		{Path: "client.ts", Content: normalizeLineEndings(g.generateClient())},
		{Path: "Glassbox-integration.ts", Content: normalizeLineEndings(g.generateErstIntegration())},
		{Path: "index.ts", Content: normalizeLineEndings(g.generateIndex())},
		{Path: "package.json", Content: normalizeLineEndings(g.generatePackageJSON())},
		{Path: "README.md", Content: normalizeLineEndings(g.generateReadme())},
	}
	return files, nil
}

// metadataHeader returns the artifact metadata comment block to prepend to a
// generated file, or an empty string when metadata embedding is disabled.
func (g *Generator) metadataHeader() string {
	if g.config.artifactMeta == nil {
		return ""
	}
	return RenderMetadataHeader(*g.config.artifactMeta) + "\n"
}

// runtimeTarget returns the effective RuntimeTarget, defaulting to RuntimeNode.
func (g *Generator) runtimeTarget() RuntimeTarget {
	if g.config.RuntimeTarget == "" {
		return RuntimeNode
	}
	return g.config.RuntimeTarget
}

// generateTypes generates TypeScript type definitions.
func (g *Generator) generateTypes() string {
	var b strings.Builder
	b.WriteString(g.metadataHeader())
	b.WriteString("// Auto-generated TypeScript types for Soroban contract\n")
	b.WriteString("// DO NOT EDIT - Generated by Glassbox generate-bindings\n\n")

	b.WriteString("export type Address = string;\n")
	b.WriteString("export type Bytes = Uint8Array;\n")
	b.WriteString("export type SorobanSymbol = string;\n\n")

	// Result<T,E> helper used by contract functions that return Result types.
	b.WriteString("export type Result<T, E = Error> = { ok: true; value: T } | { ok: false; error: E };\n\n")

	if len(g.spec.Structs) > 0 {
		b.WriteString("// ── Struct Types ────────────────────────────────────────────────────────────\n\n")
		for _, s := range g.spec.Structs {
			g.generateStructType(&b, s)
		}
	}
	if len(g.spec.Enums) > 0 {
		b.WriteString("// ── Enum Types ──────────────────────────────────────────────────────────────\n\n")
		for _, e := range g.spec.Enums {
			g.generateEnumType(&b, e)
		}
	}
	if len(g.spec.Unions) > 0 {
		b.WriteString("// ── Union Types ─────────────────────────────────────────────────────────────\n\n")
		for _, u := range g.spec.Unions {
			g.generateUnionType(&b, u)
		}
	}
	if len(g.spec.ErrorEnums) > 0 {
		b.WriteString("// ── Error Types ─────────────────────────────────────────────────────────────\n\n")
		for _, e := range g.spec.ErrorEnums {
			g.generateErrorEnumType(&b, e)
		}
	}
	if len(g.spec.Events) > 0 {
		b.WriteString("// ── Event Types ─────────────────────────────────────────────────────────────\n\n")
		for _, ev := range g.spec.Events {
			g.generateEventType(&b, ev)
		}
	}
	return b.String()
}

func (g *Generator) generateStructType(b *strings.Builder, s xdr.ScSpecUdtStructV0) {
	if s.Doc != "" {
		fmt.Fprintf(b, "/** %s */\n", s.Doc)
	}
	fmt.Fprintf(b, "export interface %s {\n", string(s.Name))
	for _, field := range s.Fields {
		fmt.Fprintf(b, "  %s: %s;\n", field.Name, g.mapTypeDefToTS(field.Type))
	}
	b.WriteString("}\n\n")
}

func (g *Generator) generateEnumType(b *strings.Builder, e xdr.ScSpecUdtEnumV0) {
	if e.Doc != "" {
		fmt.Fprintf(b, "/** %s */\n", e.Doc)
	}
	fmt.Fprintf(b, "export enum %s {\n", string(e.Name))
	for _, c := range e.Cases {
		fmt.Fprintf(b, "  %s = %d,\n", c.Name, c.Value)
	}
	b.WriteString("}\n\n")
}

func (g *Generator) generateUnionType(b *strings.Builder, u xdr.ScSpecUdtUnionV0) {
	if u.Doc != "" {
		fmt.Fprintf(b, "/** %s */\n", u.Doc)
	}
	fmt.Fprintf(b, "export type %s =\n", string(u.Name))
	for i, c := range u.Cases {
		switch c.Kind {
		case xdr.ScSpecUdtUnionCaseV0KindScSpecUdtUnionCaseVoidV0:
			fmt.Fprintf(b, "  | { tag: '%s' }", c.VoidCase.Name)
		case xdr.ScSpecUdtUnionCaseV0KindScSpecUdtUnionCaseTupleV0:
			types := make([]string, len(c.TupleCase.Type))
			for j, t := range c.TupleCase.Type {
				types[j] = g.mapTypeDefToTS(t)
			}
			fmt.Fprintf(b, "  | { tag: '%s'; values: [%s] }", c.TupleCase.Name, strings.Join(types, ", "))
		}
		if i < len(u.Cases)-1 {
			b.WriteString("\n")
		}
	}
	b.WriteString(";\n\n")
}

func (g *Generator) generateErrorEnumType(b *strings.Builder, e xdr.ScSpecUdtErrorEnumV0) {
	enumName := string(e.Name)
	if e.Doc != "" {
		fmt.Fprintf(b, "/** %s */\n", e.Doc)
	}
	fmt.Fprintf(b, "export enum %s {\n", enumName)
	for _, c := range e.Cases {
		fmt.Fprintf(b, "  %s = %d,\n", c.Name, c.Value)
	}
	b.WriteString("}\n\n")
	fmt.Fprintf(b, "export class %sError extends Error {\n", enumName)
	fmt.Fprintf(b, "  constructor(public readonly code: %s, message?: string) {\n", enumName)
	b.WriteString("    super(message ?? `Contract error: ${code}`);\n")
	fmt.Fprintf(b, "    this.name = '%sError';\n", enumName)
	b.WriteString("  }\n}\n\n")
}

func (g *Generator) generateEventType(b *strings.Builder, ev xdr.ScSpecEventV0) {
	if ev.Doc != "" {
		fmt.Fprintf(b, "/** %s */\n", ev.Doc)
	}
	fmt.Fprintf(b, "export interface %sEvent {\n", string(ev.Name))
	for _, param := range ev.Params {
		loc := "data"
		if param.Location == xdr.ScSpecEventParamLocationV0ScSpecEventParamLocationTopicList {
			loc = "topic"
		}
		fmt.Fprintf(b, "  %s: %s; // %s\n", param.Name, g.mapTypeDefToTS(param.Type), loc)
	}
	b.WriteString("}\n\n")
}

// mapTypeDefToTS converts Soroban type definitions to TypeScript types.
func (g *Generator) mapTypeDefToTS(td xdr.ScSpecTypeDef) string {
	switch td.Type {
	case xdr.ScSpecTypeScSpecTypeVal:
		return "unknown"
	case xdr.ScSpecTypeScSpecTypeBool:
		return "boolean"
	case xdr.ScSpecTypeScSpecTypeVoid:
		return "void"
	case xdr.ScSpecTypeScSpecTypeError:
		return "Error"
	case xdr.ScSpecTypeScSpecTypeU32, xdr.ScSpecTypeScSpecTypeI32:
		return "number"
	case xdr.ScSpecTypeScSpecTypeU64, xdr.ScSpecTypeScSpecTypeI64:
		return "bigint"
	case xdr.ScSpecTypeScSpecTypeTimepoint:
		return "Date"
	case xdr.ScSpecTypeScSpecTypeDuration:
		return "number"
	case xdr.ScSpecTypeScSpecTypeU128, xdr.ScSpecTypeScSpecTypeI128:
		return "bigint"
	case xdr.ScSpecTypeScSpecTypeU256, xdr.ScSpecTypeScSpecTypeI256:
		return "bigint"
	case xdr.ScSpecTypeScSpecTypeBytes:
		return "Bytes"
	case xdr.ScSpecTypeScSpecTypeString:
		return "string"
	case xdr.ScSpecTypeScSpecTypeSymbol:
		return "SorobanSymbol"
	case xdr.ScSpecTypeScSpecTypeAddress, xdr.ScSpecTypeScSpecTypeMuxedAddress:
		return "Address"
	case xdr.ScSpecTypeScSpecTypeOption:
		if td.Option != nil {
			return fmt.Sprintf("%s | null", g.mapTypeDefToTS(td.Option.ValueType))
		}
		return "unknown | null"
	case xdr.ScSpecTypeScSpecTypeResult:
		if td.Result != nil {
			return fmt.Sprintf("Result<%s, %s>", g.mapTypeDefToTS(td.Result.OkType), g.mapTypeDefToTS(td.Result.ErrorType))
		}
		return "Result<unknown, unknown>"
	case xdr.ScSpecTypeScSpecTypeVec:
		if td.Vec != nil {
			return fmt.Sprintf("Array<%s>", g.mapTypeDefToTS(td.Vec.ElementType))
		}
		return "Array<unknown>"
	case xdr.ScSpecTypeScSpecTypeMap:
		if td.Map != nil {
			return fmt.Sprintf("Map<%s, %s>", g.mapTypeDefToTS(td.Map.KeyType), g.mapTypeDefToTS(td.Map.ValueType))
		}
		return "Map<unknown, unknown>"
	case xdr.ScSpecTypeScSpecTypeTuple:
		if td.Tuple != nil {
			types := make([]string, len(td.Tuple.ValueTypes))
			for i, t := range td.Tuple.ValueTypes {
				types[i] = g.mapTypeDefToTS(t)
			}
			return fmt.Sprintf("[%s]", strings.Join(types, ", "))
		}
		return "[]"
	case xdr.ScSpecTypeScSpecTypeBytesN:
		if td.BytesN != nil {
			return fmt.Sprintf("Uint8Array /* length: %d */", td.BytesN.N)
		}
		return "Uint8Array"
	case xdr.ScSpecTypeScSpecTypeUdt:
		if td.Udt != nil {
			return td.Udt.Name
		}
		return "unknown"
	default:
		return "unknown"
	}
}

// generateMetadata generates metadata.ts with per-function ABI descriptors and
// optional source-location hints.
func (g *Generator) generateMetadata() string {
	var b strings.Builder
	b.WriteString(g.metadataHeader())
	b.WriteString("// Auto-generated ABI metadata for Soroban contract\n")
	b.WriteString("// DO NOT EDIT - Generated by Glassbox generate-bindings\n\n")

	b.WriteString("/** Source-location hint embedded in debug metadata. */\n")
	b.WriteString("export interface SourceHint {\n")
	b.WriteString("  /** Path to the WASM or source file. */\n")
	b.WriteString("  sourcePath: string;\n")
	b.WriteString("  /** Zero-based index of the function in the contract spec. */\n")
	b.WriteString("  operationIndex: number;\n")
	b.WriteString("}\n\n")

	b.WriteString("/** ABI descriptor for a single contract function parameter. */\n")
	b.WriteString("export interface ParamDescriptor {\n")
	b.WriteString("  name: string;\n")
	b.WriteString("  sorobanType: string;\n")
	b.WriteString("  tsType: string;\n")
	b.WriteString("}\n\n")

	b.WriteString("/** Full ABI descriptor for a contract function. */\n")
	b.WriteString("export interface FunctionMetadata {\n")
	b.WriteString("  name: string;\n")
	b.WriteString("  doc: string;\n")
	b.WriteString("  inputs: ParamDescriptor[];\n")
	b.WriteString("  outputs: string[];\n")
	b.WriteString("  source?: SourceHint;\n")
	b.WriteString("}\n\n")

	b.WriteString("/** Registry of all contract function metadata, keyed by function name. */\n")
	b.WriteString("export const CONTRACT_METADATA: Record<string, FunctionMetadata> = {\n")

	sourcePath := g.config.WasmSourcePath
	if sourcePath == "" {
		sourcePath = "<unknown>"
	}
	// Normalize path separators for cross-platform consistency
	sourcePath = strings.ReplaceAll(sourcePath, "\\", "/")

	for idx, fn := range g.spec.Functions {
		name := string(fn.Name)
		fmt.Fprintf(&b, "  %s: {\n", name)
		fmt.Fprintf(&b, "    name: %q,\n", name)
		doc := fn.Doc
		if doc == "" {
			doc = ""
		}
		fmt.Fprintf(&b, "    doc: %q,\n", doc)

		b.WriteString("    inputs: [\n")
		for _, inp := range fn.Inputs {
			sorobanType := abi.FormatTypeDef(inp.Type)
			tsType := g.mapTypeDefToTS(inp.Type)
			fmt.Fprintf(&b, "      { name: %q, sorobanType: %q, tsType: %q },\n",
				inp.Name, sorobanType, tsType)
		}
		b.WriteString("    ],\n")

		b.WriteString("    outputs: [")
		for i, out := range fn.Outputs {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%q", abi.FormatTypeDef(out))
		}
		b.WriteString("],\n")

		fmt.Fprintf(&b, "    source: { sourcePath: %q, operationIndex: %d },\n", sourcePath, idx)
		b.WriteString("  },\n")
	}
	b.WriteString("};\n")
	return b.String()
}

// generateClient generates the main TypeScript client class with strongly-typed
// methods and optional debug-metadata wrappers. The generated code adapts to
// the configured RuntimeTarget (node / browser / universal).
func (g *Generator) generateClient() string {
	var b strings.Builder
	b.WriteString(g.metadataHeader())
	b.WriteString("// Auto-generated TypeScript client for Soroban contract\n")
	b.WriteString("// DO NOT EDIT - Generated by Glassbox generate-bindings\n\n")

	target := g.runtimeTarget()

	// Imports vary by runtime target.
	b.WriteString("import * as StellarSdk from '@stellar/stellar-sdk';\n")
	b.WriteString("import { ErstSimulator } from './Glassbox-integration';\n")
	b.WriteString("import * as Types from './types';\n")
	b.WriteString("import { CONTRACT_METADATA, FunctionMetadata } from './metadata';\n\n")

	// SorobanProvider interface – allows injecting a custom RPC provider.
	b.WriteString("/**\n")
	b.WriteString(" * A custom Soroban RPC provider. Implement this interface to use a\n")
	b.WriteString(" * non-default transport (e.g. a browser-native fetch wrapper, a\n")
	b.WriteString(" * proxied endpoint, or a mock for testing).\n")
	b.WriteString(" */\n")
	b.WriteString("export interface SorobanProvider {\n")
	b.WriteString("  /** The RPC endpoint URL. */\n")
	b.WriteString("  rpcUrl: string;\n")
	b.WriteString("  /** Optional network passphrase override. */\n")
	b.WriteString("  networkPassphrase?: string;\n")
	b.WriteString("  /**\n")
	b.WriteString("   * Optional custom fetch implementation. When omitted the global fetch\n")
	b.WriteString("   * (browser / Node 18+) or a Node https adapter is used automatically.\n")
	b.WriteString("   */\n")
	b.WriteString("  fetch?: typeof fetch;\n")
	b.WriteString("}\n\n")

	b.WriteString("export interface ClientConfig {\n")
	b.WriteString("  contractId: string;\n")
	b.WriteString("  network: 'testnet' | 'mainnet' | 'futurenet';\n")
	b.WriteString("  rpcUrl?: string;\n")
	b.WriteString("  enableSimulation?: boolean;\n")
	b.WriteString("  /**\n")
	b.WriteString("   * Inject a custom Soroban RPC provider. When set, `rpcUrl` and the\n")
	b.WriteString("   * built-in network defaults are ignored in favour of the provider's\n")
	b.WriteString("   * `rpcUrl` and optional `networkPassphrase`.\n")
	b.WriteString("   */\n")
	b.WriteString("  provider?: SorobanProvider;\n")
	b.WriteString("}\n\n")

	b.WriteString("export interface CallOptions {\n")
	b.WriteString("  simulate?: boolean;\n")
	b.WriteString("  fee?: string;\n")
	b.WriteString("  memo?: StellarSdk.Memo;\n")
	b.WriteString("  timeoutInSeconds?: number;\n")
	b.WriteString("  /** Attach ABI debug metadata to the call context. */\n")
	b.WriteString("  withDebugMetadata?: boolean;\n")
	b.WriteString("}\n\n")

	b.WriteString("export interface CallResult<T> {\n")
	b.WriteString("  result: T;\n")
	b.WriteString("  transactionHash?: string;\n")
	b.WriteString("  simulation?: unknown;\n")
	b.WriteString("  /** ABI metadata for the invoked function (present when withDebugMetadata is true). */\n")
	b.WriteString("  debugMetadata?: FunctionMetadata;\n")
	b.WriteString("}\n\n")

	// Environment-detection helper (only emitted for universal target).
	if target == RuntimeUniversal {
		g.generateEnvDetection(&b)
	}

	className := toPascalCase(g.config.PackageName) + "Client"
	fmt.Fprintf(&b, "export class %s {\n", className)
	b.WriteString("  private readonly config: ClientConfig;\n")
	b.WriteString("  private readonly server: StellarSdk.SorobanRpc.Server;\n")
	b.WriteString("  private readonly simulator?: ErstSimulator;\n\n")

	b.WriteString("  constructor(config: ClientConfig) {\n")
	b.WriteString("    this.config = config;\n")
	b.WriteString("    const networkUrl = config.provider?.rpcUrl ?? config.rpcUrl ?? this.getDefaultRpcUrl(config.network);\n")

	switch target {
	case RuntimeBrowser:
		// Browser: pass the custom fetch (or global fetch) to the SDK server.
		b.WriteString("    const customFetch = config.provider?.fetch ?? globalThis.fetch.bind(globalThis);\n")
		b.WriteString("    this.server = new StellarSdk.SorobanRpc.Server(networkUrl, { allowHttp: false, fetch: customFetch });\n")
	case RuntimeUniversal:
		// Universal: pick fetch at runtime.
		b.WriteString("    const customFetch = config.provider?.fetch ?? _pickFetch();\n")
		b.WriteString("    this.server = new StellarSdk.SorobanRpc.Server(networkUrl, { fetch: customFetch });\n")
	default: // RuntimeNode
		// Node: custom fetch if provided, otherwise let the SDK use its default.
		b.WriteString("    const serverOpts = config.provider?.fetch ? { fetch: config.provider.fetch } : {};\n")
		b.WriteString("    this.server = new StellarSdk.SorobanRpc.Server(networkUrl, serverOpts);\n")
	}

	b.WriteString("    if (config.enableSimulation) {\n")
	b.WriteString("      this.simulator = new ErstSimulator({ network: config.network, rpcUrl: networkUrl });\n")
	b.WriteString("    }\n")
	b.WriteString("  }\n\n")

	b.WriteString("  private getDefaultRpcUrl(network: string): string {\n")
	b.WriteString("    switch (network) {\n")
	fmt.Fprintf(&b, "      case 'testnet':  return '%s';\n", endpoints.SorobanTestnet)
	fmt.Fprintf(&b, "      case 'mainnet':  return '%s';\n", endpoints.SorobanMainnet)
	fmt.Fprintf(&b, "      case 'futurenet': return '%s';\n", endpoints.SorobanFuturenet)
	b.WriteString("      default: throw new Error(`Unknown network: ${network}`);\n")
	b.WriteString("    }\n  }\n\n")

	b.WriteString("  private getNetworkPassphrase(): string {\n")
	b.WriteString("    // Custom provider may supply its own passphrase (e.g. for private networks).\n")
	b.WriteString("    if (this.config.provider?.networkPassphrase) {\n")
	b.WriteString("      return this.config.provider.networkPassphrase;\n")
	b.WriteString("    }\n")
	b.WriteString("    switch (this.config.network) {\n")
	b.WriteString("      case 'testnet':   return StellarSdk.Networks.TESTNET;\n")
	b.WriteString("      case 'mainnet':   return StellarSdk.Networks.PUBLIC;\n")
	b.WriteString("      case 'futurenet': return StellarSdk.Networks.FUTURENET;\n")
	b.WriteString("      default: throw new Error(`Unknown network: ${this.config.network}`);\n")
	b.WriteString("    }\n  }\n\n")

	b.WriteString("  private parseResult(txResult: StellarSdk.SorobanRpc.Api.GetSuccessfulTransactionResponse): unknown {\n")
	b.WriteString("    if (txResult.returnValue) {\n")
	b.WriteString("      return StellarSdk.scValToNative(txResult.returnValue);\n")
	b.WriteString("    }\n")
	b.WriteString("    throw new Error('Transaction succeeded but returned no value');\n")
	b.WriteString("  }\n\n")

	if len(g.spec.Functions) > 0 {
		b.WriteString("  // ── Contract Methods ────────────────────────────────────────────────────────\n\n")
		for _, fn := range g.spec.Functions {
			g.generateClientMethod(&b, fn)
		}
	}
	b.WriteString("}\n")
	return b.String()
}

// generateEnvDetection emits a small runtime environment-detection helper used
// by the universal target to pick the right fetch implementation.
func (g *Generator) generateEnvDetection(b *strings.Builder) {
	b.WriteString("// ── Runtime environment detection ────────────────────────────────────────────\n\n")
	b.WriteString("/** @internal Returns a fetch function appropriate for the current runtime. */\n")
	b.WriteString("function _pickFetch(): typeof fetch {\n")
	b.WriteString("  // Browser / Electron renderer / Node 18+ all expose globalThis.fetch.\n")
	b.WriteString("  if (typeof globalThis !== 'undefined' && typeof (globalThis as any).fetch === 'function') {\n")
	b.WriteString("    return (globalThis as any).fetch.bind(globalThis);\n")
	b.WriteString("  }\n")
	b.WriteString("  // Node.js < 18: attempt to load node-fetch dynamically.\n")
	b.WriteString("  // eslint-disable-next-line @typescript-eslint/no-var-requires\n")
	b.WriteString("  try { return require('node-fetch'); } catch { /* ignore */ }\n")
	b.WriteString("  throw new Error(\n")
	b.WriteString("    'No fetch implementation found. In Node < 18 install node-fetch, ' +\n")
	b.WriteString("    'or pass a custom provider.fetch to the client constructor.'\n")
	b.WriteString("  );\n")
	b.WriteString("}\n\n")
}

func (g *Generator) generateClientMethod(b *strings.Builder, fn xdr.ScSpecFunctionV0) {
	methodName := string(fn.Name)
	if fn.Doc != "" {
		fmt.Fprintf(b, "  /**\n   * %s\n   *\n", fn.Doc)
	} else {
		fmt.Fprintf(b, "  /**\n   * Calls the `%s` contract function.\n   *\n", methodName)
	}
	for _, inp := range fn.Inputs {
		fmt.Fprintf(b, "   * @param %s - %s\n", inp.Name, g.mapTypeDefToTS(inp.Type))
	}
	b.WriteString("   */\n")

	// Build parameter list: source keypair, typed contract params, options.
	params := []string{"source: StellarSdk.Keypair"}
	for _, inp := range fn.Inputs {
		params = append(params, fmt.Sprintf("%s: %s", inp.Name, g.mapTypeDefToTS(inp.Type)))
	}
	params = append(params, "options: CallOptions = {}")

	returnType := "void"
	if len(fn.Outputs) > 0 {
		returnType = g.mapTypeDefToTS(fn.Outputs[0])
	}

	fmt.Fprintf(b, "  async %s(%s): Promise<CallResult<%s>> {\n",
		methodName, strings.Join(params, ", "), returnType)

	// Input validation guard for non-void params.
	for _, inp := range fn.Inputs {
		tsType := g.mapTypeDefToTS(inp.Type)
		if tsType == "string" || tsType == "Address" {
			fmt.Fprintf(b, "    if (%s === undefined || %s === null) {\n", inp.Name, inp.Name)
			fmt.Fprintf(b, "      throw new TypeError('Parameter %s must not be null or undefined');\n", inp.Name)
			b.WriteString("    }\n")
		}
	}
	b.WriteString("\n")

	b.WriteString("    const contract = new StellarSdk.Contract(this.config.contractId);\n")
	fmt.Fprintf(b, "    const operation = contract.call('%s'", methodName)
	for _, inp := range fn.Inputs {
		fmt.Fprintf(b, ", %s", inp.Name)
	}
	b.WriteString(");\n\n")

	b.WriteString("    const account = await this.server.getAccount(source.publicKey());\n")
	b.WriteString("    const tx = new StellarSdk.TransactionBuilder(account, {\n")
	b.WriteString("      fee: options.fee ?? StellarSdk.BASE_FEE,\n")
	b.WriteString("      networkPassphrase: this.getNetworkPassphrase(),\n")
	b.WriteString("    })\n")
	b.WriteString("      .addOperation(operation)\n")
	b.WriteString("      .setTimeout(options.timeoutInSeconds ?? 30)\n")
	b.WriteString("      .build();\n\n")

	// Debug metadata attachment.
	b.WriteString("    const debugMetadata = options.withDebugMetadata\n")
	fmt.Fprintf(b, "      ? CONTRACT_METADATA['%s']\n", methodName)
	b.WriteString("      : undefined;\n\n")

	// Simulation path.
	b.WriteString("    if (options.simulate && this.simulator) {\n")
	b.WriteString("      const simResult = await this.simulator.simulate(tx);\n")
	b.WriteString("      return { result: simResult.result as " + returnType + ", simulation: simResult, debugMetadata };\n")
	b.WriteString("    }\n\n")

	// Execution path.
	b.WriteString("    tx.sign(source);\n")
	b.WriteString("    const response = await this.server.sendTransaction(tx);\n")
	b.WriteString("    if (response.status !== 'PENDING') {\n")
	b.WriteString("      throw new Error(`Transaction submission failed: ${response.status}`);\n")
	b.WriteString("    }\n\n")

	b.WriteString("    // Poll until confirmed.\n")
	b.WriteString("    let txResult = await this.server.getTransaction(response.hash);\n")
	b.WriteString("    while (txResult.status === StellarSdk.SorobanRpc.Api.GetTransactionStatus.NOT_FOUND) {\n")
	b.WriteString("      await new Promise(r => setTimeout(r, 1000));\n")
	b.WriteString("      txResult = await this.server.getTransaction(response.hash);\n")
	b.WriteString("    }\n")
	b.WriteString("    if (txResult.status !== StellarSdk.SorobanRpc.Api.GetTransactionStatus.SUCCESS) {\n")
	b.WriteString("      throw new Error(`Transaction failed: ${txResult.status}`);\n")
	b.WriteString("    }\n\n")

	b.WriteString("    return {\n")
	b.WriteString("      result: this.parseResult(txResult as StellarSdk.SorobanRpc.Api.GetSuccessfulTransactionResponse) as " + returnType + ",\n")
	b.WriteString("      transactionHash: response.hash,\n")
	b.WriteString("      debugMetadata,\n")
	b.WriteString("    };\n")
	b.WriteString("  }\n\n")
}

// generateErstIntegration generates the Glassbox simulator integration file.
// For browser targets the child_process-based runner is replaced with a
// fetch-based HTTP shim so the file remains importable without Node globals.
func (g *Generator) generateErstIntegration() string {
	var b strings.Builder
	b.WriteString(g.metadataHeader())
	b.WriteString("// Glassbox simulator integration for local testing and debugging\n")
	b.WriteString("// DO NOT EDIT - Generated by Glassbox generate-bindings\n\n")

	target := g.runtimeTarget()

	// Only import child_process when targeting Node or universal.
	switch target {
	case RuntimeBrowser:
		b.WriteString("// Browser target: child_process is not available.\n")
		b.WriteString("// Use the HTTP-based simulate/debug endpoints instead.\n\n")
	case RuntimeUniversal:
		b.WriteString("// Universal target: child_process is loaded lazily so the module can be\n")
		b.WriteString("// imported in browser environments without bundler errors.\n\n")
	default: // RuntimeNode
		b.WriteString("import { spawn } from 'child_process';\n")
	}

	b.WriteString("import * as StellarSdk from '@stellar/stellar-sdk';\n\n")

	b.WriteString("export interface ErstConfig {\n")
	b.WriteString("  network: string;\n")
	b.WriteString("  rpcUrl: string;\n")
	b.WriteString("  erstPath?: string;\n")
	b.WriteString("  /** Custom fetch for HTTP-based simulation (browser / universal). */\n")
	b.WriteString("  fetch?: typeof fetch;\n")
	b.WriteString("}\n\n")

	b.WriteString("export interface SimulationResult {\n")
	b.WriteString("  status: 'success' | 'error';\n")
	b.WriteString("  result?: unknown;\n")
	b.WriteString("  error?: string;\n")
	b.WriteString("  events?: unknown[];\n")
	b.WriteString("  logs?: string[];\n")
	b.WriteString("  budgetUsage?: {\n")
	b.WriteString("    cpuInstructions: number;\n")
	b.WriteString("    memoryBytes: number;\n")
	b.WriteString("    cpuUsagePercent: number;\n")
	b.WriteString("    memoryUsagePercent: number;\n")
	b.WriteString("  };\n")
	b.WriteString("}\n\n")

	b.WriteString("export class ErstSimulator {\n")
	b.WriteString("  private readonly config: ErstConfig;\n\n")
	b.WriteString("  constructor(config: ErstConfig) { this.config = config; }\n\n")

	b.WriteString("  async simulate(tx: StellarSdk.Transaction): Promise<SimulationResult> {\n")

	switch target {
	case RuntimeBrowser:
		b.WriteString("    return this.runHTTP('simulate', tx.toEnvelope().toXDR('base64'));\n")
	case RuntimeUniversal:
		b.WriteString("    if (_isNode()) {\n")
		b.WriteString("      return this.runGlassbox(['simulate', '--network', this.config.network,\n")
		b.WriteString("        '--rpc-url', this.config.rpcUrl, '--json'], tx.toEnvelope().toXDR('base64'));\n")
		b.WriteString("    }\n")
		b.WriteString("    return this.runHTTP('simulate', tx.toEnvelope().toXDR('base64'));\n")
	default: // RuntimeNode
		b.WriteString("    return this.runGlassbox(['simulate', '--network', this.config.network,\n")
		b.WriteString("      '--rpc-url', this.config.rpcUrl, '--json'], tx.toEnvelope().toXDR('base64'));\n")
	}
	b.WriteString("  }\n\n")

	b.WriteString("  async debugTransaction(txHash: string): Promise<SimulationResult> {\n")
	switch target {
	case RuntimeBrowser:
		b.WriteString("    return this.runHTTP('debug', txHash);\n")
	case RuntimeUniversal:
		b.WriteString("    if (_isNode()) {\n")
		b.WriteString("      return this.runGlassbox(['debug', '--network', this.config.network,\n")
		b.WriteString("        '--rpc-url', this.config.rpcUrl, '--json', txHash]);\n")
		b.WriteString("    }\n")
		b.WriteString("    return this.runHTTP('debug', txHash);\n")
	default: // RuntimeNode
		b.WriteString("    return this.runGlassbox(['debug', '--network', this.config.network,\n")
		b.WriteString("      '--rpc-url', this.config.rpcUrl, '--json', txHash]);\n")
	}
	b.WriteString("  }\n\n")

	// HTTP shim (browser + universal).
	if target == RuntimeBrowser || target == RuntimeUniversal {
		b.WriteString("  /** HTTP-based simulation endpoint (browser / Electron renderer). */\n")
		b.WriteString("  private async runHTTP(action: string, payload: string): Promise<SimulationResult> {\n")
		b.WriteString("    const fetcher = this.config.fetch ?? globalThis.fetch.bind(globalThis);\n")
		b.WriteString("    const url = `${this.config.rpcUrl}/glassbox/${action}`;\n")
		b.WriteString("    const resp = await fetcher(url, {\n")
		b.WriteString("      method: 'POST',\n")
		b.WriteString("      headers: { 'Content-Type': 'application/json' },\n")
		b.WriteString("      body: JSON.stringify({ network: this.config.network, payload }),\n")
		b.WriteString("    });\n")
		b.WriteString("    if (!resp.ok) {\n")
		b.WriteString("      throw new Error(`Glassbox HTTP error ${resp.status}: ${await resp.text()}`);\n")
		b.WriteString("    }\n")
		b.WriteString("    return resp.json() as Promise<SimulationResult>;\n")
		b.WriteString("  }\n\n")
	}

	// Node child_process runner (node + universal).
	if target == RuntimeNode || target == RuntimeUniversal {
		if target == RuntimeUniversal {
			// Lazy-load spawn so the module is importable in browsers.
			b.WriteString("  private runGlassbox(args: string[], stdin?: string): Promise<SimulationResult> {\n")
			b.WriteString("    // eslint-disable-next-line @typescript-eslint/no-var-requires\n")
			b.WriteString("    const { spawn } = require('child_process') as typeof import('child_process');\n")
		} else {
			b.WriteString("  private runGlassbox(args: string[], stdin?: string): Promise<SimulationResult> {\n")
		}
		b.WriteString("    const bin = this.config.erstPath ?? 'glassbox';\n")
		b.WriteString("    return new Promise((resolve, reject) => {\n")
		b.WriteString("      const proc = spawn(bin, args);\n")
		b.WriteString("      let out = '', err = '';\n")
		b.WriteString("      if (stdin) { proc.stdin.write(stdin); proc.stdin.end(); }\n")
		b.WriteString("      proc.stdout.on('data', (d: Buffer) => { out += d.toString(); });\n")
		b.WriteString("      proc.stderr.on('data', (d: Buffer) => { err += d.toString(); });\n")
		b.WriteString("      proc.on('close', (code: number) => {\n")
		b.WriteString("        if (code !== 0) { reject(new Error(`glassbox exited ${code}: ${err}`)); return; }\n")
		b.WriteString("        try { resolve(JSON.parse(out) as SimulationResult); }\n")
		b.WriteString("        catch (e) { reject(new Error(`Failed to parse glassbox output: ${e}`)); }\n")
		b.WriteString("      });\n")
		b.WriteString("    });\n")
		b.WriteString("  }\n")
	}

	// Universal: environment detection helper.
	if target == RuntimeUniversal {
		b.WriteString("}\n\n")
		b.WriteString("/** @internal Returns true when running in a Node.js process. */\n")
		b.WriteString("function _isNode(): boolean {\n")
		b.WriteString("  return typeof process !== 'undefined' &&\n")
		b.WriteString("    typeof process.versions !== 'undefined' &&\n")
		b.WriteString("    typeof process.versions.node !== 'undefined';\n")
		b.WriteString("}\n")
	} else {
		b.WriteString("}\n")
	}

	return b.String()
}

// generateIndex generates the barrel export file.
func (g *Generator) generateIndex() string {
	var b strings.Builder
	b.WriteString(g.metadataHeader())
	b.WriteString("// Auto-generated index file\n")
	b.WriteString("// DO NOT EDIT - Generated by Glassbox generate-bindings\n\n")
	b.WriteString("export * from './types';\n")
	b.WriteString("export * from './metadata';\n")
	b.WriteString("export * from './client';\n")
	b.WriteString("export * from './Glassbox-integration';\n")
	return b.String()
}

// generatePackageJSON generates package.json for the bindings.
func (g *Generator) generatePackageJSON() string {
	target := g.runtimeTarget()

	// Browser target: add "browser" field to exclude Node-only modules.
	browserField := ""
	if target == RuntimeBrowser {
		browserField = `
  "browser": {
    "child_process": false,
    "fs": false,
    "path": false
  },`
	}

	// Universal target: add conditional exports.
	exportsField := ""
	if target == RuntimeUniversal {
		exportsField = `
  "exports": {
    ".": {
      "browser": "./index.browser.js",
      "default": "./index.js"
    }
  },`
	}

	body := fmt.Sprintf(`{
  "name": "%s",
  "version": "1.0.0",
  "description": "TypeScript bindings for Soroban smart contract",
  "main": "index.js",
  "types": "index.d.ts",%s%s
  "scripts": {
    "build": "tsc",
    "test": "jest"
  },
  "dependencies": {
    "@stellar/stellar-sdk": "^12.0.0"
  },
  "devDependencies": {
    "@types/node": "^20.0.0",
    "typescript": "^5.0.0"
  },
  "keywords": ["stellar", "soroban", "smart-contract", "blockchain"],
  "license": "Apache-2.0"
}
`, g.config.PackageName, browserField, exportsField)

	return g.metadataHeader() + body
}

// generateReadme generates README.md documentation.
func (g *Generator) generateReadme() string {
	var b strings.Builder
	b.WriteString(g.metadataHeader())
	className := toPascalCase(g.config.PackageName) + "Client"
	target := g.runtimeTarget()

	fmt.Fprintf(&b, "# %s\n\n", g.config.PackageName)
	b.WriteString("TypeScript bindings for a Soroban smart contract, generated by `glassbox generate-bindings`.\n\n")

	// Runtime target badge.
	switch target {
	case RuntimeBrowser:
		b.WriteString("> **Runtime target:** Browser (no Node.js globals)\n\n")
	case RuntimeUniversal:
		b.WriteString("> **Runtime target:** Universal (Node.js, browser, and Electron)\n\n")
	default:
		b.WriteString("> **Runtime target:** Node.js\n\n")
	}

	b.WriteString("## Installation\n\n```bash\nnpm install\n```\n\n")

	b.WriteString("## Usage\n\n```typescript\n")
	fmt.Fprintf(&b, "import { %s } from './%s';\n\n", className, g.config.PackageName)
	fmt.Fprintf(&b, "const client = new %s({\n", className)
	if g.config.ContractID != "" {
		fmt.Fprintf(&b, "  contractId: '%s',\n", g.config.ContractID)
	} else {
		b.WriteString("  contractId: 'YOUR_CONTRACT_ID',\n")
	}
	fmt.Fprintf(&b, "  network: '%s',\n", g.config.Network)
	b.WriteString("  enableSimulation: true,\n});\n```\n\n")

	b.WriteString("## Custom RPC Provider\n\n")
	b.WriteString("Inject a custom Soroban RPC provider to use a non-default endpoint,\n")
	b.WriteString("a custom fetch implementation, or a private network passphrase:\n\n")
	b.WriteString("```typescript\n")
	fmt.Fprintf(&b, "const client = new %s({\n", className)
	b.WriteString("  contractId: 'YOUR_CONTRACT_ID',\n")
	b.WriteString("  network: 'mainnet',\n")
	b.WriteString("  provider: {\n")
	b.WriteString("    rpcUrl: 'https://my-private-rpc.example.com',\n")
	b.WriteString("    networkPassphrase: 'My Private Network ; January 2026',\n")
	b.WriteString("    // Optionally supply a custom fetch (e.g. with auth headers):\n")
	b.WriteString("    fetch: (url, init) => fetch(url, {\n")
	b.WriteString("      ...init,\n")
	b.WriteString("      headers: { ...init?.headers, Authorization: 'Bearer TOKEN' },\n")
	b.WriteString("    }),\n")
	b.WriteString("  },\n});\n```\n\n")

	if target == RuntimeBrowser || target == RuntimeUniversal {
		b.WriteString("## Browser Usage\n\n")
		b.WriteString("This package targets browser environments. It uses the global `fetch` API\n")
		b.WriteString("and does **not** import `child_process`, `fs`, or other Node-only modules.\n\n")
		b.WriteString("```typescript\n")
		fmt.Fprintf(&b, "import { %s } from './%s';\n\n", className, g.config.PackageName)
		fmt.Fprintf(&b, "const client = new %s({\n", className)
		b.WriteString("  contractId: 'YOUR_CONTRACT_ID',\n")
		b.WriteString("  network: 'testnet',\n")
		b.WriteString("  // Optionally override the fetch used for RPC calls:\n")
		b.WriteString("  provider: { rpcUrl: 'https://soroban-testnet.stellar.org', fetch: window.fetch.bind(window) },\n")
		b.WriteString("});\n```\n\n")
	}

	b.WriteString("## Debug Metadata\n\n")
	b.WriteString("Pass `withDebugMetadata: true` in `CallOptions` to attach ABI metadata to the call result:\n\n")
	b.WriteString("```typescript\n")
	if len(g.spec.Functions) > 0 {
		fn := g.spec.Functions[0]
		fmt.Fprintf(&b, "const result = await client.%s(\n  sourceKeypair,\n", string(fn.Name))
		for _, inp := range fn.Inputs {
			fmt.Fprintf(&b, "  %s,\n", inp.Name)
		}
		b.WriteString("  { withDebugMetadata: true, simulate: true },\n);\n\n")
		b.WriteString("console.log('ABI metadata:', result.debugMetadata);\n")
		b.WriteString("// { name: '...',  inputs: [...], outputs: [...], source: { sourcePath, operationIndex } }\n")
	}
	b.WriteString("```\n\n")

	b.WriteString("## Contract Methods\n\n")
	for _, fn := range g.spec.Functions {
		fmt.Fprintf(&b, "### `%s`\n\n", string(fn.Name))
		if fn.Doc != "" {
			fmt.Fprintf(&b, "%s\n\n", fn.Doc)
		}
		if len(fn.Inputs) > 0 {
			b.WriteString("**Parameters:**\n\n")
			for _, inp := range fn.Inputs {
				fmt.Fprintf(&b, "- `%s`: `%s`\n", inp.Name, g.mapTypeDefToTS(inp.Type))
			}
			b.WriteString("\n")
		}
		if len(fn.Outputs) > 0 {
			fmt.Fprintf(&b, "**Returns:** `%s`\n\n", g.mapTypeDefToTS(fn.Outputs[0]))
		}
	}

	b.WriteString("## License\n\nApache-2.0\n")
	return b.String()
}

// toPascalCase converts a string to PascalCase.
func toPascalCase(s string) string {
	words := strings.FieldsFunc(s, func(r rune) bool {
		return r == '-' || r == '_' || r == ' '
	})
	for i, word := range words {
		if len(word) > 0 {
			words[i] = strings.ToUpper(word[:1]) + strings.ToLower(word[1:])
		}
	}
	return strings.Join(words, "")
}

// sortSpec sorts all spec slices by name for deterministic output.
func (g *Generator) sortSpec() {
	sort.Slice(g.spec.Functions, func(i, j int) bool {
		return string(g.spec.Functions[i].Name) < string(g.spec.Functions[j].Name)
	})
	sort.Slice(g.spec.Structs, func(i, j int) bool {
		return string(g.spec.Structs[i].Name) < string(g.spec.Structs[j].Name)
	})
	sort.Slice(g.spec.Enums, func(i, j int) bool {
		return string(g.spec.Enums[i].Name) < string(g.spec.Enums[j].Name)
	})
	sort.Slice(g.spec.Unions, func(i, j int) bool {
		return string(g.spec.Unions[i].Name) < string(g.spec.Unions[j].Name)
	})
	sort.Slice(g.spec.ErrorEnums, func(i, j int) bool {
		return string(g.spec.ErrorEnums[i].Name) < string(g.spec.ErrorEnums[j].Name)
	})
	sort.Slice(g.spec.Events, func(i, j int) bool {
		return string(g.spec.Events[i].Name) < string(g.spec.Events[j].Name)
	})
}

// normalizeLineEndings converts all line endings to LF (\n) for cross-platform consistency.
func normalizeLineEndings(content string) string {
	return strings.ReplaceAll(content, "\r\n", "\n")
}
