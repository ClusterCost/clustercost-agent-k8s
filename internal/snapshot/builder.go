package snapshot

import (
	"fmt"
	"net/netip"
	"sort"
	"time"

	"clustercost-agent-k8s/internal/collector"
	"clustercost-agent-k8s/internal/kube"
	"clustercost-agent-k8s/internal/network"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Builder converts informer/lister state into the public snapshot model.
type Builder struct {
	clusterID       string
	classifier      *EnvironmentClassifier
	prices          *NodePriceLookup
	netPrices       *NetworkPriceLookup
	detailedNetwork bool
}

// NewBuilder returns a configured Builder.
func NewBuilder(clusterID string, classifier *EnvironmentClassifier, prices *NodePriceLookup, netPrices *NetworkPriceLookup, detailedNetwork bool) *Builder {
	if netPrices == nil {
		netPrices = NewNetworkPriceLookup(0, nil)
	}
	return &Builder{
		clusterID:       clusterID,
		classifier:      classifier,
		prices:          prices,
		netPrices:       netPrices,
		detailedNetwork: detailedNetwork,
	}
}

// Build assembles a snapshot using the cached kubernetes objects and usage metrics.
func (b *Builder) Build(nodes []*corev1.Node, namespaces []*corev1.Namespace, pods []*corev1.Pod, services []*corev1.Service, endpoints []*discoveryv1.EndpointSlice, usage map[string]kube.PodUsage, networkCollection collector.NetworkCollection, generatedAt time.Time) Snapshot {
	// 1. Process Nodes
	nodeRecords := make(map[string]*nodeAggregate, len(nodes))
	var totalNodeCost float64
	for _, node := range nodes {
		rec := NodeCostRecord{
			ClusterID:              b.clusterID,
			NodeName:               node.Name,
			CPUAllocatableMilli:    node.Status.Allocatable.Cpu().MilliValue(),
			MemoryAllocatableBytes: node.Status.Allocatable.Memory().Value(),
			Labels:                 cloneStringMap(node.Labels),
			Taints:                 formatTaints(node.Spec.Taints),
			InstanceType:           detectInstanceType(node.Labels),
			Status:                 nodeStatus(node.Status.Conditions),
			IsUnderPressure:        nodeUnderPressure(node.Status.Conditions),
		}
		rec.HourlyCost = b.prices.Price(rec.InstanceType)
		totalNodeCost += rec.HourlyCost
		nodeRecords[node.Name] = &nodeAggregate{record: rec}
	}

	// 2. Prepare Network Data (IP mappings)
	podInfoByIP := make(map[netip.Addr]network.PodInfo, len(pods))
	podByIP := make(map[netip.Addr]*corev1.Pod, len(pods))
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
			podByIP[ip] = pod
			podInfoByIP[ip] = network.PodInfo{
				Namespace:        pod.Namespace,
				Pod:              pod.Name,
				Node:             pod.Spec.NodeName,
				AvailabilityZone: nodeZones[pod.Spec.NodeName],
			}
		}
	}

	// 3. Process Network Usage
	networkUsage := networkCollection.PodUsage
	if len(networkUsage) == 0 {
		networkUsage = aggregatePodUsageFromFlows(networkCollection.Flows, podInfoByIP)
	}

	// 4. Process Pods & Aggregate Totals
	var clusterCPUReq, clusterCPUUsage int64
	var clusterMemReq, clusterMemUsage int64
	var clusterNetTx, clusterNetRx uint64
	var clusterNetCost float64

	podRecords := make([]PodRecord, 0, len(pods))

	for _, pod := range pods {
		if skipPod(pod) {
			continue
		}

		// Resource Usage
		cpuReq, memReq := sumPodRequests(pod)
		clusterCPUReq += cpuReq
		clusterMemReq += memReq

		key := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
		podUsage := usage[key]
		cpuUsage := podUsage.CPUUsageMilli
		if cpuUsage == 0 {
			cpuUsage = cpuReq
		}
		memUsage := podUsage.MemoryUsageBytes
		if memUsage == 0 {
			memUsage = memReq
		}
		clusterCPUUsage += cpuUsage
		clusterMemUsage += memUsage

		// Calculate Resource Cost (Share of Node)
		var resourceCost float64
		if nodeAgg, ok := nodeRecords[pod.Spec.NodeName]; ok {
			nodeAgg.podCount++
			nodeAgg.cpuUsageMilli += cpuUsage
			nodeAgg.memoryUsageBytes += memUsage

			allocatableCPU := nodeAgg.record.CPUAllocatableMilli
			if allocatableCPU > 0 && nodeAgg.record.HourlyCost > 0 && cpuReq > 0 {
				share := float64(cpuReq) / float64(allocatableCPU)
				if share > 1 {
					share = 1
				}
				resourceCost = share * nodeAgg.record.HourlyCost
			}
		}

		// Network Usage & Cost
		netUsage := networkUsage[key]
		if netUsage.TxBytes > 0 && len(netUsage.TxBytesByClass) == 0 {
			netUsage.TxBytesByClass = map[string]uint64{network.TrafficClassUnknown: netUsage.TxBytes}
		}

		var podNetCost float64
		var netByClass []NetworkClassTotals
		for class, txBytes := range netUsage.TxBytesByClass {
			cost := b.netPrices.EgressCost(class, txBytes)
			podNetCost += cost
			rxBytes := netUsage.RxBytesByClass[class]
			netByClass = append(netByClass, NetworkClassTotals{
				Class:            class,
				TxBytes:          txBytes,
				RxBytes:          rxBytes,
				EgressCostHourly: cost,
			})
		}
		// Sort by class for consistency
		sort.Slice(netByClass, func(i, j int) bool {
			return netByClass[i].Class < netByClass[j].Class
		})

		clusterNetTx += netUsage.TxBytes
		clusterNetRx += netUsage.RxBytes
		clusterNetCost += podNetCost

		// Owner Info
		ownerKind := ""
		ownerName := ""
		if ctrl := metav1.GetControllerOf(pod); ctrl != nil {
			ownerKind = ctrl.Kind
			ownerName = ctrl.Name
		}

		// Construct Record
		podRecords = append(podRecords, PodRecord{
			Namespace:               pod.Namespace,
			Pod:                     pod.Name,
			Node:                    pod.Spec.NodeName,
			Labels:                  cloneStringMap(pod.Labels),
			Environment:             b.classifier.Classify(pod.Namespace, pod.Labels),
			OwnerKind:               ownerKind,
			OwnerName:               ownerName,
			CPURequestMilli:         cpuReq,
			CPUUsageMilli:           cpuUsage,
			MemoryRequestBytes:      memReq,
			MemoryUsageBytes:        memUsage,
			ResourceHourlyCost:      resourceCost,
			NetworkTxBytes:          netUsage.TxBytes,
			NetworkRxBytes:          netUsage.RxBytes,
			NetworkEgressCostHourly: podNetCost,
			NetworkByClass:          netByClass,
			TotalHourlyCost:         resourceCost + podNetCost,
		})
	}

	// 5. Finalize Node Records (Utilization)
	nodesOut := make([]NodeCostRecord, 0, len(nodeRecords))
	for _, agg := range nodeRecords {
		if agg.record.CPUAllocatableMilli > 0 {
			agg.record.CPUUsagePercent = clampPercent(float64(agg.cpuUsageMilli) / float64(agg.record.CPUAllocatableMilli) * 100)
		}
		if agg.record.MemoryAllocatableBytes > 0 {
			agg.record.MemoryUsagePercent = clampPercent(float64(agg.memoryUsageBytes) / float64(agg.record.MemoryAllocatableBytes) * 100)
		}
		agg.record.PodCount = agg.podCount
		nodesOut = append(nodesOut, agg.record)
	}
	// Sort so the selected node is deterministic if there are multiple (though typically 1 agent 1 node)
	sort.Slice(nodesOut, func(i, j int) bool {
		return nodesOut[i].NodeName < nodesOut[j].NodeName
	})

	var nodeRecord *NodeCostRecord
	if len(nodesOut) > 0 {
		nodeRecord = &nodesOut[0]
	}

	return Snapshot{
		Timestamp: generatedAt,
		Node:      nodeRecord,
		Pods:      podRecords,
		Resources: ResourceSnapshot{
			ClusterID:               b.clusterID,
			CPUUsageMilliTotal:      clusterCPUUsage,
			CPURequestMilliTotal:    clusterCPUReq,
			MemoryUsageBytesTotal:   clusterMemUsage,
			MemoryRequestBytesTotal: clusterMemReq,
			TotalNodeHourlyCost:     totalNodeCost,
			NetworkTxBytesTotal:     clusterNetTx,
			NetworkRxBytesTotal:     clusterNetRx,
			NetworkEgressCostTotal:  clusterNetCost,
		},
	}
}

