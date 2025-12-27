package collector

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"clustercost-agent-k8s/internal/kube"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ClusterCollector reads kubernetes resources that feed the cost calculations.
type ClusterCollector struct {
	client        *kube.Client
	logger        *slog.Logger
	nodeCollector *NodeCollector
}

// NewClusterCollector returns a configured collector.
func NewClusterCollector(client *kube.Client, logger *slog.Logger) *ClusterCollector {
	return &ClusterCollector{
		client:        client,
		logger:        logger,
		nodeCollector: NewNodeCollector(client, logger),
	}
}

// Collect fetches pods, namespaces, and nodes in a single snapshot.
func (c *ClusterCollector) Collect(ctx context.Context) (kube.ClusterSnapshot, error) {
	start := time.Now()

	pods, err := c.client.Kubernetes.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return kube.ClusterSnapshot{}, fmt.Errorf("list pods: %w", err)
	}

	namespaces, err := c.client.Kubernetes.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return kube.ClusterSnapshot{}, fmt.Errorf("list namespaces: %w", err)
	}

	snapshot := kube.ClusterSnapshot{
		ClusterName: c.client.ClusterName,
		Timestamp:   time.Now(),
		Pods:        make([]kube.Pod, 0, len(pods.Items)),
		Namespaces:  make([]kube.Namespace, 0, len(namespaces.Items)),
	}

	nsMap := map[string]kube.Namespace{}
	for _, ns := range namespaces.Items {
		nsMap[ns.Name] = kube.Namespace{
			Name:   ns.Name,
			Labels: ns.Labels,
		}
		snapshot.Namespaces = append(snapshot.Namespaces, nsMap[ns.Name])
	}

	for _, pod := range pods.Items {
		snapshot.Pods = append(snapshot.Pods, kube.Pod{
			Namespace:  pod.Namespace,
			Name:       pod.Name,
			UID:        pod.UID,
			NodeName:   pod.Spec.NodeName,
			PodIP:      pod.Status.PodIP,
			Labels:     pod.Labels,
			OwnerKind:  controllerKind(&pod),
			OwnerName:  controllerName(&pod),
			Containers: extractContainers(pod.Spec.Containers),
		})
	}

	nodeInfos, err := c.nodeCollector.Collect(ctx, pods.Items)
	if err != nil {
		return kube.ClusterSnapshot{}, fmt.Errorf("collect nodes: %w", err)
	}
	snapshot.Nodes = nodeInfos

	c.logger.Debug("collected cluster snapshot", slog.Duration("duration", time.Since(start)))
	return snapshot, nil
}

func extractContainers(containers []corev1.Container) []kube.PodContainer {
	result := make([]kube.PodContainer, 0, len(containers))
	for _, c := range containers {
		requests := c.Resources.Requests
		limits := c.Resources.Limits
		result = append(result, kube.PodContainer{
			Name:               c.Name,
			CPURequestMilli:    requests.Cpu().MilliValue(),
			CPULimitMilli:      limits.Cpu().MilliValue(),
			MemoryRequestBytes: requests.Memory().Value(),
			MemoryLimitBytes:   limits.Memory().Value(),
		})
	}
	return result
}

func controllerKind(pod *corev1.Pod) string {
	if ctrl := metav1.GetControllerOf(pod); ctrl != nil {
		return ctrl.Kind
	}
	return ""
}

func controllerName(pod *corev1.Pod) string {
	if ctrl := metav1.GetControllerOf(pod); ctrl != nil {
		return ctrl.Name
	}
	return ""
}

func resolveInstanceType(labels map[string]string) string {
	if t, ok := labels["node.kubernetes.io/instance-type"]; ok {
		return t
	}
	if t, ok := labels["beta.kubernetes.io/instance-type"]; ok {
		return t
	}
	return ""
}
