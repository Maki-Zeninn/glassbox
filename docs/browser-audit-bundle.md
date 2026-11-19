# Browser-Safe Audit Verification Bundle

**Module path**: `src/audit/browser`
**Bundle target**: < 10 KiB minified + gzipped (zero external runtime dependencies)

## Overview

`src/audit/browser` provides a browser-safe subset of the audit package. It
supports verifying Ed25519-signed audit records produced by the Node.js signer
without requiring any Node.js built-ins or native PKCS#11 / KMS dependencies.

### What is included

| Export | Description |
|---|---|
| `verifyAuditLogBrowser` | Verify a signed audit log using the Web Crypto API |
| `getProviderCapabilities` | Report which algorithms are available at runtime |
| `canonicalStringify` | Pure deterministic JSON serializer (no deps) |
| `buildAuditHashInput` | Builds the canonical hash-input string for an audit log |

### What is NOT included (Node-only)

| Feature | Reason |
|---|---|
| `AuditLogger` / signers | Uses Node.js `crypto` built-in for Ed25519 signing |
| `SoftwareEd25519Signer` | `sign()` from `node:crypto` |
| `Pkcs11Signer` | Requires native `pkcs11js` binary |
| `KmsSigner` | Requires `@aws-sdk/client-kms` (Node.js SDK) |

When any of these unsupported providers is encountered, `verifyAuditLogBrowser`
returns an explicit `unsupported_algorithm` field so callers can degrade
gracefully instead of receiving an opaque error.

## API surface

### `verifyAuditLogBrowser(auditLog)`

```typescript
import { verifyAuditLogBrowser } from 'glassbox/src/audit/browser';

const result = await verifyAuditLogBrowser(parsedAuditLog);
// result.valid           — true if hash AND signature are correct
// result.hash_valid      — hash integrity check
// result.signature_valid — Ed25519 signature check
// result.unsupported_algorithm — set when algorithm cannot be verified
// result.detail          — human-readable explanation
```

### `getProviderCapabilities()`

```typescript
import { getProviderCapabilities } from 'glassbox/src/audit/browser';

const caps = await getProviderCapabilities();
// caps.ed25519Verify — requires Chrome 113+, Firefox 130+, Safari 17+
// caps.sha256        — universally available
// caps.pkcs11        — always false in browser
// caps.awsKms        — always false in browser
```

## Browser compatibility

| Feature | Chrome | Firefox | Safari | Edge |
|---|---|---|---|---|
| SHA-256 (`SubtleCrypto.digest`) | 37+ | 34+ | 11+ | 12+ |
| Ed25519 verify (`SubtleCrypto.verify`) | 113+ | 130+ | 17+ | 113+ |

If `ed25519Verify` is `false`, `verifyAuditLogBrowser` will return
`unsupported_algorithm: 'Ed25519'` with instructions to use the Node.js verifier.

## Bundler configuration

The browser entry point has no dynamic `require()` calls and no platform
checks, so any standard bundler (webpack, esbuild, rollup, vite) can
tree-shake it to the bare minimum.

### webpack (example)

```js
// webpack.config.js
module.exports = {
  entry: './src/audit/browser/index.ts',
  resolve: {
    fallback: {
      // These are NOT needed — the browser bundle has no Node.js imports.
      crypto: false,
      buffer: false,
      path:   false,
    },
  },
};
```

### esbuild (example)

```bash
esbuild src/audit/browser/index.ts \
  --bundle \
  --minify \
  --target=es2020 \
  --outfile=dist/glassbox-audit-browser.min.js
```

## Testing

```bash
# Unit tests (Jest / Node.js — tests the Web Crypto path via globalThis.crypto)
npx jest tests/browser-audit-verify.test.ts

# TypeScript type-check only
npx tsc --noEmit --project tsconfig.json
```
