// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package httpclient

import (
	"net"
	"net/http"
	"time"

	"github.com/dotandev/glassbox/internal/rpc"
)

var defaultClient = &http.Client{
	Timeout: 30 * time.Second,
	Transport: NewRetryTransport(),
}

// DefaultClient returns the shared default HTTP client.
func DefaultClient() *http.Client {
	return defaultClient
}

// NewRetryTransport creates a transport with retry logic for the default HTTP client.
func NewRetryTransport() http.RoundTripper {
	baseTransport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,

		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,

		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	return rpc.NewRetryTransport(rpc.DefaultRetryConfig(), baseTransport)
}
