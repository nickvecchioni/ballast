# InfraCost: Inference Cost Intelligence Platform

## Design Document & MVP Roadmap

*Working name: `infracost` (CLI: `kubectl cost`)*
*Author: Nick Vecchioni | March 2026*

---

## 1. Problem statement

Inference has overtaken training as the dominant AI infrastructure cost, now exceeding 55% of total AI cloud spend. Organizations running self-hosted inference on Kubernetes face three compounding problems:

1. **No cost attribution.** GPU utilization is tracked at the node level, but there's no mapping from "GPU X was 73% utilized" to "that utilization served 14,000 tokens of Llama-3-70B for the search team, costing $4.82."

2. **No enforcement.** Existing FinOps tools provide dashboards that explain how you exceeded your budget. They cannot prevent it. Budget controls live outside the execution path, which means they observe cost — they don't intercept it.

3. **No GPU-native intelligence.** The current K8s FinOps ecosystem (Kubecost, Vantage, Cast AI) was built for CPU/memory. GPU cost signals — MIG slices, time-slicing, inference server token counts, model-specific utilization patterns — require fundamentally different instrumentation.

## 2. Design principles

- **K8s-native.** Everything is a CRD, a controller, or a DaemonSet. No external SaaS dependency required for core functionality. Installs via Helm.
- **Open core.** Collection and attribution are open-source. Enforcement, forecasting, and the hosted dashboard are paid.
- **Go-first.** The entire backend is Go. The K8s ecosystem is Go-native, NVML bindings exist, and it's the language you know best in this domain.
- **Prometheus-compatible.** All metrics are exposed in Prometheus format. Plugs into existing Grafana stacks with zero config.
- **Minimal footprint.** The DaemonSet should consume <100MB RAM and negligible CPU. It's observing GPU workloads, not competing with them.

## 3. System architecture

```
┌─────────────────────────────────────────────────────────────────┐
│  DATA COLLECTION (DaemonSet + Sidecars)                         │
│                                                                 │
│  ┌──────────┐  ┌───────────────┐  ┌──────────┐  ┌───────────┐ │
│  │ GPU      │  │ Inference     │  │ K8s      │  │ Cloud     │ │
│  │ Metrics  │  │ Server        │  │ Metadata │  │ Billing   │ │
│  │ (DCGM)   │  │ Telemetry     │  │ Enricher │  │ Connector │ │
│  └────┬─────┘  └──────┬────────┘  └────┬─────┘  └─────┬─────┘ │
└───────┼────────────────┼────────────────┼──────────────┼───────┘
        │                │                │              │
        ▼                ▼                ▼              ▼
┌─────────────────────────────────────────────────────────────────┐
│  COST ATTRIBUTION ENGINE (Deployment)                           │
│                                                                 │
│  ┌───────────────┐  ┌──────────────┐  ┌─────────────────────┐  │
│  │ Join &        │  │ Multi-level  │  │ Anomaly Detection   │  │
│  │ Correlate     │──│ Attribution  │──│ & Forecasting       │  │
│  │               │  │              │  │                     │  │
│  └───────────────┘  └──────────────┘  └─────────────────────┘  │
│                            │                                    │
│                     ┌──────┴──────┐                             │
│                     │ VictoriaM.  │                             │
│                     │ (TSDB)      │                             │
│                     └─────────────┘                             │
└─────────────────────────────────────────────────────────────────┘
        │
        ▼
┌─────────────────────────────────────────────────────────────────┐
│  ENFORCEMENT LAYER (Controller + Webhook)                       │
│                                                                 │
│  ┌───────────────┐  ┌──────────────┐  ┌─────────────────────┐  │
│  │ Budget        │  │ Model        │  │ Admission           │  │
│  │ Controller    │  │ Routing      │  │ Webhook             │  │
│  │ (CRD watch)   │  │ Policy       │  │ (reject/downgrade)  │  │
│  └───────────────┘  └──────────────┘  └─────────────────────┘  │
└─────────────────────────────────────────────────────────────────┘
        │
        ▼
┌─────────────────────────────────────────────────────────────────┐
│  OUTPUT LAYER                                                   │
│                                                                 │
│  ┌──────────┐  ┌──────┐  ┌────────┐  ┌───────────┐  ┌──────┐ │
│  │Dashboard │  │ API  │  │ Alerts │  │ Chargeback│  │ CLI  │ │
│  │ (React)  │  │(REST)│  │(Slack) │  │  (CSV)    │  │(kube)│ │
│  └──────────┘  └──────┘  └────────┘  └───────────┘  └──────┘ │
└─────────────────────────────────────────────────────────────────┘
```

