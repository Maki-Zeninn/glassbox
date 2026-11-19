// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package rpc

import (
	"fmt"
	"os"
	"time"

	"github.com/dotandev/glassbox/internal/errors"
	"github.com/stellar/go-stellar-sdk/clients/horizonclient"
)

type ClientOption func(*clientBuilder) error

type clientBuilder struct {
	network          Network
	token            string
	horizonURL       string
	sorobanURL       string
	altURLs          []string
	sorobanAltURLs   []string
	cacheEnabled     bool
	methodTelemetry  MethodTelemetry
	config           *NetworkConfig
	httpClient       HTTPClient
	requestTimeout   time.Duration
	middlewares      []Middleware
	loggingEnabled   bool
	failureThreshold int
	retryTimeout     int
	failoverPolicy   *FailoverPolicy
	// poolConfig, when non-nil, overrides the default ProviderPool settings.
	poolConfig *PoolConfig
	// replayPinnedURL, when set, locks the ProviderPool to a single endpoint
	// for deterministic replay (disables silent failover).
	replayPinnedURL string
	// responsePayloadLimit caps response body reads; 0 means DefaultResponsePayloadLimit.
	responsePayloadLimit int64
	// aggregateFetchLimit caps total bytes fetched across all requests; 0 means DefaultAggregateFetchLimit.
	aggregateFetchLimit int64
}

const defaultHTTPTimeout = 15 * time.Second

func newBuilder() *clientBuilder {
	return &clientBuilder{
		network:            Mainnet,
		cacheEnabled:        true,
		methodTelemetry:     defaultMethodTelemetry(),
		requestTimeout:      defaultHTTPTimeout,
		failureThreshold:   5,
		retryTimeout:        60,
		responsePayloadLimit: 0, // Use default
		aggregateFetchLimit:  0, // Use default
	}
}

func WithNetwork(net Network) ClientOption {
	return func(b *clientBuilder) error {
		if net == "" {
			net = Mainnet
		}
		b.network = net
		return nil
	}
}

func WithToken(token string) ClientOption {
	return func(b *clientBuilder) error {
		b.token = token
		return nil
	}
}

func WithHorizonURL(url string) ClientOption {
	return func(b *clientBuilder) error {
		if url != "" {
			if err := isValidURL(url); err != nil {
				return errors.WrapValidationError(fmt.Sprintf("invalid HorizonURL: %v", err))
			}
		}
		b.horizonURL = url
		b.altURLs = []string{url}
		return nil
	}
}

func WithAltURLs(urls []string) ClientOption {
	return func(b *clientBuilder) error {
		for _, url := range urls {
			if err := isValidURL(url); err != nil {
				return errors.WrapValidationError(fmt.Sprintf("invalid URL in altURLs: %v", err))
			}
		}
		if len(urls) > 0 {
			b.altURLs = urls
			b.horizonURL = urls[0]
		}
		return nil
	}
}

func WithSorobanURL(url string) ClientOption {
	return func(b *clientBuilder) error {
		if url != "" {
			if err := isValidURL(url); err != nil {
				return errors.WrapValidationError(fmt.Sprintf("invalid SorobanURL: %v", err))
			}
		}
		b.sorobanURL = url
		return nil
	}
}

func WithNetworkConfig(cfg NetworkConfig) ClientOption {
	return func(b *clientBuilder) error {
		if err := ValidateNetworkConfig(cfg); err != nil {
			return errors.WrapValidationError(fmt.Sprintf("invalid network config: %v", err))
		}
		b.config = &cfg
		b.network = Network(cfg.Name)
		b.horizonURL = cfg.HorizonURL
		b.sorobanURL = cfg.SorobanRPCURL
		return nil
	}
}

func WithCacheEnabled(enabled bool) ClientOption {
	return func(b *clientBuilder) error {
		b.cacheEnabled = enabled
		return nil
	}
}

// WithRequestTimeout sets a custom HTTP request timeout for all RPC calls.
// Use this to override the default 15-second timeout, for example on slow connections.
// A value of 0 disables the timeout (not recommended for production use).
func WithRequestTimeout(d time.Duration) ClientOption {
	return func(b *clientBuilder) error {
		b.requestTimeout = d
		return nil
	}
}

func WithHTTPClient(client HTTPClient) ClientOption {
	return func(b *clientBuilder) error {
		b.httpClient = client
		return nil
	}
}

// WithMethodTelemetry injects an optional telemetry hook for SDK method timings.
// If nil is provided, a no-op implementation is used.
func WithMethodTelemetry(telemetry MethodTelemetry) ClientOption {
	return func(b *clientBuilder) error {
		if telemetry == nil {
			telemetry = defaultMethodTelemetry()
		}
		b.methodTelemetry = telemetry
		return nil
	}
}

func WithMiddleware(middlewares ...Middleware) ClientOption {
	return func(b *clientBuilder) error {
		b.middlewares = append(b.middlewares, middlewares...)
		return nil
	}
}

