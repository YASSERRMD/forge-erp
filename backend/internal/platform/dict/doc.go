// Package dict is the Kernel 7 reference-dictionary store (Parity Build
// Order Phase 1): a generic port of Dolibarr's 115 llx_c_* tables into
// ferp_dictionaries + ferp_dictionary_entries (migration 0028), seeded by
// migration 0029 from the vendored Dolibarr install data.
//
// Tenancy decision: dictionaries are GLOBAL shared data. The tables carry
// no entity_id (deliberately outside the 0024 RLS tenant boundary, like
// ferp_entities itself): one country list, one currency list, one VAT table
// serves every tenant. Per-locale display names ride in
// Entry.LocaleOverrides (locale -> label, merged by Store.List); genuinely
// tenant-specific extensions belong in the owning context's tables, not here.
// Writes are admin-only; that gate lives at the handler layer (identity
// roles), not in this store.
//
// Precision rule: no floats. VAT-style rates are stored as integer basis
// points in extra["rate_bps"] (percent * 100, so 20% -> 2000, 5.5% -> 550).
// Legacy non-numeric taux values (e.g. "TPS95") leave rate_bps absent and
// keep the verbatim text in extra["rate_text"]. All other per-dictionary
// columns (ISO codes, payment-term days, unit scale, ...) ride in extra as
// JSON numbers/strings/booleans.
//
// Core protection: every seed row is core (is_core = TRUE, and
// extra["core"] = "true" as a SQL-visible marker). Store.DeleteEntry only
// deactivates (sets active = FALSE); Store.HardDeleteEntry refuses core
// rows with a platform.ErrValidation wrap and physically deletes non-core
// rows only. Entry.IsCore is immutable via UpdateEntry.
package dict