## 4. Component deep-dives

### 4.1 Data collection layer

#### 4.1.1 GPU metrics collector (DaemonSet)

**What it does:** Runs on every GPU node. Reads NVIDIA GPU metrics via NVML (the Go bindings for NVIDIA Management Library) and maps utilization to K8s pods.

**Key metrics collected (per GPU, per second):**

| Metric | Source | Why it matters |
|--------|--------|----------------|
| `gpu_utilization_percent` | NVML `DeviceGetUtilizationRates` | Core cost driver |
| `gpu_memory_used_bytes` | NVML `DeviceGetMemoryInfo` | Capacity planning |
| `gpu_sm_occupancy` | DCGM `DCGM_FI_PROF_SM_OCCUPANCY` | True compute usage vs. memory-bound |
| `gpu_power_draw_watts` | NVML `DeviceGetPowerUsage` | Energy cost component |
| `gpu_temperature_celsius` | NVML `DeviceGetTemperature` | Throttling detection |
| `gpu_pcie_throughput_bytes` | NVML `DeviceGetPcieThroughput` | Data movement costs |

**Pod-to-GPU mapping strategy:**

For whole-GPU allocation (most common):
- Query the K8s device plugin allocation via the kubelet `PodResources` gRPC API (`/var/lib/kubelet/pod-resources/kubelet.sock`)
- This returns a direct mapping: Pod X → GPU UUID Y
- Straightforward, well-documented, stable API

For MIG slices:
- NVML `DeviceGetMigDeviceHandleByIndex` enumerates MIG instances
- Each MIG instance has a UUID that the device plugin advertises
- Map MIG UUID → Pod via PodResources API
- Track utilization per MIG instance independently

For time-sliced GPUs:
- Use NVML `DeviceGetComputeRunningProcesses` to get PIDs on each GPU
- Map PID → container via `/proc/<pid>/cgroup` → container ID → Pod
- This is the gnarliest path but necessary for time-slicing setups

**Go package structure:**

```
pkg/
  collector/
    nvml.go          // NVML wrapper, GPU metric reads
    podresources.go  // kubelet PodResources gRPC client
    pidmapper.go     // PID-to-pod fallback for time-slicing
    collector.go     // Main collection loop (1s interval)
```

**Resource budget:** Target <50MB RSS, <0.1 CPU core. NVML calls are cheap (~microseconds). The PodResources API is a local gRPC call. The main cost is the Prometheus exposition endpoint serving scrapers.

#### 4.1.2 Inference server telemetry (sidecar)

**What it does:** Scrapes the inference server's metrics endpoint and extracts token-level data. Deployed as an optional sidecar alongside vLLM/TGI/Triton pods.

**Supported inference servers (in priority order):**

1. **vLLM** (Phase 1) — Exposes `/metrics` with Prometheus format:
   - `vllm:num_requests_running` — active requests
   - `vllm:num_generation_tokens_total` — output tokens produced
   - `vllm:num_prompt_tokens_total` — input tokens processed
   - `vllm:request_success_total` — completed requests
   - `vllm:avg_generation_throughput_toks_per_s` — throughput
   - `vllm:gpu_cache_usage_perc` — KV cache utilization

2. **TGI** (Phase 2) — `/metrics` endpoint with similar token counters
3. **Triton** (Phase 3) — Prometheus metrics via Triton's metrics API
4. **Ollama** (Phase 3) — `/api/tags` and inference response headers

**The sidecar approach vs. eBPF:**

Sidecar (Phase 1): Simpler. A lightweight Go binary that polls the inference server's metrics endpoint every 5s, enriches with pod labels from the downward API, and exposes a combined Prometheus endpoint. Downside: requires modifying the pod spec (adding the sidecar container). Works with a mutating webhook to auto-inject.

