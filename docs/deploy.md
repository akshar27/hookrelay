# Deploying HookRelay

One stateless Go binary (HTTP API + dispatcher + worker pool, all in one
process) plus a Postgres database. Migrations run automatically on startup
(`store.Migrate`, before the server starts accepting traffic). Nothing in the
app is host-specific — it's configured entirely through env vars — so any
container platform works. Fly.io is the reference below.

## What you need

- A **Postgres 16** database. [Neon](https://neon.tech) free tier is easiest.
- A strong **admin token** (`openssl rand -base64 32`) — guards every
  admin/query/dashboard route.
- A **secret key** (`openssl rand -base64 32`) — seals endpoint signing
  secrets at rest. Without one the app generates an ephemeral key at boot and
  logs a warning; sealed secrets won't survive a restart, so set a real one.

## Local (Docker Compose)

```bash
docker compose --profile app up --build      # API + dashboard on :8080
```

## Fly.io

```bash
fly launch --no-deploy --copy-config --name hookrelay

# Neon gives you postgresql://USER:PASS@HOST/hookrelay?sslmode=require —
# that's already the form HOOKRELAY_DATABASE_URL wants.
fly secrets set \
  HOOKRELAY_DATABASE_URL="postgres://USER:PASS@HOST/hookrelay?sslmode=require" \
  HOOKRELAY_ADMIN_TOKEN="$(openssl rand -base64 32)" \
  HOOKRELAY_SECRET_KEY="$(openssl rand -base64 32)"

fly deploy
```

`min_machines_running = 0` scales to zero when idle — the first request after
a lull cold-starts in a couple seconds (it's a small static Go binary on
distroless, no JVM/interpreter warmup).

### Smoke test

```bash
BASE=https://hookrelay.fly.dev
ADMIN="Authorization: Bearer <the HOOKRELAY_ADMIN_TOKEN you set>"

curl -s $BASE/healthz                                    # {"status":"ok"}
curl -s $BASE/readyz                                      # checks Postgres

# an endpoint to deliver to, an API key to ingest with (separate from the
# admin token -- ingest is API-key-authed, everything else is admin-authed)
curl -s -XPOST $BASE/v1/endpoints -H "$ADMIN" -H 'content-type: application/json' \
  -d '{"name":"demo","url":"https://webhook.site/<your-bin>"}'
KEY=$(curl -s -XPOST $BASE/v1/api-keys -H "$ADMIN" -H 'content-type: application/json' \
  -d '{"name":"demo"}' | jq -r .api_key)

# send an event, watch it fan out and get delivered
curl -s -XPOST $BASE/v1/events -H "Authorization: Bearer $KEY" \
  -H 'content-type: application/json' \
  -d '{"type":"demo.ping","payload":{"hello":"world"}}'
```

Open `$BASE/?token=<admin token>` for the dashboard, `$BASE/metrics` for
Prometheus.

## Switching hosts

Any container runtime needs: `HOOKRELAY_DATABASE_URL`, `HOOKRELAY_ADMIN_TOKEN`,
`HOOKRELAY_SECRET_KEY`, and `HOOKRELAY_HTTP_ADDR` (defaults to `:8080`; the
app listens on a fixed address, not `$PORT`, so map/forward 8080 on
platforms that don't read `fly.toml`'s `internal_port`). Render / Railway:
point their Docker deploy at this repo's `Dockerfile` and set the same
secrets.

## Notes

- All state is in Postgres — run as many stateless replicas as you like
  behind a load balancer.
- `HOOKRELAY_ALLOW_INSECURE_ENDPOINTS` stays `false` in production — it only
  exists so local dev can point endpoints at `http://localhost:...`.
- The SSRF-guarded dialer (`ssrf.GuardedDialContext`) re-validates the
  resolved IP at connect time, not just at a pre-flight lookup — this matters
  more, not less, once real (attacker-controlled) endpoint URLs are reachable
  from a public deploy.
