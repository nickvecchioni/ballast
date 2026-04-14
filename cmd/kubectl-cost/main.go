package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"

	"github.com/nickvecchioni/ballast/pkg/attribution"
)

const (
	defaultMetricsURL = "http://localhost:9400/metrics"
	defaultEngineURL  = "http://localhost:8080"

	costMetricName = "ballast_pod_cost_per_hour_usd"
	utilMetricName = "ballast_gpu_utilization_percent"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	var (
		metricsURL    = defaultMetricsURL
		engineURL     = defaultEngineURL
		namespace     = ""
		allNamespaces = false
		period        = ""
		format        = "table"
	)

	var positional []string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--metrics-url" && i+1 < len(args):
			i++
			metricsURL = args[i]
		case strings.HasPrefix(args[i], "--metrics-url="):
			metricsURL = strings.TrimPrefix(args[i], "--metrics-url=")
		case args[i] == "--engine-url" && i+1 < len(args):
			i++
			engineURL = args[i]
		case strings.HasPrefix(args[i], "--engine-url="):
			engineURL = strings.TrimPrefix(args[i], "--engine-url=")
		case args[i] == "-n" && i+1 < len(args):
			i++
			namespace = args[i]
		case strings.HasPrefix(args[i], "-n="):
			namespace = strings.TrimPrefix(args[i], "-n=")
		case args[i] == "--namespace" && i+1 < len(args):
			i++
			namespace = args[i]
		case strings.HasPrefix(args[i], "--namespace="):
			namespace = strings.TrimPrefix(args[i], "--namespace=")
		case args[i] == "--period" && i+1 < len(args):
			i++
			period = args[i]
		case strings.HasPrefix(args[i], "--period="):
			period = strings.TrimPrefix(args[i], "--period=")
		case args[i] == "--format" && i+1 < len(args):
			i++
			format = args[i]
		case strings.HasPrefix(args[i], "--format="):
			format = strings.TrimPrefix(args[i], "--format=")
		case args[i] == "--all-namespaces" || args[i] == "-A":
			allNamespaces = true
		case args[i] == "--help" || args[i] == "-h":
			printUsage(stdout)
			return nil
		case strings.HasPrefix(args[i], "-"):
			return fmt.Errorf("unknown flag: %s", args[i])
		default:
			positional = append(positional, args[i])
		}
	}

	if len(positional) == 0 {
		printUsage(stderr)
		return fmt.Errorf("expected subcommand: top, summary, or export")
	}

	switch positional[0] {
	case "top":
		if len(positional) < 2 || positional[1] != "pods" {
			return fmt.Errorf("usage: kubectl cost top pods")
		}
		return topPods(metricsURL, namespace, allNamespaces, stdout)

	case "summary":
		if period == "" {
			period = "this-month"
		}
		return summary(engineURL, namespace, period, stdout)

	case "export":
		if period == "" {
			period = "this-month"
		}
		return export(engineURL, namespace, period, format, stdout)

	default:
		printUsage(stderr)
		return fmt.Errorf("unknown subcommand: %s", positional[0])
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintf(w, `Usage: kubectl cost <command> [flags]

Commands:
  top pods      Real-time per-pod GPU cost and utilization
  summary       Cost summary for a time period
  export        Export cost data as CSV or JSON

Flags:
  -n, --namespace string    Filter by namespace
  -A, --all-namespaces      Show pods across all namespaces
      --period string       Time period: 1h, 6h, 24h, 7d, 30d, this-month, last-month
      --format string       Export format: csv, json (default "table")
      --metrics-url string  Collector Prometheus endpoint (default %q)
      --engine-url string   Attribution engine API (default %q)
  -h, --help                Show this help
`, defaultMetricsURL, defaultEngineURL)
}

// --- top pods (direct from collector /metrics) ---

type podRow struct {
	namespace string
	pod       string
	gpuType   string
	utilPct   float64
	costPerHr float64
}

