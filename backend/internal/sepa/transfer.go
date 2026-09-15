package sepa

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/jackc/pgx/v5"
)

// TransferStatus mirrors the batch lifecycle for outbound credit transfers.
type TransferStatus int16

const (
	TransferDraft     TransferStatus = 0
	TransferValidated TransferStatus = 1
	TransferSent      TransferStatus = 2
	TransferCanceled  TransferStatus = -1
)

// CreditLine is one outbound payment to a supplier.
type CreditLine struct {
	CreditorName string `json:"creditor_name"`
	IBAN         string `json:"iban"`
	BIC          string `json:"bic"`
	Amount       int64  `json:"amount"` // cents, > 0
	Remittance   string `json:"remittance"`
	EndToEndID   string `json:"end_to_end_id"`
}

// Validate checks a credit line.
func (l CreditLine) Validate() error {
	if strings.TrimSpace(l.CreditorName) == "" {
		return fmt.Errorf("sepa: creditor name required: %w", platform.ErrValidation)
	}
	if err := CheckIBAN(l.IBAN); err != nil {
		return err
	}
	if l.BIC != "" {
		if err := CheckBIC(l.BIC); err != nil {
			return err
		}
	}
	if l.Amount <= 0 {
		return fmt.Errorf("sepa: amount must be positive: %w", platform.ErrValidation)
	}
	if strings.TrimSpace(l.EndToEndID) == "" {
		return fmt.Errorf("sepa: end_to_end_id required: %w", platform.ErrValidation)
	}
	return nil
}

// Transfer is one outbound credit-transfer run (Dolibarr
// PaymentByBankTransfer): debtor pays suppliers, exported as pain.001.
type Transfer struct {
	ID          int64          `json:"id"`
	EntityID    int64          `json:"entity_id"`
	Ref         string         `json:"ref"` // unique per entity
	DebtorName  string         `json:"debtor_name"`
	DebtorIBAN  string         `json:"debtor_iban"`
	DebtorBIC   string         `json:"debtor_bic"`
	RequestedAt time.Time      `json:"requested_at"`
	Lines       []CreditLine   `json:"lines"`
	Status      TransferStatus `json:"status"`
	CreatedAt   time.Time      `json:"created_at"`
	RowVersion  int64          `json:"row_version"`
}

// Validate checks transfer invariants.
func (t Transfer) Validate() error {
	if t.EntityID <= 0 {
		return fmt.Errorf("sepa: entity_id required: %w", platform.ErrValidation)
	}
	if strings.TrimSpace(t.Ref) == "" {
		return fmt.Errorf("sepa: ref required: %w", platform.ErrValidation)
	}
	if strings.TrimSpace(t.DebtorName) == "" {
		return fmt.Errorf("sepa: debtor name required: %w", platform.ErrValidation)
	}
	if err := CheckIBAN(t.DebtorIBAN); err != nil {
		return err
	}
	if err := CheckBIC(t.DebtorBIC); err != nil {
		return err
	}
	if t.RequestedAt.IsZero() {
		return fmt.Errorf("sepa: requested_at required: %w", platform.ErrValidation)
	}
	if len(t.Lines) == 0 {
		return fmt.Errorf("sepa: at least one line required: %w", platform.ErrValidation)
	}
	seen := map[string]bool{}
	for _, l := range t.Lines {
		if err := l.Validate(); err != nil {
			return fmt.Errorf("%w: %w", err, platform.ErrValidation)
		}
		if seen[l.EndToEndID] {
			return fmt.Errorf("sepa: duplicate end_to_end_id %q: %w", l.EndToEndID, platform.ErrConflict)
		}
		seen[l.EndToEndID] = true
	}
	return nil
}

// CanTransition reports whether a transfer status change is legal.
func (t Transfer) CanTransition(to TransferStatus) bool {
	switch t.Status {
	case TransferDraft:
		return to == TransferValidated || to == TransferCanceled
	case TransferValidated:
		return to == TransferSent || to == TransferCanceled
	default:
		return false
	}
}

// Total sums the transfer in cents.
func (t Transfer) Total() int64 {
	var sum int64
	for _, l := range t.Lines {
		sum += l.Amount
	}
	return sum
}

// formatMinor renders minor units as major.minor without floats
// (int64 math only; money is never float).
func formatMinor(v int64) string {
	neg := v < 0
	if neg {
		v = -v
	}
	s := fmt.Sprintf("%d.%02d", v/100, v%100)
	if neg {
		return "-" + s
	}
	return s
}

