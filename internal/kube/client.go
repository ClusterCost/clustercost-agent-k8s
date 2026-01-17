package kube

import (
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Client bundles the kubernetes API client.
type Client struct {
	Kubernetes  kubernetes.Interface
	RestConfig  *rest.Config
	ClusterName string
}

// NewClient builds the kubernetes client using in-cluster config with optional kubeconfig fallback.
func NewClient(clusterName, kubeconfigPath string) (*Client, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		if kubeconfigPath == "" {
			home := os.Getenv("HOME")
			kubeconfigPath = filepath.Join(home, ".kube", "config")
		}
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfigPath)
		if err != nil {
			return nil, fmt.Errorf("build kubeconfig: %w", err)
		}
	}

	kubeClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create kubernetes client: %w", err)
	}

	return &Client{Kubernetes: kubeClient, RestConfig: config, ClusterName: clusterName}, nil
}
