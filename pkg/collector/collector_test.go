package collector

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/nickvecchioni/ballast/pkg/billing"
	"github.com/nickvecchioni/ballast/pkg/models"
)

// --- mocks for the high-level interfaces ---

type stubGPUCollector struct {
	metrics []models.GPUMetrics
	err     error
}

func (s *stubGPUCollector) Collect() ([]models.GPUMetrics, error) { return s.metrics, s.err }
func (s *stubGPUCollector) Close() error                         { return nil }

type stubPodResources struct {
	mapping map[string]PodInfo
	err     error
}

func (s *stubPodResources) List(_ context.Context) (map[string]PodInfo, error) {
	return s.mapping, s.err
}
func (s *stubPodResources) Close() error { return nil }

// stubPricing implements billing.PricingProvider with a fixed price map.
type stubPricing struct {
	prices       map[string]float64
	defaultPrice float64
}

func (s *stubPricing) GetCostPerHour(gpuType string) float64 {
	if cost, ok := s.prices[gpuType]; ok {
		return cost
	}
	return s.defaultPrice
}

var _ billing.PricingProvider = (*stubPricing)(nil)

// --- helpers ---

func newTestCollector(t *testing.T, gpu GPUCollector, pods PodResourcesClient) (*MetricsCollector, *prometheus.Registry) {
	t.Helper()
	return newTestCollectorWithPricing(t, gpu, pods, nil)
}

func newTestCollectorWithPricing(t *testing.T, gpu GPUCollector, pods PodResourcesClient, pricing billing.PricingProvider) (*MetricsCollector, *prometheus.Registry) {
	t.Helper()
	reg := prometheus.NewRegistry()
	mc, err := NewMetricsCollector(MetricsCollectorOpts{
		GPUCollector: gpu,
		PodResources: pods,
		Pricing:      pricing,
		NodeName:     "gpu-node-01",
		Registry:     reg,
	})
	if err != nil {
		t.Fatalf("NewMetricsCollector: %v", err)
	}
	return mc, reg
}

func gaugeValue(t *testing.T, reg *prometheus.Registry, name string, labels prometheus.Labels) float64 {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			match := true
			for _, lp := range m.GetLabel() {
				if v, ok := labels[lp.GetName()]; ok && v != lp.GetValue() {
					match = false
					break
				}
			}
			if match {
				return m.GetGauge().GetValue()
			}
		}
	}
	t.Fatalf("metric %s with labels %v not found", name, labels)
	return 0
}

func TestCollectOnceJoinsGPUAndPod(t *testing.T) {
	gpu := &stubGPUCollector{
		metrics: []models.GPUMetrics{
			{
				UUID:               "GPU-aaa",
				UtilizationPercent: 73,
				MemoryUsedBytes:    54 * 1024 * 1024 * 1024,
				MemoryTotalBytes:   80 * 1024 * 1024 * 1024,
				PowerDrawWatts:     350.0,
				TemperatureCelsius: 62,
			},
		},
	}
	pods := &stubPodResources{
		mapping: map[string]PodInfo{
			"GPU-aaa": {Namespace: "search", PodName: "llm-serve-abc", ContainerName: "inference"},
		},
	}

	mc, reg := newTestCollector(t, gpu, pods)
	mc.CollectOnce(context.Background())

	labels := prometheus.Labels{
		"gpu_uuid":  "GPU-aaa",
		"pod":       "llm-serve-abc",
		"namespace": "search",
		"node":      "gpu-node-01",
	}

	if v := gaugeValue(t, reg, "ballast_gpu_utilization_percent", labels); v != 73 {
		t.Errorf("utilization = %f, want 73", v)
	}
	if v := gaugeValue(t, reg, "ballast_gpu_memory_used_bytes", labels); v != float64(54*1024*1024*1024) {
		t.Errorf("memory used = %f, want %f", v, float64(54*1024*1024*1024))
	}
	if v := gaugeValue(t, reg, "ballast_gpu_memory_total_bytes", labels); v != float64(80*1024*1024*1024) {
		t.Errorf("memory total = %f, want %f", v, float64(80*1024*1024*1024))
	}
	if v := gaugeValue(t, reg, "ballast_gpu_power_watts", labels); v != 350.0 {
		t.Errorf("power = %f, want 350", v)
	}
	if v := gaugeValue(t, reg, "ballast_gpu_temperature_celsius", labels); v != 62 {
		t.Errorf("temperature = %f, want 62", v)
	}
}

