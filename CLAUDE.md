# CLAUDE.md

## Project
InfraCost: K8s-native GPU inference cost attribution platform.
See docs/design.md for full architecture.

## Tech stack
- Go 1.22+ (all backend)
- K8s controller-runtime for CRDs/controllers
- NVIDIA go-nvml for GPU metrics
- kubelet PodResources gRPC API for GPU-to-pod mapping
- VictoriaMetrics for time-series storage
- Prometheus client_golang for metrics exposition
- Helm 3 for packaging

## Repo structure
- cmd/ — binary entrypoints (collector, engine, controller, kubectl-cost, sidecar)
- pkg/ — library packages (collector, telemetry, enricher, billing, attribution, enforcement, api, models)
- deploy/helm/ — Helm chart
- api/v1alpha1/ — CRD types (kubebuilder)

## Conventions
- Standard Go project layout
- Use interfaces for testability (especially NVML and K8s clients)
- Prometheus metrics use the `infracost_` prefix
- All errors wrapped with fmt.Errorf("context: %w", err)
- Use slog for structured logging

## Current phase
Phase 0/1: Building the GPU metrics collector DaemonSet.
Starting with pkg/collector/ — NVML wrapper and PodResources client.
```

**Your first Claude Code prompt:**

Once you have the repo skeleton with `go.mod`, `CLAUDE.md`, and `docs/design.md`, open Claude Code in that directory and hit it with something like:
```
Read docs/design.md, specifically sections 4.1.1 (GPU metrics collector) 
and 5 (repo structure). 

Then scaffold the project directory structure from section 5 -- just create 
the directories and empty .go files with package declarations. Also create 
the Makefile with build targets for each binary in cmd/.

Don't implement anything yet, just the skeleton.
```

This gives Claude Code the lay of the land without asking it to write 2000 lines of Go in one shot. Once the skeleton exists, your second prompt would be:
```
Implement pkg/collector/nvml.go. This wraps the NVIDIA go-nvml package 
(github.com/NVIDIA/go-nvml/pkg/nvml) and exposes a GPUCollector interface 
that returns per-GPU metrics (utilization, memory, power, temperature).

Use an interface so we can mock it in tests. Include a NewNVMLCollector() 
constructor that initializes NVML and a Close() that shuts it down. The 
Collect() method should return a []GPUMetrics slice with one entry per GPU.

Refer to section 4.1.1 of docs/design.md for the exact metrics to collect.
```

Then build outward from there:
```
Now implement pkg/collector/podresources.go. This connects to the kubelet 
PodResources gRPC API at /var/lib/kubelet/pod-resources/kubelet.sock and 
returns a map of GPU UUID -> (pod namespace, pod name). Use the 
k8s.io/kubelet/pkg/apis/podresources/v1 package.
```

Then:
```
Now implement pkg/collector/collector.go. This is the main collection loop 
that ties nvml.go and podresources.go together. Every 1 second, it collects 
GPU metrics and joins them with pod mappings. It exposes the results as 
Prometheus metrics using prometheus/client_golang with the infracost_ prefix.

Key metrics to expose:
- infracost_gpu_utilization_percent (gauge, labels: gpu_uuid, pod, namespace, node)
- infracost_gpu_memory_used_bytes (gauge, same labels)
- infracost_gpu_power_watts (gauge, same labels)
