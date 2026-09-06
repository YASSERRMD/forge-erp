# ForgeERP — Intentional Differences vs Dolibarr

Functional modernization, not translation. This log records every deliberate
behavioral or structural deviation so auditors can distinguish design from drift.

## Data model

1. `ferp_*` tables instead of `llx_*`; `BIGINT GENERATED ALWAYS AS IDENTITY`
   instead of `AUTO_INCREMENT`; audit columns
   (`created_at/updated_at/created_by/updated_by/row_version`) on all business tables.
2. Money is `BIGINT` minor units everywhere (never float, never `NUMERIC` in app math).
3. Extrafields EAV tables eliminated: `custom_fields JSONB` per entity with
   context-level validation. Loses per-field SQL indexing; gains schema stability.
4. `element_element` / `element_contact` generic link tables folded into explicit
   `source_type/source_id` lineage columns on documents.
5. Quantities are integer base units (Dolibarr allows fractional quantities).
6. One header table per commercial family group (`ferp_documents` for sales,
   `ferp_supplier_docs` for purchase) discriminated by `type`, instead of one
   table per document type.
7. `categorie_*` per-entity link tables collapsed into `ferp_categories` +
   `ferp_category_links` with a scope discriminator.
8. Multicurrency: rate snapshot `rate_to_base` (×1e6) on each document.

## Behavior

9. Statuses are kernel transition tables; illegal moves are 422 (Dolibarr often
   only hides buttons in UI but allows crafted requests).
10. Validated documents are API-immutable (Dolibarr allows some post-validation edits).
11. Overpayments refused (422); credit-note reversals are explicit future work,
    not silent over-application.
12. Negative stock blocked by default with a per-call override (Dolibarr: global option).
13. Supplier orders above threshold require recorded approval (Dolibarr: free validation).
14. Contract supplier prices enforced exactly when pinned (Dolibarr: informational).
15. Passwords: Argon2id (Dolibarr: salted hash varies by version); login throttling
    5 attempts / 15 min lockout.
16. Scheduler is fixed-interval jobs (Dolibarr cron expressions not ported).
17. Auth: Keycloak OIDC in production; HS256 JWT is dev/test only.
    Realm export ships in deploy/keycloak/realm.json (roles admin/manager/user;
    clients forgeerp-api bearer-only + forgeerp-web public SPA).
18. Services: time books against tasks only when task and project are open;
    messages rejected on closed tickets (Dolibarr permits late comments);
    interventions must pass through in-progress.
19. Manufacturing lite: BOM quantities are integer units (Dolibarr allows
    fractional); produce pre-checks component availability then posts
    consume+produce moves (no cross-table transaction — concurrent producers
    can race, acceptable for lite scope); workstations/MRP scheduling not ported.

## Deferred scope (post-Phase-12 candidates)

POS (takepos), HR details (holiday/expensereport/salaries),
payment plugins (stripe/paypal), surveys (opensurvey), calendar booking (bookcal),
SOAP API, LDAP sync, DAV/FTP, full i18n (120 langs → English-first + i18n-ready schema).
