package locale_test

import (
	"testing"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform/locale"
)

func mustLoader(t *testing.T, entityDefault string) *locale.Loader {
	t.Helper()
	l, err := locale.NewLoader(entityDefault)
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	return l
}

func TestResolveLocale(t *testing.T) {
	for _, tc := range []struct {
		name, header, pref, entity, want string
	}{
		{"pref wins over everything", "fr-FR, fr;q=0.9", "ar-SA", "fr", "ar-SA"},
		{"header first tag", "fr-FR, fr;q=0.9, en;q=0.8", "", "", "fr-FR"},
		{"header underscore form", "ar_SA", "", "", "ar-SA"},
		{"wildcard skipped", "*, fr;q=0.5", "", "", "fr"},
		{"entity default", "", "", "fr_FR", "fr-FR"},
		{"all empty english", "", "", "", "en"},
		{"pref normalised", "", "EN_us", "", "en-US"},
	} {
		if got := locale.ResolveLocale(tc.header, tc.pref, tc.entity); got != tc.want {
			t.Errorf("%s: ResolveLocale(%q,%q,%q) = %q, want %q",
				tc.name, tc.header, tc.pref, tc.entity, got, tc.want)
		}
	}
}

func TestFallbackChain(t *testing.T) {
	l := mustLoader(t, "")
	for _, tc := range []struct {
		name, user, key, want, wantTag string
	}{
		{"english direct", "en", "doc.invoice", "Invoice", "en"},
		{"french direct", "fr", "doc.invoice", "Facture", "fr"},
		{"region falls to base", "fr-FR", "doc.invoice", "Facture", "fr"},
		{"underscore region", "ar_SA", "doc.invoice", "فاتورة", "ar"},
		{"unknown locale english", "de", "doc.invoice", "Invoice", "en"},
		{"empty locale english", "", "error.required", "Field '%s' is required", "en"},
		{"arabic error", "ar", "error.not_found", "لا توجد سجلات", "ar"},
		{"total miss returns key", "fr", "no.such.key", "no.such.key", ""},
	} {
		got, tag := l.Lookup(tc.user, tc.key)
		if got != tc.want || tag != tc.wantTag {
			t.Errorf("%s: Lookup(%q,%q) = (%q,%q), want (%q,%q)",
				tc.name, tc.user, tc.key, got, tag, tc.want, tc.wantTag)
		}
	}
}

func TestFallbackEntityDefault(t *testing.T) {
	l := mustLoader(t, "fr")
	if got, tag := l.Lookup("de", "doc.invoice"); got != "Facture" || tag != "fr" {
		t.Errorf("entity-default fallback = (%q,%q), want (Facture,fr)", got, tag)
	}
	l2 := mustLoader(t, "xx-unshipped")
	if got, tag := l2.Lookup("yy-unshipped", "doc.invoice"); got != "Invoice" || tag != "en" {
		t.Errorf("double-miss fallback = (%q,%q), want (Invoice,en)", got, tag)
	}
}

func TestCatalogueMatch(t *testing.T) {
	l := mustLoader(t, "")
	c, tag := l.Catalogue("fr-CA")
	if tag != "fr" {
		t.Errorf("Catalogue(fr-CA) matched %q, want fr", tag)
	}
	if v, ok := c.T("party.customer"); !ok || v != "Client" {
		t.Errorf("fr party.customer = %q,%v", v, ok)
	}
}

func TestReviewedContentComplete(t *testing.T) {
	l := mustLoader(t, "")
	en, _ := l.Catalogue("en")
	for _, loc := range []string{"en", "fr", "ar"} {
		c, _ := l.Catalogue(loc)
		for key, enVal := range en.Strings {
			v, ok := c.T(key)
			if !ok || v == "" {
				t.Errorf("%s: missing key %q", loc, key)
				continue
			}
			if loc != "en" && v == enVal && key != "common.email" && key != "doc.description" && key != "doc.stock" && key != "doc.total" && key != "common.date" {
				// Cognates (Email/Description/Stock/Total/Date) are
				// legitimately identical in French; anything else flags an
				// untranslated dump.
				t.Errorf("%s: key %q identical to English %q (untranslated?)", loc, key, v)
			}
		}
	}
}

func TestPluralTagPassthrough(t *testing.T) {
	l := mustLoader(t, "")
	for loc, want := range map[string]string{"en": "one_other", "fr": "one_many", "ar": "arabic"} {
		c, _ := l.Catalogue(loc)
		if c.Plural != want {
			t.Errorf("%s: plural tag = %q, want %q", loc, c.Plural, want)
		}
		if got := locale.PluralTag(loc); got != want {
			t.Errorf("%s: PluralTag = %q, want %q", loc, got, want)
		}
	}
	if got := locale.PluralTag("de-DE"); got != "other" {
		t.Errorf("PluralTag(de-DE) = %q, want other", got)
	}
}

