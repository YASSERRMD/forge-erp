package sepa

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/finance"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform/pgtest"
	"github.com/go-chi/chi/v5"
)

type stubLedger struct{ entries []*finance.Entry }

func (s *stubLedger) PostEntry(_ context.Context, _ platform.DBTX, e *finance.Entry) error {
	s.entries = append(s.entries, e)
	return nil
}

func mandateBody(umr string) map[string]any {
	return map[string]any{
		"umr": umr, "debtor_name": "Client A",
		"iban": "DE89370400440532013000", "bic": "COBADEFFXXX",
		"sequence": "FRST",
	}
}

func phase2Router(st *MemoryStore, led Ledger) *chi.Mux {
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		Routes(r, Deps{Store: st, Ledger: led}, passthrough)
	})
	return r
}

func doReq(r http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestMandateLifecycle(t *testing.T) {
	ctx := context.Background()
	st := NewMemoryStore()

	m := &Mandate{EntityID: 1, UMR: "UMR-1", DebtorName: "Client A",
		IBAN: "DE89370400440532013000", BIC: "COBADEFFXXX", Sequence: "FRST"}
	if err := st.CreateMandate(ctx, nil, m); err != nil {
		t.Fatalf("create: %v", err)
	}
	if m.Status != MandateDraft || m.RowVersion != 1 {
		t.Fatalf("fresh mandate status=%d rv=%d", m.Status, m.RowVersion)
	}

	// Duplicate UMR per entity conflicts; other entities may reuse it.
	dup := &Mandate{EntityID: 1, UMR: "UMR-1", DebtorName: "D",
		IBAN: "DE89370400440532013000", Sequence: "RCUR"}
	if err := st.CreateMandate(ctx, nil, dup); !errors.Is(err, platform.ErrConflict) {
		t.Fatalf("duplicate umr err=%v want conflict", err)
	}
	other := &Mandate{EntityID: 2, UMR: "UMR-1", DebtorName: "D",
		IBAN: "DE89370400440532013000", Sequence: "RCUR"}
	if err := st.CreateMandate(ctx, nil, other); err != nil {
		t.Fatalf("cross-entity umr: %v", err)
	}

	// Invalid IBAN rejected.
	bad := &Mandate{EntityID: 1, UMR: "UMR-BAD", DebtorName: "D",
		IBAN: "DE89370400440532013001", Sequence: "FRST"}
	if err := st.CreateMandate(ctx, nil, bad); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("bad iban err=%v want validation", err)
	}

	// Sign needs a date and a draft.
	if _, err := st.SignMandate(ctx, nil, 1, m.ID, time.Time{}, m.RowVersion); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("zero signed_at err=%v want validation", err)
	}
	signedAt := time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)
	signed, err := st.SignMandate(ctx, nil, 1, m.ID, signedAt, m.RowVersion)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if signed.Status != MandateActive || signed.SignedAt == nil {
		t.Fatalf("signed status=%d", signed.Status)
	}
	if _, err := st.SignMandate(ctx, nil, 1, m.ID, signedAt, signed.RowVersion); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("double sign err=%v want validation", err)
	}

	// Amend keeps active, stamps amended_at; bad IBAN rejected.
	amended, err := st.AmendMandate(ctx, nil, 1, m.ID, "Client A2", "FR1420041010050500013M02606", "", signed.RowVersion)
	if err != nil {
		t.Fatalf("amend: %v", err)
	}
	if amended.Status != MandateActive || amended.AmendedAt == nil || amended.DebtorName != "Client A2" {
		t.Fatalf("amended: %+v", amended)
	}
	if _, err := st.AmendMandate(ctx, nil, 1, m.ID, "X", "XX00", "", amended.RowVersion); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("amend bad iban err=%v want validation", err)
	}
	// Draft mandates are not amendable.
	draft := &Mandate{EntityID: 1, UMR: "UMR-D", DebtorName: "D",
		IBAN: "DE89370400440532013000", Sequence: "OFF"}
	if err := st.CreateMandate(ctx, nil, draft); err != nil {
		t.Fatalf("create draft: %v", err)
	}
	if _, err := st.AmendMandate(ctx, nil, 1, draft.ID, "D", "DE89370400440532013000", "", draft.RowVersion); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("amend draft err=%v want validation", err)
	}

	// Cancel from active, then terminal.
	canceled, err := st.CancelMandate(ctx, nil, 1, m.ID, amended.RowVersion)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if canceled.Status != MandateCanceled {
		t.Fatalf("canceled status=%d", canceled.Status)
	}
	if _, err := st.CancelMandate(ctx, nil, 1, m.ID, canceled.RowVersion); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("cancel terminal err=%v want validation", err)
	}

	// Cross-tenant isolation.
	if _, err := st.MandateByID(ctx, nil, 2, m.ID); !isNotFound(err) {
		t.Fatalf("cross-tenant err=%v want not-found", err)
	}
}

