# Stage 1: build the frontend with bun.
FROM oven/bun:1.3-alpine AS frontend
WORKDIR /app
COPY package.json bun.lock turbo.json ./
COPY web/package.json ./web/
RUN bun install --frozen-lockfile
COPY web ./web
RUN cd web && bun run build

# Stage 2: build the Go binary.
FROM golang:1.26-alpine AS backend
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY sql ./sql
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/server ./cmd/server

# Stage 3: runtime. Alpine (not scratch) for ca-certificates so pgx can TLS to Neon.
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=backend  /out/server         /app/server
COPY --from=backend  /app/sql/migrations /app/sql/migrations
COPY --from=frontend /app/web/dist       /app/web/dist
EXPOSE 8080
ENTRYPOINT ["/app/server"]
