# ForgeERP Operations (Phase 6 operational parity)

How to upgrade, roll back, back up, share data across entities, and what
"fast enough" means — with the commands to reproduce every claim.

## 1. Upgrade path

Migrations are forward-only, lexical order, applied at boot by
`platform.Migrate` from the embedded `backend/migrations/*.up.sql`.
`*.down.sql` files are manual-recovery only (never applied at runtime).

**Pre-flight.** Every boot logs the schema-vs-binary report and the
loopback admin listener serves it live:

```bash
curl localhost:$FERP_ADMIN_PORT/preflight
# {"Applied":[...],"Embedded":[...],"Missing":[],"Extra":[],"Gaps":[],
#  "BelowMinimum":false,"Ready":true}
```

| Report state | Meaning | Operator action |
|---|---|---|
| `Ready:true` | history matches the binary | none |
| `Missing` non-empty | DB lags the binary | normal: boot applies them in order |
| `Extra` non-empty | DB newer than binary (or foreign migration) | unsupported: upgrade the binary first; booting continues with a loud log so rollback boots are never stranded |
| `Gaps` non-empty | out-of-order / hand-edited history | reconcile by hand, then boot |
| `BelowMinimum:true` | predates `MinSupportedVersion` | rebuild or migrate by hand |

**Supported matrix.** Upgrades roll forward from `MinSupportedVersion`
(`0001_platform`) through the current embedded head. Downgrades are not
supported: restore from backup instead (section 3).

**Rollback.** There is no automated down-migration. To roll back release N:

```bash
# 1. fresh backup of the current (broken) state for forensics
scripts/backup.sh backup --db $FERP_DATABASE_URL --docs $FERP_STORAGE_DIR --out /tmp/pre-rollback
# 2. restore the last pre-upgrade backup
scripts/backup.sh restore --db $FERP_DATABASE_URL --docs $FERP_STORAGE_DIR --in /backups/forgeerp-<date>
# 3. boot the previous binary (migrations only roll forward; the restored
#    history matches it, so preflight reports Ready)
```

## 2. Backup and restore

A system is **two** pieces that must move together: PostgreSQL (all
`ferp_*` tables) plus the document store (`FERP_STORAGE_DIR`, default
`./var/docs`; S3 deployments replicate the bucket instead). Restoring
only one leaves dangling share links or orphaned blobs.

```bash
scripts/backup.sh backup --db $FERP_DATABASE_URL --docs $FERP_STORAGE_DIR --out /backups/forgeerp-20260918
# -> forgeerp-20260918.{dump,docs.tgz,manifest.json}
scripts/backup.sh restore --db $FERP_DATABASE_URL --docs $FERP_STORAGE_DIR --in /backups/forgeerp-20260918
```

Restore wipes schema content (`DROP ... CASCADE`) so the result is whole,
never partial; the manifest records table/blob counts for the
reconciliation check. Verified 2026-09-18: backup of a migrated+seeded DB
(136 tables) → restore into an empty database → API boots, seeded admin
logs in, migration history intact (47 versions).

Schedule: daily `backup` off-host; restore-drill quarterly (the drill
above is the procedure).

## 3. Multi-entity data sharing

Every tenant row carries `entity_id` (Dolibarr `entity` equivalent,
default 1); cross-entity reads are 404-shaped, enforced per query plus a
CI guard rejecting tenant-less `WHERE id=$N` lookups. Sharing semantics
per table class (audited from `backend/migrations/*.up.sql`):

| Class | Tables | Rule |
|---|---|---|
| Tenant roots | `ferp_entities` | the tenants themselves; PK lookup only |
| Tenant rows | everything with `entity_id` (orgs, docs, entries, …) | strictly isolated per entity |
| Child rows | `ferp_doc_lines`, `ferp_entry_lines`, `ferp_stock_levels`, allocations, loan lines, file versions, transfer lines | inherit isolation from the parent (FK); no direct cross-tenant path |
| Indirect rows | `ferp_sessions` (via user), `ferp_group_members` (via group), `ferp_outbox` (relay drains per payload entity) | isolated through the parent |
| Global reference | `ferp_dictionaries`, `ferp_dictionary_entries`, `ferp_incoterms` | shared by design: one country/VAT/Incoterms list for every tenant, with per-locale display overrides |

There is no per-object-type sharing matrix (Dolibarr multicompany
"shared vs isolated" toggles): isolation is the only mode. Cross-entity
consolidation (group reporting) is future work, not silent behavior.

## 4. Performance baseline

Method: `go test -bench` on fixed fixtures (sizes in the bench names),
local Apple M4 against localhost PostgreSQL 16, 2026-09-18:

| Benchmark | Dataset | Measured |
|---|---|---|
| `partners.BenchmarkPGListOrgs` | 200 orgs, page 50 | ~181 µs/op |
| `partners.BenchmarkOrgListHTTP` | 200 orgs, page 50, chi+JSON, memory store | ~79 µs/op |
| `finance.BenchmarkPGTrialBalance` | 100 entries / 200 legs | ~76 µs/op |

Targets (dev-machine means above leave 10–100× headroom; production
numbers must be re-measured on production hardware at
`perfseed.Baseline()` scale: 10 orgs × 1k products × 500 docs):

| Endpoint class | p99 target |
|---|---|
| Master-data reads (orgs, products, dictionaries) | < 200 ms |
| Transactional writes (doc validate, post entry) | < 500 ms |
| Reporting reads (trial balance, ledger book, FEC) | < 2 s |

Reproduce: `TEST_DATABASE_URL=... go test ./internal/partners/ ./internal/finance/ -run XXX -bench .`
(CI runs benches compile-only via `go vet`; timings gate nothing —
regression hunting is manual until targets are production-measured.)

Known limits (not fixed this phase): the `Require` auth middleware costs
~2 queries per request (user + rights load, no caching); local full-suite
runs need `-p 4` (parallel per-package schema migration exhausts a
default-tuned PG's shared memory — environmental, CI unaffected).
