package collector

import (
	"context"
	"log/slog"

	"clustercost-agent-k8s/internal/config"
	"clustercost-agent-k8s/internal/kube"

	corev1 "k8s.io/api/core/v1"
)

// NodeMetricsCollector captures node CPU and memory usage.
type NodeMetricsCollector interface {
	CollectNodeMetrics(ctx context.Context, nodes []*corev1.Node) (map[string]kube.NodeUsage, error)
}

// NewNodeMetricsCollector returns an eBPF-backed collector when enabled, otherwise a noop collector.
func NewNodeMetricsCollector(cfg config.MetricsConfig, nodeName string, logger *slog.Logger) NodeMetricsCollector {
	if cfg.Enabled {
		return newCgroupNodeMetricsCollector(cfg, nodeName, logger)
	}
	return &noopNodeMetricsCollector{}
}

type noopNodeMetricsCollector struct{}

func (n *noopNodeMetricsCollector) CollectNodeMetrics(ctx context.Context, nodes []*corev1.Node) (map[string]kube.NodeUsage, error) {
	return map[string]kube.NodeUsage{}, nil
}
