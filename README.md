# scale-guard

A distributed, high-performance rate limiter built in Go and PostgreSQL.
Implements the Token Bucket algorithm with sub-millisecond hot-path latency and async write-behind persistence.

```
docker pull saka14/scale-guard:latest
```

---

## What it does

scale-guard exposes a single gRPC endpoint — `CheckRateLimit` — that consuming services call before processing each request. It answers in under 2ms and never touches the database on the hot path.

```
your service  →  CheckRateLimit(tenant_id, client_id)  →  { allowed: true/false }
```

Limits are configured per `(tenant, client)` pair in PostgreSQL and refreshed in memory every 30 seconds. Token counts are persisted asynchronously every 100ms so state survives restarts.

---

## Architecture

### Why Token Bucket

Token Bucket allows controlled bursting — a user can fire 5 requests in 50ms then go quiet, and both behaviors are handled correctly. Sliding Window has higher memory cost per client at scale. Leaky Bucket enforces a rigid output rate which is too punishing for bursty-but-legitimate e-commerce traffic.

### Three-layer design

```
┌─────────────────────────────────────────────────────────┐
│  gRPC layer  (internal/grpcserver)                      │
│  Validates request, delegates to limiter, maps response │
└───────────────────────┬─────────────────────────────────┘
                        │
┌───────────────────────▼─────────────────────────────────┐
│  Limiter  (internal/limiter)                            │
│  sync.Map of token buckets — zero DB I/O per request    │
│  ├── Write-behind goroutine: flush dirty buckets → DB   │
│  │   every 100ms in a single batched UPSERT             │
│  └── Config-watcher goroutine: reload limits from DB    │
│      every 30s, pick up changes without restart         │
└───────────────────────┬─────────────────────────────────┘
                        │
┌───────────────────────▼─────────────────────────────────┐
│  Token Bucket  (internal/bucket)                        │
│  Per-bucket mutex (not global), lazy refill,            │
│  atomic dirty flag — zero allocations on Allow()        │
└─────────────────────────────────────────────────────────┘
```

### Fail-open degraded mode

If PostgreSQL becomes unreachable:
- The service continues with the last successfully loaded in-memory state
- Write-behind flushes are silently discarded (no retry queue — prevents unbounded memory growth)
- A `CRITICAL` log is emitted on every failed flush and config refresh
- On reconnection, config is reloaded and write-behind resumes

---

## Benchmarks

Measured on Apple M3, Go 1.26, `go test -bench=. -benchmem -benchtime=3s`.

| Operation | ns/op | Allocs |
|---|---|---|
| `Allow` — single goroutine | 67 ns | 0 |
| `Allow` — parallel (GOMAXPROCS) | 154 ns | 0 |
| `Allow` — 10,000 clients in map | 105 ns | 0 |
| `Snapshot` (write-behind cost) | 5 ns | 0 |

**0 heap allocations on the hot path.** No GC pressure regardless of traffic volume.

At 154 ns/op under parallel load, a single instance can sustain ~6.5 million rate-limit checks per second — well above the 5,000 req/sec design target.

---

## Tech stack

| Layer | Technology |
|---|---|
| Language | Go 1.26 |
| Database | PostgreSQL 16 |
| DB driver | pgx/v5 |
| Query generation | sqlc |
| API | gRPC / Protocol Buffers |
| Container | Docker (distroless/static, 16.8 MB image) |
| Orchestration | Kubernetes via Helm |
| Migrations | golang-migrate |
| Testing | testcontainers-go (real PostgreSQL in CI) |

---

## Project structure

```
.
├── cmd/server/          # Entry point — wires config → store → limiter → gRPC
├── internal/
│   ├── bucket/          # Token bucket: core algorithm, benchmarks, unit tests
│   ├── config/          # Environment-based configuration
│   ├── db/              # sqlc-generated query code (DO NOT EDIT)
│   ├── grpcserver/      # gRPC adapter, validation, graceful shutdown
│   ├── limiter/         # Orchestrator: sync.Map, write-behind, config-watcher
│   ├── pb/              # Protobuf-generated code (DO NOT EDIT)
│   └── store/           # Store interface + PostgreSQL implementation
├── db/
│   ├── migrations/      # golang-migrate up/down files
│   ├── queries/         # sqlc input: named SQL queries
│   └── schema.sql       # sqlc input: table definitions
├── proto/               # gRPC service definition
├── helm/scale-guard/    # Kubernetes Helm chart
├── Dockerfile           # Multi-stage build
├── docker-compose.yml   # Local dev: postgres + migrate + scale-guard
└── sqlc.yaml            # sqlc configuration
```

---

## Running locally

**Prerequisites:** Docker, Docker Compose

```bash
# Start PostgreSQL, run migrations, and launch scale-guard
docker compose up

# The gRPC server is now listening on :50051
```

**Run tests:**

```bash
# Unit tests (no Docker required)
go test -race ./internal/bucket/... ./internal/config/... ./internal/limiter/... ./internal/grpcserver/...

# Integration tests (requires Docker for testcontainers)
go test -race -timeout=120s ./internal/store/...

# Benchmarks
go test -bench=. -benchmem ./internal/bucket/... ./internal/limiter/...
```

---

## API

Defined in [proto/ratelimit.proto](proto/ratelimit.proto).

```protobuf
rpc CheckRateLimit(CheckRateLimitRequest) returns (CheckRateLimitResponse);

message CheckRateLimitRequest {
  string tenant_id = 1;
  string client_id = 2;
}

message CheckRateLimitResponse {
  bool   allowed   = 1;
  int64  remaining = 2;  // approximate tokens left
  string reason    = 3;  // set when allowed=false
}
```

**Example with grpcurl:**

```bash
grpcurl -plaintext -d '{"tenant_id": "tenant_storefront", "client_id": "api_key_browse"}' \
  localhost:50051 scaleguard.v1.RateLimitService/CheckRateLimit
```

---

## Configuration

All configuration is via environment variables.

| Variable | Default | Description |
|---|---|---|
| `SCALE_GUARD_DB_URL` | required | PostgreSQL connection string |
| `SCALE_GUARD_GRPC_PORT` | `50051` | gRPC server port |
| `SCALE_GUARD_HEALTH_PORT` | `8080` | Health check port |
| `SCALE_GUARD_FLUSH_INTERVAL` | `100ms` | Write-behind flush frequency |
| `SCALE_GUARD_REFRESH_INTERVAL` | `30s` | Config reload frequency |
| `SCALE_GUARD_INSTANCE_ID` | hostname | Identifier for logs/metrics |
| `SCALE_GUARD_LOG_LEVEL` | `info` | Log level |

---

## Deploying to Kubernetes

```bash
helm install scale-guard helm/scale-guard \
  --set db.url="postgres://user:pass@host:5432/scale_guard?sslmode=require"
```
