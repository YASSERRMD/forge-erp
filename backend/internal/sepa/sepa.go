// Package sepa implements SEPA direct-debit batches (Dolibarr prelevement):
// creditor mandates, batched collections with IBAN mod-97 validation, and
// pain.008-style XML export for the bank. Amounts are minor units (EUR cents).
package sepa

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
)

// Batch status.
type BatchStatus int16

const (
	BatchDraft     BatchStatus = 0
	BatchValidated BatchStatus = 1
	BatchSent      BatchStatus = 2
	BatchCanceled  BatchStatus = -1
)

var bicPattern = regexp.MustCompile(`^[A-Z]{4}[A-Z]{2}[A-Z0-9]{2}([A-Z0-9]{3})?$`)

// CheckIBAN validates structure + mod-97 checksum (ISO 13616).
func CheckIBAN(iban string) error {
	s := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(iban), " ", ""))
	if len(s) < 15 || len(s) > 34 {
		return fmt.Errorf("sepa: bad IBAN length %q", iban)
	}
	rearranged := s[4:] + s[:4]
	var digits strings.Builder
	for _, c := range rearranged {
		switch {
		case c >= '0' && c <= '9':
			digits.WriteRune(c)
		case c >= 'A' && c <= 'Z':
			digits.WriteString(fmt.Sprintf("%d", int(c-'A')+10))
		default:
			return fmt.Errorf("sepa: bad IBAN character %q", iban)
		}
	}
	rem := 0
	for _, c := range digits.String() {
		rem = (rem*10 + int(c-'0')) % 97
	}
	if rem != 1 {
		return fmt.Errorf("sepa: bad IBAN checksum %q", iban)
	}
	return nil
}

// CheckBIC validates the BIC format.
func CheckBIC(bic string) error {
	if !bicPattern.MatchString(strings.ToUpper(strings.TrimSpace(bic))) {
		return fmt.Errorf("sepa: bad BIC %q", bic)
	}
	return nil
}

// Transaction is one collection line.
type Transaction struct {
	DebtorName string `json:"debtor_name"`
	IBAN       string `json:"iban"`
	BIC        string `json:"bic"`
	Amount     int64  `json:"amount"` // cents, > 0
	Remittance string `json:"remittance"`
	EndToEndID string `json:"end_to_end_id"`
}

// Validate checks a transaction.
func (t Transaction) Validate() error {
	if strings.TrimSpace(t.DebtorName) == "" {
		return errors.New("sepa: debtor name required")
	}
	if err := CheckIBAN(t.IBAN); err != nil {
		return err
	}
	if t.BIC != "" {
		if err := CheckBIC(t.BIC); err != nil {
			return err
		}
	}
	if t.Amount <= 0 {
		return errors.New("sepa: amount must be positive")
	}
	if strings.TrimSpace(t.EndToEndID) == "" {
		return errors.New("sepa: end_to_end_id required")
	}
	return nil
}

// Batch is one direct-debit run.
type Batch struct {
	ID           int64         `json:"id"`
	EntityID     int64         `json:"entity_id"`
	Ref          string        `json:"ref"` // unique per entity
	CreditorName string        `json:"creditor_name"`
	CreditorIBAN string        `json:"creditor_iban"`
	CreditorBIC  string        `json:"creditor_bic"`
	CreditorID   string        `json:"creditor_id"` // SEPA creditor identifier
	Sequence     string        `json:"sequence"`    // FRST|RCUR|OOFF|FNAL
	RequestedAt  time.Time     `json:"requested_at"`
	Transactions []Transaction `json:"transactions"`
	Status       BatchStatus   `json:"status"`
	CreatedAt    time.Time     `json:"created_at"`
	RowVersion   int64         `json:"row_version"`
}

