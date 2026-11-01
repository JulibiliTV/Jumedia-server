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
	"jumedia-server/internal/emby"
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
	if len(os.Args) > 2 || (len(os.Args) == 2 && os.Args[1] != "reset") {
		return fmt.Errorf("usage: jumedia [reset]")
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
	catalog, err := emby.LoadCatalog(cfg.EmbyCatalog)
	if err != nil {
		return err
	}
	if len(os.Args) == 2 {
		if err = seed.Reset(ctx, s, catalog); err != nil {
			return err
		}
		slog.Info("database reset complete")
		return nil
	}
	if cfg.Seed {
		if err = seed.Seed(ctx, s, catalog); err != nil {
			return err
		}
	}
	if err = seed.MigrateMedia(ctx, s, catalog); err != nil {
		return err
	}
	if err = os.MkdirAll(cfg.UploadDir(), 0755); err != nil {
		return err
	}
	handler := api.New(s, cfg)
	if err = handler.InitDataSources(ctx); err != nil {
		return err
	}
	defer handler.CloseDataSources()
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
