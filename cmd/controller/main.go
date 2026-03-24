package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	v1alpha1 "github.com/nickvecchioni/infracost/api/v1alpha1"
	"github.com/nickvecchioni/infracost/pkg/attribution"
	"github.com/nickvecchioni/infracost/pkg/enforcement"
)

func main() {
	var (
		promURL    = flag.String("prometheus-url", "http://localhost:9090", "VictoriaMetrics or Prometheus base URL")
		listenAddr = flag.String("listen-address", ":8081", "Address for the health endpoint")
		interval   = flag.Duration("reconcile-interval", 60*time.Second, "Budget reconciliation interval")
		kubeconfig = flag.String("kubeconfig", "", "Path to kubeconfig (uses in-cluster config if empty)")
	)
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	logger.Info("starting infracost-controller",
		"prometheus", *promURL,
		"listen", *listenAddr,
		"interval", interval.String(),
	)

	// --- K8s client ---
	var config *rest.Config
	var err error
	if *kubeconfig != "" {
		config, err = clientcmd.BuildConfigFromFlags("", *kubeconfig)
	} else {
		config, err = rest.InClusterConfig()
	}
	if err != nil {
		logger.Error("failed to create k8s config", "err", err)
		os.Exit(1)
	}

	dynClient, err := dynamic.NewForConfig(config)
	if err != nil {
		logger.Error("failed to create k8s dynamic client", "err", err)
		os.Exit(1)
	}

	budgetStore := enforcement.NewK8sBudgetStore(dynClient)

	// --- Attribution engine ---
	promStore := attribution.NewPromStore(*promURL)
	engine := attribution.NewEngine(promStore)

	// --- Build notifiers from CRD alert channels ---
	// The controller discovers notifiers from each budget's spec at reconcile
	// time. For global notifiers, they could be passed via flags. For now,
	// per-budget channels are used.
	var notifiers []enforcement.Notifier
	// Notifiers are built per-budget during reconciliation. A future
	// enhancement could add global --slack-webhook / --pagerduty-key flags.

	ctrl := enforcement.NewBudgetController(enforcement.BudgetControllerOpts{
		Store:     budgetStore,
		Engine:    engine,
		Notifiers: notifiers,
		Interval:  *interval,
		Logger:    logger,
	})

	// --- Health endpoint ---
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

	// Start the budget controller loop.
	go ctrl.Run(ctx)
	logger.Info("budget controller started")

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

	// Prevent unused import warnings.
	_ = v1alpha1.GroupVersion
	_ = strings.Contains
}
