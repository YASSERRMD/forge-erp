// Command api is the ForgeERP modular-monolith server entrypoint.
//
//go:generate go run genspec_openapi.go
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/agenda"
	"github.com/YASSERRMD/forge-erp/backend/internal/assets"
	"github.com/YASSERRMD/forge-erp/backend/internal/booking"
	"github.com/YASSERRMD/forge-erp/backend/internal/catalog"
	"github.com/YASSERRMD/forge-erp/backend/internal/dataio"
	"github.com/YASSERRMD/forge-erp/backend/internal/documents"
	"github.com/YASSERRMD/forge-erp/backend/internal/documentsvc"
	"github.com/YASSERRMD/forge-erp/backend/internal/events"
	"github.com/YASSERRMD/forge-erp/backend/internal/finance"
	"github.com/YASSERRMD/forge-erp/backend/internal/fx"
	"github.com/YASSERRMD/forge-erp/backend/internal/hr"
	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
	"github.com/YASSERRMD/forge-erp/backend/internal/inbound"
	"github.com/YASSERRMD/forge-erp/backend/internal/kb"
	"github.com/YASSERRMD/forge-erp/backend/internal/manufacturing"
	"github.com/YASSERRMD/forge-erp/backend/internal/members"
	"github.com/YASSERRMD/forge-erp/backend/internal/partners"
	"github.com/YASSERRMD/forge-erp/backend/internal/payments"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform/module"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform/trigger"
	"github.com/YASSERRMD/forge-erp/backend/internal/pos"
	"github.com/YASSERRMD/forge-erp/backend/internal/procurement"
	"github.com/YASSERRMD/forge-erp/backend/internal/reporting"
	"github.com/YASSERRMD/forge-erp/backend/internal/sales"
	"github.com/YASSERRMD/forge-erp/backend/internal/search"
	"github.com/YASSERRMD/forge-erp/backend/internal/sepa"
	"github.com/YASSERRMD/forge-erp/backend/internal/services"
	"github.com/YASSERRMD/forge-erp/backend/internal/survey"
	"github.com/YASSERRMD/forge-erp/backend/migrations"
	"github.com/go-chi/chi/v5"
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
	// Refuse production boot with development secrets (Phase 0 task 7).
	// Checked after the DB overlay so an operator-set secret passes.
	if err := platform.CheckProdSecrets(cfg); err != nil {
		return err
	}

	issuer, err := identity.NewIssuer(cfg.JWTSecret)
	if err != nil {
		return fmt.Errorf("jwt: %w", err)
	}
	store := identity.NewPGStore(pool)
	if err := seedAdmin(ctx, cfg, pool, store); err != nil {
		return fmt.Errorf("seed admin: %w", err)
	}
	pstore := partners.NewPGStore(pool)
	if err := seedDemoOrgs(ctx, pool, pstore); err != nil {
		return fmt.Errorf("seed demo orgs: %w", err)
	}
	cstore := catalog.NewPGStore(pool)
	if err := seedDemoCatalog(ctx, pool, cstore); err != nil {
		return fmt.Errorf("seed demo catalog: %w", err)
	}
	sstore := sales.NewPGStore(pool)
	if err := seedDemoSales(ctx, pool, sstore, pstore, cstore); err != nil {
		return fmt.Errorf("seed demo sales: %w", err)
	}
	procstore := procurement.NewPGStore(pool)
	fstore := finance.NewPGStore(pool)
	svcstore := services.NewPGStore(pool)
	mfstore := manufacturing.NewPGStore(pool)
	hrstore := hr.NewPGStore(pool)
	if err := seedDemoFinance(ctx, pool, fstore); err != nil {
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
	docSvc := &documentsvc.Service{Store: documentsvc.NewPGStore(pool), Storage: byteStorage, DB: pool}
	providers := map[string]search.Provider{
		"organization": func(ctx context.Context, entityID int64) ([]search.Result, error) {
			orgs, err := pstore.ListOrgs(ctx, pool, entityID, 500, 0)
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
			prods, err := cstore.ListProducts(ctx, pool, entityID, 500, 0)
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

	// /readyz gates on a live database connection (Phase 0 task 6).
	base := platform.Router(build, func() error { return pool.Ping(ctx) }, metrics.Instrument)
	mux, ok := base.(chi.Router)
	if !ok {
		return errors.New("platform router is not a chi router")
	}
	// One shared event bus for all contexts (NATS JetStream when configured,
	// otherwise the in-process bus). Subscribers below rely on shared delivery.
	var bus platform.Bus = platform.NewMemoryBus()
	if ncfg := platform.LoadNATSConfig(getenv); ncfg.Backend == "nats" {
		if nbus, err := platform.ConnectNATS(ncfg); err != nil {
			log.Printf("forgeerp: NATS unreachable (%v); using memory bus", err)
		} else {
			defer nbus.Close()
			bus = nbus
			log.Printf("forgeerp: event bus=nats (%s)", ncfg.URL)
		}
	} else {
		log.Print("forgeerp: event bus=memory")
	}
	// Module registry (Kernel 1): contexts self-register here as they adopt
	// module.Module; the /api/v1/modules surface lists them per entity.
	modReg := module.NewRegistry()
	// Trigger outbox relay (Kernel 3): delivers events staged inside
	// transactions (crash-safe). Disabled with FERP_RELAY_INTERVAL_S=0.
	relay := &trigger.Relay{Bus: bus}
	relaySecs := 5
	if v := os.Getenv("FERP_RELAY_INTERVAL_S"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			relaySecs = n
		}
	}
	if relaySecs > 0 {
		go func() {
			t := time.NewTicker(time.Duration(relaySecs) * time.Second)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					if n, err := relay.RunOnce(context.Background(), pool); err != nil {
						log.Printf("forgeerp: trigger relay: %v", err)
					} else if n > 0 {
						log.Printf("forgeerp: trigger relay delivered %d", n)
					}
				}
			}
		}()
		log.Printf("forgeerp: trigger relay every %ds", relaySecs)
	} else {
		log.Print("forgeerp: trigger relay disabled (FERP_RELAY_INTERVAL_S=0)")
	}
	if oss, ok := searcher.(*search.OpenSearcher); ok {
		// Write-through indexing: created orgs/products land in OpenSearch
		// immediately; startup reindex + provider fallback cover the rest.
		bus.Subscribe("forgeerp.partners.organization.created.v1", func(ctx context.Context, e platform.Event) {
			o, err := pstore.OrgByID(ctx, pool, e.EntityID, e.ID)
			if err != nil {
				return
			}
			_ = oss.IndexOne(ctx, search.MakeDocument("organization", o.EntityID, o.ID, o.Name, o.CustomerCode))
		})
		bus.Subscribe("forgeerp.catalog.product.created.v1", func(ctx context.Context, e platform.Event) {
			p, err := cstore.ProductByID(ctx, pool, e.EntityID, e.ID)
			if err != nil {
				return
			}
			_ = oss.IndexOne(ctx, search.MakeDocument("product", p.EntityID, p.ID, p.Name, p.SKU))
		})
		log.Print("forgeerp: search write-through subscribed")
	}
	identDeps := identity.Deps{Store: store, Issuer: issuer, DB: pool}
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
	// Abuse caps: 20 rps burst 40 per IP across the API (login endpoints additionally
	// guarded by per-account lockout in the identity context).
	apiLimiter := platform.NewRateLimiter(20, 40)
	// Bound the per-IP bucket map: evict buckets idle > 10m, every minute
	// (Phase 0 task 7).
	stopLimiter := apiLimiter.StartCleanup(time.Minute, 10*time.Minute)
	defer stopLimiter()
	mountAPIRoutes(mux, apiWiring{
		ident:    identDeps,
		limit:    apiLimiter.Limit,
		partners: partners.Deps{Store: pstore, Bus: bus, DB: pool},
		catalog:  catalog.Deps{Store: cstore, Bus: bus, DB: pool},
		sales:    sales.Deps{Store: sstore, Catalog: cstore, Bus: bus, DB: pool},
		procurement: procurement.Deps{Store: procstore, Catalog: cstore,
			Bus: bus, DB: pool},
		finance:  finance.Deps{Store: fstore, DB: pool},
		services: services.Deps{Store: svcstore, Bus: bus, DB: pool},
		manufacturing: manufacturing.Deps{Store: mfstore, Ledger: cstore,
			Bus: bus, DB: pool, Pool: pool},
		hr: hr.Deps{Store: hrstore, Finance: fstore, Bus: bus, DB: pool, Pool: pool},
		pos: func() pos.Deps {
			posstore := pos.NewPGStore(pool)
			var walkinOrg int64
			if v := os.Getenv("FERP_POS_WALKIN_ORG"); v != "" {
				_, _ = fmt.Sscanf(v, "%d", &walkinOrg)
			}
			return pos.Deps{Store: posstore, Catalog: cstore, Sales: sstore,
				WalkinOrg: walkinOrg, Bus: bus, DB: pool, Pool: pool}
		}(),
		reporting: reporting.Deps{Ledger: fstore, Billing: sstore, Stock: cstore,
			Orgs: pstore, DB: pool},
		payments: func() payments.Deps {
			paystore := payments.NewPGStore(pool)
			payreg := payments.NewRegistry(
				payments.NewOnlineProvider(payments.ProviderStripe),
				payments.NewOnlineProvider(payments.ProviderPayPal))
			return payments.Deps{Store: paystore, Providers: payreg,
				WebhookSecret: payments.WebhookSecretFromEnv(), Bus: bus, DB: pool}
		}(),
		booking: booking.Deps{Store: booking.NewPGStore(pool), Bus: bus, DB: pool},
		survey:  survey.Deps{Store: survey.NewPGStore(pool), Bus: bus, DB: pool},
		members: members.Deps{Store: members.NewPGStore(pool), Bus: bus, DB: pool},
		assets:  assets.Deps{Store: assets.NewPGStore(pool), Bus: bus, DB: pool},
		kb:      kb.Deps{Store: kb.NewPGStore(pool), Bus: bus, DB: pool},
		events:  events.Deps{Store: events.NewPGStore(pool), Bus: bus, DB: pool},
		dataio:  dataio.Deps{Orgs: pstore, Products: cstore, Bus: bus, DB: pool},
		fx:      fx.Deps{Store: fx.NewPGStore(pool), Bus: bus, DB: pool},
		sepa:    sepa.Deps{Store: sepa.NewPGStore(pool), Bus: bus, DB: pool},
		inbound: inbound.Deps{Store: inbound.NewPGStore(pool), Tickets: svcstore,
			Bus: bus, DB: pool},
		agenda:   agenda.Deps{Store: agenda.NewPGStore(pool), Bus: bus, DB: pool},
		modules:  module.Deps{Registry: modReg, Store: module.NewPGStore(), DB: pool},
		docSvc:   docSvc,
		searcher: searcher,
	})
	// Reminder daemon (agenda.Worker). Previously started inside the route-mount
	// closure, which runs synchronously during Route(); starting it here keeps
	// the identical startup point with route construction factored out.
	agstore := agenda.NewPGStore(pool)
	reminderSecs := 300
	if v := os.Getenv("FERP_REMINDER_INTERVAL_S"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			reminderSecs = n
		}
	}
	if reminderSecs > 0 {
		worker := &agenda.Worker{Store: agstore, DB: pool, Pool: pool,
			Interval: time.Duration(reminderSecs) * time.Second,
			Logger:   log.Default()}
		go worker.Run(ctx)
		log.Printf("forgeerp: reminder daemon every %ds", reminderSecs)
	} else {
		log.Print("forgeerp: reminder daemon disabled (FERP_REMINDER_INTERVAL_S=0)")
	}
	handler := mux
	srv := &http.Server{
		Addr:         ":" + cfg.HTTPPort,
		Handler:      handler,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
	}

	// /metrics lives off the public listener on a loopback-only admin port
	// (Phase 0 task 6): scrapers reach it via the host/pod network, the
	// public API surface does not expose it.
	adminMux := http.NewServeMux()
	adminMux.Handle("/metrics", metrics.Handler(build))
	adminSrv := &http.Server{
		Addr:         "127.0.0.1:" + cfg.AdminPort,
		Handler:      adminMux,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
	}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimout)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
		_ = adminSrv.Shutdown(shutCtx)
	}()

	go func() {
		log.Printf("forgeerp admin (metrics) listening on 127.0.0.1:%s", cfg.AdminPort)
		if err := adminSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("forgeerp: admin server: %v", err)
		}
	}()
	log.Printf("forgeerp api listening on :%s (env=%s)", cfg.HTTPPort, cfg.Env)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// apiWiring carries every module's route dependencies so the full HTTP surface
