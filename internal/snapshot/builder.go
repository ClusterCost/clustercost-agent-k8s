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
	nsRecords := make(map[string]*NamespaceCostRecord, len(namespaces))
	for _, ns := range namespaces {
		nsRecords[ns.Name] = &NamespaceCostRecord{
			ClusterID:   b.clusterID,
			Namespace:   ns.Name,
			Labels:      cloneStringMap(ns.Labels),
			Environment: b.classifier.Classify(ns.Name, ns.Labels),
		}
	}

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

	var clusterCPUReq, clusterCPUUsage int64
	var clusterMemReq, clusterMemUsage int64
	var clusterNetTx, clusterNetRx uint64
	var clusterNetCost float64

	networkByNamespace := make(map[string]*namespaceNetworkAggregate, len(namespaces))
	for _, ns := range namespaces {
		if ns == nil {
			continue
		}
		networkByNamespace[ns.Name] = &namespaceNetworkAggregate{
			record:  NamespaceNetworkRecord{Namespace: ns.Name},
			byClass: map[string]*NetworkClassTotals{},
		}
	}
	networkByClass := map[string]*NetworkClassTotals{}
	podNetworkRecords := make([]PodNetworkRecord, 0, len(pods))

	podByIP := make(map[netip.Addr]*corev1.Pod, len(pods))
	podInfoByIP := make(map[netip.Addr]network.PodInfo, len(pods))
	podByKey := make(map[string]*corev1.Pod, len(pods))
	podWorkloads := make(map[string]NetworkEndpoint, len(pods))
	nodeZones := make(map[string]string, len(nodes))

	for _, node := range nodes {
		if node == nil {
			continue
		}
		nodeZones[node.Name] = node.Labels["topology.kubernetes.io/zone"]
	}

	for _, pod := range pods {
		if pod == nil {
			continue
		}
		key := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
		podByKey[key] = pod
		if pod.Status.PodIP != "" {
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
		podWorkloads[key] = workloadEndpoint(pod)
	}

	serviceByIP := buildServiceIndex(endpoints)
	serviceIndex := map[string]NetworkEndpoint{}
	for _, svc := range services {
		if svc == nil {
			continue
		}
		serviceIndex[serviceKey(svc.Namespace, svc.Name)] = NetworkEndpoint{
			Kind:      "service",
			Namespace: svc.Namespace,
			Name:      svc.Name,
		}
	}

	networkUsage := networkCollection.PodUsage

	for _, pod := range pods {
		if skipPod(pod) {
			continue
		}
		ns := ensureNamespace(nsRecords, b.clusterID, pod.Namespace, b.classifier)
		ns.PodCount++

		cpuReq, memReq := sumPodRequests(pod)
		ns.CPURequestMilli += cpuReq
		ns.MemoryRequestBytes += memReq
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

		ns.CPUUsageMilli += cpuUsage
		ns.MemoryUsageBytes += memUsage
		clusterCPUUsage += cpuUsage
		clusterMemUsage += memUsage

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
				ns.HourlyCost += share * nodeAgg.record.HourlyCost
			}
		}
	}

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
	sort.Slice(nodesOut, func(i, j int) bool {
		return nodesOut[i].NodeName < nodesOut[j].NodeName
	})

	var podConnections map[connectionKey]*NetworkConnection
	var workloadConnections map[connectionKey]*NetworkConnection
	var namespaceConnections map[connectionKey]*NetworkConnection
	var serviceConnections map[connectionKey]*NetworkConnection

	if b.detailedNetwork {
		podConnections = make(map[connectionKey]*NetworkConnection)
		workloadConnections = make(map[connectionKey]*NetworkConnection)
		namespaceConnections = make(map[connectionKey]*NetworkConnection)
		serviceConnections = make(map[connectionKey]*NetworkConnection)

		for _, flow := range networkCollection.Flows {
			srcPod := podByIP[flow.SrcIP]
			if srcPod == nil {
				continue
			}
			srcKey := fmt.Sprintf("%s/%s", srcPod.Namespace, srcPod.Name)
			srcInfo := podInfoByIP[flow.SrcIP]
			dstPod := podByIP[flow.DstIP]
			class := network.ClassifyEgress(srcInfo, flow.DstIP, podInfoByIP)
			cost := b.netPrices.EgressCost(class, flow.TxBytes)

			srcPodEndpoint := NetworkEndpoint{Kind: "pod", Namespace: srcPod.Namespace, Name: srcPod.Name}
			srcNsEndpoint := NetworkEndpoint{Kind: "namespace", Name: srcPod.Namespace}
			srcWorkloadEndpoint := podWorkloads[srcKey]
			if srcWorkloadEndpoint.Kind == "" {
				srcWorkloadEndpoint = NetworkEndpoint{Kind: "workload", Namespace: srcPod.Namespace, Name: srcPod.Name}
			}

			var dstPodEndpoint NetworkEndpoint
			var dstNsEndpoint NetworkEndpoint
			var dstWorkloadEndpoint NetworkEndpoint
			var dstServiceEndpoints []NetworkEndpoint

			if dstPod != nil {
				dstPodEndpoint = NetworkEndpoint{Kind: "pod", Namespace: dstPod.Namespace, Name: dstPod.Name}
				dstNsEndpoint = NetworkEndpoint{Kind: "namespace", Name: dstPod.Namespace}
				dstWorkloadEndpoint = podWorkloads[fmt.Sprintf("%s/%s", dstPod.Namespace, dstPod.Name)]
				if dstWorkloadEndpoint.Kind == "" {
					dstWorkloadEndpoint = NetworkEndpoint{Kind: "workload", Namespace: dstPod.Namespace, Name: dstPod.Name}
				}
				dstServiceEndpoints = servicesForIP(flow.DstIP, serviceByIP, serviceIndex)
			} else {
				dstPodEndpoint = NetworkEndpoint{Kind: "external", Name: class}
				dstNsEndpoint = NetworkEndpoint{Kind: "external", Name: class}
				dstWorkloadEndpoint = NetworkEndpoint{Kind: "external", Name: class}
			}

			accumulateConnection(podConnections, srcPodEndpoint, dstPodEndpoint, class, flow.TxBytes, flow.RxBytes, cost)
			accumulateConnection(namespaceConnections, srcNsEndpoint, dstNsEndpoint, class, flow.TxBytes, flow.RxBytes, cost)
			accumulateConnection(workloadConnections, srcWorkloadEndpoint, dstWorkloadEndpoint, class, flow.TxBytes, flow.RxBytes, cost)

			srcServiceEndpoints := servicesForIP(flow.SrcIP, serviceByIP, serviceIndex)
			if len(srcServiceEndpoints) > 0 && len(dstServiceEndpoints) > 0 {
				for _, srcSvc := range srcServiceEndpoints {
					for _, dstSvc := range dstServiceEndpoints {
						accumulateConnection(serviceConnections, srcSvc, dstSvc, class, flow.TxBytes, flow.RxBytes, cost)
					}
				}
			} else if len(srcServiceEndpoints) > 0 {
				dstExternal := NetworkEndpoint{Kind: "external", Name: class}
				for _, srcSvc := range srcServiceEndpoints {
					accumulateConnection(serviceConnections, srcSvc, dstExternal, class, flow.TxBytes, flow.RxBytes, cost)
				}
			}
		}
	}

	if len(networkUsage) == 0 {
		networkUsage = aggregatePodUsageFromFlows(networkCollection.Flows, podInfoByIP)
	}

	for _, pod := range pods {
		if skipPod(pod) {
			continue
		}
		netKey := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
		netUsage := networkUsage[netKey]
		if netUsage.TxBytes > 0 && len(netUsage.TxBytesByClass) == 0 {
			netUsage.TxBytesByClass = map[string]uint64{network.TrafficClassUnknown: netUsage.TxBytes}
		}
		if netUsage.RxBytes > 0 && len(netUsage.RxBytesByClass) == 0 {
			netUsage.RxBytesByClass = map[string]uint64{network.TrafficClassUnknown: netUsage.RxBytes}
		}
		if netUsage.TxBytes > 0 || netUsage.RxBytes > 0 {
			ns := nsRecords[pod.Namespace]
			ns.NetworkTxBytes += netUsage.TxBytes
			ns.NetworkRxBytes += netUsage.RxBytes
			clusterNetTx += netUsage.TxBytes
			clusterNetRx += netUsage.RxBytes
		}

		podNetTotals := map[string]*NetworkClassTotals{}
		var podNetCost float64
		for class, txBytes := range netUsage.TxBytesByClass {
			cost := b.netPrices.EgressCost(class, txBytes)
			podNetCost += cost
			accumulateNetworkTotals(podNetTotals, class, txBytes, 0, cost)
			accumulateNetworkTotals(networkByClass, class, txBytes, 0, cost)
			nsRecord := ensureNamespaceNetwork(networkByNamespace, pod.Namespace)
			accumulateNetworkTotals(nsRecord.byClass, class, txBytes, 0, cost)
			nsRecord.record.TxBytes += txBytes
			nsRecord.record.EgressCostHourly += cost
			ns := nsRecords[pod.Namespace]
			ns.NetworkEgressCost += cost
		}
		for class, rxBytes := range netUsage.RxBytesByClass {
			accumulateNetworkTotals(podNetTotals, class, 0, rxBytes, 0)
			accumulateNetworkTotals(networkByClass, class, 0, rxBytes, 0)
			nsRecord := ensureNamespaceNetwork(networkByNamespace, pod.Namespace)
			accumulateNetworkTotals(nsRecord.byClass, class, 0, rxBytes, 0)
			nsRecord.record.RxBytes += rxBytes
		}

		podNetworkRecords = append(podNetworkRecords, PodNetworkRecord{
			Namespace:        pod.Namespace,
			Pod:              pod.Name,
			Node:             pod.Spec.NodeName,
			TxBytes:          netUsage.TxBytes,
			RxBytes:          netUsage.RxBytes,
			EgressCostHourly: podNetCost,
			ByClass:          flattenNetworkTotals(podNetTotals),
		})
		clusterNetCost += podNetCost
	}

	namespacesOut := make([]NamespaceCostRecord, 0, len(nsRecords))
	for _, ns := range nsRecords {
		namespacesOut = append(namespacesOut, *ns)
	}
	sort.Slice(namespacesOut, func(i, j int) bool {
		return namespacesOut[i].Namespace < namespacesOut[j].Namespace
	})

	namespacesNetwork := make([]NamespaceNetworkRecord, 0, len(networkByNamespace))
	for _, record := range networkByNamespace {
		record.record.ByClass = flattenNetworkTotals(record.byClass)
		namespacesNetwork = append(namespacesNetwork, record.record)
	}
	sort.Slice(namespacesNetwork, func(i, j int) bool {
		return namespacesNetwork[i].Namespace < namespacesNetwork[j].Namespace
	})

	sort.Slice(podNetworkRecords, func(i, j int) bool {
		if podNetworkRecords[i].Namespace == podNetworkRecords[j].Namespace {
			return podNetworkRecords[i].Pod < podNetworkRecords[j].Pod
		}
		return podNetworkRecords[i].Namespace < podNetworkRecords[j].Namespace
	})

	return Snapshot{
		Timestamp:  generatedAt,
		Namespaces: namespacesOut,
		Nodes:      nodesOut,
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
		Network: NetworkSnapshot{
			ClusterID:            b.clusterID,
			TxBytes:              clusterNetTx,
			RxBytes:              clusterNetRx,
			EgressCost:           clusterNetCost,
			ByClass:              flattenNetworkTotals(networkByClass),
			Pods:                 podNetworkRecords,
			Namespaces:           namespacesNetwork,
			PodConnections:       flattenConnections(podConnections),
			WorkloadConnections:  flattenConnections(workloadConnections),
			NamespaceConnections: flattenConnections(namespaceConnections),
			ServiceConnections:   flattenConnections(serviceConnections),
		},
	}
}

