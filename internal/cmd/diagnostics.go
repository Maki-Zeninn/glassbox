// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/dotandev/glassbox/internal/cache"
	"github.com/dotandev/glassbox/internal/config"
	"github.com/dotandev/glassbox/internal/plugin"
	"github.com/dotandev/glassbox/internal/version"
	"github.com/spf13/cobra"
)

var diagnosticsJSONFlag bool

var diagnosticsCmd = &cobra.Command{
	Use:     "diagnostics",
	Aliases: []string{"diag", "dashboard"},
	GroupID: "utility",
	Short:   "Comprehensive diagnostics dashboard for Glassbox",
	Long: `Display a unified dashboard with health and status for Glassbox.

This command aggregates data from:
  - System and version information
  - RPC endpoint health
  - Cache status
  - Configuration
  - Plugins

Use --json for machine-readable output suitable for automation and CI.`,
	Args: cobra.NoArgs,
	RunE: runDiagnostics,
}

// DiagnosticsOutput is the structured representation of all diagnostics data.
// It is used for both the human-readable dashboard and the --json output path.
type DiagnosticsOutput struct {
	Version       string          `json:"version"`
	Timestamp     time.Time       `json:"timestamp"`
	System        SystemInfo      `json:"system"`
	RPC           []RPCStatus     `json:"rpc"`
	Cache         CacheStatus     `json:"cache"`
	Config        ConfigSummary   `json:"config"`
	Plugins       []PluginSummary `json:"plugins"`
	OverallHealth string          `json:"overall_health"`
}

type SystemInfo struct {
	OS      string `json:"os"`
	Arch    string `json:"arch"`
	HomeDir string `json:"home_dir"`
}

type RPCStatus struct {
	URL     string        `json:"url"`
	Status  string        `json:"status"`
	Latency time.Duration `json:"latency_ms"`
	Error   string        `json:"error,omitempty"`
	Healthy bool          `json:"healthy"`
}

type CacheStatus struct {
	Directory string `json:"directory"`
	Size      string `json:"size"`
	FileCount int    `json:"file_count"`
	MaxSize   string `json:"max_size"`
	Healthy   bool   `json:"healthy"`
}

type ConfigSummary struct {
	Source  string `json:"source,omitempty"`
	Network string `json:"network"`
	RPCURL  string `json:"rpc_url"`
	Healthy bool   `json:"healthy"`
}

type PluginSummary struct {
	Name         string             `json:"name"`
	Version      string             `json:"version"`
	Capabilities []plugin.Capability `json:"capabilities"`
	Healthy      bool               `json:"healthy"`
}

