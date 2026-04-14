#!/usr/bin/env bash
set -euo pipefail

# End-to-end test script for Ballast on a GPU VM.
# Covers Layer 2 (standalone NVML) and Layer 3 (k3s + Helm + vLLM).
#
# Prerequisites: NVIDIA drivers installed, nvidia-smi works.
# Tested on: TensorDock V100, Lambda A10/A100, Ubuntu 22.04+
#
# Usage: bash hack/test-e2e.sh

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

step() { echo -e "\n${GREEN}===> $1${NC}\n"; }
warn() { echo -e "${YELLOW}WARN: $1${NC}"; }
fail() { echo -e "${RED}FAIL: $1${NC}"; exit 1; }

# ---------------------------------------------------------------------------
# Preflight checks
# ---------------------------------------------------------------------------
step "Preflight checks"

nvidia-smi > /dev/null 2>&1 || fail "nvidia-smi not found. Are NVIDIA drivers installed?"
echo "GPU detected:"
nvidia-smi --query-gpu=name,memory.total,driver_version --format=csv,noheader

# ---------------------------------------------------------------------------
# Install dependencies
# ---------------------------------------------------------------------------
step "Installing dependencies (Go, Helm)"

if ! command -v go &> /dev/null; then
    echo "Installing Go..."
    GO_VERSION="1.25.6"
    wget -q "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" -O /tmp/go.tar.gz
    sudo rm -rf /usr/local/go
    sudo tar -C /usr/local -xzf /tmp/go.tar.gz
    export PATH=$PATH:/usr/local/go/bin
    echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
fi
echo "Go: $(go version)"

if ! command -v helm &> /dev/null; then
    echo "Installing Helm..."
    curl -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash
fi
echo "Helm: $(helm version --short)"

# ---------------------------------------------------------------------------
# Layer 2: Standalone NVML test
# ---------------------------------------------------------------------------
step "Layer 2: Testing NVML collection (no Kubernetes)"

go run hack/test-nvml.go || fail "NVML test failed"

echo -e "${GREEN}Layer 2 PASSED${NC}"

# ---------------------------------------------------------------------------
# Layer 3: Kubernetes setup
# ---------------------------------------------------------------------------
step "Layer 3: Installing k3s"

if ! command -v kubectl &> /dev/null; then
    curl -sfL https://get.k3s.io | INSTALL_K3S_EXEC="--disable=traefik" sh -
    mkdir -p ~/.kube
    sudo cp /etc/rancher/k3s/k3s.yaml ~/.kube/config
    sudo chown $(id -u):$(id -g) ~/.kube/config
    export KUBECONFIG=~/.kube/config
    echo 'export KUBECONFIG=~/.kube/config' >> ~/.bashrc
fi

echo "Waiting for k3s to be ready..."
kubectl wait --for=condition=Ready node --all --timeout=120s

step "Installing NVIDIA container toolkit + device plugin"

# Container toolkit (lets k3s/containerd use GPUs)
if ! command -v nvidia-ctk &> /dev/null; then
    distribution=$(. /etc/os-release; echo $ID$VERSION_ID)
    curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey | \
        sudo gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg
    curl -s -L "https://nvidia.github.io/libnvidia-container/${distribution}/libnvidia-container.list" | \
        sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' | \
        sudo tee /etc/apt/sources.list.d/nvidia-container-toolkit.list
    sudo apt-get update -qq
    sudo apt-get install -y -qq nvidia-container-toolkit
fi

# Configure containerd for k3s to use NVIDIA runtime
sudo nvidia-ctk runtime configure --runtime=containerd --config=/var/lib/rancher/k3s/agent/etc/containerd/config.toml.tmpl
sudo systemctl restart k3s

echo "Waiting for k3s to restart..."
sleep 10
kubectl wait --for=condition=Ready node --all --timeout=120s

# Deploy NVIDIA device plugin
kubectl apply -f https://raw.githubusercontent.com/NVIDIA/k8s-device-plugin/v0.17.0/deployments/static/nvidia-device-plugin.yml

echo "Waiting for device plugin..."
kubectl -n kube-system wait --for=condition=Ready pod -l name=nvidia-device-plugin-ds --timeout=120s

echo "Checking GPU is visible to Kubernetes..."
sleep 5
GPU_COUNT=$(kubectl get nodes -o jsonpath='{.items[0].status.allocatable.nvidia\.com/gpu}' 2>/dev/null || echo "0")
if [ "$GPU_COUNT" = "0" ] || [ -z "$GPU_COUNT" ]; then
    warn "GPU not yet visible to K8s. Waiting 30s..."
    sleep 30
    GPU_COUNT=$(kubectl get nodes -o jsonpath='{.items[0].status.allocatable.nvidia\.com/gpu}')
fi
echo "GPUs visible to Kubernetes: $GPU_COUNT"

# ---------------------------------------------------------------------------
# Deploy vLLM with a small model
# ---------------------------------------------------------------------------
step "Deploying vLLM (Qwen2.5-1.5B-Instruct)"

cat <<'VLLM_EOF' | kubectl apply -f -
apiVersion: v1
kind: Pod
metadata:
  name: vllm-test
  namespace: default
  labels:
    app: vllm-test
