package kube

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// DetectClusterName tries to infer the human friendly cluster name from Kubernetes state.
// Multiple strategies are attempted, returning the first non-empty value discovered.
func DetectClusterName(ctx context.Context, client kubernetes.Interface) (string, error) {
	sources := []func(context.Context, kubernetes.Interface) (string, error){
		clusterNameFromClusterInfoConfigMap,
		clusterNameFromKubeadmConfig,
		clusterNameFromNodes,
	}

	var errs []error
	for _, source := range sources {
		name, err := source(ctx, client)
		if err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			errs = append(errs, err)
			continue
		}
		if name != "" {
			return name, nil
		}
	}

	if len(errs) > 0 {
		return "", errors.Join(errs...)
	}
	return "", fmt.Errorf("cluster name not discovered")
}

func clusterNameFromClusterInfoConfigMap(ctx context.Context, client kubernetes.Interface) (string, error) {
	for _, ns := range []string{"kube-system", "kube-public"} {
		cm, err := client.CoreV1().ConfigMaps(ns).Get(ctx, "cluster-info", metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return "", err
		}
		if name := clusterNameFromConfigMap(cm); name != "" {
			return name, nil
		}
	}
	return "", nil
}

func clusterNameFromKubeadmConfig(ctx context.Context, client kubernetes.Interface) (string, error) {
	cm, err := client.CoreV1().ConfigMaps("kube-system").Get(ctx, "kubeadm-config", metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	if name := strings.TrimSpace(cm.Data["cluster-name"]); name != "" {
		return name, nil
	}
	if cfgData := cm.Data["ClusterConfiguration"]; cfgData != "" {
		type clusterConfig struct {
			ClusterName string `yaml:"clusterName"`
		}
		var cfg clusterConfig
		if err := yaml.Unmarshal([]byte(cfgData), &cfg); err == nil {
			if name := strings.TrimSpace(cfg.ClusterName); name != "" {
				return name, nil
			}
		}
	}
	return "", nil
}

func clusterNameFromConfigMap(cm *corev1.ConfigMap) string {
	for _, key := range []string{"cluster-name", "clusterName", "ClusterName"} {
		if val := strings.TrimSpace(cm.Data[key]); val != "" {
			return val
		}
	}
	if kubeconfig := cm.Data["kubeconfig"]; kubeconfig != "" {
		if name := clusterNameFromKubeconfig(kubeconfig); name != "" {
			return name
		}
	}
	return ""
}

func clusterNameFromKubeconfig(content string) string {
	cfg, err := clientcmd.Load([]byte(content))
	if err != nil || cfg == nil {
		return ""
	}
	if cfg.CurrentContext != "" {
		if ctx, ok := cfg.Contexts[cfg.CurrentContext]; ok && ctx != nil {
			if name := strings.TrimSpace(ctx.Cluster); name != "" {
				return name
			}
		}
	}
	for name := range cfg.Clusters {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			return trimmed
		}
	}
	return ""
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