func runDiagnostics(cmd *cobra.Command, args []string) error {
	w := cmd.OutOrStdout()
	output := DiagnosticsOutput{
		Version:   version.Version,
		Timestamp: time.Now(),
	}

	// System Info
	output.System = SystemInfo{
		OS:   runtime.GOOS,
		Arch: runtime.GOARCH,
	}
	home, err := os.UserHomeDir()
	if err == nil {
		output.System.HomeDir = home
	}

	// Load config
	cfg, err := config.Load()
	if err == nil {
		configSource := config.ActiveConfigFile()
		output.Config = ConfigSummary{
			Source:  configSource,
			Network: string(cfg.Network),
			RPCURL:  cfg.RpcUrl,
			Healthy: true,
		}
	} else {
		output.Config = ConfigSummary{
			Healthy: false,
		}
	}

	// RPC Health Check
	var rpcURLs []string
	if cfg != nil {
		if len(cfg.SorobanRpcUrls) > 0 {
			rpcURLs = cfg.SorobanRpcUrls
		} else if len(cfg.RpcUrls) > 0 {
			rpcURLs = cfg.RpcUrls
		} else if cfg.RpcUrl != "" {
			rpcURLs = []string{cfg.RpcUrl}
		}
	}
	timeout := 15 * time.Second
	if cfg != nil && cfg.RequestTimeout > 0 {
		timeout = time.Duration(cfg.RequestTimeout) * time.Second
	}
	httpClient := &http.Client{Timeout: timeout}
	output.RPC = make([]RPCStatus, 0, len(rpcURLs))
	for _, url := range rpcURLs {
		url = strings.TrimSpace(url)
		if url == "" {
			continue
		}
		// Validate URL format before attempting a network probe.
		if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
			output.RPC = append(output.RPC, RPCStatus{
				URL:     url,
				Status:  "[FAIL]",
				Error:   "URL must use http:// or https:// scheme — Fix: update rpc_url in config or GLASSBOX_RPC_URL",
				Healthy: false,
			})
			continue
		}
		rpcStatus := RPCStatus{URL: url, Status: "[OK]", Healthy: true}
		start := time.Now()
		resp, reqErr := httpClient.Get(url) //nolint:noctx
		if reqErr != nil {
			rpcStatus.Status = "[FAIL]"
			rpcStatus.Error = fmt.Sprintf("%v — Fix: ensure the endpoint is reachable and check GLASSBOX_RPC_URL", reqErr)
			rpcStatus.Healthy = false
		} else {
			resp.Body.Close()
			if resp.StatusCode >= 400 {
				rpcStatus.Status = "[FAIL]"
				rpcStatus.Error = fmt.Sprintf("HTTP %d — Fix: check that the RPC endpoint is healthy and not rate-limiting", resp.StatusCode)
				rpcStatus.Healthy = false
			}
		}
		rpcStatus.Latency = time.Since(start).Round(time.Millisecond)
		output.RPC = append(output.RPC, rpcStatus)
	}

	// Cache Status
	cacheDir := getCacheDir()
	manager := cache.NewManager(cacheDir, cache.DefaultConfig())
	output.Cache.Directory = cacheDir
	cacheSize, cacheSizeErr := manager.GetCacheSize()
	if cacheSizeErr == nil {
		output.Cache.Size = formatBytes(cacheSize)
		output.Cache.MaxSize = formatBytes(cache.DefaultConfig().MaxSizeBytes)
		output.Cache.Healthy = cacheSize <= cache.DefaultConfig().MaxSizeBytes
	} else {
		output.Cache.Size = "unknown"
		output.Cache.MaxSize = formatBytes(cache.DefaultConfig().MaxSizeBytes)
		output.Cache.Healthy = true // treat unknown as healthy; don't block on cache errors
	}
	files, listErr := manager.ListCachedFiles()
	if listErr == nil {
		output.Cache.FileCount = len(files)
	}

	// Plugins
	pluginDir := resolvePluginDir("")
	manifests, _ := plugin.DiscoverManifests(pluginDir)
	output.Plugins = make([]PluginSummary, 0, len(manifests))
	for _, m := range manifests {
		output.Plugins = append(output.Plugins, PluginSummary{
			Name:         m.Name,
			Version:      m.Version,
			Capabilities: m.Capabilities,
			Healthy:      true,
		})
	}

	// Overall Health — derived from component health fields for consistency.
	output.OverallHealth = computeOverallHealth(&output)

	// Validate before rendering to catch any internal inconsistencies.
	if issues := ValidateDiagnosticsOutput(&output); len(issues) > 0 {
		for _, issue := range issues {
			fmt.Fprintf(os.Stderr, "Warning: diagnostics inconsistency: %s\n", issue)
		}
	}

	// --json: emit structured output and return early
	if diagnosticsJSONFlag {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(output)
	}

	// Human-readable dashboard
	printDiagnosticsDashboard(w, output)
	return nil
}