// is mounted from one place. run() fills it with PG-backed stores; the
// OpenAPI diff test and the spec generator fill it with stubs via
// stubWiring(). Route registration only captures handlers (stores are touched
// per-request), so stub stores are safe for walking the router.
type apiWiring struct {
	ident         identity.Deps
	limit         func(http.Handler) http.Handler
	partners      partners.Deps
	catalog       catalog.Deps
	sales         sales.Deps
	procurement   procurement.Deps
	finance       finance.Deps
	services      services.Deps
	manufacturing manufacturing.Deps
	hr            hr.Deps
	pos           pos.Deps
	reporting     reporting.Deps
	payments      payments.Deps
	booking       booking.Deps
	survey        survey.Deps
	members       members.Deps
	assets        assets.Deps
	kb            kb.Deps
	events        events.Deps
	dataio        dataio.Deps
	fx            fx.Deps
	sepa          sepa.Deps
	inbound       inbound.Deps
	agenda        agenda.Deps
	modules       module.Deps
	docSvc        *documentsvc.Service
	searcher      search.Searcher
}

// mountAPIRoutes mounts every module surface nested at /api/v1 plus the public
// bearer-link download outside RBAC. This is the single source of truth for
// the served route set (see TestOpenAPIRouterMatchesSpec).
func mountAPIRoutes(mux chi.Router, w apiWiring) {
	idH := identity.NewHandler(w.ident)
	mux.With(w.limit).Route("/api/v1", func(r chi.Router) {
		identity.Routes(r, w.ident)
		partners.Routes(r, w.partners, idH.Require)
		catalog.Routes(r, w.catalog, idH.Require)
		sales.Routes(r, w.sales, idH.Require)
		procurement.Routes(r, w.procurement, idH.Require)
		finance.Routes(r, w.finance, idH.Require)
		services.Routes(r, w.services, idH.Require)
		manufacturing.Routes(r, w.manufacturing, idH.Require)
		hr.Routes(r, w.hr, idH.Require)
		pos.Routes(r, w.pos, idH.Require)
		reporting.Routes(r, w.reporting, idH.Require)
		payments.Routes(r, w.payments, idH.Require)
		booking.Routes(r, w.booking, idH.Require)
		survey.Routes(r, w.survey, idH.Require)
		members.Routes(r, w.members, idH.Require)
		assets.Routes(r, w.assets, idH.Require)
		kb.Routes(r, w.kb, idH.Require)
		events.Routes(r, w.events, idH.Require)
		dataio.Routes(r, w.dataio, idH.Require)
		fx.Routes(r, w.fx, idH.Require)
		sepa.Routes(r, w.sepa, idH.Require)
		inbound.Routes(r, w.inbound, idH.Require)
		agenda.Routes(r, w.agenda, idH.Require)
		module.Routes(r, w.modules, idH.Require)
		documentsvc.Routes(r, w.docSvc, idH.Require)
		search.Routes(r, w.searcher, idH.Require)
	})
	// Public bearer-link downloads (portal-lite). Rate-limited like the API,
	// but outside RBAC: the unguessable token is the credential.
	mux.With(w.limit).Get("/public/share/{token}", documentsvc.PublicShare(w.docSvc))
}

