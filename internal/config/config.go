package config

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

// Config captures the runtime settings for the agent.
type Config struct {
	ClusterID             string            `yaml:"clusterId"`
	ClusterName           string            `yaml:"clusterName"`
	ListenAddr            string            `yaml:"listenAddr"`
	LogLevel              string            `yaml:"logLevel"`
	ScrapeIntervalSeconds int               `yaml:"scrapeIntervalSeconds"`
	KubeconfigPath        string            `yaml:"kubeconfig"`
	Pricing               PricingConfig     `yaml:"pricing"`
	Environment           EnvironmentConfig `yaml:"environment"`
}

// PricingConfig represents the simplified pricing inputs for cost calculations.
type PricingConfig struct {
	Provider              string             `yaml:"provider"`
	Region                string             `yaml:"region"`
	CPUCoreHourPriceUSD   float64            `yaml:"cpuHourPrice"`
	MemoryGiBHourPriceUSD float64            `yaml:"memoryGibHourPrice"`
	InstancePrices        map[string]float64 `yaml:"instancePrices"`
	DefaultNodeHourlyUSD  float64            `yaml:"defaultNodeHourlyUSD"`
	AWS                   AWSPricingConfig   `yaml:"aws"`
}

// AWSPricingConfig holds simple instance type pricing overrides.
type AWSPricingConfig struct {
	// NodePrices maps region -> instanceType -> hourly price in USD.
	NodePrices map[string]map[string]float64 `yaml:"nodePrices"`
}

// EnvironmentConfig holds heuristics for namespace classification.
type EnvironmentConfig struct {
	LabelKeys              []string `yaml:"labelKeys"`
	ProductionLabelValues  []string `yaml:"productionLabelValues"`
	NonProdLabelValues     []string `yaml:"nonProdLabelValues"`
	SystemLabelValues      []string `yaml:"systemLabelValues"`
	ProductionNameContains []string `yaml:"productionNameContains"`
	SystemNamespaces       []string `yaml:"systemNamespaces"`
}

// DefaultConfig returns sane defaults for the agent.
func DefaultConfig() Config {
	return Config{
		ClusterID:             "",
		ClusterName:           "kubernetes",
		ListenAddr:            ":8080",
		LogLevel:              "info",
		ScrapeIntervalSeconds: 60,
		Pricing: PricingConfig{
			Provider:              "aws",
			Region:                "us-east-1",
			CPUCoreHourPriceUSD:   0.046, // legacy fields used by the Prometheus exporter
			MemoryGiBHourPriceUSD: 0.005,
			DefaultNodeHourlyUSD:  0.1,
			AWS: AWSPricingConfig{
				NodePrices: copyNodePrices(defaultAWSNodePrices()),
			},
		},
		Environment: EnvironmentConfig{
			LabelKeys: []string{"clustercost.io/environment"},
			ProductionLabelValues: []string{
				"production", "prod",
			},
			NonProdLabelValues: []string{
				"nonprod", "staging", "dev", "test",
			},
			SystemLabelValues: []string{
				"system",
			},
			ProductionNameContains: []string{"prod"},
			SystemNamespaces: []string{
				"kube-system",
				"monitoring",
				"logging",
				"ingress",
				"istio-system",
				"linkerd",
				"cert-manager",
			},
		},
	}
}

// ScrapeInterval returns the configured interval in duration units.
func (c Config) ScrapeInterval() time.Duration {
	if c.ScrapeIntervalSeconds <= 0 {
		return 60 * time.Second
	}
	return time.Duration(c.ScrapeIntervalSeconds) * time.Second
}

