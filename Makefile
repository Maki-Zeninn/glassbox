.PHONY: build test lint lint-strict lint-unused test-unused validate-ci validate-interface clean
.PHONY: rust-lint rust-lint-strict rust-test rust-build lint-all-strict
.PHONY: build test lint validate-errors clean bench bench-rpc bench-sim bench-replay bench-sourcemap bench-profile bench-perf-regression
.PHONY: fmt fmt-go fmt-rust pre-commit
.PHONY: release release-linux release-darwin release-windows package verify-release ts-build
.PHONY: manifest-sign manifest-verify
.PHONY: reproducibility-check
.PHONY: license-scan
.PHONY: mutation-test mutation-test-report mutation-test-ci mutation-test-install \
        mutation-test-subsystems mutation-test-ci-subsystems mutation-test-subsystem-report
.PHONY: changelog-check changelog-generate changelog-dry-run
.PHONY: validate-docs validate-docs-determinism validate-docs-links validate-docs-flags
.PHONY: check-bindings-byte-stable

# Build variables
VERSION?=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT_SHA?=$(shell git rev-parse HEAD 2>/dev/null || echo "unknown")
BUILD_DATE?=$(shell date -u +"%Y-%m-%dT%H:%M:%SZ" 2>/dev/null || echo "unknown")
DIST_DIR?=dist/release

# ── Reproducible builds ────────────────────────────────────────────────────
# SOURCE_DATE_EPOCH is the Unix timestamp of the HEAD commit.  All archive
# tools (tar, zip) clamp file modification times to this value so that two
# builds from the same source produce byte-identical archives regardless of
# when they are run.
#
# The variable is exported so child processes (cargo, npm, zip wrappers)
# inherit it automatically.
#
# Reference: https://reproducible-builds.org/docs/source-date-epoch/
SOURCE_DATE_EPOCH?=$(shell git log -1 --format=%ct 2>/dev/null || echo "0")
export SOURCE_DATE_EPOCH

# Manifest signing variables — override on the command line or via environment.
# GLASSBOX_MANIFEST_SIGNING_KEY must be set externally (never default here).
SIGNER_IDENTITY?=ci-pipeline
KEY_ID?=
SBOM_REF?=

# Go build flags — inject version metadata at link time.
# -trimpath removes local file-system paths from the binary so builds on
# different machines produce identical binaries.
GO_LDFLAGS=-ldflags "-s -w \
  -X 'github.com/dotandev/glassbox/internal/version.Version=$(VERSION)' \
  -X 'github.com/dotandev/glassbox/internal/version.CommitSHA=$(COMMIT_SHA)' \
  -X 'github.com/dotandev/glassbox/internal/version.BuildDate=$(BUILD_DATE)'"

# -trimpath is added separately so it appears before -ldflags in the command.
GO_BUILD_FLAGS=-trimpath $(GO_LDFLAGS)

# Build the main binary
build:
	go build $(GO_BUILD_FLAGS) -o bin/glassbox ./cmd/glassbox

# Build for release (optimized, stripped)
build-release:
	go build $(GO_BUILD_FLAGS) -o bin/glassbox ./cmd/glassbox

# ──────────────────────────────────────────────
# Cross-compilation targets
# ──────────────────────────────────────────────

release-linux:
	@mkdir -p $(DIST_DIR)
	GOOS=linux   GOARCH=amd64  CGO_ENABLED=0 go build $(GO_BUILD_FLAGS) -o $(DIST_DIR)/glassbox-linux-amd64   ./cmd/glassbox
	GOOS=linux   GOARCH=arm64  CGO_ENABLED=0 go build $(GO_BUILD_FLAGS) -o $(DIST_DIR)/glassbox-linux-arm64   ./cmd/glassbox
	@echo "Linux binaries built in $(DIST_DIR)"

release-darwin:
	@mkdir -p $(DIST_DIR)
	GOOS=darwin  GOARCH=amd64  CGO_ENABLED=0 go build $(GO_BUILD_FLAGS) -o $(DIST_DIR)/glassbox-darwin-amd64  ./cmd/glassbox
	GOOS=darwin  GOARCH=arm64  CGO_ENABLED=0 go build $(GO_BUILD_FLAGS) -o $(DIST_DIR)/glassbox-darwin-arm64  ./cmd/glassbox
	@echo "macOS binaries built in $(DIST_DIR)"