func TestCollectOnceUnmappedGPU(t *testing.T) {
	gpu := &stubGPUCollector{
		metrics: []models.GPUMetrics{
			{UUID: "GPU-orphan", UtilizationPercent: 10, PowerDrawWatts: 50.0},
		},
	}
	pods := &stubPodResources{mapping: map[string]PodInfo{}}

	mc, reg := newTestCollector(t, gpu, pods)
	mc.CollectOnce(context.Background())

	labels := prometheus.Labels{
		"gpu_uuid":  "GPU-orphan",
		"pod":       "",
		"namespace": "",
		"node":      "gpu-node-01",
	}

	if v := gaugeValue(t, reg, "ballast_gpu_utilization_percent", labels); v != 10 {
		t.Errorf("utilization = %f, want 10", v)
	}
}

func TestCollectOnceMultipleGPUs(t *testing.T) {
	gpu := &stubGPUCollector{
		metrics: []models.GPUMetrics{
			{UUID: "GPU-001", UtilizationPercent: 40, PowerDrawWatts: 200},
			{UUID: "GPU-002", UtilizationPercent: 90, PowerDrawWatts: 400},
		},
	}
	pods := &stubPodResources{
		mapping: map[string]PodInfo{
			"GPU-001": {Namespace: "search", PodName: "pod-a"},
			"GPU-002": {Namespace: "recommend", PodName: "pod-b"},
		},
	}

	mc, reg := newTestCollector(t, gpu, pods)
	mc.CollectOnce(context.Background())

	v1 := gaugeValue(t, reg, "ballast_gpu_utilization_percent", prometheus.Labels{
		"gpu_uuid": "GPU-001", "pod": "pod-a", "namespace": "search", "node": "gpu-node-01",
	})
	if v1 != 40 {
		t.Errorf("gpu-001 utilization = %f, want 40", v1)
	}

	v2 := gaugeValue(t, reg, "ballast_gpu_utilization_percent", prometheus.Labels{
		"gpu_uuid": "GPU-002", "pod": "pod-b", "namespace": "recommend", "node": "gpu-node-01",
	})
	if v2 != 90 {
		t.Errorf("gpu-002 utilization = %f, want 90", v2)
	}
}

func TestCollectResetsStaleMetrics(t *testing.T) {
	gpu := &stubGPUCollector{
		metrics: []models.GPUMetrics{
			{UUID: "GPU-001", UtilizationPercent: 50, PowerDrawWatts: 100},
			{UUID: "GPU-002", UtilizationPercent: 60, PowerDrawWatts: 200},
		},
	}
	pods := &stubPodResources{
		mapping: map[string]PodInfo{
			"GPU-001": {Namespace: "ns1", PodName: "pod-a"},
			"GPU-002": {Namespace: "ns2", PodName: "pod-b"},
		},
	}

	mc, reg := newTestCollector(t, gpu, pods)

	// First collection: both GPUs present.
	mc.CollectOnce(context.Background())

	// Second collection: GPU-002 disappears.
	gpu.metrics = []models.GPUMetrics{
		{UUID: "GPU-001", UtilizationPercent: 55, PowerDrawWatts: 110},
	}
	mc.CollectOnce(context.Background())

	// GPU-001 should have updated value.
	v := gaugeValue(t, reg, "ballast_gpu_utilization_percent", prometheus.Labels{
		"gpu_uuid": "GPU-001", "pod": "pod-a", "namespace": "ns1", "node": "gpu-node-01",
	})
	if v != 55 {
		t.Errorf("gpu-001 utilization = %f, want 55", v)
	}

	// GPU-002's old metrics should be gone.
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range mfs {
		for _, m := range mf.GetMetric() {
			for _, lp := range m.GetLabel() {
				if lp.GetName() == "gpu_uuid" && lp.GetValue() == "GPU-002" {
					t.Errorf("stale metric for GPU-002 still present in %s", mf.GetName())
				}
			}
		}
	}
}

