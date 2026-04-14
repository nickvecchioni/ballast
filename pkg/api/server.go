package api

import (
	"log/slog"
	"net/http"

	"github.com/nickvecchioni/ballast/pkg/attribution"
)

// ServerOpts configures the API server.
type ServerOpts struct {
	Engine *attribution.Engine
	Logger *slog.Logger
}

// NewServer creates an HTTP handler with all API routes registered.
func NewServer(opts ServerOpts) http.Handler {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	h := NewHandlers(opts.Engine)

	mux := http.NewServeMux()

	// Cost query endpoints.
	mux.HandleFunc("GET /api/v1/cost/pods", h.CostPods)
	mux.HandleFunc("GET /api/v1/cost/namespaces", h.CostNamespaces)
	mux.HandleFunc("GET /api/v1/cost/summary", h.CostSummary)
	mux.HandleFunc("GET /api/v1/export", h.Export)

	// Health check.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	return LoggingMiddleware(opts.Logger, mux)
}
