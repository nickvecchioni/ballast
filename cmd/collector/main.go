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

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/nickvecchioni/infracost/pkg/billing"
	"github.com/nickvecchioni/infracost/pkg/collector"
)

func main() {
	var (
		nodeName      = flag.String("node-name", os.Getenv("NODE_NAME"), "Kubernetes node name (usually from downward API)")
		socketPath    = flag.String("pod-resources-socket", collector.DefaultSocketPath, "Path to kubelet PodResources gRPC socket")
		pricingConfig = flag.String("pricing-config", "", "Path to GPU pricing YAML file (optional)")
		listenAddr    = flag.String("listen-address", ":9400", "Address for the Prometheus metrics endpoint")
	)
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	if *nodeName == "" {
		logger.Error("--node-name is required (or set NODE_NAME env var)")
		os.Exit(1)
	}

	logger.Info("starting infracost-collector",
		"node", *nodeName,
		"listen", *listenAddr,
		"socket", *socketPath,
	)

	// --- NVML ---
	gpuCollector, err := collector.NewNVMLCollector()
	if err != nil {
		logger.Error("failed to initialize NVML", "err", err)
		os.Exit(1)
	}
	defer gpuCollector.Close()
	logger.Info("NVML initialized")

	// --- kubelet PodResources ---
	podResClient, err := collector.NewPodResourcesClient(*socketPath)
	if err != nil {
		logger.Error("failed to connect to kubelet PodResources API", "err", err)
		os.Exit(1)
	}
	defer podResClient.Close()
	logger.Info("connected to kubelet PodResources API", "socket", *socketPath)

	// --- Pricing (optional) ---
	var pricing billing.PricingProvider
	if *pricingConfig != "" {
		p, err := billing.NewStaticPricingFromFile(*pricingConfig, billing.StaticPricingOpts{})
		if err != nil {
			logger.Error("failed to load pricing config", "path", *pricingConfig, "err", err)
			os.Exit(1)
		}
		pricing = p
		logger.Info("loaded GPU pricing config", "path", *pricingConfig)
	} else {
		logger.Warn("no --pricing-config provided; cost metric will not be emitted")
	}

	// --- Prometheus registry ---
	reg := prometheus.NewRegistry()

	mc, err := collector.NewMetricsCollector(collector.MetricsCollectorOpts{
		GPUCollector: gpuCollector,
		PodResources: podResClient,
		Pricing:      pricing,
		NodeName:     *nodeName,
		Registry:     reg,
		Logger:       logger,
	})
	if err != nil {
		logger.Error("failed to create metrics collector", "err", err)
		os.Exit(1)
	}

	// --- HTTP server for /metrics ---
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	srv := &http.Server{
		Addr:              *listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	// --- Graceful shutdown ---
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	// Start the collector loop in the background.
	go mc.Run(ctx)
	logger.Info("collector loop started")

	// Start HTTP server in the background.
	go func() {
		logger.Info("serving metrics", "addr", *listenAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server error", "err", err)
			os.Exit(1)
		}
	}()

	// Block until signal.
	<-ctx.Done()
	logger.Info("shutdown signal received")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("http server shutdown error", "err", err)
	}

	logger.Info("infracost-collector stopped")
}