type nodeAggregate struct {
	record           NodeCostRecord
	podCount         int
	cpuUsageMilli    int64
	memoryUsageBytes int64
}

func ensureNamespace(set map[string]*NamespaceCostRecord, clusterID, name string, classifier *EnvironmentClassifier) *NamespaceCostRecord {
	if ns, ok := set[name]; ok {
		return ns
	}
	labels := map[string]string{}
	ns := &NamespaceCostRecord{
		ClusterID:   clusterID,
		Namespace:   name,
		Labels:      labels,
		Environment: classifier.Classify(name, nil),
	}
	set[name] = ns
	return ns
}

type namespaceNetworkAggregate struct {
	record  NamespaceNetworkRecord
	byClass map[string]*NetworkClassTotals
}

func ensureNamespaceNetwork(set map[string]*namespaceNetworkAggregate, name string) *namespaceNetworkAggregate {
	if ns, ok := set[name]; ok {
		return ns
	}
	ns := &namespaceNetworkAggregate{
		record:  NamespaceNetworkRecord{Namespace: name},
		byClass: map[string]*NetworkClassTotals{},
	}
	set[name] = ns
	return ns
}

func accumulateNetworkTotals(target map[string]*NetworkClassTotals, class string, txBytes, rxBytes uint64, cost float64) {
	if class == "" {
		class = network.TrafficClassUnknown
	}
	totals := target[class]
	if totals == nil {
		totals = &NetworkClassTotals{Class: class}
		target[class] = totals
	}
	totals.TxBytes += txBytes
	totals.RxBytes += rxBytes
	totals.EgressCostHourly += cost
}

