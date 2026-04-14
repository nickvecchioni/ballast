package telemetry

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type stubScraper struct {
	metrics *InferenceMetrics
	err     error
	name    string
}

func (s *stubScraper) Scrape(_ context.Context) (*InferenceMetrics, error) {
	return s.metrics, s.err
}

func (s *stubScraper) Name() string { return s.name }

func newTestExporter(scraper Scraper) (*InferenceExporter, *prometheus.Registry) {
	reg := prometheus.NewRegistry()
	exp := NewInferenceExporter(InferenceExporterOpts{
		Scraper:   scraper,
		PodName:   "llm-serve-abc",
		Namespace: "search",
		NodeName:  "gpu-node-01",
		ModelName: "llama-3-70b",
	})
	reg.MustRegister(exp)
	return exp, reg
}

func TestExporterCounters(t *testing.T) {
	scraper := &stubScraper{
		name: "vllm",
		metrics: &InferenceMetrics{
			PromptTokensTotal:     2450000,
			GenerationTokensTotal: 890000,
			RequestSuccessTotal:   15234,
		},
	}

	_, reg := newTestExporter(scraper)

	expected := `
		# HELP ballast_inference_prompt_tokens_total Cumulative number of prompt (input) tokens processed.
		# TYPE ballast_inference_prompt_tokens_total counter
		ballast_inference_prompt_tokens_total{model_name="llama-3-70b",namespace="search",node="gpu-node-01",pod="llm-serve-abc"} 2.45e+06
		# HELP ballast_inference_generation_tokens_total Cumulative number of generation (output) tokens produced.
		# TYPE ballast_inference_generation_tokens_total counter
		ballast_inference_generation_tokens_total{model_name="llama-3-70b",namespace="search",node="gpu-node-01",pod="llm-serve-abc"} 890000
		# HELP ballast_inference_requests_total Cumulative number of completed inference requests.
		# TYPE ballast_inference_requests_total counter
		ballast_inference_requests_total{model_name="llama-3-70b",namespace="search",node="gpu-node-01",pod="llm-serve-abc"} 15234
	`

	if err := testutil.GatherAndCompare(reg, strings.NewReader(expected),
		"ballast_inference_prompt_tokens_total",
		"ballast_inference_generation_tokens_total",
		"ballast_inference_requests_total",
	); err != nil {
		t.Errorf("counter mismatch:\n%v", err)
	}
}

func TestExporterGauges(t *testing.T) {
	scraper := &stubScraper{
		name: "vllm",
		metrics: &InferenceMetrics{
			RequestsRunning:         3,
			GPUCacheUsagePercent:    0.67,
			AvgGenerationThroughput: 2340.5,
		},
	}

	_, reg := newTestExporter(scraper)

	expected := `
		# HELP ballast_inference_requests_running Number of inference requests currently in flight.
		# TYPE ballast_inference_requests_running gauge
		ballast_inference_requests_running{model_name="llama-3-70b",namespace="search",node="gpu-node-01",pod="llm-serve-abc"} 3
		# HELP ballast_inference_gpu_cache_usage_percent GPU KV-cache utilization (0-1).
		# TYPE ballast_inference_gpu_cache_usage_percent gauge
		ballast_inference_gpu_cache_usage_percent{model_name="llama-3-70b",namespace="search",node="gpu-node-01",pod="llm-serve-abc"} 0.67
		# HELP ballast_inference_generation_throughput_tokens_per_second Average generation throughput in tokens per second.
		# TYPE ballast_inference_generation_throughput_tokens_per_second gauge
		ballast_inference_generation_throughput_tokens_per_second{model_name="llama-3-70b",namespace="search",node="gpu-node-01",pod="llm-serve-abc"} 2340.5
	`

	if err := testutil.GatherAndCompare(reg, strings.NewReader(expected),
		"ballast_inference_requests_running",
		"ballast_inference_gpu_cache_usage_percent",
		"ballast_inference_generation_throughput_tokens_per_second",
	); err != nil {
		t.Errorf("gauge mismatch:\n%v", err)
	}
}