spec:
  containers:
    - name: vllm
      image: vllm/vllm-openai:latest
      args:
        - --model=Qwen/Qwen2.5-1.5B-Instruct
        - --max-model-len=2048
        - --gpu-memory-utilization=0.8
      ports:
        - containerPort: 8000
      resources:
        limits:
          nvidia.com/gpu: "1"
  restartPolicy: Never
VLLM_EOF

echo "Waiting for vLLM to be ready (this pulls the image + model, may take a few minutes)..."
kubectl wait --for=condition=Ready pod/vllm-test --timeout=600s || {
    echo "vLLM pod status:"
    kubectl describe pod/vllm-test
    kubectl logs vllm-test --tail=50
    fail "vLLM pod did not become ready"
}
echo "vLLM is running"

# ---------------------------------------------------------------------------
# Build and load Ballast collector image
# ---------------------------------------------------------------------------
step "Building Ballast collector image"

docker build -f Dockerfile.collector -t ballast-collector:test . 2>&1 | tail -5

# Import into k3s containerd
docker save ballast-collector:test | sudo k3s ctr images import -
echo "Image loaded into k3s"

# ---------------------------------------------------------------------------
# Helm install Ballast
# ---------------------------------------------------------------------------
step "Installing Ballast via Helm"

# Override nodeSelector since TensorDock nodes may not have the label
helm install ballast deploy/helm/ballast \
    --namespace ballast --create-namespace \
    --set collector.image.repository=ballast-collector \
    --set collector.image.tag=test \
    --set collector.image.pullPolicy=Never \
    --set collector.nodeSelector=null \
    --set "collector.tolerations={}" \
    --set pricing.gpu_types.NVIDIA-Tesla-V100-SXM3-32GB.cost_per_hour_usd=0.29

echo "Waiting for collector pod..."
kubectl -n ballast wait --for=condition=Ready pod -l app.kubernetes.io/component=collector --timeout=120s || {
    echo "Collector pod status:"
    kubectl -n ballast describe pod -l app.kubernetes.io/component=collector
    kubectl -n ballast logs -l app.kubernetes.io/component=collector --tail=50
    fail "Collector pod did not become ready"
}

# ---------------------------------------------------------------------------
# Validate metrics
# ---------------------------------------------------------------------------
step "Validating metrics"

sleep 5
COLLECTOR_POD=$(kubectl -n ballast get pod -l app.kubernetes.io/component=collector -o jsonpath='{.items[0].metadata.name}')

echo "Raw metrics from collector:"
kubectl -n ballast exec "$COLLECTOR_POD" -- wget -qO- http://localhost:9400/metrics 2>/dev/null | grep "^ballast_" | head -20

echo ""
echo "GPU utilization:"
kubectl -n ballast exec "$COLLECTOR_POD" -- wget -qO- http://localhost:9400/metrics 2>/dev/null | grep "ballast_gpu_utilization_percent"

echo ""
echo "Pod cost:"
kubectl -n ballast exec "$COLLECTOR_POD" -- wget -qO- http://localhost:9400/metrics 2>/dev/null | grep "ballast_pod_cost_per_hour_usd"

# ---------------------------------------------------------------------------
# Test kubectl-cost
# ---------------------------------------------------------------------------
step "Testing kubectl-cost plugin"

go build -o /tmp/kubectl-cost ./cmd/kubectl-cost

# Port-forward in background
kubectl -n ballast port-forward svc/ballast-collector 9400:9400 &
PF_PID=$!
sleep 2

echo ""
/tmp/kubectl-cost top pods --all-namespaces --metrics-url http://localhost:9400/metrics
echo ""

kill $PF_PID 2>/dev/null || true

# ---------------------------------------------------------------------------
# Generate some load and re-check
# ---------------------------------------------------------------------------
step "Generating inference load"

VLLM_IP=$(kubectl get pod vllm-test -o jsonpath='{.status.podIP}')
echo "Sending 10 requests to vLLM..."
for i in $(seq 1 10); do
    kubectl exec vllm-test -- curl -s http://localhost:8000/v1/completions \
        -H "Content-Type: application/json" \
        -d '{"model":"Qwen/Qwen2.5-1.5B-Instruct","prompt":"Explain GPU cost attribution in one sentence.","max_tokens":50}' > /dev/null &
done
wait
echo "Load complete. Waiting for metrics to update..."
sleep 5

echo ""
echo "Metrics after load:"
kubectl -n ballast port-forward svc/ballast-collector 9400:9400 &
PF_PID=$!
sleep 2
/tmp/kubectl-cost top pods --all-namespaces --metrics-url http://localhost:9400/metrics
kill $PF_PID 2>/dev/null || true

# ---------------------------------------------------------------------------
# Done
# ---------------------------------------------------------------------------
step "All tests passed!"

echo "Summary:"
echo "  - NVML collection:  OK"
echo "  - k3s + GPU:        OK"
echo "  - vLLM running:     OK"
echo "  - Collector metrics: OK"
echo "  - kubectl-cost:     OK"
echo ""
echo "To clean up:"
echo "  kubectl delete pod vllm-test"
echo "  helm uninstall ballast -n ballast"
echo "  /usr/local/bin/k3s-uninstall.sh"
