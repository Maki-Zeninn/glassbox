# Release-note fragments

This directory holds pending release-note fragments. Each fragment is a single
TOML file describing one user-facing change. The `make changelog-generate`
target assembles them into `CHANGELOG.md` and the `make changelog-check` target
validates them in CI.

See `docs/changelog-fragments.md` for the full authoring guide.