func topPods(metricsURL, namespace string, allNamespaces bool, w io.Writer) error {
	families, err := fetchMetrics(metricsURL)
	if err != nil {
		return err
	}

	costFamily := families[costMetricName]
	utilFamily := families[utilMetricName]

	if costFamily == nil && utilFamily == nil {
		fmt.Fprintln(w, "No GPU cost data available.")
		return nil
	}

	type nsPod struct{ ns, pod string }
	utilSum := make(map[nsPod]float64)
	utilCount := make(map[nsPod]int)
	if utilFamily != nil {
		for _, m := range utilFamily.GetMetric() {
			labels := labelMap(m)
			key := nsPod{ns: labels["namespace"], pod: labels["pod"]}
			val := gaugeOrCounterValue(m)
			if !math.IsNaN(val) {
				utilSum[key] += val
				utilCount[key]++
			}
		}
	}

	var rows []podRow
	if costFamily != nil {
		for _, m := range costFamily.GetMetric() {
			labels := labelMap(m)
			ns := labels["namespace"]
			pod := labels["pod"]

			if !allNamespaces && namespace != "" && ns != namespace {
				continue
			}

			key := nsPod{ns: ns, pod: pod}
			avgUtil := 0.0
			if n := utilCount[key]; n > 0 {
				avgUtil = utilSum[key] / float64(n)
			}

			rows = append(rows, podRow{
				namespace: ns,
				pod:       pod,
				gpuType:   labels["gpu_type"],
				utilPct:   avgUtil,
				costPerHr: gaugeOrCounterValue(m),
			})
		}
	} else if utilFamily != nil {
		seen := make(map[nsPod]bool)
		for _, m := range utilFamily.GetMetric() {
			labels := labelMap(m)
			ns := labels["namespace"]
			pod := labels["pod"]

			if !allNamespaces && namespace != "" && ns != namespace {
				continue
			}

			key := nsPod{ns: ns, pod: pod}
			if seen[key] {
				continue
			}
			seen[key] = true

			avgUtil := 0.0
			if n := utilCount[key]; n > 0 {
				avgUtil = utilSum[key] / float64(n)
			}

			rows = append(rows, podRow{
				namespace: ns,
				pod:       pod,
				gpuType:   "-",
				utilPct:   avgUtil,
				costPerHr: math.NaN(),
			})
		}
	}

	if !allNamespaces && namespace == "" {
		var filtered []podRow
		for _, r := range rows {
			if r.namespace != "" {
				filtered = append(filtered, r)
			}
		}
		rows = filtered
	}

	if len(rows) == 0 {
		if namespace != "" {
			fmt.Fprintf(w, "No GPU pods found in namespace %q.\n", namespace)
		} else {
			fmt.Fprintln(w, "No GPU pods found.")
		}
		return nil
	}

	sort.Slice(rows, func(i, j int) bool {
		ci, cj := rows[i].costPerHr, rows[j].costPerHr
		if math.IsNaN(ci) {
			ci = -1
		}
		if math.IsNaN(cj) {
			cj = -1
		}
		return ci > cj
	})

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAMESPACE\tPOD\tGPU_TYPE\tGPU_UTIL\tCOST/HR")
	for _, r := range rows {
		costStr := "-"
		if !math.IsNaN(r.costPerHr) {
			costStr = fmt.Sprintf("$%.2f", r.costPerHr)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%.0f%%\t%s\n",
			r.namespace, r.pod, r.gpuType, r.utilPct, costStr)
	}
	return tw.Flush()
}

// --- summary (from engine API) ---

func summary(engineURL, namespace, period string, w io.Writer) error {
	if namespace != "" {
		return namespaceSummary(engineURL, namespace, period, w)
	}
	return clusterSummary(engineURL, period, w)
}

