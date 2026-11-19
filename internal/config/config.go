// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"

	"github.com/dotandev/glassbox/internal/endpoints"
	"github.com/dotandev/glassbox/internal/errors"
	"github.com/dotandev/glassbox/internal/pathutil"
)

func joinPath(parts ...string) string {
	return pathutil.Join(parts...)
}

// -- Interfaces --

type Parser interface {
	Parse(*Config) error
}

type DefaultAssigner interface {
	Apply(*Config)
}

type Validator interface {
	Validate(*Config) error
}

// -- Schema versioning --

// CurrentSchemaVersion is the latest config schema version this binary understands.
// Bump this constant when a new migration step is added to migrate.go.
const CurrentSchemaVersion = 1

// ErrFutureSchemaVersion is returned when a config file declares a schema
// version higher than CurrentSchemaVersion.
var ErrFutureSchemaVersion = errors.WrapConfigError(
	"config file was written by a newer version of Glassbox",
	nil,
)

// DetectSchemaVersion reads only the schema_version key from a raw TOML
// config string without fully parsing it. It returns 0 when the key is
// absent (pre-versioning files), and an error when the key is present but
// cannot be parsed as an integer.
func DetectSchemaVersion(content string) (int, error) {
	for _, line := range splitLines(content) {
		line = trimSpace(line)
		if line == "" || hasPrefix(line, "#") {
			continue
		}
		key, val, ok := splitKeyVal(line)
		if !ok {
			continue
		}
		if key != "schema_version" {
			continue
		}
		n, err := parseInt(val)
		if err != nil {
			return 0, errors.WrapConfigError(
				"schema_version must be a non-negative integer",
				err,
			)
		}
		return n, nil
	}
	return 0, nil // key absent → pre-versioning
}

// -- Types --

type DebugConfig struct {
	EnableSnapshots bool `json:"enable_snapshots"`
}

// ExternalSourceRepo maps a local source tree prefix to a remote repository URL.
type ExternalSourceRepo struct {
	Prefix    string `json:"prefix"`
	RemoteURL string `json:"remote_url"`
	Branch    string `json:"branch,omitempty"`
}

type Network string

const (
	NetworkPublic     Network = "public"
	NetworkTestnet    Network = "testnet"
	NetworkFuturenet  Network = "futurenet"
	NetworkStandalone Network = "standalone"
)

var validNetworks = map[string]bool{
	string(NetworkPublic):     true,
	string(NetworkTestnet):    true,
	string(NetworkFuturenet):  true,
	string(NetworkStandalone): true,
}

