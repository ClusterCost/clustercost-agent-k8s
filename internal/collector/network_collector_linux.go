//go:build linux

package collector

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"sync"

	"clustercost-agent-k8s/internal/network"

	"github.com/cilium/ebpf"
	corev1 "k8s.io/api/core/v1"
)

const (
	afInet  = 2
	afInet6 = 10
)

type ebpfNetworkCollector struct {
	mapPath string
	logger  *slog.Logger

	mu      sync.Mutex
	flowMap *ebpf.Map
	last    map[flowKey]flowStats
}

// flowKey/flowStats define the expected pinned map format for eBPF flow stats.
type flowKey struct {
	SrcAddr [16]byte
	DstAddr [16]byte
	Family  uint8
	Proto   uint8
	_       [2]byte
}

type flowStats struct {
	TxBytes uint64
	RxBytes uint64
}

func newEBPFNetworkCollector(mapPath string, logger *slog.Logger) NetworkCollector {
	if mapPath == "" {
		mapPath = "/sys/fs/bpf/clustercost/flows"
	}
	return &ebpfNetworkCollector{
		mapPath: mapPath,
		logger:  logger,
		last:    map[flowKey]flowStats{},
	}
}

func (c *ebpfNetworkCollector) CollectPodNetwork(ctx context.Context, pods []*corev1.Pod, nodes []*corev1.Node) (NetworkCollection, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.ensureMap(); err != nil {
		return NetworkCollection{}, err
	}

	// Build Node Metadata Maps
	nodeAZ := make(map[string]string, len(nodes))
	nodeByIP := make(map[netip.Addr]network.PodInfo, len(nodes)) // Treating usage of PodInfo for Nodes for convenience in Classifier
	for _, node := range nodes {
		if node == nil {
			continue
		}
		zone := node.Labels["topology.kubernetes.io/zone"]
		if zone == "" {
			zone = node.Labels["failure-domain.beta.kubernetes.io/zone"]
		}
		nodeAZ[node.Name] = zone

		// Map Node IPs
		for _, addr := range node.Status.Addresses {
			if addr.Type == corev1.NodeInternalIP || addr.Type == corev1.NodeExternalIP {
				if ip, err := netip.ParseAddr(addr.Address); err == nil {
					nodeByIP[ip] = network.PodInfo{
						Node:             node.Name,
						AvailabilityZone: zone,
					}
				}
			}
		}
	}

	podByIP := make(map[netip.Addr]network.PodInfo, len(pods))
	for _, pod := range pods {
		if pod == nil || pod.Status.PodIP == "" {
			continue
		}
		ip, err := netip.ParseAddr(pod.Status.PodIP)
		if err != nil {
			continue
		}
		podByIP[ip] = network.PodInfo{
			Namespace:        pod.Namespace,
			Pod:              pod.Name,
			Node:             pod.Spec.NodeName,
			AvailabilityZone: nodeAZ[pod.Spec.NodeName],
		}
	}

	flows := make([]network.Flow, 0)
	iter := c.flowMap.Iterate()
	var key flowKey
	var stats flowStats
	for iter.Next(&key, &stats) {
		srcIP, ok := ipFromFlowKey(key.SrcAddr, key.Family)
		if !ok {
			continue
		}
		dstIP, ok := ipFromFlowKey(key.DstAddr, key.Family)
		if !ok {
			continue
		}
		// Filter: only care if Src is a Pod we know?
		// Agent usually runs on Node. We only care about Src being on THIS node?
		// The BPF map might capture all flows if not filtered by node.
		// Typically DaemonSet agent filters flows for local pods.
		// Assuming BPF does filtering or we filter here.
		// podByIP contains all pods in cluster (from cache).
		// We should check if Src is in podByIP.
		_, srcIsPod := podByIP[srcIP]
		_, dstIsPod := podByIP[dstIP]
		if !srcIsPod && !dstIsPod {
			continue
		}

		delta := c.deltaStats(key, stats)
		if delta.TxBytes == 0 && delta.RxBytes == 0 {
			continue
		}
		flows = append(flows, network.Flow{
			SrcIP:    srcIP,
			DstIP:    dstIP,
			Protocol: key.Proto,
			TxBytes:  delta.TxBytes,
			RxBytes:  delta.RxBytes,
		})
	}

	if err := iter.Err(); err != nil {
		return NetworkCollection{PodUsage: nil, Flows: flows}, fmt.Errorf("iterate eBPF flow map: %w", err)
	}

	// Aggregate flows
	usage := AggregateNetworkUsage(flows, podByIP, nodeByIP)

	return NetworkCollection{PodUsage: usage, Flows: flows}, nil
}

func (c *ebpfNetworkCollector) ensureMap() error {
	if c.flowMap != nil {
		return nil
	}
	m, err := ebpf.LoadPinnedMap(c.mapPath, nil)
	if err != nil {
		return fmt.Errorf("load pinned eBPF map at %s: %w", c.mapPath, err)
	}
	c.flowMap = m
	return nil
}

func (c *ebpfNetworkCollector) deltaStats(key flowKey, current flowStats) flowStats {
	last, ok := c.last[key]
	if !ok {
		c.last[key] = current
		return current
	}
	// Handle potential restart/counter reset?
	// If current < last, assume reset.
	delta := flowStats{}
	if current.TxBytes >= last.TxBytes {
		delta.TxBytes = current.TxBytes - last.TxBytes
	} else {
		delta.TxBytes = current.TxBytes
	}
	if current.RxBytes >= last.RxBytes {
		delta.RxBytes = current.RxBytes - last.RxBytes
	} else {
		delta.RxBytes = current.RxBytes
	}

	c.last[key] = current
	return delta
}

func ipFromFlowKey(raw [16]byte, family uint8) (netip.Addr, bool) {
	switch family {
	case afInet:
		var v4 [4]byte
		copy(v4[:], raw[:4])
		return netip.AddrFrom4(v4), true
	case afInet6:
		return netip.AddrFrom16(raw), true
	default:
		return netip.Addr{}, false
	}
}