func TestCollectGPUErrorContinues(t *testing.T) {
	gpu := &stubGPUCollector{err: fmt.Errorf("nvml exploded")}
	pods := &stubPodResources{mapping: map[string]PodInfo{}}

	mc, reg := newTestCollector(t, gpu, pods)
	mc.CollectOnce(context.Background())

	// No metrics should be emitted (and no panic).
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	if len(mfs) != 0 {
		t.Errorf("expected 0 metric families, got %d", len(mfs))
	}
}

func TestCollectPodResourcesErrorContinues(t *testing.T) {
	gpu := &stubGPUCollector{
		metrics: []models.GPUMetrics{
			{UUID: "GPU-001", UtilizationPercent: 50},
		},
	}
	pods := &stubPodResources{err: fmt.Errorf("socket gone")}

	mc, reg := newTestCollector(t, gpu, pods)
	mc.CollectOnce(context.Background())

	// No metrics should be emitted.
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	if len(mfs) != 0 {
		t.Errorf("expected 0 metric families, got %d", len(mfs))
	}
}

func TestRunStopsOnContextCancel(t *testing.T) {
	gpu := &stubGPUCollector{
		metrics: []models.GPUMetrics{
			{UUID: "GPU-001", UtilizationPercent: 42, PowerDrawWatts: 100},
		},
	}
	pods := &stubPodResources{
		mapping: map[string]PodInfo{
			"GPU-001": {Namespace: "ns", PodName: "pod"},
		},
	}

	mc, _ := newTestCollector(t, gpu, pods)
	mc.interval = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		mc.Run(ctx)
		close(done)
	}()

	// Let it run a few ticks.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// success
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop after context cancellation")
	}
}

func TestPrometheusMetricNames(t *testing.T) {
	gpu := &stubGPUCollector{
		metrics: []models.GPUMetrics{
			{
				UUID:               "GPU-test",
				UtilizationPercent: 1,
				MemoryUsedBytes:    2,
				MemoryTotalBytes:   3,
				PowerDrawWatts:     4,
				TemperatureCelsius: 5,
			},
		},
	}
	pods := &stubPodResources{mapping: map[string]PodInfo{}}

	mc, reg := newTestCollector(t, gpu, pods)
	mc.CollectOnce(context.Background())

	expected := []string{
		"ballast_gpu_utilization_percent",
		"ballast_gpu_memory_used_bytes",
		"ballast_gpu_memory_total_bytes",
		"ballast_gpu_power_watts",
		"ballast_gpu_temperature_celsius",
		// ballast_pod_cost_per_hour_usd not expected here — no pricing provider
	}

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	names := make(map[string]bool)
	for _, mf := range mfs {
		names[mf.GetName()] = true
	}

	for _, name := range expected {
		if !names[name] {
			t.Errorf("metric %s not found in gathered metrics", name)
		}
	}
}

func TestPrometheusMetricLabels(t *testing.T) {
	gpu := &stubGPUCollector{
		metrics: []models.GPUMetrics{
			{UUID: "GPU-lbl", UtilizationPercent: 1, PowerDrawWatts: 1},
		},
	}
	pods := &stubPodResources{
		mapping: map[string]PodInfo{
			"GPU-lbl": {Namespace: "test-ns", PodName: "test-pod"},
		},
	}

	mc, reg := newTestCollector(t, gpu, pods)
	mc.CollectOnce(context.Background())

	// Lint all gathered metrics via the registry.
	problems, err := testutil.GatherAndLint(reg)
	if err != nil {
		t.Fatalf("lint: %v", err)
	}
	for _, p := range problems {
		t.Errorf("lint problem: %s", p.Text)
	}

	// Verify the expected label values appear in the output.
	// The With() call itself validates that all 4 label names are correct.
	out := testutil.ToFloat64(mc.utilization.With(prometheus.Labels{
		"gpu_uuid": "GPU-lbl", "pod": "test-pod", "namespace": "test-ns", "node": "gpu-node-01",
	}))
	if out != 1 {
		t.Errorf("utilization gauge = %f, want 1", out)
	}
}

