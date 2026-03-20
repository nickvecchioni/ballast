package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const sampleMetrics = `# HELP infracost_gpu_utilization_percent Current GPU compute utilization (0-100).
# TYPE infracost_gpu_utilization_percent gauge
infracost_gpu_utilization_percent{gpu_uuid="GPU-aaa",namespace="search",node="gpu-node-01",pod="llm-serve-abc"} 73
infracost_gpu_utilization_percent{gpu_uuid="GPU-bbb",namespace="search",node="gpu-node-01",pod="llm-serve-abc"} 67
infracost_gpu_utilization_percent{gpu_uuid="GPU-ccc",namespace="recommend",node="gpu-node-01",pod="rec-serve-xyz"} 42
infracost_gpu_utilization_percent{gpu_uuid="GPU-ddd",namespace="batch",node="gpu-node-02",pod="training-job"} 95
# HELP infracost_pod_cost_per_hour_usd Estimated cost per hour.
# TYPE infracost_pod_cost_per_hour_usd gauge
infracost_pod_cost_per_hour_usd{gpu_type="NVIDIA H100",namespace="search",node="gpu-node-01",pod="llm-serve-abc"} 5.46
infracost_pod_cost_per_hour_usd{gpu_type="NVIDIA A100",namespace="recommend",node="gpu-node-01",pod="rec-serve-xyz"} 0.76
infracost_pod_cost_per_hour_usd{gpu_type="NVIDIA H100",namespace="batch",node="gpu-node-02",pod="training-job"} 3.71
`

func metricsServer(body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(body))
	}))
}

func runCLI(t *testing.T, args ...string) (stdout, stderr string) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	err := run(args, &outBuf, &errBuf)
	if err != nil {
		// Append error to stderr for inspection.
		errBuf.WriteString(err.Error())
	}
	return outBuf.String(), errBuf.String()
}

func TestTopPodsAllNamespaces(t *testing.T) {
	srv := metricsServer(sampleMetrics)
	defer srv.Close()

	out, errOut := runCLI(t, "top", "pods", "--all-namespaces", "--metrics-url", srv.URL)
	if errOut != "" {
		t.Fatalf("unexpected error: %s", errOut)
	}

	// Should contain the header.
	if !strings.Contains(out, "NAMESPACE") || !strings.Contains(out, "COST/HR") {
		t.Errorf("missing table header:\n%s", out)
	}

	// All three pods should be present.
	for _, pod := range []string{"llm-serve-abc", "rec-serve-xyz", "training-job"} {
		if !strings.Contains(out, pod) {
			t.Errorf("missing pod %q in output:\n%s", pod, out)
		}
	}

	// Costs should appear.
	if !strings.Contains(out, "$5.46") {
		t.Errorf("missing cost $5.46 in output:\n%s", out)
	}
	if !strings.Contains(out, "$0.76") {
		t.Errorf("missing cost $0.76 in output:\n%s", out)
	}
}

func TestTopPodsFilterNamespace(t *testing.T) {
	srv := metricsServer(sampleMetrics)
	defer srv.Close()

	out, errOut := runCLI(t, "top", "pods", "-n", "search", "--metrics-url", srv.URL)
	if errOut != "" {
		t.Fatalf("unexpected error: %s", errOut)
	}

	if !strings.Contains(out, "llm-serve-abc") {
		t.Errorf("missing llm-serve-abc:\n%s", out)
	}
	if strings.Contains(out, "rec-serve-xyz") {
		t.Errorf("rec-serve-xyz should be filtered out:\n%s", out)
	}
	if strings.Contains(out, "training-job") {
		t.Errorf("training-job should be filtered out:\n%s", out)
	}
}

func TestTopPodsUtilizationAverage(t *testing.T) {
	srv := metricsServer(sampleMetrics)
	defer srv.Close()

	out, _ := runCLI(t, "top", "pods", "-n", "search", "--metrics-url", srv.URL)

	// llm-serve-abc has GPUs at 73% and 67%, average = 70%.
	if !strings.Contains(out, "70%") {
		t.Errorf("expected 70%% avg utilization for llm-serve-abc:\n%s", out)
	}
}

func TestTopPodsSortedByCost(t *testing.T) {
	srv := metricsServer(sampleMetrics)
	defer srv.Close()

	out, _ := runCLI(t, "top", "pods", "--all-namespaces", "--metrics-url", srv.URL)

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 4 {
		t.Fatalf("expected at least 4 lines (header + 3 pods), got %d:\n%s", len(lines), out)
	}

	// First data row should be highest cost ($5.46 llm-serve-abc).
	if !strings.Contains(lines[1], "llm-serve-abc") {
		t.Errorf("first row should be llm-serve-abc (highest cost):\n%s", out)
	}
	// Last data row should be lowest cost ($0.76 rec-serve-xyz).
	if !strings.Contains(lines[3], "rec-serve-xyz") {
		t.Errorf("last row should be rec-serve-xyz (lowest cost):\n%s", out)
	}
}