func TestMandateHTTPFlow(t *testing.T) {
	st := NewMemoryStore()
	r := phase2Router(st, nil)

	rec := doReq(r, http.MethodPost, "/api/v1/sepa/mandates", mandateBody("HTTP-1"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var m Mandate
	_ = json.NewDecoder(rec.Body).Decode(&m)

	rec = doReq(r, http.MethodPost, "/api/v1/sepa/mandates/"+itoa(m.ID)+"/sign",
		map[string]any{"signed_at": "2026-03-02T10:00:00Z", "row_version": m.RowVersion})
	if rec.Code != http.StatusOK {
		t.Fatalf("sign: code=%d body=%s", rec.Code, rec.Body.String())
	}
	_ = json.NewDecoder(rec.Body).Decode(&m)
	rec = doReq(r, http.MethodPost, "/api/v1/sepa/mandates/"+itoa(m.ID)+"/cancel",
		map[string]any{"row_version": m.RowVersion})
	if rec.Code != http.StatusOK {
		t.Fatalf("cancel: code=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestPain001Golden(t *testing.T) {
	now := time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)
	tr := Transfer{EntityID: 1, Ref: "TRF-1", DebtorName: "ForgeERP SARL",
		DebtorIBAN: "FR1420041010050500013M02606", DebtorBIC: "AGRIFRPP",
		RequestedAt: time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC),
		Status:      TransferValidated,
		Lines: []CreditLine{
			{CreditorName: "Supplier A", IBAN: "DE89370400440532013000",
				BIC: "COBADEFFXXX", Amount: 2599, Remittance: "INV-S1", EndToEndID: "C-E2E-1"},
			{CreditorName: "Supplier B", IBAN: "GB29NWBK60161331926819",
				Amount: 105, Remittance: "INV-S2", EndToEndID: "C-E2E-2"},
		}}
	raw, err := ExportPain001(tr, now)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	xml := string(raw)
	for _, want := range []string{
		"pain.001.001.03", "TRF-1", "CdtTrfTxInf", "27.04", "25.99", "1.05",
		"Supplier A", "Supplier B", "DE89370400440532013000",
		"FR1420041010050500013M02606", "C-E2E-1", "2026-03-02T10:00:00", "TRF",
	} {
		if !strings.Contains(xml, want) {
			t.Fatalf("xml missing %q:\n%s", want, xml)
		}
	}
	if strings.Contains(xml, "DrctDbt") {
		t.Fatal("pain.001 must not contain direct-debit elements")
	}
	// Draft export rejected.
	tr.Status = TransferDraft
	if _, err := ExportPain001(tr, now); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("draft export err=%v want validation", err)
	}
}

func TestTransferHTTPFlow(t *testing.T) {
	st := NewMemoryStore()
	r := phase2Router(st, nil)
	body := map[string]any{
		"ref": "TRF-HTTP", "debtor_name": "ForgeERP SARL",
		"debtor_iban": "FR1420041010050500013M02606", "debtor_bic": "AGRIFRPP",
		"requested_at": time.Now().UTC().Add(48 * time.Hour),
		"lines": []map[string]any{{
			"creditor_name": "Supplier A", "iban": "DE89370400440532013000",
			"bic": "COBADEFFXXX", "amount": 1005,
			"remittance": "INV-S1", "end_to_end_id": "T-E2E-1",
		}},
	}
	rec := doReq(r, http.MethodPost, "/api/v1/sepa/transfers", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var tr Transfer
	_ = json.NewDecoder(rec.Body).Decode(&tr)
	if tr.Total() != 1005 {
		t.Fatalf("total=%d", tr.Total())
	}
	// Draft export rejected over HTTP.
	rec = doReq(r, http.MethodGet, "/api/v1/sepa/transfers/"+itoa(tr.ID)+"/xml", nil)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("draft export: code=%d want 422", rec.Code)
	}
	rec = doReq(r, http.MethodPost, "/api/v1/sepa/transfers/"+itoa(tr.ID)+"/status",
		map[string]any{"status": 1, "row_version": tr.RowVersion})
	if rec.Code != http.StatusOK {
		t.Fatalf("validate: code=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doReq(r, http.MethodGet, "/api/v1/sepa/transfers/"+itoa(tr.ID)+"/xml", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("export: code=%d", rec.Code)
	}
	for _, want := range []string{"pain.001.001.03", "TRF-HTTP", "10.05"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("xml missing %q", want)
		}
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/xml" {
		t.Fatalf("content-type=%q", ct)
	}
}

func sentBatch(t *testing.T, ctx context.Context, st *MemoryStore) Batch {
	t.Helper()
	b := &Batch{EntityID: 1, Ref: "R-BATCH", CreditorName: "ForgeERP SARL",
		CreditorIBAN: "FR1420041010050500013M02606", CreditorBIC: "AGRIFRPP",
		CreditorID: "FR12ZZZ123456", Sequence: "RCUR",
		RequestedAt: time.Now().UTC().Add(48 * time.Hour),
		Transactions: []Transaction{{DebtorName: "Client A",
			IBAN: "DE89370400440532013000", Amount: 2599,
			Remittance: "INV-1", EndToEndID: "R-E2E-1"}}}
	if err := st.CreateBatch(ctx, nil, b); err != nil {
		t.Fatalf("batch: %v", err)
	}
	var err error
	*b, err = st.SetBatchStatus(ctx, nil, 1, b.ID, BatchValidated, b.RowVersion)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	*b, err = st.SetBatchStatus(ctx, nil, 1, b.ID, BatchSent, b.RowVersion)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	return *b
}

func TestRTransitions(t *testing.T) {
	ctx := context.Background()
	st := NewMemoryStore()
	led := &stubLedger{}
	svc := NewService(nil, st, led, nil)

	b := sentBatch(t, ctx, st)

	got, err := svc.RecordR(ctx, RCmd{EntityID: 1, BatchID: b.ID,
		EndToEndID: "R-E2E-1", Kind: RReturn, Reason: "MD01",
		JournalID: 7, ReceivableAccount: 411, BankAccount: 512})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if got.Amount != 2599 || got.LedgerRef == "" {
		t.Fatalf("r=%+v", got)
	}
	if len(led.entries) != 1 {
		t.Fatalf("ledger entries=%d want 1", len(led.entries))
	}
	e := led.entries[0]
	if e.JournalID != 7 || len(e.Lines) != 2 ||
		e.Lines[0].Debit != 2599 || e.Lines[1].Credit != 2599 {
		t.Fatalf("reversal legs: %+v", e.Lines)
	}

	// Duplicate R on the same item conflicts.
	if _, err := svc.RecordR(ctx, RCmd{EntityID: 1, BatchID: b.ID,
		EndToEndID: "R-E2E-1", Kind: RRefund, Reason: "MD01"}); !errors.Is(err, platform.ErrConflict) {
		t.Fatalf("duplicate R err=%v want conflict", err)
	}
	// Unknown item is not found.
	if _, err := svc.RecordR(ctx, RCmd{EntityID: 1, BatchID: b.ID,
		EndToEndID: "NOPE", Kind: RReject, Reason: "AC01"}); !errors.Is(err, platform.ErrNotFound) {
		t.Fatalf("unknown item err=%v want not-found", err)
	}
	// Bad kind / empty reason rejected.
	if _, err := svc.RecordR(ctx, RCmd{EntityID: 1, BatchID: b.ID,
		EndToEndID: "R-E2E-1", Kind: "BOGUS", Reason: "AC01"}); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("bad kind err=%v want validation", err)
	}
	// Draft batches cannot take R-transactions.
	draft := &Batch{EntityID: 1, Ref: "R-DRAFT", CreditorName: "C",
		CreditorIBAN: "FR1420041010050500013M02606", CreditorBIC: "AGRIFRPP",
		CreditorID: "ID", Sequence: "RCUR", RequestedAt: time.Now().UTC().Add(24 * time.Hour),
		Transactions: []Transaction{{DebtorName: "D", IBAN: "DE89370400440532013000",
			Amount: 100, Remittance: "R", EndToEndID: "D-E2E"}}}
	if err := st.CreateBatch(ctx, nil, draft); err != nil {
		t.Fatalf("draft: %v", err)
	}
	if _, err := svc.RecordR(ctx, RCmd{EntityID: 1, BatchID: draft.ID,
		EndToEndID: "D-E2E", Kind: RReject, Reason: "AC01"}); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("draft R err=%v want validation", err)
	}

	list, err := st.ListRTransactions(ctx, nil, 1, b.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("list=%d err=%v", len(list), err)
	}
}

