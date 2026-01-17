package collector

import (
	"context"
	"log/slog"

	"clustercost-agent-k8s/internal/config"
	"clustercost-agent-k8s/internal/kube"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"
)

// NodeMetricsCollector captures node CPU and memory usage from the metrics API.
type NodeMetricsCollector interface {
	CollectNodeMetrics(ctx context.Context, nodes []*corev1.Node) (map[string]kube.NodeUsage, error)
}

// NewNodeMetricsCollector returns an eBPF-backed collector when enabled, otherwise a metrics API-backed collector.
func NewNodeMetricsCollector(cfg config.MetricsConfig, nodeName string, client metricsclient.Interface, logger *slog.Logger) NodeMetricsCollector {
	if cfg.Enabled {
		return newEBPFNodeMetricsCollector(cfg, nodeName, logger)
	}
	if client == nil {
		return &noopNodeMetricsCollector{}
	}
	return &metricsNodeCollector{client: client, logger: logger}
}

type metricsNodeCollector struct {
	client metricsclient.Interface
	logger *slog.Logger
}

func (c *metricsNodeCollector) CollectNodeMetrics(ctx context.Context, nodes []*corev1.Node) (map[string]kube.NodeUsage, error) {
	if c == nil || c.client == nil {
		return map[string]kube.NodeUsage{}, nil
	}

	allowed := map[string]struct{}{}
	for _, node := range nodes {
		if node == nil || node.Name == "" {
			continue
		}
		allowed[node.Name] = struct{}{}
	}

	list, err := c.client.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	result := make(map[string]kube.NodeUsage, len(list.Items))
	for _, item := range list.Items {
		if len(allowed) > 0 {
			if _, ok := allowed[item.Name]; !ok {
				continue
			}
		}
		result[item.Name] = kube.NodeUsage{
			CPUUsageMilli:    item.Usage.Cpu().MilliValue(),
			MemoryUsageBytes: item.Usage.Memory().Value(),
		}
	}

	if c.logger != nil && len(allowed) > 0 && len(result) == 0 {
		c.logger.Debug("node metrics empty from metrics API")
	}

	return result, nil
}

type noopNodeMetricsCollector struct{}

func (n *noopNodeMetricsCollector) CollectNodeMetrics(ctx context.Context, nodes []*corev1.Node) (map[string]kube.NodeUsage, error) {
	return map[string]kube.NodeUsage{}, nil
}
