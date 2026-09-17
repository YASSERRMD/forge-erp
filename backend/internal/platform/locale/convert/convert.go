// Package convert parses Dolibarr htdocs/langs/*.lang files into ForgeERP
// locale catalogue JSON (backend/internal/platform/locale/locales/<tag>.json).
//
// Dolibarr format (NOT gettext: there are no .po/.mo files): UTF-8 text,
// one KEY=Value per line, "#" comments, with a non-translatable header
// (DIRECTION, FONTFORPDF, SeparatorDecimal, SeparatorThousand,
// FormatDateShort, ...). Only the curated KeyMap below is extracted; every
// other key stays in Dolibarr.
//
// Rerun (more locales = one new Target entry + rerun):
//
//	go run ./backend/internal/platform/locale/convert/cmd/doliconvert \
//	  -langs .forgeerp-temp/dolibarr/htdocs/langs \
//	  -out backend/internal/platform/locale/locales
//
// Maintenance contract: the MECHANISM supports ~120 locales (loader,
// fallback, formatting are locale-agnostic), but REVIEWED CONTENT ships for
// en/fr/ar only. A newly converted locale must be reviewed by a speaker
// before its JSON is committed — raw Dolibarr dumps carry ERP-vocabulary
// quirks (e.g. EN Supplier renders "Vendor", EN VAT renders "Sales tax").
package convert

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Meta carries the non-translatable header fields of a .lang file.
type Meta struct {
	Direction string // "ltr" | "rtl"
	Decimal   string // decimal separator, e.g. "." | ","
	Thousand  string // thousand separator; "" means none
}

// ParseLangFile parses one Dolibarr .lang file. Comment ("#") and blank
// lines are skipped; lines without "=" are ignored. DIRECTION /
// SeparatorDecimal / SeparatorThousand populate Meta (with Dolibarr's
// "None" → "" and "Space" → " " word mapping); every other key lands in
// entries verbatim (values are NOT trimmed — translations may carry
// significant spacing).
func ParseLangFile(r io.Reader) (map[string]string, Meta, error) {
	entries := map[string]string{}
	var meta Meta
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		switch k {
		case "DIRECTION":
			meta.Direction = strings.ToLower(strings.TrimSpace(v))
		case "SeparatorDecimal":
			meta.Decimal = strings.TrimSpace(v)
		case "SeparatorThousand":
			switch w := strings.TrimSpace(v); {
			case strings.EqualFold(w, "None"):
				meta.Thousand = ""
			case strings.EqualFold(w, "Space"):
				meta.Thousand = " "
			default:
				meta.Thousand = w
			}
		default:
			entries[k] = v
		}
	}
	if err := sc.Err(); err != nil {
		return nil, Meta{}, err
	}
	return entries, meta, nil
}

// KeyMap is the curated Dolibarr key → catalogue dotted-key map. Keys are
// grouped by source file comment for rerun maintenance; only these keys are
// extracted into shipped catalogues.
var KeyMap = map[string]string{
	// main.lang — chrome + errors.
	"Save": "common.save", "Cancel": "common.cancel", "Delete": "common.delete",
	"Confirm": "common.confirm", "Close": "common.close", "Search": "common.search",
	"Filter": "common.filter", "Date": "common.date", "Ref": "common.ref",
	"Name": "common.name", "Email": "common.email", "Status": "common.status",
	"Yes": "common.yes", "No": "common.no",
	"Language": "common.language", "Currency": "common.currency",
	"Description": "doc.description", "Quantity": "doc.quantity", "Total": "doc.total",
	"TotalHT": "doc.total_net", "TotalVAT": "doc.total_vat", "TotalTTC": "doc.total_gross",
	"VAT":   "doc.vat",
	"Error": "error.error", "Errors": "error.errors",
	"ErrorFieldRequired": "error.required", "NoRecordFound": "error.not_found",
	"ErrorUnknown": "error.unknown",
	// companies.lang — third parties.
	"Customer": "party.customer", "Supplier": "party.supplier",
	"Company": "party.company", "ThirdParty": "party.third_party",
	"Address": "common.address", "Phone": "common.phone",
	// bills.lang — documents.
	"Bill": "doc.invoice", "Bills": "doc.invoices",
	"CreditNote": "doc.credit_note", "Payment": "doc.payment",
	// orders.lang.
	"Order": "doc.order",
	// products.lang.
	"Stock": "doc.stock",
	// admin.lang.
	"Active": "common.active",
}

