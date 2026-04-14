package collector

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/nickvecchioni/ballast/pkg/billing"
)

const (
	metricsNamespace = "ballast"
	defaultInterval  = 1 * time.Second
)

var (
	gpuLabels  = []string{"gpu_uuid", "pod", "namespace", "node"}
	costLabels = []string{"pod", "namespace", "node", "gpu_type"}
)

// MetricsCollector ties together GPUCollector and PodResourcesClient,
// periodically collecting GPU metrics, joining them with pod ownership,
// and exposing the result as Prometheus gauges.
type MetricsCollector struct {
	gpu      GPUCollector
	pods     PodResourcesClient
	pricing  billing.PricingProvider
	nodeName string
	interval time.Duration
	logger   *slog.Logger

	utilization *prometheus.GaugeVec
	memoryUsed  *prometheus.GaugeVec
	memoryTotal *prometheus.GaugeVec
	power       *prometheus.GaugeVec
	temperature *prometheus.GaugeVec
	costPerHour *prometheus.GaugeVec
}

// MetricsCollectorOpts configures a MetricsCollector.
type MetricsCollectorOpts struct {
	GPUCollector GPUCollector
	PodResources PodResourcesClient
	Pricing      billing.PricingProvider // optional; enables cost metric
	NodeName     string
	Interval     time.Duration
	Registry     prometheus.Registerer
	Logger       *slog.Logger
}

// NewMetricsCollector creates a MetricsCollector, registering Prometheus
// gauges with the provided registry.
func NewMetricsCollector(opts MetricsCollectorOpts) (*MetricsCollector, error) {
	if opts.Interval == 0 {
		opts.Interval = defaultInterval
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	mc := &MetricsCollector{
		gpu:      opts.GPUCollector,
		pods:     opts.PodResources,
		pricing:  opts.Pricing,
		nodeName: opts.NodeName,
		interval: opts.Interval,
		logger:   opts.Logger,
		utilization: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: metricsNamespace,
			Name:      "gpu_utilization_percent",
			Help:      "Current GPU compute utilization (0-100).",
		}, gpuLabels),
		memoryUsed: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: metricsNamespace,
			Name:      "gpu_memory_used_bytes",
			Help:      "GPU memory currently in use in bytes.",
		}, gpuLabels),
		memoryTotal: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: metricsNamespace,
			Name:      "gpu_memory_total_bytes",
			Help:      "Total GPU memory in bytes.",
		}, gpuLabels),
		power: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: metricsNamespace,
			Name:      "gpu_power_watts",
			Help:      "Current GPU power draw in watts.",
		}, gpuLabels),
		temperature: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: metricsNamespace,
			Name:      "gpu_temperature_celsius",
			Help:      "Current GPU temperature in degrees Celsius.",
		}, gpuLabels),
		costPerHour: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: metricsNamespace,
			Name:      "pod_cost_per_hour_usd",
			Help:      "Estimated cost per hour in USD based on GPU utilization and pricing.",
		}, costLabels),
	}

	for _, c := range mc.collectors() {
		if err := opts.Registry.Register(c); err != nil {
			return nil, err
		}
	}
	return mc, nil
}

func (mc *MetricsCollector) collectors() []prometheus.Collector {
	return []prometheus.Collector{
		mc.utilization,
		mc.memoryUsed,
		mc.memoryTotal,
		mc.power,
		mc.temperature,
		mc.costPerHour,
	}
}

// Run starts the collection loop. It blocks until ctx is cancelled.
func (mc *MetricsCollector) Run(ctx context.Context) {
	ticker := time.NewTicker(mc.interval)
	defer ticker.Stop()

	// Collect immediately on start, then on every tick.
	mc.collect(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			mc.collect(ctx)
		}
	}
}

// CollectOnce runs a single collection cycle. Useful for testing.
func (mc *MetricsCollector) CollectOnce(ctx context.Context) {
	mc.collect(ctx)
}

func (mc *MetricsCollector) collect(ctx context.Context) {
	gpuMetrics, err := mc.gpu.Collect()
	if err != nil {
		mc.logger.Error("gpu collect failed", "err", err)
		return
	}

	podMap, err := mc.pods.List(ctx)
	if err != nil {
		mc.logger.Error("pod resources list failed", "err", err)
		return
	}

	// Reset all gauges so stale GPU/pod combinations are cleared.
	mc.utilization.Reset()
	mc.memoryUsed.Reset()
	mc.memoryTotal.Reset()
	mc.power.Reset()
	mc.temperature.Reset()
	mc.costPerHour.Reset()

	// costAccum aggregates cost across multiple GPUs for the same
	// (pod, namespace, gpu_type) tuple.
	type costKey struct{ pod, namespace, gpuType string }
	costAccum := make(map[costKey]float64)

	for _, gm := range gpuMetrics {
		podName := ""
		namespace := ""
		if info, ok := podMap[gm.UUID]; ok {
			// Match by GPU UUID (e.g. "GPU-271452fb-...")
			podName = info.PodName
			namespace = info.Namespace
		} else if info, ok := podMap[fmt.Sprintf("nvidia%d", gm.Index)]; ok {
			// Match by device index (e.g. "nvidia0") — used by some
			// device plugin configurations including GKE.
			podName = info.PodName
			namespace = info.Namespace
		}

		labels := prometheus.Labels{
			"gpu_uuid":  gm.UUID,
			"pod":       podName,
			"namespace": namespace,
			"node":      mc.nodeName,
		}

		mc.utilization.With(labels).Set(float64(gm.UtilizationPercent))
		mc.memoryUsed.With(labels).Set(float64(gm.MemoryUsedBytes))
		mc.memoryTotal.With(labels).Set(float64(gm.MemoryTotalBytes))
		mc.power.With(labels).Set(gm.PowerDrawWatts)
		mc.temperature.With(labels).Set(float64(gm.TemperatureCelsius))

		if mc.pricing != nil {
			rate := mc.pricing.GetCostPerHour(gm.DeviceName)
			cost := (float64(gm.UtilizationPercent) / 100.0) * rate
			key := costKey{pod: podName, namespace: namespace, gpuType: gm.DeviceName}
			costAccum[key] += cost
		}
	}

	for key, cost := range costAccum {
		mc.costPerHour.With(prometheus.Labels{
			"pod":       key.pod,
			"namespace": key.namespace,
			"node":      mc.nodeName,
			"gpu_type":  key.gpuType,
		}).Set(cost)
	}
}