// Load builds the configuration by merging defaults, file, environment, and flags.
func Load() (Config, error) {
	cfg := DefaultConfig()

	// Step 1: optional config file
	configFile := envOrDefault("CLUSTERCOST_CONFIG_FILE", "")

	fs := flag.NewFlagSet("clustercost-agent-k8s", flag.ContinueOnError)
	fs.StringVar(&configFile, "config", configFile, "Path to YAML config file")
	fs.StringVar(&cfg.ClusterID, "cluster-id", cfg.ClusterID, "Logical cluster identifier")
	fs.StringVar(&cfg.ClusterName, "cluster-name", cfg.ClusterName, "Legacy cluster name (alias for cluster-id)")
	fs.StringVar(&cfg.ListenAddr, "listen-addr", cfg.ListenAddr, "HTTP listen address")
	fs.StringVar(&cfg.LogLevel, "log-level", cfg.LogLevel, "Log level (debug, info, warn, error)")
	fs.IntVar(&cfg.ScrapeIntervalSeconds, "scrape-interval", cfg.ScrapeIntervalSeconds, "Scrape interval in seconds")
	fs.StringVar(&cfg.KubeconfigPath, "kubeconfig", cfg.KubeconfigPath, "Path to kubeconfig (optional)")
	fs.StringVar(&cfg.Pricing.Provider, "pricing-provider", cfg.Pricing.Provider, "Pricing provider identifier")
	fs.StringVar(&cfg.Pricing.Region, "pricing-region", cfg.Pricing.Region, "Pricing region")
	fs.Float64Var(&cfg.Pricing.CPUCoreHourPriceUSD, "cpu-price", cfg.Pricing.CPUCoreHourPriceUSD, "CPU core hour price in USD")
	fs.Float64Var(&cfg.Pricing.MemoryGiBHourPriceUSD, "memory-price", cfg.Pricing.MemoryGiBHourPriceUSD, "Memory GiB hour price in USD")

	if err := fs.Parse(os.Args[1:]); err != nil { // flag set already prints errors
		return Config{}, err
	}

	if configFile != "" {
		if err := loadFromFile(configFile, &cfg); err != nil {
			return Config{}, err
		}
	}

	// Apply env overrides after file load so that env > file.
	applyEnvOverrides(&cfg)

	if cfg.ClusterID == "" {
		cfg.ClusterID = cfg.ClusterName
	}
	if cfg.ClusterName == "" {
		cfg.ClusterName = cfg.ClusterID
	}

	// Flags already parsed into cfg before file/env to honor precedence order: env > file > flags would
	// be counter intuitive for operators, so we accept flags as ultimate override.

	if cfg.Pricing.CPUCoreHourPriceUSD < 0 || cfg.Pricing.MemoryGiBHourPriceUSD < 0 {
		return Config{}, errors.New("pricing values must be non-negative")
	}
	if cfg.Pricing.DefaultNodeHourlyUSD < 0 {
		return Config{}, errors.New("default node hourly price must be non-negative")
	}

	if cfg.ScrapeIntervalSeconds < 5 {
		cfg.ScrapeIntervalSeconds = 5
	}

	return cfg, nil
}

func loadFromFile(path string, cfg *Config) error {
	data, err := os.ReadFile(path) // #nosec G304 -- path provided by cluster operator
	if err != nil {
		return fmt.Errorf("read config file: %w", err)
	}

	type fileConfig Config
	var fileCfg fileConfig
	if err := yaml.Unmarshal(data, &fileCfg); err != nil {
		return fmt.Errorf("parse config file: %w", err)
	}

	mergeConfigs(cfg, Config(fileCfg))
	return nil
}

func mergeConfigs(base *Config, override Config) {
	if override.ClusterID != "" {
		base.ClusterID = override.ClusterID
	}
	if override.ClusterName != "" {
		base.ClusterName = override.ClusterName
	}
	if override.ListenAddr != "" {
		base.ListenAddr = override.ListenAddr
	}
	if override.LogLevel != "" {
		base.LogLevel = override.LogLevel
	}
	if override.ScrapeIntervalSeconds != 0 {
		base.ScrapeIntervalSeconds = override.ScrapeIntervalSeconds
	}
	if override.KubeconfigPath != "" {
		base.KubeconfigPath = override.KubeconfigPath
	}
	if override.Pricing.Provider != "" {
		base.Pricing.Provider = override.Pricing.Provider
	}
	if override.Pricing.Region != "" {
		base.Pricing.Region = override.Pricing.Region
	}
	if override.Pricing.CPUCoreHourPriceUSD != 0 {
		base.Pricing.CPUCoreHourPriceUSD = override.Pricing.CPUCoreHourPriceUSD
	}
	if override.Pricing.MemoryGiBHourPriceUSD != 0 {
		base.Pricing.MemoryGiBHourPriceUSD = override.Pricing.MemoryGiBHourPriceUSD
	}
	if override.Pricing.DefaultNodeHourlyUSD != 0 {
		base.Pricing.DefaultNodeHourlyUSD = override.Pricing.DefaultNodeHourlyUSD
	}
	if override.Pricing.InstancePrices != nil {
		if base.Pricing.InstancePrices == nil {
			base.Pricing.InstancePrices = map[string]float64{}
		}
		for k, v := range override.Pricing.InstancePrices {
			base.Pricing.InstancePrices[k] = v
		}
	}
	if override.Pricing.AWS.NodePrices != nil {
		if base.Pricing.AWS.NodePrices == nil {
			base.Pricing.AWS.NodePrices = map[string]map[string]float64{}
		}
		for region, instances := range override.Pricing.AWS.NodePrices {
			if _, ok := base.Pricing.AWS.NodePrices[region]; !ok {
				base.Pricing.AWS.NodePrices[region] = map[string]float64{}
			}
			for instance, price := range instances {
				base.Pricing.AWS.NodePrices[region][instance] = price
			}
		}
	}
	mergeEnvironmentConfig(&base.Environment, override.Environment)
}