func TestRTL(t *testing.T) {
	for loc, want := range map[string]bool{
		"ar": true, "ar-SA": true, "ar_SA": true,
		"he": true, "fa": true, "ur": true,
		"en": false, "en-US": false, "fr": false, "de": false, "": false,
	} {
		if got := locale.IsRTL(loc); got != want {
			t.Errorf("IsRTL(%q) = %v, want %v", loc, got, want)
		}
	}
	l := mustLoader(t, "")
	ar, _ := l.Catalogue("ar")
	en, _ := l.Catalogue("en")
	if !ar.RTL || en.RTL {
		t.Errorf("catalogue RTL flags: ar=%v en=%v", ar.RTL, en.RTL)
	}
}

func TestFormatMoney(t *testing.T) {
	for _, tc := range []struct {
		name             string
		minor            int64
		currency, locale string
		want             string
	}{
		{"usd default exp 2", 1200, "USD", "en", "12.00 USD"},
		{"eur lowercase code", 1200, "eur", "en", "12.00 EUR"},
		{"zero-decimal jpy", 1200, "JPY", "en", "1,200 JPY"},
		{"zero-decimal krw", 5, "KRW", "en", "5 KRW"},
		{"three-decimal bhd", 12345, "BHD", "en", "12.345 BHD"},
		{"three-decimal kwd sub-unit", 5, "KWD", "en", "0.005 KWD"},
		{"french grouping", 123456, "USD", "fr", "1 234,56 USD"},
		{"french decimal comma", 5, "EUR", "fr", "0,05 EUR"},
		{"arabic no grouping", 123456, "USD", "ar", "1234.56 USD"},
		{"negative", -1200, "USD", "en", "-12.00 USD"},
		{"zero", 0, "USD", "en", "0.00 USD"},
		{"unknown currency exp 2", 1200, "XXQ", "en", "12.00 XXQ"},
		{"mad default 2", 199, "MAD", "fr", "1,99 MAD"},
	} {
		if got := locale.FormatMoney(tc.minor, tc.currency, tc.locale); got != tc.want {
			t.Errorf("%s: FormatMoney(%d,%q,%q) = %q, want %q",
				tc.name, tc.minor, tc.currency, tc.locale, got, tc.want)
		}
	}
}

func TestCurrencyExponent(t *testing.T) {
	for code, want := range map[string]int{
		"USD": 2, "EUR": 2, "MAD": 2, "": 2, "XXQ": 2,
		"JPY": 0, "KRW": 0, "VND": 0, "XOF": 0, "CLP": 0,
		"BHD": 3, "KWD": 3, "OMR": 3, "JOD": 3, "TND": 3, "IQD": 3, "LYD": 3,
	} {
		if got := locale.CurrencyExponent(code); got != want {
			t.Errorf("CurrencyExponent(%q) = %d, want %d", code, got, want)
		}
	}
}

func TestFormatDate(t *testing.T) {
	d := time.Date(2026, 3, 9, 15, 4, 5, 0, time.UTC)
	for loc, want := range map[string]string{
		"en": "03/09/2026", "en-US": "03/09/2026",
		"fr": "09/03/2026", "ar": "09/03/2026", "de": "09/03/2026",
	} {
		if got := locale.FormatDate(d, loc); got != want {
			t.Errorf("FormatDate(%q) = %q, want %q", loc, got, want)
		}
	}
}

func TestMergeOverride(t *testing.T) {
	l := mustLoader(t, "")
	base, _ := l.Catalogue("fr")
	over := map[string]string{
		"party.customer": "Client VIP", // dict fr override wins
		"custom.note":    "Note",       // new keys pass through
		"doc.invoice":    "",           // empty never clobbers
		"  ":             "x",          // blank keys are harmless data, kept as-is
	}
	merged := locale.MergeOverride(base, over)
	if v, _ := merged.T("party.customer"); v != "Client VIP" {
		t.Errorf("override lost: %q", v)
	}
	if v, _ := merged.T("custom.note"); v != "Note" {
		t.Errorf("new key lost: %q", v)
	}
	if v, _ := merged.T("doc.invoice"); v != "Facture" {
		t.Errorf("empty override clobbered base: %q", v)
	}
	if v, _ := base.T("party.customer"); v != "Client" {
		t.Errorf("base mutated: %q", v)
	}
	if merged.Plural != base.Plural || merged.RTL != base.RTL {
		t.Errorf("merge dropped catalogue meta")
	}
}
