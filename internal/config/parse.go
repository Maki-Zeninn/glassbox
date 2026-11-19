// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dotandev/glassbox/internal/errors"
)

// splitLines, trimSpace, hasPrefix, splitKeyVal, parseInt are thin wrappers
// used by DetectSchemaVersion in config.go to avoid importing "strings" there.
func splitLines(s string) []string    { return strings.Split(s, "\n") }
func trimSpace(s string) string       { return strings.TrimSpace(s) }
func hasPrefix(s, p string) bool      { return strings.HasPrefix(s, p) }
func parseInt(s string) (int, error)  { return strconv.Atoi(strings.TrimSpace(s)) }

func splitKeyVal(line string) (key, val string, ok bool) {
	parts := strings.SplitN(line, "=", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return strings.TrimSpace(parts[0]), strings.Trim(strings.TrimSpace(parts[1]), "\"'"), true
}

// activeConfigFile records which config file was loaded during the last call
// to loadFromFile. It is reset on each call.
var activeConfigFile string

// ActiveConfigFile returns the path of the config file that was loaded during
// the most recent Load() call, or an empty string if no file was found.
func ActiveConfigFile() string {
	return activeConfigFile
}

func loadFromEnv(cfg *Config) error { //nolint:unused // Reserved for future config loading from environment
	if v := os.Getenv("GLASSBOX_RPC_URL"); v != "" {
		cfg.RpcUrl = v
	}
	if v := os.Getenv("GLASSBOX_NETWORK"); v != "" {
		cfg.Network = Network(v)
	}
	if v := os.Getenv("GLASSBOX_SIMULATOR_PATH"); v != "" {
		cfg.SimulatorPath = v
	}
	if v := os.Getenv("GLASSBOX_LOG_LEVEL"); v != "" {
		cfg.LogLevel = v
	}
	if v := os.Getenv("GLASSBOX_CACHE_PATH"); v != "" {
		cfg.CachePath = v
	}
	if v := os.Getenv("GLASSBOX_RPC_TOKEN"); v != "" {
		cfg.RPCToken = v
	}
	if v := os.Getenv("GLASSBOX_MAX_CACHE_SIZE"); v != "" {
		n, err := parseSize(v)
		if err != nil {
			return errors.WrapValidationError("GLASSBOX_MAX_CACHE_SIZE must be a valid size (e.g., 500MB)")
		}
		cfg.MaxCacheSize = n
	}
	if v := os.Getenv("GLASSBOX_CRASH_ENDPOINT"); v != "" {
		cfg.CrashEndpoint = v
	}
	if v := os.Getenv("GLASSBOX_SENTRY_DSN"); v != "" {
		cfg.CrashSentryDSN = v
	}

	if v := os.Getenv("GLASSBOX_REQUEST_TIMEOUT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return errors.WrapValidationError("GLASSBOX_REQUEST_TIMEOUT must be an integer")
		}
		cfg.RequestTimeout = n
	}

	if v := os.Getenv("GLASSBOX_EXTERNAL_SOURCE_MAP"); v != "" {
		var repos []ExternalSourceRepo
		if err := json.Unmarshal([]byte(v), &repos); err == nil {
			cfg.ExternalSourceRepos = repos
		}
	}

	if v := os.Getenv("GLASSBOX_MAX_TRACE_DEPTH"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return errors.WrapValidationError("GLASSBOX_MAX_TRACE_DEPTH must be an integer")
		}
		cfg.MaxTraceDepth = n
	}

	switch strings.ToLower(os.Getenv("GLASSBOX_CRASH_REPORTING")) {
	case "":
	case "1", "true", "yes":
		cfg.CrashReporting = true
	case "0", "false", "no":
		cfg.CrashReporting = false
	default:
		return errors.WrapValidationError("GLASSBOX_CRASH_REPORTING must be a boolean")
	}

	if urlsEnv := os.Getenv("GLASSBOX_RPC_URLS"); urlsEnv != "" {
		cfg.RpcUrls = splitAndTrim(urlsEnv)
	} else if urlsEnv := os.Getenv("STELLAR_RPC_URLS"); urlsEnv != "" {
		cfg.RpcUrls = splitAndTrim(urlsEnv)
	}

	return nil
}