func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("CLUSTERCOST_CLUSTER_ID"); v != "" {
		cfg.ClusterID = v
	}
	if v := os.Getenv("CLUSTERCOST_CLUSTER_NAME"); v != "" {
		cfg.ClusterName = v
	}
	if v := os.Getenv("CLUSTERCOST_LISTEN_ADDR"); v != "" {
		cfg.ListenAddr = v
	}
	if v := os.Getenv("CLUSTERCOST_LOG_LEVEL"); v != "" {
		cfg.LogLevel = v
	}
	if v := os.Getenv("CLUSTERCOST_SCRAPE_INTERVAL"); v != "" {
		if iv, err := strconv.Atoi(v); err == nil {
			cfg.ScrapeIntervalSeconds = iv
		}
	}
	if v := os.Getenv("CLUSTERCOST_KUBECONFIG"); v != "" {
		cfg.KubeconfigPath = v
	}
	if v := os.Getenv("CLUSTERCOST_PROVIDER"); v != "" {
		cfg.Pricing.Provider = v
	}
	if v := os.Getenv("CLUSTERCOST_REGION"); v != "" {
		cfg.Pricing.Region = v
	}
	if v := os.Getenv("CLUSTERCOST_CPU_HOUR_PRICE"); v != "" {
		if fv, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Pricing.CPUCoreHourPriceUSD = fv
		}
	}
	if v := os.Getenv("CLUSTERCOST_MEMORY_GIB_HOUR_PRICE"); v != "" {
		if fv, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Pricing.MemoryGiBHourPriceUSD = fv
		}
	}
	if v := os.Getenv("CLUSTERCOST_DEFAULT_NODE_PRICE"); v != "" {
		if fv, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Pricing.DefaultNodeHourlyUSD = fv
		}
	}
	if v := os.Getenv("CLUSTERCOST_INSTANCE_PRICES"); v != "" {
		if parsed, err := parseInstancePrices(v); err == nil {
			cfg.Pricing.InstancePrices = parsed
		}
	}
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func copyNodePrices(src map[string]map[string]float64) map[string]map[string]float64 {
	if src == nil {
		return nil
	}
	dst := make(map[string]map[string]float64, len(src))
	for region, instances := range src {
		instCopy := make(map[string]float64, len(instances))
		for it, price := range instances {
			instCopy[it] = price
		}
		dst[region] = instCopy
	}
	return dst
}

func parseInstancePrices(raw string) (map[string]float64, error) {
	if raw == "" {
		return nil, nil
	}
	var parsed map[string]float64
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil, err
	}
	return parsed, nil
}

func mergeEnvironmentConfig(base *EnvironmentConfig, override EnvironmentConfig) {
	if len(override.LabelKeys) > 0 {
		base.LabelKeys = append([]string{}, override.LabelKeys...)
	}
	if len(override.ProductionLabelValues) > 0 {
		base.ProductionLabelValues = append([]string{}, override.ProductionLabelValues...)
	}
	if len(override.NonProdLabelValues) > 0 {
		base.NonProdLabelValues = append([]string{}, override.NonProdLabelValues...)
	}
	if len(override.SystemLabelValues) > 0 {
		base.SystemLabelValues = append([]string{}, override.SystemLabelValues...)
	}
	if len(override.ProductionNameContains) > 0 {
		base.ProductionNameContains = append([]string{}, override.ProductionNameContains...)
	}
	if len(override.SystemNamespaces) > 0 {
		base.SystemNamespaces = append([]string{}, override.SystemNamespaces...)
	}
}
