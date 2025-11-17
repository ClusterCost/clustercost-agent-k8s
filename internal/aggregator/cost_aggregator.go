package aggregator

import (
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"clustercost-agent-k8s/internal/enricher"
	"clustercost-agent-k8s/internal/kube"
	"clustercost-agent-k8s/internal/pricing"
)

// PodCost represents cost and usage for a single pod.
type PodCost struct {
	Namespace          string            `json:"namespace"`
	Pod                string            `json:"pod"`
	Node               string            `json:"node"`
	HourlyCost         float64           `json:"hourlyCost"`
	CPURequestMilli    int64             `json:"cpuRequestMilli"`
	MemoryRequestBytes int64             `json:"memoryRequestBytes"`
	CPUUsageMilli      int64             `json:"cpuUsageMilli"`
	MemoryUsageBytes   int64             `json:"memoryUsageBytes"`
	Labels             map[string]string `json:"labels"`
	ControllerKind     string            `json:"controllerKind"`
	ControllerName     string            `json:"controllerName"`
}

// NamespaceCost aggregates cost at the namespace scope.
type NamespaceCost struct {
	Namespace          string            `json:"namespace"`
	HourlyCost         float64           `json:"hourlyCost"`
	PodCount           int               `json:"podCount"`
	CPURequestMilli    int64             `json:"cpuRequestMilli"`
	MemoryRequestBytes int64             `json:"memoryRequestBytes"`
	CPUUsageMilli      int64             `json:"cpuUsageMilli"`
	MemoryUsageBytes   int64             `json:"memoryUsageBytes"`
	Labels             map[string]string `json:"labels"`
}

// NodeCost aggregates pod cost on a node along with utilization metrics.
type NodeCost struct {
	Name                 string            `json:"name"`
	InstanceType         string            `json:"instanceType"`
	AvailabilityZone     string            `json:"availabilityZone"`
	ProviderID           string            `json:"providerId"`
	Labels               map[string]string `json:"labels"`
	RawNodePriceHourly   float64           `json:"rawNodePriceHourly"`
	AllocatedCostHourly  float64           `json:"allocatedCostHourly"`
	CPUAllocatableCores  float64           `json:"cpuAllocatableCores"`
	CPURequestedCores    float64           `json:"cpuRequestedCores"`
	CPUUsedCores         float64           `json:"cpuUsedCores"`
	MemoryAllocatableGiB float64           `json:"memoryAllocatableGiB"`
	MemoryRequestedGiB   float64           `json:"memoryRequestedGiB"`
	MemoryUsedGiB        float64           `json:"memoryUsedGiB"`
}

// LabelCost stores aggregation per logical label.
type LabelCost struct {
	Key        string  `json:"key"`
	Value      string  `json:"value"`
	HourlyCost float64 `json:"hourlyCost"`
}

// InstanceTypeCost aggregates raw + allocated cost per instance family.
type InstanceTypeCost struct {
	InstanceType        string  `json:"instanceType"`
	NodeCount           int     `json:"nodeCount"`
	RawHourlyCost       float64 `json:"rawHourlyCost"`
	AllocatedHourlyCost float64 `json:"allocatedHourlyCost"`
}

// ClusterCostSummary captures the overall cluster picture.
type ClusterCostSummary struct {
	ClusterName          string                 `json:"clusterName"`
	Provider             string                 `json:"provider"`
	Region               string                 `json:"region"`
	HourlyCost           float64                `json:"hourlyCost"`
	TotalCPURequestMilli int64                  `json:"totalCpuRequestMilli"`
	TotalMemoryRequestB  int64                  `json:"totalMemoryRequestBytes"`
	PodCount             int                    `json:"podCount"`
	NodeCount            int                    `json:"nodeCount"`
	GeneratedAtUnix      int64                  `json:"generatedAtUnix"`
	CostByInstanceType   []InstanceTypeCost     `json:"costByInstanceType"`
	CostByLabel          map[string][]LabelCost `json:"costByLabel"`
}

// AggregatedData holds the latest aggregation results.
type AggregatedData struct {
	GeneratedAt time.Time          `json:"generatedAt"`
	Cluster     ClusterCostSummary `json:"cluster"`
	Namespaces  []NamespaceCost    `json:"namespaces"`
	Pods        []PodCost          `json:"pods"`
	Nodes       []NodeCost         `json:"nodes"`
	Labels      []LabelCost        `json:"labels"`
}

