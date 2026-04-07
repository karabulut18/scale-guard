package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/karabulut18/scale-guard/internal/config"
	"github.com/karabulut18/scale-guard/internal/grpcserver"
	"github.com/karabulut18/scale-guard/internal/limiter"
	"github.com/karabulut18/scale-guard/internal/store"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// ── Store ──────────────────────────────────────────────────────────────
	pg, err := store.NewPostgreSQL(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer pg.Close()

	// ── Limiter ────────────────────────────────────────────────────────────
	// TODO: tenants should be loaded from config or DB — hardcoded for now.
	tenants := []string{"tenant_storefront", "tenant_analytics"}
	l := limiter.New(cfg, pg, tenants)
	if err := l.Start(ctx); err != nil {
		log.Fatalf("limiter: %v", err)
	}
	defer l.Shutdown(context.Background())

	// ── gRPC server ────────────────────────────────────────────────────────
	srv := grpcserver.New(l)
	addr := fmt.Sprintf(":%d", cfg.GRPCPort)
	if err := grpcserver.ListenAndServe(ctx, addr, srv); err != nil {
		log.Fatalf("grpc: %v", err)
	}
}