// stubWiring builds an apiWiring with no database for router-shape tests and
// the OpenAPI generator. Nil stores are never touched during registration.
func stubWiring() apiWiring {
	limiter := platform.NewRateLimiter(20, 40)
	return apiWiring{
		limit: limiter.Limit,
		modules: module.Deps{
			Registry: module.NewRegistry(),
		},
		payments: payments.Deps{
			Providers: payments.NewRegistry(),
			Bus:       platform.NewMemoryBus(),
		},
		docSvc:   &documentsvc.Service{},
		searcher: search.NewMemorySearcher(),
	}
}

// seedAdmin ensures the bootstrap administrator exists (Dolibarr install-step
// admin creation equivalent). Skipped when FERP_ADMIN_PASSWORD is unset.
func seedAdmin(ctx context.Context, cfg platform.Config, db platform.DBTX, store *identity.PGStore) error {
	if cfg.AdminPassword == "" {
		log.Print("forgeerp: FERP_ADMIN_PASSWORD unset, skipping admin seed")
		return nil
	}
	_, err := store.UserByLogin(ctx, db, 1, "admin")
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
	if err := store.CreateUser(ctx, db, u); err != nil {
		return err
	}
	log.Printf("forgeerp: seeded admin user %q", cfg.AdminEmail)
	return nil
}