eBPF (Future): Attach to the inference server's network socket at the kernel level. Zero pod modification needed. Can intercept HTTP request/response pairs and extract token counts from response headers/bodies. Much harder to build and maintain, but eliminates the "please add our sidecar" friction. Consider this for v2.

**Enrichment labels applied:**

```
infracost_model_name="llama-3-70b"
infracost_team="search"
infracost_namespace="search-prod"
infracost_deployment="llm-serve"
infracost_node="gpu-node-0a3f"
infracost_gpu_uuid="GPU-a1b2c3d4"
infracost_gpu_type="NVIDIA-H100-SXM5-80GB"
infracost_instance_type="p5.48xlarge"
```

#### 4.1.3 K8s metadata enricher

**What it does:** Watches the K8s API server for Pod, Namespace, Node, and custom resource events. Maintains an in-memory cache of the label/annotation mappings that the attribution engine needs.

**Implementation:** Use `client-go` informers (SharedInformerFactory) with a watch on Pods, Nodes, and Namespaces. This is the standard K8s controller pattern — the informer keeps a local cache that's eventually consistent with the API server, and you get callbacks on add/update/delete. No polling required.

**Label convention (documented, customer-configurable):**

```yaml
# On Pods/Deployments:
labels:
  infracost.io/team: "search"
  infracost.io/cost-center: "eng-search-123"
  infracost.io/environment: "production"

# On Namespaces:
annotations:
  infracost.io/budget-owner: "alice@company.com"
  infracost.io/monthly-budget: "15000"
```

If labels aren't present, fall back to namespace name as the team identifier. The goal is zero-config basic functionality, with labels enabling richer attribution.

#### 4.1.4 Cloud billing connector

**What it does:** Pulls actual dollar costs from cloud provider billing APIs and maps them to K8s nodes.

**Phase 1: Static pricing model.** User configures GPU cost-per-hour in a ConfigMap. This covers on-prem and simple cloud setups:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: infracost-pricing
data:
  pricing.yaml: |
    gpu_types:
      NVIDIA-H100-SXM5-80GB:
        cost_per_hour_usd: 3.90
      NVIDIA-A100-SXM4-80GB:
        cost_per_hour_usd: 1.80
      NVIDIA-L4:
        cost_per_hour_usd: 0.65
```

**Phase 2: Dynamic billing integration.** Pull from AWS CUR (Cost and Usage Reports), GCP BigQuery billing export, or Azure Cost Management API. Map instance IDs to K8s nodes via the node's `providerID` field. Handle reserved instances, spot pricing, and committed use discounts automatically. This is where the real accuracy comes from but it's complex — save for after PMF.

### 4.2 Cost attribution engine

This is the core IP. A single Go Deployment that consumes all the collected metrics and produces attributed cost data.

#### 4.2.1 The join logic

The fundamental operation is a time-aligned join across three data streams:

```
FOR each time window (1 minute):
  FOR each GPU on each node:
    1. Get GPU utilization metrics (from DCGM collector)
    2. Get pod(s) mapped to this GPU (from PodResources / PID mapper)
    3. Get token counts per pod (from inference telemetry sidecar)
    4. Get GPU cost-per-hour (from billing connector or static config)

    5. Compute:
       pod_gpu_seconds = gpu_utilization_pct × window_duration_seconds
       pod_cost = (pod_gpu_seconds / 3600) × gpu_cost_per_hour

    6. Enrich with labels:
       team, namespace, model, deployment, cost_center, environment

    7. Write attributed cost record to TSDB