release-windows:
	@mkdir -p $(DIST_DIR)
	GOOS=windows GOARCH=amd64  CGO_ENABLED=0 go build $(GO_BUILD_FLAGS) -o $(DIST_DIR)/glassbox-windows-amd64.exe ./cmd/glassbox
	@echo "Windows binary built in $(DIST_DIR)"

# Build TypeScript/Node artifacts
ts-build:
	npm ci --prefer-offline
	npm run build
	@echo "TypeScript artifacts built in dist/"

# ──────────────────────────────────────────────
# Changelog management
# ──────────────────────────────────────────────

# Validate all pending changelog fragments (run in CI on every PR).
# Exits non-zero if any fragment is malformed or has a duplicate PR number.
changelog-check:
	@bash scripts/validate-fragments.sh

# Preview the generated release section without writing any files.
changelog-dry-run:
	@bash scripts/generate-changelog.sh --dry-run

# Generate CHANGELOG.md from all pending fragments and prepend the new section.
# Set VERSION to override the auto-detected git tag:
#   make changelog-generate VERSION=v1.3.0
changelog-generate: changelog-check
	@bash scripts/generate-changelog.sh $(if $(VERSION),--version $(VERSION),)

# Validate documentation: determinism, broken links, unknown flags
validate-docs:
	@bash scripts/validate-docs.sh

# Validate documentation — determinism only
validate-docs-determinism:
	@bash scripts/validate-docs.sh --determinism

# Validate documentation — internal links only
validate-docs-links:
	@bash scripts/validate-docs.sh --links

# Validate documentation — command flag smoke check
validate-docs-flags:
	@bash scripts/validate-docs.sh --flags

# Build all release targets
release: changelog-check release-linux release-darwin release-windows ts-build
	@echo "All release targets built"

# ──────────────────────────────────────────────
# Packaging: checksums + archives
# ──────────────────────────────────────────────

# Produce per-binary SHA-256 checksums and zip/tar archives.
# Archives are reproducible: file timestamps are clamped to SOURCE_DATE_EPOCH,
# entries are sorted, and owner/group fields are normalised.
# Requires: sha256sum (Linux) or shasum -a 256 (macOS), zip, tar.
package: release
	@echo "Packaging release artifacts (SOURCE_DATE_EPOCH=$(SOURCE_DATE_EPOCH))..."
	@cd $(DIST_DIR) && \
	  for f in glassbox-linux-* glassbox-darwin-* glassbox-windows-*; do \
	    [ -f "$$f" ] || continue; \
	    echo "  archiving $$f"; \
	    case "$$f" in \
	      *.exe) \
	        zip --no-dir-entries -X -9 "$${f%.exe}.zip" "$$f" ;; \
	      *) \
	        tar --sort=name \
	            --owner=0 --group=0 --numeric-owner \
	            --mtime="@$(SOURCE_DATE_EPOCH)" \
	            -czf "$${f}.tar.gz" "$$f" ;; \
	    esac; \
	  done
	@echo "  generating checksums..."
	@cd $(DIST_DIR) && \
	  if command -v sha256sum >/dev/null 2>&1; then \
	    sha256sum *.tar.gz *.zip 2>/dev/null | LC_ALL=C sort > checksums.sha256 || true; \
	  else \
	    shasum -a 256 *.tar.gz *.zip 2>/dev/null | LC_ALL=C sort > checksums.sha256 || true; \
	  fi
	@echo "  writing version metadata..."
	@printf 'version=%s\ncommit=%s\nbuild_date=%s\nsource_date_epoch=%s\n' \
	  "$(VERSION)" "$(COMMIT_SHA)" "$(BUILD_DATE)" "$(SOURCE_DATE_EPOCH)" > $(DIST_DIR)/version.txt
	@echo "Package complete. Artifacts in $(DIST_DIR):"
	@ls -lh $(DIST_DIR)

# Smoke-test the produced binaries
verify-release:
	@bash scripts/verify-release.sh $(DIST_DIR)

# Check binary sizes against thresholds
size-check:
	@bash scripts/check_binary_size.sh

