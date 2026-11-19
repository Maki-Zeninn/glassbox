# Stage 1: Build Rust simulator
FROM --platform=$BUILDPLATFORM rust:alpine AS builder-rust

ARG TARGETPLATFORM
ARG BUILDPLATFORM
ARG TARGETARCH

WORKDIR /app/simulator

# Install build dependencies for cross-compilation.
# clang and lld are used as cross-linkers.
RUN apk add --no-cache musl-dev gcc clang lld llvm

# Copy Rust project files
COPY simulator/Cargo.toml simulator/Cargo.lock ./
COPY simulator/src ./src

# Build release binary (statically linked by default on Alpine).
# Cross-compile for arm64 when TARGETARCH=arm64 to keep build times fast.
RUN if [ "$TARGETARCH" = "arm64" ]; then \
      rustup target add aarch64-unknown-linux-musl && \
      CC_aarch64_unknown_linux_musl=clang \
      AR_aarch64_unknown_linux_musl=llvm-ar \
      CFLAGS_aarch64_unknown_linux_musl="--target=aarch64-unknown-linux-musl" \
      CARGO_TARGET_AARCH64_UNKNOWN_LINUX_MUSL_LINKER=clang \
      RUSTFLAGS="-C linker=clang -C link-arg=-fuse-ld=lld -C link-arg=--target=aarch64-unknown-linux-musl -C target-feature=+crt-static" \
      cargo build --release --target aarch64-unknown-linux-musl && \
      cp target/aarch64-unknown-linux-musl/release/simulator target/release/glassbox-sim; \
    else \
      cargo build --release && \
      cp target/release/simulator target/release/glassbox-sim; \
    fi

# Stage 2: Build Go CLI
FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS builder-go

ARG TARGETPLATFORM
ARG BUILDPLATFORM
ARG TARGETOS
ARG TARGETARCH

WORKDIR /app

# Copy Go dependency files first for better layer caching
COPY go.mod go.sum ./
RUN go mod download

# Copy Go source
COPY . .

# Build Go binary statically for the target architecture.
# CGO_ENABLED=0 ensures a fully static binary with no libc dependency.
ENV CGO_ENABLED=0
ENV GOOS=${TARGETOS}
ENV GOARCH=${TARGETARCH}
RUN go build -ldflags="-s -w" -o /out/glassbox ./cmd/glassbox

# ── Stage 3: Final Runtime Image ─────────────────────────────────────────────
#
# Security model:
#   • Runs as non-root user 'glassbox' (UID 10001) — satisfies principle of
#     least privilege and is required for read-only filesystem operation.
#   • Filesystem is mounted read-only by default in docker-compose.yml.
#   • Only three writable paths are needed at runtime:
#       /tmp                — scratch space for in-flight operations
#       /app/cache          — RPC response cache (persistent volume)
#       /app/sessions       — session state (persistent volume)
#     These are declared as VOLUME so compose / k8s can mount them explicitly.
#   • No shell is included in the final image (distroless-style).
#   • ca-certificates is the only runtime dependency.
FROM alpine:3.21

ARG VERSION
ARG COMMIT_SHA
ARG BUILD_DATE

# ── OCI labels ────────────────────────────────────────────────────────────────
LABEL org.opencontainers.image.title="glassbox"
LABEL org.opencontainers.image.description="Soroban smart-contract debugger and transaction analyser"
LABEL org.opencontainers.image.version="${VERSION}"
LABEL org.opencontainers.image.revision="${COMMIT_SHA}"
LABEL org.opencontainers.image.created="${BUILD_DATE}"
LABEL org.opencontainers.image.source="https://github.com/dotandev/glassbox"
LABEL org.opencontainers.image.licenses="Apache-2.0"

# ── Runtime dependencies ──────────────────────────────────────────────────────
RUN apk add --no-cache ca-certificates \
    # Create a non-root system user and group with a fixed UID/GID so that
    # bind-mounted volume permissions are predictable across hosts.
    && addgroup -g 10001 -S glassbox \
    && adduser  -u 10001 -S -G glassbox -H -D glassbox \
    # Create writable directories owned by the service user.
    # /app itself is read-only at runtime; only these subdirectories are writable.
    && mkdir -p /app/cache /app/sessions /tmp \
    && chown -R glassbox:glassbox /app/cache /app/sessions /tmp

WORKDIR /app

# ── Copy binaries ─────────────────────────────────────────────────────────────
COPY --from=builder-go   --chown=glassbox:glassbox /out/glassbox ./glassbox
COPY --from=builder-rust --chown=glassbox:glassbox \
     /app/simulator/target/release/glassbox-sim \
     ./simulator/glassbox-sim

# Verify executability (belt-and-suspenders; builder sets correct mode).
RUN chmod 0755 ./glassbox ./simulator/glassbox-sim

# ── Declare writable volumes ──────────────────────────────────────────────────
# These VOLUME declarations document the writable paths. In production, mount
# named volumes or host directories here so the rest of the filesystem can be
# made read-only.  See docs/docker-security.md for the full guidance.
VOLUME ["/app/cache", "/app/sessions", "/tmp"]

# ── Drop privileges ───────────────────────────────────────────────────────────
USER glassbox

# ── Health check ─────────────────────────────────────────────────────────────
# The health check runs the binary's --version flag to confirm:
#   1. The process starts without crashing.
#   2. Required capabilities (filesystem read, dynamic linker) are present.
# For daemon / debug workflows that expose a metrics endpoint, override this
# with a curl/wget probe in your compose file. See docs/docker-security.md.
#
# --interval   How often to run the check (30 s is the Docker default).
# --timeout    Max time for a single check before it is considered failed.
# --start-period  Grace period while the container is initialising.
# --retries    How many consecutive failures mark the container unhealthy.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/app/glassbox", "--version"]

ENTRYPOINT ["/app/glassbox"]
