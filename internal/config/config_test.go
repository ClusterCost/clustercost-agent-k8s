package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMergesFileEnvAndDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	cfgFile := filepath.Join(tmpDir, "config.yaml")
	err := os.WriteFile(cfgFile, []byte(`
clusterName: file-cluster
scrapeIntervalSeconds: 10
pricing:
  cpuHourPrice: 0.05
  memoryGibHourPrice: 0.01
`), 0o644)
	if err != nil {
		t.Fatalf("write config file: %v", err)
	}

	t.Setenv("CLUSTERCOST_CONFIG_FILE", cfgFile)
	t.Setenv("CLUSTERCOST_CLUSTER_NAME", "env-cluster")
	t.Setenv("CLUSTERCOST_CPU_HOUR_PRICE", "0.07")
	t.Setenv("CLUSTERCOST_MEMORY_GIB_HOUR_PRICE", "0.02")
	t.Setenv("CLUSTERCOST_SCRAPE_INTERVAL", "15")

	origArgs := os.Args
	os.Args = []string{"test-binary"}
	defer func() { os.Args = origArgs }()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.ClusterName != "env-cluster" {
		t.Fatalf("expected env cluster name, got %s", cfg.ClusterName)
	}
	if cfg.ClusterID != "env-cluster" {
		t.Fatalf("expected cluster ID synced with name, got %s", cfg.ClusterID)
	}
	if cfg.ScrapeIntervalSeconds != 15 {
		t.Fatalf("expected scrape interval 15, got %d", cfg.ScrapeIntervalSeconds)
	}
	if cfg.Pricing.CPUCoreHourPriceUSD != 0.07 {
		t.Fatalf("expected CPU price 0.07, got %f", cfg.Pricing.CPUCoreHourPriceUSD)
	}
	if cfg.Pricing.MemoryGiBHourPriceUSD != 0.02 {
		t.Fatalf("expected memory price 0.02, got %f", cfg.Pricing.MemoryGiBHourPriceUSD)
	}
}