// WorkloadCost represents the aggregate cost/usage of a kubernetes workload (controller).
type WorkloadCost struct {
	Namespace          string   `json:"namespace"`
	WorkloadKind       string   `json:"workloadKind"`
	WorkloadName       string   `json:"workloadName"`
	Team               string   `json:"team,omitempty"`
	Env                string   `json:"env,omitempty"`
	Replicas           int      `json:"replicas"`
	HourlyCost         float64  `json:"hourlyCost"`
	CPURequestedCores  float64  `json:"cpuRequestedCores"`
	CPUUsedCores       float64  `json:"cpuUsedCores"`
	MemoryRequestedGiB float64  `json:"memoryRequestedGiB"`
	MemoryUsedGiB      float64  `json:"memoryUsedGiB"`
	Nodes              []string `json:"nodes"`
}

// CostAggregator coordinates aggregations and stores the latest snapshot.
type CostAggregator struct {
	mu         sync.RWMutex
	data       AggregatedData
	calc       *pricing.Calculator
	enricher   *enricher.LabelEnricher
	nodePricer pricing.NodePriceResolver
	provider   string
	region     string
	logger     *slog.Logger
}

// NewCostAggregator returns a configured aggregator.
func NewCostAggregator(calc *pricing.Calculator, enricher *enricher.LabelEnricher, nodePricer pricing.NodePriceResolver, provider, region string, logger *slog.Logger) *CostAggregator {
	return &CostAggregator{
		calc:       calc,
		enricher:   enricher,
		nodePricer: nodePricer,
		provider:   provider,
		region:     region,
		logger:     logger,
	}
}

// Aggregate computes fresh cost data and stores it internally.
func (a *CostAggregator) Aggregate(snapshot kube.ClusterSnapshot, usage map[string]kube.PodUsage) AggregatedData {
	now := snapshot.Timestamp
	podCosts := make([]PodCost, 0, len(snapshot.Pods))
	nsMap := make(map[string]*NamespaceCost)
	nodeMap := make(map[string]*NodeCost)
	labelMap := make(map[string]map[string]float64)
	instanceTypeTotals := make(map[string]*InstanceTypeCost)
	missingInstanceLogged := map[string]struct{}{}

	nsLabels := map[string]map[string]string{}
	for _, ns := range snapshot.Namespaces {
		nsCopy := make(map[string]string, len(ns.Labels))
		for k, v := range ns.Labels {
			nsCopy[k] = v
		}
		nsLabels[ns.Name] = nsCopy
	}

	for _, node := range snapshot.Nodes {
		nodeCopy := make(map[string]string, len(node.Labels))
		for k, v := range node.Labels {
			nodeCopy[k] = v
		}
		nodeMap[node.Name] = a.prepareNode(node, nodeCopy, missingInstanceLogged)
	}

	usagePerNode := make(map[string]nodeUsageTotals)

	var clusterCost float64
	var totalCPU, totalMem int64

	for _, pod := range snapshot.Pods {
		cpuReq, memReq := pricing.PodRequestTotals(pod)
		totalCPU += cpuReq
		totalMem += memReq

		key := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
		podUsage := usage[key]
		cpuUsage := podUsage.CPUUsageMilli
		memUsage := podUsage.MemoryUsageBytes
		if cpuUsage == 0 {
			cpuUsage = cpuReq
		}
		if memUsage == 0 {
			memUsage = memReq
		}
		cost := a.calc.CostFor(cpuUsage, memUsage)

		labels := a.enricher.Merge(nsLabels[pod.Namespace], pod.Labels)
		podCost := PodCost{
			Namespace:          pod.Namespace,
			Pod:                pod.Name,
			Node:               pod.NodeName,
			HourlyCost:         cost,
			CPURequestMilli:    cpuReq,
			MemoryRequestBytes: memReq,
			CPUUsageMilli:      cpuUsage,
			MemoryUsageBytes:   memUsage,
			Labels:             labels,
			ControllerKind:     pod.OwnerKind,
			ControllerName:     pod.OwnerName,
		}
		podCosts = append(podCosts, podCost)

		nsTotals := ensureNamespace(nsMap, pod.Namespace, labels)
		nsTotals.HourlyCost += cost
		nsTotals.PodCount++
		nsTotals.CPURequestMilli += cpuReq
		nsTotals.MemoryRequestBytes += memReq
		nsTotals.CPUUsageMilli += cpuUsage
		nsTotals.MemoryUsageBytes += memUsage

		nodeTotals := ensureNode(nodeMap, pod.NodeName)
		nodeTotals.AllocatedCostHourly += cost

		for k, v := range labels {
			if v == "" {
				continue
			}
			if _, ok := labelMap[k]; !ok {
				labelMap[k] = make(map[string]float64)
			}
			labelMap[k][v] += cost
		}

		clusterCost += cost
		u := usagePerNode[pod.NodeName]
		u.cpuMilli += cpuUsage
		u.memoryBytes += memUsage
		usagePerNode[pod.NodeName] = u
	}

	namespaces := make([]NamespaceCost, 0, len(nsMap))
	for _, ns := range nsMap {
		namespaces = append(namespaces, *ns)
	}
	sort.Slice(namespaces, func(i, j int) bool { return namespaces[i].Namespace < namespaces[j].Namespace })

	nodes := make([]NodeCost, 0, len(nodeMap))
	for name, u := range usagePerNode {
		if node, ok := nodeMap[name]; ok {
			node.CPUUsedCores = coresFromMilli(u.cpuMilli)
			node.MemoryUsedGiB = gibFromBytes(u.memoryBytes)
		}
	}
	for _, node := range nodeMap {
		nodes = append(nodes, *node)
		if node.InstanceType != "" {
			it := instanceTypeTotals[node.InstanceType]
			if it == nil {
				it = &InstanceTypeCost{InstanceType: node.InstanceType}
				instanceTypeTotals[node.InstanceType] = it
			}
			it.NodeCount++
			it.RawHourlyCost += node.RawNodePriceHourly
			it.AllocatedHourlyCost += node.AllocatedCostHourly
		}
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Name < nodes[j].Name })

	labels := make([]LabelCost, 0)
	for key, values := range labelMap {
		for value, cst := range values {
			labels = append(labels, LabelCost{Key: key, Value: value, HourlyCost: cst})
		}
	}
	sort.Slice(labels, func(i, j int) bool {
		if labels[i].Key == labels[j].Key {
			return labels[i].Value < labels[j].Value
		}
		return labels[i].Key < labels[j].Key
	})

	data := AggregatedData{
		GeneratedAt: now,
		Cluster: ClusterCostSummary{
			ClusterName:          snapshot.ClusterName,
			Provider:             a.provider,
			Region:               a.region,
			HourlyCost:           clusterCost,
			TotalCPURequestMilli: totalCPU,
			TotalMemoryRequestB:  totalMem,
			PodCount:             len(snapshot.Pods),
			NodeCount:            len(snapshot.Nodes),
			GeneratedAtUnix:      now.Unix(),
			CostByInstanceType:   flattenInstanceTypeTotals(instanceTypeTotals),
			CostByLabel:          filterCostByLabel(labelMap, []string{"team", "env"}),
		},
		Namespaces: namespaces,
		Pods:       podCosts,
		Nodes:      nodes,
		Labels:     labels,
	}

	a.mu.Lock()
	a.data = data
	a.mu.Unlock()

	a.logger.Debug("aggregated costs", slog.Float64("cluster_cost_hourly", clusterCost))
	return data
}

