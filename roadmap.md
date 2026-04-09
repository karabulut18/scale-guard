# Roadmap: scale-guard

## Phase 1: Core Engine
- [x] Algorithm decision: Token Bucket — controlled bursting for bursty-but-legitimate traffic
- [x] Sync model decision: in-memory hot path + 100ms async write-behind to PostgreSQL
- [x] Fail-open decision: degraded mode with last-known state, CRITICAL alerts, no retry queue
- [x] Go module and project scaffold (`cmd/`, `internal/`, `db/`)
- [x] Thread-safe token bucket — per-bucket mutex, lazy refill, atomic dirty flag
- [x] 8 unit tests passing with `-race` detector clean

## Phase 2: Persistence & Distribution
- [x] PostgreSQL schema — `rate_limit_configs` and `bucket_states` tables, multi-tenancy via `(tenant_id, client_id)`, FK cascade, seed data
- [x] golang-migrate compatible up/down migrations
- [x] sqlc — SQL queries in `.sql` files, type-safe Go code generated, no inline query strings
- [x] Store interface + pgx/v5 PostgreSQL implementation — batched UPSERT write-behind
- [x] Limiter orchestrator — `sync.Map` hot path, write-behind goroutine (100ms), config-watcher goroutine (30s), fail-open degraded mode, atomic allow/deny metrics
- [x] 23 unit tests passing with `-race` detector clean
- [x] Integration tests against real PostgreSQL via testcontainers

## Phase 3: Networking & API
- [x] `proto/ratelimit.proto` — `CheckRateLimit` RPC definition
- [x] Protobuf + gRPC Go code generated (`internal/pb/`)
- [x] gRPC server — thin adapter over limiter, input validation, logging interceptor, graceful shutdown
- [x] `cmd/server/main.go` — fully wired entry point with signal-based graceful shutdown
- [x] 5 gRPC server tests passing with `-race` detector clean

## Phase 4: Infrastructure
- [x] Multi-stage Dockerfile — `golang:1.26-alpine` builder + `distroless/static` runtime, 16.8 MB final image
- [x] `docker-compose.yml` — postgres + migrate + scale-guard with health checks and startup ordering
- [x] Helm chart — Deployment, Service, Secret, PodDisruptionBudget
- [x] GitHub Actions CI — unit tests, integration tests, sqlc drift check, golangci-lint, helm lint
- [x] GitHub Actions release — multi-platform Docker build (amd64 + arm64), semver tagging
- [x] Docker image published: `saka14/scale-guard:0.1.0`

## Phase 5: Benchmarks
- [x] Bucket benchmarks: 67 ns/op single-goroutine, 154 ns/op parallel, 0 allocations on hot path
- [x] Limiter benchmarks: near-constant sync.Map lookup from 1 to 10,000 clients
- [x] Requirement verified: >5,000 req/sec target exceeded by ~3 orders of magnitude

## Possible future work
- [ ] Health check HTTP endpoint (`/healthz`, `/readyz`) on port 8080
- [ ] Prometheus metrics endpoint — expose allow/deny counters, flush latency
- [ ] Grafana dashboard — visualize traffic spikes and rate-limit enforcement
- [ ] `remaining` field in `CheckRateLimitResponse` — expose token count to callers for backoff
- [ ] Admin API — add/update/delete rate-limit configs without direct DB access
- [ ] Multi-instance accuracy — explore distributed token sync via PostgreSQL advisory locks or Redis for stricter global enforcement
