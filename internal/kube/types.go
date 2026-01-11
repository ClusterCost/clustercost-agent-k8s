package kube

import (
	"time"

	"k8s.io/apimachinery/pkg/types"
)

// PodContainer contains a subset of container resource specifications.
type PodContainer struct {
	Name               string
	CPURequestMilli    int64
	CPULimitMilli      int64
	MemoryRequestBytes int64
	MemoryLimitBytes   int64
}

// Pod represents a simplified pod metadata payload used by the agent.
type Pod struct {
	Namespace  string
	Name       string
	UID        types.UID
	NodeName   string
	PodIP      string
	Labels     map[string]string
	OwnerKind  string
	OwnerName  string
	Containers []PodContainer
}

// Node contains relevant metadata for pricing decisions.
type Node struct {
	Name             string
	ProviderID       string
	AvailabilityZone string
	InternalIP       string
	Labels           map[string]string
	InstanceType     string
	CapacityCPU      int64 // milli-cores
	CapacityMem      int64 // bytes
	AllocatableCPU   int64 // milli-cores
	AllocatableMem   int64 // bytes
	RequestedCPU     int64 // milli-cores
	RequestedMem     int64 // bytes
}

// Namespace describes kubernetes namespaces with cost labels.
type Namespace struct {
	Name   string
	Labels map[string]string
}

// PodUsage details actual usage metrics collected from eBPF.
type PodUsage struct {
	// CPU
	CPUUsageUserNs   uint64
	CPUUsageKernelNs uint64
	CPUThrottlingNs  uint64

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

// PodNetworkUsage captures per-pod network usage and classification.
type PodNetworkUsage struct {
	TxBytes uint64
	RxBytes uint64
	// Categorized Egress
	EgressPublicBytes   uint64
	EgressCrossAZBytes  uint64
	EgressInternalBytes uint64
}

// ClusterSnapshot is a point-in-time capture of the cluster state relevant to cost.
type ClusterSnapshot struct {
	ClusterName string
	Timestamp   time.Time
	Pods        []Pod
	Namespaces  []Namespace
	Nodes       []Node
}
