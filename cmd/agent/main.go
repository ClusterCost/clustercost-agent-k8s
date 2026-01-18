package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"strings"
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
	discoveryv1 "k8s.io/api/discovery/v1"
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

	const clusterType = "k8s"
	clusterName := cfg.ClusterName
	if override := os.Getenv("CLUSTER_NAME"); override != "" {
		clusterName = override
		logger.Info("cluster name override from env", slog.String("clusterName", clusterName))
	}
	const placeholderName = "kubernetes"
	if clusterName == placeholderName {
		clusterName = ""
	}
	kubeClient, err := kube.NewClient(clusterName, cfg.KubeconfigPath)
	if err != nil {
		logger.Error("failed to create kube client", slog.String("error", err.Error()))
		os.Exit(1)
	}

	if clusterName == "" || clusterName == placeholderName {
		detectCtx, cancelDetect := context.WithTimeout(ctx, 10*time.Second)
		if detectedName, err := kube.DetectClusterName(detectCtx, kubeClient.Kubernetes); err == nil && detectedName != "" {
			clusterName = detectedName
			kubeClient.ClusterName = detectedName
			logger.Info("detected cluster name", slog.String("clusterName", detectedName))
		} else if err != nil {
			logger.Warn("failed to detect cluster name", slog.String("error", err.Error()))
		}
		cancelDetect()
	}

	clusterID, err := kube.GetClusterID(ctx, kubeClient.Kubernetes)
	if err != nil || clusterID == "" {
		if err == nil {
			err = errors.New("cluster id empty")
		}
		logger.Error("failed to get stable cluster id", slog.String("error", err.Error()))
		os.Exit(1)
	}
	logger.Info("using kube-system namespace uid as cluster id", slog.String("content", clusterID))

	clusterRegion := ""
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
		podName := os.Getenv("POD_NAME")
		if podName == "" {
			if host, err := os.Hostname(); err == nil && host != "" {
				podName = host
			}
		}
		podNamespace := os.Getenv("POD_NAMESPACE")
		if podNamespace == "" {
			if ns, err := readServiceAccountNamespace(); err == nil && ns != "" {
				podNamespace = ns
			}
		}
		detectCtx, cancelDetect := context.WithTimeout(ctx, 10*time.Second)
		detectedNode, err := kube.DetectNodeNameFromPod(detectCtx, kubeClient.Kubernetes, podNamespace, podName)
		cancelDetect()
		if err != nil {
			logger.Error("failed to detect node name from pod", slog.String("error", err.Error()), slog.String("hint", "set NODE_NAME or --node-name"))
			os.Exit(1)
		}
		nodeName = detectedNode
	}
	logger.Info("running in node scope", slog.String("nodeName", nodeName))

	cache := kube.NewClusterCache(kubeClient.Kubernetes, 0)
	if err := cache.Start(ctx); err != nil {
		logger.Error("failed to start informers", slog.String("error", err.Error()))
		os.Exit(1)
	}

	if cfg.Network.Enabled {
		report := ebpf.Preflight(cfg, logger)
		if report.HasErrors() {
			for _, issue := range report.Issues {
				logger.Error("eBPF preflight failed", slog.String("component", issue.Component), slog.String("error", issue.Message))
			}
			logger.Warn("disabling eBPF network collector due to preflight errors")
			cfg.Network.Enabled = false
		}
	}

	var ebpfMgr *ebpf.Manager
	if cfg.Network.Enabled {
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
	nodeMetricsCollector := collector.NewNodeMetricsCollector(cfg.Metrics, nodeName, logger)
	dnsCache := collector.NewDNSCache(cfg.Network, logger)
	if dnsCache != nil {
		go dnsCache.Run(ctx)
	}

	var sender forwarder.Forwarder
	var queue *forwarder.Queue
	if cfg.Remote.Enabled && cfg.Remote.EndpointURL != "" {
		if cfg.Remote.Protocol == "grpc" {
			var err error
			sender, err = forwarder.NewGRPCSender(ctx, cfg.Remote.EndpointURL, cfg.Remote.AuthToken, cfg.Remote.Timeout, logger)
			if err != nil {
				logger.Error("failed to create grpc sender", slog.String("error", err.Error()))
			} else {
				logger.Info("connected to agent, starting data transmission via grpc", slog.String("endpoint", cfg.Remote.EndpointURL))
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

	meta := commonMetadata{
		clusterID:        clusterID,
		clusterName:      clusterName,
		nodeName:         nodeName,
		agentID:          agentID,
		version:          agentVersion,
		availabilityZone: "", // Filled in loops from node labels
		region:           clusterRegion,
	}

	networkInterval := 5 * time.Minute
	if cfg.Network.Enabled {
		if cfg.Network.ReportIntervalSeconds > 0 {
			networkInterval = time.Duration(cfg.Network.ReportIntervalSeconds) * time.Second
		}
	}

	logger.Info("starting metrics collection", slog.Int("scrape_interval_seconds", cfg.ScrapeIntervalSeconds))
	go runHybridLoop(ctx, builder, cache, metricsCollector, networkCollector, nodeMetricsCollector, dnsCache, queue, meta, store, cfg.ScrapeInterval(), networkInterval, logger)

	apiHandler := api.NewHandler(clusterType, clusterName, clusterRegion, agentVersion, agentID, store)
	mux := http.NewServeMux()
	apiHandler.Register(mux)

	server := exporter.NewServer(cfg.ListenAddr, mux, logger)

	if err := server.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("server error", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

func runHybridLoop(ctx context.Context, builder *snapshot.Builder, cache *kube.ClusterCache, metricsCollector collector.PodMetricsCollector, networkCollector collector.NetworkCollector, nodeMetricsCollector collector.NodeMetricsCollector, dnsCache *collector.DNSCache, queue *forwarder.Queue, meta commonMetadata, store *snapshot.Store, scrapeInterval, networkInterval time.Duration, logger *slog.Logger) {
	ticker := time.NewTicker(scrapeInterval)
	defer ticker.Stop()

	accumulator := NewFlowAccumulator()
	lastNetworkReport := time.Now()

	// Initial run
	processHybridTick(ctx, builder, cache, metricsCollector, networkCollector, nodeMetricsCollector, dnsCache, queue, meta, store, accumulator, &lastNetworkReport, networkInterval, logger)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			processHybridTick(ctx, builder, cache, metricsCollector, networkCollector, nodeMetricsCollector, dnsCache, queue, meta, store, accumulator, &lastNetworkReport, networkInterval, logger)
		}
	}
}

func processHybridTick(ctx context.Context, builder *snapshot.Builder, cache *kube.ClusterCache, metricsCollector collector.PodMetricsCollector, networkCollector collector.NetworkCollector, nodeMetricsCollector collector.NodeMetricsCollector, dnsCache *collector.DNSCache, queue *forwarder.Queue, meta commonMetadata, store *snapshot.Store, accumulator *FlowAccumulator, lastNetworkReport *time.Time, networkInterval time.Duration, logger *slog.Logger) {
	nodes, pods, namespaces, services, endpoints, err := getCacheObjects(cache, meta.nodeName)
	if err != nil {
		logger.Warn("cache list failed", slog.String("error", err.Error()))
		return
	}
	if len(nodes) == 0 {
		logger.Warn("node not found in cache", slog.String("node", meta.nodeName))
		return
	}
	// Update dynamic metadata
	meta.availabilityZone, meta.region, meta.instanceType = extractNodeMetadata(nodes[0])

	// 1. Collect Metrics (CPU/RAM)
	metricsCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	usage, err := metricsCollector.CollectPodMetrics(metricsCtx, pods)
	cancel()
	if err != nil {
		logger.Warn("pod metrics collection failed", slog.String("error", err.Error()))
	}
	if usage == nil {
		usage = map[string]kube.PodUsage{}
	}

	// 2. Collect Node Metrics
	nodeMetricsCtx, cancelNode := context.WithTimeout(ctx, 15*time.Second)
	nodeUsage, err := nodeMetricsCollector.CollectNodeMetrics(nodeMetricsCtx, nodes)
	cancelNode()
	if err != nil {
		logger.Warn("node metrics collection failed", slog.String("error", err.Error()))
	}

	// 3. Collect Network (60s Delta)
	networkCtx, cancelNet := context.WithTimeout(ctx, 15*time.Second)
	// Important: This call resets the collector's internal delta reference
	netCollection, err := networkCollector.CollectPodNetwork(networkCtx, pods, nodes)
	cancelNet()
	if err != nil {
		logger.Warn("network collection failed", slog.String("error", err.Error()))
	}

	// 4. Send Metrics Report (Immediate)
	// We want to include the Network Aggregates (PodUsage) in this report as requested.
	// But we DO NOT want detailed Flows here.
	dnsNames := map[netip.Addr]string{}
	if dnsCache != nil {
		dnsNames = dnsCache.Snapshot()
	}

	// Build Snapshot for Metrics (Flows ignored by setting them to nil effectively, but we pass netCollection which HAS flows?)
	// Builder.Build handles flows if present. We should strip flows for the metrics report.
	metricsNetCollection := netCollection
	metricsNetCollection.Flows = nil // Hide detailed flows from metrics report

	netSnap := builder.Build(nodes, namespaces, pods, services, endpoints, usage, metricsNetCollection, nodeUsage, dnsNames, time.Now().UTC())

	store.Update(netSnap) // Local store sees everything? Or just metrics? Let's update with metrics for now.

	if queue != nil {
		report := forwarder.AgentReport{
			Type:             forwarder.ReportTypeMetrics,
			ClusterID:        meta.clusterID,
			ClusterName:      meta.clusterName,
			NodeName:         meta.nodeName,
			AvailabilityZone: meta.availabilityZone,
			Region:           meta.region,
			InstanceType:     meta.instanceType,
			AgentID:          meta.agentID,
			Version:          meta.version,
			Timestamp:        netSnap.Timestamp,
			Snapshot:         netSnap,
		}
		if err := queue.Enqueue(report); err != nil {
			logger.Warn("queue enqueue metrics failed", slog.String("error", err.Error()))
		}
	}

	// 5. Accumulate Flows
	if len(netCollection.Flows) > 0 {
		accumulator.Add(netCollection.Flows)
	}

	// 6. Check Network Report Tick
	// 6. Check Network Report Tick
	if time.Since(*lastNetworkReport) >= networkInterval {
		accumulatedFlows := accumulator.Flush()

		// Chunking Logic
		// Limit to 1000 flows per report to avoid gRPC size limits
		const maxFlowsPerReport = 1000

		for i := 0; i < len(accumulatedFlows); i += maxFlowsPerReport {
			end := i + maxFlowsPerReport
			if end > len(accumulatedFlows) {
				end = len(accumulatedFlows)
			}
			chunk := accumulatedFlows[i:end]

			// Build Snapshot for Network Chunk
			networkSnap := builder.Build(nodes, namespaces, pods, services, endpoints, nil, collector.NetworkCollection{Flows: chunk}, nil, dnsNames, time.Now().UTC())

			if queue != nil {
				report := forwarder.AgentReport{
					Type:             forwarder.ReportTypeNetwork,
					ClusterID:        meta.clusterID,
					ClusterName:      meta.clusterName,
					NodeName:         meta.nodeName,
					AvailabilityZone: meta.availabilityZone,
					Region:           meta.region,
					InstanceType:     meta.instanceType,
					AgentID:          meta.agentID,
					Version:          meta.version,
					Timestamp:        networkSnap.Timestamp,
					Snapshot:         networkSnap,
				}
				if err := queue.Enqueue(report); err != nil {
					logger.Warn("queue enqueue network chunk failed", slog.String("error", err.Error()))
				}
			}
		}

		*lastNetworkReport = time.Now()
		logger.Debug("sent network report", slog.Int("total_flows", len(accumulatedFlows)), slog.Int("chunks", (len(accumulatedFlows)+maxFlowsPerReport-1)/maxFlowsPerReport))
	}
}

func getCacheObjects(cache *kube.ClusterCache, nodeName string) ([]*corev1.Node, []*corev1.Pod, []*corev1.Namespace, []*corev1.Service, []*discoveryv1.EndpointSlice, error) {
	nodes, err := cache.NodeLister().List(labels.Everything())
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	namespaces, err := cache.NamespaceLister().List(labels.Everything())
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	pods, err := cache.PodLister().List(labels.Everything())
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	services, err := cache.ServiceLister().List(labels.Everything())
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	endpoints, err := cache.EndpointsLister().List(labels.Everything())
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	nodes = filterNodes(nodes, nodeName)
	pods = filterPods(pods, nodeName)
	return nodes, pods, namespaces, services, endpoints, nil
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

func readServiceAccountNamespace() (string, error) {
	data, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
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

type commonMetadata struct {
	clusterID, clusterName, nodeName, agentID, version, availabilityZone, region, instanceType string
}
