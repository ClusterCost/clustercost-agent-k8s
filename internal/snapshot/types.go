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
	Labels             map[string]string `json:"labels"`
	Environment        string            `json:"environment"`
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
	Labels                 map[string]string `json:"labels"`
	Taints                 []string          `json:"taints"`
}

// ResourceSnapshot stores global cluster totals.
type ResourceSnapshot struct {
	ClusterID               string  `json:"clusterId"`
	CPUUsageMilliTotal      int64   `json:"cpuUsageMilliTotal"`
	CPURequestMilliTotal    int64   `json:"cpuRequestMilliTotal"`
	MemoryUsageBytesTotal   int64   `json:"memoryUsageBytesTotal"`
	MemoryRequestBytesTotal int64   `json:"memoryRequestBytesTotal"`
	TotalNodeHourlyCost     float64 `json:"totalNodeHourlyCost"`
}

// Snapshot is the unit exchanged between the builder and the HTTP API.
type Snapshot struct {
	Timestamp  time.Time             `json:"timestamp"`
	Namespaces []NamespaceCostRecord `json:"namespaces"`
	Nodes      []NodeCostRecord      `json:"nodes"`
	Resources  ResourceSnapshot      `json:"resources"`
}
