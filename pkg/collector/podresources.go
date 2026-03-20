package collector

import (
	"context"
	"fmt"
	"net"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	podresourcesv1 "k8s.io/kubelet/pkg/apis/podresources/v1"
)

const (
	// DefaultSocketPath is the kubelet PodResources API socket.
	DefaultSocketPath = "/var/lib/kubelet/pod-resources/kubelet.sock"

	// gpuResourceName is the device-plugin resource name for NVIDIA GPUs.
	gpuResourceName = "nvidia.com/gpu"

	// defaultConnTimeout is the gRPC dial timeout for the kubelet socket.
	defaultConnTimeout = 10 * time.Second

	// maxMsgSize caps the gRPC response size (16 MB).
	maxMsgSize = 16 * 1024 * 1024
)

// PodInfo identifies the pod and container that owns a GPU.
type PodInfo struct {
	Namespace     string
	PodName       string
	ContainerName string
}

// PodResourcesClient maps GPU UUIDs to the pods that own them.
type PodResourcesClient interface {
	// List returns a map of GPU device ID → PodInfo for every GPU
	// allocated to a pod on this node.
	List(ctx context.Context) (map[string]PodInfo, error)
	// Close tears down the underlying gRPC connection.
	Close() error
}

// KubeletPodResourcesClient implements PodResourcesClient by talking
// to the kubelet PodResources gRPC API over a unix socket.
type KubeletPodResourcesClient struct {
	lister PodResourcesLister
	conn   *grpc.ClientConn
}

// PodResourcesLister abstracts the gRPC client interface so we can
// inject a mock in tests without a real kubelet socket.
type PodResourcesLister interface {
	List(ctx context.Context, in *podresourcesv1.ListPodResourcesRequest, opts ...grpc.CallOption) (*podresourcesv1.ListPodResourcesResponse, error)
}

// NewPodResourcesClient connects to the kubelet socket and returns a ready client.
func NewPodResourcesClient(socketPath string) (*KubeletPodResourcesClient, error) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultConnTimeout)
	defer cancel()

	conn, err := grpc.DialContext(ctx, socketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", addr)
		}),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxMsgSize)),
	)
	if err != nil {
		return nil, fmt.Errorf("dial kubelet pod-resources socket %s: %w", socketPath, err)
	}

	lister := podresourcesv1.NewPodResourcesListerClient(conn)
	return &KubeletPodResourcesClient{lister: lister, conn: conn}, nil
}

// NewPodResourcesClientWithLister creates a client backed by the given
// lister, useful for injecting a mock in tests. The caller is responsible
// for closing any underlying connection.
func NewPodResourcesClientWithLister(lister PodResourcesLister) *KubeletPodResourcesClient {
	return &KubeletPodResourcesClient{lister: lister}
}

// List queries the kubelet for all pod resource allocations and returns
// a map of GPU device ID → PodInfo.
func (c *KubeletPodResourcesClient) List(ctx context.Context) (map[string]PodInfo, error) {
	resp, err := c.lister.List(ctx, &podresourcesv1.ListPodResourcesRequest{})
	if err != nil {
		return nil, fmt.Errorf("list pod resources: %w", err)
	}

	gpuMap := make(map[string]PodInfo)
	for _, pod := range resp.GetPodResources() {
		for _, container := range pod.GetContainers() {
			for _, dev := range container.GetDevices() {
				if dev.GetResourceName() != gpuResourceName {
					continue
				}
				info := PodInfo{
					Namespace:     pod.GetNamespace(),
					PodName:       pod.GetName(),
					ContainerName: container.GetName(),
				}
				for _, id := range dev.GetDeviceIds() {
					gpuMap[id] = info
				}
			}
		}
	}
	return gpuMap, nil
}

// Close shuts down the gRPC connection, if one is held.
func (c *KubeletPodResourcesClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}
