// Number/date/currency formatting for the locale package: minor-unit int64
// only, never floats. Decimal placement comes from the ISO-4217 exponent
// table below (a currency's minor-unit scale is a property of the CURRENCY,
// not the locale); digit grouping and the decimal mark come from the
// locale's separators.
package locale

import (
	"fmt"
	"strings"
	"time"
)

// zeroExp lists common zero-decimal currencies (ISO 4217 exponent 0).
var zeroExp = map[string]bool{
	"BIF": true, "CLP": true, "DJF": true, "GNF": true, "ISK": true,
	"JPY": true, "KMF": true, "KRW": true, "PYG": true, "RWF": true,
	"UGX": true, "UYI": true, "VND": true, "VUV": true,
	"XAF": true, "XOF": true, "XPF": true,
}

// threeExp lists common three-decimal currencies (ISO 4217 exponent 3).
var threeExp = map[string]bool{
	"BHD": true, "IQD": true, "JOD": true, "KWD": true,
	"LYD": true, "OMR": true, "TND": true,
}

// CurrencyExponent returns the ISO-4217 minor-unit exponent for code
// (unknown codes default to 2 — the common-case EUR/USD/GBP/MAD/... scale).
func CurrencyExponent(code string) int {
	c := strings.ToUpper(strings.TrimSpace(code))
	switch {
	case zeroExp[c]:
		return 0
	case threeExp[c]:
		return 3
	default:
		return 2
	}
}

// baseLocale lowercases and strips the region ("fr-FR" → "fr").
func baseLocale(tag string) string {
	base, _, _ := strings.Cut(strings.ToLower(strings.ReplaceAll(strings.TrimSpace(tag), "_", "-")), "-")
	return base
}

// GroupSeparators returns (decimal, thousand) marks for tag, sourced from
// the Dolibarr per-locale separators (fr "Space", ar "None") with an
// en-style default for unlisted languages.
func GroupSeparators(locale string) (decimal, thousand string) {
	switch baseLocale(locale) {
	case "fr":
		return ",", " "
	case "ar":
		return ".", ""
	case "de":
		return ",", "."
	default:
		return ".", ","
	}
}

// FormatDecimal renders a minor-unit amount with exponent fractional digits
// under locale grouping: FormatDecimal(123456, 2, "fr") → "1 234,56".
func FormatDecimal(minor int64, exponent int, locale string) string {
	if exponent < 0 {
		exponent = 0
	}
	dec, thou := GroupSeparators(locale)
	neg := minor < 0
	if neg {
		minor = -minor
	}
	div := int64(1)
	for i := 0; i < exponent; i++ {
		div *= 10
	}
	s := groupInt(minor/div, thou)
	if exponent > 0 {
		s += dec + padLeft(minor%div, exponent)
	}
	if neg {
		s = "-" + s
	}
	return s
}

// FormatMoney renders minor-unit money with the currency's exponent and the
// locale's grouping, suffixed with the ISO code: 1200/"USD"/"en" →
// "12.00 USD"; 1200/"JPY" → "1,200 JPY"; 12345/"BHD" → "12.345 BHD".
func FormatMoney(minor int64, currency, locale string) string {
	code := strings.ToUpper(strings.TrimSpace(currency))
	s := FormatDecimal(minor, CurrencyExponent(code), locale)
	if code == "" {
		return s
	}
	return s + " " + code
}

// FormatDate renders the short calendar date in the Dolibarr per-locale
// short format (en_US %m/%d/%Y, fr/ar %d/%m/%Y), in UTC for determinism.
func FormatDate(t time.Time, locale string) string {
	y, m, d := t.UTC().Date()
	if baseLocale(locale) == "en" {
		return fmt.Sprintf("%02d/%02d/%04d", int(m), d, y)
	}
	return fmt.Sprintf("%02d/%02d/%04d", d, int(m), y)
}

func groupInt(v int64, thou string) string {
	s := fmt.Sprintf("%d", v)
	if thou == "" || len(s) <= 3 {
		return s
	}
	var b strings.Builder
	rem := len(s) % 3
	if rem > 0 {
		b.WriteString(s[:rem])
	}
	for i := rem; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteString(thou)
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

func padLeft(v int64, width int) string {
	s := fmt.Sprintf("%d", v)
	for len(s) < width {
		s = "0" + s
	}
	return s
}
