# ForgeERP Architecture

Functional modernization of Dolibarr ERP/CRM into a Go + React/TypeScript
modular monolith. Full Dolibarr analysis:
`.forgeerp-temp/ARCHITECTURE_ANALYSIS.md` (gitignored working copy).

## Bounded contexts (each: `domain.go | service.go | handler.go | repository.go` + migrations + OpenAPI + tests)

| Context | Dir | Dolibarr origin |
|---|---|---|
| platform | `backend/internal/platform` | `main.inc.php`, `conf.php`, `llx_const` |
| health | platform routes | install/admin status |
| identity | `backend/internal/identity` | `user/`, `llx_user*` |
| thirdparty | `backend/internal/thirdparty` | `societe/`, `contact/` |
| catalog | `backend/internal/catalog` | `product/` |
| sales | `backend/internal/sales` | propal (`llx_propal`), `commande` |
| inventory | `backend/internal/inventory` | `stock`, `entrepot`, `fourn`, receptions |
| billing | `backend/internal/billing` | `facture`, `supplier_invoice`, `paiement` |
| accounting | `backend/internal/accounting` | `compta`, `accountancy` |
| documents | `backend/internal/documents` | `ecm/`, doc generators |
| reporting | `backend/internal/reporting` | per-module `stats/`, `cron/` |

## Rules

- Handlers depend on service interfaces; services on repository interfaces.
- No cross-context DB writes. Cross-context via service calls or domain events
  (`platform.Bus`, subjects `forgeerp.<context>.<event>.v1`).
- PostgreSQL is the system of record (`ferp_*` tables; `llx_*` origins noted in
  migration headers). Redis = cache only. MinIO = blobs.
- Every mutating endpoint is permission-gated (`identity` RBAC).
- Status machines with guarded transitions; totals computed server-side;
  ledger entries balanced and immutable once posted.
