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
20. HR: leave counts inclusive calendar days (Dolibarr: working days per
    country calendar); expense payout posts a real balanced entry via
    POST /hr/expenses/{id}/pay with explicit journal/expense/bank accounts
    (flip-first with best-effort revert bounds double-posting).
21. POS lite: anonymous sales fall back to FERP_POS_WALKIN_ORG when configured
    (else 422); voids are markers; full and partial returns issue validated
    credit notes (applied up to the open balance) and restock tracked goods —
    cash refunds for settled invoices stay out-of-band; multi-tender splits the
    gross across legs in order.
22. Payments: online providers mint intent references without network calls
    until live secrets are configured; settlement is webhook-driven and
    idempotent on terminal status; manual provider settles at intent time.
23. Audit backfill (Phase 18): draft commercial documents support full line
    replacement via PUT (validated docs stay immutable); OIDC SSO verifies
    RS256 id tokens against the realm JWKS (stdlib) with JIT provisioning at
    POST /auth/oidc; OpenAPI now covers every registered route; web UI gained
    POS checkout plus invoice validate/pay actions (full CRUD UI per context
    remains follow-up).
24. Object storage: FERP_STORAGE_BACKEND=s3 selects the stdlib SigV4 S3
    adapter (MinIO path-style); bucket must exist (create `forgeerp` once via
    MinIO console); default stays local dir.
25. Search: FERP_SEARCH_BACKEND=opensearch queries the index with provider
    fallback on any failure; startup reindexes entity 1 plus write-through
    indexing of created orgs/products via the shared bus.
26. Events: one shared bus process-wide (FERP_BUS_BACKEND=nats selects NATS
    JetStream with durable stream, else in-process memory); at-least-once
    delivery — handlers stay idempotent via status guards and row versions.
27. Schema repair (verified against real PostgreSQL): migration 0008's file
    table is `ferp_files` (it duplicated 0005's commercial `ferp_documents`
    and no database could ever have applied it); NULLable code columns read
    back via COALESCE so PG scans never fail on NULL.
28. Multicurrency: board rates (PUT/GET /fx/rates) convert minor units;
    document snapshots (rate_to_base) remain the audit source.
29. Portal-lite: bearer share links (POST /documents/{id}/share, 7-day
    default, 90-day cap) served unauthenticated at /public/share/{token}
    under the same IP rate limit.

30. Prelevement mandates: UMR unique per entity (not global); lifecycle
    draft → signed/active → canceled, amendments keep active with amended_at.
31. R-transactions: validated batch lines stay immutable — R-state lives in
    ferp_sepa_rtransactions (one row per end_to_end_id) with the money unwind
    as a finance reversal; allowed on validated/sent batches only.
32. PaymentByBankTransfer renders simplified pain.001.001.03; int64 minor
    units formatted without floats; BIC optional with NOTPROVIDED fallback.
33. Payroll runs record given gross/charges/net per line (net = gross −
    charges enforced); jurisdictional payroll calculation is never computed
    server-side. Posting writes one balanced entry atomically with the flip.
34. Asset depreciation is straight-line or declining-balance on int64 minor
    units (half-up per period, remainder last); cost basis lives on the
    schedule; disposal posts proceeds vs NBV gain/loss and retires atomically.
35. Intra-EU VAT numbers live in organization custom_fields.vat_number
    (aliases accepted); movements without one are excluded from DEB output.
36. DEB/DES file is a documented fixed-width parity layout; official DGDDI
    schema conformance stays operator-side before filing.
37. Close-preview (GET /reports/close-preview) is a computed, balanced
    proposal and never posts; closing entries post via finance.
38. Period P&L walks posted ledger entries in reporting; TrialBalance carries
    no dates, so finance owns any future date-indexed balance query.
39. Payments live depth: Stripe/PayPal clients degrade to mint-only refs when
    secrets are unset; settlement stays webhook-driven/idempotent; partial
    refunds settle to terminal Refunded; PayPal math is integer-only.
40. Outbound OAuth vault (ferp_oauth_tokens, AES-256-GCM under FERP_OAUTH_KEY):
    single key with no rotation; distinct from inbound OIDC login; no external
    crypto review yet.
41. payments PG hardening: webhook_key inserts '' (was NULLIF violating
    NOT NULL); empty-key lookups never resolve.
42. Accounting bindings replace per-module hardcoded default accounts with one
    explicit ferp_account_bindings table (Phase 3 auto-posting prerequisite).
43. Barcode/QR outputs are validated payload strings + label-sheet JSON;
    image rendering deferred to kernel 5.
44. Member loans use French amortisation (constant annuity, residual
    absorption on the last installment).
45. Donation receipts attest paid donations only (promised → 422).
46. Loan disburse/repay posts money before flipping status (non-atomic across
    ledger/status; status failure after posting surfaces as 500 with entry id).
47. Bank statements import CSV + CAMT.053 with bank-ref dedupe; reconciliation
    matches on amount + date window; transfers post paired transactions.
48. Manufacturing MRP depth: workstations carry daily minute capacity; BOM
    routings schedule MO operations by forward day-bucketing (UTC days);
    overload is flagged, never auto-resolved. Scheduled MOs produce only
    through operation completion; direct produce on a scheduled MO is 422.
