//go:build !linux

package collector

import (
	"context"
	"fmt"
	"log/slog"

	"clustercost-agent-k8s/internal/config"
	"clustercost-agent-k8s/internal/kube"

	corev1 "k8s.io/api/core/v1"
)

func newEBPFMetricsCollector(cfg config.MetricsConfig, logger *slog.Logger) PodMetricsCollector {
	return &unsupportedMetricsCollector{}
}

type unsupportedMetricsCollector struct{}

func (u *unsupportedMetricsCollector) CollectPodMetrics(ctx context.Context, pods []*corev1.Pod) (map[string]kube.PodUsage, error) {
	return nil, fmt.Errorf("eBPF metrics collector is only supported on linux")
}
