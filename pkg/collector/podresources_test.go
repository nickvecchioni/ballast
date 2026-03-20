package collector

import (
	"context"
	"fmt"
	"testing"

	"google.golang.org/grpc"
	podresourcesv1 "k8s.io/kubelet/pkg/apis/podresources/v1"
)

// mockLister implements PodResourcesLister for tests.
type mockLister struct {
	resp *podresourcesv1.ListPodResourcesResponse
	err  error
}

func (m *mockLister) List(_ context.Context, _ *podresourcesv1.ListPodResourcesRequest, _ ...grpc.CallOption) (*podresourcesv1.ListPodResourcesResponse, error) {
	return m.resp, m.err
}

func pod(ns, name string, containers ...*podresourcesv1.ContainerResources) *podresourcesv1.PodResources {
	return &podresourcesv1.PodResources{
		Namespace:  ns,
		Name:       name,
		Containers: containers,
	}
}

func container(name string, devices ...*podresourcesv1.ContainerDevices) *podresourcesv1.ContainerResources {
	return &podresourcesv1.ContainerResources{
		Name:    name,
		Devices: devices,
	}
}

func gpuDevices(ids ...string) *podresourcesv1.ContainerDevices {
	return &podresourcesv1.ContainerDevices{
		ResourceName: "nvidia.com/gpu",
		DeviceIds:    ids,
	}
}

func nonGPUDevices(resource string, ids ...string) *podresourcesv1.ContainerDevices {
	return &podresourcesv1.ContainerDevices{
		ResourceName: resource,
		DeviceIds:    ids,
	}
}

func TestListSinglePodSingleGPU(t *testing.T) {
	lister := &mockLister{
		resp: &podresourcesv1.ListPodResourcesResponse{
			PodResources: []*podresourcesv1.PodResources{
				pod("search", "llm-serve-abc",
					container("inference", gpuDevices("GPU-aaa-111")),
				),
			},
		},
	}

	client := NewPodResourcesClientWithLister(lister)
	gpuMap, err := client.List(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(gpuMap) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(gpuMap))
	}

	info, ok := gpuMap["GPU-aaa-111"]
	if !ok {
		t.Fatal("GPU-aaa-111 not found in map")
	}
	if info.Namespace != "search" {
		t.Errorf("namespace = %q, want %q", info.Namespace, "search")
	}
	if info.PodName != "llm-serve-abc" {
		t.Errorf("pod = %q, want %q", info.PodName, "llm-serve-abc")
	}
	if info.ContainerName != "inference" {
		t.Errorf("container = %q, want %q", info.ContainerName, "inference")
	}
}