# ──────────────────────────────────────────────
# Signed release manifest
#
# manifest-sign  — generate and sign dist/release/manifest.json.
#   Requires GLASSBOX_MANIFEST_SIGNING_KEY to be set to a PKCS#8 PEM
#   Ed25519 private key (literal PEM text or a file path).
#   The private key is NEVER written to disk or embedded in the manifest.
#
#   Usage:
#     GLASSBOX_MANIFEST_SIGNING_KEY="$(cat ./release-key.pem)" make manifest-sign
#     GLASSBOX_MANIFEST_SIGNING_KEY=/path/to/key.pem make manifest-sign
#
#   Optional overrides (defaults come from git):
#     VERSION COMMIT_SHA BUILD_DATE SIGNER_IDENTITY KEY_ID SBOM_REF
#
# manifest-verify — offline verification of dist/release/manifest.json.
#   Requires python3 + the 'cryptography' package for Ed25519 verification.
#   All other checks (presence, SHA-256, no-unlisted) need only bash + python3.
# ──────────────────────────────────────────────

manifest-sign: package
	@if [ -z "$(GLASSBOX_MANIFEST_SIGNING_KEY)" ]; then \
	  echo "ERROR: GLASSBOX_MANIFEST_SIGNING_KEY is not set."; \
	  echo "       Set it to a PKCS#8 PEM Ed25519 private key (file path or literal PEM)."; \
	  exit 1; \
	fi
	@VERSION="$(VERSION)" COMMIT_SHA="$(COMMIT_SHA)" BUILD_DATE="$(BUILD_DATE)" \
	  SIGNER_IDENTITY="$(SIGNER_IDENTITY)" KEY_ID="$(KEY_ID)" SBOM_REF="$(SBOM_REF)" \
	  bash scripts/sign-manifest.sh $(DIST_DIR)

manifest-verify:
	@bash scripts/verify-manifest.sh $(DIST_DIR)/manifest.json $(DIST_DIR)

# ──────────────────────────────────────────────
# Reproducibility check
#
# Builds glassbox-linux-amd64 twice in isolated temp directories from the
# same SOURCE_DATE_EPOCH, then compares SHA-256 hashes.  A mismatch means
# some build input is non-deterministic.
#
# Usage:
#   make reproducibility-check
#   make reproducibility-check SOURCE_DATE_EPOCH=1700000000
# ──────────────────────────────────────────────

reproducibility-check:
	@bash scripts/check-reproducibility.sh

# ──────────────────────────────────────────────
# License scanning
#
# Scans Go, Rust, and Node dependencies against the policy in
# license-policy.json and fails on violations.
#
# Usage:
#   make license-scan
# ──────────────────────────────────────────────

license-scan:
	@bash scripts/check-licenses.sh

# Compare the current SBOM against the previous release SBOM.
# Usage: make sbom-diff PREV_SBOM=path/to/old.spdx.json NEW_SBOM=path/to/new.spdx.json
# If PREV_SBOM is omitted, attempts to download the latest GitHub release SBOM.
sbom-diff:
	@if [ -z "$(PREV_SBOM)" ]; then \
	  echo "ERROR: PREV_SBOM is not set. Provide the path to the previous release SBOM."; \
	  echo "       Example: make sbom-diff PREV_SBOM=dist/old/glassbox-v1.0.0.spdx.json NEW_SBOM=dist/release/glassbox-v1.1.0.spdx.json"; \
	  exit 1; \
	fi
	@bash scripts/sbom-diff.sh \
	  "$(PREV_SBOM)" \
	  "$(if $(NEW_SBOM),$(NEW_SBOM),$(DIST_DIR)/glassbox-$(VERSION).spdx.json)" \
	  --policy license-policy.json \
	  --output "$(if $(SBOM_DIFF_OUTPUT),$(SBOM_DIFF_OUTPUT),$(DIST_DIR)/sbom-diff.json)" \
	  --format "$(if $(SBOM_DIFF_FORMAT),$(SBOM_DIFF_FORMAT),text)"

# Run tests
test:
	go test ./...

# Run full linter suite
lint:
	golangci-lint run

