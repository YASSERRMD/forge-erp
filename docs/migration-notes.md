# ForgeERP Migration Notes (intentional Dolibarr divergences)

Log every deliberate behavioural/schema difference here with rationale.

## Platform

- Health surface is `/healthz` (liveness) + `/readyz` (readiness, optional DB
  gate) instead of Dolibarr's admin status pages and instead of the
  phase-02 draft's `/api/v1/status`. Rationale: Kubernetes-native probes;
  no information loss (version/commit in body).
- Table prefix `ferp_*` instead of Dolibarr `llx_*`. Origin table noted in each
  migration file header.
- Multi-company `entity` int column → explicit `ferp_entities` table + scoping.
  Rationale: referential integrity, clearer tenancy seam.
- Triggers (synchronous PHP) → domain events on `platform.Bus`
  (`forgeerp.<ctx>.<event>.v1`); NATS JetStream adapter in Phase 10.
- `conf.php` + `llx_const` → env-first `FERP_*` config with `ferp_config` overlay.

## Identity (Phase 03 and onward — appended as built)
