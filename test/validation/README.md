# Validation Fixtures

Fixtures used by the scheduled validation suite live under `test/validation/fixtures/`.

Rules
- Use deterministic, minimal data.
- Do not include real keys, hashes, or credentials.
- Audit fixtures must use `testonly_` semantics or filename prefixes and be
  marked clearly at the top of the file.
