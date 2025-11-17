package snapshot

import (
	"fmt"
	"sort"
	"time"

	"clustercost-agent-k8s/internal/kube"

	corev1 "k8s.io/api/core/v1"
)

// Builder converts informer/lister state into the public snapshot model.
type Builder struct {
	clusterID  string
	classifier *EnvironmentClassifier
	prices     *NodePriceLookup
}

// NewBuilder returns a configured Builder.
func NewBuilder(clusterID string, classifier *EnvironmentClassifier, prices *NodePriceLookup) *Builder {
	return &Builder{
		clusterID:  clusterID,
		classifier: classifier,
		prices:     prices,
	}
}

// Build assembles a snapshot using the cached kubernetes objects and usage metrics.
func (b *Builder) Build(nodes []*corev1.Node, namespaces []*corev1.Namespace, pods []*corev1.Pod, usage map[string]kube.PodUsage, generatedAt time.Time) Snapshot {
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

	namespacesOut := make([]NamespaceCostRecord, 0, len(nsRecords))
	for _, ns := range nsRecords {
		namespacesOut = append(namespacesOut, *ns)
	}
	sort.Slice(namespacesOut, func(i, j int) bool {
		return namespacesOut[i].Namespace < namespacesOut[j].Namespace
	})

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
