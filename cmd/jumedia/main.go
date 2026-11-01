package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"jumedia-server/internal/api"
	"jumedia-server/internal/config"
	"jumedia-server/internal/seed"
	"jumedia-server/internal/store"
)

func main() {
	if err := run(); err != nil {
		slog.Error("jumedia stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	args := os.Args[1:]
	if len(args) > 0 && !(len(args) == 1 && args[0] == "reset" || len(args) == 3 && args[0] == "admin" || len(args) == 3 && args[0] == "demo" && args[1] == "import") {
		return fmt.Errorf("usage: jumedia [reset | admin <username> <password> | demo import <file>]")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	s, err := store.Open(ctx, cfg.DBDriver, cfg.DBDSN)
	if err != nil {
		return err
	}
	defer s.Close()
	if err = seed.Seed(ctx, s); err != nil {
		return err
	}
	if len(args) == 3 && args[0] == "admin" {
		return seed.Admin(ctx, s, args[1], args[2])
	}
	if len(args) == 0 {
		if err = seed.BootstrapAdmin(ctx, s, cfg.AdminUsername, cfg.AdminPassword); err != nil {
			return err
		}
	}
	if err = os.MkdirAll(cfg.UploadDir(), 0755); err != nil {
		return err
	}
	handler := api.New(s, cfg)
	if err = handler.InitDataSources(ctx); err != nil {
		return err
	}
	defer handler.CloseDataSources()
	if err = handler.InitDemoMedia(ctx); err != nil {
		return err
	}
	if len(args) == 1 && args[0] == "reset" {
		if err = handler.ResetApplication(ctx); err != nil {
			return err
		}
		if err = seed.BootstrapAdmin(ctx, s, cfg.AdminUsername, cfg.AdminPassword); err != nil {
			return err
		}
		slog.Info("database reset complete")
		return nil
	}
	if len(args) == 3 && args[0] == "demo" {
		raw, e := os.ReadFile(args[2])
		if e != nil {
			return e
		}
		p, e := seed.ParsePackage(raw)
		if e != nil {
			return e
		}
		return handler.ImportDemo(ctx, p)
	}
	catalog, err := seed.StoredCatalog(ctx, s)
	if err != nil {
		return err
	}
	if len(catalog.Series)+len(catalog.Movies) > 0 {
		if err = seed.MigrateMedia(ctx, s, catalog); err != nil {
			return err
		}
	}

	if err = handler.InitLiveCaches(ctx); err != nil {
		return err
	}
	defer handler.CloseLiveCaches()
	if err = handler.InitLiveChat(ctx); err != nil {
		return err
	}
	defer handler.CloseLiveChat()
	srv := &http.Server{Addr: cfg.Addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second}
	done := make(chan error, 1)
	go func() { done <- srv.ListenAndServe() }()
	slog.Info("jumedia listening", "addr", cfg.Addr, "database", cfg.DBDriver)
	select {
	case err = <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	handler.CloseLiveChat()
	handler.CloseLiveCaches()
	handler.CloseDataSources()
	if err = srv.Shutdown(shutdown); err != nil {
		_ = srv.Close()
		return err
	}
	err = <-done
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
