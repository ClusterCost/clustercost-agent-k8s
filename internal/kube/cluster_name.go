package kube

import (
	"context"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// DetectClusterName tries to infer the human friendly cluster name from Kubernetes state.
// For now, only node label/annotation heuristics are used.
func DetectClusterName(ctx context.Context, client kubernetes.Interface) (string, error) {
	name, err := clusterNameFromNodes(ctx, client)
	if err != nil {
		return "", err
	}
	if name == "" {
		return "", fmt.Errorf("cluster name not discovered")
	}
	return name, nil
}

var nodeClusterLabelCandidates = []string{
	"kubernetes.azure.com/cluster",
	"alpha.eksctl.io/cluster-name",
	"eks.amazonaws.com/cluster-name",
	"cluster.x-k8s.io/cluster-name",
	"kops.k8s.io/cluster",
	"kops.k8s.io/cluster-name",
	"kubeone.io/cluster-name",
	"microk8s.io/cluster-name",
}

func clusterNameFromNodes(ctx context.Context, client kubernetes.Interface) (string, error) {
	nodes, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", err
	}
	for _, node := range nodes.Items {
		for _, key := range nodeClusterLabelCandidates {
			if value := strings.TrimSpace(node.Labels[key]); value != "" {
				return value, nil
			}
		}
		if value := strings.TrimSpace(node.Annotations["alpha.eksctl.io/cluster-name"]); value != "" {
			return value, nil
		}
	}
	return "", nil
}

// GetClusterID returns the unique identifier of the cluster,
// typically using the kube-system namespace UID.
func GetClusterID(ctx context.Context, client kubernetes.Interface) (string, error) {
	ns, err := client.CoreV1().Namespaces().Get(ctx, "kube-system", metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	return string(ns.UID), nil
}
