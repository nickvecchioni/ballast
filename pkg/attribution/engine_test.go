package attribution

import (
	"context"
	"testing"
	"time"
)

// mockStore implements Store with canned responses keyed by query substring.
type mockStore struct {
	instantResults map[string][]MetricSample
	rangeResults   map[string][]MetricSeries
}

func (m *mockStore) Query(_ context.Context, promql string, _ time.Time) ([]MetricSample, error) {
	for key, result := range m.instantResults {
		if contains(promql, key) {
			return result, nil
		}
	}
	return nil, nil
}

func (m *mockStore) QueryRange(_ context.Context, promql string, _, _ time.Time, _ time.Duration) ([]MetricSeries, error) {
	for key, result := range m.rangeResults {
		if contains(promql, key) {
			return result, nil
		}
	}
	return nil, nil
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// fixedPeriod returns a Period of exactly the given duration.
func fixedPeriod(d time.Duration) Period {
	end := time.Date(2026, 3, 24, 12, 0, 0, 0, time.UTC)
	return Period{Start: end.Add(-d), End: end}
}

func TestCostByPods(t *testing.T) {
	store := &mockStore{
		instantResults: map[string][]MetricSample{
			"ballast_pod_cost_per_hour_usd": {
				{Labels: map[string]string{"pod": "serve-a", "namespace": "search", "node": "n1", "gpu_type": "H100"}, Value: 3.90},
				{Labels: map[string]string{"pod": "serve-b", "namespace": "batch", "node": "n1", "gpu_type": "A100"}, Value: 1.80},
			},
			"ballast_gpu_utilization_percent": {
				{Labels: map[string]string{"pod": "serve-a", "namespace": "search", "node": "n1"}, Value: 73},
				{Labels: map[string]string{"pod": "serve-b", "namespace": "batch", "node": "n1"}, Value: 45},
			},
		},
	}

	e := NewEngine(store)
	period := fixedPeriod(time.Hour)

	pods, err := e.CostByPods(context.Background(), "", period)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(pods) != 2 {
		t.Fatalf("expected 2 pods, got %d", len(pods))
	}

	// Check serve-a.
	a := pods[0]
	if a.Pod != "serve-a" {
		t.Errorf("pod = %q, want %q", a.Pod, "serve-a")
	}
	if a.CostPerHr != 3.90 {
		t.Errorf("cost/hr = %f, want 3.90", a.CostPerHr)
	}
	if a.TotalCost != 3.90 { // 1 hour × $3.90
		t.Errorf("total cost = %f, want 3.90", a.TotalCost)
	}
	if a.AvgUtil != 73 {
		t.Errorf("util = %f, want 73", a.AvgUtil)
	}
}

func TestCostByPodsNamespaceFilter(t *testing.T) {
	store := &mockStore{
		instantResults: map[string][]MetricSample{
			"ballast_pod_cost_per_hour_usd": {
				{Labels: map[string]string{"pod": "serve-a", "namespace": "search", "node": "n1", "gpu_type": "H100"}, Value: 3.90},
			},
			"ballast_gpu_utilization_percent": {
				{Labels: map[string]string{"pod": "serve-a", "namespace": "search", "node": "n1"}, Value: 50},
			},
		},
	}

	e := NewEngine(store)
	period := fixedPeriod(time.Hour)

	pods, err := e.CostByPods(context.Background(), "search", period)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(pods) != 1 {
		t.Fatalf("expected 1 pod, got %d", len(pods))
	}
	if pods[0].Namespace != "search" {
		t.Errorf("namespace = %q, want %q", pods[0].Namespace, "search")
	}
}

func TestCostByNamespaces(t *testing.T) {
	store := &mockStore{
		instantResults: map[string][]MetricSample{
			"sum by (namespace)": {
				{Labels: map[string]string{"namespace": "search"}, Value: 5.70},
				{Labels: map[string]string{"namespace": "batch"}, Value: 1.80},
			},
			"avg by (namespace)": {
				{Labels: map[string]string{"namespace": "search"}, Value: 65},
				{Labels: map[string]string{"namespace": "batch"}, Value: 40},
			},
			"count by (namespace)": {
				{Labels: map[string]string{"namespace": "search"}, Value: 2},
				{Labels: map[string]string{"namespace": "batch"}, Value: 1},
			},
		},
	}

	e := NewEngine(store)
	period := fixedPeriod(24 * time.Hour)

	ns, err := e.CostByNamespaces(context.Background(), period)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(ns) != 2 {
		t.Fatalf("expected 2 namespaces, got %d", len(ns))
	}

	if ns[0].Namespace != "search" {
		t.Errorf("ns = %q, want %q", ns[0].Namespace, "search")
	}
	if ns[0].CostPerHr != 5.70 {
		t.Errorf("cost/hr = %f, want 5.70", ns[0].CostPerHr)
	}
	if ns[0].TotalCost != 5.70*24 {
		t.Errorf("total = %f, want %f", ns[0].TotalCost, 5.70*24)
	}
	if ns[0].GPUCount != 2 {
		t.Errorf("gpu count = %d, want 2", ns[0].GPUCount)
	}
}

func TestSummary(t *testing.T) {
	store := &mockStore{
		instantResults: map[string][]MetricSample{
			"sum by (namespace)": {
				{Labels: map[string]string{"namespace": "search"}, Value: 4.00},
				{Labels: map[string]string{"namespace": "batch"}, Value: 2.00},
			},
			"avg by (namespace)": {
				{Labels: map[string]string{"namespace": "search"}, Value: 70},
				{Labels: map[string]string{"namespace": "batch"}, Value: 50},
			},
			"count by (namespace)": {
				{Labels: map[string]string{"namespace": "search"}, Value: 2},
				{Labels: map[string]string{"namespace": "batch"}, Value: 1},
			},
		},
	}

	e := NewEngine(store)
	period := fixedPeriod(time.Hour)

	s, err := e.Summary(context.Background(), period)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if s.TotalCostPerHr != 6.0 {
		t.Errorf("total cost/hr = %f, want 6.0", s.TotalCostPerHr)
	}
	if s.TotalCost != 6.0 {
		t.Errorf("total cost = %f, want 6.0", s.TotalCost)
	}
	if s.ProjectedMonthly != 6.0*730 {
		t.Errorf("projected = %f, want %f", s.ProjectedMonthly, 6.0*730)
	}
	if s.GPUCount != 3 {
		t.Errorf("gpu count = %d, want 3", s.GPUCount)
	}
	// Weighted avg: (70*2 + 50*1) / 3 = 63.33
	expectedUtil := (70.0*2 + 50.0*1) / 3.0
	if s.AvgUtil < expectedUtil-0.1 || s.AvgUtil > expectedUtil+0.1 {
		t.Errorf("avg util = %f, want ~%f", s.AvgUtil, expectedUtil)
	}
}

func TestParsePeriod(t *testing.T) {
	tests := []struct {
		input    string
		minHours float64
		maxHours float64
	}{
		{"1h", 0.9, 1.1},
		{"6h", 5.9, 6.1},
		{"24h", 23.9, 24.1},
		{"1d", 23.9, 24.1},
		{"7d", 167, 169},
		{"30d", 719, 721},
	}

	for _, tt := range tests {
		p := ParsePeriod(tt.input)
		hours := p.Hours()
		if hours < tt.minHours || hours > tt.maxHours {
			t.Errorf("ParsePeriod(%q).Hours() = %f, want between %f and %f", tt.input, hours, tt.minHours, tt.maxHours)
		}
	}
}

func TestPromDuration(t *testing.T) {
	now := time.Now()
	tests := []struct {
		period Period
		want   string
	}{
		{Period{Start: now.Add(-30 * time.Second), End: now}, "1m"},
		{Period{Start: now.Add(-5 * time.Minute), End: now}, "5m"},
		{Period{Start: now.Add(-2 * time.Hour), End: now}, "2h"},
		{Period{Start: now.Add(-48 * time.Hour), End: now}, "2d"},
	}

	for _, tt := range tests {
		got := promDuration(tt.period)
		if got != tt.want {
			t.Errorf("promDuration(%v) = %q, want %q", tt.period.End.Sub(tt.period.Start), got, tt.want)
		}
	}
}
