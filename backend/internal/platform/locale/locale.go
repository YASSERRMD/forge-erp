// Package locale is the Phase 4 localisation backend mechanism (Parity
// Build Order Phase 4, backend half).
//
// Catalogue format (per-locale JSON under locales/, embedded via go:embed):
//
//	{"locale": "fr", "source": "...", "plural": "one_many",
//	 "direction": "ltr", "decimal": ",", "thousand": " ",
//	 "strings": {"common": {"save": "Enregistrer"}, ...}}
//
// Nested section → key → string plus an opaque plural-rules tag (carried
// through, never interpreted — plural evaluation is a frontend concern).
//
// Lookup fallback chain: user locale → base language (fr-FR → fr) → entity
// default → its base → English. Missing keys fall back to the English value;
// a key missing even in English resolves to the key itself (fail visible).
//
// Maintenance honesty: the MECHANISM is locale-agnostic (any of Dolibarr's
// ~120 lang dirs converts with one Target entry + rerun of doliconvert),
// but REVIEWED CONTENT ships for en + fr + ar only. New conversions need a
// speaker review before commit — see convert.KeyMap / convert.Authored.
//
// Dictionary wiring (dict package is another worker's code — NOT modified):
// dict entries already carry LocaleOverrides merged by Store.List
// (dictionary, locale, ...). Callers in an entity context resolve the
// effective locale with ResolveLocale and pass it to List; when rendering
// catalogue-driven UI with per-tenant dictionary labels, overlay them with
// MergeOverride. locale never imports dict (no cycle risk either way).
//
// Frontend follow-up (NOT this task): SPA string extraction — the catalogue
// JSON shape is directly consumable by the web client, but key coverage and
// t() wiring are still open.
package locale

import (
	"embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed locales/*.json
var localeFS embed.FS

// DefaultLocale is the final fallback of every chain. English catalogues
// are the reviewed source set, so English always loads.
const DefaultLocale = "en"

// Catalogue is one flattened locale catalogue.
type Catalogue struct {
	Locale   string
	Plural   string // opaque CLDR-style plural-rules tag (passthrough)
	RTL      bool
	Decimal  string            // decimal separator
	Thousand string            // thousand separator ("" = none)
	Strings  map[string]string // dotted key ("doc.invoice") → string
}

// T returns the string for a dotted key.
func (c Catalogue) T(key string) (string, bool) {
	v, ok := c.Strings[key]
	return v, ok
}

type fileCatalogue struct {
	Locale    string                       `json:"locale"`
	Plural    string                       `json:"plural"`
	Direction string                       `json:"direction"`
	Decimal   string                       `json:"decimal"`
	Thousand  string                       `json:"thousand"`
	Strings   map[string]map[string]string `json:"strings"`
}

func loadEmbedded() (map[string]Catalogue, error) {
	entries, err := localeFS.ReadDir("locales")
	if err != nil {
		return nil, fmt.Errorf("locale: read embedded locales: %w", err)
	}
	out := make(map[string]Catalogue, len(entries))
	for _, e := range entries {
		raw, err := localeFS.ReadFile("locales/" + e.Name())
		if err != nil {
			return nil, fmt.Errorf("locale: read %s: %w", e.Name(), err)
		}
		var fc fileCatalogue
		if err := json.Unmarshal(raw, &fc); err != nil {
			return nil, fmt.Errorf("locale: decode %s: %w", e.Name(), err)
		}
		if strings.TrimSpace(fc.Locale) == "" {
			return nil, fmt.Errorf("locale: %s has empty locale tag", e.Name())
		}
		flat := make(map[string]string, len(fc.Strings)*4)
		for ns, m := range fc.Strings {
			for k, v := range m {
				flat[ns+"."+k] = v
			}
		}
		out[strings.ToLower(fc.Locale)] = Catalogue{
			Locale: fc.Locale, Plural: fc.Plural,
			RTL:      strings.EqualFold(strings.TrimSpace(fc.Direction), "rtl"),
			Decimal:  fc.Decimal,
			Thousand: fc.Thousand,
			Strings:  flat,
		}
	}
	if _, ok := out[DefaultLocale]; !ok {
		return nil, fmt.Errorf("locale: embedded %q catalogue missing", DefaultLocale)
	}
	return out, nil
}

// Loader resolves catalogues along the fallback chain. The zero value is
// not usable — build with NewLoader.
type Loader struct {
	entityDefault string
	cats          map[string]Catalogue // lowercase tag → catalogue
}

// NewLoader loads the embedded catalogues; entityDefault is the entity's
// configured default locale ("" = English).
func NewLoader(entityDefault string) (*Loader, error) {
	cats, err := loadEmbedded()
	if err != nil {
		return nil, err
	}
	return &Loader{entityDefault: entityDefault, cats: cats}, nil
}

// chain expands the fallback walk: user locale → its base → entity default
// → its base → English, lowercased and deduplicated.
func (l *Loader) chain(userLocale string) []string {
	var out []string
	add := func(tag string) {
		t := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(tag), "_", "-"))
		if t == "" {
			return
		}
		for _, e := range out {
			if e == t {
				return
			}
		}
		out = append(out, t)
		if base, _, ok := strings.Cut(t, "-"); ok {
			for _, e := range out {
				if e == base {
					return
				}
			}
			out = append(out, base)
		}
	}
	add(userLocale)
	add(l.entityDefault)
	add(DefaultLocale)
	return out
}

