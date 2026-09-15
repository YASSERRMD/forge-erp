package finance

import (
	"encoding/csv"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ImportLine is one parsed bank-statement line (CSV or CAMT.053) awaiting
// import into ferp_bank_transactions. Amount is signed minor units.
type ImportLine struct {
	Ref       string    `json:"ref"`
	Label     string    `json:"label"`
	Amount    int64     `json:"amount"`
	ValueDate time.Time `json:"value_date"`
}

// StatementLine pairs a stored transaction with its running balance.
type StatementLine struct {
	Transaction BankTransaction `json:"transaction"`
	Balance     int64           `json:"balance"` // cumulative minor units through this line
}

// ReconMatch binds an import line to a stored transaction (or leaves it
// unmatched). Status is "matched" or "unmatched".
type ReconMatch struct {
	Ref           string `json:"ref"`
	TransactionID int64  `json:"transaction_id,omitempty"`
	Status        string `json:"status"`
}

// TransferCmd moves amount (>0) from one bank account to another as a paired,
// atomic transaction pair (out negative on From, in positive on To).
type TransferCmd struct {
	EntityID      int64
	FromAccountID int64
	ToAccountID   int64
	Amount        int64 // minor units, must be > 0
	Label         string
	Ref           string // shared bank ref stem; "" disables dedupe
	ValueDate     time.Time
}

func (c TransferCmd) validate() error {
	if c.FromAccountID == 0 || c.ToAccountID == 0 {
		return errors.New("finance: transfer requires both accounts")
	}
	if c.FromAccountID == c.ToAccountID {
		return errors.New("finance: transfer accounts must differ")
	}
	if c.Amount <= 0 {
		return errors.New("finance: transfer amount must be positive")
	}
	return nil
}

// ParseBankCSV parses a statement CSV with a header row. Recognized headers
// (case-insensitive): ref(erence), date (value_date|value date), label
// (|description|libelle|memo), amount (|montant). Amounts are decimal major
// units ("12.34", "-5,20"); dates accept YYYY-MM-DD, DD/MM/YYYY or RFC3339.
func ParseBankCSV(r io.Reader) ([]ImportLine, error) {
	cr := csv.NewReader(r)
	cr.TrimLeadingSpace = true
	header, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("finance: csv header: %w", err)
	}
	idx := map[string]int{}
	for i, h := range header {
		idx[strings.ToLower(strings.TrimSpace(h))] = i
	}
	pick := func(names ...string) int {
		for _, n := range names {
			if i, ok := idx[n]; ok {
				return i
			}
		}
		return -1
	}
	refI := pick("ref", "reference", "bank_ref")
	dateI := pick("date", "value_date", "value date", "valuedate")
	labelI := pick("label", "description", "libelle", "memo", "libellé")
	amtI := pick("amount", "montant")
	if dateI < 0 || amtI < 0 {
		return nil, errors.New("finance: csv requires date and amount columns")
	}
	var out []ImportLine
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("finance: csv row: %w", err)
		}
		at := func(i int) string {
			if i < 0 || i >= len(rec) {
				return ""
			}
			return strings.TrimSpace(rec[i])
		}
		amount, err := parseDecimalMinor(at(amtI))
		if err != nil {
			return nil, fmt.Errorf("finance: csv amount %q: %w", at(amtI), err)
		}
		date, err := parseStatementDate(at(dateI))
		if err != nil {
			return nil, fmt.Errorf("finance: csv date %q: %w", at(dateI), err)
		}
		out = append(out, ImportLine{Ref: at(refI), Label: at(labelI), Amount: amount, ValueDate: date})
	}
	return out, nil
}

// parseDecimalMinor converts "12.34" / "-5,20" major units to minor units
// (2 fractional digits, half away from zero is irrelevant — exact input).
func parseDecimalMinor(s string) (int64, error) {
	s = strings.ReplaceAll(strings.TrimSpace(s), " ", "")
	if s == "" {
		return 0, errors.New("empty amount")
	}
	neg := false
	if strings.HasPrefix(s, "-") {
		neg = true
		s = s[1:]
	} else if strings.HasPrefix(s, "+") {
		s = s[1:]
	}
	switch {
	case strings.Contains(s, ".") && strings.Contains(s, ","):
		s = strings.ReplaceAll(s, ",", "")
	case strings.Contains(s, ","):
		s = strings.Replace(s, ",", ".", 1)
	}
	parts := strings.SplitN(s, ".", 2)
	var whole int64
	if parts[0] != "" {
		var err error
		if whole, err = strconv.ParseInt(parts[0], 10, 64); err != nil {
			return 0, err
		}
	}
	var frac int64
	if len(parts) == 2 {
		f := parts[1]
		if len(f) > 2 {
			return 0, errors.New("more than 2 decimals")
		}
		for len(f) < 2 {
			f += "0"
		}
		if f != "" {
			var err error
			if frac, err = strconv.ParseInt(f, 10, 64); err != nil {
				return 0, err
			}
		}
	}
	v := whole*100 + frac
	if neg {
		v = -v
	}
	return v, nil
}

