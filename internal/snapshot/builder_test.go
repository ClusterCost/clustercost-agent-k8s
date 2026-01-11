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
		NonProdLabelValues:     []string{"nonprod"},
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
				{Kind: "Deployment", Name: "api", Controller: boolPtr(true)},
			},
			Labels: map[string]string{
				"clustercost.io/environment": "prod",
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
				{Kind: "Deployment", Name: "worker", Controller: boolPtr(true)},
			},
			Labels: map[string]string{
				"clustercost.io/environment": "nonprod",
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

	var prodPod, nonProdPod PodRecord
	foundProd, foundNonProd := false, false

	for _, p := range snap.Pods {
		if p.Pod == "api-0" && p.Namespace == "payments" {
			prodPod = p
			foundProd = true
		} else if p.Pod == "worker-0" && p.Namespace == "sandbox" {
			nonProdPod = p
			foundNonProd = true
		}
	}

	if !foundProd || !foundNonProd {
		t.Fatalf("expected to find api-0 and worker-0 pods")
	}

	// Verify Prod Pod (api-0)
	if prodPod.Environment != "production" {
		t.Fatalf("payments env = %s", prodPod.Environment)
	}
	if prodPod.OwnerKind != "Deployment" || prodPod.OwnerName != "api" {
		t.Fatalf("unexpected owner: %s/%s", prodPod.OwnerKind, prodPod.OwnerName)
	}
	if prodPod.CPURequestMilli != 500 || prodPod.CPUUsageMilli != 400 {
		t.Fatalf("unexpected prod pod resource stats: %+v", prodPod)
	}
	// 500m / 2000m * 0.1 = 0.025
	if !almostEqual(prodPod.ResourceHourlyCost, 0.025) {
		t.Fatalf("prod pod resource cost %.4f", prodPod.ResourceHourlyCost)
	}
	if prodPod.NetworkTxBytes != 1024 || prodPod.NetworkRxBytes != 2048 {
		t.Fatalf("prod pod network totals unexpected: %+v", prodPod)
	}
	if len(prodPod.NetworkByClass) != 1 || prodPod.NetworkByClass[0].Class != "public_internet" {
		t.Fatalf("prod pod network class totals unexpected: %+v", prodPod.NetworkByClass)
	}

	// Verify NonProd Pod (worker-0)
	if nonProdPod.Environment != "nonprod" {
		t.Fatalf("sandbox env = %s", nonProdPod.Environment)
	}
	if nonProdPod.CPUUsageMilli != 250 {
		t.Fatalf("sandbox usage fallback expected 250, got %d", nonProdPod.CPUUsageMilli)
	}
	// 250m / 2000m * 0.1 = 0.0125
	if !almostEqual(nonProdPod.ResourceHourlyCost, 0.0125) {
		t.Fatalf("sandbox pod cost %.4f", nonProdPod.ResourceHourlyCost)
	}

	if snap.Node == nil {
		t.Fatalf("expected single node record")
	}
	nodeRec := *snap.Node
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
}

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < 0.0001
}

func boolPtr(b bool) *bool {
	return &b
}
