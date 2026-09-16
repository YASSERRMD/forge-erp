package dataio

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/YASSERRMD/forge-erp/backend/internal/documents"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// registry resolves a context key to its adapter. Members/sales adapters are
// present only when wired (main.go must pass them); orgs/products always are.
func (h *Handler) registry(docType documents.DocType) map[string]Adapter {
	reg := map[string]Adapter{
		"orgs":     orgAdapter{store: h.deps.Orgs},
		"products": productAdapter{store: h.deps.Products},
	}
	if h.deps.Members != nil {
		reg["members"] = memberAdapter{store: h.deps.Members}
	}
	if h.deps.Sales != nil {
		if docType == "" {
			docType = documents.TypeInvoice
		}
		reg["sales"] = salesAdapter{store: h.deps.Sales, docType: docType}
	}
	return reg
}

// RoutesExchange mounts the generic import/export surface (called from Routes).
func RoutesExchange(r chi.Router, h *Handler, mw Middleware) {
	r.With(mw("dataio", "import", "write")).Post("/imports", h.ImportGeneric)
	r.With(mw("dataio", "export", "read")).Get("/exports/{context}", h.ExportGeneric)
}

// ImportRequest takes {context, mapping, dry_run} plus the CSV payload.
// mapping translates CSV header names to canonical field names; when empty,
// identically-named columns map automatically.
type ImportRequest struct {
	Context string            `json:"context"`
	Mapping map[string]string `json:"mapping"`
	DryRun  bool              `json:"dry_run"`
	CSV     string            `json:"csv"`
}

// ImportGeneric runs a per-context import with per-row results. Dry-run mode
// returns row errors without writing anything.
func (h *Handler) ImportGeneric(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var in ImportRequest
	defer r.Body.Close()
	if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 4<<20)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "dataio: bad request")
		return
	}
	ad, ok := h.registry("")[in.Context]
	if !ok {
		writeErr(w, http.StatusBadRequest, "dataio: unknown context "+in.Context)
		return
	}
	header, rows, err := parseCSVText(in.CSV)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	mapping := in.Mapping
	if len(mapping) == 0 {
		mapping = DefaultMapping(ad, header)
	}
	recs, err := ApplyMapping(ad, header, rows, mapping)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	rep := RunImport(r.Context(), h.deps.DB, entityID, ad, recs, in.DryRun)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(rep)
}

// ExportGeneric streams one context as CSV (canonical header + quoted rows).
// Sales accepts ?type=proposal|order|shipment|invoice|credit_note (default invoice).
func (h *Handler) ExportGeneric(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	ctxKey := chi.URLParam(r, "context")
	var dt documents.DocType
	if ctxKey == "sales" {
		dt = documents.DocType(strings.TrimSpace(r.URL.Query().Get("type")))
		if dt == "" {
			dt = documents.TypeInvoice
		}
	}
	ad, ok := h.registry(dt)[ctxKey]
	if !ok {
		writeErr(w, http.StatusBadRequest, "dataio: unknown context "+ctxKey)
		return
	}
	rows, err := ad.Export(r.Context(), h.deps.DB, entityID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "export failed")
		return
	}
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", `attachment; filename="`+ctxKey+`.csv"`)
	cw := csv.NewWriter(w)
	defer cw.Flush()
	_ = cw.Write(ad.Columns())
	_ = cw.WriteAll(rows)
}

func parseCSVText(text string) ([]string, [][]string, error) {
	if strings.TrimSpace(text) == "" {
		return nil, nil, errEmptyCSV("dataio: empty CSV")
	}
	rows, err := csv.NewReader(strings.NewReader(text)).ReadAll()
	if err != nil {
		return nil, nil, errEmptyCSV("dataio: malformed CSV")
	}
	if len(rows) == 0 {
		return nil, nil, errEmptyCSV("dataio: empty CSV")
	}
	header := make([]string, len(rows[0]))
	for i, c := range rows[0] {
		header[i] = strings.TrimSpace(c)
	}
	return header, rows[1:], nil
}

type csvError string

func errEmptyCSV(s string) csvError { return csvError(s) }

func (e csvError) Error() string { return string(e) }

// RenderCSV renders header + rows with standard quoting (used by tests and
// callers that need a CSV string instead of the streaming endpoint).
func RenderCSV(header []string, rows [][]string) string {
	var buf bytes.Buffer
	cw := csv.NewWriter(&buf)
	_ = cw.Write(header)
	_ = cw.WriteAll(rows)
	cw.Flush()
	return buf.String()
}