```

**Handling shared GPUs (MIG / time-slicing):**

For MIG: each slice is an independent GPU instance with its own utilization metric. Attribution is clean — Slice A belongs to Pod X, Slice B to Pod Y.

For time-slicing: multiple pods share a GPU in round-robin. Use PID-based mapping to determine which pod was active during which fraction of the time window. This is inherently approximate — you're sampling at 1s intervals, and time-slicing operates at ~ms granularity. The approach: attribute proportionally based on the fraction of samples where each pod's processes were active on the GPU.

#### 4.2.2 Multi-level attribution

The engine maintains running aggregations at four granularity levels:

| Level | Key | Example |
|-------|-----|---------|
| Request | request_id (if available) | "Request abc123 cost $0.003" |
| Endpoint | namespace + deployment + model | "search/llm-serve/llama-3-70b costs $12.40/hr" |
| Team | team label or namespace | "Search team spent $847 this week" |
| Cluster | cluster name | "prod-us-east-1 total GPU spend: $23,400/mo" |

Each level is a different Prometheus metric series. The CLI and dashboard can query any level.

#### 4.2.3 Storage (VictoriaMetrics)

**Why VictoriaMetrics over vanilla Prometheus:**
- Better compression (10x less disk for same data)
- Handles high cardinality (important when you have per-pod, per-model, per-team labels)
- Prometheus-compatible query API (PromQL works)
- Simple single-binary deployment
- Built-in downsampling for historical data

**Retention policy:**
- Raw (1s resolution): 7 days
- 1-minute aggregates: 30 days
- 1-hour aggregates: 1 year

Deploy as a StatefulSet within the Helm chart. For the hosted/SaaS version, use VictoriaMetrics Cloud.

### 4.3 Enforcement layer

#### 4.3.1 Custom resource definitions

**InferenceBudget CRD:**

```yaml
apiVersion: infracost.io/v1alpha1
kind: InferenceBudget
metadata:
  name: search-team-budget
  namespace: search
spec:
  # Budget limits
  limits:
    monthly_usd: 15000
    daily_usd: 600          # optional daily cap
    per_request_usd: 0.50   # optional per-request ceiling

  # Alert thresholds (percentage of monthly limit)
  alerts:
    thresholds: [50, 75, 90, 100]
    channels:
      - type: slack
        webhook: "https://hooks.slack.com/..."
      - type: pagerduty
        routing_key: "abc123"

  # Enforcement mode
  enforcement:
    mode: hard              # hard | soft | monitor
    # hard: reject new GPU pod requests when over budget
    # soft: alert but allow
    # monitor: collect data only

    # Actions at each threshold
    actions:
      - at_percent: 75
        action: alert
      - at_percent: 90
        action: downgrade_model_tier
        target_tier: cost-optimized
      - at_percent: 100
        action: reject_new_pods

  # Model routing policy (optional)
  model_routing:
    default_tier: standard
    tiers:
      - name: premium
        models: ["llama-3-70b", "mixtral-8x22b"]
        max_budget_percent: 60
      - name: standard
        models: ["llama-3-8b", "mistral-7b"]
      - name: cost-optimized
        models: ["llama-3-8b-q4", "phi-3-mini"]

status:
  current_month_spend_usd: 8432.17
  daily_spend_usd: 312.50
  projected_monthly_usd: 14200.00
  budget_percent_used: 56.2
  last_updated: "2026-03-20T14:30:00Z"
```

**InferenceCostReport CRD (read-only, controller-generated):**

```yaml
apiVersion: infracost.io/v1alpha1
kind: InferenceCostReport
metadata:
  name: search-2026-03-weekly-12
  namespace: search
spec:
  period: weekly
  start: "2026-03-13T00:00:00Z"
  end: "2026-03-20T00:00:00Z"
status:
  total_cost_usd: 2134.50
  breakdown:
    by_model:
      - model: llama-3-70b
        cost_usd: 1820.30
        tokens_served: 45_000_000
        cost_per_million_tokens: 40.45
        avg_gpu_utilization: 0.67
      - model: llama-3-8b
        cost_usd: 314.20
        tokens_served: 120_000_000
        cost_per_million_tokens: 2.62
        avg_gpu_utilization: 0.42
    by_deployment:
      - deployment: llm-serve-primary
        cost_usd: 1650.00
      - deployment: llm-serve-batch
        cost_usd: 484.50
  efficiency:
    avg_gpu_utilization: 0.58
    estimated_waste_usd: 340.00
    recommendations:
      - "llm-serve-batch avg utilization is 23%. Consider time-sharing with another workload or scaling down."
      - "llama-3-70b serves 30% of tokens but 85% of cost. Evaluate if llama-3-8b can handle lower-complexity requests."
