# syntax=docker/dockerfile:1

# Base images are pinned by digest for reproducible builds; Dependabot
# (docker ecosystem, /ci) bumps tag and digest together.

# Stage 1: build the frontend with bun.
FROM oven/bun:1.4-alpine@sha256:da816670775f4c2b4e990b6b1847cf5540dcdc721a42033dd331ce7a88ba6a3b AS frontend
WORKDIR /app
COPY package.json bun.lock turbo.json ./
COPY web/package.json ./web/
RUN bun install --frozen-lockfile
COPY web ./web
RUN cd web && bun run build

# Stage 2: build the Go binary. Runs on the build host's architecture and
# cross-compiles, so multi-platform builds don't need emulation.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine@sha256:70bd7ddea8b6cbe5d6943cde279d6b67feb10cc924fb9100d1d8757c1575b280 AS backend
ARG TARGETOS
ARG TARGETARCH
WORKDIR /app
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY sql ./sql
# -trimpath and -buildvcs=false keep the binary reproducible: no build-host
# paths, and no VCS stamp (.git is excluded from the build context).
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -buildvcs=false -ldflags="-s -w" -o /out/server ./cmd/server

# Stage 3: runtime. distroless/static ships CA certificates (pgx TLS to Neon)
# and tzdata with no shell or package manager, and runs as uid 65532.
FROM gcr.io/distroless/static-debian13:nonroot@sha256:8ce6dd87e9823c5b48dcfb2c8604714ccb351ce59607327bcc4957448da279b5
WORKDIR /app
COPY --from=backend  /out/server         /app/server
COPY --from=backend  /app/sql/migrations /app/sql/migrations
COPY --from=frontend /app/web/dist       /app/web/dist
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/app/server"]