// Validate checks batch invariants.
func (b Batch) Validate() error {
	if b.EntityID <= 0 {
		return errors.New("sepa: entity_id required")
	}
	if strings.TrimSpace(b.Ref) == "" {
		return errors.New("sepa: ref required")
	}
	if strings.TrimSpace(b.CreditorName) == "" {
		return errors.New("sepa: creditor name required")
	}
	if err := CheckIBAN(b.CreditorIBAN); err != nil {
		return err
	}
	if err := CheckBIC(b.CreditorBIC); err != nil {
		return err
	}
	switch b.Sequence {
	case "FRST", "RCUR", "OOFF", "FNAL":
	default:
		return fmt.Errorf("sepa: bad sequence %q", b.Sequence)
	}
	if b.RequestedAt.IsZero() {
		return errors.New("sepa: requested_at required")
	}
	if len(b.Transactions) == 0 {
		return errors.New("sepa: at least one transaction required")
	}
	seen := map[string]bool{}
	for _, t := range b.Transactions {
		if err := t.Validate(); err != nil {
			return err
		}
		if seen[t.EndToEndID] {
			return fmt.Errorf("sepa: duplicate end_to_end_id %q", t.EndToEndID)
		}
		seen[t.EndToEndID] = true
	}
	return nil
}

// CanTransition reports whether a batch status change is legal.
func (b Batch) CanTransition(to BatchStatus) bool {
	switch b.Status {
	case BatchDraft:
		return to == BatchValidated || to == BatchCanceled
	case BatchValidated:
		return to == BatchSent || to == BatchCanceled
	default:
		return false
	}
}

// Total sums the batch in cents.
func (b Batch) Total() int64 {
	var sum int64
	for _, t := range b.Transactions {
		sum += t.Amount
	}
	return sum
}

// pain08 is a simplified pain.008.001.02 document (enough for bank upload;
// proprietary extensions are out of scope).
type pain08 struct {
	XMLName xml.Name   `xml:"Document"`
	Xmlns   string     `xml:"xmlns,attr"`
	Cstmr   cstmrDrfBt `xml:"CstmrDrctDbtInitn"`
}

type cstmrDrfBt struct {
	GrpHdr grpHdr  `xml:"GrpHdr"`
	PmtInf pmtInf  `xml:"PmtInf"`
}

type grpHdr struct {
	MsgID   string `xml:"MsgId"`
	CreDtTm string `xml:"CreDtTm"`
	NbOfTxs int    `xml:"NbOfTxs"`
	CtrlSum string `xml:"CtrlSum"`
	InitgPty initgPty `xml:"InitgPty"`
}

type initgPty struct {
	Nm string `xml:"Nm"`
}

type pmtInf struct {
	PmtInfID      string   `xml:"PmtInfId"`
	PmtMtd        string   `xml:"PmtMtd"`
	NbOfTxs       int      `xml:"NbOfTxs"`
	CtrlSum       string   `xml:"CtrlSum"`
	SeqTp         string   `xml:"SeqTp"`
	ReqdColltnDt  string   `xml:"ReqdColltnDt"`
	Cdtr          nmParty  `xml:"Cdtr"`
	CdtrAcct      ibanAcct `xml:"CdtrAcct"`
	CdtrAgt       bicAgt   `xml:"CdtrAgt"`
	CdtrSchmeID   schmeID  `xml:"CdtrSchmeId"`
	DrctDbtTxInf  []txInf  `xml:"DrctDbtTx"`
}

type nmParty struct {
	Nm string `xml:"Nm"`
}

type ibanAcct struct {
	ID struct {
		IBAN string `xml:"IBAN"`
	} `xml:"Id"`
}

type bicAgt struct {
	FinInstnID struct {
		BIC string `xml:"BIC"`
	} `xml:"FinInstnId"`
}

type schmeID struct {
	ID struct {
		PrvtID struct {
			Othr struct {
				ID      string `xml:"Id"`
				SchmeNm struct {
					Prtry string `xml:"Prtry"`
				} `xml:"SchmeNm"`
			} `xml:"Othr"`
		} `xml:"PrvtId"`
	} `xml:"Id"`
}

