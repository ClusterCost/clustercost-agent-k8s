package collector

import (
	"context"
	"log/slog"

	"clustercost-agent-k8s/internal/config"
	"clustercost-agent-k8s/internal/kube"

	corev1 "k8s.io/api/core/v1"
)

// PodMetricsCollector captures per-pod CPU/memory usage.
type PodMetricsCollector interface {
	CollectPodMetrics(ctx context.Context, pods []*corev1.Pod) (map[string]kube.PodUsage, error)
}

// NewPodMetricsCollector returns an eBPF-backed metrics collector.
func NewPodMetricsCollector(cfg config.MetricsConfig, logger *slog.Logger) PodMetricsCollector {
	if !cfg.Enabled {
		return &noopPodMetricsCollector{}
	}
	return newEBPFMetricsCollector(cfg, logger)
}

type noopPodMetricsCollector struct{}

func (n *noopPodMetricsCollector) CollectPodMetrics(ctx context.Context, pods []*corev1.Pod) (map[string]kube.PodUsage, error) {
	return map[string]kube.PodUsage{}, nil
}
