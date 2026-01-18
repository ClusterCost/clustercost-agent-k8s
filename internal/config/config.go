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
	Enabled               bool   `yaml:"enabled"`
	Detailed              bool   `yaml:"detailed"`
	DNSCapture            bool   `yaml:"dnsCapture"`
	BPFMapPath            string `yaml:"bpfMapPath"`
	DNSMapPath            string `yaml:"dnsMapPath"`
	DNSCacheEntries       int    `yaml:"dnsCacheEntries"`
	DNSSampleRate         int    `yaml:"dnsSampleRate"`
	ReportIntervalSeconds int    `yaml:"reportIntervalSeconds"`
	ObjectPath            string `yaml:"objectPath"`
	CgroupPath            string `yaml:"cgroupPath"`
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
		ScrapeIntervalSeconds: 15,
		Network: NetworkConfig{
			Enabled:               true,
			Detailed:              true,
			DNSCapture:            true,
			BPFMapPath:            "/sys/fs/bpf/clustercost/flows",
			DNSMapPath:            "/sys/fs/bpf/clustercost/dns_events",
			DNSCacheEntries:       10000,
			DNSSampleRate:         100,
			ReportIntervalSeconds: 300,
			ObjectPath:            "/opt/clustercost/bpf/flows.bpf.o",
			CgroupPath:            "/sys/fs/cgroup",
		},
		Metrics: MetricsConfig{
			Enabled:    true,
			CgroupRoot: "/sys/fs/cgroup",
		},
		Remote: RemoteConfig{
			Enabled:       true,
			EndpointURL:   "clustercost-dashboard.clustercost.svc.cluster.local:9091",
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
	fs.IntVar(&cfg.Network.ReportIntervalSeconds, "network-report-interval", cfg.Network.ReportIntervalSeconds, "Network report interval in seconds")
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

	explicitFlags := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) {
		explicitFlags[f.Name] = true
	})

	if configFile != "" {
		if err := loadFromFile(configFile, &cfg, explicitFlags); err != nil {
			return Config{}, err
		}
	}

	// Step 2: Environment Variables (Manual Mapping)
	// We check these BEFORE flags so flags can still override them if needed,
	// BUT because we parse flags above, we need to be careful.
	// Actually, the standard precedence is: Defaults < Config File < Env Vars < Flags.
	// Since we already parsed flags, we should apply Env Vars ONLY if the flag wasn't explicitly set.
	// However, `explicitFlags` map tracks that.

	val := ""
	if val = os.Getenv("CLUSTERCOST_SCRAPE_INTERVAL"); val != "" && !explicitFlags["scrape-interval"] {
		if v, err := time.ParseDuration(val); err == nil {
			cfg.ScrapeIntervalSeconds = int(v.Seconds())
		} else {
			// Try parsing as int
			var s int
			if _, err := fmt.Sscanf(val, "%d", &s); err == nil {
				cfg.ScrapeIntervalSeconds = s
			}
		}
	}

	if val = os.Getenv("CLUSTERCOST_ENABLE_NETWORK"); val != "" && !explicitFlags["enable-network"] {
		cfg.Network.Enabled = (val == "true" || val == "1")
	}

	if val = os.Getenv("CLUSTERCOST_NETWORK_REPORT_INTERVAL"); val != "" && !explicitFlags["network-report-interval"] {
		var s int
		if _, err := fmt.Sscanf(val, "%d", &s); err == nil {
			cfg.Network.ReportIntervalSeconds = s
		}
	}

	if val = os.Getenv("CLUSTERCOST_REMOTE_ENDPOINT"); val != "" && !explicitFlags["remote-endpoint"] {
		cfg.Remote.EndpointURL = val
	}

	if val = os.Getenv("CLUSTERCOST_REMOTE_PROTOCOL"); val != "" && !explicitFlags["remote-protocol"] {
		cfg.Remote.Protocol = val
	}

	if val = os.Getenv("CLUSTERCOST_REMOTE_AUTH_TOKEN"); val != "" && !explicitFlags["remote-auth-token"] {
		cfg.Remote.AuthToken = val
	}

	// Flags already parsed into cfg before file load to honor precedence order: file > flags.

	// Validation & Clamping
	if cfg.ScrapeIntervalSeconds < 5 {
		fmt.Fprintf(os.Stderr, "warning: scrape interval %ds is too low; clamping to minimum 5s\n", cfg.ScrapeIntervalSeconds)
		cfg.ScrapeIntervalSeconds = 5
	}
	if cfg.Network.Enabled {
		if cfg.Network.ReportIntervalSeconds < 60 {
			fmt.Fprintf(os.Stderr, "warning: network report interval %ds is too low; clamping to minimum 60s\n", cfg.Network.ReportIntervalSeconds)
			cfg.Network.ReportIntervalSeconds = 60
		}
	}

	return cfg, nil
}

