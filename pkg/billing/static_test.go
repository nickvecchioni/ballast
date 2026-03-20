package billing

import (
	"os"
	"path/filepath"
	"testing"
)

const testYAML = `
gpu_types:
  NVIDIA-H100-SXM5-80GB:
    cost_per_hour_usd: 3.90
  NVIDIA-A100-SXM4-80GB:
    cost_per_hour_usd: 1.80
  NVIDIA-L4:
    cost_per_hour_usd: 0.65
`

func mustProvider(t *testing.T, yaml string, opts StaticPricingOpts) *StaticPricingProvider {
	t.Helper()
	p, err := NewStaticPricingFromBytes([]byte(yaml), opts)
	if err != nil {
		t.Fatalf("NewStaticPricingFromBytes: %v", err)
	}
	return p
}

func TestExactConfigKey(t *testing.T) {
	p := mustProvider(t, testYAML, StaticPricingOpts{})

	tests := []struct {
		key  string
		want float64
	}{
		{"NVIDIA-H100-SXM5-80GB", 3.90},
		{"NVIDIA-A100-SXM4-80GB", 1.80},
		{"NVIDIA-L4", 0.65},
	}
	for _, tt := range tests {
		if got := p.GetCostPerHour(tt.key); got != tt.want {
			t.Errorf("GetCostPerHour(%q) = %f, want %f", tt.key, got, tt.want)
		}
	}
}

func TestNVMLNameMatchesConfigKey(t *testing.T) {
	p := mustProvider(t, testYAML, StaticPricingOpts{})

	// NVML returns spaces where the config uses dashes.
	tests := []struct {
		nvmlName string
		want     float64
	}{
		{"NVIDIA H100 SXM5 80GB", 3.90},
		{"NVIDIA A100 SXM4 80GB", 1.80},
		{"NVIDIA L4", 0.65},
	}
	for _, tt := range tests {
		if got := p.GetCostPerHour(tt.nvmlName); got != tt.want {
			t.Errorf("GetCostPerHour(%q) = %f, want %f", tt.nvmlName, got, tt.want)
		}
	}
}

func TestCaseInsensitive(t *testing.T) {
	p := mustProvider(t, testYAML, StaticPricingOpts{})

	if got := p.GetCostPerHour("nvidia-h100-sxm5-80gb"); got != 3.90 {
		t.Errorf("lowercase lookup = %f, want 3.90", got)
	}
	if got := p.GetCostPerHour("Nvidia H100 SXM5 80GB"); got != 3.90 {
		t.Errorf("mixed case lookup = %f, want 3.90", got)
	}
}

func TestFallbackDefaultPrice(t *testing.T) {
	p := mustProvider(t, testYAML, StaticPricingOpts{})

	got := p.GetCostPerHour("NVIDIA RTX 4090")
	if got != defaultCostPerHourUSD {
		t.Errorf("unknown GPU = %f, want default %f", got, defaultCostPerHourUSD)
	}
}

func TestCustomDefaultPrice(t *testing.T) {
	p := mustProvider(t, testYAML, StaticPricingOpts{DefaultPrice: 2.50})

	got := p.GetCostPerHour("UNKNOWN-GPU")
	if got != 2.50 {
		t.Errorf("unknown GPU with custom default = %f, want 2.50", got)
	}
}

func TestEmptyGPUTypes(t *testing.T) {
	p := mustProvider(t, "gpu_types: {}", StaticPricingOpts{})

	got := p.GetCostPerHour("NVIDIA H100")
	if got != defaultCostPerHourUSD {
		t.Errorf("empty config = %f, want default %f", got, defaultCostPerHourUSD)
	}
}

func TestMissingGPUTypesKey(t *testing.T) {
	p := mustProvider(t, "other_key: 123", StaticPricingOpts{})

	got := p.GetCostPerHour("anything")
	if got != defaultCostPerHourUSD {
		t.Errorf("missing gpu_types = %f, want default %f", got, defaultCostPerHourUSD)
	}
}

func TestInvalidYAML(t *testing.T) {
	_, err := NewStaticPricingFromBytes([]byte(":::not yaml"), StaticPricingOpts{})
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pricing.yaml")
	if err := os.WriteFile(path, []byte(testYAML), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	p, err := NewStaticPricingFromFile(path, StaticPricingOpts{})
	if err != nil {
		t.Fatalf("NewStaticPricingFromFile: %v", err)
	}

	if got := p.GetCostPerHour("NVIDIA H100 SXM5 80GB"); got != 3.90 {
		t.Errorf("from file = %f, want 3.90", got)
	}
}

func TestLoadFromFileMissing(t *testing.T) {
	_, err := NewStaticPricingFromFile("/nonexistent/pricing.yaml", StaticPricingOpts{})
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestNormaliseUnderscores(t *testing.T) {
	yaml := `
gpu_types:
  NVIDIA_H100_80GB:
    cost_per_hour_usd: 3.90
`
	p := mustProvider(t, yaml, StaticPricingOpts{})

	// All of these should resolve to the same normalised form.
	variants := []string{
		"NVIDIA_H100_80GB",
		"NVIDIA-H100-80GB",
		"NVIDIA H100 80GB",
		"nvidia h100 80gb",
	}
	for _, v := range variants {
		if got := p.GetCostPerHour(v); got != 3.90 {
			t.Errorf("GetCostPerHour(%q) = %f, want 3.90", v, got)
		}
	}
}

func TestExtraWhitespace(t *testing.T) {
	p := mustProvider(t, testYAML, StaticPricingOpts{})

	if got := p.GetCostPerHour("  NVIDIA  H100  SXM5  80GB  "); got != 3.90 {
		t.Errorf("extra whitespace = %f, want 3.90", got)
	}
}

func TestImplementsInterface(t *testing.T) {
	var _ PricingProvider = (*StaticPricingProvider)(nil)
}

func TestZeroPriceEntry(t *testing.T) {
	yaml := `
gpu_types:
  FREE-GPU:
    cost_per_hour_usd: 0
`
	p := mustProvider(t, yaml, StaticPricingOpts{DefaultPrice: 5.00})

	// An explicit 0 in config should return 0, not the default.
	if got := p.GetCostPerHour("FREE-GPU"); got != 0 {
		t.Errorf("zero-price GPU = %f, want 0", got)
	}
}