// seedDemoOrgs inserts a minimal demo dataset (one customer + one supplier with
// contacts) when the organizations table is empty. Development/demo only.
func seedDemoOrgs(ctx context.Context, db platform.DBTX, store *partners.PGStore) error {
	existing, err := store.ListOrgs(ctx, db, 1, 1, 0)
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
		if err := store.CreateOrg(ctx, db, &demo[i]); err != nil {
			return err
		}
	}
	contact := &partners.Contact{EntityID: 1, OrgID: demo[0].ID,
		FirstName: "Ada", LastName: "Lovelace", Email: "ada@acme.example", Role: "billing"}
	if err := contact.Validate(); err != nil {
		return err
	}
	if err := store.CreateContact(ctx, db, contact); err != nil {
		return err
	}
	log.Print("forgeerp: seeded demo organizations")
	return nil
}

// seedDemoCatalog inserts a demo warehouse + products with opening stock.
// Development/demo only.
func seedDemoCatalog(ctx context.Context, db platform.DBTX, store *catalog.PGStore) error {
	existing, err := store.ListProducts(ctx, db, 1, 1, 0)
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
	if err := store.CreateWarehouse(ctx, db, w); err != nil {
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
		if err := store.CreateProduct(ctx, db, &demo[i]); err != nil {
			return err
		}
	}
	if _, err := store.AppendMovement(ctx, db, &catalog.StockMovement{EntityID: 1,
		ProductID: demo[0].ID, WarehouseID: w.ID, Qty: 100, UnitCost: 1200,
		Reason: catalog.ReasonReceipt, Ref: "OPENING"}, false); err != nil {
		return err
	}
	log.Print("forgeerp: seeded demo catalog")
	return nil
}

