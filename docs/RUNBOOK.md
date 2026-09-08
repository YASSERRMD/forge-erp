# ForgeERP Runbook

## Local development

```bash
docker compose up -d postgres        # + redis/nats/minio as needed
cd backend && go test ./... && go run ./cmd/api
# Frontend:
cd frontend && npm install && npm run dev   # vite on :5173, proxies /api to :8080
```

Seed: `FERP_ADMIN_EMAIL` / `FERP_ADMIN_PASSWORD` create the admin on boot;
demo orgs, catalog, sales chain, and chart of accounts seed when tables are empty.

## Configuration (`FERP_*`, env-first; `ferp_config` DB overlay for marked keys)

- `FERP_ENV` (development), `FERP_HTTP_PORT` (8080)
- `FERP_DATABASE_URL` (postgres DSN)
- `FERP_JWT_SECRET` (dev default insecure — override everywhere real)
- `FERP_ADMIN_EMAIL` / `FERP_ADMIN_PASSWORD` (seed gate)
- `FERP_STORAGE_DIR` (./var/docs), `FERP_OIDC_ISSUER/REALM/CLIENT_ID` (Keycloak)
- Storage: `FERP_STORAGE_BACKEND` (dir|s3), `FERP_S3_ENDPOINT/BUCKET/REGION/ACCESS_KEY/SECRET_KEY/SSL`
  (MinIO defaults match compose; create the `forgeerp` bucket once via console)
- Search: `FERP_SEARCH_BACKEND` (memory|opensearch), `FERP_SEARCH_URL/INDEX`
- Events: `FERP_BUS_BACKEND` (memory|nats), `FERP_NATS_URL` (nats://localhost:4222)
- Reminders: `FERP_REMINDER_INTERVAL_S` (300; 0 disables the daemon)
- POS: `FERP_POS_WALKIN_ORG` (org id for anonymous sales; unset = require org)
- Mail: `FERP_SMTP_HOST/PORT/FROM/USER/PASS/TLS` (mailpit defaults: localhost:1025)
- Webhooks: `FERP_STRIPE_WEBHOOK_SECRET` (enables /payments/webhooks/stripe)
- Timeouts: `FERP_READ_TIMEOUT_S` (10), `FERP_WRITE_TIMEOUT_S` (15), `FERP_SHUTDOWN_TIMEOUT_S` (10)

## Fresh-boot guarantee

Every migration is proven against real PostgreSQL (initdb scratch instance):
all `*.up.sql` apply in lexical order with zero errors. The one historical
repair: migration 0008's file table was renamed `ferp_documents` →
`ferp_files` (it collided with the 0005 commercial-documents table and no
database could ever have applied it).

## Health & observability

- `GET /healthz` (liveness), `GET /readyz` (readiness + DB), `GET /metrics` (Prometheus).
- Request IDs on every response; `X-Content-Type-Options`/`X-Frame-Options` set.
- Rate limit: 20 rps / burst 40 per IP on `/api/v1`; login additionally throttled
  per account (5 failures → 15 min lockout).

## Operations

- Migrations run automatically at boot from `backend/migrations` (tracked in
  `ferp_schema_migrations`); down files are manual-recovery only.
- Chain verification: `GET /api/v1/finance/chain-verify?journal_id=N`.
- Trial balance: `GET /api/v1/finance/trial-balance` (assert `balanced: true`).
- Backups: PostgreSQL PITR + MinIO versioning (deployment concern).
- Kubernetes: `helm lint|template deploy/helm/forgeerp`; ArgoCD: `deploy/argocd/application.yaml`.

## E2E validation

```bash
cd backend && go test ./e2e/ -v   # quote-to-cash + procure-to-pay + balanced trial, no services needed
```