// Authored fills catalogue keys with no Dolibarr source (verified by a
// speaker at authoring time, not converted). Keep this list short and say
// so: every entry here is a maintenance liability on reruns.
var Authored = map[string]map[string]string{
	"en": {"common.inactive": "Inactive", "doc.tax": "Tax"},
	"fr": {"common.inactive": "Inactif", "doc.tax": "Taxe"},
	"ar": {"common.inactive": "غير نشط", "doc.tax": "ضريبة"},
}

// Target is one locale to convert: catalogue tag, Dolibarr directory, CLDR
// plural-rules tag (opaque passthrough — see locale package), and the .lang
// basenames merged in order (later files win on collision).
type Target struct {
	Tag    string
	Dir    string
	Plural string
	Files  []string
}

// DefaultTargets is the reviewed-content set. funder note: mechanism covers
// ~120 Dolibarr lang dirs; only these three ship reviewed.
var DefaultTargets = []Target{
	{Tag: "en", Dir: "en_US", Plural: "one_other", Files: []string{"main", "companies", "bills", "orders", "products", "admin"}},
	{Tag: "fr", Dir: "fr_FR", Plural: "one_many", Files: []string{"main", "companies", "bills", "orders", "products", "admin"}},
	{Tag: "ar", Dir: "ar_SA", Plural: "arabic", Files: []string{"main", "companies", "bills", "orders", "products", "admin"}},
}

// CatalogueFile is the on-disk JSON shape: nested section → key → string
// plus the plural-rules tag. The locale loader flattens Strings to dotted
// keys at startup.
type CatalogueFile struct {
	Locale    string                       `json:"locale"`
	Source    string                       `json:"source"`
	Plural    string                       `json:"plural"`
	Direction string                       `json:"direction"`
	Decimal   string                       `json:"decimal"`
	Thousand  string                       `json:"thousand"`
	Strings   map[string]map[string]string `json:"strings"`
}

// BuildCatalogue maps merged .lang entries onto the catalogue shape for tgt.
// Dolibarr keys outside KeyMap are dropped; curated keys missing (or empty)
// in the source are SKIPPED so the loader's English fallback covers them.
// Authored overlays apply last.
func BuildCatalogue(tgt Target, entries map[string]string, meta Meta, source string) CatalogueFile {
	if meta.Direction != "rtl" {
		meta.Direction = "ltr"
	}
	if meta.Decimal == "" {
		meta.Decimal = "."
	}
	cf := CatalogueFile{
		Locale: tgt.Tag, Source: source, Plural: tgt.Plural,
		Direction: meta.Direction, Decimal: meta.Decimal, Thousand: meta.Thousand,
		Strings: map[string]map[string]string{},
	}
	put := func(dotted, v string) {
		ns, k, ok := strings.Cut(dotted, ".")
		if !ok || strings.TrimSpace(v) == "" {
			return
		}
		if cf.Strings[ns] == nil {
			cf.Strings[ns] = map[string]string{}
		}
		cf.Strings[ns][k] = v
	}
	for dk, ck := range KeyMap {
		if v, ok := entries[dk]; ok {
			put(ck, v)
		}
	}
	for k, v := range Authored[tgt.Tag] {
		put(k, v)
	}
	return cf
}

// ConvertLangs converts every target from langsRoot (<dir>/*.lang) and
// writes <outDir>/<tag>.json (deterministic: encoding/json sorts map keys).
func ConvertLangs(langsRoot, outDir string, targets []Target) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("convert: mkdir %s: %w", outDir, err)
	}
	for _, tgt := range targets {
		merged := map[string]string{}
		var meta Meta
		var used []string
		for _, f := range tgt.Files {
			p := filepath.Join(langsRoot, tgt.Dir, f+".lang")
			fh, err := os.Open(p)
			if err != nil {
				return fmt.Errorf("convert: open %s: %w", p, err)
			}
			e, m, err := ParseLangFile(fh)
			closeErr := fh.Close()
			if err != nil {
				return fmt.Errorf("convert: parse %s: %w", p, err)
			}
			if err := closeErr; err != nil {
				return fmt.Errorf("convert: close %s: %w", p, err)
			}
			if f == "main" {
				meta = m
			}
			for k, v := range e {
				merged[k] = v
			}
			used = append(used, f+".lang")
		}
		src := fmt.Sprintf("dolibarr %s (%s)", tgt.Dir, strings.Join(used, "+"))
		cf := BuildCatalogue(tgt, merged, meta, src)
		raw, err := json.MarshalIndent(cf, "", "  ")
		if err != nil {
			return fmt.Errorf("convert: encode %s: %w", tgt.Tag, err)
		}
		raw = append(raw, '\n')
		out := filepath.Join(outDir, tgt.Tag+".json")
		if err := os.WriteFile(out, raw, 0o644); err != nil {
			return fmt.Errorf("convert: write %s: %w", out, err)
		}
	}
	return nil
}