# Run strict linting (fail on all warnings)
lint-strict:
	@echo "Running strict Go linting..."
	@golangci-lint run --config=.golangci.yml --max-issues-per-linter=0 --max-same-issues=0
	@go vet ./...
	@echo " Strict linting passed"

# Run unused code detection
lint-unused:
	./scripts/lint-unused.sh

# Test unused code detection setup
test-unused:
	./scripts/test-unused-detection.sh

# Validate CI/CD configuration
validate-ci:
	./scripts/validate-ci.sh
# Validate error standardization
validate-errors:
	./scripts/validate-errors.sh

# Validate interface implementation
validate-interface:
	./scripts/validate-interface.sh

# Clean build artifacts
clean:
	rm -rf bin/
	go clean -cache

# Install dependencies
deps:
	go mod tidy
	go mod download

# Run all benchmarks (RPC, replay, sourcemap, simulator)
bench:
	go test -bench=. -benchmem ./internal/rpc ./internal/replay ./internal/sourcemap ./internal/simulator

# Run RPC benchmarks only
bench-rpc:
	go test -bench=. -benchmem ./internal/rpc

# Run simulator benchmarks only
bench-sim:
	go test -bench=. -benchmem ./internal/simulator

# Run replay benchmarks only
bench-replay:
	go test -bench=. -benchmem ./internal/replay

# Run sourcemap benchmarks only
bench-sourcemap:
	go test -bench=. -benchmem ./internal/sourcemap

# Run benchmarks with CPU profiling
bench-profile:
	go test -bench=. -benchmem -cpuprofile=cpu.prof ./internal/rpc ./internal/replay ./internal/sourcemap ./internal/simulator

# Run performance regression tests for simulator
bench-perf-regression:
	@echo "Running performance regression tests..."
	@go test -v -run 'TestPerfRegression' ./internal/simulator/...
	@echo " Performance regression tests passed"

# Run performance budget validation
bench-budget:
	@echo "Running performance budget validation..."
	@go test -v -run 'TestBudget\|TestLoadPerformance\|TestCheckBudget\|TestTraceSize' ./internal/perfmetrics/...
	@echo " Performance budget validation passed"

# Run race-condition tests for event bus
test-race:
	@echo "Running race-condition tests..."
	@go test -race -v -run 'TestRace\|TestConcurrent' ./internal/eventbus/...
	@echo " Race-condition tests passed"

# Rust simulator targets
.PHONY: rust-lint rust-lint-strict rust-test rust-build

# Run Rust linting
rust-lint:
	cd simulator && cargo clippy --all-targets --all-features

# Run strict Rust linting (fail on all warnings)
rust-lint-strict:
	@echo "Running strict Rust linting..."
	@cd simulator && cargo clippy --all-targets --all-features -- \
		-D warnings \
		-D clippy::all \
		-D unused-variables \
		-D unused-imports \
		-D unused-mut \
		-D dead-code \
		-D unused-assignments \
		-W clippy::pedantic \
		-W clippy::nursery
	@echo " Strict Rust linting passed"

# Run Rust tests
rust-test:
	cd simulator && cargo test --verbose

# Build Rust simulator
rust-build:
	cd simulator && cargo build --verbose

# Run all strict linting (Go + Rust)
lint-all-strict: lint-strict rust-lint-strict
	@echo " All strict linting passed"

# ──────────────────────────────────────────────
# Formatting targets
# ──────────────────────────────────────────────

# Format Go files (gofmt + goimports)
fmt-go:
	@echo "Formatting Go files..."
	@gofmt -w .
	@if command -v goimports >/dev/null 2>&1; then \
		goimports -w .; \
	else \
		echo "⚠  goimports not found. Install: go install golang.org/x/tools/cmd/goimports@latest"; \
	fi
	@echo "✓ Go formatting done"

# Format Rust files (cargo fmt)
fmt-rust:
	@echo "Formatting Rust files..."
	@cd simulator && cargo fmt
	@echo "✓ Rust formatting done"

# Format everything (Go + Rust)
fmt: fmt-go fmt-rust
	@echo "✓ All formatting done"

# ──────────────────────────────────────────────
# Pre-commit setup
# ──────────────────────────────────────────────

