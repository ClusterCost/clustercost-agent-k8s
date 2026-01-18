package collector

import (
	"fmt"
	"net/netip"

	"clustercost-agent-k8s/internal/kube"
	"clustercost-agent-k8s/internal/network"
)

// AggregateNetworkUsage sums up flow metrics into Pod usage with classification.
// It is platform-independent logic extracted for testing.
func AggregateNetworkUsage(flows []network.Flow, podByIP, nodeByIP map[netip.Addr]network.PodInfo) (map[string]kube.PodNetworkUsage, map[string]kube.PodNetworkUsage) {
	podResult := make(map[string]kube.PodNetworkUsage)
	nodeResult := make(map[string]kube.PodNetworkUsage)

	for _, f := range flows {
		// 1. Process Source (TX / Egress Cost)
		if srcPod, ok := podByIP[f.SrcIP]; ok {
			// Source is a Pod
			processEgress(srcPod, f, podByIP, nodeByIP, podResult)
		} else if srcNode, ok := nodeByIP[f.SrcIP]; ok {
			// Source is a Node (Host Traffic)
			// Use Node Name as key
			processEgress(srcNode, f, podByIP, nodeByIP, nodeResult)
		}

		// 2. Process Destination (RX)
		if dstPod, ok := podByIP[f.DstIP]; ok {
			keyStr := fmt.Sprintf("%s/%s", dstPod.Namespace, dstPod.Pod)
			u := podResult[keyStr]
			u.RxBytes += f.RxBytes
			podResult[keyStr] = u
		} else if dstNode, ok := nodeByIP[f.DstIP]; ok {
			keyStr := dstNode.Node // Node Name
			u := nodeResult[keyStr]
			u.RxBytes += f.RxBytes
			nodeResult[keyStr] = u
		}
	}
	return podResult, nodeResult
}

func processEgress(src network.PodInfo, f network.Flow, podByIP, nodeByIP map[netip.Addr]network.PodInfo, results map[string]kube.PodNetworkUsage) {
	// Determine key: Pod uses "ns/name", Node just handles "name" inside PodInfo struct (Namespace is empty)
	keyStr := ""
	if src.Namespace != "" {
		keyStr = fmt.Sprintf("%s/%s", src.Namespace, src.Pod)
	} else {
		keyStr = src.Node
	}

	class := network.ClassifyEgress(src, f.DstIP, podByIP, nodeByIP)
	u := results[keyStr]
	u.TxBytes += f.TxBytes

	switch class {
	case network.TrafficClassPublicInternet:
		u.EgressPublicBytes += f.TxBytes
	case network.TrafficClassInterAZ:
		u.EgressCrossAZBytes += f.TxBytes
	case network.TrafficClassIntraNode, network.TrafficClassIntraAZ, network.TrafficClassVPCPrivate:
		u.EgressInternalBytes += f.TxBytes
	default:
		u.EgressInternalBytes += f.TxBytes
	}
	results[keyStr] = u
}
