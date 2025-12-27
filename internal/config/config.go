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
	NodeName              string            `yaml:"nodeName"`
	ListenAddr            string            `yaml:"listenAddr"`
	LogLevel              string            `yaml:"logLevel"`
	ScrapeIntervalSeconds int               `yaml:"scrapeIntervalSeconds"`
	KubeconfigPath        string            `yaml:"kubeconfig"`
	Pricing               PricingConfig     `yaml:"pricing"`
	Network               NetworkConfig     `yaml:"network"`
	Metrics               MetricsConfig     `yaml:"metrics"`
	Remote                RemoteConfig      `yaml:"remote"`
	Environment           EnvironmentConfig `yaml:"environment"`
}

// PricingConfig represents the simplified pricing inputs for cost calculations.
type PricingConfig struct {
	Provider              string               `yaml:"provider"`
	Region                string               `yaml:"region"`
	CPUCoreHourPriceUSD   float64              `yaml:"cpuHourPrice"`
	MemoryGiBHourPriceUSD float64              `yaml:"memoryGibHourPrice"`
	InstancePrices        map[string]float64   `yaml:"instancePrices"`
	DefaultNodeHourlyUSD  float64              `yaml:"defaultNodeHourlyUSD"`
	AWS                   AWSPricingConfig     `yaml:"aws"`
	Network               NetworkPricingConfig `yaml:"network"`
}

// AWSPricingConfig holds simple instance type pricing overrides.
type AWSPricingConfig struct {
	// NodePrices maps region -> instanceType -> hourly price in USD.
	NodePrices map[string]map[string]float64 `yaml:"nodePrices"`
}

// NetworkPricingConfig captures per-class network egress prices.
type NetworkPricingConfig struct {
	DefaultEgressGiBPriceUSD float64            `yaml:"defaultEgressGiBPriceUSD"`
	EgressGiBPricesUSD       map[string]float64 `yaml:"egressGiBPricesUSD"`
}

// NetworkConfig configures network usage collection.
type NetworkConfig struct {
	Enabled    bool   `yaml:"enabled"`
	BPFMapPath string `yaml:"bpfMapPath"`
	ObjectPath string `yaml:"objectPath"`
	CgroupPath string `yaml:"cgroupPath"`
}

// MetricsConfig configures eBPF-based usage collection.
type MetricsConfig struct {
	Enabled    bool   `yaml:"enabled"`
	BPFMapPath string `yaml:"bpfMapPath"`
	CgroupRoot string `yaml:"cgroupRoot"`
	ObjectPath string `yaml:"objectPath"`
	CgroupPath string `yaml:"cgroupPath"`
}

