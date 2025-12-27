//go:build !linux

package collector

import (
	"context"
	"fmt"
	"log/slog"

	corev1 "k8s.io/api/core/v1"
)

type ebpfNetworkCollector struct{}

func newEBPFNetworkCollector(mapPath string, logger *slog.Logger) NetworkCollector {
	return &ebpfNetworkCollector{}
}

func (c *ebpfNetworkCollector) CollectPodNetwork(ctx context.Context, pods []*corev1.Pod, nodes []*corev1.Node) (NetworkCollection, error) {
	return NetworkCollection{}, fmt.Errorf("eBPF network collector is only supported on linux")
}