// Config represents the general configuration for Glassbox
type Config struct {
	// SchemaVersion documents which config schema this file was written for.
	// The current supported version is CurrentSchemaVersion (1).
	// Missing or zero means "pre-versioning" — treated as version 1 on load.
	// Values above CurrentSchemaVersion are rejected with an actionable error.
	SchemaVersion int `json:"schema_version,omitempty"`

	RpcUrl  string   `json:"rpc_url,omitempty"`
	RpcUrls []string `json:"rpc_urls,omitempty"`
	// SorobanRpcUrls holds multiple Soroban RPC endpoints for adaptive failover.
	// When set, these are used for all Soroban JSON-RPC calls (getLedgerEntries,
	// simulateTransaction, getHealth) instead of RpcUrl/RpcUrls.
	SorobanRpcUrls []string `json:"soroban_rpc_urls,omitempty"`
	// FailoverStrategy controls how the adaptive selector picks among endpoints.
	// Valid values: "weighted" (default), "sticky", "round_robin".
	FailoverStrategy  string  `json:"failover_strategy,omitempty"`
	Network           Network `json:"network,omitempty"`
	NetworkPassphrase string  `json:"network_passphrase,omitempty"`
	SimulatorPath     string  `json:"simulator_path,omitempty"`
	LogLevel          string  `json:"log_level,omitempty"`
	CachePath         string  `json:"cache_path,omitempty"`
	RPCToken          string  `json:"rpc_token,omitempty"`
	// Telemetry enables optional command usage telemetry.
	Telemetry bool `json:"telemetry,omitempty"`
	// TelemetryAnonymized sends telemetry data in anonymized mode when enabled.
	TelemetryAnonymized bool `json:"telemetry_anonymized,omitempty"`
	// TelemetryAnonymizedConfigured tracks whether anonymized mode was explicitly set.
	TelemetryAnonymizedConfigured bool `json:"-"`
	// MaxCacheSize is the maximum size of the source map cache in bytes.
	MaxCacheSize int64 `json:"max_cache_size,omitempty"`
	// CrashReporting enables opt-in anonymous crash reporting.
	CrashReporting bool `json:"crash_reporting,omitempty"`
	// CrashEndpoint is a custom HTTPS URL that receives JSON crash reports.
	CrashEndpoint string `json:"crash_endpoint,omitempty"`
	// CrashSentryDSN is a Sentry Data Source Name for crash reporting.
	CrashSentryDSN string `json:"crash_sentry_dsn,omitempty"`
	// RequestTimeout is the HTTP request timeout in seconds for all RPC calls.
	RequestTimeout int `json:"request_timeout,omitempty"`
	// TelemetryEnabled controls OpenTelemetry usage (opt-in default).
	TelemetryEnabled bool `json:"telemetry_enabled,omitempty"`
	// TelemetryEndpoint is an optional OTLP exporter URL.
	TelemetryEndpoint string `json:"telemetry_endpoint,omitempty"`
	// TelemetrySampleRate is the fraction of high-frequency trace events to emit [0.0, 1.0].
	// 1.0 emits all events (default); 0.1 emits ~10%; 0.0 disables trace event telemetry.
	TelemetrySampleRate float64 `json:"telemetry_sample_rate,omitempty"`
	// TelemetrySampleRateSet tracks whether the sample rate was explicitly set,
	// so an explicit 0.0 is not replaced by the default.
	TelemetrySampleRateSet bool `json:"-"`
	// MaxTraceDepth is the maximum depth of the call tree before it is truncated.
	MaxTraceDepth int `json:"max_trace_depth,omitempty"`
	// ExternalSourceRepos maps local path prefixes to remote GitHub repositories for source links.
	ExternalSourceRepos []ExternalSourceRepo `json:"external_source_repos,omitempty"`
	// FailureThreshold is the number of failures before the circuit breaker opens.
	FailureThreshold int `json:"failure_threshold,omitempty"`
	// RetryTimeout is the duration in seconds to wait before retrying a failed endpoint.
	RetryTimeout int `json:"retry_timeout,omitempty"`
	// BuildManifestPath is the path to a glassbox-build-manifest.json file used
	// for cross-machine source mapping from reproducible builds (Issue #45).
	// Can also be set via the GLASSBOX_BUILD_MANIFEST environment variable.
	// The --build-manifest CLI flag takes precedence over this value.
	BuildManifestPath string `json:"build_manifest_path,omitempty"`

	// Plugin contains plugin execution policy settings.
	Plugin PluginConfig `json:"plugin,omitempty"`
}

type PluginConfig struct {
	AllowUntrusted bool `json:"allow_untrusted,omitempty"`
}

// -- Constants & Defaults --

const defaultRequestTimeout = 15
const defaultFailureThreshold = 5
const defaultRetryTimeout = 60

var validLogLevels = map[string]bool{
	"trace": true,
	"debug": true,
	"info":  true,
	"warn":  true,
	"error": true,
}

var defaultConfig = &Config{
	RpcUrl:              endpoints.SorobanTestnet,
	Network:             NetworkTestnet,
	SimulatorPath:       "",
	LogLevel:            "info",
	CachePath:           joinPath(os.ExpandEnv("$HOME"), ".Glassbox", "cache"),
	RequestTimeout:      defaultRequestTimeout,
	TelemetryEnabled:    false,
	TelemetrySampleRate: 1.0,
	TelemetryAnonymized: true,
	MaxCacheSize:        0,
	MaxTraceDepth:       50,
	FailureThreshold:    defaultFailureThreshold,
	RetryTimeout:        defaultRetryTimeout,
}

// -- Core Functions --

