package collector

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"clustercost-agent-k8s/internal/kube"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
)

type podMetricsLister interface {
	List(ctx context.Context, opts metav1.ListOptions) (*metricsv1beta1.PodMetricsList, error)
}

// MetricsCollector retrieves usage metrics from the metrics.k8s.io API.
type MetricsCollector struct {
	pods   podMetricsLister
	logger *slog.Logger
	mu     sync.RWMutex
	last   map[string]kube.PodUsage
}

// NewMetricsCollector returns a configured collector.
func NewMetricsCollector(client *kube.Client, logger *slog.Logger) *MetricsCollector {
	var podsClient podMetricsLister
	if client != nil && client.Metrics != nil {
		podsClient = client.Metrics.MetricsV1beta1().PodMetricses("")
	}
	return &MetricsCollector{pods: podsClient, logger: logger}
}

// CollectPodMetrics returns usage metrics keyed by namespace/pod name.
func (c *MetricsCollector) CollectPodMetrics(ctx context.Context) (map[string]kube.PodUsage, error) {
	if c.pods == nil {
		return nil, fmt.Errorf("metrics client not configured")
	}
	metricsList, err := c.pods.List(ctx, metav1.ListOptions{})
	if err != nil {
		c.mu.RLock()
		cached := c.last
		c.mu.RUnlock()
		if cached == nil {
			return nil, fmt.Errorf("list pod metrics: %w", err)
		}
		return cached, fmt.Errorf("list pod metrics: %w", err)
	}

	result := make(map[string]kube.PodUsage, len(metricsList.Items))
	for _, m := range metricsList.Items {
		var cpuMilli int64
		var memBytes int64
		for _, container := range m.Containers {
			cpuMilli += container.Usage.Cpu().MilliValue()
			memBytes += container.Usage.Memory().Value()
		}
		key := fmt.Sprintf("%s/%s", m.Namespace, m.Name)
		result[key] = kube.PodUsage{CPUUsageMilli: cpuMilli, MemoryUsageBytes: memBytes}
	}

	c.mu.Lock()
	c.last = result
	c.mu.Unlock()

	return result, nil
}
