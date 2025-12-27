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

// PodNetworkRecord provides per-pod traffic classification.
type PodNetworkRecord struct {
	Namespace        string               `json:"namespace"`
	Pod              string               `json:"pod"`
	Node             string               `json:"node"`
	TxBytes          uint64               `json:"txBytes"`
	RxBytes          uint64               `json:"rxBytes"`
	EgressCostHourly float64              `json:"egressCostHourly"`
	ByClass          []NetworkClassTotals `json:"byClass"`
}

// NamespaceNetworkRecord aggregates network usage by namespace.
type NamespaceNetworkRecord struct {
	Namespace        string               `json:"namespace"`
	TxBytes          uint64               `json:"txBytes"`
	RxBytes          uint64               `json:"rxBytes"`
	EgressCostHourly float64              `json:"egressCostHourly"`
	ByClass          []NetworkClassTotals `json:"byClass"`
}

// NetworkSnapshot provides network usage and cost details.
type NetworkSnapshot struct {
	ClusterID            string                   `json:"clusterId"`
	TxBytes              uint64                   `json:"txBytes"`
	RxBytes              uint64                   `json:"rxBytes"`
	EgressCost           float64                  `json:"egressCostHourly"`
	ByClass              []NetworkClassTotals     `json:"byClass"`
	Pods                 []PodNetworkRecord       `json:"pods"`
	Namespaces           []NamespaceNetworkRecord `json:"namespaces"`
	PodConnections       []NetworkConnection      `json:"podConnections"`
	WorkloadConnections  []NetworkConnection      `json:"workloadConnections"`
	NamespaceConnections []NetworkConnection      `json:"namespaceConnections"`
	ServiceConnections   []NetworkConnection      `json:"serviceConnections"`
}

// NetworkEndpoint identifies a connection endpoint.
type NetworkEndpoint struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
}

// NetworkConnection captures aggregated flow traffic between endpoints.
type NetworkConnection struct {
	Source           NetworkEndpoint `json:"source"`
	Destination      NetworkEndpoint `json:"destination"`
	Class            string          `json:"class"`
	TxBytes          uint64          `json:"txBytes"`
	RxBytes          uint64          `json:"rxBytes"`
	EgressCostHourly float64         `json:"egressCostHourly"`
}

// Snapshot is the unit exchanged between the builder and the HTTP API.
type Snapshot struct {
	Timestamp  time.Time             `json:"timestamp"`
	Namespaces []NamespaceCostRecord `json:"namespaces"`
	Nodes      []NodeCostRecord      `json:"nodes"`
	Resources  ResourceSnapshot      `json:"resources"`
	Network    NetworkSnapshot       `json:"network"`
}
