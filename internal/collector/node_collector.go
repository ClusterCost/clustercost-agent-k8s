package collector

import (
	"context"
	"log/slog"

	"clustercost-agent-k8s/internal/kube"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NodeCollector gathers node level metadata and aggregates pod requests per node.
type NodeCollector struct {
	client *kube.Client
	logger *slog.Logger
}

// NewNodeCollector constructs a NodeCollector.
func NewNodeCollector(client *kube.Client, logger *slog.Logger) *NodeCollector {
	return &NodeCollector{client: client, logger: logger}
}

// Collect returns the list of nodes enriched with resource information.
// The optional pods slice is used to aggregate requested CPU/memory per node.
func (c *NodeCollector) Collect(ctx context.Context, pods []corev1.Pod) ([]kube.Node, error) {
	nodeList, err := c.client.Kubernetes.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	requests := aggregateNodeRequests(pods)

	nodes := make([]kube.Node, 0, len(nodeList.Items))
	for _, node := range nodeList.Items {
		req := requests[node.Name]
		nodes = append(nodes, kube.Node{
			Name:             node.Name,
			ProviderID:       node.Spec.ProviderID,
			AvailabilityZone: node.Labels["topology.kubernetes.io/zone"],
			InternalIP:       nodeInternalIP(node.Status.Addresses),
			Labels:           node.Labels,
			InstanceType:     resolveInstanceType(node.Labels),
			CapacityCPU:      node.Status.Capacity.Cpu().MilliValue(),
			CapacityMem:      node.Status.Capacity.Memory().Value(),
			AllocatableCPU:   node.Status.Allocatable.Cpu().MilliValue(),
			AllocatableMem:   node.Status.Allocatable.Memory().Value(),
			RequestedCPU:     req.cpuMilli,
			RequestedMem:     req.memoryBytes,
		})
	}

	return nodes, nil
}

type nodeRequestTotals struct {
	cpuMilli    int64
	memoryBytes int64
}

func aggregateNodeRequests(pods []corev1.Pod) map[string]nodeRequestTotals {
	result := make(map[string]nodeRequestTotals)
	for _, pod := range pods {
		if pod.Spec.NodeName == "" {
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

func nodeInternalIP(addresses []corev1.NodeAddress) string {
	for _, addr := range addresses {
		if addr.Type == corev1.NodeInternalIP && addr.Address != "" {
			return addr.Address
		}
	}
	return ""
}
