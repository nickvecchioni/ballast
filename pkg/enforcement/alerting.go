package enforcement

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Alert is a budget threshold notification.
type Alert struct {
	Namespace      string
	BudgetName     string
	ThresholdPct   int
	CurrentSpend   float64
	MonthlyBudget  float64
	PercentUsed    float64
	ProjectedSpend float64
}

// Notifier sends budget alerts to external systems.
type Notifier interface {
	Send(ctx context.Context, alert Alert) error
	Type() string
}

// SlackNotifier sends alerts to a Slack incoming webhook.
type SlackNotifier struct {
	WebhookURL string
	httpClient *http.Client
}

// NewSlackNotifier creates a Slack notifier.
func NewSlackNotifier(webhookURL string) *SlackNotifier {
	return &SlackNotifier{
		WebhookURL: webhookURL,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (s *SlackNotifier) Type() string { return "slack" }

func (s *SlackNotifier) Send(ctx context.Context, alert Alert) error {
	statusEmoji := ":white_check_mark:"
	if alert.PercentUsed >= 100 {
		statusEmoji = ":rotating_light:"
	} else if alert.PercentUsed >= 90 {
		statusEmoji = ":red_circle:"
	} else if alert.PercentUsed >= 75 {
		statusEmoji = ":warning:"
	}

	text := fmt.Sprintf(
		"%s *Ballast Budget Alert*\n"+
			"Namespace: `%s` | Budget: `%s`\n"+
			"Threshold: *%d%%* reached\n"+
			"Current Spend: *$%.2f* / $%.2f (%.1f%%)\n"+
			"Projected Monthly: *$%.2f*",
		statusEmoji,
		alert.Namespace, alert.BudgetName,
		alert.ThresholdPct,
		alert.CurrentSpend, alert.MonthlyBudget, alert.PercentUsed,
		alert.ProjectedSpend,
	)

	payload, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return fmt.Errorf("marshal slack payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.WebhookURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create slack request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send slack alert: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("slack returned status %d", resp.StatusCode)
	}
	return nil
}

// PagerDutyNotifier sends alerts via PagerDuty Events API v2.
type PagerDutyNotifier struct {
	RoutingKey string
	httpClient *http.Client
}

// NewPagerDutyNotifier creates a PagerDuty notifier.
func NewPagerDutyNotifier(routingKey string) *PagerDutyNotifier {
	return &PagerDutyNotifier{
		RoutingKey: routingKey,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (p *PagerDutyNotifier) Type() string { return "pagerduty" }

func (p *PagerDutyNotifier) Send(ctx context.Context, alert Alert) error {
	severity := "info"
	if alert.PercentUsed >= 100 {
		severity = "critical"
	} else if alert.PercentUsed >= 90 {
		severity = "error"
	} else if alert.PercentUsed >= 75 {
		severity = "warning"
	}

	event := map[string]any{
		"routing_key":  p.RoutingKey,
		"event_action": "trigger",
		"dedup_key":    fmt.Sprintf("ballast-%s-%s-%d", alert.Namespace, alert.BudgetName, alert.ThresholdPct),
		"payload": map[string]any{
			"summary":  fmt.Sprintf("Ballast: %s budget %d%% threshold reached ($%.2f/$%.2f)", alert.Namespace, alert.ThresholdPct, alert.CurrentSpend, alert.MonthlyBudget),
			"severity": severity,
			"source":   "ballast",
			"group":    alert.Namespace,
			"custom_details": map[string]any{
				"namespace":       alert.Namespace,
				"budget":          alert.BudgetName,
				"current_spend":   alert.CurrentSpend,
				"monthly_budget":  alert.MonthlyBudget,
				"percent_used":    alert.PercentUsed,
				"projected_spend": alert.ProjectedSpend,
			},
		},
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal pagerduty event: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://events.pagerduty.com/v2/enqueue", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create pagerduty request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send pagerduty alert: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("pagerduty returned status %d", resp.StatusCode)
	}
	return nil
}