func parseStatementDate(s string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02", "02/01/2006", time.RFC3339, "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, strings.TrimSpace(s)); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, errors.New("unrecognized date")
}

// camt.053 (ISO 20022) minimal statement parser: BkToCstmrStmt > Stmt > Ntry
// with Amt (+Ccy), CdtDbtInd (CRDT/DBIT), BookgDt/ValDt and references.
type camtAmount struct {
	Ccy   string `xml:"Ccy,attr"`
	Value string `xml:",chardata"`
}

type camtDate struct {
	Dt   string `xml:"Dt"`
	DtTm string `xml:"DtTm"`
}

type camtEntry struct {
	Amt         camtAmount `xml:"Amt"`
	CdtDbtInd   string     `xml:"CdtDbtInd"`
	BookgDt     camtDate   `xml:"BookgDt"`
	ValDt       camtDate   `xml:"ValDt"`
	NtryRef     string     `xml:"NtryRef"`
	AcctSvcrRef string     `xml:"AcctSvcrRef"`
	AddtlInf    string     `xml:"AddtlNtryInf"`
}

type camtStmt struct {
	ID      string      `xml:"Id"`
	Entries []camtEntry `xml:"Ntry"`
}

type camtDocument struct {
	Stmts []camtStmt `xml:"BkToCstmrStmt>Stmt"`
}

// ParseCAMT053 parses an ISO 20022 camt.053 statement file into import lines.
// DBIT entries map to negative amounts; the line ref prefers NtryRef over
// AcctSvcrRef; the value date prefers ValDt over BookgDt.
func ParseCAMT053(r io.Reader) ([]ImportLine, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("finance: camt read: %w", err)
	}
	var doc camtDocument
	if err := xml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("finance: camt parse: %w", err)
	}
	if len(doc.Stmts) == 0 {
		return nil, errors.New("finance: camt has no statements")
	}
	var out []ImportLine
	for _, st := range doc.Stmts {
		for _, e := range st.Entries {
			amount, err := parseDecimalMinor(strings.TrimSpace(e.Amt.Value))
			if err != nil {
				return nil, fmt.Errorf("finance: camt amount %q: %w", e.Amt.Value, err)
			}
			switch strings.ToUpper(strings.TrimSpace(e.CdtDbtInd)) {
			case "DBIT":
				amount = -abs(amount)
			case "CRDT", "":
				amount = abs(amount)
			default:
				return nil, fmt.Errorf("finance: camt bad CdtDbtInd %q", e.CdtDbtInd)
			}
			ds := e.ValDt.Dt
			if ds == "" {
				ds = e.ValDt.DtTm
			}
			if ds == "" {
				ds = e.BookgDt.Dt
			}
			if ds == "" {
				ds = e.BookgDt.DtTm
			}
			date, err := parseStatementDate(ds)
			if err != nil {
				return nil, fmt.Errorf("finance: camt date %q: %w", ds, err)
			}
			ref := strings.TrimSpace(e.NtryRef)
			if ref == "" {
				ref = strings.TrimSpace(e.AcctSvcrRef)
			}
			out = append(out, ImportLine{Ref: ref, Label: strings.TrimSpace(e.AddtlInf),
				Amount: amount, ValueDate: date})
		}
	}
	return out, nil
}

func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// ApplyRunningBalance sorts txs by (value_date, id) and annotates the
// cumulative balance through each line.
func ApplyRunningBalance(txs []BankTransaction) []StatementLine {
	cp := append([]BankTransaction(nil), txs...)
	sort.Slice(cp, func(i, j int) bool {
		if cp[i].ValueDate.Equal(cp[j].ValueDate) {
			return cp[i].ID < cp[j].ID
		}
		return cp[i].ValueDate.Before(cp[j].ValueDate)
	})
	out := make([]StatementLine, 0, len(cp))
	var bal int64
	for _, t := range cp {
		bal += t.Amount
		out = append(out, StatementLine{Transaction: t, Balance: bal})
	}
	return out
}

// MatchTransactions greedily pairs each import line with one unreconciled
// stored transaction of equal amount whose value date falls within window of
// the line date. Consumed transactions match at most once; unmatched lines
// report status "unmatched".
func MatchTransactions(txs []BankTransaction, lines []ImportLine, window time.Duration) []ReconMatch {
	if window < 0 {
		window = 0
	}
	used := map[int64]bool{}
	out := make([]ReconMatch, 0, len(lines))
	for _, l := range lines {
		m := ReconMatch{Ref: l.Ref, Status: "unmatched"}
		for _, t := range txs {
			if used[t.ID] || t.Reconciled || t.Amount != l.Amount {
				continue
			}
			d := t.ValueDate.Sub(l.ValueDate)
			if d < 0 {
				d = -d
			}
			if d <= window {
				used[t.ID] = true
				m.TransactionID, m.Status = t.ID, "matched"
				break
			}
		}
		out = append(out, m)
	}
	return out
}