# Install pre-commit hooks
pre-commit:
	@echo "Setting up pre-commit hooks..."
	@if command -v pre-commit >/dev/null 2>&1; then \
		pre-commit install; \
		echo "✓ Pre-commit hooks installed"; \
	else \
		echo "⚠  pre-commit not found. Install: pip install pre-commit"; \
		exit 1; \
	fi

# ──────────────────────────────────────────────
# Mutation testing (gremlins)
#
# gremlins is a Go mutation testing tool that runs the existing test suite
# against small code mutations to find untested branches.
# https://github.com/go-gremlins/gremlins
#
# Focused package scope — only packages whose validation logic is worth
# mutation-testing (config, trace export, session integrity, audit validation).
# Expanding this set is cheap; shrinking it after the fact is painful.
#
# Agreed mutation score threshold: 70 % (documented in docs/ci-artifacts.md).
# ──────────────────────────────────────────────

# Packages in scope for mutation testing (aggregate run).
# One per line so diffs are easy to review when scope changes.
MUTATION_PACKAGES := \
	./internal/cmd/... \
	./internal/session/... \
	./internal/trace/... \
	./internal/simulator/...

# HTML and JSON report output directory.
MUTATION_REPORT_DIR ?= mutation-report

# Minimum mutation score (0-100) for the aggregate run.  Jobs fail below this threshold.
MUTATION_THRESHOLD ?= 70

# ── Per-subsystem mutation groups ─────────────────────────────────────────────
#
# Each subsystem has its own package scope and minimum kill-rate threshold.
# Thresholds are set higher for security-critical code (signing, validation)
# because a surviving mutant there indicates an untested security boundary.
#
# Subsystem     Packages                                    Threshold  Rationale
# ──────────────────────────────────────────────────────────────────────────────
# security      internal/signer, internal/secutil           75%        Ed25519 signing path; key derivation
# validation    internal/cmd (command validation layer)     70%        CLI flag parsing; path/input checks
# replay        internal/replay, internal/deterministic     70%        Deterministic replay boundaries
# serialization internal/session, internal/manifest         70%        Canonicalization; archive integrity
#
# Exclusions (justify each):
#   internal/signer/kms.go      — AWS KMS I/O; mutations require live HSM to kill
#   internal/signer/pkcs11.go   — PKCS#11 I/O; nondeterministic without real token
#   internal/simulator/fuzzing_harness*.go — fuzzing infrastructure; not production logic
#
MUTATION_SUBSYSTEMS := security validation replay serialization

# security subsystem
MUTATION_SUBSYSTEM_security_PACKAGES  := ./internal/signer/... ./internal/secutil/...
MUTATION_SUBSYSTEM_security_THRESHOLD := 75
# Exclude KMS/PKCS#11 I/O stubs — mutations survive only because the real HSM
# is not present in mutation-test CI.  These paths have integration tests.
MUTATION_SUBSYSTEM_security_EXCLUDE   := ./internal/signer/kms.go ./internal/signer/pkcs11.go

# validation subsystem
MUTATION_SUBSYSTEM_validation_PACKAGES  := ./internal/cmd/...
MUTATION_SUBSYSTEM_validation_THRESHOLD := 70
MUTATION_SUBSYSTEM_validation_EXCLUDE   :=

# replay subsystem
MUTATION_SUBSYSTEM_replay_PACKAGES  := ./internal/replay/... ./internal/deterministic/...
MUTATION_SUBSYSTEM_replay_THRESHOLD := 70
MUTATION_SUBSYSTEM_replay_EXCLUDE   :=

# serialization subsystem
MUTATION_SUBSYSTEM_serialization_PACKAGES  := ./internal/session/... ./internal/manifest/...
MUTATION_SUBSYSTEM_serialization_THRESHOLD := 70
# Exclude session archive fuzz scaffolding — mutations in stubs are not meaningful.
MUTATION_SUBSYSTEM_serialization_EXCLUDE   :=

# Install gremlins from the pinned version used in CI.
# Pin to a specific version so contributors get the same results locally.
GREMLINS_VERSION ?= v0.5.1

mutation-test-install:
	@echo "Installing gremlins $(GREMLINS_VERSION)..."
	@go install github.com/go-gremlins/gremlins/cmd/gremlins@$(GREMLINS_VERSION)
	@echo "gremlins installed: $$(gremlins --version 2>&1 || true)"

