// Command api is the ForgeERP modular-monolith server entrypoint.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/YASSERRMD/forge-erp/backend/internal/catalog"
	"github.com/YASSERRMD/forge-erp/backend/internal/documents"
	"github.com/YASSERRMD/forge-erp/backend/internal/documentsvc"
	"github.com/YASSERRMD/forge-erp/backend/internal/finance"
	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
	"github.com/YASSERRMD/forge-erp/backend/internal/hr"
	"github.com/YASSERRMD/forge-erp/backend/internal/manufacturing"
	"github.com/YASSERRMD/forge-erp/backend/internal/partners"
	"github.com/YASSERRMD/forge-erp/backend/internal/payments"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/YASSERRMD/forge-erp/backend/internal/pos"
	"github.com/YASSERRMD/forge-erp/backend/internal/procurement"
	"github.com/YASSERRMD/forge-erp/backend/internal/reporting"
	"github.com/YASSERRMD/forge-erp/backend/internal/sales"
	"github.com/YASSERRMD/forge-erp/backend/internal/search"
	"github.com/YASSERRMD/forge-erp/backend/internal/services"
	"github.com/YASSERRMD/forge-erp/backend/migrations"
)

var (
	version = "dev"
	commit  = "none"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("forgeerp: %v", err)
	}
}

