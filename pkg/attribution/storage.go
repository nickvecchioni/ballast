package attribution

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// MetricSample is a single metric value with its label set.
type MetricSample struct {
	Labels map[string]string
	Value  float64
	Time   time.Time
}

// MetricSeries is a time series with multiple data points.
type MetricSeries struct {
	Labels map[string]string
	Values []TimeValue
}

// TimeValue is a single (timestamp, value) pair.
type TimeValue struct {
	Time  time.Time
	Value float64
}

// Store queries a Prometheus-compatible TSDB (VictoriaMetrics or Prometheus).
type Store interface {
	// Query executes an instant PromQL query and returns the result vector.
	Query(ctx context.Context, promql string, ts time.Time) ([]MetricSample, error)
	// QueryRange executes a range PromQL query and returns the result matrix.
	QueryRange(ctx context.Context, promql string, start, end time.Time, step time.Duration) ([]MetricSeries, error)
}

// PromStore implements Store using the Prometheus HTTP query API.
type PromStore struct {
	baseURL    string
	httpClient *http.Client
}

// NewPromStore creates a store backed by a Prometheus-compatible API.
func NewPromStore(baseURL string) *PromStore {
	return &PromStore{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// promResponse is the Prometheus API JSON response envelope.
type promResponse struct {
	Status string   `json:"status"`
	Error  string   `json:"error"`
	Data   promData `json:"data"`
}

type promData struct {
	ResultType string       `json:"resultType"`
	Result     []promResult `json:"result"`
}

type promResult struct {
	Metric map[string]string `json:"metric"`
	Value  []any             `json:"value"`  // [unix_timestamp, "value_string"]
	Values [][]any           `json:"values"` // [[ts, "val"], ...]
}

func (s *PromStore) Query(ctx context.Context, promql string, ts time.Time) ([]MetricSample, error) {
	params := url.Values{
		"query": {promql},
		"time":  {formatTime(ts)},
	}

	resp, err := s.doGet(ctx, "/api/v1/query", params)
	if err != nil {
		return nil, err
	}

	var samples []MetricSample
	for _, r := range resp.Data.Result {
		tv, err := parseValuePair(r.Value)
		if err != nil {
			continue
		}
		samples = append(samples, MetricSample{
			Labels: r.Metric,
			Value:  tv.Value,
			Time:   tv.Time,
		})
	}
	return samples, nil
}

func (s *PromStore) QueryRange(ctx context.Context, promql string, start, end time.Time, step time.Duration) ([]MetricSeries, error) {
	params := url.Values{
		"query": {promql},
		"start": {formatTime(start)},
		"end":   {formatTime(end)},
		"step":  {fmt.Sprintf("%.0fs", step.Seconds())},
	}

	resp, err := s.doGet(ctx, "/api/v1/query_range", params)
	if err != nil {
		return nil, err
	}

	var series []MetricSeries
	for _, r := range resp.Data.Result {
		s := MetricSeries{Labels: r.Metric}
		for _, vp := range r.Values {
			tv, err := parseValuePair(vp)
			if err != nil {
				continue
			}
			s.Values = append(s.Values, tv)
		}
		series = append(series, s)
	}
	return series, nil
}

func (s *PromStore) doGet(ctx context.Context, path string, params url.Values) (*promResponse, error) {
	u := s.baseURL + path + "?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query %s: %w", path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("query %s: status %d: %s", path, resp.StatusCode, string(body))
	}

	var pr promResponse
	if err := json.Unmarshal(body, &pr); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	if pr.Status != "success" {
		return nil, fmt.Errorf("query %s: %s", path, pr.Error)
	}

	return &pr, nil
}

func formatTime(t time.Time) string {
	return strconv.FormatFloat(float64(t.Unix())+float64(t.Nanosecond())/1e9, 'f', 3, 64)
}

func parseValuePair(vp []any) (TimeValue, error) {
	if len(vp) != 2 {
		return TimeValue{}, fmt.Errorf("expected 2 elements, got %d", len(vp))
	}

	ts, ok := vp[0].(float64)
	if !ok {
		return TimeValue{}, fmt.Errorf("expected float64 timestamp")
	}

	valStr, ok := vp[1].(string)
	if !ok {
		return TimeValue{}, fmt.Errorf("expected string value")
	}

	val, err := strconv.ParseFloat(valStr, 64)
	if err != nil {
		return TimeValue{}, fmt.Errorf("parse value %q: %w", valStr, err)
	}

	return TimeValue{
		Time:  time.Unix(int64(ts), int64((ts-float64(int64(ts)))*1e9)),
		Value: val,
	}, nil
}
