// FEC export (Phase 3): French statutory general-ledger file (Article A47
// A-1 CGI). Byte shape mirrors Dolibarr's own exporter
// (accountancy/class/accountancyexport.class.php): TAB-separated, CRLF line
// endings, YYYYMMDD dates, comma decimals padded to 13, labels unaccented
// with tabs stripped. Only the 18 normative columns ship; Dolibarr's three
// supplementary columns (DateLimitReglmt, NumFacture, FichierFacture) are
// omitted. Lettering columns export empty (no entry lettering ported) and
// Montantdevise/Idevise export empty (base currency only).
package finance

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// FECRow is one export line (one entry leg).
type FECRow struct {
	JournalCode  string
	JournalLabel string
	PieceNum     int64 // entry id
	Date         time.Time
	AccountCode  string
	AccountLabel string
	PieceRef     string
	Label        string
	Debit        int64 // minor units
	Credit       int64 // minor units
}

// BuildFECRows flattens posted entries into export rows (one per leg),
// ordered by entry id. Journals/accounts resolve labels; unknown ids fall
// back to the numeric id so the export never silently drops a leg.
func BuildFECRows(entries []Entry, journals map[int64]Journal, accounts map[int64]Account) []FECRow {
	sorted := append([]Entry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	var out []FECRow
	for _, e := range sorted {
		j := journals[e.JournalID]
		for _, l := range e.Lines {
			a := accounts[l.AccountID]
			code := a.Code
			if code == "" {
				code = fmt.Sprintf("%d", l.AccountID)
			}
			out = append(out, FECRow{
				JournalCode: j.Code, JournalLabel: j.Label,
				PieceNum: e.ID, Date: e.Date,
				AccountCode: code, AccountLabel: a.Label,
				PieceRef: e.Ref, Label: firstLine(l.Label),
				Debit: l.Debit, Credit: l.Credit,
			})
		}
	}
	return out
}

// firstLine keeps the label's first line (multi-line memos would break rows).
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// unaccent folds diacritics (é→e) like Dolibarr's dol_string_unaccent.
func unaccent(s string) string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	out, _, _ := transform.String(t, s)
	return out
}

// cleanCell strips tabs/CR/LF and unaccents a label cell.
func cleanCell(s string) string {
	s = strings.ReplaceAll(s, "\t", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return unaccent(strings.TrimSpace(s))
}

// fecDate formats YYYYMMDD (zero time → empty, never a zero date).
func fecDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("20060102")
}

// fecAmount formats minor units as comma decimals, left-padded to 13 like
// Dolibarr (number_format(x, 2, ',', ”) + STR_PAD_LEFT 13).
func fecAmount(minor int64) string {
	neg := minor < 0
	if neg {
		minor = -minor
	}
	s := fmt.Sprintf("%d,%02d", minor/100, minor%100)
	if neg {
		s = "-" + s
	}
	if len(s) < 13 {
		s = strings.Repeat(" ", 13-len(s)) + s
	}
	return s
}

// fecHeader is the 18 normative column names.
var fecHeader = []string{"JournalCode", "JournalLib", "EcritureNum", "EcritureDate",
	"CompteNum", "CompteLib", "CompAuxNum", "CompAuxLib", "PieceRef", "PieceDate",
	"EcritureLib", "Debit", "Credit", "EcritureLet", "DateLet", "ValidDate",
	"Montantdevise", "Idevise"}

// WriteFEC renders rows with the header (CRLF endings, trailing CRLF).
func WriteFEC(w io.Writer, rows []FECRow) error {
	if _, err := io.WriteString(w, strings.Join(fecHeader, "\t")+"\r\n"); err != nil {
		return err
	}
	for _, r := range rows {
		cells := []string{
			cleanCell(r.JournalCode), cleanCell(r.JournalLabel),
			fmt.Sprintf("%d", r.PieceNum), fecDate(r.Date),
			cleanCell(r.AccountCode), cleanCell(r.AccountLabel),
			"", "",
			cleanCell(r.PieceRef), fecDate(r.Date),
			cleanCell(r.Label), fecAmount(r.Debit), fecAmount(r.Credit),
			"", "", fecDate(r.Date),
			"", "",
		}
		if _, err := io.WriteString(w, strings.Join(cells, "\t")+"\r\n"); err != nil {
			return err
		}
	}
	return nil
}
