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

	"github.com/nickvecchioni/ballast/pkg/telemetry"
)

func main() {
	var (
		vllmURL    = flag.String("vllm-url", "http://localhost:8000/metrics", "vLLM metrics endpoint URL")
		modelName  = flag.String("model-name", os.Getenv("MODEL_NAME"), "Model name label (e.g. llama-3-70b)")
		podName    = flag.String("pod-name", os.Getenv("POD_NAME"), "Pod name (from downward API)")
		namespace  = flag.String("namespace", os.Getenv("POD_NAMESPACE"), "Pod namespace (from downward API)")
		nodeName   = flag.String("node-name", os.Getenv("NODE_NAME"), "Node name (from downward API)")
		listenAddr = flag.String("listen-address", ":9401", "Address for the Prometheus metrics endpoint")
	)
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	if *podName == "" || *namespace == "" || *nodeName == "" {
		logger.Error("pod-name, namespace, and node-name are required (set POD_NAME, POD_NAMESPACE, NODE_NAME env vars)")
		os.Exit(1)
	}

	logger.Info("starting ballast-sidecar",
		"vllm_url", *vllmURL,
		"model", *modelName,
		"pod", *podName,
		"namespace", *namespace,
		"node", *nodeName,
		"listen", *listenAddr,
	)

	scraper := telemetry.NewVLLMScraper(telemetry.VLLMScraperOpts{
		TargetURL: *vllmURL,
	})

	exporter := telemetry.NewInferenceExporter(telemetry.InferenceExporterOpts{
		Scraper:   scraper,
		PodName:   *podName,
		Namespace: *namespace,
		NodeName:  *nodeName,
		ModelName: *modelName,
		Logger:    logger,
	})

	reg := prometheus.NewRegistry()
	reg.MustRegister(exporter)

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

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	go func() {
		logger.Info("serving metrics", "addr", *listenAddr)
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

	logger.Info("ballast-sidecar stopped")
}