type txInf struct {
	PmtID struct {
		EndToEndID string `xml:"EndToEndId"`
	} `xml:"PmtId"`
	Amt struct {
		Value    string `xml:",chardata"`
		Currency string `xml:"Ccy,attr"`
	} `xml:"InstdAmt"`
	DbtrAgt bicAgt  `xml:"DbtrAgt"`
	Dbtr    nmParty `xml:"Dbtr"`
	DbtrAcct ibanAcct `xml:"DbtrAcct"`
	RmtInf  struct {
		Ustrd string `xml:"Ustrd"`
	} `xml:"RmtInf"`
}

func cents(v int64) string {
	return fmt.Sprintf("%d.%02d", v/100, v%100)
}

// ExportXML renders the pain.008 document (validated batches only).
func ExportXML(b Batch, now time.Time) ([]byte, error) {
	if b.Status != BatchValidated && b.Status != BatchSent {
		return nil, errors.New("sepa: export validated batches only")
	}
	if err := b.Validate(); err != nil {
		return nil, err
	}
	info := pmtInf{
		PmtInfID: b.Ref, PmtMtd: "DD",
		NbOfTxs:  len(b.Transactions), CtrlSum: cents(b.Total()),
		SeqTp: b.Sequence, ReqdColltnDt: b.RequestedAt.Format("2006-01-02"),
	}
	info.Cdtr.Nm = b.CreditorName
	info.CdtrAcct.ID.IBAN = strings.ReplaceAll(b.CreditorIBAN, " ", "")
	info.CdtrAgt.FinInstnID.BIC = b.CreditorBIC
	info.CdtrSchmeID.ID.PrvtID.Othr.ID = b.CreditorID
	info.CdtrSchmeID.ID.PrvtID.Othr.SchmeNm.Prtry = "SEPA"
	for _, t := range b.Transactions {
		var tx txInf
		tx.PmtID.EndToEndID = t.EndToEndID
		tx.Amt.Value = cents(t.Amount)
		tx.Amt.Currency = "EUR"
		tx.Dbtr.Nm = t.DebtorName
		tx.DbtrAcct.ID.IBAN = strings.ReplaceAll(t.IBAN, " ", "")
		if t.BIC != "" {
			tx.DbtrAgt.FinInstnID.BIC = t.BIC
		} else {
			tx.DbtrAgt.FinInstnID.BIC = "NOTPROVIDED"
		}
		tx.RmtInf.Ustrd = t.Remittance
		info.DrctDbtTxInf = append(info.DrctDbtTxInf, tx)
	}
	doc := pain08{Xmlns: "urn:iso:std:iso:20022:tech:xsd:pain.008.001.02"}
	doc.Cstmr.GrpHdr = grpHdr{MsgID: b.Ref, CreDtTm: now.Format("2006-01-02T15:04:05"),
		NbOfTxs: len(b.Transactions), CtrlSum: cents(b.Total())}
	doc.Cstmr.GrpHdr.InitgPty.Nm = b.CreditorName
	doc.Cstmr.PmtInf = info
	raw, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), raw...), nil
}

// Store is the persistence contract for SEPA batches.
type Store interface {
	CreateBatch(ctx context.Context, b *Batch) error
	BatchByID(ctx context.Context, id int64) (Batch, error)
	ListBatches(ctx context.Context, entityID int64) ([]Batch, error)
	SetBatchStatus(ctx context.Context, id int64, to BatchStatus, rowVersion int64) (Batch, error)
}

// PGStore implements Store against PostgreSQL.
type PGStore struct{ pool *pgxpool.Pool }

// NewPGStore wraps a pool.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

const batchCols = `id, entity_id, ref, creditor_name, creditor_iban, creditor_bic, creditor_id, sequence, requested_at, transactions, status, created_at, row_version`

func scanBatch(row pgx.Row) (Batch, error) {
	var b Batch
	var txs []byte
	err := row.Scan(&b.ID, &b.EntityID, &b.Ref, &b.CreditorName, &b.CreditorIBAN,
		&b.CreditorBIC, &b.CreditorID, &b.Sequence, &b.RequestedAt, &txs,
		&b.Status, &b.CreatedAt, &b.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return Batch{}, identity.ErrNotFound
	}
	if err != nil {
		return Batch{}, err
	}
	_ = json.Unmarshal(txs, &b.Transactions)
	return b, nil
}

