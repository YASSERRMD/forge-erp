package label

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform/docgen"
	"github.com/jung-kurt/gofpdf"
)

// frozenCreationDate stamps label PDFs (mirrors docgen determinism:
// identical subject bytes render byte-identical PDFs).
var frozenCreationDate = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

// LabelModel renders the label family through the docgen registry
// (docgen used as a library — this package never modifies it).
type LabelModel struct{}

// Code implements docgen.DocModel.
func (LabelModel) Code() string { return "label-sheet" }

// Applies implements docgen.DocModel (label family only).
func (LabelModel) Applies(docType string) bool { return docType == "label" }

// Render implements docgen.DocModel: one bordered cell per label, laid out
// with SheetDef.CellXY, paginating by LabelsPerSheet.
func (LabelModel) Render(_ context.Context, subject any, _ string) (io.Reader, string, error) {
	sub, ok := subject.(LabelSubject)
	if !ok {
		return nil, "", fmt.Errorf("label: model needs LabelSubject: %w", platform.ErrValidation)
	}
	if err := sub.Sheet.Validate(); err != nil {
		return nil, "", err
	}
	if len(sub.Rows) == 0 {
		return nil, "", fmt.Errorf("label: no rows to print: %w", platform.ErrValidation)
	}
	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.SetCreationDate(frozenCreationDate)
	pdf.SetModificationDate(frozenCreationDate)
	pdf.SetCatalogSort(true)
	pdf.SetTitle(sub.Title, false)
	pdf.SetAuthor("ForgeERP", false)
	pdf.SetMargins(0, 0, 0)
	pdf.SetAutoPageBreak(false, 0)
	var lastPage int64 = -1
	for i, row := range sub.Rows {
		page, x, y, err := sub.Sheet.CellXY(int64(i))
		if err != nil {
			return nil, "", err
		}
		if page != lastPage {
			pdf.AddPage()
			lastPage = page
		}
		pdf.SetXY(x, y)
		lines := row.Lines(sub.Sheet.Fields)
		pdf.SetFont("Helvetica", "", 8)
		pdf.CellFormat(sub.Sheet.LabelWMM, sub.Sheet.LabelHMM, "", "1", 0, "", false, 0, "")
		n := len(lines)
		if n == 0 {
			n = 1
		}
		lineH := sub.Sheet.LabelHMM / float64(n)
		for j, ln := range lines {
			pdf.SetXY(x+1, y+float64(j)*lineH)
			pdf.CellFormat(sub.Sheet.LabelWMM-2, lineH, ln, "", 0, "", false, 0, "")
		}
	}
	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, "", err
	}
	return bytes.NewReader(buf.Bytes()), "application/pdf", nil
}

// RegisterDefault installs the label model into a docgen registry.
func RegisterDefault(r *docgen.Registry) { r.Register(LabelModel{}) }

// Service owns the label boundary: sheet CRUD passes through the store;
// PDF rendering resolves the sheet and dispatches through the docgen
// registry (label model registered by the caller via RegisterDefault,
// falling back to a local model when the registry is nil).
type Service struct {
	Store    Store
	Registry *docgen.Registry
	DB       platform.DBTX
	Bus      platform.Bus
}

func (s *Service) registry() *docgen.Registry {
	if s.Registry != nil {
		return s.Registry
	}
	r := docgen.NewRegistry()
	r.Register(LabelModel{})
	return r
}

func (s *Service) published(ctx context.Context, entityID, id int64, subject, entity string) {
	if s.Bus == nil {
		return
	}
	_ = s.Bus.Publish(ctx, platform.Event{Subject: subject, Entity: entity, EntityID: entityID, ID: id})
}

// RenderPDF renders rows onto the named sheet (by code) and returns PDF bytes.
func (s *Service) RenderPDF(ctx context.Context, entityID int64, code string, rows []LabelRow) ([]byte, string, error) {
	sheet, err := s.Store.SheetByCode(ctx, s.DB, entityID, code)
	if err != nil {
		return nil, "", err
	}
	model, err := s.registry().Lookup(LabelModel{}.Code())
	if err != nil {
		return nil, "", err
	}
	reader, contentType, err := model.Render(ctx, LabelSubject{Sheet: sheet, Rows: rows, Title: sheet.Code}, "")
	if err != nil {
		return nil, "", err
	}
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(reader); err != nil {
		return nil, "", err
	}
	s.published(ctx, entityID, sheet.ID, "forgeerp.label.sheet.rendered.v1", "sheet")
	return buf.Bytes(), contentType, nil
}