func run() error {
	cfg := platform.Load()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := platform.OpenPool(ctx, cfg)
	if err != nil {
		return fmt.Errorf("database not reachable (%v); start it via `docker compose up -d postgres`", err)
	}
	defer pool.Close()

	if err := platform.Migrate(ctx, pool, migrations.FS); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	if n, err := platform.OverlayFromDB(ctx, pool, &cfg); err != nil {
		log.Printf("forgeerp: config overlay skipped: %v", err)
	} else if n > 0 {
		log.Printf("forgeerp: applied %d config overlay keys", n)
	}

	issuer, err := identity.NewIssuer(cfg.JWTSecret)
	if err != nil {
		return fmt.Errorf("jwt: %w", err)
	}
	store := identity.NewPGStore(pool)
	if err := seedAdmin(ctx, cfg, store); err != nil {
		return fmt.Errorf("seed admin: %w", err)
	}
	pstore := partners.NewPGStore(pool)
	if err := seedDemoOrgs(ctx, pstore); err != nil {
		return fmt.Errorf("seed demo orgs: %w", err)
	}
	cstore := catalog.NewPGStore(pool)
	if err := seedDemoCatalog(ctx, cstore); err != nil {
		return fmt.Errorf("seed demo catalog: %w", err)
	}
	sstore := sales.NewPGStore(pool)
	if err := seedDemoSales(ctx, sstore, pstore, cstore); err != nil {
		return fmt.Errorf("seed demo sales: %w", err)
	}
	procstore := procurement.NewPGStore(pool)
	fstore := finance.NewPGStore(pool)
	svcstore := services.NewPGStore(pool)
	mfstore := manufacturing.NewPGStore(pool)
	hrstore := hr.NewPGStore(pool)
	if err := seedDemoFinance(ctx, fstore); err != nil {
		return fmt.Errorf("seed demo finance: %w", err)
	}

	// Cross-cutting (Phase 09): document storage, metrics, search.
	build := platform.BuildInfo{Version: version, Commit: commit}
	metrics := platform.NewMetrics()
	storageDir := os.Getenv("FERP_STORAGE_DIR")
	if storageDir == "" {
		storageDir = "./var/docs"
	}
	getenv := func(k, d string) string {
		if v := os.Getenv(k); v != "" {
			return v
		}
		return d
	}
	var byteStorage documentsvc.Storage
	if documentsvc.S3Enabled(getenv) {
		byteStorage = documentsvc.NewS3Storage(documentsvc.LoadS3Config(getenv))
		log.Print("forgeerp: document storage backend=s3")
	} else {
		dirStorage, err := documentsvc.NewDirStorage(storageDir)
		if err != nil {
			return fmt.Errorf("storage dir: %w", err)
		}
		byteStorage = dirStorage
		log.Print("forgeerp: document storage backend=dir")
	}
	// documentsvc metadata needs a Store; PG metadata lands with the PG adapter —
	// serve metadata in-memory for now is wrong for prod, so wire a minimal PG
	// metadata store inline via pool below.
	docSvc := &documentsvc.Service{Store: documentsvc.NewPGStore(pool), Storage: byteStorage}
	providers := map[string]search.Provider{
		"organization": func(ctx context.Context, entityID int64) ([]search.Result, error) {
			orgs, err := pstore.ListOrgs(ctx, entityID, 500, 0)
			if err != nil {
				return nil, err
			}
			out := make([]search.Result, 0, len(orgs))
			for _, o := range orgs {
				out = append(out, search.Result{ID: o.ID, Label: o.Name, Ref: o.CustomerCode})
			}
			return out, nil
		},
		"product": func(ctx context.Context, entityID int64) ([]search.Result, error) {
			prods, err := cstore.ListProducts(ctx, entityID, 500, 0)
			if err != nil {
				return nil, err
			}
			out := make([]search.Result, 0, len(prods))
			for _, p := range prods {
				out = append(out, search.Result{ID: p.ID, Label: p.Name, Ref: p.SKU})
			}
			return out, nil
		},
	}
	var searcher search.Searcher
	if search.OpenSearchEnabled(getenv) {
		oss := search.NewOpenSearcher(search.LoadOSConfig(getenv), providers)
		if n, err := oss.Reindex(ctx, 1); err != nil {
			log.Printf("forgeerp: search reindex failed (%v); OpenSearch queries fall back to providers", err)
		} else {
			log.Printf("forgeerp: search backend=opensearch (%d docs indexed)", n)
		}
		searcher = oss
	} else {
		mem := search.NewMemorySearcher()
		for scope, p := range providers {
			mem.Register(scope, p)
		}
		log.Print("forgeerp: search backend=memory")
		searcher = mem
	}

	base := platform.Router(build)
	mux, ok := base.(chi.Router)
	if !ok {
		return errors.New("platform router is not a chi router")
	}
	identDeps := identity.Deps{Store: store, Issuer: issuer}
	if kc := identity.LoadKeycloakConfig(func(k, d string) string {
		if v := os.Getenv(k); v != "" {
			return v
		}
		return d
	}); kc.Enabled {
		if verifier, err := identity.NewKeycloakVerifier(kc, nil); err != nil {
			log.Printf("forgeerp: OIDC discovery failed (%v); SSO login disabled", err)
		} else {
			identDeps.OIDC = verifier
			log.Printf("forgeerp: OIDC SSO enabled (realm %s)", kc.Realm)
		}
	} else {
		log.Print("forgeerp: FERP_OIDC_ISSUER unset; SSO login disabled (dev JWT only)")
	}
	idH := identity.NewHandler(identDeps)
	mux.Use(metrics.Instrument)
	// Abuse caps: 20 rps burst 40 per IP across the API (login endpoints additionally
	// guarded by per-account lockout in the identity context).
	apiLimiter := platform.NewRateLimiter(20, 40)
	mux.With(apiLimiter.Limit).Route("/api/v1", func(r chi.Router) {
		identity.Routes(r, identDeps)
		partners.Routes(r, partners.Deps{Store: pstore, Bus: platform.NewMemoryBus()},
			idH.Require)
		catalog.Routes(r, catalog.Deps{Store: cstore, Bus: platform.NewMemoryBus()},
			idH.Require)
		sales.Routes(r, sales.Deps{Store: sstore, Catalog: cstore, Bus: platform.NewMemoryBus()},
			idH.Require)
		procurement.Routes(r, procurement.Deps{Store: procstore, Catalog: cstore, Bus: platform.NewMemoryBus()},
			idH.Require)
		finance.Routes(r, finance.Deps{Store: fstore}, idH.Require)
		services.Routes(r, services.Deps{Store: svcstore, Bus: platform.NewMemoryBus()},
			idH.Require)
		manufacturing.Routes(r, manufacturing.Deps{Store: mfstore, Ledger: cstore, Bus: platform.NewMemoryBus()},
			idH.Require)
		hr.Routes(r, hr.Deps{Store: hrstore, Finance: fstore, Bus: platform.NewMemoryBus()}, idH.Require)
		posstore := pos.NewPGStore(pool)
		var walkinOrg int64
		if v := os.Getenv("FERP_POS_WALKIN_ORG"); v != "" {
			_, _ = fmt.Sscanf(v, "%d", &walkinOrg)
		}
		pos.Routes(r, pos.Deps{Store: posstore, Catalog: cstore, Sales: sstore, WalkinOrg: walkinOrg, Bus: platform.NewMemoryBus()},
			idH.Require)
		reporting.Routes(r, reporting.Deps{Ledger: fstore, Billing: sstore, Stock: cstore},
			idH.Require)
		paystore := payments.NewPGStore(pool)
		payreg := payments.NewRegistry(
			payments.NewOnlineProvider(payments.ProviderStripe),
			payments.NewOnlineProvider(payments.ProviderPayPal))
		payments.Routes(r, payments.Deps{Store: paystore, Providers: payreg,
			WebhookSecret: payments.WebhookSecretFromEnv(), Bus: platform.NewMemoryBus()},
			idH.Require)
		documentsvc.Routes(r, docSvc, idH.Require)
		search.Routes(r, searcher, idH.Require)
	})
	mux.Handle("/metrics", metrics.Handler(build))
	handler := mux
	srv := &http.Server{
		Addr:         ":" + cfg.HTTPPort,
		Handler:      handler,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
	}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimout)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	log.Printf("forgeerp api listening on :%s (env=%s)", cfg.HTTPPort, cfg.Env)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// seedAdmin ensures the bootstrap administrator exists (Dolibarr install-step