func TestTopPodsEmptyMetrics(t *testing.T) {
	srv := metricsServer("")
	defer srv.Close()

	out, _ := runCLI(t, "top", "pods", "--all-namespaces", "--metrics-url", srv.URL)

	if !strings.Contains(out, "No GPU cost data") {
		t.Errorf("expected 'No GPU cost data' message:\n%s", out)
	}
}

func TestTopPodsNamespaceNotFound(t *testing.T) {
	srv := metricsServer(sampleMetrics)
	defer srv.Close()

	out, _ := runCLI(t, "top", "pods", "-n", "nonexistent", "--metrics-url", srv.URL)

	if !strings.Contains(out, "No GPU pods found") {
		t.Errorf("expected 'No GPU pods found' message:\n%s", out)
	}
}

func TestTopPodsConnectionError(t *testing.T) {
	_, errOut := runCLI(t, "top", "pods", "--all-namespaces", "--metrics-url", "http://127.0.0.1:1/metrics")

	if errOut == "" {
		t.Fatal("expected error for connection refused")
	}
}

func TestTopPodsHTTP500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, errOut := runCLI(t, "top", "pods", "--all-namespaces", "--metrics-url", srv.URL)

	if !strings.Contains(errOut, "500") {
		t.Errorf("expected 500 in error: %s", errOut)
	}
}

func TestNoSubcommand(t *testing.T) {
	_, errOut := runCLI(t, )

	if !strings.Contains(errOut, "expected subcommand") {
		t.Errorf("expected usage error: %s", errOut)
	}
}

func TestUnknownFlag(t *testing.T) {
	_, errOut := runCLI(t, "top", "pods", "--bogus")

	if !strings.Contains(errOut, "unknown flag") {
		t.Errorf("expected unknown flag error: %s", errOut)
	}
}

func TestHelpFlag(t *testing.T) {
	out, _ := runCLI(t, "--help")

	if !strings.Contains(out, "Usage:") {
		t.Errorf("expected usage output:\n%s", out)
	}
}

func TestTopPodsOnlyUtilization(t *testing.T) {
	// Only utilization metric, no cost.
	utilOnly := `# TYPE infracost_gpu_utilization_percent gauge
infracost_gpu_utilization_percent{gpu_uuid="GPU-aaa",namespace="search",node="n1",pod="llm-serve"} 80
`
	srv := metricsServer(utilOnly)
	defer srv.Close()

	out, errOut := runCLI(t, "top", "pods", "--all-namespaces", "--metrics-url", srv.URL)
	if errOut != "" {
		t.Fatalf("unexpected error: %s", errOut)
	}

	if !strings.Contains(out, "llm-serve") {
		t.Errorf("missing llm-serve:\n%s", out)
	}
	if !strings.Contains(out, "80%") {
		t.Errorf("missing 80%% utilization:\n%s", out)
	}
	// Cost should show as "-".
	if !strings.Contains(out, "-") {
		t.Errorf("expected '-' for missing cost:\n%s", out)
	}
}

func TestLongNamespaceFlag(t *testing.T) {
	srv := metricsServer(sampleMetrics)
	defer srv.Close()

	out, errOut := runCLI(t, "top", "pods", "--namespace", "batch", "--metrics-url", srv.URL)
	if errOut != "" {
		t.Fatalf("unexpected error: %s", errOut)
	}

	if !strings.Contains(out, "training-job") {
		t.Errorf("missing training-job:\n%s", out)
	}
	if strings.Contains(out, "llm-serve-abc") {
		t.Errorf("llm-serve-abc should not appear:\n%s", out)
	}
}

func TestGPUTypeInOutput(t *testing.T) {
	srv := metricsServer(sampleMetrics)
	defer srv.Close()

	out, _ := runCLI(t, "top", "pods", "--all-namespaces", "--metrics-url", srv.URL)

	if !strings.Contains(out, "NVIDIA H100") {
		t.Errorf("missing GPU type NVIDIA H100:\n%s", out)
	}
	if !strings.Contains(out, "NVIDIA A100") {
		t.Errorf("missing GPU type NVIDIA A100:\n%s", out)
	}
}

func TestMetricsURLEqualsForm(t *testing.T) {
	srv := metricsServer(sampleMetrics)
	defer srv.Close()

	out, errOut := runCLI(t, "top", "pods", "-A", "--metrics-url="+srv.URL)
	if errOut != "" {
		t.Fatalf("unexpected error: %s", errOut)
	}

	if !strings.Contains(out, "llm-serve-abc") {
		t.Errorf("missing pod:\n%s", out)
	}
}