```

#### 4.3.2 Budget controller

Standard K8s controller pattern using controller-runtime:

1. Watch InferenceBudget CRDs
2. Every 60s, query the attribution engine for current spend per namespace
3. Compare against budget limits
4. Update InferenceBudget status subresource
5. If thresholds crossed: fire alerts, update routing policies, or mark namespace as over-budget

The controller writes a `infracost.io/budget-status` annotation on the namespace:
- `ok` — under 75%
- `warning` — 75-90%
- `critical` — 90-100%
- `exceeded` — over 100%

#### 4.3.3 Admission webhook

A ValidatingWebhookConfiguration that intercepts Pod CREATE/UPDATE requests for pods requesting GPU resources (`nvidia.com/gpu`, `nvidia.com/mig-*`).

Logic:
1. Check the pod's namespace for an InferenceBudget
2. If budget enforcement is `hard` and status is `exceeded`, reject the pod with a clear error message: "Namespace 'search' has exceeded its monthly inference budget ($15,000). Current spend: $15,432. Contact your platform team or adjust the budget."
3. If budget enforcement is `soft`, allow but annotate the pod with a warning

This is intentionally conservative — it only blocks *new* pod creation, not existing running pods. You don't want to kill running inference servers when a budget threshold is crossed. That would cause outages.

### 4.4 Output layer

#### 4.4.1 kubectl plugin (CLI)

Install: `kubectl krew install cost` (or standalone binary)

**Core commands:**

```bash
# Real-time cost view (like kubectl top but for cost)
kubectl cost top pods -n search
# NAMESPACE  POD                    MODEL          GPU_UTIL  TOKENS/s  COST/HR
# search     llm-serve-abc-12345    llama-3-70b    67%       2,340     $2.61
# search     llm-serve-abc-67890    llama-3-8b     42%       8,900     $0.27
# search     embedding-serve-xyz    bge-large      18%       12,100    $0.12

# Team-level summary
kubectl cost summary -n search --period=this-month
# Team: search | Budget: $15,000 | Spent: $8,432 (56.2%) | Projected: $14,200
# Top models by cost:
#   llama-3-70b:  $7,200 (85.4%)
#   llama-3-8b:   $1,000 (11.9%)
#   bge-large:    $232   (2.7%)

# Cluster-wide overview
kubectl cost summary --all-namespaces --period=last-7d
# NAMESPACE   TEAM      7D_COST    BUDGET    UTIL%    TOP_MODEL
# search      search    $2,134     $15,000   58%      llama-3-70b
# recommend   ml-rec    $1,890     $10,000   43%      mixtral-8x22b
# chatbot     product   $940       $5,000    72%      llama-3-8b

# Export for finance
kubectl cost export --period=2026-03 --format=csv > march-chargeback.csv
```

Implementation: Use the `kubectl` plugin mechanism (a binary named `kubectl-cost` in PATH). Talks to the attribution engine's REST API running inside the cluster (accessed via `kubectl port-forward` or an Ingress).

#### 4.4.2 Dashboard (React)

Phase 2 deliverable. A React + TypeScript SPA that queries the attribution engine's REST API.

**Key views:**
1. **Overview** — Cluster-wide GPU spend, budget status across namespaces, trend line
2. **Team drill-down** — Per-team cost breakdown by model, deployment, time
3. **Model economics** — Cost per million tokens, utilization efficiency, model comparison
4. **Alerts & anomalies** — Timeline of budget threshold crossings, spike events
5. **Recommendations** — Actionable waste reduction suggestions with estimated savings

Tech: React, TypeScript, Recharts for visualization, TanStack Query for data fetching. Deploy as a static build served by the attribution engine's Go HTTP server. No separate frontend deployment.

#### 4.4.3 REST API

Served by the attribution engine Deployment. Endpoints:

```
GET  /api/v1/cost/pods?namespace=X&period=1h
GET  /api/v1/cost/namespaces?period=this-month
GET  /api/v1/cost/models?namespace=X&period=7d
GET  /api/v1/cost/summary?period=this-month
GET  /api/v1/budgets
GET  /api/v1/budgets/{namespace}
GET  /api/v1/recommendations?namespace=X
GET  /api/v1/export?period=2026-03&format=csv

