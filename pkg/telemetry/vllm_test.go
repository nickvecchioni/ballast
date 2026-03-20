package telemetry

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Sample vLLM /metrics output (Prometheus exposition format).
const sampleVLLMMetrics = `# HELP vllm:num_requests_running Number of requests currently running on GPU.
# TYPE vllm:num_requests_running gauge
vllm:num_requests_running 3
# HELP vllm:num_prompt_tokens_total Number of prefill tokens processed.
# TYPE vllm:num_prompt_tokens_total counter
vllm:num_prompt_tokens_total 2450000
# HELP vllm:num_generation_tokens_total Number of generation tokens processed.
# TYPE vllm:num_generation_tokens_total counter
vllm:num_generation_tokens_total 890000
# HELP vllm:request_success_total Count of successfully completed requests.
# TYPE vllm:request_success_total counter
vllm:request_success_total 15234
# HELP vllm:avg_generation_throughput_toks_per_s Average generation throughput in tokens/s.
# TYPE vllm:avg_generation_throughput_toks_per_s gauge
vllm:avg_generation_throughput_toks_per_s 2340.5
# HELP vllm:gpu_cache_usage_perc GPU KV-cache usage. 1 means 100 percent usage.
# TYPE vllm:gpu_cache_usage_perc gauge
vllm:gpu_cache_usage_perc 0.67
`

func TestScrapeVLLMText(t *testing.T) {
	m, err := ScrapeVLLMText(sampleVLLMMetrics)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	tests := []struct {
		name string
		got  float64
		want float64
	}{
		{"PromptTokensTotal", m.PromptTokensTotal, 2450000},
		{"GenerationTokensTotal", m.GenerationTokensTotal, 890000},
		{"RequestsRunning", m.RequestsRunning, 3},
		{"RequestSuccessTotal", m.RequestSuccessTotal, 15234},
		{"AvgGenerationThroughput", m.AvgGenerationThroughput, 2340.5},
		{"GPUCacheUsagePercent", m.GPUCacheUsagePercent, 0.67},
	}

	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s = %f, want %f", tt.name, tt.got, tt.want)
		}
	}
}

func TestScrapeHTTPServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		w.Write([]byte(sampleVLLMMetrics))
	}))
	defer srv.Close()

	scraper := NewVLLMScraper(VLLMScraperOpts{TargetURL: srv.URL})
	m, err := scraper.Scrape(context.Background())
	if err != nil {
		t.Fatalf("scrape error: %v", err)
	}

	if m.PromptTokensTotal != 2450000 {
		t.Errorf("prompt tokens = %f, want 2450000", m.PromptTokensTotal)
	}
	if m.GenerationTokensTotal != 890000 {
		t.Errorf("generation tokens = %f, want 890000", m.GenerationTokensTotal)
	}
	if m.GPUCacheUsagePercent != 0.67 {
		t.Errorf("cache usage = %f, want 0.67", m.GPUCacheUsagePercent)
	}
}

func TestScrapeHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	scraper := NewVLLMScraper(VLLMScraperOpts{TargetURL: srv.URL})
	_, err := scraper.Scrape(context.Background())
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestScrapeConnectionRefused(t *testing.T) {
	scraper := NewVLLMScraper(VLLMScraperOpts{
		TargetURL: "http://127.0.0.1:1", // nothing listening
		Timeout:   100 * time.Millisecond,
	})
	_, err := scraper.Scrape(context.Background())
	if err == nil {
		t.Fatal("expected error for connection refused")
	}
}

func TestScrapeContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Block until context is cancelled.
		<-r.Context().Done()
	}))
	defer srv.Close()

	scraper := NewVLLMScraper(VLLMScraperOpts{
		TargetURL: srv.URL,
		Timeout:   5 * time.Second,
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := scraper.Scrape(ctx)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestScrapeEmptyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(""))
	}))
	defer srv.Close()

	scraper := NewVLLMScraper(VLLMScraperOpts{TargetURL: srv.URL})
	m, err := scraper.Scrape(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// All values should be zero.
	if m.PromptTokensTotal != 0 || m.GenerationTokensTotal != 0 || m.RequestsRunning != 0 {
		t.Error("expected all zeros for empty response")
	}
}

func TestScrapePartialMetrics(t *testing.T) {
	partial := `# TYPE vllm:num_prompt_tokens_total counter
vllm:num_prompt_tokens_total 42000
# TYPE vllm:gpu_cache_usage_perc gauge
vllm:gpu_cache_usage_perc 0.95
`
	m, err := ScrapeVLLMText(partial)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	if m.PromptTokensTotal != 42000 {
		t.Errorf("prompt tokens = %f, want 42000", m.PromptTokensTotal)
	}
	if m.GPUCacheUsagePercent != 0.95 {
		t.Errorf("cache = %f, want 0.95", m.GPUCacheUsagePercent)
	}
	// Absent metrics stay at zero.
	if m.GenerationTokensTotal != 0 {
		t.Errorf("generation tokens = %f, want 0", m.GenerationTokensTotal)
	}
}

