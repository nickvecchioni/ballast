package telemetry

import (
	"context"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const inferenceNamespace = "ballast_inference"

var inferenceLabels = []string{"pod", "namespace", "node", "model_name"}

// InferenceExporter is a prometheus.Collector that scrapes an inference
// server on each Prometheus collection and re-exposes the metrics with
// ballast_inference_ prefix and pod-level labels.
type InferenceExporter struct {
	scraper    Scraper
	labels     prometheus.Labels
	labelVals  []string
	scrapeTimeout time.Duration
	logger     *slog.Logger

	promptTokensDesc      *prometheus.Desc
	generationTokensDesc  *prometheus.Desc
	requestsTotalDesc     *prometheus.Desc
	requestsRunningDesc   *prometheus.Desc
	cachUsageDesc         *prometheus.Desc
	throughputDesc        *prometheus.Desc
}

// InferenceExporterOpts configures an InferenceExporter.
type InferenceExporterOpts struct {
	Scraper       Scraper
	PodName       string
	Namespace     string
	NodeName      string
	ModelName     string
	ScrapeTimeout time.Duration // defaults to 5s
	Logger        *slog.Logger
}

// NewInferenceExporter creates an exporter that re-exposes inference
// server metrics with the ballast_inference_ prefix.
func NewInferenceExporter(opts InferenceExporterOpts) *InferenceExporter {
	if opts.ScrapeTimeout == 0 {
		opts.ScrapeTimeout = 5 * time.Second
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	labelVals := []string{opts.PodName, opts.Namespace, opts.NodeName, opts.ModelName}

	return &InferenceExporter{
		scraper:       opts.Scraper,
		labelVals:     labelVals,
		scrapeTimeout: opts.ScrapeTimeout,
		logger:        opts.Logger,
		promptTokensDesc: prometheus.NewDesc(
			inferenceNamespace+"_prompt_tokens_total",
			"Cumulative number of prompt (input) tokens processed.",
			inferenceLabels, nil,
		),
		generationTokensDesc: prometheus.NewDesc(
			inferenceNamespace+"_generation_tokens_total",
			"Cumulative number of generation (output) tokens produced.",
			inferenceLabels, nil,
		),
		requestsTotalDesc: prometheus.NewDesc(
			inferenceNamespace+"_requests_total",
			"Cumulative number of completed inference requests.",
			inferenceLabels, nil,
		),
		requestsRunningDesc: prometheus.NewDesc(
			inferenceNamespace+"_requests_running",
			"Number of inference requests currently in flight.",
			inferenceLabels, nil,
		),
		cachUsageDesc: prometheus.NewDesc(
			inferenceNamespace+"_gpu_cache_usage_percent",
			"GPU KV-cache utilization (0-1).",
			inferenceLabels, nil,
		),
		throughputDesc: prometheus.NewDesc(
			inferenceNamespace+"_generation_throughput_tokens_per_second",
			"Average generation throughput in tokens per second.",
			inferenceLabels, nil,
		),
	}
}

// Describe implements prometheus.Collector.
func (e *InferenceExporter) Describe(ch chan<- *prometheus.Desc) {
	ch <- e.promptTokensDesc
	ch <- e.generationTokensDesc
	ch <- e.requestsTotalDesc
	ch <- e.requestsRunningDesc
	ch <- e.cachUsageDesc
	ch <- e.throughputDesc
}

// Collect implements prometheus.Collector. It scrapes the inference server
// and emits const metrics.
func (e *InferenceExporter) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), e.scrapeTimeout)
	defer cancel()

	m, err := e.scraper.Scrape(ctx)
	if err != nil {
		e.logger.Error("inference scrape failed", "scraper", e.scraper.Name(), "err", err)
		return
	}

	ch <- prometheus.MustNewConstMetric(e.promptTokensDesc, prometheus.CounterValue, m.PromptTokensTotal, e.labelVals...)
	ch <- prometheus.MustNewConstMetric(e.generationTokensDesc, prometheus.CounterValue, m.GenerationTokensTotal, e.labelVals...)
	ch <- prometheus.MustNewConstMetric(e.requestsTotalDesc, prometheus.CounterValue, m.RequestSuccessTotal, e.labelVals...)
	ch <- prometheus.MustNewConstMetric(e.requestsRunningDesc, prometheus.GaugeValue, m.RequestsRunning, e.labelVals...)
	ch <- prometheus.MustNewConstMetric(e.cachUsageDesc, prometheus.GaugeValue, m.GPUCacheUsagePercent, e.labelVals...)
	ch <- prometheus.MustNewConstMetric(e.throughputDesc, prometheus.GaugeValue, m.AvgGenerationThroughput, e.labelVals...)
}
