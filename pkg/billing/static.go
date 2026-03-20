package billing

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

const defaultCostPerHourUSD = 1.00

// PricingProvider returns the cost per hour for a given GPU type.
type PricingProvider interface {
	// GetCostPerHour returns the hourly cost in USD for the named GPU type.
	// The gpuType string is the value returned by NVML DeviceGetName
	// (e.g. "NVIDIA H100 80GB HBM3").
	GetCostPerHour(gpuType string) float64
}

// pricingFile is the on-disk YAML structure mounted from the ConfigMap.
type pricingFile struct {
	GPUTypes map[string]gpuPricing `yaml:"gpu_types"`
}

type gpuPricing struct {
	CostPerHourUSD float64 `yaml:"cost_per_hour_usd"`
}

// StaticPricingProvider implements PricingProvider using a YAML config file.
type StaticPricingProvider struct {
	// prices maps normalised GPU type name → cost per hour USD.
	prices       map[string]float64
	defaultPrice float64
}

// StaticPricingOpts configures a StaticPricingProvider.
type StaticPricingOpts struct {
	// DefaultPrice is returned when a GPU type has no explicit entry.
	// Zero means use the package-level default ($1.00/hr).
	DefaultPrice float64
}

// NewStaticPricingFromFile loads pricing from a YAML file (typically
// ConfigMap-mounted at /etc/infracost/pricing.yaml).
func NewStaticPricingFromFile(path string, opts StaticPricingOpts) (*StaticPricingProvider, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read pricing file %s: %w", path, err)
	}
	return NewStaticPricingFromBytes(data, opts)
}

// NewStaticPricingFromBytes parses pricing from raw YAML bytes.
func NewStaticPricingFromBytes(data []byte, opts StaticPricingOpts) (*StaticPricingProvider, error) {
	var pf pricingFile
	if err := yaml.Unmarshal(data, &pf); err != nil {
		return nil, fmt.Errorf("parse pricing yaml: %w", err)
	}

	defPrice := opts.DefaultPrice
	if defPrice == 0 {
		defPrice = defaultCostPerHourUSD
	}

	prices := make(map[string]float64, len(pf.GPUTypes))
	for name, gp := range pf.GPUTypes {
		prices[normalise(name)] = gp.CostPerHourUSD
	}

	return &StaticPricingProvider{
		prices:       prices,
		defaultPrice: defPrice,
	}, nil
}

// GetCostPerHour returns the configured hourly USD cost for gpuType.
// Falls back to the default price if the type is not found.
func (p *StaticPricingProvider) GetCostPerHour(gpuType string) float64 {
	if cost, ok := p.prices[normalise(gpuType)]; ok {
		return cost
	}
	return p.defaultPrice
}

// normalise converts a GPU type string to a canonical form so that
// config keys ("NVIDIA-H100-SXM5-80GB") match NVML names
// ("NVIDIA H100 SXM5 80GB") regardless of delimiters or case.
func normalise(s string) string {
	s = strings.ToUpper(s)
	s = strings.ReplaceAll(s, "-", " ")
	s = strings.ReplaceAll(s, "_", " ")
	// Collapse multiple spaces.
	fields := strings.Fields(s)
	return strings.Join(fields, " ")
}