// WithLoggingEnabled enables or disables the built-in LoggingMiddleware.
// When enabled, every outbound HTTP request is logged at INFO level with its
// method, URL, response status, and round-trip latency. The logging middleware
// is always placed outermost so it observes the full logical request duration.
func WithLoggingEnabled(enabled bool) ClientOption {
	return func(b *clientBuilder) error {
		b.loggingEnabled = enabled
		return nil
	}
}

// WithCircuitBreakerThreshold sets the number of failures before the circuit breaker opens.
func WithCircuitBreakerThreshold(threshold int) ClientOption {
	return func(b *clientBuilder) error {
		if threshold > 0 {
			b.failureThreshold = threshold
		}
		return nil
	}
}

// WithCircuitBreakerTimeout sets the duration in seconds to wait before retrying a failed endpoint.
func WithCircuitBreakerTimeout(timeout int) ClientOption {
	return func(b *clientBuilder) error {
		if timeout > 0 {
			b.retryTimeout = timeout
		}
		return nil
	}
}

// WithSorobanAltURLs configures multiple Soroban RPC endpoints for adaptive failover.
// The first URL in the list is used as the primary endpoint. When a request fails,
// the client selects the next endpoint according to the active FailoverPolicy.
// All URLs must be valid http/https URLs.
func WithSorobanAltURLs(urls []string) ClientOption {
	return func(b *clientBuilder) error {
		for _, u := range urls {
			if err := isValidURL(u); err != nil {
				return errors.WrapValidationError(fmt.Sprintf("invalid URL in sorobanAltURLs: %v", err))
			}
		}
		if len(urls) > 0 {
			b.sorobanAltURLs = urls
			b.sorobanURL = urls[0]
		}
		return nil
	}
}

// WithFailoverPolicy sets a custom FailoverPolicy for Soroban RPC endpoint selection.
// Use this to override the default weighted strategy, degraded threshold, or recovery
// probe interval. If not called, DefaultFailoverPolicy() is used.
func WithFailoverPolicy(policy FailoverPolicy) ClientOption {
	return func(b *clientBuilder) error {
		b.failoverPolicy = &policy
		return nil
	}
}

// WithPoolConfig sets a custom ProviderPool configuration. The pool is used for
// ordered provider selection with health tracking, per-request deadlines, and an
// explicit retry policy. If not called, DefaultPoolConfig() is used.
func WithPoolConfig(cfg PoolConfig) ClientOption {
	return func(b *clientBuilder) error {
		b.poolConfig = &cfg
		return nil
	}
}

// WithMaxRetries overrides the maximum number of provider attempts in the pool
// retry policy. Minimum 1; values ≤ 0 are ignored.
func WithMaxRetries(n int) ClientOption {
	return func(b *clientBuilder) error {
		if n > 0 {
			if b.poolConfig == nil {
				cfg := DefaultPoolConfig()
				b.poolConfig = &cfg
			}
			b.poolConfig.MaxRetries = n
		}
		return nil
	}
}

// WithRequestDeadline sets a per-attempt context deadline for every provider
// request. Each attempt gets its own deadline independently of the parent context.
// A zero value disables the per-attempt deadline (parent context governs only).
func WithRequestDeadline(d time.Duration) ClientOption {
	return func(b *clientBuilder) error {
		if b.poolConfig == nil {
			cfg := DefaultPoolConfig()
			b.poolConfig = &cfg
		}
		b.poolConfig.RequestDeadline = d
		return nil
	}
}

// WithResponsePayloadLimit sets the maximum number of bytes the client will
// read from any Soroban RPC response body before returning a
// PayloadTooLargeError. Use 0 to keep the default (DefaultResponsePayloadLimit,
// currently 32 MiB). Values below 1 KiB are silently clamped to 1 KiB to
// prevent accidental breakage.
func WithResponsePayloadLimit(n int64) ClientOption {
	return func(b *clientBuilder) error {
		const minLimit = 1024
		if n > 0 && n < minLimit {
			n = minLimit
		}
		b.responsePayloadLimit = n
		return nil
	}
}

// WithAggregateFetchLimit sets the maximum total bytes the client will fetch
// across all RPC responses in a session before returning an
// AggregateLimitExceededError. Use 0 to keep the default (DefaultAggregateFetchLimit,
// currently 512 MiB). Values below 1 MiB are silently clamped to 1 MiB to
// prevent accidental breakage.
func WithAggregateFetchLimit(n int64) ClientOption {
	return func(b *clientBuilder) error {
		const minLimit = 1024 * 1024
		if n > 0 && n < minLimit {
			n = minLimit
		}
		b.aggregateFetchLimit = n
		return nil
	}
}

// WithReplayPinProvider locks the provider pool to a single endpoint URL for
// deterministic replay. When pinned, silent failover is disabled: if the pinned
// provider fails the error is returned immediately with an explicit message.
// Pass an empty string to disable pinning.
func WithReplayPinProvider(url string) ClientOption {
	return func(b *clientBuilder) error {
		b.replayPinnedURL = url
		return nil
	}
}

