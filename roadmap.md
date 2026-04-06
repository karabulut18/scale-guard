# Roadmap: Distributed High-Performance Rate Limiter

## Phase 1: Core Engine (The "Systems" Logic)
- [x] **Algorithm Decision:** Token Bucket selected. See `criteria.md §0` for rationale.
- [x] **Sync Model Decision:** Local in-memory hot path + 100ms async write-behind to PostgreSQL.
- [x] **Fail-Open Decision:** Degraded mode using last-known in-memory state, with critical alerts.
- [ ] **Scaffold:** Initialize Go module, directory structure, and `go.mod`.
- [ ] **Implementation:** Build a thread-safe in-memory token bucket using `sync/atomic` (mirroring C++ lock-free patterns).
- [ ] **Unit Testing:** Achieve >90% coverage including race condition detection (`go test -race`).

## Phase 2: Persistence & Distribution (The "Backend" Logic)
- [ ] [cite_start]**Database Schema:** Design a PostgreSQL schema for "Bucket" configurations and "Client" usage stats[cite: 32, 47].
- [ ] **Optimization:** Implement a "Write-Behind" or "Batch Update" strategy to Postgres to avoid hitting the DB for every single request.
- [ ] **Integration:** Connect the Go service to PostgreSQL using `pgx` (high-performance driver).

## Phase 3: Networking & API
- [ ] [cite_start]**GRPC Interface:** Define a `.proto` file for the `CheckRateLimit` service (faster than REST for internal microservices [cite: 22]).
- [ ] **Middleware:** Create a Go middleware that can be easily plugged into any web framework (like Gin or Echo).

## Phase 4: Infrastructure & "The Cart Curt"
- [ ] [cite_start]**Dockerization:** Create a multi-stage Dockerfile to keep the image size minimal[cite: 31].
- [ ] **Orchestration:** Write a basic Helm Chart to deploy the service + PostgreSQL onto a local Kubernetes (Minikube/Kind) cluster.
- [ ] **CI/CD:** Setup a GitHub Action to run tests and build the Docker image automatically on every push.

## Phase 5: Demonstration (The "Recruiter" View)
- [ ] **Benchmarking:** Use `go test -bench` to document the nanoseconds-per-op.
- [ ] **Visuals:** Create a simple Grafana dashboard (or a mock script) showing the rate limiter in action during a "load spike."