// Copyright (c) glassbox Authors.
// SPDX-License-Identifier: Apache-2.0

// Minimal module declarations to keep TS builds working in environments without types packages.

declare module 'fast-json-stable-stringify' {
  const stringify: (value: any) => string;
  export default stringify;
}

// Test-only helpers exported from the pkcs11js manual mock (src/__mocks__/pkcs11js.ts).
// These are not part of the real pkcs11js package — they are only available when
// jest.mock('pkcs11js') is active.
declare module 'pkcs11js' {
  export function __getState(): any;
  export function __resetState(): void;
  export function __setFailure(method: string, failure: { message: string; code?: number; method?: string; times?: number }): void;
  export function __setSlots(slots: number[]): void;
  export function __setTokenLabels(labels: Record<string, string>): void;
  export function __setKeys(keys: number[]): void;
}