// Data returns the latest aggregated data.
func (a *CostAggregator) Data() AggregatedData {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.data
}

// Workloads returns workload-level aggregations derived from the latest pod data.
func (a *CostAggregator) Workloads() []WorkloadCost {
	return WorkloadsFromPods(a.Data().Pods)
}

func ensureNamespace(cache map[string]*NamespaceCost, name string, labels map[string]string) *NamespaceCost {
	if ns, ok := cache[name]; ok {
		return ns
	}
	ns := &NamespaceCost{Namespace: name, Labels: labels}
	cache[name] = ns
	return ns
}

func ensureNode(cache map[string]*NodeCost, name string) *NodeCost {
	if node, ok := cache[name]; ok {
		return node
	}
	node := &NodeCost{Name: name, Labels: map[string]string{}}
	cache[name] = node
	return node
}

type nodeUsageTotals struct {
	cpuMilli    int64
	memoryBytes int64
}

func (a *CostAggregator) prepareNode(node kube.Node, labels map[string]string, missing map[string]struct{}) *NodeCost {
	var price float64
	if a.nodePricer != nil && node.InstanceType != "" {
		if p, ok := a.nodePricer.NodeHourlyPrice(node.InstanceType); ok {
			price = p
		} else {
			if _, logged := missing[node.InstanceType]; !logged {
				a.logger.Warn("missing node price", slog.String("instance_type", node.InstanceType), slog.String("region", a.region))
				missing[node.InstanceType] = struct{}{}
			}
		}
	}
	return &NodeCost{
		Name:                 node.Name,
		InstanceType:         node.InstanceType,
		AvailabilityZone:     node.AvailabilityZone,
		ProviderID:           node.ProviderID,
		Labels:               labels,
		RawNodePriceHourly:   price,
		AllocatedCostHourly:  0,
		CPUAllocatableCores:  coresFromMilli(node.AllocatableCPU),
		CPURequestedCores:    coresFromMilli(node.RequestedCPU),
		MemoryAllocatableGiB: gibFromBytes(node.AllocatableMem),
		MemoryRequestedGiB:   gibFromBytes(node.RequestedMem),
	}
}

