# ForgeERP

Functional modernization of Dolibarr ERP & CRM (reference clone in `.forgeerp-temp/dolibarr/`,
gitignored autonomous workspace) into a Go + React/TypeScript modular monolith.

## Stack

Go (chi, pgx) · React + TypeScript · PostgreSQL 16 · Redis · NATS JetStream · MinIO ·
OpenSearch · Keycloak (OIDC) · REST + OpenAPI · OTel/Prometheus/Grafana/Loki/Tempo ·
Docker/Kubernetes/Helm/Argo CD.

## Quickstart

```bash
docker compose up -d postgres            # or the full stack: docker compose up -d
cd backend && go test ./... && go run ./cmd/api
curl localhost:8080/healthz
```

Config is env-first (`FERP_*` — see `backend/internal/platform/config.go`).
Seed admin: `FERP_ADMIN_EMAIL` / `FERP_ADMIN_PASSWORD` (Phase 03).

## Layout

- `backend/cmd/api` — server entrypoint
- `backend/internal/platform` — shared kernel (config, pgx pool, migrations runner, event bus, HTTP)
- `backend/internal/<context>` — bounded contexts (identity, partners, catalog, sales, …)
- `backend/migrations` — canonical SQL history (`ferp_*`; Dolibarr `llx_*` origins in file headers)
- `api/openapi.yaml` — authoritative REST surface
- `.forgeerp-temp/` — autonomous instruction system (MASTER, STATUS, analysis, plan, phases)

## Working agreements

`MASTER.md` (in `.forgeerp-temp/`) is authoritative for architecture, migration, testing,
and Git rules: phase branches, atomic commits, PRs with full merge commits, never squash.