GET  /metrics  (Prometheus scrape endpoint)
```

Auth: ServiceAccount token validation for in-cluster access. API key for external access (dashboard, CI/CD).

## 5. Repo structure

```
infracost/
├── cmd/
│   ├── collector/          # DaemonSet binary
│   │   └── main.go
│   ├── engine/             # Attribution engine + API server
│   │   └── main.go
│   ├── controller/         # Budget controller + webhook
│   │   └── main.go
│   ├── kubectl-cost/       # CLI plugin
│   │   └── main.go
│   └── sidecar/            # Inference telemetry sidecar
│       └── main.go
├── pkg/
│   ├── collector/
│   │   ├── nvml.go         # NVIDIA GPU metric collection
│   │   ├── podresources.go # kubelet PodResources client
│   │   ├── pidmapper.go    # PID-to-pod for time-slicing
│   │   └── collector.go    # Main loop, Prometheus exporter
│   ├── telemetry/
│   │   ├── vllm.go         # vLLM metrics scraper
│   │   ├── tgi.go          # TGI metrics scraper
│   │   └── scraper.go      # Common interface
│   ├── enricher/
│   │   └── enricher.go     # K8s metadata cache (informers)
│   ├── billing/
│   │   ├── static.go       # ConfigMap-based pricing
│   │   ├── aws.go          # AWS CUR integration
│   │   └── gcp.go          # GCP BigQuery billing
│   ├── attribution/
│   │   ├── engine.go       # Core join + attribution logic
│   │   ├── aggregator.go   # Multi-level aggregation
│   │   └── storage.go      # VictoriaMetrics read/write
│   ├── enforcement/
│   │   ├── controller.go   # Budget reconciler
│   │   ├── webhook.go      # Admission webhook handler
│   │   └── routing.go      # Model tier routing logic
│   ├── api/
│   │   ├── server.go       # HTTP server
│   │   ├── handlers.go     # REST endpoint handlers
│   │   └── middleware.go    # Auth, logging, CORS
│   └── models/
│       ├── types.go         # Core data types
│       └── crd_types.go     # CRD Go types (kubebuilder)
├── api/
│   └── v1alpha1/
│       ├── inferencebudget_types.go
│       ├── inferencecost_report_types.go
│       └── groupversion_info.go
├── config/
│   ├── crd/                # Generated CRD YAMLs
│   ├── rbac/               # RBAC manifests
│   ├── webhook/            # Webhook config
│   └── samples/            # Example CRs
├── deploy/
│   └── helm/
│       └── infracost/
│           ├── Chart.yaml
│           ├── values.yaml
│           ├── templates/
│           │   ├── daemonset.yaml
│           │   ├── deployment.yaml
│           │   ├── configmap.yaml
│           │   ├── service.yaml
│           │   ├── serviceaccount.yaml
│           │   ├── clusterrole.yaml
│           │   ├── webhook.yaml
│           │   └── victoriametrics.yaml
│           └── crds/
├── dashboard/              # React frontend (Phase 2)
│   ├── src/
│   └── package.json
├── hack/
│   ├── dev-cluster.sh      # Spin up local GPU k3s
│   └── load-gen.sh         # Generate test inference traffic
├── docs/
│   ├── getting-started.md
│   ├── architecture.md
│   └── configuration.md
├── Dockerfile.collector
├── Dockerfile.engine
├── Dockerfile.controller
├── go.mod
├── go.sum
├── Makefile
└── README.md
```

## 6. Data flow example

Here's what happens when a user sends an inference request to a vLLM pod:

```
1. User sends "Summarize this document" to search team's LLM endpoint
                │
2. Request hits vLLM pod (search/llm-serve-abc) on gpu-node-07
                │
3. vLLM processes: 2,400 input tokens → 350 output tokens (1.2s)
                │
4. SIMULTANEOUSLY, every second:
   │
   ├─ GPU Collector (DaemonSet on gpu-node-07) reads:
   │  - GPU 0 utilization: 73%
   │  - GPU 0 memory: 68GB / 80GB
   │  - Mapped to pod: search/llm-serve-abc (via PodResources API)
   │
   ├─ Inference Sidecar (in llm-serve-abc pod) reads vLLM /metrics:
   │  - prompt_tokens_total increased by 2,400
   │  - generation_tokens_total increased by 350
   │  - model: meta-llama/Llama-3-70B
   │
   └─ K8s Enricher has cached:
      - pod search/llm-serve-abc → team=search, cost-center=eng-123
      - node gpu-node-07 → instance-type=p5.48xlarge, gpu=H100

