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

	"github.com/go-chi/chi/v5"
	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
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

	issuer, err := identity.NewIssuer(cfg.JWTSecret)
	if err != nil {
		return fmt.Errorf("jwt: %w", err)
	}
	store := identity.NewPGStore(pool)
	if err := seedAdmin(ctx, cfg, store); err != nil {
		return fmt.Errorf("seed admin: %w", err)
	}

	base := platform.Router(platform.BuildInfo{Version: version, Commit: commit})
	mux, ok := base.(chi.Router)
	if !ok {
		return errors.New("platform router is not a chi router")
	}
	mux.Route("/api/v1", func(r chi.Router) {
		identity.Routes(r, identity.Deps{Store: store, Issuer: issuer})
	})
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