func (s *PGStore) CreateBatch(ctx context.Context, b *Batch) error {
	if err := b.Validate(); err != nil {
		return err
	}
	raw, _ := json.Marshal(b.Transactions)
	return s.pool.QueryRow(ctx, `INSERT INTO ferp_sepa_batches
		(entity_id, ref, creditor_name, creditor_iban, creditor_bic, creditor_id,
		 sequence, requested_at, transactions, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id, row_version`,
		b.EntityID, b.Ref, b.CreditorName, b.CreditorIBAN, b.CreditorBIC, b.CreditorID,
		b.Sequence, b.RequestedAt, raw, b.Status,
	).Scan(&b.ID, &b.RowVersion)
}

func (s *PGStore) BatchByID(ctx context.Context, id int64) (Batch, error) {
	return scanBatch(s.pool.QueryRow(ctx, `SELECT `+batchCols+` FROM ferp_sepa_batches WHERE id=$1`, id))
}

func (s *PGStore) ListBatches(ctx context.Context, entityID int64) ([]Batch, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+batchCols+` FROM ferp_sepa_batches WHERE entity_id=$1 ORDER BY id`, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Batch
	for rows.Next() {
		b, err := scanBatch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *PGStore) SetBatchStatus(ctx context.Context, id int64, to BatchStatus, rowVersion int64) (Batch, error) {
	b, err := s.BatchByID(ctx, id)
	if err != nil {
		return Batch{}, err
	}
	if b.RowVersion != rowVersion {
		return Batch{}, identity.ErrVersionConflict
	}
	if !b.CanTransition(to) {
		return Batch{}, errors.New("sepa: illegal transition")
	}
	tag, err := s.pool.Exec(ctx, `UPDATE ferp_sepa_batches SET status=$1, row_version=row_version+1
		WHERE id=$2 AND row_version=$3`, to, id, rowVersion)
	if err != nil {
		return Batch{}, err
	}
	if tag.RowsAffected() == 0 {
		return Batch{}, identity.ErrVersionConflict
	}
	b.Status = to
	b.RowVersion++
	return b, nil
}

// MemoryStore is the in-process fake for handler tests.
type MemoryStore struct {
	mu      sync.Mutex
	seq     int64
	batches map[int64]Batch
}

// NewMemoryStore builds an empty fake.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{batches: map[int64]Batch{}}
}

func (m *MemoryStore) next() int64 { m.seq++; return m.seq }

func (m *MemoryStore) CreateBatch(_ context.Context, b *Batch) error {
	if err := b.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.batches {
		if e.EntityID == b.EntityID && e.Ref == b.Ref {
			return errors.New("sepa: duplicate ref")
		}
	}
	b.ID = m.next()
	b.RowVersion = 1
	m.batches[b.ID] = *b
	return nil
}

func (m *MemoryStore) BatchByID(_ context.Context, id int64) (Batch, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.batches[id]
	if !ok {
		return Batch{}, identity.ErrNotFound
	}
	return b, nil
}

func (m *MemoryStore) ListBatches(_ context.Context, entityID int64) ([]Batch, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Batch
	for _, b := range m.batches {
		if b.EntityID == entityID {
			out = append(out, b)
		}
	}
	return out, nil
}

func (m *MemoryStore) SetBatchStatus(_ context.Context, id int64, to BatchStatus, rowVersion int64) (Batch, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.batches[id]
	if !ok {
		return Batch{}, identity.ErrNotFound
	}
	if b.RowVersion != rowVersion {
		return Batch{}, identity.ErrVersionConflict
	}
	if !b.CanTransition(to) {
		return Batch{}, errors.New("sepa: illegal transition")
	}
	b.Status = to
	b.RowVersion++
	m.batches[id] = b
	return b, nil
}
