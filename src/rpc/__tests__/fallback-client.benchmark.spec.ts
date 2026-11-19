// Copyright (c) 2026 dotandev
// SPDX-License-Identifier: MIT OR Apache-2.0

import { FallbackRPCClient } from '../fallback-client';
import { RPCConfig } from '../../config/rpc-config';
import axios from 'axios';
import MockAdapter from 'axios-mock-adapter';

describe('FallbackRPCClient Benchmarks', () => {
    let client: FallbackRPCClient;
    let mock: MockAdapter;

    const config: RPCConfig = {
        urls: ['https://rpc1.test.com'],
        timeout: 5000,
        retries: 0,
        retryDelay: 10,
        circuitBreakerThreshold: 3,
        circuitBreakerTimeout: 10000,
        maxRedirects: 5,
    };

    beforeEach(() => {
        mock = new MockAdapter(axios as any);
        client = new FallbackRPCClient(config);
    });

    afterEach(() => {
        mock.restore();
    });

    describe('Computation benchmarks', () => {
        it('should benchmark chunking performance', () => {
            const wasmPaths = Array(1000).fill(null).map((_, i) => `/path/to/contract${i}.wasm`);

            const start = performance.now();

            const chunks = client['chunkStringSlice'](wasmPaths, 64);

            const end = performance.now();
            const time = end - start;

            console.log(`Chunking 1000 paths: ${time.toFixed(2)}ms, chunks: ${chunks.length}`);
            expect(chunks.length).toBe(16);
            expect(time).toBeLessThan(10);
        });

        it('should measure health status retrieval performance', () => {
            const iterations = 1000;

            const start = performance.now();
            for (let i = 0; i < iterations; i++) {
                client.getHealthStatus();
            }
            const end = performance.now();
            const avgTime = (end - start) / iterations;

            console.log(`getHealthStatus avg: ${avgTime.toFixed(4)}ms`);
            expect(avgTime).toBeLessThan(1);
        });

        it('should measure circuit breaker performance', async () => {
            mock.onPost('https://rpc1.test.com/fail').networkError();
            mock.onPost('https://rpc1.test.com/success').reply(200, { ok: true });

            for (let i = 0; i < 3; i++) {
                try { await client.request('/fail', {}); } catch {}
            }

            const start = performance.now();
            try { await client.request('/success', {}); } catch {}
            const end = performance.now();

            console.log(`Circuit breaker check: ${end - start}ms`);
        });
    });
});
