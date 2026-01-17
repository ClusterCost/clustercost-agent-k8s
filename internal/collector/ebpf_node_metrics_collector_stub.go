//go:build !linux

package collector

import (
	"log/slog"

	"clustercost-agent-k8s/internal/config"
)

func newEBPFNodeMetricsCollector(cfg config.MetricsConfig, nodeName string, logger *slog.Logger) NodeMetricsCollector {
	return &noopNodeMetricsCollector{}
}
