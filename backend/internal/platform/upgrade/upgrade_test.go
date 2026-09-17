package upgrade

import (
	"testing"

	"github.com/YASSERRMD/forge-erp/backend/migrations"
)

// Down-file parity: every *.up.sql must ship its manual-recovery *.down.sql.
// Down files are never applied automatically; this test fails the build if a
// migration author adds an up file without its rollback counterpart (rollback
// story: docs/OPERATIONS.md).
func TestDownFilesExist(t *testing.T) {
	embedded, err := EmbeddedVersions(migrations.FS)
	if err != nil {
		t.Fatalf("EmbeddedVersions: %v", err)
	}
	if len(embedded) == 0 {
		t.Fatal("no embedded migrations found")
	}
	for _, v := range embedded {
		if _, err := migrations.FS.Open(v + ".down.sql"); err != nil {
			t.Errorf("migration %s: missing %s.down.sql (down-migration discipline)", v, v)
		}
		if _, err := migrations.FS.Open(v + ".up.sql"); err != nil {
			t.Errorf("migration %s: cannot open %s.up.sql: %v", v, v, err)
		}
	}
	t.Logf("verified %d migrations each carry a down file", len(embedded))
}

func TestCheckLaggingDB(t *testing.T) {
	embedded := []string{"0001_platform", "0002_identity", "0003_partners"}
	rep := Check([]string{"0001_platform"}, embedded)
	if !rep.Ready {
		t.Fatalf("lagging DB should be upgrade-ready: %+v", rep)
	}
	if len(rep.Missing) != 2 || rep.Missing[0] != "0002_identity" {
		t.Fatalf("unexpected missing: %v", rep.Missing)
	}
	if len(rep.Extra) != 0 || len(rep.Gaps) != 0 || rep.BelowMinimum {
		t.Fatalf("unexpected flags: %+v", rep)
	}
}

func TestCheckCurrentDB(t *testing.T) {
	embedded := []string{"0001_platform", "0002_identity"}
	rep := Check([]string{"0002_identity", "0001_platform"}, embedded) // unsorted input ok
	if !rep.Ready || len(rep.Missing) != 0 {
		t.Fatalf("current DB should be ready with nothing missing: %+v", rep)
	}
}

func TestCheckExtraVersionsBlock(t *testing.T) {
	embedded := []string{"0001_platform", "0002_identity"}
	rep := Check([]string{"0001_platform", "0002_identity", "9999_future"}, embedded)
	if rep.Ready {
		t.Fatal("DB newer than binary must not be ready")
	}
	if len(rep.Extra) != 1 || rep.Extra[0] != "9999_future" {
		t.Fatalf("unexpected extra: %v", rep.Extra)
	}
}

func TestCheckGapsBlock(t *testing.T) {
	embedded := []string{"0001_platform", "0002_identity", "0003_partners", "0004_catalog"}
	rep := Check([]string{"0001_platform", "0003_partners", "0004_catalog"}, embedded)
	if rep.Ready {
		t.Fatal("out-of-order history must not be ready")
	}
	if len(rep.Gaps) != 1 || rep.Gaps[0] != "0002_identity" {
		t.Fatalf("unexpected gaps: %v", rep.Gaps)
	}
}

func TestCheckFreshDB(t *testing.T) {
	rep := Check(nil, []string{"0001_platform", "0002_identity"})
	if rep.Ready {
		t.Fatal("fresh DB is an install, not an upgrade")
	}
	if !rep.BelowMinimum {
		t.Fatal("fresh DB must report BelowMinimum")
	}
	if len(rep.Missing) != 2 {
		t.Fatalf("fresh DB misses everything: %v", rep.Missing)
	}
}

func TestCheckDuplicatesTolerated(t *testing.T) {
	rep := Check(
		[]string{"0001_platform", "0001_platform"},
		[]string{"0001_platform", "0001_platform"},
	)
	if !rep.Ready {
		t.Fatalf("duplicates should be tolerated: %+v", rep)
	}
}

func TestDescribeMentionsFindings(t *testing.T) {
	rep := Check([]string{"0001_platform", "9999_future"}, []string{"0001_platform", "0002_identity"})
	s := Describe(rep)
	if s == "" {
		t.Fatal("empty describe")
	}
	t.Logf("describe: %s", s)
}