type nodeAggregate struct {
	record           NodeCostRecord
	podCount         int
	cpuUsageMilli    int64
	memoryUsageBytes int64
}

func aggregatePodUsageFromFlows(flows []network.Flow, podByIP map[netip.Addr]network.PodInfo) map[string]kube.PodNetworkUsage {
	result := map[string]kube.PodNetworkUsage{}
	for _, flow := range flows {
		srcPod, ok := podByIP[flow.SrcIP]
		if !ok {
			continue
		}
		class := network.ClassifyEgress(srcPod, flow.DstIP, podByIP)
		key := fmt.Sprintf("%s/%s", srcPod.Namespace, srcPod.Pod)
		usage := result[key]
		usage.TxBytes += flow.TxBytes
		usage.RxBytes += flow.RxBytes
		if usage.TxBytesByClass == nil {
			usage.TxBytesByClass = map[string]uint64{}
		}
		if usage.RxBytesByClass == nil {
			usage.RxBytesByClass = map[string]uint64{}
		}
		usage.TxBytesByClass[class] += flow.TxBytes
		usage.RxBytesByClass[class] += flow.RxBytes
		result[key] = usage
	}
	return result
}

func sumPodRequests(pod *corev1.Pod) (cpuMilli int64, memoryBytes int64) {
	for _, c := range pod.Spec.Containers {
		cpuMilli += c.Resources.Requests.Cpu().MilliValue()
		memoryBytes += c.Resources.Requests.Memory().Value()
	}
	for _, c := range pod.Spec.InitContainers {
		cpuMilli += c.Resources.Requests.Cpu().MilliValue()
		memoryBytes += c.Resources.Requests.Memory().Value()
	}
	// Ephemeral containers could be added if needed, but typically low impact
	return
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

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func detectInstanceType(labels map[string]string) string {
	if labels == nil {
		return ""
	}
	for _, key := range []string{
		"node.kubernetes.io/instance-type",
		"beta.kubernetes.io/instance-type",
		"node.k8s.amazonaws.com/instance-type",
	} {
		if v := labels[key]; v != "" {
			return v
		}
	}
	return ""
}

func formatTaints(taints []corev1.Taint) []string {
	if len(taints) == 0 {
		return nil
	}
	out := make([]string, 0, len(taints))
	for _, t := range taints {
		if t.Value == "" {
			out = append(out, fmt.Sprintf("%s:%s", t.Key, t.Effect))
		} else {
			out = append(out, fmt.Sprintf("%s=%s:%s", t.Key, t.Value, t.Effect))
		}
	}
	sort.Strings(out)
	return out
}

func nodeStatus(conditions []corev1.NodeCondition) string {
	for _, c := range conditions {
		if c.Type == corev1.NodeReady {
			if c.Status == corev1.ConditionTrue {
				return "Ready"
			}
			return "NotReady"
		}
	}
	return "Unknown"
}

func nodeUnderPressure(conditions []corev1.NodeCondition) bool {
	pressureTypes := map[corev1.NodeConditionType]struct{}{
		corev1.NodeDiskPressure:   {},
		corev1.NodeMemoryPressure: {},
		corev1.NodePIDPressure:    {},
	}
	for _, c := range conditions {
		if _, ok := pressureTypes[c.Type]; ok && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func clampPercent(value float64) float64 {
	switch {
	case value < 0:
		return 0
	case value > 100:
		return 100
	default:
		return value
	}
}
