package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMergesFileAndDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	cfgFile := filepath.Join(tmpDir, "config.yaml")
	err := os.WriteFile(cfgFile, []byte(`
clusterName: file-cluster
scrapeIntervalSeconds: 10
`), 0o644)
	if err != nil {
		t.Fatalf("write config file: %v", err)
	}

	t.Setenv("CLUSTERCOST_CONFIG_FILE", cfgFile)
	origArgs := os.Args
	os.Args = []string{"test-binary"}
	defer func() { os.Args = origArgs }()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.ClusterName != "file-cluster" {
		t.Fatalf("expected file cluster name, got %s", cfg.ClusterName)
	}
	if cfg.ScrapeIntervalSeconds != 10 {
		t.Fatalf("expected scrape interval 10, got %d", cfg.ScrapeIntervalSeconds)
	}
}

func TestLoadRespectsExplicitNetworkDisable(t *testing.T) {
	tmpDir := t.TempDir()
	cfgFile := filepath.Join(tmpDir, "config.yaml")
	err := os.WriteFile(cfgFile, []byte(`
network:
  enabled: false
  detailed: false
  dnsCapture: false
metrics:
  enabled: false
`), 0o644)
	if err != nil {
		t.Fatalf("write config file: %v", err)
	}

	t.Setenv("CLUSTERCOST_CONFIG_FILE", cfgFile)
	origArgs := os.Args
	os.Args = []string{"test-binary"}
	defer func() { os.Args = origArgs }()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Network.Enabled {
		t.Fatalf("expected network enabled=false, got true")
	}
	if cfg.Network.Detailed {
		t.Fatalf("expected network detailed=false, got true")
	}
	if cfg.Network.DNSCapture {
		t.Fatalf("expected dns capture=false, got true")
	}
	if cfg.Metrics.Enabled {
		t.Fatalf("expected metrics enabled=false, got true")
	}
}