func TestListMultiGPUPod(t *testing.T) {
	lister := &mockLister{
		resp: &podresourcesv1.ListPodResourcesResponse{
			PodResources: []*podresourcesv1.PodResources{
				pod("training", "big-model-xyz",
					container("trainer",
						gpuDevices("GPU-001", "GPU-002", "GPU-003", "GPU-004"),
					),
				),
			},
		},
	}

	client := NewPodResourcesClientWithLister(lister)
	gpuMap, err := client.List(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(gpuMap) != 4 {
		t.Fatalf("expected 4 entries, got %d", len(gpuMap))
	}

	for _, id := range []string{"GPU-001", "GPU-002", "GPU-003", "GPU-004"} {
		info, ok := gpuMap[id]
		if !ok {
			t.Errorf("%s not found", id)
			continue
		}
		if info.PodName != "big-model-xyz" {
			t.Errorf("%s pod = %q, want %q", id, info.PodName, "big-model-xyz")
		}
	}
}

func TestListMultiplePods(t *testing.T) {
	lister := &mockLister{
		resp: &podresourcesv1.ListPodResourcesResponse{
			PodResources: []*podresourcesv1.PodResources{
				pod("search", "llm-serve-a",
					container("inference", gpuDevices("GPU-aaa")),
				),
				pod("recommend", "rec-serve-b",
					container("model", gpuDevices("GPU-bbb")),
				),
				pod("chatbot", "chat-serve-c",
					container("llm", gpuDevices("GPU-ccc", "GPU-ddd")),
				),
			},
		},
	}

	client := NewPodResourcesClientWithLister(lister)
	gpuMap, err := client.List(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(gpuMap) != 4 {
		t.Fatalf("expected 4 entries, got %d", len(gpuMap))
	}

	tests := []struct {
		gpuID     string
		wantNS    string
		wantPod   string
		wantCont  string
	}{
		{"GPU-aaa", "search", "llm-serve-a", "inference"},
		{"GPU-bbb", "recommend", "rec-serve-b", "model"},
		{"GPU-ccc", "chatbot", "chat-serve-c", "llm"},
		{"GPU-ddd", "chatbot", "chat-serve-c", "llm"},
	}

	for _, tt := range tests {
		info, ok := gpuMap[tt.gpuID]
		if !ok {
			t.Errorf("%s not found", tt.gpuID)
			continue
		}
		if info.Namespace != tt.wantNS || info.PodName != tt.wantPod || info.ContainerName != tt.wantCont {
			t.Errorf("%s = {%s, %s, %s}, want {%s, %s, %s}",
				tt.gpuID, info.Namespace, info.PodName, info.ContainerName,
				tt.wantNS, tt.wantPod, tt.wantCont)
		}
	}
}

func TestListFiltersNonGPUDevices(t *testing.T) {
	lister := &mockLister{
		resp: &podresourcesv1.ListPodResourcesResponse{
			PodResources: []*podresourcesv1.PodResources{
				pod("default", "mixed-pod",
					container("app",
						gpuDevices("GPU-real"),
						nonGPUDevices("intel.com/sriov", "vf-0", "vf-1"),
						nonGPUDevices("amd.com/gpu", "amd-0"),
					),
				),
			},
		},
	}

	client := NewPodResourcesClientWithLister(lister)
	gpuMap, err := client.List(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(gpuMap) != 1 {
		t.Fatalf("expected 1 entry (only nvidia.com/gpu), got %d", len(gpuMap))
	}
	if _, ok := gpuMap["GPU-real"]; !ok {
		t.Error("GPU-real not found")
	}
}

func TestListPodsWithNoGPU(t *testing.T) {
	lister := &mockLister{
		resp: &podresourcesv1.ListPodResourcesResponse{
			PodResources: []*podresourcesv1.PodResources{
				pod("default", "cpu-pod",
					container("web"),
				),
				pod("default", "another-cpu-pod",
					container("api", nonGPUDevices("intel.com/qat", "qat-0")),
				),
			},
		},
	}

	client := NewPodResourcesClientWithLister(lister)
	gpuMap, err := client.List(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(gpuMap) != 0 {
		t.Fatalf("expected 0 entries for non-GPU pods, got %d", len(gpuMap))
	}
}

func TestListEmptyResponse(t *testing.T) {
	lister := &mockLister{
		resp: &podresourcesv1.ListPodResourcesResponse{},
	}

	client := NewPodResourcesClientWithLister(lister)
	gpuMap, err := client.List(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(gpuMap) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(gpuMap))
	}
}

func TestListGRPCError(t *testing.T) {
	lister := &mockLister{
		err: fmt.Errorf("connection refused"),
	}

	client := NewPodResourcesClientWithLister(lister)
	_, err := client.List(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestCloseWithoutConnection(t *testing.T) {
	client := NewPodResourcesClientWithLister(&mockLister{
		resp: &podresourcesv1.ListPodResourcesResponse{},
	})

	// Close on a client with no underlying conn should be a no-op.
	if err := client.Close(); err != nil {
		t.Fatalf("unexpected close error: %v", err)
	}
}

func TestClientImplementsInterface(t *testing.T) {
	var _ PodResourcesClient = (*KubeletPodResourcesClient)(nil)
}

func TestMultipleContainersPerPod(t *testing.T) {
	lister := &mockLister{
		resp: &podresourcesv1.ListPodResourcesResponse{
			PodResources: []*podresourcesv1.PodResources{
				pod("ml", "multi-container",
					container("model-a", gpuDevices("GPU-111")),
					container("model-b", gpuDevices("GPU-222")),
				),
			},
		},
	}

	client := NewPodResourcesClientWithLister(lister)
	gpuMap, err := client.List(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(gpuMap) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(gpuMap))
	}

	if info := gpuMap["GPU-111"]; info.ContainerName != "model-a" {
		t.Errorf("GPU-111 container = %q, want %q", info.ContainerName, "model-a")
	}
	if info := gpuMap["GPU-222"]; info.ContainerName != "model-b" {
		t.Errorf("GPU-222 container = %q, want %q", info.ContainerName, "model-b")
	}
}
