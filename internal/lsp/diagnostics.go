// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package lsp

// diagnostics.go translates a simulation outcome into LSP diagnostics so a
// developer sees simulation failures as inline editor annotations instead of
// having to map terminal output back to their source.
//
// Translation is deliberately separated from transport (see client.go):
// `DiagnosticsFromSimulation` is a pure function that needs no socket, and
// DiagnosticsPublisher only adds connection handling and the timeout.
//
// Severity mapping:
//
//   - a WASM trap                     -> Error    (the run failed here)
//   - instruction cost over threshold -> Warning  (correct, but expensive)
//   - deprecated host function call   -> Hint     (informational, not broken)

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/dotandev/glassbox/internal/security"
	"github.com/dotandev/glassbox/internal/simulator"
	"github.com/dotandev/glassbox/internal/sourcemap"
)

// DefaultResourceCostThreshold is the CPU instruction count above which a
// simulation is reported as a warning. Overridable via Options.
const DefaultResourceCostThreshold uint64 = 1_000_000

// LSP diagnostic severities.
const (
	SeverityError       = 1
	SeverityWarning     = 2
	SeverityInformation = 3
	SeverityHint        = 4
)

// diagnosticSource labels every diagnostic so a developer can tell at a glance
// that it came from Glassbox rather than from rustc or clippy.
const diagnosticSource = "glassbox"

// Diagnostic codes emitted by Glassbox.
const (
	CodeTrap                  = "glassbox.trap"
	CodeResourceCost          = "glassbox.resource-cost"
	CodeDeprecatedHostFunction = "glassbox.deprecated-host-function"
)

// Options configures diagnostic translation.
type Options struct {
	// ResourceCostThreshold is the CPU instruction count above which a run is
	// reported as a warning. Zero means DefaultResourceCostThreshold.
	ResourceCostThreshold uint64
	// RootDir is the workspace root, used to make source paths relative before
	// converting them to file:// URIs.
	RootDir string
	// ArtifactURI is the fallback URI for findings with no source location.
	ArtifactURI string
	// Timeout bounds a whole publish operation. Zero means DefaultTimeout.
	Timeout time.Duration
}

// threshold returns the configured resource cost threshold.
func (o Options) threshold() uint64 {
	if o.ResourceCostThreshold == 0 {
		return DefaultResourceCostThreshold
	}
	return o.ResourceCostThreshold
}

// timeout returns the configured publish timeout.
func (o Options) timeout() time.Duration {
	if o.Timeout == 0 {
		return DefaultTimeout
	}
	return o.Timeout
}

// ErrNoClient is returned when a publisher is used before a client is
// connected. Callers that treat publication as best-effort should ignore it.
var ErrNoClient = fmt.Errorf("lsp: diagnostics publisher has no connected client")

// DiagnosticsPublisher sends diagnostics to a connected language client.
type DiagnosticsPublisher struct {
	client  *Client
	options Options
}

// NewDiagnosticsPublisher creates a publisher bound to a client.
//
// A nil client is allowed: every Publish call then becomes a no-op returning
// ErrNoClient, which is what makes `glassbox debug --lsp-socket` safe when the
// endpoint is not listening.
func NewDiagnosticsPublisher(client *Client, opts Options) *DiagnosticsPublisher {
	return &DiagnosticsPublisher{client: client, options: opts}
}

// Publish translates the simulation outcome and pushes the diagnostics to the
// client. It returns ErrNoClient (not a crash) when no client is connected,
// and the whole operation is bounded by Options.Timeout so a wedged editor
// cannot stall the debug command.
func (p *DiagnosticsPublisher) Publish(
	ctx context.Context,
	result *simulator.SimulationResponse,
	mappings []sourcemap.FallbackResult,
	findings []security.Finding,
) error {
	if p == nil || p.client == nil || !p.client.Connected() {
		return ErrNoClient
	}

	byURI := DiagnosticsFromSimulation(result, mappings, findings, p.options)
	if len(byURI) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, p.options.timeout())
	defer cancel()

	return p.client.PublishDiagnostics(ctx, byURI)
}

// DiagnosticsMap groups diagnostics by the document URI they belong to.
type DiagnosticsMap map[string][]ProtocolDiagnostic