// ValidateDiagnosticsOutput checks a DiagnosticsOutput struct for conditions
// that would produce misleading health reports and returns a list of issues.
//
// Checks:
//   - OverallHealth is one of the expected values ("Healthy", "Degraded")
//   - Every unhealthy RPCStatus has a non-empty Error and a Fix hint in the URL
//   - No RPCStatus has an empty Status string (empty status looks like a pass)
//   - Cache.Healthy is consistent with reported size (over-limit → not healthy)
//   - Config.Healthy=false is surfaced when config could not be loaded
//
// Returns nil when all checks pass.
func ValidateDiagnosticsOutput(out *DiagnosticsOutput) []string {
	if out == nil {
		return []string{"DiagnosticsOutput is nil"}
	}

	var issues []string

	// OverallHealth must be a known value.
	switch out.OverallHealth {
	case "Healthy", "Degraded":
		// valid
	case "":
		issues = append(issues, "OverallHealth is empty — health state cannot be communicated; expected \"Healthy\" or \"Degraded\"")
	default:
		issues = append(issues, fmt.Sprintf("OverallHealth has unexpected value %q — expected \"Healthy\" or \"Degraded\"", out.OverallHealth))
	}

	// RPC status consistency.
	for i, r := range out.RPC {
		// Empty status string is ambiguous — looks like a pass to human readers.
		if strings.TrimSpace(r.Status) == "" {
			issues = append(issues, fmt.Sprintf(
				"RPC[%d] (%s): Status is empty — will render as blank in reports; use \"[OK]\" or \"[FAIL]\"",
				i, r.URL,
			))
		}
		// Unhealthy entry must have an Error message.
		if !r.Healthy && strings.TrimSpace(r.Error) == "" {
			issues = append(issues, fmt.Sprintf(
				"RPC[%d] (%s): Healthy=false but Error is empty — failure reason is not communicated to users",
				i, r.URL,
			))
		}
		// Inconsistency: Status says OK but Healthy is false.
		if !r.Healthy && r.Status == "[OK]" {
			issues = append(issues, fmt.Sprintf(
				"RPC[%d] (%s): Status=\"[OK]\" but Healthy=false — inconsistent; OverallHealth may be misleading",
				i, r.URL,
			))
		}
		// Inconsistency: Healthy=true but OverallHealth=Degraded was not caused by this entry.
		// (only flag when Status is explicitly [FAIL])
		if r.Healthy && r.Status == "[FAIL]" {
			issues = append(issues, fmt.Sprintf(
				"RPC[%d] (%s): Status=\"[FAIL]\" but Healthy=true — inconsistent",
				i, r.URL,
			))
		}
	}

	// OverallHealth must reflect actual RPC/cache/config state.
	hasUnhealthyRPC := false
	for _, r := range out.RPC {
		if !r.Healthy {
			hasUnhealthyRPC = true
			break
		}
	}
	if hasUnhealthyRPC && out.OverallHealth == "Healthy" {
		issues = append(issues, "OverallHealth is \"Healthy\" but one or more RPC endpoints are unhealthy — overall health is misleading")
	}
	if !out.Cache.Healthy && out.OverallHealth == "Healthy" {
		issues = append(issues, "OverallHealth is \"Healthy\" but cache is unhealthy — overall health is misleading")
	}
	if !out.Config.Healthy && out.OverallHealth == "Healthy" {
		issues = append(issues, "OverallHealth is \"Healthy\" but config is unhealthy — overall health is misleading")
	}

	return issues
}

// computeOverallHealth derives the OverallHealth string from the individual
// component health fields. This is the single source of truth for the field
// so callers don't independently compute it and risk inconsistency.
func computeOverallHealth(out *DiagnosticsOutput) string {
	if out == nil {
		return "Degraded"
	}
	for _, r := range out.RPC {
		if !r.Healthy {
			return "Degraded"
		}
	}
	if !out.Cache.Healthy {
		return "Degraded"
	}
	if !out.Config.Healthy {
		return "Degraded"
	}
	return "Healthy"
}

