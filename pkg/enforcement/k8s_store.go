package enforcement

import (
	"context"
	"encoding/json"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"

	v1alpha1 "github.com/nickvecchioni/ballast/api/v1alpha1"
)

var budgetGVR = schema.GroupVersionResource{
	Group:    "ballast.io",
	Version:  "v1alpha1",
	Resource: "inferencebudgets",
}

// K8sBudgetStore implements BudgetStore using the K8s dynamic client.
// This avoids needing generated typed clients — the dynamic client works
// with any CRD as long as we handle the JSON conversion ourselves.
type K8sBudgetStore struct {
	client dynamic.Interface
}

// NewK8sBudgetStore creates a budget store backed by the K8s API.
func NewK8sBudgetStore(client dynamic.Interface) *K8sBudgetStore {
	return &K8sBudgetStore{client: client}
}

func (s *K8sBudgetStore) ListBudgets(ctx context.Context) ([]v1alpha1.InferenceBudget, error) {
	list, err := s.client.Resource(budgetGVR).Namespace("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list inference budgets: %w", err)
	}

	var budgets []v1alpha1.InferenceBudget
	for _, item := range list.Items {
		budget, err := unstructuredToBudget(&item)
		if err != nil {
			continue
		}
		budgets = append(budgets, *budget)
	}
	return budgets, nil
}

func (s *K8sBudgetStore) UpdateBudgetStatus(ctx context.Context, budget *v1alpha1.InferenceBudget) error {
	statusPatch, err := json.Marshal(map[string]any{
		"status": budget.Status,
	})
	if err != nil {
		return fmt.Errorf("marshal status patch: %w", err)
	}

	_, err = s.client.Resource(budgetGVR).
		Namespace(budget.Namespace).
		Patch(ctx, budget.Name, types.MergePatchType, statusPatch, metav1.PatchOptions{}, "status")
	if err != nil {
		return fmt.Errorf("patch budget status %s/%s: %w", budget.Namespace, budget.Name, err)
	}
	return nil
}

func unstructuredToBudget(obj *unstructured.Unstructured) (*v1alpha1.InferenceBudget, error) {
	data, err := obj.MarshalJSON()
	if err != nil {
		return nil, err
	}
	var budget v1alpha1.InferenceBudget
	if err := json.Unmarshal(data, &budget); err != nil {
		return nil, err
	}
	return &budget, nil
}