// pain01 is a simplified pain.001.001.03 customer credit transfer.
type pain01 struct {
	XMLName xml.Name    `xml:"Document"`
	Xmlns   string      `xml:"xmlns,attr"`
	Cstmr   cstmrCdtTrf `xml:"CstmrCdtTrfInitn"`
}

type cstmrCdtTrf struct {
	GrpHdr grpHdr01 `xml:"GrpHdr"`
	PmtInf pmtInf01 `xml:"PmtInf"`
}

type grpHdr01 struct {
	MsgID    string   `xml:"MsgId"`
	CreDtTm  string   `xml:"CreDtTm"`
	NbOfTxs  int      `xml:"NbOfTxs"`
	CtrlSum  string   `xml:"CtrlSum"`
	InitgPty initgPty `xml:"InitgPty"`
}

type pmtInf01 struct {
	PmtInfID    string     `xml:"PmtInfId"`
	PmtMtd      string     `xml:"PmtMtd"`
	NbOfTxs     int        `xml:"NbOfTxs"`
	CtrlSum     string     `xml:"CtrlSum"`
	ReqdExctnDt string     `xml:"ReqdExctnDt"`
	Dbtr        nmParty    `xml:"Dbtr"`
	DbtrAcct    ibanAcct   `xml:"DbtrAcct"`
	DbtrAgt     bicAgt     `xml:"DbtrAgt"`
	CdtTrfTxInf []cdtTxInf `xml:"CdtTrfTxInf"`
}

type cdtTxInf struct {
	PmtID struct {
		EndToEndID string `xml:"EndToEndId"`
	} `xml:"PmtId"`
	Amt struct {
		Value    string `xml:",chardata"`
		Currency string `xml:"Ccy,attr"`
	} `xml:"Amt"`
	CdtrAgt  bicAgt   `xml:"CdtrAgt"`
	Cdtr     nmParty  `xml:"Cdtr"`
	CdtrAcct ibanAcct `xml:"CdtrAcct"`
	RmtInf   struct {
		Ustrd string `xml:"Ustrd"`
	} `xml:"RmtInf"`
}

// ExportPain001 renders the pain.001 document (validated transfers only).
func ExportPain001(t Transfer, now time.Time) ([]byte, error) {
	if t.Status != TransferValidated && t.Status != TransferSent {
		return nil, fmt.Errorf("sepa: export validated transfers only: %w", platform.ErrValidation)
	}
	if err := t.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	info := pmtInf01{
		PmtInfID: t.Ref, PmtMtd: "TRF",
		NbOfTxs: len(t.Lines), CtrlSum: formatMinor(t.Total()),
		ReqdExctnDt: t.RequestedAt.Format("2006-01-02"),
	}
	info.Dbtr.Nm = t.DebtorName
	info.DbtrAcct.ID.IBAN = strings.ReplaceAll(t.DebtorIBAN, " ", "")
	info.DbtrAgt.FinInstnID.BIC = t.DebtorBIC
	for _, l := range t.Lines {
		var tx cdtTxInf
		tx.PmtID.EndToEndID = l.EndToEndID
		tx.Amt.Value = formatMinor(l.Amount)
		tx.Amt.Currency = "EUR"
		tx.Cdtr.Nm = l.CreditorName
		tx.CdtrAcct.ID.IBAN = strings.ReplaceAll(l.IBAN, " ", "")
		if l.BIC != "" {
			tx.CdtrAgt.FinInstnID.BIC = l.BIC
		} else {
			tx.CdtrAgt.FinInstnID.BIC = "NOTPROVIDED"
		}
		tx.RmtInf.Ustrd = l.Remittance
		info.CdtTrfTxInf = append(info.CdtTrfTxInf, tx)
	}
	doc := pain01{Xmlns: "urn:iso:std:iso:20022:tech:xsd:pain.001.001.03"}
	doc.Cstmr.GrpHdr = grpHdr01{MsgID: t.Ref, CreDtTm: now.Format("2006-01-02T15:04:05"),
		NbOfTxs: len(t.Lines), CtrlSum: formatMinor(t.Total())}
	doc.Cstmr.GrpHdr.InitgPty.Nm = t.DebtorName
	doc.Cstmr.PmtInf = info
	raw, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), raw...), nil
}

type transferStore interface {
	CreateTransfer(ctx context.Context, db platform.DBTX, t *Transfer) error
	TransferByID(ctx context.Context, db platform.DBTX, entityID int64, id int64) (Transfer, error)
	ListTransfers(ctx context.Context, db platform.DBTX, entityID int64) ([]Transfer, error)
	SetTransferStatus(ctx context.Context, db platform.DBTX, entityID int64, id int64, to TransferStatus, rowVersion int64) (Transfer, error)
}

