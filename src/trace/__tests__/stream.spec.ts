// Copyright (c) 2026 dotandev
// SPDX-License-Identifier: MIT OR Apache-2.0

import {
    TraceStream,
    TraceEvent,
    StreamableTrace,
    TraceStepEvent,
    TraceHostCallEvent,
    TraceWarningEvent,
    TraceCompletionEvent,
} from '../stream';

const MINIMAL_TRACE: StreamableTrace = {
    transaction_hash: 'abc123',
    states: [
        { step: 1, timestamp: '2026-01-01T00:00:01Z', operation: 'invoke', function: 'transfer', arguments: ['alice', 10] },
        { step: 2, timestamp: '2026-01-01T00:00:02Z', operation: 'event', return_value: { ok: true } },
    ],
};

const TRACE_WITH_HOST_AND_ERROR: StreamableTrace = {
    transaction_hash: 'def456',
    states: [
        {
            step: 1, timestamp: '2026-01-01T00:00:01Z', operation: 'invoke',
            host_state: { ledger: 100 }, memory: { heap: 512 },
        },
        {
            step: 2, timestamp: '2026-01-01T00:00:02Z', operation: 'call',
            error: 'insufficient balance',
        },
    ],
};

async function collectEvents(stream: TraceStream): Promise<TraceEvent[]> {
    const events: TraceEvent[] = [];
    for await (const e of stream) {
        events.push(e);
    }
    return events;
}

describe('TraceStream – async iterator', () => {
    it('yields events in order: step events then completion', async () => {
        const events = await collectEvents(new TraceStream(MINIMAL_TRACE));
        const kinds = events.map(e => e.kind);
        expect(kinds).toEqual(['step', 'step', 'completion']);
    });

    it('maps step fields onto TraceStepEvent correctly', async () => {
        const events = await collectEvents(new TraceStream(MINIMAL_TRACE));
        const first = events[0] as TraceStepEvent;
        expect(first.kind).toBe('step');
        expect(first.step).toBe(1);
        expect(first.operation).toBe('invoke');
        expect(first.function).toBe('transfer');
        expect(first.arguments).toEqual(['alice', 10]);
    });

    it('yields a hostCall event after the step when host_state is present', async () => {
        const events = await collectEvents(new TraceStream(TRACE_WITH_HOST_AND_ERROR));
        const kinds = events.map(e => e.kind);
        expect(kinds).toContain('hostCall');
        const hc = events.find(e => e.kind === 'hostCall') as TraceHostCallEvent;
        expect(hc.step).toBe(1);
        expect(hc.host_state).toEqual({ ledger: 100 });
        expect(hc.memory).toEqual({ heap: 512 });
    });

    it('yields a warning event when the step has an error field', async () => {
        const events = await collectEvents(new TraceStream(TRACE_WITH_HOST_AND_ERROR));
        const warn = events.find(e => e.kind === 'warning') as TraceWarningEvent;
        expect(warn).toBeDefined();
        expect(warn.step).toBe(2);
        expect(warn.message).toBe('insufficient balance');
    });

    it('yields a completion event as the final event', async () => {
        const events = await collectEvents(new TraceStream(MINIMAL_TRACE));
        const last = events[events.length - 1] as TraceCompletionEvent;
        expect(last.kind).toBe('completion');
        expect(last.transaction_hash).toBe('abc123');
        expect(last.total_steps).toBe(2);
    });

    it('emits no events for an empty trace except completion', async () => {
        const empty: StreamableTrace = { transaction_hash: 'empty', states: [] };
        const events = await collectEvents(new TraceStream(empty));
        expect(events).toHaveLength(1);
        expect(events[0].kind).toBe('completion');
        expect((events[0] as TraceCompletionEvent).total_steps).toBe(0);
    });

    it('does not include host_state fields on step events', async () => {
        const events = await collectEvents(new TraceStream(TRACE_WITH_HOST_AND_ERROR));
        const stepEvents = events.filter(e => e.kind === 'step') as TraceStepEvent[];
        for (const se of stepEvents) {
            expect((se as any).host_state).toBeUndefined();
            expect((se as any).memory).toBeUndefined();
        }
    });
});

describe('TraceStream – cancellation via AbortSignal', () => {
    it('stops iteration when signal is aborted before iteration begins', async () => {
        const controller = new AbortController();
        controller.abort();
        const events = await collectEvents(new TraceStream(MINIMAL_TRACE, { signal: controller.signal }));
        // Signal already aborted: generator returns immediately on first check
        expect(events.length).toBe(0);
    });

    it('stops mid-stream when signal is aborted', async () => {
        const controller = new AbortController();
        const events: TraceEvent[] = [];
        const stream = new TraceStream(MINIMAL_TRACE, { signal: controller.signal });
        for await (const e of stream) {
            events.push(e);
            if (events.length === 1) controller.abort(); // abort after first event
        }
        expect(events.length).toBe(1);
        expect(events[0].kind).toBe('step');
    });

    it('does not yield completion when cancelled', async () => {
        const controller = new AbortController();
        controller.abort();
        const events = await collectEvents(new TraceStream(MINIMAL_TRACE, { signal: controller.signal }));
        expect(events.some(e => e.kind === 'completion')).toBe(false);
    });
});

