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

	"github.com/nickvecchioni/infracost/pkg/api"
	"github.com/nickvecchioni/infracost/pkg/attribution"
)

func main() {
	var (
		promURL    = flag.String("prometheus-url", "http://localhost:9090", "VictoriaMetrics or Prometheus base URL")
		listenAddr = flag.String("listen-address", ":8080", "Address for the REST API")
	)
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	logger.Info("starting infracost-engine",
		"prometheus", *promURL,
		"listen", *listenAddr,
	)

	store := attribution.NewPromStore(*promURL)
	engine := attribution.NewEngine(store)

	handler := api.NewServer(api.ServerOpts{
		Engine: engine,
		Logger: logger,
	})

	srv := &http.Server{
		Addr:              *listenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	go func() {
		logger.Info("serving API", "addr", *listenAddr)
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

	logger.Info("infracost-engine stopped")
}
