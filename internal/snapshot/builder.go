package snapshot

import (
	"fmt"
	"net/netip"
	"strings"
	"time"

	"clustercost-agent-k8s/internal/collector"
	"clustercost-agent-k8s/internal/kube"
	"clustercost-agent-k8s/internal/network"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
)

// Builder converts informer/lister state into the public snapshot model.
type Builder struct {
	clusterID string
}

// NewBuilder returns a configured Builder.
func NewBuilder(clusterID string) *Builder {
	return &Builder{
		clusterID: clusterID,
	}
}

// Build assembles a snapshot using the cached kubernetes objects and usage metrics.
func (b *Builder) Build(nodes []*corev1.Node, namespaces []*corev1.Namespace, pods []*corev1.Pod, services []*corev1.Service, endpoints []*discoveryv1.EndpointSlice, usage map[string]kube.PodUsage, networkCollection collector.NetworkCollection, generatedAt time.Time) Snapshot {

	// 1. Prepare Network Data (IP mappings)
	// We need to resolve IPs to find traffic categories (CrossAZ, Public, Internal)
	podInfoByIP := make(map[netip.Addr]network.PodInfo, len(pods))
	nodeZones := make(map[string]string, len(nodes))

	for _, node := range nodes {
		if node != nil {
			nodeZones[node.Name] = node.Labels["topology.kubernetes.io/zone"]
		}
	}

	for _, pod := range pods {
		if pod == nil || pod.Status.PodIP == "" {
			continue
		}
		ip, err := netip.ParseAddr(pod.Status.PodIP)
		if err == nil {
			podInfoByIP[ip] = network.PodInfo{
				Namespace:        pod.Namespace,
				Pod:              pod.Name,
				Node:             pod.Spec.NodeName,
				AvailabilityZone: nodeZones[pod.Spec.NodeName],
			}
		}
	}

	// 2. Process Network Usage
	aggregatedNetwork := map[string]kube.PodNetworkUsage{}

	if len(networkCollection.Flows) > 0 {
		aggregatedNetwork = aggregatePodUsageFromFlows(networkCollection.Flows, podInfoByIP)
	} else if len(networkCollection.PodUsage) > 0 {
		// Fallback or if collector already matches format?
		aggregatedNetwork = networkCollection.PodUsage
	}

	// 3. Process Pods & Build Metrics
	podMetrics := make([]PodMetric, 0, len(pods))

	for _, pod := range pods {
		if skipPod(pod) {
			continue
		}

		key := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)

		// CPU & Memory from 'usage'
		podUsage := usage[key]

		// Network from 'aggregatedNetwork'
		netUsage := aggregatedNetwork[key]

		containerID := ""
		if len(pod.Status.ContainerStatuses) > 0 {
			cID := pod.Status.ContainerStatuses[0].ContainerID
			parts := strings.Split(cID, "://")
			if len(parts) > 1 {
				containerID = parts[1]
			} else {
				containerID = cID
			}
		}

		var pid uint32 = 0

		pm := PodMetric{
			PodUID:      string(pod.UID),
			ContainerID: containerID,
			PID:         pid,
			Namespace:   pod.Namespace,
			PodName:     pod.Name,

			Cpu: CpuMetrics{
				UsageUser:   podUsage.CPUUsageUserNs,
				UsageKernel: podUsage.CPUUsageKernelNs,
				Throttling:  podUsage.CPUThrottlingNs,
			},
			Memory: MemoryMetrics{
				RSS:        podUsage.MemoryRSS,
				PageFaults: podUsage.MemoryPageFaults,
			},
			Network: NetworkMetrics{
				BytesSent:      netUsage.TxBytes,
				BytesReceived:  netUsage.RxBytes,
				EgressPublic:   netUsage.EgressPublicBytes,
				EgressCrossAZ:  netUsage.EgressCrossAZBytes,
				EgressInternal: netUsage.EgressInternalBytes,
			},
			Storage: StorageMetrics{
				ReadBytes:    podUsage.StorageReadBytes,
				WriteBytes:   podUsage.StorageWriteBytes,
				ReadOps:      podUsage.StorageReadOps,
				WriteOps:     podUsage.StorageWriteOps,
				TotalLatency: podUsage.StorageTotalLatency,
			},
		}

		podMetrics = append(podMetrics, pm)
	}

	return Snapshot{
		Timestamp: generatedAt,
		Pods:      podMetrics,
	}
}

func aggregatePodUsageFromFlows(flows []network.Flow, podByIP map[netip.Addr]network.PodInfo) map[string]kube.PodNetworkUsage {
	result := map[string]kube.PodNetworkUsage{}
	for _, flow := range flows {
		srcPod, ok := podByIP[flow.SrcIP]
		if !ok {
			continue
		}

		var egressPublic, egressCrossAZ, egressInternal uint64

		if dstInfo, destIsPod := podByIP[flow.DstIP]; destIsPod {
			// Internal Pod-to-Pod
			if srcPod.AvailabilityZone == dstInfo.AvailabilityZone {
				egressInternal = flow.TxBytes
			} else {
				egressCrossAZ = flow.TxBytes
			}
		} else {
			if flow.DstIP.IsPrivate() {
				egressInternal = flow.TxBytes
			} else {
				egressPublic = flow.TxBytes
			}
		}

		key := fmt.Sprintf("%s/%s", srcPod.Namespace, srcPod.Pod)
		usage := result[key]
		usage.TxBytes += flow.TxBytes
		usage.RxBytes += flow.RxBytes

		usage.EgressPublicBytes += egressPublic
		usage.EgressCrossAZBytes += egressCrossAZ
		usage.EgressInternalBytes += egressInternal

		result[key] = usage
	}
	return result
}

func skipPod(pod *corev1.Pod) bool {
	if pod == nil {
		return true
	}
	if pod.Spec.NodeName == "" {
		return true
	}
	if pod.DeletionTimestamp != nil {
		return true
	}
	if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
		return true
	}
	return false
}