func flattenNetworkTotals(totals map[string]*NetworkClassTotals) []NetworkClassTotals {
	if len(totals) == 0 {
		return nil
	}
	result := make([]NetworkClassTotals, 0, len(totals))
	for _, item := range totals {
		result = append(result, *item)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Class < result[j].Class
	})
	return result
}

type connectionKey struct {
	srcKind      string
	srcNamespace string
	srcName      string
	dstKind      string
	dstNamespace string
	dstName      string
	class        string
}

func accumulateConnection(target map[connectionKey]*NetworkConnection, src, dst NetworkEndpoint, class string, txBytes, rxBytes uint64, cost float64) {
	if class == "" {
		class = network.TrafficClassUnknown
	}
	key := connectionKey{
		srcKind:      src.Kind,
		srcNamespace: src.Namespace,
		srcName:      src.Name,
		dstKind:      dst.Kind,
		dstNamespace: dst.Namespace,
		dstName:      dst.Name,
		class:        class,
	}
	conn := target[key]
	if conn == nil {
		conn = &NetworkConnection{
			Source:      src,
			Destination: dst,
			Class:       class,
		}
		target[key] = conn
	}
	conn.TxBytes += txBytes
	conn.RxBytes += rxBytes
	conn.EgressCostHourly += cost
}

