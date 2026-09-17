// Command coa_convert regenerates the per-country statutory chart packs in
// ../packs from Dolibarr's llx_accounting_account_*.sql install data:
//
//	go run ./backend/internal/finance/coa_convert \
//	  --dolibarr .forgeerp-temp/dolibarr/htdocs/install/mysql/data \
//	  --out backend/internal/finance/packs
//
// It is rerunnable: re-running against a newer Dolibarr checkout refreshes
// the generated pack_*.sql files (header records the source). Pack choice:
// FR ships PCG25-DEV (metropolitan standard; PCG18-ASSOC/PCGAFR14-DEV left
// out), DE ships SKR03 (SKR04 left out), US ships US-BASE (US-GAAP-BASIC
// left out). Only active='1' rows are kept when the source carries an
// active flag.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Row is one parsed Dolibarr accounting-account row.
type Row struct {
	Version string // fk_pcg_version
	Type    string // pcg_type
	Code    string // account_number
	Label   string
	Active  string // "" when the source has no active column
}

// splitValues tokenizes a parenthesised VALUES tuple, honouring
// single-quoted strings with '' escapes; numbers/bare words pass through.
func splitValues(s string) []string {
	var out []string
	var cur strings.Builder
	inStr := false
	flush := func() {
		out = append(out, strings.TrimSpace(cur.String()))
		cur.Reset()
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inStr {
			if c == '\'' {
				if i+1 < len(s) && s[i+1] == '\'' {
					cur.WriteByte('\'')
					i++
				} else {
					inStr = false
				}
			} else {
				cur.WriteByte(c)
			}
			continue
		}
		switch c {
		case '\'':
			inStr = true
		case ',':
			flush()
		case '(',
			')',
			';':
			// tuple boundaries, dropped
		default:
			cur.WriteByte(c)
		}
	}
	if strings.TrimSpace(cur.String()) != "" || len(out) > 0 {
		flush()
	}
	return out
}

// parsePack extracts rows from one Dolibarr llx_accounting_account file.
// Layout: (entity, rowid, fk_pcg_version, pcg_type, account_number,
// account_parent, label[, active[, ...]]).
func parsePack(path string) ([]Row, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rows []Row
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "INSERT INTO llx_accounting_account") {
			continue
		}
		idx := strings.Index(line, "VALUES")
		if idx < 0 {
			continue
		}
		f := splitValues(line[idx+len("VALUES"):])
		if len(f) < 7 {
			return nil, fmt.Errorf("short tuple in %s: %q", path, line)
		}
		r := Row{Version: f[2], Type: f[3], Code: f[4], Label: f[6]}
		if len(f) >= 8 {
			r.Active = f[7]
		}
		rows = append(rows, r)
	}
	return rows, nil
}

// hasPrefix reports whether s starts with any of prefs.
func hasPrefix(s string, prefs []string) bool {
	for _, p := range prefs {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// frCategory folds PCG classes onto asset/liability/equity/revenue/expense.
// Classes 4/5 mix assets and liabilities in the PCG, so the class default
// (asset) is refined with the well-known third-party/tax liability prefixes;
// revenue/expense (classes 6/7) are exact, which is what the year-end close
// keys off.
func frCategory(code string) string {
	if hasPrefix(code, []string{"15", "16", "17", "18",
		"401", "402", "403", "404", "405", "406", "407", "408", "4090", "419",
		"42", "444", "445", "446", "447", "448", "455", "462", "519"}) {
		return "liability"
	}
	switch firstDigit(code) {
	case '1':
		return "equity"
	case '2', '3', '4', '5':
		return "asset"
	case '6':
		return "expense"
	case '7':
		return "revenue"
	case '8':
		return "equity"
	}
	return "asset"
}

// deCategory maps SKR03 classes; 16xx-17xx carry the DATEV liabilities.
func deCategory(code string) string {
	if hasPrefix(code, []string{"16", "17"}) {
		return "liability"
	}
	switch firstDigit(code) {
	case '0', '9':
		return "equity"
	case '1', '2':
		return "asset"
	case '3', '4', '5', '6', '7':
		return "expense"
	case '8':
		return "revenue"
	}
	return "asset"
}

// usCategory maps US-BASE pcg_type values directly.
func usCategory(t string) string {
	switch t {
	case "ASSETS":
		return "asset"
	case "LIABILITIES":
		return "liability"
	case "EQUITY", "CAPITAL":
		return "equity"
	case "INCOME", "OTHER_REVENUE":
		return "revenue"
	case "COGS", "EXPENSE", "OTHER_EXPENSES":
		return "expense"
	}
	return "asset"
}

func firstDigit(s string) byte {
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			return s[i]
		}
	}
	return 0
}

func sqlQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// parentOf returns the longest proper prefix of code present in codes.
func parentOf(code string, codes map[string]bool) string {
	for i := len(code) - 1; i > 0; i-- {
		if codes[code[:i]] {
			return code[:i]
		}
	}
	return ""
}

type packSpec struct {
	file    string // Dolibarr source file
	pack    string // ferp pack code
	version string // fk_pcg_version filter
	cat     func(Row) string
}

func main() {
	dolibarr := flag.String("dolibarr", "", "Dolibarr mysql/data directory")
	out := flag.String("out", "", "output directory for pack_*.sql")
	flag.Parse()
	if *dolibarr == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "usage: coa_convert --dolibarr <dir> --out <dir>")
		os.Exit(2)
	}
	specs := []packSpec{
		{"llx_accounting_account_fr.sql", "FR", "PCG25-DEV", func(r Row) string { return frCategory(r.Code) }},
		{"llx_accounting_account_de.sql", "DE", "SKR03", func(r Row) string { return deCategory(r.Code) }},
		{"llx_accounting_account_us.sql", "US", "US-BASE", func(r Row) string { return usCategory(r.Type) }},
	}
	for _, sp := range specs {
		rows, err := parsePack(filepath.Join(*dolibarr, sp.file))
		if err != nil {
			fmt.Fprintln(os.Stderr, "parse:", err)
			os.Exit(1)
		}
		seen := map[string]bool{}
		type acct struct{ code, label, parent, cat string }
		var accts []acct
		var skippedInactive, skippedVersion, skippedDup int
		for _, r := range rows {
			if r.Version != sp.version {
				skippedVersion++
				continue
			}
			if r.Active == "0" {
				skippedInactive++
				continue
			}
			if r.Code == "" || seen[r.Code] {
				skippedDup++
				continue
			}
			seen[r.Code] = true
			accts = append(accts, acct{code: r.Code, label: r.Label, cat: sp.cat(r)})
		}
		for i := range accts {
			accts[i].parent = parentOf(accts[i].code, seen)
		}
		sort.Slice(accts, func(i, j int) bool { return accts[i].code < accts[j].code })
		var sb strings.Builder
		fmt.Fprintf(&sb, "-- pack_%s.sql: %s statutory chart generated by coa_convert from\n", strings.ToLower(sp.pack), sp.pack)
		fmt.Fprintf(&sb, "-- Dolibarr %s (fk_pcg_version=%s). DO NOT EDIT: rerun\n", sp.file, sp.version)
		fmt.Fprintf(&sb, "--   go run ./backend/internal/finance/coa_convert --dolibarr <dir> --out <dir>\n")
		fmt.Fprintf(&sb, "-- __ENTITY__ is substituted with the entity id by the pack loader.\n")
		sb.WriteString("INSERT INTO ferp_accounting_accounts (entity_id, pack, code, label, parent, category) VALUES\n")
		for i, a := range accts {
			parent := "NULL"
			if a.parent != "" {
				parent = sqlQuote(a.parent)
			}
			sep := ","
			if i == len(accts)-1 {
				sep = ""
			}
			fmt.Fprintf(&sb, "(__ENTITY__, %s, %s, %s, %s, %s)%s\n",
				sqlQuote(sp.pack), sqlQuote(a.code), sqlQuote(a.label), parent, sqlQuote(a.cat), sep)
		}
		sb.WriteString("ON CONFLICT (entity_id, pack, code) DO NOTHING;\n")
		dest := filepath.Join(*out, "pack_"+strings.ToLower(sp.pack)+".sql")
		if err := os.WriteFile(dest, []byte(sb.String()), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "write:", err)
			os.Exit(1)
		}
		fmt.Printf("%s: %d accounts (%s), skipped version=%d inactive=%d dup=%d -> %s\n",
			sp.pack, len(accts), sp.version, skippedVersion, skippedInactive, skippedDup, dest)
	}
}