// admin creation equivalent). Skipped when FERP_ADMIN_PASSWORD is unset.
func seedAdmin(ctx context.Context, cfg platform.Config, store *identity.PGStore) error {
	if cfg.AdminPassword == "" {
		log.Print("forgeerp: FERP_ADMIN_PASSWORD unset, skipping admin seed")
		return nil
	}
	_, err := store.UserByLogin(ctx, 1, "admin")
	if err == nil {
		return nil // already seeded
	}
	if !errors.Is(err, identity.ErrNotFound) {
		return err
	}
	hash, err := identity.HashPassword(cfg.AdminPassword)
	if err != nil {
		return err
	}
	u := &identity.User{EntityID: 1, Login: "admin", Email: cfg.AdminEmail,
		FirstName: "Forge", LastName: "Admin", Status: identity.UserActive,
		PasswordHash: hash, IsAdmin: true}
	if err := store.CreateUser(ctx, u); err != nil {
		return err
	}
	log.Printf("forgeerp: seeded admin user %q", cfg.AdminEmail)
	return nil
}

// seedDemoOrgs inserts a minimal demo dataset (one customer + one supplier with
// contacts) when the organizations table is empty. Development/demo only.
func seedDemoOrgs(ctx context.Context, store *partners.PGStore) error {
	existing, err := store.ListOrgs(ctx, 1, 1, 0)
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		return nil
	}
	demo := []partners.Organization{
		{EntityID: 1, Name: "Acme Industries", IsCustomer: true, CustomerCode: "ACME-001",
			Email: "contact@acme.example", Status: partners.OrgActive},
		{EntityID: 1, Name: "Globex Supplies", IsSupplier: true, SupplierCode: "GLOB-001",
			Email: "sales@globex.example", Status: partners.OrgActive},
	}
	for i := range demo {
		if err := demo[i].Validate(); err != nil {
			return err
		}
		if err := store.CreateOrg(ctx, &demo[i]); err != nil {
			return err
		}
	}
	contact := &partners.Contact{EntityID: 1, OrgID: demo[0].ID,
		FirstName: "Ada", LastName: "Lovelace", Email: "ada@acme.example", Role: "billing"}
	if err := contact.Validate(); err != nil {
		return err
	}
	if err := store.CreateContact(ctx, contact); err != nil {
		return err
	}
	log.Print("forgeerp: seeded demo organizations")
	return nil
}

// seedDemoCatalog inserts a demo warehouse + products with opening stock.
// Development/demo only.
func seedDemoCatalog(ctx context.Context, store *catalog.PGStore) error {
	existing, err := store.ListProducts(ctx, 1, 1, 0)
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		return nil
	}
	w := &catalog.Warehouse{EntityID: 1, Code: "MAIN", Label: "Main warehouse", Status: 1}
	if err := w.Validate(); err != nil {
		return err
	}
	if err := store.CreateWarehouse(ctx, w); err != nil {
		return err
	}
	demo := []catalog.Product{
		{EntityID: 1, SKU: "WID-001", Name: "Standard widget", Type: catalog.ProductGoods,
			Unit: "unit", NetPrice: 1990, VATRateBps: 2000, Status: catalog.ProductActive, StockTracked: true},
		{EntityID: 1, SKU: "SVC-001", Name: "Consulting hour", Type: catalog.ProductService,
			Unit: "hour", NetPrice: 12000, VATRateBps: 2000, Status: catalog.ProductActive},
	}
	for i := range demo {
		if err := demo[i].Validate(); err != nil {
			return err
		}
		if err := store.CreateProduct(ctx, &demo[i]); err != nil {
			return err
		}
	}
	if _, err := store.AppendMovement(ctx, &catalog.StockMovement{EntityID: 1,
		ProductID: demo[0].ID, WarehouseID: w.ID, Qty: 100, UnitCost: 1200,
		Reason: catalog.ReasonReceipt, Ref: "OPENING"}, false); err != nil {
		return err
	}
	log.Print("forgeerp: seeded demo catalog")
	return nil
}

