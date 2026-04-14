// Standalone NVML test — validates GPU metric collection without Kubernetes.
// Usage: go run hack/test-nvml.go
package main

import (
	"fmt"
	"os"

	"github.com/nickvecchioni/ballast/pkg/collector"
)

func main() {
	fmt.Println("=== Ballast NVML Test ===")
	fmt.Println()

	c, err := collector.NewNVMLCollector()
	if err != nil {
		fmt.Fprintf(os.Stderr, "NVML init failed: %v\n", err)
		fmt.Fprintln(os.Stderr, "Make sure NVIDIA drivers are installed and nvidia-smi works.")
		os.Exit(1)
	}
	defer c.Close()

	metrics, err := c.Collect()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Collect failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Found %d GPU(s):\n\n", len(metrics))
	for _, m := range metrics {
		fmt.Printf("  GPU %d: %s\n", m.Index, m.DeviceName)
		fmt.Printf("    UUID:          %s\n", m.UUID)
		fmt.Printf("    Utilization:   %d%%\n", m.UtilizationPercent)
		fmt.Printf("    Memory:        %.1f / %.1f GB (%.0f%%)\n",
			float64(m.MemoryUsedBytes)/1e9,
			float64(m.MemoryTotalBytes)/1e9,
			float64(m.MemoryUsedBytes)/float64(m.MemoryTotalBytes)*100)
		fmt.Printf("    Power:         %.1f W\n", m.PowerDrawWatts)
		fmt.Printf("    Temperature:   %d C\n", m.TemperatureCelsius)
		fmt.Printf("    PCIe TX:       %d KB/s\n", m.PCIeTxBytesPerSec)
		fmt.Printf("    PCIe RX:       %d KB/s\n", m.PCIeRxBytesPerSec)
		fmt.Println()
	}

	fmt.Println("NVML collection OK")
}
