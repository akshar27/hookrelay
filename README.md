# HookRelay

An outbound-webhook delivery service. Producers post events; HookRelay fans them
out to subscribed endpoints, signs each payload (Standard Webhooks / HMAC), retries
failures on exponential backoff, isolates slow or dead endpoints with a
per-endpoint circuit breaker, dead-letters what never succeeds, and gives
operators a dashboard to inspect every delivery attempt and replay any of them.

The infrastructure Stripe, GitHub, and Shopify each built internally for their
outbound webhooks — as a self-hostable Go service.

**Status: in build (M1 of 7).** Design: [`docs/system-design.md`](docs/system-design.md) ·
milestones: [`docs/milestones.md`](docs/milestones.md).

## Develop

```bash
make up      # Postgres on host port 5435
make run     # http://localhost:8080  — migrations run on boot
make test    # spins up a throwaway Postgres via testcontainers
```
