package convert_test

import (
	"strings"
	"testing"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform/locale/convert"
)

const fixture = `# Dolibarr language file - Source file is en_US - main
DIRECTION=rtl
SeparatorDecimal=.
SeparatorThousand=None
Save=حفظ
Error=خطأ
# a comment
NoEqualsLine
`

func TestParseLangFile(t *testing.T) {
	entries, meta, err := convert.ParseLangFile(strings.NewReader(fixture))
	if err != nil {
		t.Fatalf("ParseLangFile: %v", err)
	}
	if meta.Direction != "rtl" || meta.Decimal != "." || meta.Thousand != "" {
		t.Errorf("meta = %+v, want rtl/./empty", meta)
	}
	if entries["Save"] != "حفظ" || entries["Error"] != "خطأ" {
		t.Errorf("entries = %v", entries)
	}
	if _, ok := entries["# a comment"]; ok {
		t.Errorf("comment leaked into entries")
	}
	if _, ok := entries["NoEqualsLine"]; ok {
		t.Errorf("bare line leaked into entries")
	}
}

func TestParseSeparatorsWords(t *testing.T) {
	entries, meta, err := convert.ParseLangFile(strings.NewReader(
		"SeparatorDecimal=,\nSeparatorThousand=Space\nSave=Enregistrer\n"))
	if err != nil {
		t.Fatalf("ParseLangFile: %v", err)
	}
	if meta.Decimal != "," || meta.Thousand != " " {
		t.Errorf("meta = %+v, want ,/space", meta)
	}
	if entries["Save"] != "Enregistrer" {
		t.Errorf("entries = %v", entries)
	}
}

func TestBuildCatalogueGolden(t *testing.T) {
	entries, meta, err := convert.ParseLangFile(strings.NewReader(fixture))
	if err != nil {
		t.Fatalf("ParseLangFile: %v", err)
	}
	tgt := convert.Target{Tag: "ar", Dir: "ar_SA", Plural: "arabic"}
	cf := convert.BuildCatalogue(tgt, entries, meta, "fixture")
	if cf.Locale != "ar" || cf.Plural != "arabic" || cf.Direction != "rtl" {
		t.Errorf("header = %+v", cf)
	}
	if cf.Decimal != "." || cf.Thousand != "" {
		t.Errorf("separators = %q/%q", cf.Decimal, cf.Thousand)
	}
	if got := cf.Strings["common"]["save"]; got != "حفظ" {
		t.Errorf("common.save = %q", got)
	}
	if got := cf.Strings["error"]["error"]; got != "خطأ" {
		t.Errorf("error.error = %q", got)
	}
	// Curated-but-absent CONVERTED keys are skipped (loader falls back to
	// English); the authored overlay still applies (hence a doc section
	// containing only tax).
	if _, ok := cf.Strings["doc"]["invoice"]; ok {
		t.Errorf("absent doc.invoice should be skipped")
	}
	if got := cf.Strings["common"]["inactive"]; got != "غير نشط" {
		t.Errorf("authored common.inactive = %q", got)
	}
	// Unknown Dolibarr keys never leak into catalogues.
	for ns, m := range cf.Strings {
		for k := range m {
			if ns == "common" && k == "save" {
				continue
			}
			if k == "inactive" || k == "tax" {
				continue // authored
			}
			found := false
			for _, ck := range convert.KeyMap {
				if ck == ns+"."+k {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("unmapped key leaked: %s.%s", ns, k)
			}
		}
	}
}

func TestDefaultTargetsReviewedTrio(t *testing.T) {
	if len(convert.DefaultTargets) != 3 {
		t.Fatalf("DefaultTargets = %d, want 3 (en/fr/ar reviewed)", len(convert.DefaultTargets))
	}
	seen := map[string]bool{}
	for _, tg := range convert.DefaultTargets {
		seen[tg.Tag] = true
		if len(tg.Files) == 0 || tg.Plural == "" || tg.Dir == "" {
			t.Errorf("incomplete target %+v", tg)
		}
	}
	for _, want := range []string{"en", "fr", "ar"} {
		if !seen[want] {
			t.Errorf("missing reviewed target %q", want)
		}
	}
}