func flattenConnections(target map[connectionKey]*NetworkConnection) []NetworkConnection {
	if len(target) == 0 {
		return nil
	}
	result := make([]NetworkConnection, 0, len(target))
	for _, conn := range target {
		result = append(result, *conn)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Source.Kind == result[j].Source.Kind {
			if result[i].Source.Namespace == result[j].Source.Namespace {
				if result[i].Source.Name == result[j].Source.Name {
					if result[i].Destination.Kind == result[j].Destination.Kind {
						if result[i].Destination.Namespace == result[j].Destination.Namespace {
							if result[i].Destination.Name == result[j].Destination.Name {
								return result[i].Class < result[j].Class
							}
							return result[i].Destination.Name < result[j].Destination.Name
						}
						return result[i].Destination.Namespace < result[j].Destination.Namespace
					}
					return result[i].Destination.Kind < result[j].Destination.Kind
				}
				return result[i].Source.Name < result[j].Source.Name
			}
			return result[i].Source.Namespace < result[j].Source.Namespace
		}
		return result[i].Source.Kind < result[j].Source.Kind
	})
	return result
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

func buildServiceIndex(endpoints []*discoveryv1.EndpointSlice) map[netip.Addr][]string {
	result := map[netip.Addr][]string{}
	for _, ep := range endpoints {
		if ep == nil {
			continue
		}
		svcName := ep.Labels["kubernetes.io/service-name"]
		if svcName == "" {
			continue
		}
		svcKey := serviceKey(ep.Namespace, svcName)
		for _, endpoint := range ep.Endpoints {
			for _, addr := range endpoint.Addresses {
				ip, err := netip.ParseAddr(addr)
				if err != nil {
					continue
				}
				result[ip] = append(result[ip], svcKey)
			}
		}
	}
	return result
}

func servicesForIP(ip netip.Addr, index map[netip.Addr][]string, svcIndex map[string]NetworkEndpoint) []NetworkEndpoint {
	keys := index[ip]
	if len(keys) == 0 {
		return nil
	}
	result := make([]NetworkEndpoint, 0, len(keys))
	for _, key := range keys {
		if svc, ok := svcIndex[key]; ok {
			result = append(result, svc)
		}
	}
	return result
}

func serviceKey(namespace, name string) string {
	if namespace == "" || name == "" {
		return ""
	}
	return namespace + "/" + name
}

func workloadEndpoint(pod *corev1.Pod) NetworkEndpoint {
	if pod == nil {
		return NetworkEndpoint{}
	}
	if ctrl := metav1.GetControllerOf(pod); ctrl != nil {
		return NetworkEndpoint{
			Kind:      "workload",
			Namespace: pod.Namespace,
			Name:      fmt.Sprintf("%s/%s", ctrl.Kind, ctrl.Name),
		}
	}
	return NetworkEndpoint{
		Kind:      "workload",
		Namespace: pod.Namespace,
		Name:      fmt.Sprintf("Pod/%s", pod.Name),
	}
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
	for _, c := range pod.Spec.EphemeralContainers {
		cpuMilli += c.Resources.Requests.Cpu().MilliValue()
		memoryBytes += c.Resources.Requests.Memory().Value()
	}
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