// Catalogue returns the best catalogue for userLocale plus the matched tag.
func (l *Loader) Catalogue(userLocale string) (Catalogue, string) {
	for _, t := range l.chain(userLocale) {
		if c, ok := l.cats[t]; ok {
			return c, t
		}
	}
	c := l.cats[DefaultLocale]
	return c, DefaultLocale
}

// Lookup resolves one dotted key along the fallback chain, returning the
// value and the tag it came from. Total miss returns (key, "").
func (l *Loader) Lookup(userLocale, key string) (string, string) {
	for _, t := range l.chain(userLocale) {
		if c, ok := l.cats[t]; ok {
			if v, ok := c.Strings[key]; ok && strings.TrimSpace(v) != "" {
				return v, t
			}
		}
	}
	return key, ""
}

// Normalize canonicalises a locale tag to BCP 47-ish form: language
// lowercase, region uppercase, "-" separator ("fr_fr" → "fr-FR"). Empty
// stays empty (callers apply fallback).
func Normalize(tag string) string {
	t := strings.TrimSpace(strings.ReplaceAll(tag, "_", "-"))
	if t == "" {
		return ""
	}
	parts := strings.Split(t, "-")
	parts[0] = strings.ToLower(parts[0])
	for i := 1; i < len(parts); i++ {
		if len(parts[i]) == 2 {
			parts[i] = strings.ToUpper(parts[i])
		} else {
			parts[i] = strings.ToLower(parts[i])
		}
	}
	return strings.Join(parts, "-")
}

// ResolveLocale picks the effective locale tag: explicit user preference
// first, then the Accept-Language header's first non-wildcard tag, then the
// entity default, then English. It does NOT negotiate against shipped
// catalogues — the Loader's fallback chain handles unsupported tags.
func ResolveLocale(userHeader, userPref, entityDefault string) string {
	if t := Normalize(userPref); t != "" {
		return t
	}
	if t := parseAcceptLanguage(userHeader); t != "" {
		return t
	}
	if t := Normalize(entityDefault); t != "" {
		return t
	}
	return DefaultLocale
}

// parseAcceptLanguage returns the first non-wildcard tag of an
// Accept-Language header ("fr-FR, fr;q=0.9, en;q=0.8" → "fr-FR").
func parseAcceptLanguage(header string) string {
	for _, part := range strings.Split(header, ",") {
		tag, _, _ := strings.Cut(part, ";")
		tag = strings.TrimSpace(tag)
		if tag == "" || tag == "*" {
			continue
		}
		return Normalize(tag)
	}
	return ""
}

// rtlLangs are base languages written right-to-left.
var rtlLangs = map[string]bool{
	"ar": true, "he": true, "fa": true, "ur": true,
	"ps": true, "sd": true, "yi": true, "dv": true, "ku": true,
}

// IsRTL reports whether tag is a right-to-left locale (base-language match,
// so "ar-SA" and "ar" both qualify; AR is the shipped RTL proof).
func IsRTL(tag string) bool {
	base, _, _ := strings.Cut(strings.ToLower(strings.ReplaceAll(strings.TrimSpace(tag), "_", "-")), "-")
	return rtlLangs[base]
}

// pluralTags maps reviewed base languages to their plural-rules tag;
// unknown languages get "other" (safe passthrough default).
var pluralTags = map[string]string{
	"en": "one_other",
	"fr": "one_many",
	"ar": "arabic",
}

// PluralTag returns the plural-rules tag for tag's base language.
func PluralTag(tag string) string {
	base, _, _ := strings.Cut(strings.ToLower(strings.ReplaceAll(strings.TrimSpace(tag), "_", "-")), "-")
	if p, ok := pluralTags[base]; ok {
		return p
	}
	return "other"
}

// MergeOverride overlays dictionary per-locale labels (dict.Entry
// LocaleOverrides, K7) onto a catalogue copy: non-empty overrides win,
// catalogue values cover everything else. dict is NOT modified — the caller
// wires it as:
//
//	localeTag := locale.ResolveLocale(acceptLang, userPref, entityDefault)
//	entries, _ := dictStore.List(ctx, db, "country", localeTag, true)
//	over := map[string]string{} // entry.Code → entry.Label, as needed
//	cat := locale.MergeOverride(base, over)
func MergeOverride(base Catalogue, overrides map[string]string) Catalogue {
	out := Catalogue{
		Locale: base.Locale, Plural: base.Plural, RTL: base.RTL,
		Decimal: base.Decimal, Thousand: base.Thousand,
		Strings: make(map[string]string, len(base.Strings)+len(overrides)),
	}
	for k, v := range base.Strings {
		out.Strings[k] = v
	}
	for k, v := range overrides {
		if strings.TrimSpace(v) == "" {
			continue
		}
		out.Strings[k] = v
	}
	return out
}