func TestRHTTPFlow(t *testing.T) {
	ctx := context.Background()
	st := NewMemoryStore()
	b := sentBatch(t, ctx, st)
	r := phase2Router(st, nil)

	rec := doReq(r, http.MethodPost, "/api/v1/sepa/batches/"+itoa(b.ID)+"/rtransactions",
		map[string]any{"end_to_end_id": "R-E2E-1", "kind": "RETURN", "reason": "MD01"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("record: code=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doReq(r, http.MethodGet, "/api/v1/sepa/batches/"+itoa(b.ID)+"/rtransactions", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: code=%d", rec.Code)
	}
	var list []RTransaction
	_ = json.NewDecoder(rec.Body).Decode(&list)
	if len(list) != 1 {
		t.Fatalf("list=%d", len(list))
	}
}

func TestFormatMinor(t *testing.T) {
	for in, want := range map[int64]string{0: "0.00", 5: "0.05", 105: "1.05", 2500: "25.00", 100: "1.00"} {
		if got := formatMinor(in); got != want {
			t.Errorf("formatMinor(%d)=%q want %q", in, got, want)
		}
	}
}

func TestPGMandates(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	st := NewPGStore(pool)
	m := &Mandate{EntityID: 1, UMR: "PG-UMR", DebtorName: "Client A",
		IBAN: "DE89370400440532013000", BIC: "COBADEFFXXX", Sequence: "FRST"}
	if err := st.CreateMandate(ctx, pool, m); err != nil {
		t.Fatalf("create: %v", err)
	}
	if m.ID == 0 || m.RowVersion != 1 {
		t.Fatalf("m=%+v", m)
	}
	dup := &Mandate{EntityID: 1, UMR: "PG-UMR", DebtorName: "D",
		IBAN: "DE89370400440532013000", Sequence: "RCUR"}
	if err := st.CreateMandate(ctx, pool, dup); err == nil {
		t.Fatal("duplicate umr accepted")
	}
	signedAt := time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)
	signed, err := st.SignMandate(ctx, pool, 1, m.ID, signedAt, m.RowVersion)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	amended, err := st.AmendMandate(ctx, pool, 1, m.ID, "Client A2",
		"FR1420041010050500013M02606", "", signed.RowVersion)
	if err != nil {
		t.Fatalf("amend: %v", err)
	}
	if amended.AmendedAt == nil {
		t.Fatal("amended_at not stamped")
	}
	canceled, err := st.CancelMandate(ctx, pool, 1, m.ID, amended.RowVersion)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if canceled.Status != MandateCanceled {
		t.Fatalf("status=%d", canceled.Status)
	}
	list, err := st.ListMandates(ctx, pool, 1)
	if err != nil || len(list) != 1 {
		t.Fatalf("list=%d err=%v", len(list), err)
	}
}