describe('TraceStream – subscribe callback adapter', () => {
    it('delivers all events in order via callback', async () => {
        const received: TraceEvent[] = [];
        const stream = new TraceStream(MINIMAL_TRACE);
        await new Promise<void>(resolve => {
            const cancel = stream.subscribe(async event => {
                received.push(event);
                if (event.kind === 'completion') resolve();
            });
            void cancel; // kept for reference
        });
        expect(received.map(e => e.kind)).toEqual(['step', 'step', 'completion']);
    });

    it('stops delivering after cancel() is called', async () => {
        const received: TraceEvent[] = [];
        const stream = new TraceStream(MINIMAL_TRACE);
        await new Promise<void>(resolve => {
            const cancel = stream.subscribe(event => {
                received.push(event);
                if (received.length === 1) {
                    cancel();
                    setTimeout(resolve, 20); // brief wait to confirm no more arrive
                }
            });
        });
        expect(received.length).toBe(1);
    });

    it('does not crash the stream when the handler throws', async () => {
        const received: string[] = [];
        const stream = new TraceStream(MINIMAL_TRACE);
        await new Promise<void>(resolve => {
            stream.subscribe(event => {
                if (event.kind === 'step') throw new Error('handler error');
                if (event.kind === 'completion') {
                    received.push('completion');
                    resolve();
                }
            });
        });
        // completion should still arrive despite handler throwing on step events
        expect(received).toContain('completion');
    });

    it('awaits async handlers before dispatching the next event (backpressure)', async () => {
        const order: number[] = [];
        let dispatchCount = 0;
        const stream = new TraceStream(MINIMAL_TRACE);
        await new Promise<void>(resolve => {
            stream.subscribe(async event => {
                const seq = ++dispatchCount;
                order.push(seq);
                // Simulate slow async work
                await new Promise(r => setTimeout(r, 5));
                order.push(seq * 10); // post-work marker
                if (event.kind === 'completion') resolve();
            });
        });
        // Verify sequential pattern: [1, 10, 2, 20, 3, 30]
        for (let i = 0; i < order.length - 1; i += 2) {
            expect(order[i + 1]).toBe(order[i] * 10);
        }
    });
});

describe('TraceStream – AsyncIterable<TraceEvent> source passthrough', () => {
    async function* makeSource(events: TraceEvent[]): AsyncGenerator<TraceEvent> {
        for (const e of events) yield e;
    }

    it('passes through events from an async source unchanged', async () => {
        const events: TraceEvent[] = [
            { kind: 'step', step: 1, timestamp: 't', operation: 'op' },
            { kind: 'completion', transaction_hash: 'x', total_steps: 1 },
        ];
        const stream = new TraceStream(makeSource(events));
        const collected = await collectEvents(stream);
        expect(collected).toEqual(events);
    });

    it('respects cancellation on async source', async () => {
        const events: TraceEvent[] = [
            { kind: 'step', step: 1, timestamp: 't', operation: 'op' },
            { kind: 'completion', transaction_hash: 'x', total_steps: 1 },
        ];
        const controller = new AbortController();
        controller.abort();
        const stream = new TraceStream(makeSource(events), { signal: controller.signal });
        const collected = await collectEvents(stream);
        expect(collected.length).toBe(0);
    });
});

describe('TraceStream – reconnect behavior', () => {
    it('delivers all events on a new stream after a previous stream was cancelled', async () => {
        const controller = new AbortController();
        controller.abort();
        const cancelled = await collectEvents(new TraceStream(MINIMAL_TRACE, { signal: controller.signal }));
        expect(cancelled.length).toBe(0);

        // Create a fresh stream from the same source — simulates reconnect
        const reconnected = await collectEvents(new TraceStream(MINIMAL_TRACE));
        expect(reconnected.map(e => e.kind)).toEqual(['step', 'step', 'completion']);
    });

    it('delivers all events on a new stream after a previous stream completed', async () => {
        const first = await collectEvents(new TraceStream(MINIMAL_TRACE));
        expect(first.map(e => e.kind)).toEqual(['step', 'step', 'completion']);

        const second = await collectEvents(new TraceStream(MINIMAL_TRACE));
        expect(second.map(e => e.kind)).toEqual(['step', 'step', 'completion']);
        expect(second).toEqual(first);
    });

    it('subscribe can be called again on a new stream after cancellation', async () => {
        const stream1 = new TraceStream(MINIMAL_TRACE);
        const received1: string[] = [];
        const cancel = stream1.subscribe(event => { received1.push(event.kind); cancel(); });
        await new Promise(r => setTimeout(r, 30));
        expect(received1).toHaveLength(1); // cancelled after first event

        // New stream after cancel — full delivery expected
        const stream2 = new TraceStream(MINIMAL_TRACE);
        const received2: string[] = [];
        await new Promise<void>(resolve => {
            stream2.subscribe(event => {
                received2.push(event.kind);
                if (event.kind === 'completion') resolve();
            });
        });
        expect(received2).toEqual(['step', 'step', 'completion']);
    });
});