# Run mutation tests interactively.  Produces an HTML report in
# $(MUTATION_REPORT_DIR)/ that can be opened in a browser.
#
# Usage:
#   make mutation-test                  # use default packages and threshold
#   make mutation-test MUTATION_THRESHOLD=60
#   make mutation-test MUTATION_PACKAGES=./internal/session/...
mutation-test: mutation-test-install
	@echo "Running mutation tests..."
	@echo "  Scope     : $(MUTATION_PACKAGES)"
	@echo "  Threshold : $(MUTATION_THRESHOLD)%"
	@echo "  Report    : $(MUTATION_REPORT_DIR)/"
	@mkdir -p "$(MUTATION_REPORT_DIR)"
	@gremlins unleash \
		--threshold=$(MUTATION_THRESHOLD) \
		--output=$(MUTATION_REPORT_DIR)/gremlins-report.json \
		$(MUTATION_PACKAGES) \
		2>&1 | tee "$(MUTATION_REPORT_DIR)/gremlins.log"
	@echo "Mutation test complete.  Report: $(MUTATION_REPORT_DIR)/gremlins-report.json"

# CI-mode mutation test: machine-readable JSON output only, no TTY spinner,
# exits non-zero when the mutation score falls below MUTATION_THRESHOLD.
# Called by .github/workflows/mutation-test.yml.
mutation-test-ci: mutation-test-install
	@mkdir -p "$(MUTATION_REPORT_DIR)"
	gremlins unleash \
		--threshold=$(MUTATION_THRESHOLD) \
		--output=$(MUTATION_REPORT_DIR)/gremlins-report.json \
		--silent \
		$(MUTATION_PACKAGES) \
		2>&1 | tee "$(MUTATION_REPORT_DIR)/gremlins.log"

# Python script used by mutation-test-report.  Using define/endef avoids the
# GNU Make "missing separator" error that occurs when unindented Python code
# appears after a heredoc opener inside a recipe.
define MUTATION_REPORT_SCRIPT
import json, sys, collections

report_path = sys.argv[1]
threshold   = int(sys.argv[2])

with open(report_path) as f:
    data = json.load(f)

total    = data.get("total_mutants", 0)
killed   = data.get("killed",        0)
survived = data.get("survived",      0)
score    = round(killed / total * 100, 1) if total else 0.0

print(f"  Total mutants : {total}")
print(f"  Killed        : {killed}")
print(f"  Survived      : {survived}")
print(f"  Score         : {score}% (threshold: {threshold}%)")
print()

if survived:
    by_pkg = collections.defaultdict(list)
    for m in data.get("mutants", []):
        if m.get("status") == "SURVIVED":
            pkg = m.get("package", "unknown")
            by_pkg[pkg].append(m)
    print("Surviving mutants by package:")
    for pkg in sorted(by_pkg):
        print(f"  {pkg} ({len(by_pkg[pkg])} mutant(s))")
        for m in by_pkg[pkg][:5]:
            print(f"    - {m.get('file','?')}:{m.get('line','?')} - {m.get('mutation_type','?')}")
        if len(by_pkg[pkg]) > 5:
            print(f"    ... and {len(by_pkg[pkg])-5} more (see full report)")

if score < threshold:
    print(f"\nFAIL: mutation score {score}% is below threshold {threshold}%")
    sys.exit(1)
else:
    print(f"\nPASS: mutation score {score}% meets threshold {threshold}%")
endef
export MUTATION_REPORT_SCRIPT

# Generate a human-readable summary from the last JSON report.
# Prints surviving mutants grouped by package so contributors know where to
# add tests.  Requires the JSON report to exist (run mutation-test first).
mutation-test-report:
	@if [ ! -f "$(MUTATION_REPORT_DIR)/gremlins-report.json" ]; then \
		echo "No report found.  Run 'make mutation-test' first."; \
		exit 1; \
	fi
	@echo "=== Mutation Test Summary ==="
	@printf '%s\n' "$$MUTATION_REPORT_SCRIPT" | \
		python3 - "$(MUTATION_REPORT_DIR)/gremlins-report.json" "$(MUTATION_THRESHOLD)"

