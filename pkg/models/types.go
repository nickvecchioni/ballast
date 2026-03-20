package models

import "time"

// GPUMetrics holds a single snapshot of metrics for one GPU.
type GPUMetrics struct {
	UUID               string
	DeviceName         string
	Index              int
	UtilizationPercent uint32 // 0-100
	MemoryUsedBytes    uint64
	MemoryTotalBytes   uint64
	PowerDrawWatts     float64
	TemperatureCelsius uint32
	PCIeTxBytesPerSec  uint32 // KB/s from NVML
	PCIeRxBytesPerSec  uint32 // KB/s from NVML
	Timestamp          time.Time
}