func TestExporterAllMetricNames(t *testing.T) {
	scraper := &stubScraper{
		name: "vllm",
		metrics: &InferenceMetrics{
			PromptTokensTotal:       100,
			GenerationTokensTotal:   200,
			RequestSuccessTotal:     10,
			RequestsRunning:         1,
			GPUCacheUsagePercent:    0.5,
			AvgGenerationThroughput: 1000,
		},
	}

	_, reg := newTestExporter(scraper)

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	expected := map[string]bool{
		"ballast_inference_prompt_tokens_total":                       false,
		"ballast_inference_generation_tokens_total":                   false,
		"ballast_inference_requests_total":                            false,
		"ballast_inference_requests_running":                          false,
		"ballast_inference_gpu_cache_usage_percent":                   false,
		"ballast_inference_generation_throughput_tokens_per_second":   false,
	}

	for _, mf := range mfs {
		if _, ok := expected[mf.GetName()]; ok {
			expected[mf.GetName()] = true
		}
	}

	for name, found := range expected {
		if !found {
			t.Errorf("metric %s not found", name)
		}
	}
}

func TestExporterLabels(t *testing.T) {
	scraper := &stubScraper{
		name:    "vllm",
		metrics: &InferenceMetrics{PromptTokensTotal: 1},
	}

	_, reg := newTestExporter(scraper)

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	wantLabels := map[string]string{
		"pod":        "llm-serve-abc",
		"namespace":  "search",
		"node":       "gpu-node-01",
		"model_name": "llama-3-70b",
	}

	for _, mf := range mfs {
		for _, m := range mf.GetMetric() {
			for _, lp := range m.GetLabel() {
				want, ok := wantLabels[lp.GetName()]
				if !ok {
					t.Errorf("unexpected label %q on %s", lp.GetName(), mf.GetName())
					continue
				}
				if lp.GetValue() != want {
					t.Errorf("%s label %s = %q, want %q", mf.GetName(), lp.GetName(), lp.GetValue(), want)
				}
			}
		}
	}
}

func TestExporterScrapeError(t *testing.T) {
	scraper := &stubScraper{
		name: "vllm",
		err:  fmt.Errorf("connection refused"),
	}

	_, reg := newTestExporter(scraper)

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	// No metrics should be emitted on error.
	if len(mfs) != 0 {
		t.Errorf("expected 0 metric families on error, got %d", len(mfs))
	}
}

func TestExporterZeroMetrics(t *testing.T) {
	scraper := &stubScraper{
		name:    "vllm",
		metrics: &InferenceMetrics{},
	}

	_, reg := newTestExporter(scraper)

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	// All 6 metrics should be present even with zero values.
	if len(mfs) != 6 {
		t.Errorf("expected 6 metric families, got %d", len(mfs))
	}
}

func TestExporterDescribe(t *testing.T) {
	scraper := &stubScraper{name: "vllm", metrics: &InferenceMetrics{}}
	exp, _ := newTestExporter(scraper)

	ch := make(chan *prometheus.Desc, 10)
	exp.Describe(ch)
	close(ch)

	var descs []*prometheus.Desc
	for d := range ch {
		descs = append(descs, d)
	}

	if len(descs) != 6 {
		t.Errorf("expected 6 descriptors, got %d", len(descs))
	}
}

func TestExporterLint(t *testing.T) {
	scraper := &stubScraper{
		name: "vllm",
		metrics: &InferenceMetrics{
			PromptTokensTotal:       100,
			GenerationTokensTotal:   200,
			RequestSuccessTotal:     10,
			RequestsRunning:         1,
			GPUCacheUsagePercent:    0.5,
			AvgGenerationThroughput: 1000,
		},
	}

	_, reg := newTestExporter(scraper)

	problems, err := testutil.GatherAndLint(reg)
	if err != nil {
		t.Fatalf("lint error: %v", err)
	}
	for _, p := range problems {
		t.Errorf("lint: %s", p.Text)
	}
}
