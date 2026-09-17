// Package incoterm owns the Incoterms 2020 code table (Dolibarr
// llx_c_incoterms equivalent): static validation for sales documents plus a
// seeded lookup table (ferp_incoterms) served over HTTP. Procurement-side
// shipping documents reuse Validate as a follow-up without touching the
// procurement package (see DIFFERENCES).
package incoterm

import (
	"fmt"
	"strings"
)

// Mode scopes a term to its transport applicability.
type Mode string

const (
	ModeAny Mode = "any" // any mode of transport
	ModeSea Mode = "sea" // sea and inland waterway only
)

// Term is one Incoterms 2020 rule.
type Term struct {
	Code  string `json:"code"`
	Label string `json:"label"`
	Mode  Mode   `json:"mode"`
}

// Table is the full Incoterms 2020 set (11 codes, ICC publication 723).
var Table = []Term{
	{Code: "EXW", Label: "Ex Works", Mode: ModeAny},
	{Code: "FCA", Label: "Free Carrier", Mode: ModeAny},
	{Code: "CPT", Label: "Carriage Paid To", Mode: ModeAny},
	{Code: "CIP", Label: "Carriage and Insurance Paid To", Mode: ModeAny},
	{Code: "DAP", Label: "Delivered At Place", Mode: ModeAny},
	{Code: "DPU", Label: "Delivered at Place Unloaded", Mode: ModeAny},
	{Code: "DDP", Label: "Delivered Duty Paid", Mode: ModeAny},
	{Code: "FAS", Label: "Free Alongside Ship", Mode: ModeSea},
	{Code: "FOB", Label: "Free On Board", Mode: ModeSea},
	{Code: "CFR", Label: "Cost and Freight", Mode: ModeSea},
	{Code: "CIF", Label: "Cost, Insurance and Freight", Mode: ModeSea},
}

var byCode = func() map[string]Term {
	m := make(map[string]Term, len(Table))
	for _, t := range Table {
		m[t.Code] = t
	}
	return m
}()

// Normalize trims and upper-cases a candidate code.
func Normalize(code string) string { return strings.ToUpper(strings.TrimSpace(code)) }

// Validate reports whether code is an Incoterms 2020 code. Empty is invalid
// here — callers treat "no incoterm" as "" before calling.
func Validate(code string) error {
	if _, ok := byCode[Normalize(code)]; !ok {
		return fmt.Errorf("incoterm: unknown code %q (want one of EXW FCA CPT CIP DAP DPU DDP FAS FOB CFR CIF)", code)
	}
	return nil
}

// Lookup returns the term for code (after Normalize) or false.
func Lookup(code string) (Term, bool) {
	t, ok := byCode[Normalize(code)]
	return t, ok
}

// Codes returns every code in ICC presentation order.
func Codes() []string {
	out := make([]string, 0, len(Table))
	for _, t := range Table {
		out = append(out, t.Code)
	}
	return out
}
