// Package upgrade provides pre-flight compatibility checks for operator-driven
// upgrades. It reads the applied history from ferp_schema_migrations, compares
// it against the embedded canonical history (backend/migrations via
// migrations.FS), and reports missing/extra versions plus whether the database
// predates the minimum supported version.
//
// This package never applies migrations and never touches down files at
// runtime: platform.Migrate owns apply; *.down.sql files are manual-recovery
// only (see the rollback story in docs/OPERATIONS.md).
package upgrade

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// MinSupportedVersion is the oldest database version this binary can upgrade
// from. A database whose applied history starts (or maxes out) below this
// version must be rebuilt or migrated by hand — the migrator only knows how
// to roll forward from here.
const MinSupportedVersion = "0001_platform"

// Report is the outcome of a pre-flight compatibility check.
type Report struct {
	// Embedded is the canonical *.up.sql version list, lexical order.
	Embedded []string
	// Applied is the ferp_schema_migrations version list, lexical order.
	Applied []string
	// Missing are embedded versions not yet applied (normal when the DB lags
	// the binary; the migrator will apply them in order).
	Missing []string
	// Extra are applied versions unknown to this binary (the DB is newer than
	// the code, or a foreign migration ran). Upgrading with Extra non-empty
	// is unsupported — downgrade the database first or upgrade the binary.
	Extra []string
	// Gaps are applied versions skipped between the lowest and highest
	// applied version (out-of-order or hand-edited history). An empty Gaps
	// with non-empty Missing means a clean lagging database.
	Gaps []string
	// BelowMinimum is true when the database predates MinSupportedVersion
	// (its lowest applied version sorts below it, or it is fresh/empty).
	BelowMinimum bool
	// Ready is true when the upgrade path is supported: no Extra versions,
	// no Gaps, and not BelowMinimum. Missing versions are fine — that is
	// exactly what a forward upgrade applies.
	Ready bool
}

// EmbeddedVersions lists the canonical migration versions (*.up.sql basenames
// minus the suffix) in lexical order from an embedded FS such as
// migrations.FS.
func EmbeddedVersions(files fs.FS) ([]string, error) {
	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		return nil, fmt.Errorf("upgrade: read migrations: %w", err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".up.sql") {
			out = append(out, strings.TrimSuffix(e.Name(), ".up.sql"))
		}
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil, fmt.Errorf("upgrade: no *.up.sql files embedded")
	}
	return out, nil
}

// Check compares an applied history against the canonical embedded history.
// Both slices may be unsorted and may contain duplicates; inputs are not
// mutated. Fresh databases pass applied=nil (BelowMinimum=true, Ready=false —
// the operator story for a fresh database is install, not upgrade).
func Check(applied, embedded []string) Report {
	appliedSet := map[string]bool{}
	for _, v := range applied {
		appliedSet[v] = true
	}
	embeddedSet := map[string]bool{}
	for _, v := range embedded {
		embeddedSet[v] = true
	}

	rep := Report{}
	for v := range appliedSet {
		rep.Applied = append(rep.Applied, v)
	}
	sort.Strings(rep.Applied)
	rep.Embedded = append(rep.Embedded, embedded...)
	sort.Strings(rep.Embedded)

	for _, v := range rep.Embedded {
		if !appliedSet[v] {
			rep.Missing = append(rep.Missing, v)
		}
	}
	for _, v := range rep.Applied {
		if !embeddedSet[v] {
			rep.Extra = append(rep.Extra, v)
		}
	}

	// Gaps: embedded versions strictly between the lowest and highest
	// applied version that were never applied (classic out-of-order symptom).
	if len(rep.Applied) > 0 {
		lo, hi := rep.Applied[0], rep.Applied[len(rep.Applied)-1]
		for _, v := range rep.Embedded {
			if v > lo && v < hi && !appliedSet[v] {
				rep.Gaps = append(rep.Gaps, v)
			}
		}
	}

	if len(rep.Applied) == 0 || rep.Applied[0] < MinSupportedVersion {
		rep.BelowMinimum = true
	}

	rep.Ready = len(rep.Extra) == 0 && len(rep.Gaps) == 0 && !rep.BelowMinimum
	return rep
}

// LoadApplied reads the applied version history from ferp_schema_migrations.
// A missing table means a fresh database: it returns (nil, nil) and lets
// Check mark the report BelowMinimum so the caller can route to install.
func LoadApplied(ctx context.Context, db platform.DBTX) ([]string, error) {
	rows, err := db.Query(ctx, `SELECT version FROM ferp_schema_migrations ORDER BY version`)
	if err != nil {
		// Fresh database — the migrations table is created by
		// platform.Migrate on first boot.
		if strings.Contains(err.Error(), "does not exist") ||
			strings.Contains(err.Error(), "42P01") {
			return nil, nil
		}
		return nil, fmt.Errorf("upgrade: load applied: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("upgrade: scan applied: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Preflight loads the applied history and checks it against the embedded
// canonical history. It performs no writes.
func Preflight(ctx context.Context, db platform.DBTX, files fs.FS) (Report, error) {
	embedded, err := EmbeddedVersions(files)
	if err != nil {
		return Report{}, err
	}
	applied, err := LoadApplied(ctx, db)
	if err != nil {
		return Report{}, err
	}
	return Check(applied, embedded), nil
}

// Describe renders a one-line-per-finding human summary for runbook output.
func Describe(rep Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "applied=%d embedded=%d ready=%v", len(rep.Applied), len(rep.Embedded), rep.Ready)
	if len(rep.Missing) > 0 {
		fmt.Fprintf(&b, "\nmissing (%d, will apply on boot): %s", len(rep.Missing), strings.Join(rep.Missing, ", "))
	}
	if len(rep.Extra) > 0 {
		fmt.Fprintf(&b, "\nEXTRA (%d, unsupported — DB newer than binary): %s", len(rep.Extra), strings.Join(rep.Extra, ", "))
	}
	if len(rep.Gaps) > 0 {
		fmt.Fprintf(&b, "\nGAPS (%d, out-of-order history): %s", len(rep.Gaps), strings.Join(rep.Gaps, ", "))
	}
	if rep.BelowMinimum {
		fmt.Fprintf(&b, "\nBELOW MINIMUM supported version %s", MinSupportedVersion)
	}
	return b.String()
}
