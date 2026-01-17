package snapshot

import "time"

// Snapshot maps the agent's view of the world at a point in time.
type Snapshot struct {
	Timestamp   time.Time           `json:"timestamp"`
	Pods        []PodMetric         `json:"pods"`
	Nodes       []NodeMetric        `json:"nodes,omitempty"`
	Connections []NetworkConnection `json:"connections,omitempty"`
}

// PodMetric captures telemetry for a single pod.
type PodMetric struct {
	// Identity
	PodUID      string `json:"podUid"`
	ContainerID string `json:"containerId"`
	PID         uint32 `json:"pid"`
	Namespace   string `json:"namespace"`
	PodName     string `json:"podName"`

	// Compute
	Cpu    CpuMetrics    `json:"cpu"`
	Memory MemoryMetrics `json:"memory"`

	// Network
	Network NetworkMetrics `json:"network"`

	// Storage
	Storage StorageMetrics `json:"storage"`
}

type CpuMetrics struct {
	RequestMillicores uint64 `json:"requestMillicores"`
	LimitMillicores   uint64 `json:"limitMillicores"`
	UsageMillicores   uint64 `json:"usageMillicores"`
}

type MemoryMetrics struct {
	RSS          uint64 `json:"rssBytes"`
	PageFaults   uint64 `json:"pageFaultsMajor"`
	RequestBytes uint64 `json:"requestBytes"`
	LimitBytes   uint64 `json:"limitBytes"`
}

type NetworkMetrics struct {
	BytesSent      uint64 `json:"bytesSent"`
	BytesReceived  uint64 `json:"bytesReceived"`
	EgressPublic   uint64 `json:"egressPublicBytes"`
	EgressCrossAZ  uint64 `json:"egressCrossAZBytes"`
	EgressInternal uint64 `json:"egressInternalBytes"`
}

type NetworkConnection struct {
	Src           NetworkEndpoint `json:"src"`
	Dst           NetworkEndpoint `json:"dst"`
	Protocol      uint32          `json:"protocol"`
	BytesSent     uint64          `json:"bytesSent"`
	BytesReceived uint64          `json:"bytesReceived"`
	EgressClass   string          `json:"egressClass"`
	DstKind       string          `json:"dstKind"`
	ServiceMatch  string          `json:"serviceMatch"`
	IsEgress      bool            `json:"isEgress"`
}

type NetworkEndpoint struct {
	IP               string       `json:"ip"`
	DnsName          string       `json:"dnsName,omitempty"`
	Namespace        string       `json:"namespace,omitempty"`
	PodName          string       `json:"podName,omitempty"`
	NodeName         string       `json:"nodeName,omitempty"`
	AvailabilityZone string       `json:"availabilityZone,omitempty"`
	Services         []ServiceRef `json:"services,omitempty"`
}

type NodeMetric struct {
	NodeName            string `json:"nodeName"`
	CPUUsageMillicores  uint64 `json:"cpuUsageMillicores"`
	MemoryUsageBytes    uint64 `json:"memoryUsageBytes"`
	CapacityCPUMilli    uint64 `json:"capacityCpuMillicores"`
	CapacityMemoryBytes uint64 `json:"capacityMemoryBytes"`
	AllocatableCPUMilli uint64 `json:"allocatableCpuMillicores"`
	AllocatableMemBytes uint64 `json:"allocatableMemoryBytes"`
	RequestedCPUMilli   uint64 `json:"requestedCpuMillicores"`
	RequestedMemBytes   uint64 `json:"requestedMemoryBytes"`
	ThrottlingNs        uint64 `json:"throttlingNs"`
}

type ServiceRef struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

type StorageMetrics struct {
	ReadBytes    uint64 `json:"readBytes"`
	WriteBytes   uint64 `json:"writeBytes"`
	ReadOps      uint64 `json:"readOps"`
	WriteOps     uint64 `json:"writeOps"`
	TotalLatency uint64 `json:"totalLatencyNs"`
}
