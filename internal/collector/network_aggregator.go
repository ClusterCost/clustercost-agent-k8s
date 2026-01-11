package collector

import (
	"fmt"
	"net/netip"

	"clustercost-agent-k8s/internal/kube"
	"clustercost-agent-k8s/internal/network"
)

// AggregateNetworkUsage sums up flow metrics into Pod usage with classification.
// It is platform-independent logic extracted for testing.
func AggregateNetworkUsage(flows []network.Flow, podByIP, nodeByIP map[netip.Addr]network.PodInfo) map[string]kube.PodNetworkUsage {
	result := make(map[string]kube.PodNetworkUsage)

	for _, f := range flows {
		srcPod, ok := podByIP[f.SrcIP]
		if !ok {
			continue
		}

		class := network.ClassifyEgress(srcPod, f.DstIP, podByIP, nodeByIP)
		keyStr := fmt.Sprintf("%s/%s", srcPod.Namespace, srcPod.Pod)

		u := result[keyStr]
		u.TxBytes += f.TxBytes
		u.RxBytes += f.RxBytes

		switch class {
		case network.TrafficClassPublicInternet:
			u.EgressPublicBytes += f.TxBytes
		case network.TrafficClassInterAZ:
			u.EgressCrossAZBytes += f.TxBytes
		case network.TrafficClassIntraNode, network.TrafficClassIntraAZ, network.TrafficClassVPCPrivate:
			u.EgressInternalBytes += f.TxBytes
		default:
			// Unknown is generally internal or unclassified private
			u.EgressInternalBytes += f.TxBytes
		}
		result[keyStr] = u
	}
	return result
}
