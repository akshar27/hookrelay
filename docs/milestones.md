# HookRelay — Build Milestones

Stack: Go 1.26 · chi · pgx/v5 · sqlc · goose · PostgreSQL 16 · slog ·
prometheus/client_golang · HTMX + `html/template` · testcontainers-go.
Each milestone ends with passing tests and a commit
(author `Akshar Gothi <akshargothi70@gmail.com>`).

- [x] **M1 — Skeleton + ops.** Go module, layered packages
  (`cmd/hookrelay`, `internal/{api,store,config,worker}`), `chi` server, env
  config, `pgxpool`, goose migrations embedded + run on boot, `slog` JSON
  logging with request ids, `/healthz` `/readyz` (DB ping) `/metrics`
  (Prometheus), graceful shutdown, Docker + Compose (Postgres on host 5435),
  `testcontainers-go` helper, GitHub Actions (vet + staticcheck + test). One
  `GET /v1/ping` that round-trips to Postgres.

- [x] **M2 — API keys, endpoints, SSRF guard.** Migration `0002`: `api_keys`,
  `endpoints` (0001 is the baseline). sqlc wired. Auth middleware: API key (`sha256` lookup by prefix)
  for `/v1/events`, admin bearer for the rest. Endpoint CRUD with generated
  signing secret (AES-GCM sealed via `HOOKRELAY_SECRET_KEY`), compiled
  event-type filter, `rate_limit_rps` / `timeout_ms` / `max_attempts`. **SSRF
  guard at create time** (parse URL, reject private/link-local/metadata IP
  literals; require https unless opted out). Table-driven tests for the filter
  matcher and the SSRF classifier.

- [ ] **M3 — Event ingest + idempotent fan-out.** Migration `0003`: `events`,
  `deliveries`. `POST /v1/events` → insert + `202`; dedupe on
  `(api_key_id, idempotency_key)` → `200 {deduped:true}`; 256 KB payload cap.
  Dispatcher: in-process channel of new event ids + a 30 s safety sweep of
  `fanned_out=false`; each event expands via
  `INSERT INTO deliveries … SELECT … FROM endpoints WHERE match(filter,type)
  ON CONFLICT (event_id,endpoint_id) DO NOTHING`, then `fanned_out=true`.
  Tests: dedupe, fan-out only to matching enabled endpoints, fan-out idempotent
  under double-processing.

- [ ] **M4 — Worker pool, delivery, retry, dead-letter.** Migration `0004`:
  `attempts`. Claim query (`status IN ('pending','failed') AND next_attempt_at
  <= now()`, `FOR UPDATE SKIP LOCKED`, lease via `locked_until` +
  `attempt_count += 1` at claim). Delivery pipeline: resolve host, Standard
  Webhooks signing (`Webhook-Id/Timestamp/Signature`), POST with
  `timeout_ms`, record `attempts` row + next state in one txn. Backoff table
  with equal jitter; `dead` after `max_attempts` → emits a `delivery.dead`
  event. Lease reaper flips stale `delivering` → `failed` (`worker_lost`).
  Tests against an `httptest.Server`: 2xx→succeeded, 5xx→retry schedule,
  timeout→retry, exhausted→dead, crash (reaper) → re-queued, signature verifies.

- [ ] **M5 — Circuit breaker + rate limit + delivery-time SSRF.** In-process
  `map[endpointID]*Breaker` (closed→open→half-open, one probe per cooldown) and
  `map[endpointID]*rate.Limiter` token buckets; breaker state snapshotted to
  `endpoints` every 5 s + on transition. Delivery-time DNS re-resolution + IP
  re-check (DNS-rebinding defence). 429 honours `Retry-After` and does not trip
  the breaker; 4xx capped at `max_4xx_attempts`. Tests: breaker opens after N
  fails / half-open probe closes or re-opens / blocked deliveries carry no
  attempt row; limiter reschedules without an attempt; rebinding IP is blocked.

- [ ] **M6 — Query API + replay + endpoint ops.** `GET /v1/deliveries`
  (filters + keyset cursor), `GET /v1/deliveries/{id}` (full attempt timeline),
  `GET /v1/events{,/{id}}`, `POST /v1/deliveries/{id}/replay` (new row),
  `POST /v1/events/{id}/replay` (re-fan-out to current endpoints),
  `POST /v1/endpoints/{id}/rotate-secret`, `.../reset-breaker`, `PATCH` status
  (`paused`). Endpoint health summary (24 h success rate, p95 attempt latency,
  circuit state). Tests for pagination, both replay paths, rotate-secret.

- [ ] **M7 — Dashboard + metrics + polish.** HTMX + `html/template` pages:
  deliveries feed with filters, per-delivery attempt timeline, endpoint health
  board (circuit state, success rate, p95), replay button, pause/resume, secret
  rotation. Prometheus metrics (deliveries by status, attempt-latency
  histogram, circuit-state gauge, queue depth gauge). Multi-stage Dockerfile
  (single static binary + templates), `docker compose up`. README (architecture
  mermaid, quickstart, screenshots/gif, design decisions & tradeoffs,
  out-of-scope, deploy). `docs/resume-bullets.md`. CI builds the image.
