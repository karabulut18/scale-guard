# System Criteria & Requirements

This document outlines the technical standards and performance goals for the Distributed Rate Limiter.

## 0. Architectural Decisions (Locked)

### Decision 1: Algorithm — Token Bucket
We use **Token Bucket** (not Leaky Bucket, not Sliding Window).

**Why:** Token Bucket naturally allows controlled bursting, which is the correct behavior for e-commerce traffic (a user adding items to a cart may fire 5 requests in 50ms, then go quiet). Sliding Window has higher memory cost per client at scale. Leaky Bucket enforces a rigid output rate which is too punishing for bursty-but-legitimate traffic.

**Implications:** Each client has a bucket with a `capacity` (max tokens) and a `refill_rate` (tokens/sec). A request consumes 1 token. Tokens are added back at the refill rate up to capacity. If the bucket is empty, the request is denied.

---

### Decision 2: Distributed Sync Model — Local Hot Path + Async Write-Behind
The hot path (every `CheckRateLimit` call) operates entirely on an **in-memory token bucket** per client, with no synchronous DB call.

**Sync strategy:**
- On startup: load all client configs from PostgreSQL into memory.
- Hot path: atomic in-memory token bucket operations only (no DB I/O).
- Write-behind: a background goroutine flushes token counts to PostgreSQL every **100ms** in a single batched `UPDATE`.
- Config refresh: a background goroutine re-reads client configs from PostgreSQL every **30 seconds** (TTL-based), picking up dynamic limit changes without restart.

**Why:** This is the only design that can hit sub-2ms p99 AND use PostgreSQL (not Redis). Any synchronous DB call on the hot path would blow the latency budget under load. The 100ms write-behind introduces a small window of inaccuracy across instances, which is an accepted trade-off for the performance target.

---

### Decision 3: Fail-Open Strategy — Degraded Mode with Last-Known Config
If the PostgreSQL connection is lost:

1. The service continues operating using the **last successfully loaded in-memory state** (last-known config + current token counts).
2. Write-behind flushes are **skipped and discarded** (not queued) to avoid unbounded memory growth.
3. A `CRITICAL` alert is logged on every failed flush and config refresh attempt.
4. When the connection is restored, the service **re-syncs config from DB** and **resumes write-behind**. It does NOT attempt to reconcile the discarded flush window — the small inaccuracy is accepted.

**Why not allow-all unconditionally:** That would make the rate limiter useless during outages, which is the highest-risk time. Last-known config is a much safer fallback.

**Why not queue flushes:** Under a sustained DB outage, an unbounded queue becomes a memory leak. Silent discard with logging is safer for a stateless service.

---

## 1. Functional Requirements
- **Multi-Tenant Support:** The system must support different rate limits for different API keys or Client IDs.
- **Dynamic Configuration:** Limits (e.g., 100 req/sec) must be adjustable via PostgreSQL without restarting the service.
- **Accurate Distribution:** Rate limits must be enforced globally across multiple running instances of the limiter.

## 2. Non-Functional Requirements (Performance & Reliability)
- **Low Latency:** The `CheckRateLimit` operation must complete in under **2ms** (p99).
- **High Throughput:** A single instance should handle **>5,000 requests per second**.
- **Fail-Open Strategy:** If the PostgreSQL connection is lost, the service should default to a "Fail-Open" state to avoid blocking legitimate traffic, while logging a critical alert.
- **Consistency:** Use PostgreSQL's transactional integrity to ensure counter increments are atomic and accurate.

## 3. Engineering Standards
- **Concurrency:** Zero usage of global mutexes where `sync/atomic` or lock-free channels can be used.
- **Observability:** Must provide a health-check endpoint and export metrics for "Allow vs. Deny" counts.
- **Testing:** - 100% coverage on core limiting logic.
    - Integration tests using `Testcontainers` for real PostgreSQL interactions.
    - Race condition detection enabled during all CI/CD builds.