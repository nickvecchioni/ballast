package attribution

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const instantResponse = `{
  "status": "success",
  "data": {
    "resultType": "vector",
    "result": [
      {
        "metric": {"namespace": "search", "pod": "llm-serve", "gpu_type": "H100"},
        "value": [1711234567.000, "2.847"]
      },
      {
        "metric": {"namespace": "batch", "pod": "train-job", "gpu_type": "A100"},
        "value": [1711234567.000, "1.500"]
      }
    ]
  }
}`

const rangeResponse = `{
  "status": "success",
  "data": {
    "resultType": "matrix",
    "result": [
      {
        "metric": {"namespace": "search", "pod": "llm-serve"},
        "values": [[1711234500.000, "2.500"], [1711234560.000, "3.000"], [1711234620.000, "2.800"]]
      }
    ]
  }
}`

func TestQueryInstant(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("query") == "" {
			t.Error("missing query param")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(instantResponse))
	}))
	defer srv.Close()

	store := NewPromStore(srv.URL)
	samples, err := store.Query(context.Background(), "ballast_pod_cost_per_hour_usd", time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(samples) != 2 {
		t.Fatalf("expected 2 samples, got %d", len(samples))
	}

	if samples[0].Labels["pod"] != "llm-serve" {
		t.Errorf("pod = %q, want %q", samples[0].Labels["pod"], "llm-serve")
	}
	if samples[0].Value != 2.847 {
		t.Errorf("value = %f, want 2.847", samples[0].Value)
	}
	if samples[1].Labels["namespace"] != "batch" {
		t.Errorf("namespace = %q, want %q", samples[1].Labels["namespace"], "batch")
	}
}

func TestQueryRange(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query_range" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(rangeResponse))
	}))
	defer srv.Close()

	store := NewPromStore(srv.URL)
	now := time.Now()
	series, err := store.QueryRange(context.Background(), "test_metric", now.Add(-time.Hour), now, time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(series) != 1 {
		t.Fatalf("expected 1 series, got %d", len(series))
	}
	if len(series[0].Values) != 3 {
		t.Fatalf("expected 3 values, got %d", len(series[0].Values))
	}
	if series[0].Values[0].Value != 2.5 {
		t.Errorf("first value = %f, want 2.5", series[0].Values[0].Value)
	}
	if series[0].Labels["pod"] != "llm-serve" {
		t.Errorf("pod = %q, want %q", series[0].Labels["pod"], "llm-serve")
	}
}

func TestQueryAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status": "error", "error": "bad query"}`))
	}))
	defer srv.Close()

	store := NewPromStore(srv.URL)
	_, err := store.Query(context.Background(), "bad", time.Now())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestQueryHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))
	defer srv.Close()

	store := NewPromStore(srv.URL)
	_, err := store.Query(context.Background(), "test", time.Now())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestQueryConnectionError(t *testing.T) {
	store := NewPromStore("http://127.0.0.1:1")
	_, err := store.Query(context.Background(), "test", time.Now())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestQueryEmptyResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status": "success", "data": {"resultType": "vector", "result": []}}`))
	}))
	defer srv.Close()

	store := NewPromStore(srv.URL)
	samples, err := store.Query(context.Background(), "test", time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(samples) != 0 {
		t.Errorf("expected 0 samples, got %d", len(samples))
	}
}

func TestStoreImplementsInterface(t *testing.T) {
	var _ Store = (*PromStore)(nil)
}