func clusterSummary(engineURL, period string, w io.Writer) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	url := fmt.Sprintf("%s/api/v1/cost/summary?period=%s", engineURL, period)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("query engine: %w", err)
	}
	defer resp.Body.Close()

	var s attribution.ClusterSummary
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}

	fmt.Fprintf(w, "Cluster GPU Cost Summary (period: %s)\n\n", period)
	fmt.Fprintf(w, "  Total Cost/hr:       $%.2f\n", s.TotalCostPerHr)
	fmt.Fprintf(w, "  Total Spend:         $%.2f\n", s.TotalCost)
	fmt.Fprintf(w, "  Projected Monthly:   $%.2f\n", s.ProjectedMonthly)
	fmt.Fprintf(w, "  Avg GPU Utilization: %.0f%%\n", s.AvgUtil)
	fmt.Fprintf(w, "  Active GPUs:         %d\n\n", s.GPUCount)

	if len(s.Namespaces) > 0 {
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "NAMESPACE\tCOST/HR\tTOTAL\tGPU_UTIL\tGPUS")
		for _, ns := range s.Namespaces {
			fmt.Fprintf(tw, "%s\t$%.2f\t$%.2f\t%.0f%%\t%d\n",
				ns.Namespace, ns.CostPerHr, ns.TotalCost, ns.AvgUtil, ns.GPUCount)
		}
		tw.Flush()
	}

	return nil
}

func namespaceSummary(engineURL, namespace, period string, w io.Writer) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	url := fmt.Sprintf("%s/api/v1/cost/pods?namespace=%s&period=%s", engineURL, namespace, period)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("query engine: %w", err)
	}
	defer resp.Body.Close()

	var pods []attribution.PodCost
	if err := json.NewDecoder(resp.Body).Decode(&pods); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}

	var totalCostPerHr, totalCost, totalUtil float64
	for _, p := range pods {
		totalCostPerHr += p.CostPerHr
		totalCost += p.TotalCost
		totalUtil += p.AvgUtil
	}
	avgUtil := 0.0
	if len(pods) > 0 {
		avgUtil = totalUtil / float64(len(pods))
	}

	fmt.Fprintf(w, "Namespace: %s | Period: %s\n", namespace, period)
	fmt.Fprintf(w, "  Cost/hr: $%.2f | Total: $%.2f | Avg Util: %.0f%%\n\n", totalCostPerHr, totalCost, avgUtil)

	if len(pods) > 0 {
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "POD\tGPU_TYPE\tGPU_UTIL\tCOST/HR\tTOTAL")
		for _, p := range pods {
			fmt.Fprintf(tw, "%s\t%s\t%.0f%%\t$%.2f\t$%.2f\n",
				p.Pod, p.GPUType, p.AvgUtil, p.CostPerHr, p.TotalCost)
		}
		tw.Flush()
	}

	return nil
}

// --- export (from engine API) ---

func export(engineURL, namespace, period, format string, w io.Writer) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	nsParam := ""
	if namespace != "" {
		nsParam = "&namespace=" + namespace
	}

	switch format {
	case "csv":
		url := fmt.Sprintf("%s/api/v1/export?period=%s%s", engineURL, period, nsParam)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return fmt.Errorf("query engine: %w", err)
		}
		defer resp.Body.Close()
		_, err = io.Copy(w, resp.Body)
		return err

	case "json":
		url := fmt.Sprintf("%s/api/v1/cost/pods?period=%s%s", engineURL, period, nsParam)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return fmt.Errorf("query engine: %w", err)
		}
		defer resp.Body.Close()
		_, err = io.Copy(w, resp.Body)
		return err

	default:
		return fmt.Errorf("unsupported format %q (use csv or json)", format)
	}
}

// --- helpers ---

func fetchMetrics(url string) (map[string]*dto.MetricFamily, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetch metrics from %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch metrics from %s: status %d", url, resp.StatusCode)
	}

	parser := expfmt.NewTextParser(model.UTF8Validation)
	families, err := parser.TextToMetricFamilies(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("parse metrics: %w", err)
	}
	return families, nil
}

func labelMap(m *dto.Metric) map[string]string {
	labels := make(map[string]string)
	for _, lp := range m.GetLabel() {
		labels[lp.GetName()] = lp.GetValue()
	}
	return labels
}

func gaugeOrCounterValue(m *dto.Metric) float64 {
	if g := m.GetGauge(); g != nil {
		return g.GetValue()
	}
	if c := m.GetCounter(); c != nil {
		return c.GetValue()
	}
	if u := m.GetUntyped(); u != nil {
		return u.GetValue()
	}
	return 0
}