// seedDemoSales inserts a demo quote-to-cash chain (proposal → order → shipment →
// invoice, partially paid) when no sales documents exist. Development/demo only.
func seedDemoSales(ctx context.Context, sstore *sales.PGStore, pstore *partners.PGStore, cstore *catalog.PGStore) error {
	existing, err := sstore.ListDocs(ctx, 1, "invoice", 1, 0)
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		return nil
	}
	orgs, err := pstore.ListOrgs(ctx, 1, 1, 0)
	if err != nil || len(orgs) == 0 {
		return err
	}
	prods, err := cstore.ListProducts(ctx, 1, 1, 0)
	if err != nil || len(prods) == 0 {
		return err
	}
	ym := "202609"
	lines := []documents.Line{{ProductID: prods[0].ID, Label: prods[0].Name,
		Qty: 2, UnitNet: prods[0].NetPrice, VATRateBps: prods[0].VATRateBps}}
	mkDoc := func(t documents.DocType, srcT documents.DocType, srcID int64) *sales.Document {
		return &sales.Document{EntityID: 1, Type: t, OrgID: orgs[0].ID,
			Currency: "USD", RateToBase: 1000000, SourceType: srcT, SourceID: srcID, Lines: lines}
	}
	prop := mkDoc(documents.TypeProposal, "", 0)
	if err := sstore.CreateDoc(ctx, prop, ym); err != nil {
		return err
	}
	if _, err := sstore.SetStatus(ctx, prop.ID, sales.ProposalSigned); err != nil {
		// Signed requires validated first; walk the chain explicitly.
		if _, err := sstore.SetStatus(ctx, prop.ID, sales.ProposalValidated); err != nil {
			return err
		}
		if _, err := sstore.SetStatus(ctx, prop.ID, sales.ProposalSigned); err != nil {
			return err
		}
	}
	next, err := sales.Convert(*prop, documents.TypeOrder)
	if err != nil {
		return err
	}
	ord := &next
	if err := sstore.CreateDoc(ctx, ord, ym); err != nil {
		return err
	}
	if _, err := sstore.SetStatus(ctx, ord.ID, sales.OrderValidated); err != nil {
		return err
	}
	nx2, err := sales.Convert(*ord, documents.TypeInvoice)
	if err != nil {
		return err
	}
	inv := &nx2
	if err := sstore.CreateDoc(ctx, inv, ym); err != nil {
		return err
	}
	if _, err := sstore.SetStatus(ctx, inv.ID, sales.InvoiceValidated); err != nil {
		return err
	}
	bal, err := sstore.InvoiceBalance(ctx, inv.ID)
	if err != nil {
		return err
	}
	pay := &sales.Payment{EntityID: 1, OrgID: orgs[0].ID, Amount: bal / 2,
		Currency: "USD", Method: "transfer", PaidAt: time.Now().UTC()}
	if _, err := sstore.RecordPayment(ctx, pay, []int64{inv.ID}, ym); err != nil {
		return err
	}
	log.Print("forgeerp: seeded demo sales chain")
	return nil
}

// seedDemoFinance inserts a minimal chart of accounts, journals, an open fiscal
// year, and a demo bank account. Development/demo only.
func seedDemoFinance(ctx context.Context, store *finance.PGStore) error {
	tb, err := store.TrialBalance(ctx, 1)
	if err != nil {
		return err
	}
	if len(tb) > 0 {
		return nil
	}
	accts := []finance.Account{
		{EntityID: 1, Code: "411000", Label: "Customers", Type: "asset"},
		{EntityID: 1, Code: "401000", Label: "Suppliers", Type: "liability"},
		{EntityID: 1, Code: "512000", Label: "Bank", Type: "asset"},
		{EntityID: 1, Code: "707000", Label: "Sales", Type: "revenue"},
		{EntityID: 1, Code: "607000", Label: "Purchases", Type: "expense"},
		{EntityID: 1, Code: "445700", Label: "VAT collected", Type: "liability"},
	}
	for i := range accts {
		if err := store.CreateAccount(ctx, &accts[i]); err != nil {
			return err
		}
	}
	for _, j := range []finance.Journal{
		{EntityID: 1, Code: "VEN", Label: "Sales"},
		{EntityID: 1, Code: "ACH", Label: "Purchases"},
		{EntityID: 1, Code: "BNK", Label: "Bank"},
	} {
		jj := j
		if err := store.CreateJournal(ctx, &jj); err != nil {
			return err
		}
	}
	now := time.Now().UTC()
	fy := &finance.FiscalYear{EntityID: 1, Label: "FY", Locked: false,
		StartDate: time.Date(now.Year(), 1, 1, 0, 0, 0, 0, time.UTC),
		EndDate:   time.Date(now.Year(), 12, 31, 23, 59, 59, 0, time.UTC)}
	if err := store.CreateFiscalYear(ctx, fy); err != nil {
		return err
	}
	ba := &finance.BankAccount{EntityID: 1, Code: "BNK1", Label: "Main account"}
	if err := store.CreateBankAccount(ctx, ba); err != nil {
		return err
	}
	log.Print("forgeerp: seeded demo finance")
	return nil
}
