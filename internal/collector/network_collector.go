package collector

import (
	"context"
	"log/slog"

	"clustercost-agent-k8s/internal/kube"
	"clustercost-agent-k8s/internal/network"

	corev1 "k8s.io/api/core/v1"
)

// NetworkCollectorConfig configures network usage collection.
type NetworkCollectorConfig struct {
	Enabled    bool
	BPFMapPath string
}

// NetworkCollection contains aggregated per-pod usage plus flow deltas.
type NetworkCollection struct {
	PodUsage map[string]kube.PodNetworkUsage
	Flows    []network.Flow
}

// NetworkCollector captures per-pod network usage.
type NetworkCollector interface {
	CollectPodNetwork(ctx context.Context, pods []*corev1.Pod, nodes []*corev1.Node) (NetworkCollection, error)
}

// NewNetworkCollector returns a network collector implementation.
func NewNetworkCollector(cfg NetworkCollectorConfig, logger *slog.Logger) NetworkCollector {
	if !cfg.Enabled {
		return &noopNetworkCollector{}
	}
	return newEBPFNetworkCollector(cfg.BPFMapPath, logger)
}

type noopNetworkCollector struct{}

func (n *noopNetworkCollector) CollectPodNetwork(ctx context.Context, pods []*corev1.Pod, nodes []*corev1.Node) (NetworkCollection, error) {
	return NetworkCollection{PodUsage: map[string]kube.PodNetworkUsage{}}, nil
}