# Run mutation tests for all subsystems in sequence, enforcing per-subsystem
# thresholds.  Each subsystem writes its report to a separate subdirectory so
# results can be reviewed independently.
#
# Usage:
#   make mutation-test-subsystems
#   make mutation-test-subsystems MUTATION_REPORT_DIR=my-reports
mutation-test-subsystems: mutation-test-install
	@echo "Running per-subsystem mutation tests..."
	@failed=0; \
	for sub in $(MUTATION_SUBSYSTEMS); do \
		pkgs=$$(eval echo "\$$MUTATION_SUBSYSTEM_$${sub}_PACKAGES"); \
		thr=$$(eval echo "\$$MUTATION_SUBSYSTEM_$${sub}_THRESHOLD"); \
		dir="$(MUTATION_REPORT_DIR)/$${sub}"; \
		mkdir -p "$${dir}"; \
		echo ""; \
		echo "── Subsystem: $${sub} (threshold: $${thr}%) ──────────────────"; \
		echo "   Packages : $${pkgs}"; \
		if ! gremlins unleash \
			--threshold=$${thr} \
			--output="$${dir}/gremlins-report.json" \
			$${pkgs} \
			2>&1 | tee "$${dir}/gremlins.log"; then \
			echo "FAIL: $${sub} mutation score below $${thr}%"; \
			failed=1; \
		else \
			echo "PASS: $${sub}"; \
		fi; \
	done; \
	if [ $$failed -ne 0 ]; then \
		echo ""; \
		echo "One or more subsystems failed their mutation threshold."; \
		echo "Run 'make mutation-test-subsystem-report' for a summary."; \
		exit 1; \
	fi; \
	echo ""; \
	echo "All subsystems passed."

# CI variant: machine-readable JSON only, no TTY spinner.
# Called by .github/workflows/mutation-test.yml subsystem matrix jobs.
#
# Required env vars when called from CI:
#   MUTATION_SUBSYSTEM   — one of: security validation replay serialization
mutation-test-ci-subsystems: mutation-test-install
	@sub="$(MUTATION_SUBSYSTEM)"; \
	if [ -z "$$sub" ]; then \
		echo "ERROR: MUTATION_SUBSYSTEM is not set."; \
		echo "Usage: make mutation-test-ci-subsystems MUTATION_SUBSYSTEM=security"; \
		exit 1; \
	fi; \
	pkgs=$$(eval echo "\$$MUTATION_SUBSYSTEM_$${sub}_PACKAGES"); \
	thr=$$(eval echo "\$$MUTATION_SUBSYSTEM_$${sub}_THRESHOLD"); \
	dir="$(MUTATION_REPORT_DIR)/$${sub}"; \
	mkdir -p "$${dir}"; \
	gremlins unleash \
		--threshold=$${thr} \
		--output="$${dir}/gremlins-report.json" \
		--silent \
		$${pkgs} \
		2>&1 | tee "$${dir}/gremlins.log"

# Print a per-subsystem summary table after running mutation-test-subsystems.
mutation-test-subsystem-report:
	@python3 - "$(MUTATION_REPORT_DIR)" "$(MUTATION_SUBSYSTEMS)" <<'PYEOF'
import json, sys, os, collections

report_base  = sys.argv[1]
subsystems   = sys.argv[2].split()

rows = []
for sub in subsystems:
    report_path = os.path.join(report_base, sub, "gremlins-report.json")
    if not os.path.exists(report_path):
        rows.append((sub, "-", "-", "-", "-", "NO REPORT"))
        continue
    with open(report_path) as f:
        data = json.load(f)
    total    = data.get("total_mutants", 0)
    killed   = data.get("killed", 0)
    survived = data.get("survived", 0)
    score    = round(killed / total * 100, 1) if total else 0.0
    status   = "PASS" if score >= 70 else "FAIL"
    rows.append((sub, total, killed, survived, f"{score}%", status))

print(f"{'Subsystem':<16} {'Total':>6} {'Killed':>7} {'Survived':>9} {'Score':>7}  Status")
print("-" * 60)
for row in rows:
    print(f"{row[0]:<16} {str(row[1]):>6} {str(row[2]):>7} {str(row[3]):>9} {str(row[4]):>7}  {row[5]}")
