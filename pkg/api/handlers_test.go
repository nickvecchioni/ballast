package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nickvecchioni/infracost/pkg/attribution"
)

// mockStore returns canned data for all queries.
type mockStore struct{}

func (m *mockStore) Query(_ context.Context, promql string, _ time.Time) ([]attribution.MetricSample, error) {
	if strings.Contains(promql, "infracost_pod_cost_per_hour_usd") && !strings.Contains(promql, "sum by") {
		return []attribution.MetricSample{
			{Labels: map[string]string{"pod": "serve-a", "namespace": "search", "node": "n1", "gpu_type": "H100"}, Value: 3.90},
			{Labels: map[string]string{"pod": "serve-b", "namespace": "batch", "node": "n1", "gpu_type": "A100"}, Value: 1.80},
		}, nil
	}
	if strings.Contains(promql, "infracost_gpu_utilization_percent") && !strings.Contains(promql, "sum by") && !strings.Contains(promql, "avg by") && !strings.Contains(promql, "count by") {
		return []attribution.MetricSample{
			{Labels: map[string]string{"pod": "serve-a", "namespace": "search", "node": "n1"}, Value: 73},
			{Labels: map[string]string{"pod": "serve-b", "namespace": "batch", "node": "n1"}, Value: 45},
		}, nil
	}
	if strings.Contains(promql, "sum by (namespace)") {
		return []attribution.MetricSample{
			{Labels: map[string]string{"namespace": "search"}, Value: 3.90},
			{Labels: map[string]string{"namespace": "batch"}, Value: 1.80},
		}, nil
	}
	if strings.Contains(promql, "avg by (namespace)") {
		return []attribution.MetricSample{
			{Labels: map[string]string{"namespace": "search"}, Value: 73},
			{Labels: map[string]string{"namespace": "batch"}, Value: 45},
		}, nil
	}
	if strings.Contains(promql, "count by (namespace)") {
		return []attribution.MetricSample{
			{Labels: map[string]string{"namespace": "search"}, Value: 1},
			{Labels: map[string]string{"namespace": "batch"}, Value: 1},
		}, nil
	}
	return nil, nil
}

func (m *mockStore) QueryRange(_ context.Context, _ string, _, _ time.Time, _ time.Duration) ([]attribution.MetricSeries, error) {
	return nil, nil
}

func newTestServer() http.Handler {
	engine := attribution.NewEngine(&mockStore{})
	return NewServer(ServerOpts{Engine: engine})
}

func TestCostPodsEndpoint(t *testing.T) {
	srv := newTestServer()
	req := httptest.NewRequest("GET", "/api/v1/cost/pods?period=1h", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var pods []attribution.PodCost
	if err := json.NewDecoder(w.Body).Decode(&pods); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(pods) != 2 {
		t.Fatalf("expected 2 pods, got %d", len(pods))
	}

	// Should be sorted by cost desc.
	if pods[0].Pod != "serve-a" {
		t.Errorf("first pod = %q, want serve-a", pods[0].Pod)
	}
}

func TestCostPodsNamespaceFilter(t *testing.T) {
	srv := newTestServer()
	req := httptest.NewRequest("GET", "/api/v1/cost/pods?namespace=search&period=1h", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

func TestCostNamespacesEndpoint(t *testing.T) {
	srv := newTestServer()
	req := httptest.NewRequest("GET", "/api/v1/cost/namespaces?period=1h", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var ns []attribution.NamespaceCost
	if err := json.NewDecoder(w.Body).Decode(&ns); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(ns) != 2 {
		t.Fatalf("expected 2 namespaces, got %d", len(ns))
	}
}

func TestCostSummaryEndpoint(t *testing.T) {
	srv := newTestServer()
	req := httptest.NewRequest("GET", "/api/v1/cost/summary?period=1h", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var s attribution.ClusterSummary
	if err := json.NewDecoder(w.Body).Decode(&s); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if s.TotalCostPerHr != 5.70 {
		t.Errorf("total cost/hr = %f, want 5.70", s.TotalCostPerHr)
	}
	if s.GPUCount != 2 {
		t.Errorf("gpu count = %d, want 2", s.GPUCount)
	}
}

func TestExportCSVEndpoint(t *testing.T) {
	srv := newTestServer()
	req := httptest.NewRequest("GET", "/api/v1/export?period=1h&format=csv", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "text/csv") {
		t.Errorf("content-type = %q, want text/csv", w.Header().Get("Content-Type"))
	}

	body := w.Body.String()
	if !strings.Contains(body, "namespace") {
		t.Error("CSV should contain header row")
	}
	if !strings.Contains(body, "serve-a") {
		t.Error("CSV should contain pod data")
	}
}

func TestHealthzEndpoint(t *testing.T) {
	srv := newTestServer()
	req := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}
