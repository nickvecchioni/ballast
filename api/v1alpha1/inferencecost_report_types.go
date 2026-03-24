package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// InferenceCostReportSpec defines the report parameters.
type InferenceCostReportSpec struct {
	// Period is "daily", "weekly", or "monthly".
	Period string `json:"period"`
	// Start of the reporting window.
	Start metav1.Time `json:"start"`
	// End of the reporting window.
	End metav1.Time `json:"end"`
}

// InferenceCostReportStatus holds the generated report data.
type InferenceCostReportStatus struct {
	// TotalCostUSD is the total GPU spend in the reporting window.
	TotalCostUSD float64 `json:"total_cost_usd"`
	// Breakdown contains per-model and per-deployment cost details.
	Breakdown CostBreakdown `json:"breakdown,omitempty"`
	// Efficiency has utilization and waste metrics.
	Efficiency EfficiencyReport `json:"efficiency,omitempty"`
}

// CostBreakdown splits cost by model and deployment.
type CostBreakdown struct {
	ByModel      []ModelCost      `json:"by_model,omitempty"`
	ByDeployment []DeploymentCost `json:"by_deployment,omitempty"`
}

// ModelCost is cost attributed to a specific model.
type ModelCost struct {
	Model           string  `json:"model"`
	CostUSD         float64 `json:"cost_usd"`
	AvgUtilization  float64 `json:"avg_gpu_utilization"`
}

// DeploymentCost is cost attributed to a deployment.
type DeploymentCost struct {
	Deployment string  `json:"deployment"`
	CostUSD    float64 `json:"cost_usd"`
}

// EfficiencyReport contains utilization and waste data.
type EfficiencyReport struct {
	AvgGPUUtilization float64  `json:"avg_gpu_utilization"`
	EstimatedWasteUSD float64  `json:"estimated_waste_usd"`
	Recommendations   []string `json:"recommendations,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// InferenceCostReport is a controller-generated cost report for a namespace.
type InferenceCostReport struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   InferenceCostReportSpec   `json:"spec,omitempty"`
	Status InferenceCostReportStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// InferenceCostReportList contains a list of InferenceCostReports.
type InferenceCostReportList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []InferenceCostReport `json:"items"`
}

func (in *InferenceCostReport) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

func (in *InferenceCostReport) DeepCopy() *InferenceCostReport {
	if in == nil {
		return nil
	}
	out := new(InferenceCostReport)
	*out = *in
	out.Status = *in.Status.DeepCopy()
	return out
}

func (in *InferenceCostReportStatus) DeepCopy() *InferenceCostReportStatus {
	if in == nil {
		return nil
	}
	out := new(InferenceCostReportStatus)
	*out = *in
	if in.Breakdown.ByModel != nil {
		out.Breakdown.ByModel = make([]ModelCost, len(in.Breakdown.ByModel))
		copy(out.Breakdown.ByModel, in.Breakdown.ByModel)
	}
	if in.Breakdown.ByDeployment != nil {
		out.Breakdown.ByDeployment = make([]DeploymentCost, len(in.Breakdown.ByDeployment))
		copy(out.Breakdown.ByDeployment, in.Breakdown.ByDeployment)
	}
	if in.Efficiency.Recommendations != nil {
		out.Efficiency.Recommendations = make([]string, len(in.Efficiency.Recommendations))
		copy(out.Efficiency.Recommendations, in.Efficiency.Recommendations)
	}
	return out
}

func (in *InferenceCostReportList) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	out := new(InferenceCostReportList)
	*out = *in
	if in.Items != nil {
		out.Items = make([]InferenceCostReport, len(in.Items))
		for i := range in.Items {
			out.Items[i] = *in.Items[i].DeepCopy()
		}
	}
	return out
}