func Load() (*Config, error) {
	cfg := &Config{}
	parsers := []Parser{fileParser{}, envParser{}}
	for _, parser := range parsers {
		if err := parser.Parse(cfg); err != nil {
			return nil, err
		}
	}

	configDefaultsAssigner{}.Apply(cfg)

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func DefaultConfig() *Config {
	return &Config{
		RpcUrl:              defaultConfig.RpcUrl,
		Network:             defaultConfig.Network,
		SimulatorPath:       defaultConfig.SimulatorPath,
		LogLevel:            defaultConfig.LogLevel,
		CachePath:           defaultConfig.CachePath,
		Telemetry:           defaultConfig.Telemetry,
		TelemetryAnonymized: defaultConfig.TelemetryAnonymized,
		TelemetryEnabled:    defaultConfig.TelemetryEnabled,
		TelemetryEndpoint:   defaultConfig.TelemetryEndpoint,
		TelemetrySampleRate: defaultConfig.TelemetrySampleRate,
		RequestTimeout:      defaultConfig.RequestTimeout,
		MaxCacheSize:        defaultConfig.MaxCacheSize,
		MaxTraceDepth:       defaultConfig.MaxTraceDepth,
		FailureThreshold:    defaultConfig.FailureThreshold,
		RetryTimeout:        defaultConfig.RetryTimeout,
	}
}

// -- Config Methods --

func (c *Config) MergeDefaults() {
	configDefaultsAssigner{}.Apply(c)
}

func NewConfig(rpcUrl string, network Network) *Config {
	return &Config{
		RpcUrl:              rpcUrl,
		Network:             network,
		SimulatorPath:       defaultConfig.SimulatorPath,
		LogLevel:            defaultConfig.LogLevel,
		CachePath:           defaultConfig.CachePath,
		Telemetry:           defaultConfig.Telemetry,
		TelemetryAnonymized: defaultConfig.TelemetryAnonymized,
		RequestTimeout:      defaultConfig.RequestTimeout,
		MaxCacheSize:        defaultConfig.MaxCacheSize,
		MaxTraceDepth:       defaultConfig.MaxTraceDepth,
		FailureThreshold:    defaultConfig.FailureThreshold,
		RetryTimeout:        defaultConfig.RetryTimeout,
	}
}

func (c *Config) WithSimulatorPath(path string) *Config {
	c.SimulatorPath = path
	return c
}

func (c *Config) WithLogLevel(level string) *Config {
	c.LogLevel = level
	return c
}

func (c *Config) WithCachePath(path string) *Config {
	c.CachePath = path
	return c
}

func (c *Config) WithRequestTimeout(timeout int) *Config {
	c.RequestTimeout = timeout
	return c
}

func (c *Config) Validate() error {
	validators := []Validator{
		RPCValidator{},
		NetworkValidator{},
		SimulatorValidator{},
		LogLevelValidator{},
		TimeoutValidator{},
		MaxTraceDepthValidator{},
		CrashReportingValidator{},
	}
	for _, v := range validators {
		if err := v.Validate(c); err != nil {
			return err
		}
	}
	return nil
}

func (c *Config) NetworkURL() string {
	switch c.Network {
	case NetworkPublic:
		return endpoints.SorobanMainnet
	case NetworkTestnet:
		return endpoints.SorobanTestnet
	case NetworkFuturenet:
		return endpoints.SorobanFuturenet
	case NetworkStandalone:
		return "http://localhost:8000"
	default:
		return c.RpcUrl
	}
}

func (c *Config) String() string {
	data, _ := json.MarshalIndent(c, "", "  ")
	return string(data)
}

// -- Load/Save Config --

func GetGeneralConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return joinPath(home, ".Glassbox", "config.json"), nil
}

func LoadConfig() (*Config, error) {
	configPath, err := GetGeneralConfigPath()
	if err != nil {
		return nil, err
	}

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return DefaultConfig(), nil
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, errors.WrapConfigError("failed to read config file", err)
	}

	config := DefaultConfig()
	if err := json.Unmarshal(data, config); err != nil {
		return nil, errors.WrapConfigError("failed to parse config file", err)
	}

	// Reject future schema versions loaded via the JSON path too.
	if config.SchemaVersion > CurrentSchemaVersion {
		return nil, errors.WrapConfigError(
			"config file declares schema_version "+strconv.Itoa(config.SchemaVersion)+
				" but this Glassbox binary only supports up to version "+
				strconv.Itoa(CurrentSchemaVersion)+
				"; upgrade Glassbox or run 'glassbox config migrate'",
			nil,
		)
	}

	return config, nil
}

func SaveConfig(config *Config) error {
	configPath, err := GetGeneralConfigPath()
	if err != nil {
		return err
	}

	configDir := filepath.Dir(configPath)
	if err := os.MkdirAll(configDir, 0700); err != nil {
		return errors.WrapConfigError("failed to create config directory", err)
	}

	// Always stamp the current schema version before writing.
	config.SchemaVersion = CurrentSchemaVersion

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return errors.WrapConfigError("failed to marshal config", err)
	}

	return os.WriteFile(configPath, data, 0600)
}