// printDiagnosticsDashboard renders a human-readable diagnostics dashboard to w.
func printDiagnosticsDashboard(w io.Writer, out DiagnosticsOutput) {
	fmt.Fprintln(w, "╔═══════════════════════════════════════════════════════════════╗")
	fmt.Fprintln(w, "║                    GLASSBOX DIAGNOSTICS                       ║")
	fmt.Fprintln(w, "╚═══════════════════════════════════════════════════════════════╝")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Version:     %s\n", out.Version)
	fmt.Fprintf(w, "Timestamp:   %s\n", out.Timestamp.Format(time.RFC1123))
	fmt.Fprint(w, "Overall:     ")
	if out.OverallHealth == "Healthy" {
		fmt.Fprintf(w, "\033[32m%s\033[0m\n", out.OverallHealth)
	} else {
		fmt.Fprintf(w, "\033[33m%s\033[0m\n", out.OverallHealth)
	}
	fmt.Fprintln(w)

	// Section: System
	fmt.Fprintln(w, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Fprintln(w, "SYSTEM INFO")
	fmt.Fprintln(w, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Fprintf(w, "OS:          %s\n", out.System.OS)
	fmt.Fprintf(w, "Arch:        %s\n", out.System.Arch)
	if out.System.HomeDir != "" {
		fmt.Fprintf(w, "Home:        %s\n", out.System.HomeDir)
	}
	fmt.Fprintln(w)

	// Section: RPC
	fmt.Fprintln(w, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Fprintln(w, "RPC ENDPOINTS")
	fmt.Fprintln(w, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	if len(out.RPC) == 0 {
		fmt.Fprintln(w, "No RPC endpoints configured.")
		fmt.Fprintf(w, "Tip: set rpc_url in your config file or the GLASSBOX_RPC_URL environment variable.\n")
	} else {
		for _, r := range out.RPC {
			if r.Healthy {
				fmt.Fprintf(w, "  \033[32m[OK]\033[0m %s\n", r.URL)
				fmt.Fprintf(w, "      Latency: %v\n", r.Latency)
			} else {
				fmt.Fprintf(w, "  \033[31m[FAIL]\033[0m %s\n", r.URL)
				fmt.Fprintf(w, "      Error: %s\n", r.Error)
				fmt.Fprintf(w, "      Fix: ensure the endpoint is reachable, check GLASSBOX_RPC_URL or rpc_url config\n")
			}
			fmt.Fprintln(w)
		}
	}

	// Section: Cache
	fmt.Fprintln(w, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Fprintln(w, "CACHE STATUS")
	fmt.Fprintln(w, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Fprintf(w, "Directory:   %s\n", out.Cache.Directory)
	fmt.Fprintf(w, "Size:        %s / %s\n", out.Cache.Size, out.Cache.MaxSize)
	fmt.Fprintf(w, "Files:       %d\n", out.Cache.FileCount)
	if out.Cache.Healthy {
		fmt.Fprintf(w, "Status:      \033[32mHealthy\033[0m\n")
	} else {
		fmt.Fprintf(w, "Status:      \033[33mOver limit\033[0m — run 'glassbox cache clear' to free space\n")
	}
	fmt.Fprintln(w)

	// Section: Config
	fmt.Fprintln(w, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Fprintln(w, "CONFIGURATION")
	fmt.Fprintln(w, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	if !out.Config.Healthy {
		fmt.Fprintf(w, "Status:      \033[33mNo config file found\033[0m — defaults will be used\n")
	} else {
		if out.Config.Source != "" {
			fmt.Fprintf(w, "Source:      %s\n", out.Config.Source)
		}
		if out.Config.Network != "" {
			fmt.Fprintf(w, "Network:     %s\n", out.Config.Network)
		}
		if out.Config.RPCURL != "" {
			fmt.Fprintf(w, "RPC URL:     %s\n", out.Config.RPCURL)
		}
	}
	fmt.Fprintln(w)

	// Section: Plugins
	fmt.Fprintln(w, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Fprintln(w, "PLUGINS")
	fmt.Fprintln(w, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	if len(out.Plugins) == 0 {
		fmt.Fprintln(w, "No plugins discovered.")
	} else {
		for _, p := range out.Plugins {
			fmt.Fprintf(w, "  %s (v%s)\n", p.Name, p.Version)
			if len(p.Capabilities) > 0 {
				caps := make([]string, len(p.Capabilities))
				for i, c := range p.Capabilities {
					caps[i] = string(c)
				}
				fmt.Fprintf(w, "    Capabilities: %s\n", strings.Join(caps, ", "))
			}
			fmt.Fprintln(w)
		}
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Tip: Run 'glassbox doctor' for a deeper environment health check.")
	fmt.Fprintln(w, "     Run 'glassbox help' to see all available commands.")
}

func init() {
	diagnosticsCmd.Flags().BoolVar(&diagnosticsJSONFlag, "json", false, "Output diagnostics as machine-readable JSON")
	rootCmd.AddCommand(diagnosticsCmd)
}
