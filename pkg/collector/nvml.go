package collector

import (
	"fmt"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/nickvecchioni/ballast/pkg/models"
)

// GPUCollector reads GPU metrics from the host.
type GPUCollector interface {
	// Collect returns a snapshot of metrics for every GPU on the node.
	Collect() ([]models.GPUMetrics, error)
	// Close releases any resources held by the collector.
	Close() error
}

// NVMLDevice abstracts the subset of nvml.Device methods we use,
// making the real NVML library mockable in tests.
type NVMLDevice interface {
	GetUUID() (string, nvml.Return)
	GetName() (string, nvml.Return)
	GetUtilizationRates() (nvml.Utilization, nvml.Return)
	GetMemoryInfo() (nvml.Memory, nvml.Return)
	GetPowerUsage() (uint32, nvml.Return)
	GetTemperature(nvml.TemperatureSensors) (uint32, nvml.Return)
	GetPcieThroughput(nvml.PcieUtilCounter) (uint32, nvml.Return)
}

// NVMLLibrary abstracts the package-level NVML functions so the
// collector can be tested without a real GPU.
type NVMLLibrary interface {
	Init() nvml.Return
	Shutdown() nvml.Return
	DeviceGetCount() (int, nvml.Return)
	DeviceGetHandleByIndex(index int) (NVMLDevice, nvml.Return)
}

// realNVML delegates to the actual go-nvml package functions.
type realNVML struct{}

func (r *realNVML) Init() nvml.Return                 { return nvml.Init() }
func (r *realNVML) Shutdown() nvml.Return              { return nvml.Shutdown() }
func (r *realNVML) DeviceGetCount() (int, nvml.Return) { return nvml.DeviceGetCount() }

func (r *realNVML) DeviceGetHandleByIndex(index int) (NVMLDevice, nvml.Return) {
	dev, ret := nvml.DeviceGetHandleByIndex(index)
	if ret != nvml.SUCCESS {
		return nil, ret
	}
	return dev, ret
}

// NVMLCollector implements GPUCollector using the NVIDIA NVML library.
type NVMLCollector struct {
	lib NVMLLibrary
}

// NewNVMLCollector initialises NVML and returns a ready collector.
func NewNVMLCollector() (*NVMLCollector, error) {
	return NewNVMLCollectorWithLib(&realNVML{})
}

// NewNVMLCollectorWithLib creates a collector backed by the provided
// NVMLLibrary, useful for injecting a mock in tests.
func NewNVMLCollectorWithLib(lib NVMLLibrary) (*NVMLCollector, error) {
	if ret := lib.Init(); ret != nvml.SUCCESS {
		return nil, fmt.Errorf("nvml init: %v", nvml.ErrorString(ret))
	}
	return &NVMLCollector{lib: lib}, nil
}

// Collect reads metrics from every GPU visible on the node.
func (c *NVMLCollector) Collect() ([]models.GPUMetrics, error) {
	count, ret := c.lib.DeviceGetCount()
	if ret != nvml.SUCCESS {
		return nil, fmt.Errorf("nvml device count: %v", nvml.ErrorString(ret))
	}

	now := time.Now()
	metrics := make([]models.GPUMetrics, 0, count)

	for i := 0; i < count; i++ {
		m, err := c.collectDevice(i, now)
		if err != nil {
			return nil, fmt.Errorf("gpu %d: %w", i, err)
		}
		metrics = append(metrics, m)
	}
	return metrics, nil
}

func (c *NVMLCollector) collectDevice(index int, ts time.Time) (models.GPUMetrics, error) {
	dev, ret := c.lib.DeviceGetHandleByIndex(index)
	if ret != nvml.SUCCESS {
		return models.GPUMetrics{}, fmt.Errorf("get handle: %v", nvml.ErrorString(ret))
	}

	uuid, ret := dev.GetUUID()
	if ret != nvml.SUCCESS {
		return models.GPUMetrics{}, fmt.Errorf("get uuid: %v", nvml.ErrorString(ret))
	}

	name, ret := dev.GetName()
	if ret != nvml.SUCCESS {
		return models.GPUMetrics{}, fmt.Errorf("get name: %v", nvml.ErrorString(ret))
	}

	util, ret := dev.GetUtilizationRates()
	if ret != nvml.SUCCESS {
		return models.GPUMetrics{}, fmt.Errorf("get utilization: %v", nvml.ErrorString(ret))
	}

	mem, ret := dev.GetMemoryInfo()
	if ret != nvml.SUCCESS {
		return models.GPUMetrics{}, fmt.Errorf("get memory info: %v", nvml.ErrorString(ret))
	}

	powerMW, ret := dev.GetPowerUsage()
	if ret != nvml.SUCCESS {
		return models.GPUMetrics{}, fmt.Errorf("get power usage: %v", nvml.ErrorString(ret))
	}

	temp, ret := dev.GetTemperature(nvml.TEMPERATURE_GPU)
	if ret != nvml.SUCCESS {
		return models.GPUMetrics{}, fmt.Errorf("get temperature: %v", nvml.ErrorString(ret))
	}

	pcieTx, ret := dev.GetPcieThroughput(nvml.PCIE_UTIL_TX_BYTES)
	if ret != nvml.SUCCESS {
		return models.GPUMetrics{}, fmt.Errorf("get pcie tx: %v", nvml.ErrorString(ret))
	}

	pcieRx, ret := dev.GetPcieThroughput(nvml.PCIE_UTIL_RX_BYTES)
	if ret != nvml.SUCCESS {
		return models.GPUMetrics{}, fmt.Errorf("get pcie rx: %v", nvml.ErrorString(ret))
	}

	return models.GPUMetrics{
		UUID:               uuid,
		DeviceName:         name,
		Index:              index,
		UtilizationPercent: util.Gpu,
		MemoryUsedBytes:    mem.Used,
		MemoryTotalBytes:   mem.Total,
		PowerDrawWatts:     float64(powerMW) / 1000.0, // mW → W
		TemperatureCelsius: temp,
		PCIeTxBytesPerSec:  pcieTx,
		PCIeRxBytesPerSec:  pcieRx,
		Timestamp:          ts,
	}, nil
}

// Close shuts down the NVML library.
func (c *NVMLCollector) Close() error {
	if ret := c.lib.Shutdown(); ret != nvml.SUCCESS {
		return fmt.Errorf("nvml shutdown: %v", nvml.ErrorString(ret))
	}
	return nil
}
