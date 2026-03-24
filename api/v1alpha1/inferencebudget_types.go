package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// InferenceBudgetSpec defines the desired budget for a namespace.
type InferenceBudgetSpec struct {
	// Limits defines spending caps.
	Limits BudgetLimits `json:"limits"`

	// Alerts configures threshold-based notifications.
	Alerts AlertConfig `json:"alerts,omitempty"`

	// Enforcement controls what happens when budgets are exceeded.
	Enforcement EnforcementConfig `json:"enforcement,omitempty"`
}

// BudgetLimits defines spending caps.
type BudgetLimits struct {
	// MonthlyUSD is the maximum monthly spend in USD.
	MonthlyUSD float64 `json:"monthly_usd"`
	// DailyUSD is an optional daily spending cap.
	DailyUSD float64 `json:"daily_usd,omitempty"`
}

// AlertConfig defines when and how to send alerts.
type AlertConfig struct {
	// Thresholds is a list of percentages of the monthly limit
	// at which to trigger alerts (e.g. [50, 75, 90, 100]).
	Thresholds []int `json:"thresholds,omitempty"`
	// Channels defines where to send alerts.
	Channels []AlertChannel `json:"channels,omitempty"`
}

// AlertChannel is a notification destination.
type AlertChannel struct {
	// Type is the channel type: "slack" or "pagerduty".
	Type string `json:"type"`
	// Webhook is the Slack incoming webhook URL.
	Webhook string `json:"webhook,omitempty"`
	// RoutingKey is the PagerDuty Events API v2 routing key.
	RoutingKey string `json:"routing_key,omitempty"`
}

// EnforcementConfig controls budget enforcement behaviour.
type EnforcementConfig struct {
	// Mode is one of: "hard", "soft", "monitor".
	// hard: reject new GPU pods when over budget.
	// soft: alert but allow.
	// monitor: collect data only.
	Mode string `json:"mode,omitempty"`
}

// InferenceBudgetStatus reflects the current budget state.
type InferenceBudgetStatus struct {
	// CurrentMonthSpendUSD is the total spend this month.
	CurrentMonthSpendUSD float64 `json:"current_month_spend_usd"`
	// DailySpendUSD is today's spend so far.
	DailySpendUSD float64 `json:"daily_spend_usd"`
	// ProjectedMonthlyUSD is the projected end-of-month spend.
	ProjectedMonthlyUSD float64 `json:"projected_monthly_usd"`
	// BudgetPercentUsed is the percentage of the monthly budget consumed.
	BudgetPercentUsed float64 `json:"budget_percent_used"`
	// BudgetStatus is one of: "ok", "warning", "critical", "exceeded".
	BudgetStatus string `json:"budget_status"`
	// LastAlertedThreshold tracks the highest threshold that has been alerted
	// to avoid re-alerting on the same threshold.
	LastAlertedThreshold int `json:"last_alerted_threshold,omitempty"`
	// LastUpdated is the timestamp of the most recent reconciliation.
	LastUpdated metav1.Time `json:"last_updated"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Monthly Budget",type=number,JSONPath=`.spec.limits.monthly_usd`
// +kubebuilder:printcolumn:name="Spent",type=number,JSONPath=`.status.current_month_spend_usd`
// +kubebuilder:printcolumn:name="% Used",type=number,JSONPath=`.status.budget_percent_used`
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.budget_status`

// InferenceBudget defines a GPU inference spending budget for a namespace.
type InferenceBudget struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   InferenceBudgetSpec   `json:"spec,omitempty"`
	Status InferenceBudgetStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// InferenceBudgetList contains a list of InferenceBudgets.
type InferenceBudgetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []InferenceBudget `json:"items"`
}

func (in *InferenceBudget) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

func (in *InferenceBudget) DeepCopy() *InferenceBudget {
	if in == nil {
		return nil
	}
	out := new(InferenceBudget)
	*out = *in
	out.Spec = *in.Spec.DeepCopy()
	out.Status = in.Status
	return out
}

func (in *InferenceBudgetSpec) DeepCopy() *InferenceBudgetSpec {
	if in == nil {
		return nil
	}
	out := new(InferenceBudgetSpec)
	*out = *in
	if in.Alerts.Thresholds != nil {
		out.Alerts.Thresholds = make([]int, len(in.Alerts.Thresholds))
		copy(out.Alerts.Thresholds, in.Alerts.Thresholds)
	}
	if in.Alerts.Channels != nil {
		out.Alerts.Channels = make([]AlertChannel, len(in.Alerts.Channels))
		copy(out.Alerts.Channels, in.Alerts.Channels)
	}
	return out
}

func (in *InferenceBudgetList) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	out := new(InferenceBudgetList)
	*out = *in
	if in.Items != nil {
		out.Items = make([]InferenceBudget, len(in.Items))
		for i := range in.Items {
			out.Items[i] = *in.Items[i].DeepCopy()
		}
	}
	return out
}
