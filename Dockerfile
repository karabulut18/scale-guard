# ── Stage 1: Build ────────────────────────────────────────────────────────
# Use the full Go image to compile. This stage never ends up in production.
FROM golang:1.26-alpine AS builder

WORKDIR /app

# Copy dependency files first and download them.
# Docker caches this layer separately — if go.mod/go.sum haven't changed,
# this expensive step is skipped on subsequent builds.
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source and build.
# CGO_ENABLED=0 produces a fully static binary — no libc dependency.
# This is what allows the final image to be scratch/distroless.
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o /scale-guard ./cmd/server

# ── Stage 2: Migrate ──────────────────────────────────────────────────────
# A separate stage for golang-migrate so it doesn't bloat the runtime image.
FROM golang:1.26-alpine AS migrate
RUN go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest

# ── Stage 3: Runtime ──────────────────────────────────────────────────────
# distroless/static has no shell, no package manager, no attack surface.
# It contains only ca-certificates and timezone data — both needed by Go services.
FROM gcr.io/distroless/static:nonroot

# Copy the compiled binary from the builder stage.
COPY --from=builder /scale-guard /scale-guard

# Document the ports this container listens on.
EXPOSE 50051
EXPOSE 8080

ENTRYPOINT ["/scale-guard"]