49. Receipt reprints resolve labels/prices live from catalog (sales store no
    price snapshot); drawer expectation counts cash-method sales only; X/Z
    totals exclude voided/returned sales; ESC/POS folds to 7-bit ASCII.
50. Offline till replay requires an open session; payloads replayed after
    close flip to failed with the checkout error preserved.
51. ECM folders are unique per (entity, parent, name) with cycle-guarded
    moves; re-upload versions instead of duplicating (restore inserts a new
    version); filing rules map (scope, object_type) to folder templates;
    full-text search indexes text/* + filenames only (no binary parsers).
52. Portal customers are not staff users: opaque org-bound bearers, entity
    resolved from the token (SHA256 salted, not argon2id); cross-customer
    IDs 404, never 403; quotes acceptable only when validated.
53. Cron cron_expr takes precedence when set (empty = legacy interval);
    failed jobs advance schedule without hot-looping and emit a bus alert;
    unregistered codes record as failed runs, never silently.
54. HRM employees are distinct from identity users with no FK (logins are
    auth handles); terminated is terminal via a dedicated endpoint.
55. Generic import/export is adapter-based (members/products/orgs/sales);
    dry-run validates without writing, per-row errors; sales imports one
    single-line document per row, money in minor units.
56. Document lines carry a kind discriminator (normal/section/subtotal,
    Dolibarr `special_code` equivalent): marker lines validate as markers and
    contribute zero, so totals always equal the priced lines; per-subtotal
    block totals are computed, not stored.
57. Incoterms 2020 ship as a seeded code table (any/sea modes); sales
    documents carry one incoterm code (procurement wiring is follow-up work).
58. Inter-warehouse transfers (TRF-YYYYMM-####) validate into paired
    transfer_out/transfer_in movements with a PMP snapshot per line;
    insufficient source stock is 422 with no partial post; validated transfers
    are terminal (reversal is a new transfer, Dolibarr has no equivalent doc).
59. Dynamic price expressions evaluate over base|qty|cost with exact rational
    arithmetic (half-up to minor units, no floats); org-specific assignments
    win over org-0 fallbacks; archived rules are skipped, never deleted.
60. Partnership commissions accrue in basis points on referred sale totals;
    payouts (notes/bank transfers) are explicitly out of scope — no endpoint.
61. Mailing campaigns queue one recipient per address with an unsubscribe
    token; suppressed addresses are excluded from future expansions;
    unsubscribed rows fail closed (never sent, never counted as sent).
62. DataPolicy retention is dry-run-first (missing rule is 404, nothing
    mutates); subjects are projected seams (resigned/excluded members,
    inactive orgs) — erasure requests log intent, anonymisation is per-field.
63. Label sheets render through the docgen registry to deterministic PDFs
    (frozen creation date); Avery 65-up default geometry ships built-in.
64. PORT-LITE tools stay thin by design: bookmarks (idempotent toggle),
    quick memos (per-user), collab (comments only, no co-editing), AI (one
    assist endpoint over a replaceable provider; runs log excerpts only),
    website (read-only published-article API for a static site, no CMS),
    LDAP (read-only sync, refused without FERP_LDAP_URL; Keycloak remains
    the federated path), ModuleBuilder (scaffold + install/uninstall records).
65. Statutory accounting tables ship ahead of the posting engine: per-country
    chart packs (FR PCG25-DEV, DE SKR03, US US-BASE — sibling variants left
    out), idempotent commercial-document postings, close/reopen audit trail.
    Auto-posting and FEC export are follow-up work, not silent omissions.
66. Rights seeding is idempotent by partial unique index: the original
    multi-column UNIQUE never fired on NULL user_id/group_id (PostgreSQL NULL
    semantics), so re-activation duplicated grants until 0046.
67. Dolibarr migration is an offline pipeline (scripts/dolibarr_import):
    CSV dumps → deterministic ferp_*.json with reconciliation counts;
    float money converts via Decimal(str()) so float 1.015 lands on 102.
68. Statutory posting is event-driven and immediate: validating a sales or
    supplier invoice posts the entry at once (DR control / CR revenue / CR
    VAT per rate) through bindings — there is no draft-review step. Replay
    safety comes from the (entity, doc_type, doc_id) idempotency row, and
    review happens after posting (postings list, trial balance, FEC).
    Missing bindings fail closed naming the key; per-line product accounts
    are not resolved (one default revenue account per invoice).
69. Binding resolution tries the specific key then "default" (documented
    convention, not Dolibarr behavior — Dolibarr scatters per-screen
    defaults with no fallback chain).
70. FEC ships the 18 normative columns only (Dolibarr's 3 supplementary
    columns omitted); lettering columns export empty (no entry lettering
    ported); amounts are base-currency only (Montantdevise/Idevise empty).
71. Fiscal-year reopening is allowed with a mandatory audit row (Dolibarr
    has no reopen trail); the close entry id links the close row.

## Deferred scope (post-Phase-12 candidates)

POS (takepos), HR details (holiday/expensereport/salaries),
payment plugins (stripe/paypal), surveys (opensurvey), calendar booking (bookcal),
SOAP API, LDAP sync, DAV/FTP, full i18n (120 langs → English-first + i18n-ready schema).