// seedDemoSales inserts a demo quote-to-cash chain (proposal → order → shipment →
// invoice, partially paid) when no sales documents exist. Development/demo only.
func seedDemoSales(ctx context.Context, db platform.DBTX, sstore *sales.PGStore, pstore *partners.PGStore, cstore *catalog.PGStore) error {
	existing, err := sstore.ListDocs(ctx, db, 1, "invoice", 1, 0)
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		return nil
	}
	orgs, err := pstore.ListOrgs(ctx, db, 1, 1, 0)
	if err != nil || len(orgs) == 0 {
		return err
	}
	prods, err := cstore.ListProducts(ctx, db, 1, 1, 0)
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
	if err := sstore.CreateDoc(ctx, db, prop, ym); err != nil {
		return err
	}
	if _, err := sstore.SetStatus(ctx, db, 1, prop.ID, sales.ProposalSigned); err != nil {
		// Signed requires validated first; walk the chain explicitly.
		if _, err := sstore.SetStatus(ctx, db, 1, prop.ID, sales.ProposalValidated); err != nil {
			return err
		}
		if _, err := sstore.SetStatus(ctx, db, 1, prop.ID, sales.ProposalSigned); err != nil {
			return err
		}
	}
	next, err := sales.Convert(*prop, documents.TypeOrder)
	if err != nil {
		return err
	}
	ord := &next
	if err := sstore.CreateDoc(ctx, db, ord, ym); err != nil {
		return err
	}
	if _, err := sstore.SetStatus(ctx, db, 1, ord.ID, sales.OrderValidated); err != nil {
		return err
	}
	nx2, err := sales.Convert(*ord, documents.TypeInvoice)
	if err != nil {
		return err
	}
	inv := &nx2
	if err := sstore.CreateDoc(ctx, db, inv, ym); err != nil {
		return err
	}
	if _, err := sstore.SetStatus(ctx, db, 1, inv.ID, sales.InvoiceValidated); err != nil {
		return err
	}
	bal, err := sstore.InvoiceBalance(ctx, db, 1, inv.ID)
	if err != nil {
		return err
	}
	pay := &sales.Payment{EntityID: 1, OrgID: orgs[0].ID, Amount: bal / 2,
		Currency: "USD", Method: "transfer", PaidAt: time.Now().UTC()}
	if _, err := sstore.RecordPayment(ctx, db, pay, []int64{inv.ID}, ym); err != nil {
		return err
	}
	log.Print("forgeerp: seeded demo sales chain")
	return nil
}