func loadFromFile(path string, cfg *Config, explicitFlags map[string]bool) error {
	data, err := os.ReadFile(path) // #nosec G304 -- path provided by cluster operator
	if err != nil {
		return fmt.Errorf("read config file: %w", err)
	}

	type fileConfig Config
	var fileCfg fileConfig
	if err := yaml.Unmarshal(data, &fileCfg); err != nil {
		return fmt.Errorf("parse config file: %w", err)
	}

	mergeConfigs(cfg, Config(fileCfg), explicitFlags)
	applyExplicitOverrides(cfg, Config(fileCfg), data, explicitFlags)
	return nil
}

func applyExplicitOverrides(base *Config, override Config, raw []byte, explicitFlags map[string]bool) {
	var rawCfg map[string]any
	if err := yaml.Unmarshal(raw, &rawCfg); err != nil {
		return
	}

	if networkRaw, ok := rawCfg["network"].(map[string]any); ok {
		if _, ok := networkRaw["enabled"]; ok && !explicitFlags["enable-network"] {
			base.Network.Enabled = override.Network.Enabled
		}
		if _, ok := networkRaw["detailed"]; ok && !explicitFlags["enable-network-detailed"] {
			base.Network.Detailed = override.Network.Detailed
		}
		if _, ok := networkRaw["dnsCapture"]; ok && !explicitFlags["enable-network-dns"] {
			base.Network.DNSCapture = override.Network.DNSCapture
		}
		if _, ok := networkRaw["dnsCacheEntries"]; ok && !explicitFlags["network-dns-cache-entries"] {
			base.Network.DNSCacheEntries = override.Network.DNSCacheEntries
		}
		if _, ok := networkRaw["dnsSampleRate"]; ok && !explicitFlags["network-dns-sample-rate"] {
			base.Network.DNSSampleRate = override.Network.DNSSampleRate
		}
		if _, ok := networkRaw["reportIntervalSeconds"]; ok && !explicitFlags["network-report-interval"] {
			base.Network.ReportIntervalSeconds = override.Network.ReportIntervalSeconds
		}
	}

	if metricsRaw, ok := rawCfg["metrics"].(map[string]any); ok {
		if _, ok := metricsRaw["enabled"]; ok && !explicitFlags["enable-metrics"] {
			base.Metrics.Enabled = override.Metrics.Enabled
		}
	}
}

