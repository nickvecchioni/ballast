package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nickvecchioni/infracost/pkg/attribution"
	_ "github.com/nickvecchioni/infracost/pkg/enforcement" // Budget controller; K8s CRD watcher wiring TBD.
)

func main() {
	var (
		promURL    = flag.String("prometheus-url", "http://localhost:9090", "VictoriaMetrics or Prometheus base URL")
		listenAddr = flag.String("listen-address", ":8081", "Address for the health/webhook endpoint")
		interval   = flag.Duration("reconcile-interval", 60*time.Second, "Budget reconciliation interval")
	)
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	logger.Info("starting infracost-controller",
		"prometheus", *promURL,
		"listen", *listenAddr,
		"interval", interval.String(),
	)

	store := attribution.NewPromStore(*promURL)
	engine := attribution.NewEngine(store)

	// In a full deployment, the controller would use a K8s client to list
	// InferenceBudget CRs and update their status. For now, the BudgetStore
	// interface is satisfied by a K8s-backed implementation (not yet wired).
	// This binary serves as the entry point; the K8s client wiring will be
	// added when the CRDs are deployed.
	logger.Warn("budget controller running in standalone mode (no K8s CRD watcher)")
	_ = engine

	// Health endpoint.
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	srv := &http.Server{
		Addr:              *listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	go func() {
		logger.Info("serving health endpoint", "addr", *listenAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server error", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	logger.Info("shutdown signal received")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("http server shutdown error", "err", err)
	}

	logger.Info("infracost-controller stopped")
}