func flattenInstanceTypeTotals(m map[string]*InstanceTypeCost) []InstanceTypeCost {
	result := make([]InstanceTypeCost, 0, len(m))
	for _, v := range m {
		result = append(result, *v)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].InstanceType < result[j].InstanceType })
	return result
}

func filterCostByLabel(all map[string]map[string]float64, keys []string) map[string][]LabelCost {
	result := make(map[string][]LabelCost)
	for _, key := range keys {
		values := all[key]
		if len(values) == 0 {
			continue
		}
		list := make([]LabelCost, 0, len(values))
		for value, cost := range values {
			list = append(list, LabelCost{Key: key, Value: value, HourlyCost: cost})
		}
		sort.Slice(list, func(i, j int) bool { return list[i].Value < list[j].Value })
		result[key] = list
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func coresFromMilli(milli int64) float64 {
	return float64(milli) / 1000.0
}

const bytesInGiB = 1024 * 1024 * 1024

func gibFromBytes(bytes int64) float64 {
	return float64(bytes) / bytesInGiB
}

// WorkloadsFromPods aggregates workloads from pod costs.
func WorkloadsFromPods(pods []PodCost) []WorkloadCost {
	type key struct {
		namespace string
		kind      string
		name      string
	}
	type agg struct {
		cost      float64
		replicas  int
		cpuReq    int64
		cpuUse    int64
		memReq    int64
		memUse    int64
		nodes     map[string]struct{}
		labelFreq map[string]map[string]int
	}
	result := make(map[key]*agg)

	for _, pod := range pods {
		if pod.ControllerKind == "" || pod.ControllerName == "" {
			continue
		}
		k := key{namespace: pod.Namespace, kind: pod.ControllerKind, name: pod.ControllerName}
		if _, ok := result[k]; !ok {
			result[k] = &agg{
				nodes:     map[string]struct{}{},
				labelFreq: map[string]map[string]int{"team": {}, "env": {}},
			}
		}
		a := result[k]
		a.cost += pod.HourlyCost
		a.replicas++
		a.cpuReq += pod.CPURequestMilli
		a.cpuUse += pod.CPUUsageMilli
		a.memReq += pod.MemoryRequestBytes
		a.memUse += pod.MemoryUsageBytes
		if pod.Node != "" {
			a.nodes[pod.Node] = struct{}{}
		}
		for _, labelKey := range []string{"team", "env"} {
			if val := pod.Labels[labelKey]; val != "" {
				a.labelFreq[labelKey][val]++
			}
		}
	}

	workloads := make([]WorkloadCost, 0, len(result))
	for k, agg := range result {
		nodes := make([]string, 0, len(agg.nodes))
		for node := range agg.nodes {
			nodes = append(nodes, node)
		}
		sort.Strings(nodes)

		workloads = append(workloads, WorkloadCost{
			Namespace:          k.namespace,
			WorkloadKind:       k.kind,
			WorkloadName:       k.name,
			Team:               mostCommonValue(agg.labelFreq["team"]),
			Env:                mostCommonValue(agg.labelFreq["env"]),
			Replicas:           agg.replicas,
			HourlyCost:         agg.cost,
			CPURequestedCores:  coresFromMilli(agg.cpuReq),
			CPUUsedCores:       coresFromMilli(agg.cpuUse),
			MemoryRequestedGiB: gibFromBytes(agg.memReq),
			MemoryUsedGiB:      gibFromBytes(agg.memUse),
			Nodes:              nodes,
		})
	}

	sort.Slice(workloads, func(i, j int) bool {
		if workloads[i].Namespace == workloads[j].Namespace {
			if workloads[i].WorkloadKind == workloads[j].WorkloadKind {
				return workloads[i].WorkloadName < workloads[j].WorkloadName
			}
			return workloads[i].WorkloadKind < workloads[j].WorkloadKind
		}
		return workloads[i].Namespace < workloads[j].Namespace
	})
	return workloads
}

func mostCommonValue(freq map[string]int) string {
	var (
		bestValue string
		bestCount int
	)
	for value, count := range freq {
		if count > bestCount || (count == bestCount && value < bestValue) {
			bestValue = value
			bestCount = count
		}
	}
	return bestValue
}