const transferCols = `id, entity_id, ref, debtor_name, debtor_iban, debtor_bic, requested_at, lines, status, created_at, row_version`

func scanTransfer(row pgx.Row) (Transfer, error) {
	var t Transfer
	var lines []byte
	err := row.Scan(&t.ID, &t.EntityID, &t.Ref, &t.DebtorName, &t.DebtorIBAN,
		&t.DebtorBIC, &t.RequestedAt, &lines, &t.Status, &t.CreatedAt, &t.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return Transfer{}, identity.ErrNotFound
	}
	if err != nil {
		return Transfer{}, err
	}
	_ = json.Unmarshal(lines, &t.Lines)
	return t, nil
}

func (s *PGStore) CreateTransfer(ctx context.Context, db platform.DBTX, t *Transfer) error {
	if err := t.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	raw, _ := json.Marshal(t.Lines)
	return db.QueryRow(ctx, `INSERT INTO ferp_sepa_transfers
		(entity_id, ref, debtor_name, debtor_iban, debtor_bic, requested_at, lines, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id, row_version`,
		t.EntityID, t.Ref, t.DebtorName, t.DebtorIBAN, t.DebtorBIC,
		t.RequestedAt, raw, t.Status,
	).Scan(&t.ID, &t.RowVersion)
}

func (s *PGStore) TransferByID(ctx context.Context, db platform.DBTX, entityID int64, id int64) (Transfer, error) {
	return scanTransfer(db.QueryRow(ctx, `SELECT `+transferCols+` FROM ferp_sepa_transfers WHERE id=$1 AND entity_id=$2`, id, entityID))
}

func (s *PGStore) ListTransfers(ctx context.Context, db platform.DBTX, entityID int64) ([]Transfer, error) {
	rows, err := db.Query(ctx, `SELECT `+transferCols+` FROM ferp_sepa_transfers WHERE entity_id=$1 ORDER BY id`, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Transfer
	for rows.Next() {
		t, err := scanTransfer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *PGStore) SetTransferStatus(ctx context.Context, db platform.DBTX, entityID int64, id int64, to TransferStatus, rowVersion int64) (Transfer, error) {
	t, err := s.TransferByID(ctx, db, entityID, id)
	if err != nil {
		return Transfer{}, err
	}
	if t.RowVersion != rowVersion {
		return Transfer{}, identity.ErrVersionConflict
	}
	if !t.CanTransition(to) {
		return Transfer{}, fmt.Errorf("sepa: illegal transition: %w", platform.ErrValidation)
	}
	tag, err := db.Exec(ctx, `UPDATE ferp_sepa_transfers SET status=$1, row_version=row_version+1
		WHERE id=$2 AND row_version=$3 AND entity_id=$4`, to, id, rowVersion, entityID)
	if err != nil {
		return Transfer{}, err
	}
	if tag.RowsAffected() == 0 {
		return Transfer{}, identity.ErrVersionConflict
	}
	t.Status = to
	t.RowVersion++
	return t, nil
}

func (m *MemoryStore) CreateTransfer(_ context.Context, _ platform.DBTX, t *Transfer) error {
	if err := t.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.transfers {
		if e.EntityID == t.EntityID && e.Ref == t.Ref {
			return fmt.Errorf("sepa: duplicate ref: %w", platform.ErrConflict)
		}
	}
	m.tseq++
	t.ID = m.tseq
	t.RowVersion = 1
	m.transfers[t.ID] = *t
	return nil
}

func (m *MemoryStore) TransferByID(_ context.Context, _ platform.DBTX, entityID int64, id int64) (Transfer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.transfers[id]
	if !ok || t.EntityID != entityID {
		return Transfer{}, identity.ErrNotFound
	}
	return t, nil
}

func (m *MemoryStore) ListTransfers(_ context.Context, _ platform.DBTX, entityID int64) ([]Transfer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Transfer
	for _, t := range m.transfers {
		if t.EntityID == entityID {
			out = append(out, t)
		}
	}
	return out, nil
}

func (m *MemoryStore) SetTransferStatus(_ context.Context, _ platform.DBTX, entityID int64, id int64, to TransferStatus, rowVersion int64) (Transfer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.transfers[id]
	if !ok || t.EntityID != entityID {
		return Transfer{}, identity.ErrNotFound
	}
	if t.RowVersion != rowVersion {
		return Transfer{}, identity.ErrVersionConflict
	}
	if !t.CanTransition(to) {
		return Transfer{}, fmt.Errorf("sepa: illegal transition: %w", platform.ErrValidation)
	}
	t.Status = to
	t.RowVersion++
	m.transfers[id] = t
	return t, nil
}
