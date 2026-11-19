// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/dotandev/glassbox/internal/config"
	"github.com/spf13/cobra"
)

var (
	configShowJSONFlag    bool
	configShowExplainFlag bool
)

// configValueSource tracks where each config value came from.
type configValueSource struct {
	Value  string `json:"value"`
	Source string `json:"source"`
}

// configShowOutput represents the structured output for config show.
type configShowOutput struct {
	Values map[string]configValueSource `json:"values"`
	Source string                        `json:"source_file,omitempty"`
}

var configShowCmd = &cobra.Command{
	Use:     "config show",
	Short:   "Print the active Glassbox configuration and its source",
	GroupID: "utility",
	Long: `Print the active Glassbox configuration resolved from all sources.

Configuration is loaded in the following precedence order (highest wins):
  1. CLI flags
  2. Environment variables (GLASSBOX_*)
  3. Repository-local config  (.glassbox.toml  or  .Glassbox.toml)
  4. Home directory config    (~/.glassbox/config.toml  or  ~/.Glassbox.toml)
  5. Built-in defaults

Config file locations searched (first match wins):
  .glassbox.toml                  (current directory)
  .Glassbox.toml                  (current directory)
  ~/.glassbox/config.toml         (home directory, XDG-style)
  ~/.Glassbox.toml                (home directory, legacy)
  /etc/Glassbox/config.toml       (system-wide)

Use --json to output machine-readable JSON with source annotations.
Use --explain to see a per-field resolution report showing each setting's
source, precedence, effective value, whether a default was applied, and
whether the value was redacted.  Secrets are never shown in plain text.`,
	Example: `  # Human-readable config summary
  glassbox config show

  # Machine-readable JSON with source annotations
  glassbox config show --json

  # Per-field resolution report (why each value won)
  glassbox config show --explain

  # Per-field resolution report as JSON (for scripting)
  glassbox config show --explain --json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("failed to load configuration: %w", err)
		}

		// Get the active config file source
		configSource := config.ActiveConfigFile()

		// --explain: per-field resolution report
		if configShowExplainFlag {
			report := config.BuildResolveReport(cfg, configSource)
			if configShowJSONFlag {
				data, err := json.MarshalIndent(report, "", "  ")
				if err != nil {
					return fmt.Errorf("failed to marshal explain report: %w", err)
				}
				fmt.Fprintln(cmd.OutOrStdout(), string(data))
				return nil
			}
			printExplainReport(cmd, report)
			return nil
		}

		if configShowJSONFlag {
			// JSON output mode with source annotations
			output := buildConfigOutput(cfg, configSource)
			data, err := json.MarshalIndent(output, "", "  ")
			if err != nil {
				return fmt.Errorf("failed to marshal config: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(data))
			return nil
		}

		// Default: human-readable output
		fmt.Fprintln(cmd.OutOrStdout(), "Active configuration:")
		fmt.Fprintln(cmd.OutOrStdout(), cfg.String())

		if configSource != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "\nLoaded from: %s\n", configSource)
		} else {
			fmt.Fprintln(cmd.OutOrStdout(), "\nNo config file found — using defaults and environment variables.")
		}

		// Show source breakdown
		fmt.Fprintln(cmd.OutOrStdout(), "\nConfiguration sources:")
		showSourceBreakdown(cmd)

		return nil
	},
}

// printExplainReport renders a ResolveReport in human-readable form.
func printExplainReport(cmd *cobra.Command, report config.ResolveReport) {
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "Configuration resolution report")
	fmt.Fprintln(out, strings.Repeat("─", 60))

	if report.ActiveFile != "" {
		fmt.Fprintf(out, "Highest-priority config file: %s\n\n", report.ActiveFile)
	} else {
		fmt.Fprintln(out, "No config file found — values come from environment variables and built-in defaults.")
	}

	// Column header
	fmt.Fprintf(out, "  %-28s %-12s %-40s\n", "FIELD", "SOURCE", "EFFECTIVE VALUE")
	fmt.Fprintln(out, "  "+strings.Repeat("─", 84))

	for _, rv := range report.Fields {
		source := string(rv.Source)
		if rv.DefaultApplied {
			source += " (default)"
		}
		displayVal := rv.EffectiveValue
		if rv.Redacted {
			displayVal = "[redacted]"
		}

		// Truncate long values so the table stays readable.
		const maxValLen = 38
		if len(displayVal) > maxValLen {
			displayVal = displayVal[:maxValLen-1] + "…"
		}

		fmt.Fprintf(out, "  %-28s %-20s %s\n", rv.Field, source, displayVal)

		// Show the env var or file path as an extra annotation.
		if rv.Source == config.SourceEnvironment && rv.EnvVar != "" {
			fmt.Fprintf(out, "    ↳ env: %s\n", rv.EnvVar)
		} else if rv.Source == config.SourceFile && rv.FilePath != "" {
			fmt.Fprintf(out, "    ↳ file: %s\n", rv.FilePath)
		}
	}

	if len(report.ConflictNotes) > 0 {
		fmt.Fprintln(out, "\nConflict resolution notes:")
		for _, note := range report.ConflictNotes {
			fmt.Fprintf(out, "  • %s\n", note)
		}
	}
}

var configCmd = &cobra.Command{
	Use:     "config",
	Short:   "Manage Glassbox configuration",
	GroupID: "utility",
}

func init() {
	configShowCmd.Flags().BoolVar(&configShowJSONFlag, "json", false, "Output configuration as JSON with source annotations")
	configShowCmd.Flags().BoolVar(&configShowExplainFlag, "explain", false, "Show a per-field resolution report: source, precedence, effective value, and default/redaction status")
	configCmd.AddCommand(configShowCmd)
	rootCmd.AddCommand(configCmd)
}

// buildConfigOutput creates a structured config output with source annotations.
func buildConfigOutput(cfg *config.Config, configSource string) configShowOutput {
	output := configShowOutput{
		Values: make(map[string]configValueSource),
	}

	if configSource != "" {
		output.Source = configSource
	}

	// Determine source for each config value
	if cfg.RpcUrl != "" {
		output.Values["rpc_url"] = configValueSource{
			Value:  cfg.RpcUrl,
			Source: resolveConfigSource("GLASSBOX_RPC_URL", configSource, "default"),
		}
	}
	if cfg.Network != "" {
		output.Values["network"] = configValueSource{
			Value:  string(cfg.Network),
			Source: resolveConfigSource("GLASSBOX_NETWORK", configSource, "default"),
		}
	}
	if cfg.NetworkPassphrase != "" {
		output.Values["network_passphrase"] = configValueSource{
			Value:  "[redacted]",
			Source: resolveConfigSource("GLASSBOX_NETWORK_PASSPHRASE", configSource, "default"),
		}
	}
	if cfg.RPCToken != "" {
		output.Values["rpc_token"] = configValueSource{
			Value:  "[redacted]",
			Source: resolveConfigSource("GLASSBOX_RPC_TOKEN", configSource, "default"),
		}
	}
	if cfg.LogLevel != "" {
		output.Values["log_level"] = configValueSource{
			Value:  cfg.LogLevel,
			Source: resolveConfigSource("GLASSBOX_LOG_LEVEL", configSource, "default"),
		}
	}
	if cfg.CachePath != "" {
		output.Values["cache_path"] = configValueSource{
			Value:  cfg.CachePath,
			Source: resolveConfigSource("GLASSBOX_CACHE_PATH", configSource, "default"),
		}
	}
	if cfg.RequestTimeout > 0 {
		output.Values["request_timeout"] = configValueSource{
			Value:  fmt.Sprintf("%d", cfg.RequestTimeout),
			Source: resolveConfigSource("GLASSBOX_REQUEST_TIMEOUT", configSource, "default"),
		}
	}
	if cfg.Telemetry {
		output.Values["telemetry"] = configValueSource{
			Value:  "true",
			Source: resolveConfigSource("GLASSBOX_TELEMETRY", configSource, "default"),
		}
	}
	if cfg.TelemetryAnonymized {
		output.Values["telemetry_anonymized"] = configValueSource{
			Value:  "true",
			Source: resolveConfigSource("GLASSBOX_TELEMETRY_ANONYMIZED", configSource, "default"),
		}
	}
	if cfg.FailoverStrategy != "" {
		output.Values["failover_strategy"] = configValueSource{
			Value:  cfg.FailoverStrategy,
			Source: resolveConfigSource("GLASSBOX_FAILOVER_STRATEGY", configSource, "default"),
		}
	}
	if len(cfg.SorobanRpcUrls) > 0 {
		output.Values["soroban_rpc_urls"] = configValueSource{
			Value:  fmt.Sprintf("%v", stripURLCredentials(cfg.SorobanRpcUrls)),
			Source: resolveConfigSource("GLASSBOX_SOROBAN_RPC_URLS", configSource, "default"),
		}
	}

	return output
}

// resolveConfigSource determines the source of a config value.
func resolveConfigSource(envVar, configFile, defaultVal string) string {
	if os.Getenv(envVar) != "" {
		return "environment"
	}
	if configFile != "" {
		return "file"
	}
	return defaultVal
}

// showSourceBreakdown displays a breakdown of configuration sources.
func showSourceBreakdown(cmd *cobra.Command) {
	sources := []struct {
		name    string
		envVar  string
		path    string
		checked bool
	}{
		{name: "CLI flags", envVar: "", path: "", checked: false},
		{name: "Environment variables", envVar: "GLASSBOX_*", path: "", checked: os.Getenv("GLASSBOX_RPC_URL") != ""},
		{name: "Local config", envVar: "", path: ".glassbox.toml", checked: fileExists(".glassbox.toml")},
		{name: "Local config", envVar: "", path: ".Glassbox.toml", checked: fileExists(".Glassbox.toml")},
		{name: "User config", envVar: "", path: "~/.glassbox/config.toml", checked: fileExists(userConfigPath())},
		{name: "Legacy user config", envVar: "", path: "~/.Glassbox.toml", checked: fileExists(legacyUserConfigPath())},
		{name: "System config", envVar: "", path: "/etc/Glassbox/config.toml", checked: fileExists("/etc/Glassbox/config.toml")},
	}

	seen := make(map[string]bool)
	for _, s := range sources {
		if s.checked && !seen[s.name] {
			fmt.Fprintf(cmd.OutOrStdout(), "  ✓ %s (active)\n", s.name)
			seen[s.name] = true
		}
	}
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	// Expand home directory
	if path == "~/.glassbox/config.toml" || path == "~/.Glassbox.toml" {
		home, err := os.UserHomeDir()
		if err != nil {
			return false
		}
		path = filepath.Join(home, ".glassbox", "config.toml")
	}
	_, err := os.Stat(path)
	return err == nil
}

func userConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".glassbox", "config.toml")
}

func legacyUserConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".Glassbox.toml")
}

// defaultConfigFilePath returns the canonical ~/.glassbox/config.toml path.
func defaultConfigFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".glassbox", "config.toml"), nil
}

// stripURLCredentials removes userinfo (username:password) from a list of URLs
// so they can be safely displayed in config output without leaking credentials.
// Non-parseable entries are replaced with "[invalid url]".
func stripURLCredentials(urls []string) []string {
	cleaned := make([]string, 0, len(urls))
	for _, raw := range urls {
		u, err := url.Parse(raw)
		if err != nil {
			cleaned = append(cleaned, "[invalid url]")
			continue
		}
		// Remove any embedded credentials (user:pass@host).
		u.User = nil
		cleaned = append(cleaned, u.String())
	}
	return cleaned
}