func TestScrapeInvalidMetricsBody(t *testing.T) {
	_, err := ScrapeVLLMText("this is not prometheus format\n\n")
	if err == nil {
		t.Fatal("expected error for invalid prometheus format")
	}
}

func TestScrapeUnrelatedMetricsIgnored(t *testing.T) {
	mixed := `# TYPE http_requests_total counter
http_requests_total 99999
# TYPE vllm:num_prompt_tokens_total counter
vllm:num_prompt_tokens_total 500
# TYPE node_cpu_seconds_total counter
node_cpu_seconds_total 12345
`
	m, err := ScrapeVLLMText(mixed)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	if m.PromptTokensTotal != 500 {
		t.Errorf("prompt tokens = %f, want 500", m.PromptTokensTotal)
	}
	// Unrelated metrics should not appear anywhere.
	if m.RequestSuccessTotal != 0 {
		t.Errorf("request success = %f, want 0", m.RequestSuccessTotal)
	}
}

func TestScrapeZeroValues(t *testing.T) {
	zeros := `# TYPE vllm:num_prompt_tokens_total counter
vllm:num_prompt_tokens_total 0
# TYPE vllm:num_generation_tokens_total counter
vllm:num_generation_tokens_total 0
# TYPE vllm:num_requests_running gauge
vllm:num_requests_running 0
# TYPE vllm:gpu_cache_usage_perc gauge
vllm:gpu_cache_usage_perc 0
`
	m, err := ScrapeVLLMText(zeros)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	if m.PromptTokensTotal != 0 || m.GenerationTokensTotal != 0 ||
		m.RequestsRunning != 0 || m.GPUCacheUsagePercent != 0 {
		t.Error("expected all zeros")
	}
}

func TestScraperName(t *testing.T) {
	s := NewVLLMScraper(VLLMScraperOpts{TargetURL: "http://localhost:8000/metrics"})
	if s.Name() != "vllm" {
		t.Errorf("name = %q, want %q", s.Name(), "vllm")
	}
}

func TestScraperImplementsInterface(t *testing.T) {
	var _ Scraper = (*VLLMScraper)(nil)
}

func TestDefaultTimeout(t *testing.T) {
	s := NewVLLMScraper(VLLMScraperOpts{TargetURL: "http://localhost:8000/metrics"})
	if s.httpClient.Timeout != 5*time.Second {
		t.Errorf("timeout = %v, want 5s", s.httpClient.Timeout)
	}
}

func TestCustomTimeout(t *testing.T) {
	s := NewVLLMScraper(VLLMScraperOpts{
		TargetURL: "http://localhost:8000/metrics",
		Timeout:   10 * time.Second,
	})
	if s.httpClient.Timeout != 10*time.Second {
		t.Errorf("timeout = %v, want 10s", s.httpClient.Timeout)
	}
}

func TestScrapeLargeValues(t *testing.T) {
	large := `# TYPE vllm:num_prompt_tokens_total counter
vllm:num_prompt_tokens_total 9999999999999
# TYPE vllm:num_generation_tokens_total counter
vllm:num_generation_tokens_total 5555555555555
`
	m, err := ScrapeVLLMText(large)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	if m.PromptTokensTotal != 9999999999999 {
		t.Errorf("prompt tokens = %f, want 9999999999999", m.PromptTokensTotal)
	}
	if m.GenerationTokensTotal != 5555555555555 {
		t.Errorf("generation tokens = %f, want 5555555555555", m.GenerationTokensTotal)
	}
}

func TestScrapeNaNIgnored(t *testing.T) {
	nan := `# TYPE vllm:avg_generation_throughput_toks_per_s gauge
vllm:avg_generation_throughput_toks_per_s NaN
# TYPE vllm:num_prompt_tokens_total counter
vllm:num_prompt_tokens_total 100
`
	m, err := ScrapeVLLMText(nan)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	// NaN should be skipped, leaving field at zero.
	if m.AvgGenerationThroughput != 0 {
		t.Errorf("throughput = %f, want 0 (NaN skipped)", m.AvgGenerationThroughput)
	}
	if m.PromptTokensTotal != 100 {
		t.Errorf("prompt tokens = %f, want 100", m.PromptTokensTotal)
	}
}

func TestMetricValueNaN(t *testing.T) {
	// Verify our NaN check in metricValue via parseVLLMMetrics.
	text := `# TYPE vllm:gpu_cache_usage_perc gauge
vllm:gpu_cache_usage_perc NaN
`
	m, _ := ScrapeVLLMText(text)
	if math.IsNaN(m.GPUCacheUsagePercent) {
		t.Error("NaN should not propagate to InferenceMetrics")
	}
}
