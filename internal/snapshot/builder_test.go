package snapshot

import (
	"math"
	"net/netip"
	"testing"
	"time"

	"clustercost-agent-k8s/internal/collector"
	"clustercost-agent-k8s/internal/kube"
	"clustercost-agent-k8s/internal/network"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestBuilderAggregatesSnapshot(t *testing.T) {
	classifier := NewEnvironmentClassifier(ClassifierConfig{
		LabelKeys:              []string{"clustercost.io/environment"},
		ProductionLabelValues:  []string{"prod"},
		SystemNamespaces:       []string{"kube-system"},
		ProductionNameContains: []string{"prod"},
	})
	prices := NewNodePriceLookup(map[string]float64{"m6a.large": 0.1}, 0.2)
	netPrices := NewNetworkPriceLookup(0, nil)
	builder := NewBuilder("cluster-1", classifier, prices, netPrices, true)

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node-1",
			Labels: map[string]string{
				"node.kubernetes.io/instance-type": "m6a.large",
			},
		},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("2000m"),
				corev1.ResourceMemory: resource.MustParse("4Gi"),
			},
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			},
		},
	}

	nsProd := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "payments",
			Labels: map[string]string{"clustercost.io/environment": "prod"},
		},
	}
	nsNonProd := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "sandbox",
		},
	}

	podProd := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "api-0",
			Namespace: "payments",
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "Deployment", Name: "api"},
			},
		},
		Spec: corev1.PodSpec{
			NodeName: node.Name,
			Containers: []corev1.Container{
				{
					Name: "app",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("500m"),
							corev1.ResourceMemory: resource.MustParse("1Gi"),
						},
					},
				},
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, PodIP: "10.0.0.10"},
	}

	podNonProd := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "worker-0",
			Namespace: "sandbox",
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "Deployment", Name: "worker"},
			},
		},
		Spec: corev1.PodSpec{
			NodeName: node.Name,
			Containers: []corev1.Container{
				{
					Name: "worker",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("250m"),
							corev1.ResourceMemory: resource.MustParse("512Mi"),
						},
					},
				},
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, PodIP: "10.0.0.11"},
	}

	memUsage := resource.MustParse("800Mi")
	usage := map[string]kube.PodUsage{
		"payments/api-0": {CPUUsageMilli: 400, MemoryUsageBytes: memUsage.Value()},
		// worker pod intentionally missing to test fallback to requests
	}
	networkCollection := collector.NetworkCollection{
		PodUsage: map[string]kube.PodNetworkUsage{
			"payments/api-0": {
				TxBytes:        1024,
				RxBytes:        2048,
				TxBytesByClass: map[string]uint64{"public_internet": 1024},
				RxBytesByClass: map[string]uint64{"public_internet": 2048},
			},
		},
		Flows: []network.Flow{
			{SrcIP: netip.MustParseAddr("10.0.0.10"), DstIP: netip.MustParseAddr("10.0.0.11"), TxBytes: 512, RxBytes: 256},
			{SrcIP: netip.MustParseAddr("10.0.0.10"), DstIP: netip.MustParseAddr("8.8.8.8"), TxBytes: 512, RxBytes: 128},
		},
	}

	serviceAPI := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "api",
			Namespace: "payments",
		},
	}
	serviceWorker := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "worker",
			Namespace: "sandbox",
		},
	}
	endpointsAPI := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "api-1",
			Namespace: "payments",
			Labels: map[string]string{
				"kubernetes.io/service-name": "api",
			},
		},
		Endpoints: []discoveryv1.Endpoint{
			{
				Addresses: []string{"10.0.0.10"},
			},
		},
	}
	endpointsWorker := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "worker-1",
			Namespace: "sandbox",
			Labels: map[string]string{
				"kubernetes.io/service-name": "worker",
			},
		},
		Endpoints: []discoveryv1.Endpoint{
			{
				Addresses: []string{"10.0.0.11"},
			},
		},
	}

	snap := builder.Build(
		[]*corev1.Node{node},
		[]*corev1.Namespace{nsProd, nsNonProd},
		[]*corev1.Pod{podProd, podNonProd},
		[]*corev1.Service{serviceAPI, serviceWorker},
		[]*discoveryv1.EndpointSlice{endpointsAPI, endpointsWorker},
		usage,
		networkCollection,
		time.Unix(123, 0),
	)

	if len(snap.Namespaces) != 2 {
		t.Fatalf("expected 2 namespaces, got %d", len(snap.Namespaces))
	}

	var prodNS, nonProdNS NamespaceCostRecord
	for _, ns := range snap.Namespaces {
		switch ns.Namespace {
		case "payments":
			prodNS = ns
		case "sandbox":
			nonProdNS = ns
		}
	}

	if prodNS.Environment != "production" {
		t.Fatalf("payments env = %s", prodNS.Environment)
	}
	if prodNS.PodCount != 1 || prodNS.CPURequestMilli != 500 || prodNS.CPUUsageMilli != 400 {
		t.Fatalf("unexpected prod namespace stats: %+v", prodNS)
	}
	if !almostEqual(prodNS.HourlyCost, 0.025) {
		t.Fatalf("prod hourly cost %.4f", prodNS.HourlyCost)
	}

	if nonProdNS.Environment != "nonprod" {
		t.Fatalf("sandbox env = %s", nonProdNS.Environment)
	}
	if nonProdNS.CPUUsageMilli != 250 {
		t.Fatalf("sandbox usage fallback expected 250, got %d", nonProdNS.CPUUsageMilli)
	}
	if !almostEqual(nonProdNS.HourlyCost, 0.0125) {
		t.Fatalf("sandbox hourly cost %.4f", nonProdNS.HourlyCost)
	}
	if prodNS.NetworkTxBytes != 1024 || prodNS.NetworkRxBytes != 2048 {
		t.Fatalf("prod network totals unexpected: %+v", prodNS)
	}

	if len(snap.Nodes) != 1 {
		t.Fatalf("expected single node record")
	}
	nodeRec := snap.Nodes[0]
	if nodeRec.PodCount != 2 {
		t.Fatalf("node podCount %d", nodeRec.PodCount)
	}
	if !almostEqual(nodeRec.CPUUsagePercent, 32.5) {
		t.Fatalf("node cpu usage percent %.2f", nodeRec.CPUUsagePercent)
	}

	res := snap.Resources
	if res.CPURequestMilliTotal != 750 || res.CPUUsageMilliTotal != 650 {
		t.Fatalf("unexpected cluster cpu totals %+v", res)
	}
	if !almostEqual(res.TotalNodeHourlyCost, 0.1) {
		t.Fatalf("cluster node cost %.4f", res.TotalNodeHourlyCost)
	}
	if res.NetworkTxBytesTotal != 1024 || res.NetworkRxBytesTotal != 2048 {
		t.Fatalf("cluster network totals unexpected: %+v", res)
	}
	if len(snap.Network.Pods) != 2 {
		t.Fatalf("expected 2 pod network records, got %d", len(snap.Network.Pods))
	}
	if len(snap.Network.ByClass) != 1 || snap.Network.ByClass[0].Class != "public_internet" {
		t.Fatalf("network class totals unexpected: %+v", snap.Network.ByClass)
	}
	if len(snap.Network.PodConnections) == 0 {
		t.Fatalf("expected pod connection records")
	}
	if len(snap.Network.WorkloadConnections) == 0 {
		t.Fatalf("expected workload connection records")
	}
	if len(snap.Network.NamespaceConnections) == 0 {
		t.Fatalf("expected namespace connection records")
	}
	if !hasServiceConnection(snap.Network.ServiceConnections, "payments", "api", "sandbox", "worker") {
		t.Fatalf("expected service connection between api and worker")
	}
}

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < 0.0001
}

func hasServiceConnection(conns []NetworkConnection, srcNS, srcName, dstNS, dstName string) bool {
	for _, conn := range conns {
		if conn.Source.Kind != "service" || conn.Destination.Kind != "service" {
			continue
		}
		if conn.Source.Namespace == srcNS && conn.Source.Name == srcName &&
			conn.Destination.Namespace == dstNS && conn.Destination.Name == dstName {
			return true
		}
	}
	return false
}