// ProtocolDiagnostic mirrors the LSP Diagnostic shape.
type ProtocolDiagnostic struct {
	Range    ProtocolRange `json:"range"`
	Severity int           `json:"severity"`
	Code     string        `json:"code,omitempty"`
	Source   string        `json:"source"`
	Message  string        `json:"message"`
}

// ProtocolRange is a zero-based LSP range; lines and characters are 0-based.
type ProtocolRange struct {
	Start ProtocolPosition `json:"start"`
	End   ProtocolPosition `json:"end"`
}

// ProtocolPosition is a zero-based line/character pair.
type ProtocolPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Diagnostic is a translated diagnostic paired with the URI it belongs to, so
// the translation helpers can stay linear and the caller does the grouping.
type Diagnostic struct {
	URI        string
	Diagnostic ProtocolDiagnostic
}

// DiagnosticsFromSimulation converts a simulation outcome into diagnostics
// grouped by document URI. It performs no I/O and is safe to call in tests.
func DiagnosticsFromSimulation(
	result *simulator.SimulationResponse,
	mappings []sourcemap.FallbackResult,
	findings []security.Finding,
	opts Options,
) DiagnosticsMap {
	out := DiagnosticsMap{}
	for _, diag := range TranslateSimulation(result, mappings, findings, opts) {
		out[diag.URI] = append(out[diag.URI], diag.Diagnostic)
	}
	return out
}

// TranslateSimulation returns the flat, ordered list of diagnostics for a
// simulation outcome: traps first, then resource cost, then host function hints.
func TranslateSimulation(
	result *simulator.SimulationResponse,
	mappings []sourcemap.FallbackResult,
	findings []security.Finding,
	opts Options,
) []Diagnostic {
	var out []Diagnostic
	out = append(out, trapDiagnostics(result, mappings, opts)...)
	out = append(out, resourceDiagnostics(result, mappings, opts.threshold(), opts)...)
	out = append(out, hostFunctionDiagnostics(result, mappings, findings, opts)...)
	return out
}

// trapDiagnostics reports the trap location of a failed simulation as an error.
func trapDiagnostics(
	result *simulator.SimulationResponse,
	mappings []sourcemap.FallbackResult,
	opts Options,
) []Diagnostic {
	loc := trapLocation(result)
	if loc == nil {
		return nil
	}

	uri, line := resolveLocation(loc.File, int(loc.Line), mappings, opts)
	if uri == "" {
		return nil
	}

	return []Diagnostic{{
		URI: uri,
		Diagnostic: ProtocolDiagnostic{
			Range:    lineRange(line),
			Severity: SeverityError,
			Code:     CodeTrap,
			Source:   diagnosticSource,
			Message:  trapMessage(result),
		},
	}}
}

// trapLocation is the source location a failed simulation should be reported
// against, preferring the simulator's own mapping over ours.
func trapLocation(result *simulator.SimulationResponse) *simulator.SourceLocation {
	if result == nil {
		return nil
	}
	if result.SourceLocation != nil && result.SourceLocation.File != "" {
		return result.SourceLocation
	}
	if result.StackTrace != nil {
		for _, frame := range result.StackTrace.Frames {
			if frame != nil && frame.SourceLocation != nil && frame.SourceLocation.File != "" {
				return frame.SourceLocation
			}
		}
	}
	return nil
}

// trapMessage describes the failure in the developer's terms.
func trapMessage(result *simulator.SimulationResponse) string {
	if result == nil {
		return "simulation trapped"
	}

	parts := make([]string, 0, 3)
	if result.Error != "" {
		parts = append(parts, result.Error)
	}
	if result.StackTrace != nil && result.StackTrace.RawMessage != "" {
		parts = append(parts, result.StackTrace.RawMessage)
	}
	if result.ErrorCode != "" {
		parts = append(parts, "code "+result.ErrorCode)
	}
	if len(parts) == 0 {
		return "simulation trapped"
	}
	return strings.Join(parts, " — ")
}

