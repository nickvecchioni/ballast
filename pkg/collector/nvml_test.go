package collector

import (
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// mockDevice implements NVMLDevice with configurable return values.
type mockDevice struct {
	uuid        string
	name        string
	utilization nvml.Utilization
	memory      nvml.Memory
	powerMW     uint32
	temperature uint32
	pcieTx      uint32
	pcieRx      uint32
	failOn      string // method name to fail on, empty = succeed
}

func (d *mockDevice) GetUUID() (string, nvml.Return) {
	if d.failOn == "GetUUID" {
		return "", nvml.ERROR_UNKNOWN
	}
	return d.uuid, nvml.SUCCESS
}

func (d *mockDevice) GetName() (string, nvml.Return) {
	if d.failOn == "GetName" {
		return "", nvml.ERROR_UNKNOWN
	}
	return d.name, nvml.SUCCESS
}

func (d *mockDevice) GetUtilizationRates() (nvml.Utilization, nvml.Return) {
	if d.failOn == "GetUtilizationRates" {
		return nvml.Utilization{}, nvml.ERROR_UNKNOWN
	}
	return d.utilization, nvml.SUCCESS
}

func (d *mockDevice) GetMemoryInfo() (nvml.Memory, nvml.Return) {
	if d.failOn == "GetMemoryInfo" {
		return nvml.Memory{}, nvml.ERROR_UNKNOWN
	}
	return d.memory, nvml.SUCCESS
}

func (d *mockDevice) GetPowerUsage() (uint32, nvml.Return) {
	if d.failOn == "GetPowerUsage" {
		return 0, nvml.ERROR_UNKNOWN
	}
	return d.powerMW, nvml.SUCCESS
}

func (d *mockDevice) GetTemperature(nvml.TemperatureSensors) (uint32, nvml.Return) {
	if d.failOn == "GetTemperature" {
		return 0, nvml.ERROR_UNKNOWN
	}
	return d.temperature, nvml.SUCCESS
}

func (d *mockDevice) GetPcieThroughput(counter nvml.PcieUtilCounter) (uint32, nvml.Return) {
	if d.failOn == "GetPcieThroughput" {
		return 0, nvml.ERROR_UNKNOWN
	}
	if counter == nvml.PCIE_UTIL_TX_BYTES {
		return d.pcieTx, nvml.SUCCESS
	}
	return d.pcieRx, nvml.SUCCESS
}

// mockNVML implements NVMLLibrary backed by in-memory mock devices.
type mockNVML struct {
	devices    []*mockDevice
	initFail   bool
	shutFail   bool
	countFail  bool
	handleFail int // index to fail on; -1 = none
}

func (m *mockNVML) Init() nvml.Return {
	if m.initFail {
		return nvml.ERROR_UNKNOWN
	}
	return nvml.SUCCESS
}

func (m *mockNVML) Shutdown() nvml.Return {
	if m.shutFail {
		return nvml.ERROR_UNKNOWN
	}
	return nvml.SUCCESS
}

func (m *mockNVML) DeviceGetCount() (int, nvml.Return) {
	if m.countFail {
		return 0, nvml.ERROR_UNKNOWN
	}
	return len(m.devices), nvml.SUCCESS
}

func (m *mockNVML) DeviceGetHandleByIndex(index int) (NVMLDevice, nvml.Return) {
	if index == m.handleFail {
		return nil, nvml.ERROR_UNKNOWN
	}
	if index < 0 || index >= len(m.devices) {
		return nil, nvml.ERROR_INVALID_ARGUMENT
	}
	return m.devices[index], nvml.SUCCESS
}

func newMockLib(devices ...*mockDevice) *mockNVML {
	return &mockNVML{devices: devices, handleFail: -1}
}

func newTestDevice(uuid, name string) *mockDevice {
	return &mockDevice{
		uuid:        uuid,
		name:        name,
		utilization: nvml.Utilization{Gpu: 73, Memory: 45},
		memory:      nvml.Memory{Total: 80 * 1024 * 1024 * 1024, Used: 54 * 1024 * 1024 * 1024, Free: 26 * 1024 * 1024 * 1024},
		powerMW:     350000, // 350 W
		temperature: 62,
		pcieTx:      500000,
		pcieRx:      250000,
	}
}

func TestCollectSingleGPU(t *testing.T) {
	dev := newTestDevice("GPU-abc123", "NVIDIA H100 80GB HBM3")
	lib := newMockLib(dev)

	c, err := NewNVMLCollectorWithLib(lib)
	if err != nil {
		t.Fatalf("unexpected init error: %v", err)
	}
	defer c.Close()

	metrics, err := c.Collect()
	if err != nil {
		t.Fatalf("unexpected collect error: %v", err)
	}

	if len(metrics) != 1 {
		t.Fatalf("expected 1 GPU, got %d", len(metrics))
	}

	m := metrics[0]
	if m.UUID != "GPU-abc123" {
		t.Errorf("uuid = %q, want %q", m.UUID, "GPU-abc123")
	}
	if m.DeviceName != "NVIDIA H100 80GB HBM3" {
		t.Errorf("name = %q, want %q", m.DeviceName, "NVIDIA H100 80GB HBM3")
	}
	if m.Index != 0 {
		t.Errorf("index = %d, want 0", m.Index)
	}
	if m.UtilizationPercent != 73 {
		t.Errorf("utilization = %d, want 73", m.UtilizationPercent)
	}
	if m.MemoryUsedBytes != 54*1024*1024*1024 {
		t.Errorf("memory used = %d, want %d", m.MemoryUsedBytes, 54*1024*1024*1024)
	}
	if m.MemoryTotalBytes != 80*1024*1024*1024 {
		t.Errorf("memory total = %d, want %d", m.MemoryTotalBytes, 80*1024*1024*1024)
	}
	if m.PowerDrawWatts != 350.0 {
		t.Errorf("power = %f, want 350.0", m.PowerDrawWatts)
	}
	if m.TemperatureCelsius != 62 {
		t.Errorf("temp = %d, want 62", m.TemperatureCelsius)
	}
	if m.PCIeTxBytesPerSec != 500000 {
		t.Errorf("pcie tx = %d, want 500000", m.PCIeTxBytesPerSec)
	}
	if m.PCIeRxBytesPerSec != 250000 {
		t.Errorf("pcie rx = %d, want 250000", m.PCIeRxBytesPerSec)
	}
	if m.Timestamp.IsZero() {
		t.Error("timestamp should not be zero")
	}
}

func TestCollectMultipleGPUs(t *testing.T) {
	lib := newMockLib(
		newTestDevice("GPU-0001", "NVIDIA A100"),
		newTestDevice("GPU-0002", "NVIDIA A100"),
		newTestDevice("GPU-0003", "NVIDIA A100"),
	)
	// Give each device different utilization.
	lib.devices[0].utilization.Gpu = 10
	lib.devices[1].utilization.Gpu = 50
	lib.devices[2].utilization.Gpu = 90

	c, err := NewNVMLCollectorWithLib(lib)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	defer c.Close()

	metrics, err := c.Collect()
	if err != nil {
		t.Fatalf("collect: %v", err)
	}

	if len(metrics) != 3 {
		t.Fatalf("expected 3 GPUs, got %d", len(metrics))
	}

	expectedUtil := []uint32{10, 50, 90}
	for i, m := range metrics {
		if m.UtilizationPercent != expectedUtil[i] {
			t.Errorf("gpu %d utilization = %d, want %d", i, m.UtilizationPercent, expectedUtil[i])
		}
		if m.Index != i {
			t.Errorf("gpu %d index = %d, want %d", i, m.Index, i)
		}
	}
}

func TestInitFailure(t *testing.T) {
	lib := newMockLib()
	lib.initFail = true

	_, err := NewNVMLCollectorWithLib(lib)
	if err == nil {
		t.Fatal("expected init error, got nil")
	}
}

func TestCloseFailure(t *testing.T) {
	lib := newMockLib()
	lib.shutFail = true

	c, err := NewNVMLCollectorWithLib(lib)
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	if err := c.Close(); err == nil {
		t.Fatal("expected close error, got nil")
	}
}

func TestDeviceGetCountFailure(t *testing.T) {
	lib := newMockLib()
	lib.countFail = true

	c, err := NewNVMLCollectorWithLib(lib)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	defer c.Close()

	_, err = c.Collect()
	if err == nil {
		t.Fatal("expected collect error, got nil")
	}
}

func TestDeviceHandleFailure(t *testing.T) {
	lib := newMockLib(newTestDevice("GPU-0001", "H100"))
	lib.handleFail = 0

	c, err := NewNVMLCollectorWithLib(lib)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	defer c.Close()

	_, err = c.Collect()
	if err == nil {
		t.Fatal("expected collect error on handle failure")
	}
}

func TestDeviceMethodFailures(t *testing.T) {
	methods := []string{
		"GetUUID",
		"GetName",
		"GetUtilizationRates",
		"GetMemoryInfo",
		"GetPowerUsage",
		"GetTemperature",
		"GetPcieThroughput",
	}

	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			dev := newTestDevice("GPU-fail", "broken")
			dev.failOn = method
			lib := newMockLib(dev)

			c, err := NewNVMLCollectorWithLib(lib)
			if err != nil {
				t.Fatalf("init: %v", err)
			}
			defer c.Close()

			_, err = c.Collect()
			if err == nil {
				t.Fatalf("expected error when %s fails", method)
			}
		})
	}
}

func TestCollectorImplementsInterface(t *testing.T) {
	// Compile-time check that NVMLCollector satisfies GPUCollector.
	var _ GPUCollector = (*NVMLCollector)(nil)
}

func TestPowerConversion(t *testing.T) {
	dev := newTestDevice("GPU-pwr", "H100")
	dev.powerMW = 123456 // 123.456 W
	lib := newMockLib(dev)

	c, err := NewNVMLCollectorWithLib(lib)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	defer c.Close()

	metrics, err := c.Collect()
	if err != nil {
		t.Fatalf("collect: %v", err)
	}

	want := 123.456
	if metrics[0].PowerDrawWatts != want {
		t.Errorf("power = %f, want %f", metrics[0].PowerDrawWatts, want)
	}
}
