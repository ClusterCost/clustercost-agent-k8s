package kube

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// DetectNodeNameFromPod resolves the node name hosting the given pod.
func DetectNodeNameFromPod(ctx context.Context, client kubernetes.Interface, namespace, podName string) (string, error) {
	if namespace == "" || podName == "" {
		return "", fmt.Errorf("pod namespace and name are required to detect node name")
	}
	pod, err := client.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	if pod.Spec.NodeName == "" {
		return "", fmt.Errorf("pod %s/%s has no node assigned", namespace, podName)
	}
	return pod.Spec.NodeName, nil
}
