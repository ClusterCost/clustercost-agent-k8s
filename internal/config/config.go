package config

import (
	"flag"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config captures the runtime settings for the agent.
type Config struct {
	ClusterName           string        `yaml:"clusterName"`
	NodeName              string        `yaml:"nodeName"`
	ListenAddr            string        `yaml:"listenAddr"`
	LogLevel              string        `yaml:"logLevel"`
	ScrapeIntervalSeconds int           `yaml:"scrapeIntervalSeconds"`
	KubeconfigPath        string        `yaml:"kubeconfig"`
	Network               NetworkConfig `yaml:"network"`
	Metrics               MetricsConfig `yaml:"metrics"`
	Remote                RemoteConfig  `yaml:"remote"`
}

// NetworkConfig configures network usage collection.
type NetworkConfig struct {
	Enabled         bool   `yaml:"enabled"`
	Detailed        bool   `yaml:"detailed"`
	DNSCapture      bool   `yaml:"dnsCapture"`
	BPFMapPath      string `yaml:"bpfMapPath"`
	DNSMapPath      string `yaml:"dnsMapPath"`
	DNSCacheEntries int    `yaml:"dnsCacheEntries"`
	DNSSampleRate   int    `yaml:"dnsSampleRate"`
	ObjectPath      string `yaml:"objectPath"`
	CgroupPath      string `yaml:"cgroupPath"`
}

// MetricsConfig configures eBPF-based usage collection.
type MetricsConfig struct {
	Enabled    bool   `yaml:"enabled"`
	CgroupRoot string `yaml:"cgroupRoot"`
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
	Protocol      string        `yaml:"protocol"` // "http" or "grpc"
}

// DefaultConfig returns sane defaults for the agent.
func DefaultConfig() Config {
	return Config{
		ClusterName:           "kubernetes",
		NodeName:              "",
		ListenAddr:            ":8080",
		LogLevel:              "info",
		ScrapeIntervalSeconds: 60,
		Network: NetworkConfig{
			Enabled:         true,
			Detailed:        true,
			DNSCapture:      true,
			BPFMapPath:      "/sys/fs/bpf/clustercost/flows",
			DNSMapPath:      "/sys/fs/bpf/clustercost/dns_events",
			DNSCacheEntries: 10000,
			DNSSampleRate:   100,
			ObjectPath:      "/opt/clustercost/bpf/flows.bpf.o",
			CgroupPath:      "/sys/fs/cgroup",
		},
		Metrics: MetricsConfig{
			Enabled:    true,
			CgroupRoot: "/sys/fs/cgroup",
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
			Protocol:      "grpc",
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
	fs.StringVar(&cfg.ClusterName, "cluster-name", cfg.ClusterName, "Cluster name (friendly)")
	fs.StringVar(&cfg.NodeName, "node-name", cfg.NodeName, "Node name (for daemonset mode)")
	fs.StringVar(&cfg.ListenAddr, "listen-addr", cfg.ListenAddr, "HTTP listen address")
	fs.StringVar(&cfg.LogLevel, "log-level", cfg.LogLevel, "Log level (debug, info, warn, error)")
	fs.IntVar(&cfg.ScrapeIntervalSeconds, "scrape-interval", cfg.ScrapeIntervalSeconds, "Scrape interval in seconds")
	fs.StringVar(&cfg.KubeconfigPath, "kubeconfig", cfg.KubeconfigPath, "Path to kubeconfig (optional)")
	fs.BoolVar(&cfg.Network.Enabled, "enable-network", cfg.Network.Enabled, "Enable eBPF-based network collection")
	fs.BoolVar(&cfg.Network.Detailed, "enable-network-detailed", cfg.Network.Detailed, "Enable detailed network connection reporting")
	fs.BoolVar(&cfg.Network.DNSCapture, "enable-network-dns", cfg.Network.DNSCapture, "Enable DNS capture for network connections")
	fs.StringVar(&cfg.Network.BPFMapPath, "ebpf-map-path", cfg.Network.BPFMapPath, "Pinned eBPF map path for network flow stats")
	fs.StringVar(&cfg.Network.DNSMapPath, "ebpf-dns-map-path", cfg.Network.DNSMapPath, "Pinned eBPF map path for DNS events")
	fs.IntVar(&cfg.Network.DNSCacheEntries, "network-dns-cache-entries", cfg.Network.DNSCacheEntries, "Max DNS cache entries in memory")
	fs.IntVar(&cfg.Network.DNSSampleRate, "network-dns-sample-rate", cfg.Network.DNSSampleRate, "DNS sample rate percent (1-100)")
	fs.StringVar(&cfg.Network.ObjectPath, "ebpf-net-object", cfg.Network.ObjectPath, "Path to eBPF network object file")
	fs.StringVar(&cfg.Network.CgroupPath, "ebpf-net-cgroup-path", cfg.Network.CgroupPath, "Cgroup path for eBPF network attachment")
	fs.BoolVar(&cfg.Metrics.Enabled, "enable-metrics", cfg.Metrics.Enabled, "Enable cgroup-based metrics collection")
	fs.StringVar(&cfg.Metrics.CgroupRoot, "cgroup-root", cfg.Metrics.CgroupRoot, "Root path for cgroup filesystem")
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
	fs.StringVar(&cfg.Remote.Protocol, "remote-protocol", cfg.Remote.Protocol, "Remote protocol (http or grpc)")

	if err := fs.Parse(os.Args[1:]); err != nil { // flag set already prints errors
		return Config{}, err
	}

	if configFile != "" {
		if err := loadFromFile(configFile, &cfg); err != nil {
			return Config{}, err
		}
	}

	// Flags already parsed into cfg before file load to honor precedence order: file > flags.

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
	applyExplicitOverrides(cfg, Config(fileCfg), data)
	return nil
}

func applyExplicitOverrides(base *Config, override Config, raw []byte) {
	var rawCfg map[string]any
	if err := yaml.Unmarshal(raw, &rawCfg); err != nil {
		return
	}

	if networkRaw, ok := rawCfg["network"].(map[string]any); ok {
		if _, ok := networkRaw["enabled"]; ok {
			base.Network.Enabled = override.Network.Enabled
		}
		if _, ok := networkRaw["detailed"]; ok {
			base.Network.Detailed = override.Network.Detailed
		}
		if _, ok := networkRaw["dnsCapture"]; ok {
			base.Network.DNSCapture = override.Network.DNSCapture
		}
		if _, ok := networkRaw["dnsCacheEntries"]; ok {
			base.Network.DNSCacheEntries = override.Network.DNSCacheEntries
		}
		if _, ok := networkRaw["dnsSampleRate"]; ok {
			base.Network.DNSSampleRate = override.Network.DNSSampleRate
		}
	}

	if metricsRaw, ok := rawCfg["metrics"].(map[string]any); ok {
		if _, ok := metricsRaw["enabled"]; ok {
			base.Metrics.Enabled = override.Metrics.Enabled
		}
	}
}

func mergeConfigs(base *Config, override Config) {
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
	mergeNetworkConfig(&base.Network, override.Network)
	mergeMetricsConfig(&base.Metrics, override.Metrics)
	mergeRemoteConfig(&base.Remote, override.Remote)
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func mergeNetworkConfig(base *NetworkConfig, override NetworkConfig) {
	if override.Enabled {
		base.Enabled = override.Enabled
	}
	if override.Detailed {
		base.Detailed = override.Detailed
	}
	if override.DNSCapture {
		base.DNSCapture = override.DNSCapture
	}
	if override.BPFMapPath != "" {
		base.BPFMapPath = override.BPFMapPath
	}
	if override.DNSMapPath != "" {
		base.DNSMapPath = override.DNSMapPath
	}
	if override.DNSCacheEntries != 0 {
		base.DNSCacheEntries = override.DNSCacheEntries
	}
	if override.DNSSampleRate != 0 {
		base.DNSSampleRate = override.DNSSampleRate
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
	if override.CgroupRoot != "" {
		base.CgroupRoot = override.CgroupRoot
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
	if override.Protocol != "" {
		base.Protocol = override.Protocol
	}
}
