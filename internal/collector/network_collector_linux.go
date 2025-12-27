//go:build linux

package collector

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"sync"

	"clustercost-agent-k8s/internal/kube"
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

	nodeAZ := make(map[string]string, len(nodes))
	for _, node := range nodes {
		if node == nil {
			continue
		}
		nodeAZ[node.Name] = node.Labels["topology.kubernetes.io/zone"]
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

	result := map[string]kube.PodNetworkUsage{}
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
		srcPod, ok := podByIP[srcIP]
		if !ok {
			continue
		}
		delta := c.deltaStats(key, stats)
		if delta.TxBytes == 0 && delta.RxBytes == 0 {
			continue
		}
		flows = append(flows, network.Flow{
			SrcIP:   srcIP,
			DstIP:   dstIP,
			TxBytes: delta.TxBytes,
			RxBytes: delta.RxBytes,
		})
		class := network.ClassifyEgress(srcPod, dstIP, podByIP)
		keyStr := fmt.Sprintf("%s/%s", srcPod.Namespace, srcPod.Pod)
		usage := result[keyStr]
		usage.TxBytes += delta.TxBytes
		usage.RxBytes += delta.RxBytes
		usage.TxBytesByClass = addBytesByClass(usage.TxBytesByClass, class, delta.TxBytes)
		usage.RxBytesByClass = addBytesByClass(usage.RxBytesByClass, class, delta.RxBytes)
		result[keyStr] = usage
	}

	if err := iter.Err(); err != nil {
		return NetworkCollection{PodUsage: result, Flows: flows}, fmt.Errorf("iterate eBPF flow map: %w", err)
	}
	return NetworkCollection{PodUsage: result, Flows: flows}, nil
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
	delta := flowStats{
		TxBytes: diffUint64(current.TxBytes, last.TxBytes),
		RxBytes: diffUint64(current.RxBytes, last.RxBytes),
	}
	c.last[key] = current
	return delta
}

func diffUint64(current, previous uint64) uint64 {
	if current >= previous {
		return current - previous
	}
	return current
}

func addBytesByClass(m map[string]uint64, class string, delta uint64) map[string]uint64 {
	if delta == 0 {
		return m
	}
	if m == nil {
		m = map[string]uint64{}
	}
	m[class] += delta
	return m
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