func splitAndTrim(s string) []string {
	parts := strings.Split(s, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// loadFromFile loads configuration from all discovered config files in
// ascending priority order. Values from higher-priority files override those
// from lower-priority ones; each file that is found is merged into c.
//
// Resolution order (lowest → highest priority):
//  1. /etc/Glassbox/config.toml         — system-wide defaults
//  2. ~/.Glassbox.toml                  — home directory, legacy name
//  3. ~/.glassbox/config.toml           — home directory, XDG-style
//  4. .Glassbox.toml                    — project directory, legacy name
//  5. .glassbox.toml                    — project directory, XDG-style
//
// After file loading, environment variables (applied by envParser) override
// all file-sourced values, and CLI flags override everything.
//
// ActiveConfigFile returns the path of the highest-priority file that was
// successfully loaded, or an empty string when no file was found.
func (c *Config) loadFromFile() error {
	activeConfigFile = "" // reset on each load

	home := os.ExpandEnv("$HOME")

	// Sources listed in ascending priority order; later entries override earlier.
	sources := []string{
		"/etc/Glassbox/config.toml",
		filepath.Join(home, ".Glassbox.toml"),
		filepath.Join(home, ".glassbox", "config.toml"),
		".Glassbox.toml",
		".glassbox.toml",
	}

	for _, path := range sources {
		// Resolve to an absolute path so ActiveConfigFile always returns a
		// canonical, non-relative location regardless of the caller's working
		// directory.
		abs, absErr := filepath.Abs(path)
		if absErr != nil {
			abs = path
		}

		// Skip files that do not exist; propagate all other errors (e.g. parse
		// validation failures) so misconfigured files are surfaced immediately.
		if _, statErr := os.Stat(abs); statErr != nil {
			continue
		}
		if err := c.loadTOML(abs); err != nil {
			return err
		}
		activeConfigFile = abs
	}

	return nil
}

func (c *Config) loadTOML(path string) error {
	if _, err := os.Stat(path); err != nil {
		return err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	content := string(data)

	// Reject files that declare a schema version newer than this binary supports.
	// Pre-versioning files (schema_version absent) are treated as version 1.
	v, vErr := DetectSchemaVersion(content)
	if vErr != nil {
		return vErr
	}
	if v > CurrentSchemaVersion {
		return errors.WrapConfigError(
			"config file declares schema_version "+strconv.Itoa(v)+
				" but this Glassbox binary only supports up to version "+
				strconv.Itoa(CurrentSchemaVersion)+
				"; upgrade Glassbox or run 'glassbox config migrate --path "+path+"' with a newer binary",
			nil,
		)
	}

	return c.parseTOML(content)
}

func (c *Config) parseTOML(content string) error {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)

		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		rawVal := strings.TrimSpace(parts[1])

		if key == "rpc_urls" && strings.HasPrefix(rawVal, "[") && strings.HasSuffix(rawVal, "]") {
			rawVal = strings.Trim(rawVal, "[]")
			elems := strings.Split(rawVal, ",")
			var urls []string
			for _, p := range elems {
				urls = append(urls, strings.Trim(strings.TrimSpace(p), "\"'"))
			}
			c.RpcUrls = urls
			continue
		}

		value := strings.Trim(rawVal, "\"'")

		switch key {
		case "schema_version":
			n, err := strconv.Atoi(value)
			if err != nil {
				return errors.WrapValidationError("schema_version must be a non-negative integer")
			}
			c.SchemaVersion = n
		case "rpc_url":
			c.RpcUrl = value
		case "rpc_urls":
			c.RpcUrls = splitAndTrim(value)
		case "network":
			c.Network = Network(value)
		case "network_passphrase":
			c.NetworkPassphrase = value
		case "simulator_path":
			c.SimulatorPath = value
		case "log_level":
			c.LogLevel = value
		case "cache_path":
			c.CachePath = value
		case "rpc_token":
			c.RPCToken = value
		case "crash_reporting":
			switch strings.ToLower(value) {
			case "true", "1", "yes":
				c.CrashReporting = true
			case "false", "0", "no":
				c.CrashReporting = false
			default:
				return errors.WrapValidationError("crash_reporting must be a boolean")
			}
		case "crash_endpoint":
			c.CrashEndpoint = value
		case "crash_sentry_dsn":
			c.CrashSentryDSN = value
		case "telemetry":
			switch strings.ToLower(value) {
			case "true", "1", "yes":
				c.Telemetry = true
			case "false", "0", "no":
				c.Telemetry = false
			default:
				return errors.WrapValidationError("telemetry must be a boolean")
			}
		case "telemetry_anonymized":
			switch strings.ToLower(value) {
			case "true", "1", "yes":
				c.TelemetryAnonymized = true
			case "false", "0", "no":
				c.TelemetryAnonymized = false
			default:
				return errors.WrapValidationError("telemetry_anonymized must be a boolean")
			}
			c.TelemetryAnonymizedConfigured = true
		case "telemetry_sample_rate":
			f, err := strconv.ParseFloat(value, 64)
			if err != nil {
				return errors.WrapValidationError("telemetry_sample_rate must be a number between 0.0 and 1.0")
			}
			c.TelemetrySampleRate = f
			c.TelemetrySampleRateSet = true
		case "request_timeout":
			n, err := strconv.Atoi(value)
			if err != nil {
				return errors.WrapValidationError("request_timeout must be an integer")
			}
			c.RequestTimeout = n
		case "max_trace_depth":
			n, err := strconv.Atoi(value)
			if err != nil {
				return errors.WrapValidationError("max_trace_depth must be an integer")
			}
			c.MaxTraceDepth = n
		case "max_cache_size":
			n, err := parseSize(value)
			if err != nil {
				return errors.WrapValidationError("max_cache_size must be a valid size (e.g., 500MB)")
			}
			c.MaxCacheSize = n
		case "failure_threshold":
			n, err := strconv.Atoi(value)
			if err != nil {
				return errors.WrapValidationError("failure_threshold must be an integer")
			}
			c.FailureThreshold = n
		case "external_source_map":
			var repos []ExternalSourceRepo
			if err := json.Unmarshal([]byte(value), &repos); err != nil {
				return errors.WrapValidationError("external_source_map must be a JSON array of {prefix, remote_url, branch}")
			}
			c.ExternalSourceRepos = repos
		case "retry_timeout":
			n, err := strconv.Atoi(value)
			if err != nil {
				return errors.WrapValidationError("retry_timeout must be an integer")
			}
			c.RetryTimeout = n
		}
	}

	return nil
}

func getEnv(key, defaultValue string) string {
	if !strings.HasPrefix(key, "GLASSBOX_") {
		return defaultValue
	}
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func parseSize(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}

	var multiplier int64 = 1
	lowerValue := strings.ToLower(value)

	if strings.HasSuffix(lowerValue, "kb") {
		multiplier = 1024
		value = strings.TrimSuffix(value, "kb")
	} else if strings.HasSuffix(lowerValue, "mb") {
		multiplier = 1024 * 1024
		value = strings.TrimSuffix(value, "mb")
	} else if strings.HasSuffix(lowerValue, "gb") {
		multiplier = 1024 * 1024 * 1024
		value = strings.TrimSuffix(value, "gb")
	} else if strings.HasSuffix(lowerValue, "k") {
		multiplier = 1000
		value = strings.TrimSuffix(value, "k")
	} else if strings.HasSuffix(lowerValue, "m") {
		multiplier = 1000 * 1000
		value = strings.TrimSuffix(value, "m")
	} else if strings.HasSuffix(lowerValue, "g") {
		multiplier = 1000 * 1000 * 1000
		value = strings.TrimSuffix(value, "g")
	}

	n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return 0, err
	}

	return n * multiplier, nil
}
