package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"clustercost-agent-k8s/internal/api"
	"clustercost-agent-k8s/internal/collector"
	"clustercost-agent-k8s/internal/config"
	"clustercost-agent-k8s/internal/exporter"
	"clustercost-agent-k8s/internal/kube"
	"clustercost-agent-k8s/internal/logging"
	"clustercost-agent-k8s/internal/snapshot"
	"clustercost-agent-k8s/internal/version"

	"k8s.io/apimachinery/pkg/labels"
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
			clusterID = clusterName
		}
	}

	clusterRegion := cfg.Pricing.Region
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

	cache := kube.NewClusterCache(kubeClient.Kubernetes, 0)
	if err := cache.Start(ctx); err != nil {
		logger.Error("failed to start informers", slog.String("error", err.Error()))
		os.Exit(1)
	}

	metricsCollector := collector.NewMetricsCollector(kubeClient, logger)
	classifier := snapshot.NewEnvironmentClassifier(snapshot.ClassifierConfig{
		LabelKeys:              cfg.Environment.LabelKeys,
		ProductionLabelValues:  cfg.Environment.ProductionLabelValues,
		NonProdLabelValues:     cfg.Environment.NonProdLabelValues,
		SystemLabelValues:      cfg.Environment.SystemLabelValues,
		ProductionNameContains: cfg.Environment.ProductionNameContains,
		SystemNamespaces:       cfg.Environment.SystemNamespaces,
	})
	priceLookup := snapshot.NewNodePriceLookup(cfg.Pricing.InstancePrices, cfg.Pricing.DefaultNodeHourlyUSD)
	builder := snapshot.NewBuilder(clusterID, classifier, priceLookup)
	store := snapshot.NewStore()

	go runSnapshotLoop(ctx, builder, cache, metricsCollector, store, cfg.ScrapeInterval(), logger)

	apiHandler := api.NewHandler(clusterType, clusterName, clusterRegion, agentVersion, store)
	mux := http.NewServeMux()
	apiHandler.Register(mux)

	server := exporter.NewServer(cfg.ListenAddr, mux, logger)

	if err := server.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("server error", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

func runSnapshotLoop(ctx context.Context, builder *snapshot.Builder, cache *kube.ClusterCache, metricsCollector *collector.MetricsCollector, store *snapshot.Store, interval time.Duration, logger *slog.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		if err := buildOnce(ctx, builder, cache, metricsCollector, store, logger); err != nil {
			logger.Warn("snapshot refresh failed", slog.String("error", err.Error()))
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func buildOnce(ctx context.Context, builder *snapshot.Builder, cache *kube.ClusterCache, metricsCollector *collector.MetricsCollector, store *snapshot.Store, logger *slog.Logger) error {
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

	metricsCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	usage, metricsErr := metricsCollector.CollectPodMetrics(metricsCtx)
	cancel()
	if metricsErr != nil {
		logger.Warn("using cached pod metrics", slog.String("error", metricsErr.Error()))
	}

	store.Update(builder.Build(nodes, namespaces, pods, usage, time.Now().UTC()))
	return nil
}
