package telemetry

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

// vLLM metric names (Prometheus exposition format uses colons).
const (
	vllmPromptTokens      = "vllm:num_prompt_tokens_total"
	vllmGenerationTokens  = "vllm:num_generation_tokens_total"
	vllmRequestsRunning   = "vllm:num_requests_running"
	vllmRequestSuccess    = "vllm:request_success_total"
	vllmGenThroughput     = "vllm:avg_generation_throughput_toks_per_s"
	vllmGPUCacheUsage     = "vllm:gpu_cache_usage_perc"
)

// VLLMScraper implements Scraper by hitting a vLLM /metrics endpoint
// and parsing the Prometheus exposition format.
type VLLMScraper struct {
	targetURL  string
	httpClient *http.Client
}

// VLLMScraperOpts configures a VLLMScraper.
type VLLMScraperOpts struct {
	// TargetURL is the full URL to the vLLM metrics endpoint
	// (e.g. "http://localhost:8000/metrics").
	TargetURL string
	// Timeout for each HTTP scrape request. Defaults to 5s.
	Timeout time.Duration
}

// NewVLLMScraper creates a scraper targeting the given vLLM endpoint.
func NewVLLMScraper(opts VLLMScraperOpts) *VLLMScraper {
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	return &VLLMScraper{
		targetURL: opts.TargetURL,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

func (s *VLLMScraper) Name() string { return "vllm" }

// Scrape fetches /metrics from the vLLM server and parses the response.
func (s *VLLMScraper) Scrape(ctx context.Context) (*InferenceMetrics, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.targetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("vllm scrape: create request: %w", err)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("vllm scrape: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("vllm scrape: unexpected status %d", resp.StatusCode)
	}

	return parseVLLMMetrics(resp.Body)
}

// parseVLLMMetrics reads Prometheus exposition format and extracts the
// vLLM metrics we care about.
func parseVLLMMetrics(r io.Reader) (*InferenceMetrics, error) {
	parser := expfmt.NewTextParser(model.UTF8Validation)
	families, err := parser.TextToMetricFamilies(r)
	if err != nil {
		return nil, fmt.Errorf("vllm scrape: parse metrics: %w", err)
	}

	m := &InferenceMetrics{}
	for name, mf := range families {
		val := metricValue(mf)
		if math.IsNaN(val) {
			continue
		}
		switch name {
		case vllmPromptTokens:
			m.PromptTokensTotal = val
		case vllmGenerationTokens:
			m.GenerationTokensTotal = val
		case vllmRequestsRunning:
			m.RequestsRunning = val
		case vllmRequestSuccess:
			m.RequestSuccessTotal = val
		case vllmGenThroughput:
			m.AvgGenerationThroughput = val
		case vllmGPUCacheUsage:
			m.GPUCacheUsagePercent = val
		}
	}
	return m, nil
}

// metricValue extracts a single scalar value from a metric family.
// For counters/gauges with multiple label combinations, it sums them
// (vLLM typically has a single series per metric name). Histograms and
// summaries are ignored.
func metricValue(mf *dto.MetricFamily) float64 {
	var total float64
	for _, m := range mf.GetMetric() {
		switch {
		case m.GetGauge() != nil:
			total += m.GetGauge().GetValue()
		case m.GetCounter() != nil:
			total += m.GetCounter().GetValue()
		case m.GetUntyped() != nil:
			total += m.GetUntyped().GetValue()
		}
	}
	return total
}

// ScrapeVLLMText is a convenience for tests: parses vLLM metrics from a
// raw Prometheus text string.
func ScrapeVLLMText(text string) (*InferenceMetrics, error) {
	return parseVLLMMetrics(strings.NewReader(text))
}
