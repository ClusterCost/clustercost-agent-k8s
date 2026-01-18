package main

import corev1 "k8s.io/api/core/v1"

func extractNodeMetadata(node *corev1.Node) (zone, region, instanceType string) {
	if node == nil {
		return "", "", ""
	}
	labels := node.Labels
	zone = labels["topology.kubernetes.io/zone"]
	if zone == "" {
		zone = labels["failure-domain.beta.kubernetes.io/zone"]
	}
	region = labels["topology.kubernetes.io/region"]
	if region == "" {
		region = labels["failure-domain.beta.kubernetes.io/region"]
	}
	instanceType = labels["node.kubernetes.io/instance-type"]
	if instanceType == "" {
		instanceType = labels["beta.kubernetes.io/instance-type"]
	}
	return
}