// -- Parsers --

type envParser struct{}

func (envParser) Parse(cfg *Config) error {
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
		n, _ := strconv.ParseInt(v, 10, 64)
		if n > 0 {
			cfg.MaxCacheSize = n
		}
	}
	if v := os.Getenv("GLASSBOX_REQUEST_TIMEOUT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.RequestTimeout = n
		}
	}
	if v := os.Getenv("GLASSBOX_MAX_TRACE_DEPTH"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.MaxTraceDepth = n
		}
	}
	if v := os.Getenv("GLASSBOX_TELEMETRY"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.Telemetry = b
		}
	}
	if v := os.Getenv("GLASSBOX_TELEMETRY_ANONYMIZED"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.TelemetryAnonymized = b
			cfg.TelemetryAnonymizedConfigured = true
		}
	}
	if v := os.Getenv("GLASSBOX_CRASH_REPORTING"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.CrashReporting = b
		}
	}
	if v := os.Getenv("GLASSBOX_CRASH_ENDPOINT"); v != "" {
		cfg.CrashEndpoint = v
	}
	if v := os.Getenv("GLASSBOX_SENTRY_DSN"); v != "" {
		cfg.CrashSentryDSN = v
	}
	if v := os.Getenv("GLASSBOX_RPC_URLS"); v != "" {
		cfg.RpcUrls = splitAndTrim(v)
	}
	if v := os.Getenv("GLASSBOX_SOROBAN_RPC_URLS"); v != "" {
		cfg.SorobanRpcUrls = splitAndTrim(v)
	}
	if v := os.Getenv("GLASSBOX_FAILOVER_STRATEGY"); v != "" {
		cfg.FailoverStrategy = v
	}
	if v := os.Getenv("GLASSBOX_FAILURE_THRESHOLD"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.FailureThreshold = n
		}
	}
	if v := os.Getenv("GLASSBOX_RETRY_TIMEOUT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.RetryTimeout = n
		}
	}
	if v := os.Getenv("GLASSBOX_TELEMETRY_ENABLED"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.TelemetryEnabled = b
		}
	}
	if v := os.Getenv("GLASSBOX_TELEMETRY_ENDPOINT"); v != "" {
		cfg.TelemetryEndpoint = v
	}
	if v := os.Getenv("GLASSBOX_TELEMETRY_SAMPLE_RATE"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.TelemetrySampleRate = f
			cfg.TelemetrySampleRateSet = true
		}
	}
	if v := os.Getenv("GLASSBOX_BUILD_MANIFEST"); v != "" {
		cfg.BuildManifestPath = v
	}
	if v := os.Getenv("GLASSBOX_PLUGIN_ALLOW_UNTRUSTED"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.Plugin.AllowUntrusted = b
		}
	}
	return nil
}

type fileParser struct{}

func (fileParser) Parse(cfg *Config) error {
	return cfg.loadFromFile()
}

// -- Validators --

type RPCValidator struct{}

type configDefaultsAssigner struct{}

func (configDefaultsAssigner) Apply(cfg *Config) {
	if cfg.RpcUrl == "" {
		cfg.RpcUrl = defaultConfig.RpcUrl
	}
	if cfg.Network == "" {
		cfg.Network = defaultConfig.Network
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = defaultConfig.LogLevel
	}
	if cfg.CachePath == "" {
		cfg.CachePath = defaultConfig.CachePath
	}
	if cfg.RequestTimeout == 0 {
		cfg.RequestTimeout = defaultRequestTimeout
	}
	if cfg.MaxTraceDepth == 0 {
		cfg.MaxTraceDepth = 50
	}
	if cfg.FailureThreshold == 0 {
		cfg.FailureThreshold = defaultFailureThreshold
	}
	if cfg.RetryTimeout == 0 {
		cfg.RetryTimeout = defaultRetryTimeout
	}
	if cfg.Telemetry && !cfg.TelemetryAnonymizedConfigured {
		cfg.TelemetryAnonymized = defaultConfig.TelemetryAnonymized
	}
	if cfg.TelemetrySampleRate == 0 && !cfg.TelemetrySampleRateSet {
		cfg.TelemetrySampleRate = defaultConfig.TelemetrySampleRate
	}
}
