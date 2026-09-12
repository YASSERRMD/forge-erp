<div align="center">
  <img src="docs/assets/forgeerp-logo.png" alt="ForgeERP logo" width="200" />
  <h1>ForgeERP</h1>
  <p><strong>Modern open-source ERP &amp; CRM</strong> — a complete functional modernization of Dolibarr, rebuilt as a Go + React modular monolith.</p>
  <p>
    <a href="https://github.com/YASSERRMD/forge-erp/actions/workflows/ci.yml"><img src="https://github.com/YASSERRMD/forge-erp/actions/workflows/ci.yml/badge.svg" alt="CI status" /></a>
    <img src="https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white" alt="Go 1.25" />
    <img src="https://img.shields.io/badge/React-18-61DAFB?logo=react&logoColor=white" alt="React 18" />
    <img src="https://img.shields.io/badge/TypeScript-5-3178C6?logo=typescript&logoColor=white" alt="TypeScript 5" />
    <img src="https://img.shields.io/badge/PostgreSQL-16-336791?logo=postgresql&logoColor=white" alt="PostgreSQL 16" />
    <img src="https://img.shields.io/badge/license-MIT-C5A55A" alt="MIT license" />
  </p>
</div>

## Contents

- [About](#about)
- [Why ForgeERP](#why-forgeerp)
- [Features](#features)
- [Architecture](#architecture)
- [Quickstart](#quickstart)
- [Configuration](#configuration)
- [API](#api)
- [Project structure](#project-structure)
- [Quality gates](#quality-gates)
- [Roadmap](#roadmap)
- [Contributing](#contributing)
- [Acknowledgments](#acknowledgments)
- [License](#license)

## About

ForgeERP takes the battle-tested business logic of [Dolibarr](https://github.com/Dolibarr/dolibarr)
ERP/CRM — quotes, orders, invoices, inventory, manufacturing, HR, projects —
and re-implements it as a clean, typed, observable modern stack. This is a
**functional modernization, not a port**: every workflow was re-analyzed from
the PHP source, redesigned around explicit domain models and server-side state
machines, and verified with characterization tests. Intentional deviations are
logged in [docs/DIFFERENCES.md](docs/DIFFERENCES.md).

> **Experiment note:** this repository was built as an end-to-end experiment in
> autonomous software engineering — designed, implemented, tested, and merged
> through 40+ pull requests by an AI coding agent running in
> [OpenCode](https://opencode.ai), powered by **Meta Muse Spark 1.3**
> (`muse-spark`). The working method (autonomous phases, atomic commits, PR-only
> merges into `main`) is documented in `.forgeerp-temp/MASTER.md`.

## Why ForgeERP

| Dolibarr (legacy) | ForgeERP (this repo) |
|---|---|
| PHP monolith, per-module pages | Go modular monolith, one REST API + React SPA |
| Client-side status handling | Server-side state machines, immutable validated docs |
| `llx_*` schema, MySQL-first | Clean PostgreSQL schema, versioned migrations |
| Built-in password auth | Keycloak OIDC (dev JWT fallback), RBAC matrix |
| Triggers in-process only | Shared event bus, NATS JetStream-ready |
| Local file dirs | S3/MinIO selectable backend |
| No full-text search | OpenSearch with provider fallback |
| TCPDF reports | PDF/CSV exports, P&L, margins, receivables, dashboards |

## Features

**Sell** — organizations, contacts, categories · quotes → orders → shipments →
invoices → payments · credit notes with allocation · POS (multi-tender, walk-in,
full/partial returns, voids) · price lists, variants with EAN-13 barcodes.

**Buy & make** — supplier proposals/orders/receptions/invoices with approval
gates and contract pricing · warehouses, lots, PMP-valued stock ledger · BOMs
and manufacturing orders with ledger-backed produce.

**Money** — double-entry ledger (hash-chained), journals, fiscal years, bank
accounts/transactions/reconciliation, loans, trial balance, P&L, receivables,
per-product margins, intra-EU VAT, SEPA pain.008 batches, FX board, payment
intents + Stripe-style webhooks.

**People & work** — users/groups/RBAC, HR (leave, expenses with ledger payout,
payroll), members/donations, recruitment pipeline, projects/tasks/time,
contracts, interventions, helpdesk tickets + knowledge base, agenda with
reminder daemon, resource booking, events, surveys.

**Platform** — documents with bearer share links, SMTP mail, signed webhooks,
scheduler, CSV import/export, 229-key EN/FR UI, icon sidebar, chart dashboard.

## Architecture

![ForgeERP architecture](docs/assets/forgeerp-architecture.png)

Modular monolith first: 27 bounded contexts (`backend/internal/*`) share one
process and one PostgreSQL database (23 versioned migrations, `ferp_*` tables).
Cross-context communication goes over one shared event bus (in-process today,
NATS JetStream via `FERP_BUS_BACKEND=nats`). MinIO/S3, OpenSearch, Keycloak,
Redis and the OTel stack sit behind env-selected adapters with in-process
fallbacks, so the system boots with just Postgres.

Details: [docs/architecture.md](docs/architecture.md) ·
[backend pattern](docs/backend-pattern.md) · [runbook](docs/RUNBOOK.md) ·
[migration notes](docs/migration-notes.md)

## Quickstart

Prerequisites: Docker, Go 1.25+, Node 20+.

```bash
# 1. Infrastructure (or the full stack: docker compose up -d)
docker compose up -d postgres

# 2. Backend — migrates, seeds, serves on :8080
cd backend && go test ./... && go run ./cmd/api
curl localhost:8080/healthz

# 3. Frontend — Vite dev server on :5173 (proxies /api to :8080)
cd frontend && npm install && npm run dev
```

First boot seeds demo data. Create the admin login:

```bash
FERP_ADMIN_EMAIL=admin@example.com FERP_ADMIN_PASSWORD='change-me-please' go run ./cmd/api
```

Then sign in at http://localhost:5173.

## Configuration

Env-first (`FERP_*`). The complete table lives in [docs/RUNBOOK.md](docs/RUNBOOK.md);
the most-used keys:

| Variable | Default | Purpose |
|---|---|---|
| `FERP_DATABASE_URL` | — | PostgreSQL DSN (required) |
| `FERP_HTTP_PORT` | `8080` | API listen port |
| `FERP_JWT_SECRET` | insecure dev default | Override everywhere real |
| `FERP_ADMIN_EMAIL` / `FERP_ADMIN_PASSWORD` | — | Seeds the admin user |
| `FERP_OIDC_ISSUER` / `REALM` / `CLIENT_ID` | — | Enables Keycloak SSO (`POST /auth/oidc`) |
| `FERP_STORAGE_BACKEND` | `dir` | `dir` or `s3` (MinIO) |
| `FERP_SEARCH_BACKEND` | `memory` | `memory` or `opensearch` |
| `FERP_BUS_BACKEND` | `memory` | `memory` or `nats` |
| `FERP_SMTP_HOST/PORT/FROM` | mailpit defaults | Outbound mail |
| `FERP_POS_WALKIN_ORG` | — | Default org for anonymous till sales |
| `FERP_REMINDER_INTERVAL_S` | `300` | Agenda daemon tick (`0` disables) |
| `FERP_STRIPE_WEBHOOK_SECRET` | — | Enables `/payments/webhooks/stripe` |

## API

The contract is [api/openapi.yaml](api/openapi.yaml) (~150 paths, verified
against the routers in both directions). Auth is a Bearer dev JWT, or a
Keycloak identity via OIDC with JIT provisioning:

```bash
# password login
curl -X POST localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"login":"admin","password":"change-me-please"}'

# authenticated call
curl localhost:8080/api/v1/organizations?limit=50 \
  -H "Authorization: Bearer <access_token>"
```

Interactive exploration: every handler family follows
`domain → store → handler` with RBAC triples (`module.entity.action`),
`422` on illegal state transitions, `409` on row-version conflicts.

## Project structure

```text
backend/cmd/api          server entrypoint (migrations, seeds, routes, daemons)
backend/internal/platform shared kernel (config, pgx pool, migrator, bus, HTTP, metrics)
backend/internal/<ctx>   27 bounded contexts, each domain → store → handler + tests
backend/migrations       0001–0022 versioned SQL, up/down pairs, proven on real PG
backend/e2e              end-to-end chain tests (quote-to-cash, procure-to-pay…)
frontend/src             React SPA: icon sidebar, chart dashboard, 21 pages, EN/FR
api/openapi.yaml         the contract
deploy/                  compose stack, Helm chart, ArgoCD app, Keycloak realm
docs/                    architecture, runbook, differences, migration notes
.forgeerp-temp/          autonomous instruction system (gitignored working state)
```

## Quality gates

Every change lands through a short-lived branch → PR → **full merge commit**
(no squash), with green CI required:

- Backend: `go build`, `go vet`, `go test ./...` (29 packages, incl. `-race` clean)
- Fresh-install proof: migrations apply on empty PostgreSQL; double-boot idempotent
- Frontend: `vitest`, `tsc --noEmit`, production `vite build`
- Spec parity: OpenAPI ↔ router check both directions; i18n key parity EN/FR

## Roadmap

Done: all core ERP domains, POS, manufacturing, HR, reporting, OIDC, billing
integrations, EN/FR UI. Deliberately out of scope: legacy protocols
(SOAP/LDAP/DAV/CMS), Stripe live charging (needs secrets), backend message
translation. See [docs/DIFFERENCES.md](docs/DIFFERENCES.md) for every
intentional deviation and [docs/RUNBOOK.md](docs/RUNBOOK.md) for operations.

## Contributing

1. Create a feature branch (`feat_…`, `fix_…`, `phase_…`) — never commit to `main`.
2. One atomic commit per task (`feat(scope): …`, `test(scope): …`, `docs(scope): …`).
3. Push, open a PR, merge with a **full merge commit**.
4. Keep `go test ./...` and the frontend checks green; update `api/openapi.yaml`
   and `docs/` alongside behavior changes.

## Acknowledgments

- [Dolibarr ERP/CRM](https://github.com/Dolibarr/dolibarr) — the reference
  application this project modernizes; business workflows follow its semantics.
- Built autonomously with [OpenCode](https://opencode.ai) powered by
  **Meta Muse Spark 1.3**.

## License

MIT — see [LICENSE](LICENSE).

---
<div align="center"><sub>Mohamed Yasser | Solutions Architect</sub></div>
