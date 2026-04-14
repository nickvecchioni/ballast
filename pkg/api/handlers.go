package api

import (
	"encoding/json"
	"net/http"

	"github.com/nickvecchioni/ballast/pkg/attribution"
)

// Handlers holds the REST API endpoint handlers.
type Handlers struct {
	engine *attribution.Engine
}

// NewHandlers creates the API handlers backed by the given attribution engine.
func NewHandlers(engine *attribution.Engine) *Handlers {
	return &Handlers{engine: engine}
}

// CostPods returns per-pod cost for a period.
// GET /api/v1/cost/pods?namespace=X&period=1h
func (h *Handlers) CostPods(w http.ResponseWriter, r *http.Request) {
	namespace := r.URL.Query().Get("namespace")
	period := attribution.ParsePeriod(r.URL.Query().Get("period"))

	pods, err := h.engine.CostByPods(r.Context(), namespace, period)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	attribution.SortPodsBycost(pods)
	writeJSON(w, http.StatusOK, pods)
}

// CostNamespaces returns aggregated cost per namespace.
// GET /api/v1/cost/namespaces?period=this-month
func (h *Handlers) CostNamespaces(w http.ResponseWriter, r *http.Request) {
	period := attribution.ParsePeriod(r.URL.Query().Get("period"))

	namespaces, err := h.engine.CostByNamespaces(r.Context(), period)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	attribution.SortNamespacesByCost(namespaces)
	writeJSON(w, http.StatusOK, namespaces)
}

// CostSummary returns a cluster-wide cost summary.
// GET /api/v1/cost/summary?period=this-month
func (h *Handlers) CostSummary(w http.ResponseWriter, r *http.Request) {
	period := attribution.ParsePeriod(r.URL.Query().Get("period"))

	summary, err := h.engine.Summary(r.Context(), period)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, summary)
}

// Export returns pod cost data as CSV.
// GET /api/v1/export?namespace=X&period=this-month&format=csv
func (h *Handlers) Export(w http.ResponseWriter, r *http.Request) {
	namespace := r.URL.Query().Get("namespace")
	period := attribution.ParsePeriod(r.URL.Query().Get("period"))

	pods, err := h.engine.CostByPods(r.Context(), namespace, period)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	attribution.SortPodsBycost(pods)

	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", "attachment; filename=ballast-export.csv")
	if err := attribution.ExportCSV(w, pods, period); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
