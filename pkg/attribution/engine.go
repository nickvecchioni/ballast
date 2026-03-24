package attribution

import (
	"context"
	"fmt"
	"time"
)

// PodCost represents attributed cost for a single pod.
type PodCost struct {
	Namespace string  `json:"namespace"`
	Pod       string  `json:"pod"`
	Node      string  `json:"node"`
	GPUType   string  `json:"gpu_type"`
	CostPerHr float64 `json:"cost_per_hour_usd"`
	AvgUtil   float64 `json:"avg_gpu_utilization_percent"`
	TotalCost float64 `json:"total_cost_usd"`
}

// NamespaceCost represents aggregated cost for a namespace.
type NamespaceCost struct {
	Namespace string  `json:"namespace"`
	CostPerHr float64 `json:"cost_per_hour_usd"`
	TotalCost float64 `json:"total_cost_usd"`
	AvgUtil   float64 `json:"avg_gpu_utilization_percent"`
	GPUCount  int     `json:"gpu_count"`
}

// ClusterSummary is an overview of cluster-wide GPU spend.
type ClusterSummary struct {
	TotalCostPerHr   float64         `json:"total_cost_per_hour_usd"`
	TotalCost        float64         `json:"total_cost_usd"`
	ProjectedMonthly float64         `json:"projected_monthly_usd"`
	AvgUtil          float64         `json:"avg_gpu_utilization_percent"`
	GPUCount         int             `json:"gpu_count"`
	Period           string          `json:"period"`
	Namespaces       []NamespaceCost `json:"namespaces"`
}

// Period defines a time range for queries.
type Period struct {
	Start time.Time
	End   time.Time
}

// ParsePeriod converts a human-readable period string to a time range.
func ParsePeriod(s string) Period {
	now := time.Now()
	switch s {
	case "1h":
		return Period{Start: now.Add(-1 * time.Hour), End: now}
	case "6h":
		return Period{Start: now.Add(-6 * time.Hour), End: now}
	case "24h", "1d":
		return Period{Start: now.Add(-24 * time.Hour), End: now}
	case "7d":
		return Period{Start: now.Add(-7 * 24 * time.Hour), End: now}
	case "30d":
		return Period{Start: now.Add(-30 * 24 * time.Hour), End: now}
	case "this-month":
		start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		return Period{Start: start, End: now}
	case "last-month":
		start := time.Date(now.Year(), now.Month()-1, 1, 0, 0, 0, 0, now.Location())
		end := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		return Period{Start: start, End: end}
	default:
		// Default to last hour.
		return Period{Start: now.Add(-1 * time.Hour), End: now}
	}
}

// Hours returns the duration of the period in hours.
func (p Period) Hours() float64 {
	return p.End.Sub(p.Start).Hours()
}

// Engine provides cost attribution queries backed by a Prometheus-compatible store.
type Engine struct {
	store Store
}

// NewEngine creates an attribution engine.
func NewEngine(store Store) *Engine {
	return &Engine{store: store}
}

// CostByPods returns per-pod cost for the given period.
// If namespace is empty, returns all namespaces.
func (e *Engine) CostByPods(ctx context.Context, namespace string, period Period) ([]PodCost, error) {
	nsFilter := ""
	if namespace != "" {
		nsFilter = fmt.Sprintf(`namespace="%s"`, namespace)
	}

	// Get average cost rate over the period.
	costQuery := fmt.Sprintf(
		`avg_over_time(infracost_pod_cost_per_hour_usd{%s}[%s])`,
		nsFilter, promDuration(period),
	)
	costSamples, err := e.store.Query(ctx, costQuery, period.End)
	if err != nil {
		return nil, fmt.Errorf("query pod costs: %w", err)
	}

	// Get average utilization over the period.
	utilQuery := fmt.Sprintf(
		`avg by (pod, namespace, node) (avg_over_time(infracost_gpu_utilization_percent{%s}[%s]))`,
		nsFilter, promDuration(period),
	)
	utilSamples, err := e.store.Query(ctx, utilQuery, period.End)
	if err != nil {
		return nil, fmt.Errorf("query pod utilization: %w", err)
	}

	// Index utilization by (namespace, pod).
	type nsPod struct{ ns, pod string }
	utilMap := make(map[nsPod]float64)
	for _, s := range utilSamples {
		key := nsPod{ns: s.Labels["namespace"], pod: s.Labels["pod"]}
		utilMap[key] = s.Value
	}

	hours := period.Hours()
	var pods []PodCost
	for _, s := range costSamples {
		key := nsPod{ns: s.Labels["namespace"], pod: s.Labels["pod"]}
		pods = append(pods, PodCost{
			Namespace: s.Labels["namespace"],
			Pod:       s.Labels["pod"],
			Node:      s.Labels["node"],
			GPUType:   s.Labels["gpu_type"],
			CostPerHr: s.Value,
			AvgUtil:   utilMap[key],
			TotalCost: s.Value * hours,
		})
	}
	return pods, nil
}

