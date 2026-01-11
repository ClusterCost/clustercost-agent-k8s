package snapshot

import "time"

// NamespaceCostRecord is the namespace-level payload required by the backend.
type NamespaceCostRecord struct {
	ClusterID          string            `json:"clusterId"`
	Namespace          string            `json:"namespace"`
	HourlyCost         float64           `json:"hourlyCost"`
	PodCount           int               `json:"podCount"`
	CPURequestMilli    int64             `json:"cpuRequestMilli"`
	MemoryRequestBytes int64             `json:"memoryRequestBytes"`
	CPUUsageMilli      int64             `json:"cpuUsageMilli"`
	MemoryUsageBytes   int64             `json:"memoryUsageBytes"`
	NetworkTxBytes     uint64            `json:"networkTxBytes"`
	NetworkRxBytes     uint64            `json:"networkRxBytes"`
	NetworkEgressCost  float64           `json:"networkEgressCostHourly"`
	Labels             map[string]string `json:"labels,omitempty"`
	Environment        string            `json:"environment,omitempty"`
}

// NodeCostRecord captures node pricing and utilization.
type NodeCostRecord struct {
	ClusterID              string            `json:"clusterId"`
	NodeName               string            `json:"nodeName"`
	HourlyCost             float64           `json:"hourlyCost"`
	CPUUsagePercent        float64           `json:"cpuUsagePercent"`
	MemoryUsagePercent     float64           `json:"memoryUsagePercent"`
	CPUAllocatableMilli    int64             `json:"cpuAllocatableMilli"`
	MemoryAllocatableBytes int64             `json:"memoryAllocatableBytes"`
	PodCount               int               `json:"podCount"`
	Status                 string            `json:"status"`
	IsUnderPressure        bool              `json:"isUnderPressure"`
	InstanceType           string            `json:"instanceType"`
	Labels                 map[string]string `json:"labels,omitempty"`
	Taints                 []string          `json:"taints,omitempty"`
}

// ResourceSnapshot stores global cluster totals.
type ResourceSnapshot struct {
	ClusterID               string  `json:"clusterId"`
	CPUUsageMilliTotal      int64   `json:"cpuUsageMilliTotal"`
	CPURequestMilliTotal    int64   `json:"cpuRequestMilliTotal"`
	MemoryUsageBytesTotal   int64   `json:"memoryUsageBytesTotal"`
	MemoryRequestBytesTotal int64   `json:"memoryRequestBytesTotal"`
	TotalNodeHourlyCost     float64 `json:"totalNodeHourlyCost"`
	NetworkTxBytesTotal     uint64  `json:"networkTxBytesTotal"`
	NetworkRxBytesTotal     uint64  `json:"networkRxBytesTotal"`
	NetworkEgressCostTotal  float64 `json:"networkEgressCostHourlyTotal"`
}

// NetworkClassTotals summarizes traffic and cost for a class.
type NetworkClassTotals struct {
	Class            string  `json:"class"`
	TxBytes          uint64  `json:"txBytes"`
	RxBytes          uint64  `json:"rxBytes"`
	EgressCostHourly float64 `json:"egressCostHourly"`
}

// PodRecord captures all metadata, resource usage, and network usage for a single pod.
type PodRecord struct {
	// Metadata
	Namespace   string            `json:"namespace"`
	Pod         string            `json:"pod"`
	Node        string            `json:"node"`
	Labels      map[string]string `json:"labels,omitempty"`
	Environment string            `json:"environment,omitempty"`
	OwnerKind   string            `json:"ownerKind,omitempty"`
	OwnerName   string            `json:"ownerName,omitempty"`

	// Resources
	CPURequestMilli    int64   `json:"cpuRequestMilli"`
	CPUUsageMilli      int64   `json:"cpuUsageMilli"`
	MemoryRequestBytes int64   `json:"memoryRequestBytes"`
	MemoryUsageBytes   int64   `json:"memoryUsageBytes"`
	ResourceHourlyCost float64 `json:"resourceHourlyCost"`

	// Network
	NetworkTxBytes          uint64               `json:"networkTxBytes"`
	NetworkRxBytes          uint64               `json:"networkRxBytes"`
	NetworkEgressCostHourly float64              `json:"networkEgressCostHourly"`
	NetworkByClass          []NetworkClassTotals `json:"networkByClass,omitempty"`

	// Total
	TotalHourlyCost float64 `json:"totalHourlyCost"`
}

// Snapshot is the unit exchanged between the builder and the HTTP API.
type Snapshot struct {
	Timestamp time.Time        `json:"timestamp"`
	Node      *NodeCostRecord  `json:"node"`
	Pods      []PodRecord      `json:"pods"`
	Resources ResourceSnapshot `json:"resources"`
}
