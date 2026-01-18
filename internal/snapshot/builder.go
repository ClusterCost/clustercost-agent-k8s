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

// BuilderConfig captures snapshot builder configuration.
type BuilderConfig struct {
	ClusterID       string
	NetworkDetailed bool
}

// Builder converts informer/lister state into the public snapshot model.
type Builder struct {
	clusterID       string
	networkDetailed bool
}

// NewBuilder returns a configured Builder.
func NewBuilder(cfg BuilderConfig) *Builder {
	return &Builder{
		clusterID:       cfg.ClusterID,
		networkDetailed: cfg.NetworkDetailed,
	}
}

// Build assembles a snapshot using the cached kubernetes objects and usage metrics.
func (b *Builder) Build(nodes []*corev1.Node, namespaces []*corev1.Namespace, pods []*corev1.Pod, services []*corev1.Service, endpoints []*discoveryv1.EndpointSlice, usage map[string]kube.PodUsage, networkCollection collector.NetworkCollection, nodeUsage map[string]kube.NodeUsage, dnsNames map[netip.Addr]string, generatedAt time.Time) Snapshot {

	// 1. Prepare Network Data (IP mappings)
	// We need to resolve IPs to find traffic categories (CrossAZ, Public, Internal)
	podInfoByIP := make(map[netip.Addr]network.PodInfo, len(pods))
	nodeZones := make(map[string]string, len(nodes))
	nodeByIP := make(map[netip.Addr]network.PodInfo, len(nodes))

	for _, node := range nodes {
		if node != nil {
			zone := node.Labels["topology.kubernetes.io/zone"]
			if zone == "" {
				zone = node.Labels["failure-domain.beta.kubernetes.io/zone"]
			}
			nodeZones[node.Name] = zone
			for _, addr := range node.Status.Addresses {
				if addr.Type != corev1.NodeInternalIP && addr.Type != corev1.NodeExternalIP {
					continue
				}
				if ip, err := netip.ParseAddr(addr.Address); err == nil {
					nodeByIP[ip] = network.PodInfo{
						Node:             node.Name,
						AvailabilityZone: zone,
					}
				}
			}
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
	aggregatedNodeNetwork := map[string]kube.PodNetworkUsage{}

	if len(networkCollection.Flows) > 0 {
		aggregatedNetwork, aggregatedNodeNetwork = collector.AggregateNetworkUsage(networkCollection.Flows, podInfoByIP, nodeByIP)
	} else {
		// Fallback if collector passed pre-aggregated values
		if len(networkCollection.PodUsage) > 0 {
			aggregatedNetwork = networkCollection.PodUsage
		}
		if len(networkCollection.NodeUsage) > 0 {
			aggregatedNodeNetwork = networkCollection.NodeUsage
		}
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
				RequestMillicores: sumCPU(pod.Spec.Containers, true),
				LimitMillicores:   sumCPU(pod.Spec.Containers, false),
				UsageMillicores:   clampUint64(podUsage.CPUUsageMilli),
			},
			Memory: MemoryMetrics{
				RSS:          podUsage.MemoryRSS,
				PageFaults:   podUsage.MemoryPageFaults,
				RequestBytes: sumMemory(pod.Spec.Containers, true),
				LimitBytes:   sumMemory(pod.Spec.Containers, false),
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

	connections := []NetworkConnection{}
	if b.networkDetailed && len(networkCollection.Flows) > 0 {
		connections = b.buildNetworkConnections(networkCollection.Flows, podInfoByIP, nodeByIP, services, endpoints, dnsNames)
	}

	nodeMetrics := buildNodeMetrics(nodes, pods, nodeUsage, aggregatedNodeNetwork)

	return Snapshot{
		Timestamp:   generatedAt,
		Pods:        podMetrics,
		Nodes:       nodeMetrics,
		Connections: connections,
	}
}

func (b *Builder) buildNetworkConnections(flows []network.Flow, podByIP, nodeByIP map[netip.Addr]network.PodInfo, services []*corev1.Service, endpoints []*discoveryv1.EndpointSlice, dnsNames map[netip.Addr]string) []NetworkConnection {
	serviceByIP := buildServiceIPIndex(services)
	endpointServiceByIP := buildEndpointServiceIndex(endpoints)

	connections := make([]NetworkConnection, 0, len(flows))
	for _, flow := range flows {
		if flow.TxBytes == 0 && flow.RxBytes == 0 {
			continue
		}
		src := resolveEndpoint(flow.SrcIP, podByIP, nodeByIP, serviceByIP, endpointServiceByIP, dnsNames)
		dst := resolveEndpoint(flow.DstIP, podByIP, nodeByIP, serviceByIP, endpointServiceByIP, dnsNames)
		dstKind, serviceMatch := classifyDestination(flow.DstIP, podByIP, nodeByIP, serviceByIP, endpointServiceByIP)

		class := network.TrafficClassUnknown
		isEgress := false
		if srcPod, ok := podByIP[flow.SrcIP]; ok {
			class = network.ClassifyEgress(srcPod, flow.DstIP, podByIP, nodeByIP)
			if dstKind != "pod" && dstKind != "node" && dstKind != "service" {
				isEgress = true
			}
		}

		connections = append(connections, NetworkConnection{
			Src:           src,
			Dst:           dst,
			Protocol:      uint32(flow.Protocol),
			BytesSent:     flow.TxBytes,
			BytesReceived: flow.RxBytes,
			EgressClass:   class,
			DstKind:       dstKind,
			ServiceMatch:  serviceMatch,
			IsEgress:      isEgress,
		})
	}

	return connections
}

type nodeRequestTotals struct {
	cpuMilli    int64
	memoryBytes int64
}

func buildNodeMetrics(nodes []*corev1.Node, pods []*corev1.Pod, usage map[string]kube.NodeUsage, netUsage map[string]kube.PodNetworkUsage) []NodeMetric {
	requests := aggregateNodeRequests(pods)
	metrics := make([]NodeMetric, 0, len(nodes))
	for _, node := range nodes {
		if node == nil || node.Name == "" {
			continue
		}
		req := requests[node.Name]
		current := usage[node.Name]
		net := netUsage[node.Name]

		metrics = append(metrics, NodeMetric{
			NodeName:            node.Name,
			CPUUsageMillicores:  clampUint64(current.CPUUsageMilli),
			MemoryUsageBytes:    clampUint64(current.MemoryUsageBytes),
			CapacityCPUMilli:    clampUint64(node.Status.Capacity.Cpu().MilliValue()),
			CapacityMemoryBytes: clampUint64(node.Status.Capacity.Memory().Value()),
			AllocatableCPUMilli: clampUint64(node.Status.Allocatable.Cpu().MilliValue()),
			AllocatableMemBytes: clampUint64(node.Status.Allocatable.Memory().Value()),
			RequestedCPUMilli:   clampUint64(req.cpuMilli),
			RequestedMemBytes:   clampUint64(req.memoryBytes),
			ThrottlingNs:        current.ThrottledNs,
			Network: NetworkMetrics{
				BytesSent:      net.TxBytes,
				BytesReceived:  net.RxBytes,
				EgressPublic:   net.EgressPublicBytes,
				EgressCrossAZ:  net.EgressCrossAZBytes,
				EgressInternal: net.EgressInternalBytes,
			},
		})
	}
	return metrics
}

func aggregateNodeRequests(pods []*corev1.Pod) map[string]nodeRequestTotals {
	result := make(map[string]nodeRequestTotals)
	for _, pod := range pods {
		if pod == nil || pod.Spec.NodeName == "" {
			continue
		}
		var podCPU, podMem int64
		for _, c := range pod.Spec.Containers {
			podCPU += c.Resources.Requests.Cpu().MilliValue()
			podMem += c.Resources.Requests.Memory().Value()
		}
		totals := result[pod.Spec.NodeName]
		totals.cpuMilli += podCPU
		totals.memoryBytes += podMem
		result[pod.Spec.NodeName] = totals
	}
	return result
}

func clampUint64(value int64) uint64 {
	if value <= 0 {
		return 0
	}
	return uint64(value)
}

type serviceMatch struct {
	Ref   ServiceRef
	Match string
}

func buildServiceIPIndex(services []*corev1.Service) map[netip.Addr][]serviceMatch {
	result := make(map[netip.Addr][]serviceMatch)
	for _, svc := range services {
		if svc == nil {
			continue
		}
		ref := ServiceRef{Namespace: svc.Namespace, Name: svc.Name}
		for _, ipStr := range serviceClusterIPs(svc) {
			ip, err := netip.ParseAddr(ipStr)
			if err != nil {
				continue
			}
			result[ip] = appendServiceMatch(result[ip], serviceMatch{Ref: ref, Match: "cluster_ip"})
		}
		for _, ipStr := range serviceExternalIPs(svc) {
			ip, err := netip.ParseAddr(ipStr)
			if err != nil {
				continue
			}
			result[ip] = appendServiceMatch(result[ip], serviceMatch{Ref: ref, Match: "external_ip"})
		}
		for _, ipStr := range serviceLoadBalancerIPs(svc) {
			ip, err := netip.ParseAddr(ipStr)
			if err != nil {
				continue
			}
			result[ip] = appendServiceMatch(result[ip], serviceMatch{Ref: ref, Match: "load_balancer_ip"})
		}
	}
	return result
}

func serviceClusterIPs(svc *corev1.Service) []string {
	if svc == nil {
		return nil
	}
	ips := make([]string, 0, len(svc.Spec.ClusterIPs)+1)
	if svc.Spec.ClusterIP != "" && svc.Spec.ClusterIP != corev1.ClusterIPNone {
		ips = append(ips, svc.Spec.ClusterIP)
	}
	ips = append(ips, svc.Spec.ClusterIPs...)
	return ips
}

func serviceExternalIPs(svc *corev1.Service) []string {
	if svc == nil {
		return nil
	}
	ips := make([]string, 0, len(svc.Spec.ExternalIPs))
	ips = append(ips, svc.Spec.ExternalIPs...)
	return ips
}

func serviceLoadBalancerIPs(svc *corev1.Service) []string {
	if svc == nil {
		return nil
	}
	ips := make([]string, 0, len(svc.Status.LoadBalancer.Ingress))
	for _, ingress := range svc.Status.LoadBalancer.Ingress {
		if ingress.IP != "" {
			ips = append(ips, ingress.IP)
		}
	}
	return ips
}

func buildEndpointServiceIndex(endpoints []*discoveryv1.EndpointSlice) map[netip.Addr][]serviceMatch {
	result := make(map[netip.Addr][]serviceMatch)
	for _, slice := range endpoints {
		if slice == nil {
			continue
		}
		svcName := slice.Labels[discoveryv1.LabelServiceName]
		if svcName == "" {
			continue
		}
		ref := ServiceRef{Namespace: slice.Namespace, Name: svcName}
		for _, endpoint := range slice.Endpoints {
			for _, addr := range endpoint.Addresses {
				ip, err := netip.ParseAddr(addr)
				if err != nil {
					continue
				}
				result[ip] = appendServiceMatch(result[ip], serviceMatch{Ref: ref, Match: "endpoint"})
			}
		}
	}
	return result
}

func resolveEndpoint(ip netip.Addr, podByIP, nodeByIP map[netip.Addr]network.PodInfo, serviceByIP, endpointServiceByIP map[netip.Addr][]serviceMatch, dnsNames map[netip.Addr]string) NetworkEndpoint {
	endpoint := NetworkEndpoint{IP: ip.String()}
	if name, ok := dnsNames[ip]; ok && name != "" {
		endpoint.DnsName = name
	}
	if pod, ok := podByIP[ip]; ok {
		endpoint.Namespace = pod.Namespace
		endpoint.PodName = pod.Pod
		endpoint.NodeName = pod.Node
		endpoint.AvailabilityZone = pod.AvailabilityZone
	}
	if node, ok := nodeByIP[ip]; ok {
		if endpoint.NodeName == "" {
			endpoint.NodeName = node.Node
		}
		if endpoint.AvailabilityZone == "" {
			endpoint.AvailabilityZone = node.AvailabilityZone
		}
	}
	endpoint.Services = appendServiceMatches(endpoint.Services, serviceByIP[ip])
	endpoint.Services = appendServiceMatches(endpoint.Services, endpointServiceByIP[ip])
	return endpoint
}

func classifyDestination(ip netip.Addr, podByIP, nodeByIP map[netip.Addr]network.PodInfo, serviceByIP, endpointServiceByIP map[netip.Addr][]serviceMatch) (string, string) {
	if !ip.IsValid() {
		return "unknown", "none"
	}
	if _, ok := podByIP[ip]; ok {
		if matches := endpointServiceByIP[ip]; len(matches) > 0 {
			return "pod", selectServiceMatch(matches)
		}
		return "pod", "none"
	}
	if _, ok := nodeByIP[ip]; ok {
		return "node", "none"
	}
	if matches := serviceByIP[ip]; len(matches) > 0 {
		return "service", selectServiceMatch(matches)
	}
	if ip.IsPrivate() {
		return "private", "none"
	}
	if ip.IsGlobalUnicast() {
		return "external", "none"
	}
	return "unknown", "none"
}

func selectServiceMatch(matches []serviceMatch) string {
	if len(matches) == 0 {
		return "none"
	}
	seen := map[string]struct{}{}
	for _, m := range matches {
		seen[m.Match] = struct{}{}
	}
	if len(seen) == 1 {
		for key := range seen {
			return key
		}
	}
	return "multiple"
}

func appendServiceRef(list []ServiceRef, ref ServiceRef) []ServiceRef {
	for _, existing := range list {
		if existing == ref {
			return list
		}
	}
	return append(list, ref)
}

func appendServiceMatch(list []serviceMatch, match serviceMatch) []serviceMatch {
	for _, existing := range list {
		if existing.Ref == match.Ref && existing.Match == match.Match {
			return list
		}
	}
	return append(list, match)
}

func appendServiceMatches(list []ServiceRef, matches []serviceMatch) []ServiceRef {
	for _, match := range matches {
		list = appendServiceRef(list, match.Ref)
	}
	return list
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

func sumCPU(containers []corev1.Container, requests bool) uint64 {
	var total int64
	for _, c := range containers {
		if requests {
			total += c.Resources.Requests.Cpu().MilliValue()
		} else {
			total += c.Resources.Limits.Cpu().MilliValue()
		}
	}
	if total < 0 {
		return 0
	}
	return uint64(total)
}

func sumMemory(containers []corev1.Container, requests bool) uint64 {
	var total int64
	for _, c := range containers {
		if requests {
			if q, ok := c.Resources.Requests[corev1.ResourceMemory]; ok {
				total += q.Value()
			}
		} else {
			if q, ok := c.Resources.Limits[corev1.ResourceMemory]; ok {
				total += q.Value()
			}
		}
	}
	if total < 0 {
		return 0
	}
	return uint64(total)
}
