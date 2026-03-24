package enforcement

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1alpha1 "github.com/nickvecchioni/infracost/api/v1alpha1"
	"github.com/nickvecchioni/infracost/pkg/attribution"
)

// BudgetStore abstracts reading and updating InferenceBudget CRs so we
// can test the controller without a real K8s API server.
type BudgetStore interface {
	// ListBudgets returns all InferenceBudget CRs across all namespaces.
	ListBudgets(ctx context.Context) ([]v1alpha1.InferenceBudget, error)
	// UpdateBudgetStatus writes the status subresource of a budget.
	UpdateBudgetStatus(ctx context.Context, budget *v1alpha1.InferenceBudget) error
}

// BudgetController reconciles InferenceBudget CRs against actual spend.
type BudgetController struct {
	store     BudgetStore
	engine    *attribution.Engine
	notifiers []Notifier
	interval  time.Duration
	logger    *slog.Logger
}

// BudgetControllerOpts configures the controller.
type BudgetControllerOpts struct {
	Store     BudgetStore
	Engine    *attribution.Engine
	Notifiers []Notifier
	Interval  time.Duration // defaults to 60s
	Logger    *slog.Logger
}

// NewBudgetController creates a budget reconciler.
func NewBudgetController(opts BudgetControllerOpts) *BudgetController {
	if opts.Interval == 0 {
		opts.Interval = 60 * time.Second
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &BudgetController{
		store:     opts.Store,
		engine:    opts.Engine,
		notifiers: opts.Notifiers,
		interval:  opts.Interval,
		logger:    opts.Logger,
	}
}

// Run starts the reconciliation loop. Blocks until ctx is cancelled.
func (c *BudgetController) Run(ctx context.Context) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	c.reconcileAll(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.reconcileAll(ctx)
		}
	}
}

func (c *BudgetController) reconcileAll(ctx context.Context) {
	budgets, err := c.store.ListBudgets(ctx)
	if err != nil {
		c.logger.Error("failed to list budgets", "err", err)
		return
	}

	for i := range budgets {
		if err := c.reconcile(ctx, &budgets[i]); err != nil {
			c.logger.Error("reconcile failed",
				"budget", budgets[i].Name,
				"namespace", budgets[i].Namespace,
				"err", err,
			)
		}
	}
}

// Reconcile processes a single InferenceBudget CR.
func (c *BudgetController) reconcile(ctx context.Context, budget *v1alpha1.InferenceBudget) error {
	ns := budget.Namespace

	// Query month-to-date spend.
	monthPeriod := attribution.ParsePeriod("this-month")
	nsCosts, err := c.engine.CostByNamespaces(ctx, monthPeriod)
	if err != nil {
		return fmt.Errorf("query namespace costs: %w", err)
	}

	var monthlySpend float64
	for _, nc := range nsCosts {
		if nc.Namespace == ns {
			monthlySpend = nc.TotalCost
			break
		}
	}

	// Query today's spend.
	dayPeriod := attribution.ParsePeriod("24h")
	dayCosts, err := c.engine.CostByNamespaces(ctx, dayPeriod)
	if err != nil {
		return fmt.Errorf("query daily costs: %w", err)
	}

	var dailySpend float64
	for _, nc := range dayCosts {
		if nc.Namespace == ns {
			dailySpend = nc.TotalCost
			break
		}
	}

	// Compute budget percentage and status.
	percentUsed := 0.0
	if budget.Spec.Limits.MonthlyUSD > 0 {
		percentUsed = (monthlySpend / budget.Spec.Limits.MonthlyUSD) * 100
	}

	// Project monthly spend based on current hourly rate.
	summary, err := c.engine.Summary(ctx, monthPeriod)
	projectedMonthly := 0.0
	if err == nil {
		for _, nc := range summary.Namespaces {
			if nc.Namespace == ns {
				projectedMonthly = nc.CostPerHr * 730
				break
			}
		}
	}

	budgetStatus := computeStatus(percentUsed)

	// Update status.
	budget.Status = v1alpha1.InferenceBudgetStatus{
		CurrentMonthSpendUSD: monthlySpend,
		DailySpendUSD:        dailySpend,
		ProjectedMonthlyUSD:  projectedMonthly,
		BudgetPercentUsed:    percentUsed,
		BudgetStatus:         budgetStatus,
		LastAlertedThreshold: budget.Status.LastAlertedThreshold,
		LastUpdated:          metav1.Now(),
	}

	// Check thresholds and fire alerts.
	c.checkThresholds(ctx, budget, percentUsed, monthlySpend, projectedMonthly)

	// Persist status update.
	if err := c.store.UpdateBudgetStatus(ctx, budget); err != nil {
		return fmt.Errorf("update budget status: %w", err)
	}

	c.logger.Info("budget reconciled",
		"budget", budget.Name,
		"namespace", ns,
		"spend", fmt.Sprintf("$%.2f", monthlySpend),
		"percent", fmt.Sprintf("%.1f%%", percentUsed),
		"status", budgetStatus,
	)

	return nil
}

func (c *BudgetController) checkThresholds(ctx context.Context, budget *v1alpha1.InferenceBudget, percentUsed, currentSpend, projected float64) {
	thresholds := budget.Spec.Alerts.Thresholds
	if len(thresholds) == 0 {
		return
	}

	// Sort thresholds ascending.
	sort.Ints(thresholds)

	for _, threshold := range thresholds {
		if percentUsed < float64(threshold) {
			break
		}
		if threshold <= budget.Status.LastAlertedThreshold {
			continue // already alerted for this threshold
		}

		alert := Alert{
			Namespace:      budget.Namespace,
			BudgetName:     budget.Name,
			ThresholdPct:   threshold,
			CurrentSpend:   currentSpend,
			MonthlyBudget:  budget.Spec.Limits.MonthlyUSD,
			PercentUsed:    percentUsed,
			ProjectedSpend: projected,
		}

		for _, notifier := range c.notifiers {
			if err := notifier.Send(ctx, alert); err != nil {
				c.logger.Error("alert send failed",
					"type", notifier.Type(),
					"threshold", threshold,
					"err", err,
				)
			} else {
				c.logger.Info("alert sent",
					"type", notifier.Type(),
					"namespace", budget.Namespace,
					"threshold", threshold,
				)
			}
		}

		budget.Status.LastAlertedThreshold = threshold
	}
}

func computeStatus(percentUsed float64) string {
	switch {
	case percentUsed >= 100:
		return "exceeded"
	case percentUsed >= 90:
		return "critical"
	case percentUsed >= 75:
		return "warning"
	default:
		return "ok"
	}
}
