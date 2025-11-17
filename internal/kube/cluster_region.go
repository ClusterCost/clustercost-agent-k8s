package kube

import (
	"context"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// DetectClusterRegion returns the best guess of the cluster's cloud region using node metadata.
func DetectClusterRegion(ctx context.Context, client kubernetes.Interface) (string, error) {
	nodes, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", err
	}
	for _, node := range nodes.Items {
		if region := regionFromNode(node.Labels, node.Spec.ProviderID); region != "" {
			return region, nil
		}
	}
	return "", fmt.Errorf("cluster region not discovered")
}

func regionFromNode(labels map[string]string, providerID string) string {
	for _, key := range []string{"topology.kubernetes.io/region", "failure-domain.beta.kubernetes.io/region"} {
		if value := strings.TrimSpace(labels[key]); value != "" {
			return value
		}
	}
	for _, key := range []string{"topology.kubernetes.io/zone", "failure-domain.beta.kubernetes.io/zone"} {
		if zone := strings.TrimSpace(labels[key]); zone != "" {
			if region := regionFromZone(zone); region != "" {
				return region
			}
		}
	}
	if region := regionFromProviderID(providerID); region != "" {
		return region
	}
	return ""
}

func regionFromZone(zone string) string {
	if zone == "" {
		return ""
	}
	if idx := strings.LastIndex(zone, "-"); idx > 0 {
		suffix := zone[idx+1:]
		if len(suffix) == 1 && suffix[0] >= 'a' && suffix[0] <= 'z' {
			return zone[:idx]
		}
	}
	last := zone[len(zone)-1]
	if last >= 'a' && last <= 'z' && len(zone) > 1 {
		return zone[:len(zone)-1]
	}
	return ""
}

func regionFromProviderID(providerID string) string {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return ""
	}
	parts := strings.Split(providerID, "/")
	switch {
	case strings.HasPrefix(providerID, "aws://"):
		if len(parts) >= 4 {
			if region := regionFromZone(parts[3]); region != "" {
				return region
			}
		}
	case strings.HasPrefix(providerID, "gce://") || strings.HasPrefix(providerID, "gke://"):
		if len(parts) >= 4 {
			if region := regionFromZone(parts[3]); region != "" {
				return region
			}
		}
	case strings.HasPrefix(providerID, "ibm://"):
		if len(parts) >= 4 {
			if region := regionFromZone(parts[3]); region != "" {
				return region
			}
		}
	}
	return ""
}