// seedDemoFinance inserts a minimal chart of accounts, journals, an open fiscal
// year, and a demo bank account. Development/demo only. Idempotent: skips when
// any chart accounts exist (trial balance stays empty until entries post, so it
// must not be used as the emptiness check).
func seedDemoFinance(ctx context.Context, db platform.DBTX, store *finance.PGStore) error {
	existing, err := store.Accounts(ctx, db, 1)
	if err != nil {
		return err
	}
	if len(existing) > 0 {
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
		if err := store.CreateAccount(ctx, db, &accts[i]); err != nil {
			return err
		}
	}
	for _, j := range []finance.Journal{
		{EntityID: 1, Code: "VEN", Label: "Sales"},
		{EntityID: 1, Code: "ACH", Label: "Purchases"},
		{EntityID: 1, Code: "BNK", Label: "Bank"},
	} {
		jj := j
		if err := store.CreateJournal(ctx, db, &jj); err != nil {
			return err
		}
	}
	now := time.Now().UTC()
	fy := &finance.FiscalYear{EntityID: 1, Label: "FY", Locked: false,
		StartDate: time.Date(now.Year(), 1, 1, 0, 0, 0, 0, time.UTC),
		EndDate:   time.Date(now.Year(), 12, 31, 23, 59, 59, 0, time.UTC)}
	if err := store.CreateFiscalYear(ctx, db, fy); err != nil {
		return err
	}
	ba := &finance.BankAccount{EntityID: 1, Code: "BNK1", Label: "Main account"}
	if err := store.CreateBankAccount(ctx, db, ba); err != nil {
		return err
	}
	log.Print("forgeerp: seeded demo finance")
	return nil
}