func TestDefaultInterval(t *testing.T) {
	reg := prometheus.NewRegistry()
	mc, err := NewMetricsCollector(MetricsCollectorOpts{
		GPUCollector: &stubGPUCollector{},
		PodResources: &stubPodResources{mapping: map[string]PodInfo{}},
		NodeName:     "node",
		Registry:     reg,
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if mc.interval != defaultInterval {
		t.Errorf("interval = %v, want %v", mc.interval, defaultInterval)
	}
}

func TestMetricHelpStrings(t *testing.T) {
	gpu := &stubGPUCollector{
		metrics: []models.GPUMetrics{
			{UUID: "GPU-help", UtilizationPercent: 1, PowerDrawWatts: 1},
		},
	}
	pods := &stubPodResources{mapping: map[string]PodInfo{}}

	mc, reg := newTestCollector(t, gpu, pods)
	mc.CollectOnce(context.Background())

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	for _, mf := range mfs {
		if mf.GetHelp() == "" {
			t.Errorf("metric %s has empty help string", mf.GetName())
		}
	}
}

func TestPodRemappingBetweenCycles(t *testing.T) {
	gpu := &stubGPUCollector{
		metrics: []models.GPUMetrics{
			{UUID: "GPU-001", UtilizationPercent: 80, PowerDrawWatts: 300},
		},
	}
	pods := &stubPodResources{
		mapping: map[string]PodInfo{
			"GPU-001": {Namespace: "ns-a", PodName: "pod-old"},
		},
	}

	mc, reg := newTestCollector(t, gpu, pods)

	// First cycle: GPU-001 owned by pod-old.
	mc.CollectOnce(context.Background())

	// Second cycle: GPU-001 remapped to pod-new.
	pods.mapping = map[string]PodInfo{
		"GPU-001": {Namespace: "ns-b", PodName: "pod-new"},
	}
	mc.CollectOnce(context.Background())

	// Should have new labels, not old.
	v := gaugeValue(t, reg, "ballast_gpu_utilization_percent", prometheus.Labels{
		"gpu_uuid": "GPU-001", "pod": "pod-new", "namespace": "ns-b", "node": "gpu-node-01",
	})
	if v != 80 {
		t.Errorf("utilization = %f, want 80", v)
	}

	// Old label combo should be gone.
	mfs, _ := reg.Gather()
	for _, mf := range mfs {
		if mf.GetName() != "ballast_gpu_utilization_percent" {
			continue
		}
		for _, m := range mf.GetMetric() {
			for _, lp := range m.GetLabel() {
				if lp.GetName() == "pod" && lp.GetValue() == "pod-old" {
					t.Error("stale label pod=pod-old still present after remapping")
				}
			}
		}
	}
}

func TestMetricOutput(t *testing.T) {
	gpu := &stubGPUCollector{
		metrics: []models.GPUMetrics{
			{UUID: "GPU-fmt", UtilizationPercent: 99, PowerDrawWatts: 1},
		},
	}
	pods := &stubPodResources{
		mapping: map[string]PodInfo{
			"GPU-fmt": {Namespace: "prod", PodName: "serve-xyz"},
		},
	}

	mc, _ := newTestCollector(t, gpu, pods)
	mc.CollectOnce(context.Background())

	expected := `
		# HELP ballast_gpu_utilization_percent Current GPU compute utilization (0-100).
		# TYPE ballast_gpu_utilization_percent gauge
		ballast_gpu_utilization_percent{gpu_uuid="GPU-fmt",namespace="prod",node="gpu-node-01",pod="serve-xyz"} 99
	`
	if err := testutil.CollectAndCompare(mc.utilization, strings.NewReader(expected)); err != nil {
		t.Errorf("metric output mismatch:\n%v", err)
	}
}

// --- cost metric tests ---

func TestCostPerHourBasic(t *testing.T) {
	gpu := &stubGPUCollector{
		metrics: []models.GPUMetrics{
			{
				UUID:               "GPU-aaa",
				DeviceName:         "NVIDIA H100 80GB HBM3",
				UtilizationPercent: 50,
			},
		},
	}
	pods := &stubPodResources{
		mapping: map[string]PodInfo{
			"GPU-aaa": {Namespace: "search", PodName: "llm-serve"},
		},
	}
	pricing := &stubPricing{
		prices: map[string]float64{"NVIDIA H100 80GB HBM3": 4.00},
	}

	mc, reg := newTestCollectorWithPricing(t, gpu, pods, pricing)
	mc.CollectOnce(context.Background())

	// 50% of $4.00/hr = $2.00/hr
	v := gaugeValue(t, reg, "ballast_pod_cost_per_hour_usd", prometheus.Labels{
		"pod": "llm-serve", "namespace": "search", "node": "gpu-node-01", "gpu_type": "NVIDIA H100 80GB HBM3",
	})
	if v != 2.0 {
		t.Errorf("cost = %f, want 2.0", v)
	}
}

func TestCostPerHourFullUtilization(t *testing.T) {
	gpu := &stubGPUCollector{
		metrics: []models.GPUMetrics{
			{UUID: "GPU-001", DeviceName: "NVIDIA A100", UtilizationPercent: 100},
		},
	}
	pods := &stubPodResources{
		mapping: map[string]PodInfo{
			"GPU-001": {Namespace: "ml", PodName: "train-job"},
		},
	}
	pricing := &stubPricing{prices: map[string]float64{"NVIDIA A100": 1.80}}

	mc, reg := newTestCollectorWithPricing(t, gpu, pods, pricing)
	mc.CollectOnce(context.Background())

	v := gaugeValue(t, reg, "ballast_pod_cost_per_hour_usd", prometheus.Labels{
		"pod": "train-job", "namespace": "ml", "node": "gpu-node-01", "gpu_type": "NVIDIA A100",
	})
	if v != 1.80 {
		t.Errorf("cost = %f, want 1.80", v)
	}
}

func TestCostPerHourZeroUtilization(t *testing.T) {
	gpu := &stubGPUCollector{
		metrics: []models.GPUMetrics{
			{UUID: "GPU-001", DeviceName: "NVIDIA L4", UtilizationPercent: 0},
		},
	}
	pods := &stubPodResources{
		mapping: map[string]PodInfo{
			"GPU-001": {Namespace: "idle", PodName: "idle-pod"},
		},
	}
	pricing := &stubPricing{prices: map[string]float64{"NVIDIA L4": 0.65}}

	mc, reg := newTestCollectorWithPricing(t, gpu, pods, pricing)
	mc.CollectOnce(context.Background())

	v := gaugeValue(t, reg, "ballast_pod_cost_per_hour_usd", prometheus.Labels{
		"pod": "idle-pod", "namespace": "idle", "node": "gpu-node-01", "gpu_type": "NVIDIA L4",
	})
	if v != 0 {
		t.Errorf("cost = %f, want 0", v)
	}
}

func TestCostAggregatesMultiGPUSamePod(t *testing.T) {
	// Pod with 4 A100s, each at 75% utilization.
	gpu := &stubGPUCollector{
		metrics: []models.GPUMetrics{
			{UUID: "GPU-001", DeviceName: "NVIDIA A100", UtilizationPercent: 75},
			{UUID: "GPU-002", DeviceName: "NVIDIA A100", UtilizationPercent: 75},
			{UUID: "GPU-003", DeviceName: "NVIDIA A100", UtilizationPercent: 75},
			{UUID: "GPU-004", DeviceName: "NVIDIA A100", UtilizationPercent: 75},
		},
	}
	pods := &stubPodResources{
		mapping: map[string]PodInfo{
			"GPU-001": {Namespace: "training", PodName: "big-job"},
			"GPU-002": {Namespace: "training", PodName: "big-job"},
			"GPU-003": {Namespace: "training", PodName: "big-job"},
			"GPU-004": {Namespace: "training", PodName: "big-job"},
		},
	}
	pricing := &stubPricing{prices: map[string]float64{"NVIDIA A100": 2.00}}

	mc, reg := newTestCollectorWithPricing(t, gpu, pods, pricing)
	mc.CollectOnce(context.Background())

	// 4 GPUs × 75% × $2.00 = $6.00/hr
	v := gaugeValue(t, reg, "ballast_pod_cost_per_hour_usd", prometheus.Labels{
		"pod": "big-job", "namespace": "training", "node": "gpu-node-01", "gpu_type": "NVIDIA A100",
	})
	if v != 6.0 {
		t.Errorf("cost = %f, want 6.0", v)
	}
}

func TestCostMultiplePodsMultipleTypes(t *testing.T) {
	gpu := &stubGPUCollector{
		metrics: []models.GPUMetrics{
			{UUID: "GPU-001", DeviceName: "NVIDIA H100", UtilizationPercent: 100},
			{UUID: "GPU-002", DeviceName: "NVIDIA L4", UtilizationPercent: 50},
		},
	}
	pods := &stubPodResources{
		mapping: map[string]PodInfo{
			"GPU-001": {Namespace: "search", PodName: "serve-a"},
			"GPU-002": {Namespace: "batch", PodName: "serve-b"},
		},
	}
	pricing := &stubPricing{prices: map[string]float64{
		"NVIDIA H100": 4.00,
		"NVIDIA L4":   0.60,
	}}

	mc, reg := newTestCollectorWithPricing(t, gpu, pods, pricing)
	mc.CollectOnce(context.Background())

	v1 := gaugeValue(t, reg, "ballast_pod_cost_per_hour_usd", prometheus.Labels{
		"pod": "serve-a", "namespace": "search", "node": "gpu-node-01", "gpu_type": "NVIDIA H100",
	})
	if v1 != 4.0 {
		t.Errorf("serve-a cost = %f, want 4.0", v1)
	}

	v2 := gaugeValue(t, reg, "ballast_pod_cost_per_hour_usd", prometheus.Labels{
		"pod": "serve-b", "namespace": "batch", "node": "gpu-node-01", "gpu_type": "NVIDIA L4",
	})
	if v2 != 0.30 {
		t.Errorf("serve-b cost = %f, want 0.30", v2)
	}
}

func TestCostFallbackPrice(t *testing.T) {
	gpu := &stubGPUCollector{
		metrics: []models.GPUMetrics{
			{UUID: "GPU-001", DeviceName: "UNKNOWN GPU", UtilizationPercent: 100},
		},
	}
	pods := &stubPodResources{
		mapping: map[string]PodInfo{
			"GPU-001": {Namespace: "ns", PodName: "pod"},
		},
	}
	pricing := &stubPricing{
		prices:       map[string]float64{},
		defaultPrice: 1.50,
	}

	mc, reg := newTestCollectorWithPricing(t, gpu, pods, pricing)
	mc.CollectOnce(context.Background())

	v := gaugeValue(t, reg, "ballast_pod_cost_per_hour_usd", prometheus.Labels{
		"pod": "pod", "namespace": "ns", "node": "gpu-node-01", "gpu_type": "UNKNOWN GPU",
	})
	if v != 1.50 {
		t.Errorf("cost = %f, want 1.50", v)
	}
}

func TestCostNotEmittedWithoutPricing(t *testing.T) {
	gpu := &stubGPUCollector{
		metrics: []models.GPUMetrics{
			{UUID: "GPU-001", DeviceName: "NVIDIA H100", UtilizationPercent: 50},
		},
	}
	pods := &stubPodResources{
		mapping: map[string]PodInfo{
			"GPU-001": {Namespace: "ns", PodName: "pod"},
		},
	}

	// No pricing provider.
	mc, reg := newTestCollector(t, gpu, pods)
	mc.CollectOnce(context.Background())

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() == "ballast_pod_cost_per_hour_usd" {
			t.Error("cost metric should not be emitted without a pricing provider")
		}
	}
}

func TestCostUnmappedGPU(t *testing.T) {
	gpu := &stubGPUCollector{
		metrics: []models.GPUMetrics{
			{UUID: "GPU-orphan", DeviceName: "NVIDIA H100", UtilizationPercent: 80},
		},
	}
	pods := &stubPodResources{mapping: map[string]PodInfo{}}
	pricing := &stubPricing{prices: map[string]float64{"NVIDIA H100": 4.00}}

	mc, reg := newTestCollectorWithPricing(t, gpu, pods, pricing)
	mc.CollectOnce(context.Background())

	// Unmapped GPU still gets a cost entry with empty pod/namespace.
	v := gaugeValue(t, reg, "ballast_pod_cost_per_hour_usd", prometheus.Labels{
		"pod": "", "namespace": "", "node": "gpu-node-01", "gpu_type": "NVIDIA H100",
	})
	// 80% × $4.00 = $3.20
	if v != 3.20 {
		t.Errorf("cost = %f, want 3.20", v)
	}
}

func TestCostResetsOnNewCycle(t *testing.T) {
	gpu := &stubGPUCollector{
		metrics: []models.GPUMetrics{
			{UUID: "GPU-001", DeviceName: "NVIDIA A100", UtilizationPercent: 50},
			{UUID: "GPU-002", DeviceName: "NVIDIA L4", UtilizationPercent: 100},
		},
	}
	pods := &stubPodResources{
		mapping: map[string]PodInfo{
			"GPU-001": {Namespace: "ns", PodName: "pod-a"},
			"GPU-002": {Namespace: "ns", PodName: "pod-b"},
		},
	}
	pricing := &stubPricing{prices: map[string]float64{
		"NVIDIA A100": 2.00,
		"NVIDIA L4":   0.60,
	}}

	mc, reg := newTestCollectorWithPricing(t, gpu, pods, pricing)
	mc.CollectOnce(context.Background())

	// Second cycle: GPU-002 disappears.
	gpu.metrics = []models.GPUMetrics{
		{UUID: "GPU-001", DeviceName: "NVIDIA A100", UtilizationPercent: 80},
	}
	mc.CollectOnce(context.Background())

	// pod-a cost updated: 80% × $2.00 = $1.60
	v := gaugeValue(t, reg, "ballast_pod_cost_per_hour_usd", prometheus.Labels{
		"pod": "pod-a", "namespace": "ns", "node": "gpu-node-01", "gpu_type": "NVIDIA A100",
	})
	if v != 1.60 {
		t.Errorf("cost = %f, want 1.60", v)
	}

	// pod-b cost should be gone.
	mfs, _ := reg.Gather()
	for _, mf := range mfs {
		if mf.GetName() != "ballast_pod_cost_per_hour_usd" {
			continue
		}
		for _, m := range mf.GetMetric() {
			for _, lp := range m.GetLabel() {
				if lp.GetName() == "pod" && lp.GetValue() == "pod-b" {
					t.Error("stale cost for pod-b still present")
				}
			}
		}
	}
}

func TestCostMetricOutput(t *testing.T) {
	gpu := &stubGPUCollector{
		metrics: []models.GPUMetrics{
			{UUID: "GPU-001", DeviceName: "NVIDIA H100", UtilizationPercent: 73},
		},
	}
	pods := &stubPodResources{
		mapping: map[string]PodInfo{
			"GPU-001": {Namespace: "search", PodName: "llm-serve"},
		},
	}
	pricing := &stubPricing{prices: map[string]float64{"NVIDIA H100": 3.90}}

	mc, _ := newTestCollectorWithPricing(t, gpu, pods, pricing)
	mc.CollectOnce(context.Background())

	// 73% × $3.90 = $2.847
	expected := `
		# HELP ballast_pod_cost_per_hour_usd Estimated cost per hour in USD based on GPU utilization and pricing.
		# TYPE ballast_pod_cost_per_hour_usd gauge
		ballast_pod_cost_per_hour_usd{gpu_type="NVIDIA H100",namespace="search",node="gpu-node-01",pod="llm-serve"} 2.847
	`
	if err := testutil.CollectAndCompare(mc.costPerHour, strings.NewReader(expected)); err != nil {
		t.Errorf("cost metric output mismatch:\n%v", err)
	}
}