// CostByNamespaces returns aggregated cost per namespace.
func (e *Engine) CostByNamespaces(ctx context.Context, period Period) ([]NamespaceCost, error) {
	costQuery := fmt.Sprintf(
		`sum by (namespace) (avg_over_time(infracost_pod_cost_per_hour_usd[%s]))`,
		promDuration(period),
	)
	costSamples, err := e.store.Query(ctx, costQuery, period.End)
	if err != nil {
		return nil, fmt.Errorf("query namespace costs: %w", err)
	}

	utilQuery := fmt.Sprintf(
		`avg by (namespace) (avg_over_time(infracost_gpu_utilization_percent[%s]))`,
		promDuration(period),
	)
	utilSamples, err := e.store.Query(ctx, utilQuery, period.End)
	if err != nil {
		return nil, fmt.Errorf("query namespace utilization: %w", err)
	}

	countQuery := fmt.Sprintf(
		`count by (namespace) (avg_over_time(infracost_gpu_utilization_percent[%s]))`,
		promDuration(period),
	)
	countSamples, err := e.store.Query(ctx, countQuery, period.End)
	if err != nil {
		return nil, fmt.Errorf("query namespace gpu counts: %w", err)
	}

	utilMap := make(map[string]float64)
	for _, s := range utilSamples {
		utilMap[s.Labels["namespace"]] = s.Value
	}
	countMap := make(map[string]int)
	for _, s := range countSamples {
		countMap[s.Labels["namespace"]] = int(s.Value)
	}

	hours := period.Hours()
	var namespaces []NamespaceCost
	for _, s := range costSamples {
		ns := s.Labels["namespace"]
		namespaces = append(namespaces, NamespaceCost{
			Namespace: ns,
			CostPerHr: s.Value,
			TotalCost: s.Value * hours,
			AvgUtil:   utilMap[ns],
			GPUCount:  countMap[ns],
		})
	}
	return namespaces, nil
}

// Summary returns a cluster-wide cost summary.
func (e *Engine) Summary(ctx context.Context, period Period) (*ClusterSummary, error) {
	namespaces, err := e.CostByNamespaces(ctx, period)
	if err != nil {
		return nil, err
	}

	summary := &ClusterSummary{
		Period:     fmt.Sprintf("%s to %s", period.Start.Format(time.RFC3339), period.End.Format(time.RFC3339)),
		Namespaces: namespaces,
	}

	var totalUtil float64
	for _, ns := range namespaces {
		summary.TotalCostPerHr += ns.CostPerHr
		summary.TotalCost += ns.TotalCost
		summary.GPUCount += ns.GPUCount
		totalUtil += ns.AvgUtil * float64(ns.GPUCount)
	}

	if summary.GPUCount > 0 {
		summary.AvgUtil = totalUtil / float64(summary.GPUCount)
	}

	// Project monthly based on current hourly rate.
	summary.ProjectedMonthly = summary.TotalCostPerHr * 730 // avg hours/month

	return summary, nil
}

func promDuration(p Period) string {
	d := p.End.Sub(p.Start)
	if d < time.Minute {
		return "1m"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