func TestPGTransfersAndR(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	st := NewPGStore(pool)
	tr := &Transfer{EntityID: 1, Ref: "PG-TRF", DebtorName: "ForgeERP SARL",
		DebtorIBAN: "FR1420041010050500013M02606", DebtorBIC: "AGRIFRPP",
		RequestedAt: time.Now().UTC().Add(24 * time.Hour),
		Lines: []CreditLine{{CreditorName: "Supplier A",
			IBAN: "DE89370400440532013000", Amount: 100,
			Remittance: "R", EndToEndID: "PG-T-E2E"}}}
	if err := st.CreateTransfer(ctx, pool, tr); err != nil {
		t.Fatalf("transfer: %v", err)
	}
	if _, err := st.SetTransferStatus(ctx, pool, 1, tr.ID, TransferValidated, tr.RowVersion); err != nil {
		t.Fatalf("validate: %v", err)
	}

	b := &Batch{EntityID: 1, Ref: "PG-R-BATCH", CreditorName: "C",
		CreditorIBAN: "FR1420041010050500013M02606", CreditorBIC: "AGRIFRPP",
		CreditorID: "ID", Sequence: "RCUR", RequestedAt: time.Now().UTC().Add(24 * time.Hour),
		Transactions: []Transaction{{DebtorName: "D", IBAN: "DE89370400440532013000",
			Amount: 100, Remittance: "R", EndToEndID: "PG-R-E2E"}}}
	if err := st.CreateBatch(ctx, pool, b); err != nil {
		t.Fatalf("batch: %v", err)
	}
	if _, err := st.SetBatchStatus(ctx, pool, 1, b.ID, BatchValidated, b.RowVersion); err != nil {
		t.Fatalf("validate batch: %v", err)
	}
	rt := &RTransaction{EntityID: 1, BatchID: b.ID, EndToEndID: "PG-R-E2E",
		Kind: RReject, Reason: "AC01", Amount: 100, LedgerRef: "SEPA-R-x"}
	if err := st.RecordRTransaction(ctx, pool, rt); err != nil {
		t.Fatalf("record R: %v", err)
	}
	list, err := st.ListRTransactions(ctx, pool, 1, b.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("list=%d err=%v", len(list), err)
	}
}