func mergeConfigs(base *Config, override Config, explicitFlags map[string]bool) {
	if override.ClusterName != "" && !explicitFlags["cluster-name"] {
		base.ClusterName = override.ClusterName
	}
	if override.NodeName != "" && !explicitFlags["node-name"] {
		base.NodeName = override.NodeName
	}
	if override.ListenAddr != "" && !explicitFlags["listen-addr"] {
		base.ListenAddr = override.ListenAddr
	}
	if override.LogLevel != "" && !explicitFlags["log-level"] {
		base.LogLevel = override.LogLevel
	}
	if override.ScrapeIntervalSeconds != 0 && !explicitFlags["scrape-interval"] {
		base.ScrapeIntervalSeconds = override.ScrapeIntervalSeconds
	}
	if override.KubeconfigPath != "" && !explicitFlags["kubeconfig"] {
		base.KubeconfigPath = override.KubeconfigPath
	}
	mergeNetworkConfig(&base.Network, override.Network, explicitFlags)
	mergeMetricsConfig(&base.Metrics, override.Metrics, explicitFlags)
	mergeRemoteConfig(&base.Remote, override.Remote, explicitFlags)
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func mergeNetworkConfig(base *NetworkConfig, override NetworkConfig, explicitFlags map[string]bool) {
	if override.Enabled && !explicitFlags["enable-network"] {
		base.Enabled = override.Enabled
	}
	if override.Detailed && !explicitFlags["enable-network-detailed"] {
		base.Detailed = override.Detailed
	}
	if override.DNSCapture && !explicitFlags["enable-network-dns"] {
		base.DNSCapture = override.DNSCapture
	}
	if override.BPFMapPath != "" && !explicitFlags["ebpf-map-path"] {
		base.BPFMapPath = override.BPFMapPath
	}
	if override.DNSMapPath != "" && !explicitFlags["ebpf-dns-map-path"] {
		base.DNSMapPath = override.DNSMapPath
	}
	if override.DNSCacheEntries != 0 && !explicitFlags["network-dns-cache-entries"] {
		base.DNSCacheEntries = override.DNSCacheEntries
	}
	if override.DNSSampleRate != 0 && !explicitFlags["network-dns-sample-rate"] {
		base.DNSSampleRate = override.DNSSampleRate
	}
	if override.ReportIntervalSeconds != 0 && !explicitFlags["network-report-interval"] {
		base.ReportIntervalSeconds = override.ReportIntervalSeconds
	}
	if override.ObjectPath != "" && !explicitFlags["ebpf-net-object"] {
		base.ObjectPath = override.ObjectPath
	}
	if override.CgroupPath != "" && !explicitFlags["ebpf-net-cgroup-path"] {
		base.CgroupPath = override.CgroupPath
	}
}

func mergeMetricsConfig(base *MetricsConfig, override MetricsConfig, explicitFlags map[string]bool) {
	if override.Enabled && !explicitFlags["enable-metrics"] {
		base.Enabled = override.Enabled
	}
	if override.CgroupRoot != "" && !explicitFlags["cgroup-root"] {
		base.CgroupRoot = override.CgroupRoot
	}
}

func mergeRemoteConfig(base *RemoteConfig, override RemoteConfig, explicitFlags map[string]bool) {
	if override.Enabled && !explicitFlags["remote-enabled"] {
		base.Enabled = override.Enabled
	}
	if override.EndpointURL != "" && !explicitFlags["remote-endpoint"] {
		base.EndpointURL = override.EndpointURL
	}
	if override.AuthToken != "" && !explicitFlags["remote-auth-token"] {
		base.AuthToken = override.AuthToken
	}
	if override.Timeout != 0 && !explicitFlags["remote-timeout"] {
		base.Timeout = override.Timeout
	}
	if override.QueueDir != "" && !explicitFlags["remote-queue-dir"] {
		base.QueueDir = override.QueueDir
	}
	if override.FlushEvery != 0 && !explicitFlags["remote-flush-every"] {
		base.FlushEvery = override.FlushEvery
	}
	if override.MaxBatch != 0 && !explicitFlags["remote-max-batch"] {
		base.MaxBatch = override.MaxBatch
	}
	if override.MaxRetries != 0 && !explicitFlags["remote-max-retries"] {
		base.MaxRetries = override.MaxRetries
	}
	if override.Backoff != 0 && !explicitFlags["remote-backoff"] {
		base.Backoff = override.Backoff
	}
	if override.MaxBatchBytes != 0 && !explicitFlags["remote-max-batch-bytes"] {
		base.MaxBatchBytes = override.MaxBatchBytes
	}
	if override.MemoryBuffer != 0 && !explicitFlags["remote-memory-buffer"] {
		base.MemoryBuffer = override.MemoryBuffer
	}
	if override.GzipEnabled && !explicitFlags["remote-gzip"] {
		base.GzipEnabled = override.GzipEnabled
	}
	if override.Protocol != "" && !explicitFlags["remote-protocol"] {
		base.Protocol = override.Protocol
	}
}
