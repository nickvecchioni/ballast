package telemetry

import "context"

// InferenceMetrics holds a snapshot of token-level metrics from an
// inference server.
type InferenceMetrics struct {
	// PromptTokensTotal is the cumulative number of input tokens processed.
	PromptTokensTotal float64
	// GenerationTokensTotal is the cumulative number of output tokens produced.
	GenerationTokensTotal float64
	// RequestsRunning is the current number of in-flight requests.
	RequestsRunning float64
	// RequestSuccessTotal is the cumulative count of completed requests.
	RequestSuccessTotal float64
	// AvgGenerationThroughput is tokens per second (generation).
	AvgGenerationThroughput float64
	// GPUCacheUsagePercent is KV cache utilization (0-1).
	GPUCacheUsagePercent float64
}

// Scraper fetches inference server metrics. Implementations exist for
// vLLM (Phase 1), with TGI and Triton planned.
type Scraper interface {
	// Scrape fetches the latest metrics from the inference server.
	Scrape(ctx context.Context) (*InferenceMetrics, error)

	// Name returns a human-readable identifier for this scraper type
	// (e.g. "vllm", "tgi").
	Name() string
}
