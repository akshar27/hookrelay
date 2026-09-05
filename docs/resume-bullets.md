# HookRelay — résumé bullets

Every claim traces to code in this repo. Counts are real (test count). Latency /
throughput figures in the design doc are "designed for" targets — leave them out
unless you benchmark.

## Short (one-liner)

- Built **HookRelay**, an outbound-webhook delivery service in Go — Postgres as
  the work queue (`FOR UPDATE SKIP LOCKED` + leases), Standard Webhooks HMAC
  signing, exponential-backoff retries, a per-endpoint circuit breaker, a
  dead-letter path, an SSRF guard, and a server-rendered operator dashboard.

## Standard (2–3 bullets)

- Designed and built a **concurrent webhook delivery engine in Go**: a
  coordinator-free worker pool claims due deliveries from Postgres with
  `SELECT … FOR UPDATE SKIP LOCKED`, takes a lease, and a reaper re-queues any
  a crashed worker left in flight — at-least-once, with a stable `Webhook-Id`
  per delivery so receivers can dedupe. Scales by adding stateless instances.
- Implemented the **reliability layer**: an equal-jitter exponential-backoff
  retry table, a dead-letter path that emits a `delivery.dead` event, a
  per-endpoint **circuit breaker** (closed → open → half-open, one probe per
  cooldown) that keeps one bad consumer from starving the pool, per-endpoint
  token-bucket rate limiting, and 429-aware flow control that doesn't trip the
  breaker.
- Got the **security and operability** details right: Standard Webhooks
  HMAC-SHA256 signing with a timestamp tolerance, AES-256-GCM sealed signing
  secrets, and an **SSRF guard that re-resolves the destination host at delivery
  time** (DNS-rebinding aware). Plus an HTMX + `html/template` dashboard
  (delivery feed, per-attempt timeline, one-click replay, pause / circuit
  reset), Prometheus metrics, embedded goose migrations, ~50 tests
  (`httptest` + testcontainers-go), and CI.

## Talking points (for interviews)

- **Why Postgres instead of a broker:** `SKIP LOCKED` gives correct
  multi-consumer semantics with nothing extra to run; the claim query is the
  documented first bottleneck (`docs/system-design.md §9`) with concrete
  mitigations before you'd reach for Kafka.
- **`attempt_count` charged at claim, refunded on a gate:** bounds retries
  across a crash, but paused / rate-limited deliveries don't burn an attempt.
- **In-process breaker/limiter:** exact for one worker instance, approximate
  across many — the queue stays correctly shared; a periodic DB snapshot feeds
  the dashboard. Deliberate v1 scope cut, Redis in v2.
- **Replay as a new row:** `is_replay` + a partial unique index
  (`WHERE NOT is_replay`) keeps fan-out idempotent while letting an operator
  re-send.
- **sqlc:** typed Go from plain SQL; nullable filter params handled with
  `sqlc.narg` + `::type` casts so Postgres can infer types.
