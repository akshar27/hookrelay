# HookRelay

A webhook delivery service. Producers post events to HookRelay; it fans them out
to subscribed endpoints, signs each payload, retries failures on an exponential
backoff, isolates slow or dead endpoints with a per-endpoint circuit breaker,
dead-letters what never succeeds, and gives operators a dashboard to inspect
every delivery attempt and replay any of them.

The infrastructure Stripe, GitHub, and Shopify each built internally for their
outbound webhooks — as a self-hostable service.

**Status: in design.** See [`docs/system-design.md`](docs/system-design.md).
