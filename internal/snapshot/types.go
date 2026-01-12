package snapshot

import "time"

// Snapshot maps the agent's view of the world at a point in time.
type Snapshot struct {
	Timestamp time.Time   `json:"timestamp"`
	Pods      []PodMetric `json:"pods"`
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
	UsageUser         uint64 `json:"usageUserNs"`
	UsageKernel       uint64 `json:"usageKernelNs"`
	Throttling        uint64 `json:"throttlingNs"`
	RequestMillicores uint64 `json:"requestMillicores"`
	LimitMillicores   uint64 `json:"limitMillicores"`
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

type StorageMetrics struct {
	ReadBytes    uint64 `json:"readBytes"`
	WriteBytes   uint64 `json:"writeBytes"`
	ReadOps      uint64 `json:"readOps"`
	WriteOps     uint64 `json:"writeOps"`
	TotalLatency uint64 `json:"totalLatencyNs"`
}
