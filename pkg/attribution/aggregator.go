package attribution

import (
	"encoding/csv"
	"fmt"
	"io"
	"sort"
)

// SortPodsBycost sorts pods by total cost descending.
func SortPodsBycost(pods []PodCost) {
	sort.Slice(pods, func(i, j int) bool {
		return pods[i].TotalCost > pods[j].TotalCost
	})
}

// SortNamespacesByCost sorts namespaces by total cost descending.
func SortNamespacesByCost(ns []NamespaceCost) {
	sort.Slice(ns, func(i, j int) bool {
		return ns[i].TotalCost > ns[j].TotalCost
	})
}

// ExportCSV writes pod cost data as CSV for finance/chargeback.
func ExportCSV(w io.Writer, pods []PodCost, period Period) error {
	cw := csv.NewWriter(w)
	defer cw.Flush()

	if err := cw.Write([]string{
		"namespace", "pod", "node", "gpu_type",
		"avg_gpu_utilization_percent", "cost_per_hour_usd", "total_cost_usd",
		"period_start", "period_end", "period_hours",
	}); err != nil {
		return fmt.Errorf("write csv header: %w", err)
	}

	for _, p := range pods {
		if err := cw.Write([]string{
			p.Namespace,
			p.Pod,
			p.Node,
			p.GPUType,
			fmt.Sprintf("%.1f", p.AvgUtil),
			fmt.Sprintf("%.4f", p.CostPerHr),
			fmt.Sprintf("%.4f", p.TotalCost),
			period.Start.Format("2006-01-02T15:04:05Z"),
			period.End.Format("2006-01-02T15:04:05Z"),
			fmt.Sprintf("%.1f", period.Hours()),
		}); err != nil {
			return fmt.Errorf("write csv row: %w", err)
		}
	}
	return nil
}
