package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"syscall"
	"time"

	"clustercost-agent-k8s/internal/api"
	"clustercost-agent-k8s/internal/collector"
	"clustercost-agent-k8s/internal/config"
	"clustercost-agent-k8s/internal/ebpf"
	"clustercost-agent-k8s/internal/exporter"
	"clustercost-agent-k8s/internal/forwarder"
	"clustercost-agent-k8s/internal/kube"
	"clustercost-agent-k8s/internal/logging"
	"clustercost-agent-k8s/internal/snapshot"
	"clustercost-agent-k8s/internal/version"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/google/uuid"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		panic(err)
	}
	agentVersion := version.Value()

	logger := logging.New(cfg.LogLevel)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	clusterID := cfg.ClusterID
	const clusterType = "k8s"
	clusterName := cfg.ClusterName
	if override := os.Getenv("CLUSTER_NAME"); override != "" {
		clusterName = override
		logger.Info("cluster name override from env", slog.String("clusterName", clusterName))
	}
	kubeClient, err := kube.NewClient(clusterName, cfg.KubeconfigPath)
	if err != nil {
		logger.Error("failed to create kube client", slog.String("error", err.Error()))
		os.Exit(1)
	}

	const placeholderName = "kubernetes"
	const unknownClusterName = "unknown"

	if clusterName == "" || clusterName == placeholderName {
		detectCtx, cancelDetect := context.WithTimeout(ctx, 10*time.Second)
		if detectedName, err := kube.DetectClusterName(detectCtx, kubeClient.Kubernetes); err == nil && detectedName != "" {
			clusterName = detectedName
			kubeClient.ClusterName = detectedName
			if clusterID == "" || clusterID == placeholderName {
				clusterID = detectedName
			}
			logger.Info("detected cluster name", slog.String("clusterName", detectedName))
		} else if err != nil {
			logger.Warn("failed to detect cluster name", slog.String("error", err.Error()))
		}
		cancelDetect()

		if clusterName == "" || clusterName == placeholderName {
			clusterName = unknownClusterName
			logger.Info("defaulting cluster name to unknown")
		}
		if clusterID == "" || clusterID == placeholderName {
			// Try to get a stable ID from kube-system namespace
			if id, err := kube.GetClusterID(ctx, kubeClient.Kubernetes); err == nil && id != "" {
				clusterID = id
				logger.Info("using kube-system namespace uid as cluster id", slog.String("content", id))
			} else {
				clusterID = clusterName
				logger.Warn("failed to get stable cluster id", slog.String("error", err.Error()))
			}
		}
	}

	clusterRegion := cfg.Pricing.Region // Keeping config field for compatibility, but not using logic
	regionCtx, cancelRegion := context.WithTimeout(ctx, 10*time.Second)
	if detectedRegion, err := kube.DetectClusterRegion(regionCtx, kubeClient.Kubernetes); err == nil && detectedRegion != "" {
		clusterRegion = detectedRegion
		logger.Info("detected cluster region", slog.String("clusterRegion", detectedRegion))
	} else if err != nil {
		logger.Warn("failed to detect cluster region", slog.String("error", err.Error()))
	}
	cancelRegion()

	logger.Info("starting clustercost agent",
		slog.String("version", agentVersion),
		slog.String("clusterType", clusterType),
		slog.String("clusterId", clusterID),
		slog.String("clusterName", clusterName),
		slog.String("clusterRegion", clusterRegion),
	)

	nodeName := cfg.NodeName
	if nodeName == "" {
		if envNode := os.Getenv("NODE_NAME"); envNode != "" {
			nodeName = envNode
		}
	}
	if nodeName == "" {
		if host, err := os.Hostname(); err == nil && host != "" {
			nodeName = host
		}
	}
	if nodeName != "" {
		logger.Info("running in node scope", slog.String("nodeName", nodeName))
	} else {
		if cfg.Metrics.Enabled || cfg.Network.Enabled {
			// eBPF typically needs node scope; fall back to disabling collectors.
			logger.Warn("node name missing; disabling eBPF collectors", slog.String("hint", "set NODE_NAME or --node-name"))
			cfg.Metrics.Enabled = false
			cfg.Network.Enabled = false
		}
		logger.Warn("node name not set; using cluster-wide view")
	}

	cache := kube.NewClusterCache(kubeClient.Kubernetes, 0)
	if err := cache.Start(ctx); err != nil {
		logger.Error("failed to start informers", slog.String("error", err.Error()))
		os.Exit(1)
	}

	if cfg.Metrics.Enabled || cfg.Network.Enabled {
		report := ebpf.Preflight(cfg, logger)
		if report.HasErrors() {
			for _, issue := range report.Issues {
				logger.Error("eBPF preflight failed", slog.String("component", issue.Component), slog.String("error", issue.Message))
			}
			logger.Warn("disabling eBPF collectors due to preflight errors")
			cfg.Metrics.Enabled = false
			cfg.Network.Enabled = false
		}
	}

	var ebpfMgr *ebpf.Manager
	if cfg.Metrics.Enabled || cfg.Network.Enabled {
		ebpfMgr, err = ebpf.Start(cfg, logger)
		if err != nil {
			logger.Error("failed to start eBPF programs", slog.String("error", err.Error()))
			os.Exit(1)
		}
		defer ebpfMgr.Close()
	}

	metricsCollector := collector.NewPodMetricsCollector(cfg.Metrics, logger)
	networkCollector := collector.NewNetworkCollector(collector.NetworkCollectorConfig{
		Enabled:    cfg.Network.Enabled,
		BPFMapPath: cfg.Network.BPFMapPath,
	}, logger)
	nodeMetricsCollector := collector.NewNodeMetricsCollector(cfg.Metrics, nodeName, kubeClient.Metrics, logger)
	dnsCache := collector.NewDNSCache(cfg.Network, logger)
	if dnsCache != nil {
		go dnsCache.Run(ctx)
	}

	var sender forwarder.Forwarder
	var queue *forwarder.Queue
	if cfg.Remote.Enabled && cfg.Remote.EndpointURL != "" {
		if cfg.Remote.Protocol == "grpc" {
			var err error
			sender, err = forwarder.NewGRPCSender(ctx, cfg.Remote.EndpointURL, cfg.Remote.AuthToken, cfg.Remote.Timeout)
			if err != nil {
				logger.Error("failed to create grpc sender", slog.String("error", err.Error()))
			} else {
				go func() {
					<-ctx.Done()
					if err := sender.Close(); err != nil {
						logger.Warn("failed to close grpc sender", slog.String("error", err.Error()))
					}
				}()
			}
		} else {
			sender = forwarder.NewHTTPSender(cfg.Remote.EndpointURL, cfg.Remote.AuthToken, cfg.Remote.Timeout, cfg.Remote.GzipEnabled)
		}

		if sender != nil {
			queue = forwarder.NewQueue(cfg.Remote.QueueDir, cfg.Remote.MaxBatch, cfg.Remote.MaxRetries, cfg.Remote.Backoff, cfg.Remote.FlushEvery, cfg.Remote.MaxBatchBytes, cfg.Remote.MemoryBuffer, sender, logger)
			logger.Info("remote forwarding enabled", slog.String("endpoint", cfg.Remote.EndpointURL), slog.String("protocol", cfg.Remote.Protocol))
			go queue.Run(ctx)
		}
	}

	builder := snapshot.NewBuilder(snapshot.BuilderConfig{
		ClusterID:       clusterID,
		NetworkPricing:  cfg.Pricing.Network,
		NetworkDetailed: cfg.Network.Detailed,
	})
	store := snapshot.NewStore()

	// Generate Stable Agent ID based on Node Name (if available) or Cluster ID
	agentID := uuid.NewString()
	if nodeName != "" {
		// Deterministic UUID v5 based on Node Name ensures stability across restarts
		// Using NameSpaceDNS as a stable namespace for hostnames
		agentID = uuid.NewSHA1(uuid.NameSpaceDNS, []byte(nodeName)).String()
	}
	logger.Info("agent id generated", slog.String("agentId", agentID), slog.String("scope", "stable-node-identity"))

	go runSnapshotLoop(ctx, builder, cache, metricsCollector, networkCollector, nodeMetricsCollector, dnsCache, queue, clusterID, clusterName, nodeName, agentID, agentVersion, store, cfg.ScrapeInterval(), logger)

	apiHandler := api.NewHandler(clusterType, clusterName, clusterRegion, agentVersion, agentID, store)
	mux := http.NewServeMux()
	apiHandler.Register(mux)

	server := exporter.NewServer(cfg.ListenAddr, mux, logger)

	if err := server.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("server error", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

func runSnapshotLoop(ctx context.Context, builder *snapshot.Builder, cache *kube.ClusterCache, metricsCollector collector.PodMetricsCollector, networkCollector collector.NetworkCollector, nodeMetricsCollector collector.NodeMetricsCollector, dnsCache *collector.DNSCache, queue *forwarder.Queue, clusterID, clusterName, nodeName, agentID, version string, store *snapshot.Store, interval time.Duration, logger *slog.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		if err := buildOnce(ctx, builder, cache, metricsCollector, networkCollector, nodeMetricsCollector, dnsCache, queue, clusterID, clusterName, nodeName, agentID, version, store, logger); err != nil {
			logger.Warn("snapshot refresh failed", slog.String("error", err.Error()))
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func buildOnce(ctx context.Context, builder *snapshot.Builder, cache *kube.ClusterCache, metricsCollector collector.PodMetricsCollector, networkCollector collector.NetworkCollector, nodeMetricsCollector collector.NodeMetricsCollector, dnsCache *collector.DNSCache, queue *forwarder.Queue, clusterID, clusterName, nodeName, agentID, version string, store *snapshot.Store, logger *slog.Logger) error {
	nodes, err := cache.NodeLister().List(labels.Everything())
	if err != nil {
		return err
	}
	namespaces, err := cache.NamespaceLister().List(labels.Everything())
	if err != nil {
		return err
	}
	pods, err := cache.PodLister().List(labels.Everything())
	if err != nil {
		return err
	}
	services, err := cache.ServiceLister().List(labels.Everything())
	if err != nil {
		return err
	}
	endpoints, err := cache.EndpointsLister().List(labels.Everything())
	if err != nil {
		return err
	}

	var availabilityZone, region, instanceType string
	if nodeName != "" {
		nodes = filterNodes(nodes, nodeName)
		pods = filterPods(pods, nodeName)
		if len(nodes) > 0 {
			labels := nodes[0].Labels
			availabilityZone = labels["topology.kubernetes.io/zone"]
			if availabilityZone == "" {
				availabilityZone = labels["failure-domain.beta.kubernetes.io/zone"]
			}
			region = labels["topology.kubernetes.io/region"]
			if region == "" {
				region = labels["failure-domain.beta.kubernetes.io/region"]
			}
			instanceType = labels["node.kubernetes.io/instance-type"]
			if instanceType == "" {
				instanceType = labels["beta.kubernetes.io/instance-type"]
			}
		}
	}

	metricsCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	usage, metricsErr := metricsCollector.CollectPodMetrics(metricsCtx, pods)
	cancel()
	if metricsErr != nil {
		logger.Warn("pod metrics collection failed", slog.String("error", metricsErr.Error()))
	}
	if usage == nil {
		usage = map[string]kube.PodUsage{}
	}

	networkCtx, cancelNetwork := context.WithTimeout(ctx, 15*time.Second)
	networkCollection, networkErr := networkCollector.CollectPodNetwork(networkCtx, pods, nodes)
	cancelNetwork()
	if networkErr != nil {
		logger.Warn("network usage collection failed", slog.String("error", networkErr.Error()))
	}

	nodeMetricsCtx, cancelNodeMetrics := context.WithTimeout(ctx, 15*time.Second)
	nodeUsage, nodeMetricsErr := nodeMetricsCollector.CollectNodeMetrics(nodeMetricsCtx, nodes)
	cancelNodeMetrics()
	if nodeMetricsErr != nil {
		logger.Warn("node metrics collection failed", slog.String("error", nodeMetricsErr.Error()))
	}
	if nodeUsage == nil {
		nodeUsage = map[string]kube.NodeUsage{}
	}

	dnsNames := map[netip.Addr]string{}
	if dnsCache != nil {
		dnsNames = dnsCache.Snapshot()
	}

	store.Update(builder.Build(nodes, namespaces, pods, services, endpoints, usage, networkCollection, nodeUsage, dnsNames, time.Now().UTC()))

	if queue != nil {
		report := forwarder.AgentReport{
			ClusterID:        clusterID,
			ClusterName:      clusterName,
			NodeName:         nodeName,
			AvailabilityZone: availabilityZone,
			Region:           region,
			InstanceType:     instanceType,
			AgentID:          agentID,
			Version:          version,
			Timestamp:        time.Now().UTC(),
			Snapshot:         store.LatestSnapshot(),
		}
		if err := queue.Enqueue(report); err != nil {
			logger.Warn("queue enqueue failed", slog.String("error", err.Error()))
		}
	}
	return nil
}

func filterNodes(nodes []*corev1.Node, nodeName string) []*corev1.Node {
	if nodeName == "" {
		return nodes
	}
	filtered := make([]*corev1.Node, 0, 1)
	for _, node := range nodes {
		if node != nil && node.Name == nodeName {
			filtered = append(filtered, node)
			break
		}
	}
	return filtered
}

func filterPods(pods []*corev1.Pod, nodeName string) []*corev1.Pod {
	if nodeName == "" {
		return pods
	}
	filtered := make([]*corev1.Pod, 0, len(pods))
	for _, pod := range pods {
		if pod != nil && pod.Spec.NodeName == nodeName {
			filtered = append(filtered, pod)
		}
	}
	return filtered
}