// resourceDiagnostics reports a run whose CPU cost exceeds the threshold,
// annotating the diagnostic with the observed cost. Budget usage is a whole-run
// aggregate, so it is reported against the trap frame when there is one and
// otherwise against the first mapped location.
func resourceDiagnostics(
	result *simulator.SimulationResponse,
	mappings []sourcemap.FallbackResult,
	threshold uint64,
	opts Options,
) []Diagnostic {
	if result == nil || result.BudgetUsage == nil {
		return nil
	}

	usage := result.BudgetUsage
	if usage.CPUInstructions <= threshold {
		return nil
	}

	file, line := "", 0
	if loc := trapLocation(result); loc != nil {
		file, line = loc.File, int(loc.Line)
	} else if len(mappings) > 0 {
		file, line = mappings[0].File, mappings[0].Line
	}

	uri, resolved := resolveLocation(file, line, mappings, opts)
	if uri == "" {
		return nil
	}

	return []Diagnostic{{
		URI: uri,
		Diagnostic: ProtocolDiagnostic{
			Range:    lineRange(resolved),
			Severity: SeverityWarning,
			Code:     CodeResourceCost,
			Source:   diagnosticSource,
			Message: fmt.Sprintf(
				"simulation used %d CPU instructions, above the %d instruction warning threshold",
				usage.CPUInstructions,
				threshold,
			),
		},
	}}
}

// hostFunctionDiagnostics reports deprecated host function calls as hints. The
// location is the first mapped source location, because a diagnostic event
// carries no address of its own.
func hostFunctionDiagnostics(
	result *simulator.SimulationResponse,
	mappings []sourcemap.FallbackResult,
	findings []security.Finding,
	opts Options,
) []Diagnostic {
	if result == nil || len(result.DiagnosticEvents) == 0 {
		return nil
	}

	// Index findings by title so a deprecated host function reported by the
	// security detector enriches the inline hint.
	byTitle := make(map[string]security.Finding, len(findings))
	for _, finding := range findings {
		byTitle[finding.Title] = finding
	}

	var out []Diagnostic
	for _, event := range result.DiagnosticEvents {
		haystack := strings.Join(event.Topics, " ") + " " + event.Data
		name, deprecated := security.DeprecatedHostFunctionIn(haystack)
		if !deprecated {
			continue
		}

		uri, line := resolveLocation("", 0, mappings, opts)
		if uri == "" {
			continue
		}

		message := fmt.Sprintf("contract calls deprecated host function %s", name)
		if description, ok := security.HostFunctionDescriptions[name]; ok {
			message += ": " + description
		}
		if finding, ok := byTitle[name]; ok && finding.Description != "" {
			message += ". " + finding.Description
		}

		out = append(out, Diagnostic{
			URI: uri,
			Diagnostic: ProtocolDiagnostic{
				Range:    lineRange(line),
				Severity: SeverityHint,
				Code:     CodeDeprecatedHostFunction,
				Source:   diagnosticSource,
				Message:  message,
			},
		})
	}

	return out
}

// resolveLocation turns a 1-based source path and line into a document URI and
// a 0-based line, falling back to the first mapped location when the direct
// path is empty.
func resolveLocation(file string, line int, mappings []sourcemap.FallbackResult, opts Options) (string, int) {
	if file == "" {
		for _, mapping := range mappings {
			if mapping.File != "" {
				file, line = mapping.File, mapping.Line
				break
			}
		}
	}
	if file == "" {
		return "", 0
	}

	uri := documentURI(file, opts.RootDir)
	if uri == "" {
		return "", 0
	}
	return uri, zeroBasedLine(line)
}

// documentURI converts a source path to a file:// URI, made relative to the
// workspace root when possible so it matches what the editor opened.
func documentURI(path, root string) string {
	if path == "" {
		return ""
	}

	cleaned := filepath.Clean(path)
	if root != "" {
		if rel, err := filepath.Rel(filepath.Clean(root), cleaned); err == nil &&
			!strings.HasPrefix(rel, "..") {
			cleaned = rel
		}
	}

	slashed := filepath.ToSlash(cleaned)
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed
	}
	segments := strings.Split(slashed, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return "file://" + strings.Join(segments, "/")
}

// zeroBasedLine converts a 1-based source line to the 0-based line LSP uses,
// clamping unknown lines to the first line of the file.
func zeroBasedLine(line int) int {
	if line <= 1 {
		return 0
	}
	return line - 1
}

// lineRange builds a zero-based, single-line range for a source line.
func lineRange(line int) ProtocolRange {
	return ProtocolRange{
		Start: ProtocolPosition{Line: line, Character: 0},
		End:   ProtocolPosition{Line: line, Character: 0},
	}
}