func NewClient(opts ...ClientOption) (*Client, error) {
	builder := newBuilder()

	if builder.token == "" {
		builder.token = os.Getenv("GLASSBOX_RPC_TOKEN")
	}

	for _, opt := range opts {
		if err := opt(builder); err != nil {
			return nil, err
		}
	}

	if err := builder.validate(); err != nil {
		return nil, err
	}

	return builder.build()
}

func (b *clientBuilder) validate() error {
	if b.network == "" {
		b.network = Mainnet
	}

	if err := ValidateNetworkDiscovery(b.network, b.sorobanURL); err != nil {
		return err
	}

	if b.horizonURL == "" && b.sorobanURL == "" {
		b.horizonURL = b.getDefaultHorizonURL(b.network)
	}

	return nil
}

func (b *clientBuilder) getDefaultHorizonURL(net Network) string {
	switch net {
	case Testnet:
		return TestnetHorizonURL
	case Futurenet:
		return FuturenetHorizonURL
	default:
		return MainnetHorizonURL
	}
}

func (b *clientBuilder) getDefaultSorobanURL(net Network) string {
	switch net {
	case Testnet:
		return TestnetSorobanURL
	case Futurenet:
		return FuturenetSorobanURL
	default:
		return MainnetSorobanURL
	}
}

func (b *clientBuilder) getConfig(net Network) NetworkConfig {
	switch net {
	case Testnet:
		return TestnetConfig
	case Futurenet:
		return FuturenetConfig
	default:
		return MainnetConfig
	}
}

func (b *clientBuilder) build() (*Client, error) {
	if b.sorobanURL == "" {
		b.sorobanURL = b.getDefaultSorobanURL(b.network)
	}

	if b.config == nil {
		cfg := b.getConfig(b.network)
		b.config = &cfg
	}

	if b.horizonURL == "" {
		b.horizonURL = b.config.HorizonURL
	}

	if len(b.altURLs) == 0 {
		b.altURLs = []string{b.horizonURL}
	}

	// If no explicit Soroban alt URLs were provided, seed from the primary Soroban URL.
	if len(b.sorobanAltURLs) == 0 {
		b.sorobanAltURLs = []string{b.sorobanURL}
	}

	if b.httpClient == nil {
		mws := b.middlewares
		if b.loggingEnabled {
			// Prepend so the logging middleware is outermost in the chain,
			// ensuring it captures the full round-trip including all user middlewares.
			mws = append([]Middleware{NewLoggingMiddleware()}, mws...)
		}
		b.httpClient = createHTTPClient(b.token, b.requestTimeout, mws...)
	}

	policy := DefaultFailoverPolicy()
	if b.failoverPolicy != nil {
		policy = *b.failoverPolicy
	}

	hc := NewHealthCollector()
	selector := NewEndpointSelector(policy, hc)

	// Build the ProviderPool for ordered failover with health tracking.
	poolCfg := DefaultPoolConfig()
	if b.poolConfig != nil {
		poolCfg = *b.poolConfig
	}
	if b.requestTimeout > 0 && poolCfg.RequestDeadline == 0 {
		poolCfg.RequestDeadline = b.requestTimeout
	}
	if b.replayPinnedURL != "" {
		poolCfg.PinnedURL = b.replayPinnedURL
	}

	// The pool governs Soroban endpoints; seed it from sorobanAltURLs (or the
	// single sorobanURL when only one endpoint is configured).
	poolURLs := b.sorobanAltURLs
	if len(poolURLs) == 0 && b.sorobanURL != "" {
		poolURLs = []string{b.sorobanURL}
	}
	if len(poolURLs) == 0 {
		poolURLs = []string{b.getDefaultSorobanURL(b.network)}
	}
	pool := NewProviderPool(poolURLs, poolCfg)

	return &Client{
		HorizonURL: b.horizonURL,
		Horizon: &horizonclient.Client{
			HorizonURL: b.horizonURL,
			HTTP:       b.httpClient,
		},
		Network:          b.network,
		SorobanURL:       b.sorobanURL,
		AltURLs:          b.altURLs,
		SorobanAltURLs:   b.sorobanAltURLs,
		httpClient:       b.httpClient,
		token:            b.token,
		Config:           *b.config,
		CacheEnabled:     b.cacheEnabled,
		methodTelemetry:  b.methodTelemetry,
		failures:         make(map[string]int),
		lastFailure:      make(map[string]time.Time),
		FailureThreshold: b.failureThreshold,
		RetryTimeout:     b.retryTimeout,
		middlewares:      b.middlewares,
		healthCollector:      hc,
		selector:             selector,
		failoverPolicy:       policy,
		providerPool:         pool,
		ResponsePayloadLimit: b.responsePayloadLimit,
		AggregateFetchLimit: b.aggregateFetchLimit,
		aggregateTracker:    NewAggregateTracker(b.aggregateFetchLimit),
	}, nil
}