PYEOF

# ──────────────────────────────────────────────
# Dependency Compatibility testing
#
# These targets mirror the steps in .github/workflows/dep-compat.yml and can
# be run locally before or after a dependency bump.
#
# Typical workflow after a planned SDK/host/crypto upgrade:
#   1. make dep-compat-capture-update   # regenerate golden baselines
#   2. git diff internal/depcompat/testdata/golden/
#   3. make dep-compat-compare          # verify no unexpected regressions remain
#   4. Commit the updated baselines in a PR.
# ──────────────────────────────────────────────

# Run the depcompat Go package tests only (fast, no binary required).
dep-compat-test:
	@echo "Running depcompat unit tests..."
	@go test -race -v ./internal/depcompat/...
	@echo "✓ depcompat unit tests passed"

# Capture harness outputs for all dep groups (requires a built binary).
dep-compat-capture:
	@echo "Capturing dep-compat outputs..."
	@bash scripts/dep-compat-capture.sh --output-dir /tmp/depcompat-capture

# Dry-run capture: show what would be executed without running the harness.
dep-compat-capture-dry:
	@bash scripts/dep-compat-capture.sh --dry-run --output-dir /tmp/depcompat-capture-dry

# Capture AND overwrite golden baselines. Review the diff before committing.
dep-compat-capture-update:
	@echo "Capturing and updating golden baselines..."
	@bash scripts/dep-compat-capture.sh \
		--output-dir /tmp/depcompat-capture-update \
		--update-golden
	@echo ""
	@echo "Review the changes with:"
	@echo "  git diff internal/depcompat/testdata/golden/"

# Compare the last capture against golden baselines, emit report to stdout.
dep-compat-compare:
	@echo "Comparing dep-compat outputs against goldens..."
	@bash scripts/dep-compat-compare.sh \
		--captured-dir /tmp/depcompat-capture \
		--fail-on-unexpected
	@echo "✓ dep-compat comparison complete"

# Run capture + compare in a single pass (local full check).
dep-compat: dep-compat-capture dep-compat-compare

# Run the Go-layer report generator against a captured dir.
# Usage: make dep-compat-report CAPTURED_DIR=/tmp/depcompat-capture
dep-compat-report:
	@go run ./cmd/dep-compat-report \
		--captured-dir "$(CAPTURED_DIR)" \
		--output-json "$(or $(REPORT_FILE),/tmp/compat-report.json)" \
		--verbose

.PHONY: dep-compat dep-compat-test dep-compat-capture dep-compat-capture-dry \
        dep-compat-capture-update dep-compat-compare dep-compat-report

# ──────────────────────────────────────────────
# Byte-stable bindings verification
#
# Verifies that generated TypeScript bindings are byte-stable across
# multiple generations. This detects non-determinism in the generation
# process (ordering, line endings, timestamps, etc.).
#
# Usage:
#   make check-bindings-byte-stable
#   make check-bindings-byte-stable BINDINGS_DIR=./src/generated
# ──────────────────────────────────────────────

BINDINGS_DIR ?= ./src/generated
CHECK_BINDINGS_SPEC_FILE ?=
CHECK_BINDINGS_WASM_FILE ?=

check-bindings-byte-stable: build
	@echo "Running byte-stable bindings verification..."
	@if [ -n "$(CHECK_BINDINGS_WASM_FILE)" ]; then \
		./bin/glassbox check-bindings "$(CHECK_BINDINGS_WASM_FILE)" \
			--output "$(BINDINGS_DIR)" --byte-verify; \
	elif [ -n "$(CHECK_BINDINGS_SPEC_FILE)" ]; then \
		./bin/glassbox check-bindings \
			--spec-file "$(CHECK_BINDINGS_SPEC_FILE)" \
			--output "$(BINDINGS_DIR)" --byte-verify; \
	else \
		echo "ERROR: Set CHECK_BINDINGS_WASM_FILE or CHECK_BINDINGS_SPEC_FILE"; \
		echo "Usage: make check-bindings-byte-stable CHECK_BINDINGS_WASM_FILE=contract.wasm"; \
		exit 1; \
	fi
	@echo "✓ Byte-stable bindings verification passed"