// RemoteConfig configures forwarding snapshots to a central agent.
type RemoteConfig struct {
	Enabled       bool          `yaml:"enabled"`
	EndpointURL   string        `yaml:"endpointUrl"`
	AuthToken     string        `yaml:"authToken"`
	Timeout       time.Duration `yaml:"timeout"`
	QueueDir      string        `yaml:"queueDir"`
	FlushEvery    time.Duration `yaml:"flushEvery"`
	MaxBatch      int           `yaml:"maxBatch"`
	MaxRetries    int           `yaml:"maxRetries"`
	Backoff       time.Duration `yaml:"backoff"`
	MaxBatchBytes int64         `yaml:"maxBatchBytes"`
	MemoryBuffer  int           `yaml:"memoryBuffer"`
	GzipEnabled   bool          `yaml:"gzipEnabled"`
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
		NodeName:              "",
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
			Network: NetworkPricingConfig{
				DefaultEgressGiBPriceUSD: 0,
				EgressGiBPricesUSD:       map[string]float64{},
			},
		},
		Network: NetworkConfig{
			Enabled:    false,
			BPFMapPath: "/sys/fs/bpf/clustercost/flows",
			ObjectPath: "/opt/clustercost/bpf/flows.bpf.o",
			CgroupPath: "/sys/fs/cgroup",
		},
		Metrics: MetricsConfig{
			Enabled:    true,
			BPFMapPath: "/sys/fs/bpf/clustercost/metrics",
			CgroupRoot: "/sys/fs/cgroup",
			ObjectPath: "/opt/clustercost/bpf/metrics.bpf.o",
			CgroupPath: "/sys/fs/cgroup",
		},
		Remote: RemoteConfig{
			Enabled:       false,
			EndpointURL:   "",
			AuthToken:     "",
			Timeout:       5 * time.Second,
			QueueDir:      "/var/lib/clustercost/queue",
			FlushEvery:    5 * time.Second,
			MaxBatch:      50,
			MaxRetries:    5,
			Backoff:       10 * time.Second,
			MaxBatchBytes: 512 * 1024,
			MemoryBuffer:  200,
			GzipEnabled:   true,
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
	fs.StringVar(&cfg.NodeName, "node-name", cfg.NodeName, "Node name (for daemonset mode)")
	fs.StringVar(&cfg.ListenAddr, "listen-addr", cfg.ListenAddr, "HTTP listen address")
	fs.StringVar(&cfg.LogLevel, "log-level", cfg.LogLevel, "Log level (debug, info, warn, error)")
	fs.IntVar(&cfg.ScrapeIntervalSeconds, "scrape-interval", cfg.ScrapeIntervalSeconds, "Scrape interval in seconds")
	fs.StringVar(&cfg.KubeconfigPath, "kubeconfig", cfg.KubeconfigPath, "Path to kubeconfig (optional)")
	fs.StringVar(&cfg.Pricing.Provider, "pricing-provider", cfg.Pricing.Provider, "Pricing provider identifier")
	fs.StringVar(&cfg.Pricing.Region, "pricing-region", cfg.Pricing.Region, "Pricing region")
	fs.Float64Var(&cfg.Pricing.CPUCoreHourPriceUSD, "cpu-price", cfg.Pricing.CPUCoreHourPriceUSD, "CPU core hour price in USD")
	fs.Float64Var(&cfg.Pricing.MemoryGiBHourPriceUSD, "memory-price", cfg.Pricing.MemoryGiBHourPriceUSD, "Memory GiB hour price in USD")
	fs.BoolVar(&cfg.Network.Enabled, "enable-network-cost", cfg.Network.Enabled, "Enable eBPF-based network cost collection")
	fs.StringVar(&cfg.Network.BPFMapPath, "ebpf-map-path", cfg.Network.BPFMapPath, "Pinned eBPF map path for network flow stats")
	fs.StringVar(&cfg.Network.ObjectPath, "ebpf-net-object", cfg.Network.ObjectPath, "Path to eBPF network object file")
	fs.StringVar(&cfg.Network.CgroupPath, "ebpf-net-cgroup-path", cfg.Network.CgroupPath, "Cgroup path for eBPF network attachment")
	fs.Float64Var(&cfg.Pricing.Network.DefaultEgressGiBPriceUSD, "network-egress-price", cfg.Pricing.Network.DefaultEgressGiBPriceUSD, "Default network egress price per GiB in USD")
	fs.BoolVar(&cfg.Metrics.Enabled, "enable-ebpf-metrics", cfg.Metrics.Enabled, "Enable eBPF-based pod metrics collection")
	fs.StringVar(&cfg.Metrics.BPFMapPath, "ebpf-metrics-map-path", cfg.Metrics.BPFMapPath, "Pinned eBPF map path for pod metrics")
	fs.StringVar(&cfg.Metrics.CgroupRoot, "cgroup-root", cfg.Metrics.CgroupRoot, "Root path for cgroup v2 filesystem")
	fs.StringVar(&cfg.Metrics.ObjectPath, "ebpf-metrics-object", cfg.Metrics.ObjectPath, "Path to eBPF metrics object file")
	fs.StringVar(&cfg.Metrics.CgroupPath, "ebpf-metrics-cgroup-path", cfg.Metrics.CgroupPath, "Cgroup path for eBPF metrics attachment")
	fs.BoolVar(&cfg.Remote.Enabled, "remote-enabled", cfg.Remote.Enabled, "Enable sending snapshots to a central agent")
	fs.StringVar(&cfg.Remote.EndpointURL, "remote-endpoint", cfg.Remote.EndpointURL, "Central agent HTTP endpoint URL")
	fs.StringVar(&cfg.Remote.AuthToken, "remote-auth-token", cfg.Remote.AuthToken, "Bearer token for central agent")
	fs.DurationVar(&cfg.Remote.Timeout, "remote-timeout", cfg.Remote.Timeout, "Timeout for central agent requests")
	fs.StringVar(&cfg.Remote.QueueDir, "remote-queue-dir", cfg.Remote.QueueDir, "Disk queue directory for remote forwarding")
	fs.DurationVar(&cfg.Remote.FlushEvery, "remote-flush-every", cfg.Remote.FlushEvery, "Flush interval for remote forwarding")
	fs.IntVar(&cfg.Remote.MaxBatch, "remote-max-batch", cfg.Remote.MaxBatch, "Max reports per batch")
	fs.IntVar(&cfg.Remote.MaxRetries, "remote-max-retries", cfg.Remote.MaxRetries, "Max retries per batch")
	fs.DurationVar(&cfg.Remote.Backoff, "remote-backoff", cfg.Remote.Backoff, "Backoff before retrying a failed batch")
	fs.Int64Var(&cfg.Remote.MaxBatchBytes, "remote-max-batch-bytes", cfg.Remote.MaxBatchBytes, "Max payload size per batch in bytes")
	fs.IntVar(&cfg.Remote.MemoryBuffer, "remote-memory-buffer", cfg.Remote.MemoryBuffer, "In-memory buffer size before spooling to disk")
	fs.BoolVar(&cfg.Remote.GzipEnabled, "remote-gzip", cfg.Remote.GzipEnabled, "Enable gzip compression for batches")

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
	if cfg.Pricing.Network.DefaultEgressGiBPriceUSD < 0 {
		return Config{}, errors.New("network egress price must be non-negative")
	}
	for class, price := range cfg.Pricing.Network.EgressGiBPricesUSD {
		if price < 0 {
			return Config{}, fmt.Errorf("network egress price for class %s must be non-negative", class)
		}
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
	if override.NodeName != "" {
		base.NodeName = override.NodeName
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
	mergeNetworkPricingConfig(&base.Pricing.Network, override.Pricing.Network)
	mergeNetworkConfig(&base.Network, override.Network)
	mergeMetricsConfig(&base.Metrics, override.Metrics)
	mergeRemoteConfig(&base.Remote, override.Remote)
	mergeEnvironmentConfig(&base.Environment, override.Environment)
}

func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("CLUSTERCOST_CLUSTER_ID"); v != "" {
		cfg.ClusterID = v
	}
	if v := os.Getenv("CLUSTERCOST_CLUSTER_NAME"); v != "" {
		cfg.ClusterName = v
	}
	if v := os.Getenv("CLUSTERCOST_NODE_NAME"); v != "" {
		cfg.NodeName = v
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
	if v := os.Getenv("CLUSTERCOST_NETWORK_ENABLED"); v != "" {
		if bv, err := strconv.ParseBool(v); err == nil {
			cfg.Network.Enabled = bv
		}
	}
	if v := os.Getenv("CLUSTERCOST_EBPF_MAP_PATH"); v != "" {
		cfg.Network.BPFMapPath = v
	}
	if v := os.Getenv("CLUSTERCOST_EBPF_NET_OBJECT"); v != "" {
		cfg.Network.ObjectPath = v
	}
	if v := os.Getenv("CLUSTERCOST_EBPF_NET_CGROUP_PATH"); v != "" {
		cfg.Network.CgroupPath = v
	}
	if v := os.Getenv("CLUSTERCOST_NETWORK_EGRESS_GIB_PRICE"); v != "" {
		if fv, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Pricing.Network.DefaultEgressGiBPriceUSD = fv
		}
	}
	if v := os.Getenv("CLUSTERCOST_NETWORK_EGRESS_GIB_PRICES"); v != "" {
		if parsed, err := parseNetworkPrices(v); err == nil {
			cfg.Pricing.Network.EgressGiBPricesUSD = parsed
		}
	}
	if v := os.Getenv("CLUSTERCOST_EBPF_METRICS_ENABLED"); v != "" {
		if bv, err := strconv.ParseBool(v); err == nil {
			cfg.Metrics.Enabled = bv
		}
	}
	if v := os.Getenv("CLUSTERCOST_EBPF_METRICS_MAP_PATH"); v != "" {
		cfg.Metrics.BPFMapPath = v
	}
	if v := os.Getenv("CLUSTERCOST_CGROUP_ROOT"); v != "" {
		cfg.Metrics.CgroupRoot = v
	}
	if v := os.Getenv("CLUSTERCOST_EBPF_METRICS_OBJECT"); v != "" {
		cfg.Metrics.ObjectPath = v
	}
	if v := os.Getenv("CLUSTERCOST_EBPF_METRICS_CGROUP_PATH"); v != "" {
		cfg.Metrics.CgroupPath = v
	}
	if v := os.Getenv("CLUSTERCOST_REMOTE_ENABLED"); v != "" {
		if bv, err := strconv.ParseBool(v); err == nil {
			cfg.Remote.Enabled = bv
		}
	}
	if v := os.Getenv("CLUSTERCOST_REMOTE_ENDPOINT"); v != "" {
		cfg.Remote.EndpointURL = v
	}
	if v := os.Getenv("CLUSTERCOST_REMOTE_AUTH_TOKEN"); v != "" {
		cfg.Remote.AuthToken = v
	}
	if v := os.Getenv("CLUSTERCOST_REMOTE_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.Remote.Timeout = d
		}
	}
	if v := os.Getenv("CLUSTERCOST_REMOTE_QUEUE_DIR"); v != "" {
		cfg.Remote.QueueDir = v
	}
	if v := os.Getenv("CLUSTERCOST_REMOTE_FLUSH_EVERY"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.Remote.FlushEvery = d
		}
	}
	if v := os.Getenv("CLUSTERCOST_REMOTE_MAX_BATCH"); v != "" {
		if iv, err := strconv.Atoi(v); err == nil {
			cfg.Remote.MaxBatch = iv
		}
	}
	if v := os.Getenv("CLUSTERCOST_REMOTE_MAX_RETRIES"); v != "" {
		if iv, err := strconv.Atoi(v); err == nil {
			cfg.Remote.MaxRetries = iv
		}
	}
	if v := os.Getenv("CLUSTERCOST_REMOTE_BACKOFF"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.Remote.Backoff = d
		}
	}
	if v := os.Getenv("CLUSTERCOST_REMOTE_MAX_BATCH_BYTES"); v != "" {
		if iv, err := strconv.ParseInt(v, 10, 64); err == nil {
			cfg.Remote.MaxBatchBytes = iv
		}
	}
	if v := os.Getenv("CLUSTERCOST_REMOTE_MEMORY_BUFFER"); v != "" {
		if iv, err := strconv.Atoi(v); err == nil {
			cfg.Remote.MemoryBuffer = iv
		}
	}
	if v := os.Getenv("CLUSTERCOST_REMOTE_GZIP"); v != "" {
		if bv, err := strconv.ParseBool(v); err == nil {
			cfg.Remote.GzipEnabled = bv
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

func parseNetworkPrices(raw string) (map[string]float64, error) {
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

func mergeNetworkPricingConfig(base *NetworkPricingConfig, override NetworkPricingConfig) {
	if override.DefaultEgressGiBPriceUSD != 0 {
		base.DefaultEgressGiBPriceUSD = override.DefaultEgressGiBPriceUSD
	}
	if override.EgressGiBPricesUSD != nil {
		if base.EgressGiBPricesUSD == nil {
			base.EgressGiBPricesUSD = map[string]float64{}
		}
		for class, price := range override.EgressGiBPricesUSD {
			base.EgressGiBPricesUSD[class] = price
		}
	}
}

func mergeNetworkConfig(base *NetworkConfig, override NetworkConfig) {
	if override.Enabled {
		base.Enabled = override.Enabled
	}
	if override.BPFMapPath != "" {
		base.BPFMapPath = override.BPFMapPath
	}
	if override.ObjectPath != "" {
		base.ObjectPath = override.ObjectPath
	}
	if override.CgroupPath != "" {
		base.CgroupPath = override.CgroupPath
	}
}

func mergeMetricsConfig(base *MetricsConfig, override MetricsConfig) {
	if override.Enabled {
		base.Enabled = override.Enabled
	}
	if override.BPFMapPath != "" {
		base.BPFMapPath = override.BPFMapPath
	}
	if override.CgroupRoot != "" {
		base.CgroupRoot = override.CgroupRoot
	}
	if override.ObjectPath != "" {
		base.ObjectPath = override.ObjectPath
	}
	if override.CgroupPath != "" {
		base.CgroupPath = override.CgroupPath
	}
}

func mergeRemoteConfig(base *RemoteConfig, override RemoteConfig) {
	if override.Enabled {
		base.Enabled = override.Enabled
	}
	if override.EndpointURL != "" {
		base.EndpointURL = override.EndpointURL
	}
	if override.AuthToken != "" {
		base.AuthToken = override.AuthToken
	}
	if override.Timeout != 0 {
		base.Timeout = override.Timeout
	}
	if override.QueueDir != "" {
		base.QueueDir = override.QueueDir
	}
	if override.FlushEvery != 0 {
		base.FlushEvery = override.FlushEvery
	}
	if override.MaxBatch != 0 {
		base.MaxBatch = override.MaxBatch
	}
	if override.MaxRetries != 0 {
		base.MaxRetries = override.MaxRetries
	}
	if override.Backoff != 0 {
		base.Backoff = override.Backoff
	}
	if override.MaxBatchBytes != 0 {
		base.MaxBatchBytes = override.MaxBatchBytes
	}
	if override.MemoryBuffer != 0 {
		base.MemoryBuffer = override.MemoryBuffer
	}
	if override.GzipEnabled {
		base.GzipEnabled = override.GzipEnabled
	}
}