5. Attribution Engine (every 60s) computes:
   - llm-serve-abc used 73% of 1 H100 for this minute
   - H100 cost: $3.90/hr → $0.065/min
   - Pod cost this minute: 0.73 × $0.065 = $0.047
   - Tokens served this minute: ~24,000 prompt + ~3,500 generation
   - Cost per million tokens: $1.71

6. Engine writes to VictoriaMetrics:
   infracost_pod_cost_usd{namespace="search",pod="llm-serve-abc",
     model="llama-3-70b",team="search"} 0.047

7. Budget Controller checks:
   - search namespace InferenceBudget: $15,000/mo
   - Current spend: $8,432.22 (56.2%)
   - Status: "ok" (under 75% threshold)
   - No action needed

8. CLI query returns:
   $ kubectl cost top pods -n search
   POD              MODEL          GPU%  TOKENS/s  $/HR
   llm-serve-abc    llama-3-70b    73%   2,340     $2.85
```

## 7. MVP roadmap

### Phase 0: Dev environment setup (3-5 days)

**Goal:** A working local environment where you can develop and test.

- [ ] Provision a GPU cloud instance for testing (Lambda Cloud or RunPod, single H100)
- [ ] Install k3s with NVIDIA GPU Operator
- [ ] Deploy vLLM with a small model (Llama-3-8B-Instruct)
- [ ] Verify DCGM exporter is running and producing metrics
- [ ] Write a simple load generator script (curl loop hitting vLLM's `/v1/completions`)
- [ ] Verify you can see GPU utilization change in response to load
- [ ] Initialize Go module, set up repo structure, Makefile basics

**Exit criteria:** You can send inference requests, see GPU metrics change, and have a Go project skeleton.

### Phase 1: The useful metric exporter (2-3 weeks)

**Goal:** A DaemonSet that answers "what is each pod costing me right now?" Open-source this.

**Week 1:**
- [ ] Implement `pkg/collector/nvml.go` — NVML wrapper for GPU metrics
- [ ] Implement `pkg/collector/podresources.go` — kubelet PodResources client
- [ ] Implement `pkg/collector/collector.go` — main loop, join GPU→Pod
- [ ] Expose Prometheus metrics endpoint with pod-level GPU utilization
- [ ] Test: deploy as DaemonSet, verify metrics appear in Prometheus

**Week 2:**
- [ ] Implement `pkg/telemetry/vllm.go` — vLLM metrics scraper
- [ ] Implement the inference sidecar binary (or initially just run it as part of the DaemonSet for simplicity)
- [ ] Add token count metrics enriched with pod labels
- [ ] Implement `pkg/billing/static.go` — ConfigMap-based GPU pricing
- [ ] Compute and expose `infracost_pod_cost_per_hour` metric

**Week 3:**
- [ ] Implement `cmd/kubectl-cost/main.go` — basic `kubectl cost top pods`
- [ ] Create Helm chart with DaemonSet + ConfigMap
- [ ] Write Grafana dashboard JSON (import-ready)
- [ ] Write README with screenshots, quick-start guide
- [ ] Publish to GitHub, post on Hacker News / Reddit r/kubernetes

**Exit criteria:** `helm install infracost ./deploy/helm/infracost` on a GPU K8s cluster gives you per-pod GPU cost in Prometheus/Grafana and via `kubectl cost top pods`. Open-source, Apache 2.0 license.

### Phase 2: Attribution engine + basic enforcement (3-4 weeks)

**Goal:** Historical cost data, team-level attribution, and budget alerts. This is where you start charging.

**Week 4-5:**
- [ ] Deploy VictoriaMetrics as a StatefulSet in the Helm chart
- [ ] Implement `pkg/attribution/engine.go` — the join + correlate logic
- [ ] Implement `pkg/attribution/aggregator.go` — multi-level rollups
- [ ] Implement `pkg/attribution/storage.go` — VictoriaMetrics writer
- [ ] Add historical queries to the REST API
- [ ] Extend CLI: `kubectl cost summary --period=this-month`

**Week 6-7:**
- [ ] Scaffold CRDs with kubebuilder (InferenceBudget, InferenceCostReport)
- [ ] Implement `pkg/enforcement/controller.go` — budget reconciler (soft enforcement only: alerts)
- [ ] Add Slack/PagerDuty webhook alerting for budget thresholds
- [ ] Implement the `kubectl cost export` command for chargeback CSV
- [ ] Write docs: configuration guide, architecture overview
- [ ] Begin building the React dashboard (overview page, team drill-down)

**Exit criteria:** Teams can set monthly budgets via CRD, get alerted when approaching limits, view historical cost data, and export for finance.

### Phase 3: Hard enforcement + polish (3-4 weeks)

**Goal:** Admission webhook, model routing, dashboard v1. Ship the paid tier.

**Week 8-9:**
- [ ] Implement `pkg/enforcement/webhook.go` — admission webhook
- [ ] Implement `pkg/enforcement/routing.go` — model tier routing policy
- [ ] Add anomaly detection (z-score over rolling windows)
- [ ] Add spend forecasting (linear projection from trailing data)
- [ ] Implement the InferenceCostReport controller (auto-generate weekly reports)

**Week 10-11:**
- [ ] Finish dashboard: model economics view, recommendations, alerts timeline
- [ ] Add TGI support to the telemetry scraper
- [ ] Implement MIG-aware cost attribution
- [ ] Write integration tests (deploy on real GPU cluster, run load, verify costs)
- [ ] Create landing page and docs site
- [ ] Set up hosted demo environment

**Exit criteria:** Full product loop: collect → attribute → alert → enforce → report. Dashboard is usable. Helm chart is production-hardened.

### Phase 4: Growth + enterprise (ongoing)

- [ ] Cloud billing connectors (AWS CUR, GCP BigQuery)
- [ ] Multi-cluster support (federated cost view)
- [ ] API proxy for third-party LLM cost tracking (OpenAI, Anthropic, etc.)
- [ ] SSO/RBAC for dashboard
- [ ] eBPF-based collection (no sidecar needed)
- [ ] Hosted SaaS option (control plane in cloud, agent in customer cluster)
- [ ] SOC2 compliance

## 8. Business model

**Open-source tier (free):**
- GPU metrics collector (DaemonSet)
- Pod-level cost attribution
- Prometheus metrics endpoint
- kubectl plugin (basic commands)
- Grafana dashboard template

**Pro tier ($X/node/month, self-hosted):**
- VictoriaMetrics-backed historical data
- InferenceBudget CRDs with soft enforcement (alerts)
- Team-level attribution and chargeback export
- React dashboard
- Slack/PagerDuty integrations

**Enterprise tier (custom pricing):**
- Hard enforcement (admission webhook)
- Model routing policies
- Anomaly detection and forecasting
- Multi-cluster federation
- Cloud billing integration (dynamic pricing)
- SSO/RBAC
- Priority support

## 9. Key risks and mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| NVIDIA changes NVML/DCGM APIs | Low | High | Pin to NVML versions, abstract behind interface |
| Kubecost adds GPU-native features | Medium | Medium | Move faster; they're CPU-first, GPU is a bolt-on |
| Cloud providers bundle GPU FinOps | Medium | High | Focus on multi-cloud + on-prem; providers only optimize their own cloud |
| K8s DRA makes custom scheduling obsolete | Low | Low | DRA helps collection (better GPU metadata), doesn't solve cost attribution |
| Low adoption of open-source core | Medium | High | Ship useful Grafana dashboards on day 1; solve a pain people Google for |

## 10. Success metrics

**Phase 1 (Month 1):**
- GitHub stars: 200+
- Helm installs: 50+
- HN/Reddit posts get traction

**Phase 2 (Month 2-3):**
- 5+ companies testing in staging
- 2+ design partners providing feedback
- First paid pilot

**Phase 3 (Month 3-4):**
- 3+ paying customers
- $5K+ MRR
- Featured in CNCF landscape or KubeCon talk proposal submitted
