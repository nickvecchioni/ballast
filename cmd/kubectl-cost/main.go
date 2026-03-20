package main

import (
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
)

const (
	defaultMetricsURL = "http://localhost:9400/metrics"

	costMetricName = "infracost_pod_cost_per_hour_usd"
	utilMetricName = "infracost_gpu_utilization_percent"
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
		namespace     = ""
		allNamespaces = false
	)

	// Simple flag parsing to avoid flag package's os.Exit behaviour.
	var positional []string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--metrics-url" && i+1 < len(args):
			i++
			metricsURL = args[i]
		case strings.HasPrefix(args[i], "--metrics-url="):
			metricsURL = strings.TrimPrefix(args[i], "--metrics-url=")
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

	if len(positional) < 2 || positional[0] != "top" || positional[1] != "pods" {
		printUsage(stderr)
		return fmt.Errorf("expected subcommand: top pods")
	}

	return topPods(metricsURL, namespace, allNamespaces, stdout)
}

func printUsage(w io.Writer) {
	fmt.Fprintf(w, `Usage: kubectl cost top pods [flags]

Show per-pod GPU cost and utilization.

Flags:
  -n, --namespace string    Filter by namespace
  -A, --all-namespaces      Show pods across all namespaces
      --metrics-url string  Prometheus metrics endpoint (default %q)
  -h, --help                Show this help
`, defaultMetricsURL)
}

// podRow is one row in the output table.
type podRow struct {
	namespace string
	pod       string
	gpuType   string
	utilPct   float64 // average across GPUs
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

	// Build utilization index: (namespace, pod) → average utilization.
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

	// Build rows from cost metric.
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
		// No cost metric but we have utilization — show what we can.
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
		// Default: only show pods that have a namespace set.
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

	// Sort: highest cost first.
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
