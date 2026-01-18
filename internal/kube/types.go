package kube

// PodUsage details actual usage metrics collected from cgroups.
type PodUsage struct {
	// CPU
	CPUUsageMilli int64

	// Memory
	MemoryRSS        uint64
	MemoryPageFaults uint64

	// Storage
	StorageReadBytes    uint64
	StorageWriteBytes   uint64
	StorageReadOps      uint64
	StorageWriteOps     uint64
	StorageTotalLatency uint64
}

// NodeUsage captures node-level CPU and memory usage.
type NodeUsage struct {
	CPUUsageMilli    int64
	MemoryUsageBytes int64
	ThrottledNs      uint64
}

// PodNetworkUsage captures per-pod network usage and classification.
type PodNetworkUsage struct {
	TxBytes uint64
	RxBytes uint64
	// Categorized Egress
	EgressPublicBytes   uint64
	EgressCrossAZBytes  uint64
	EgressInternalBytes uint64
}
