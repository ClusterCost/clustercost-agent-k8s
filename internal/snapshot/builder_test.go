package snapshot

import (
	"math"
	"testing"
	"time"

	"clustercost-agent-k8s/internal/kube"

	corev1 "k8s.io/api/core/v1"
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
	builder := NewBuilder("cluster-1", classifier, prices)

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
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}

	podNonProd := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "worker-0",
			Namespace: "sandbox",
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
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}

	memUsage := resource.MustParse("800Mi")
	usage := map[string]kube.PodUsage{
		"payments/api-0": {CPUUsageMilli: 400, MemoryUsageBytes: memUsage.Value()},
		// worker pod intentionally missing to test fallback to requests
	}

	snap := builder.Build(
		[]*corev1.Node{node},
		[]*corev1.Namespace{nsProd, nsNonProd},
		[]*corev1.Pod{podProd, podNonProd},
		usage,
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
}

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < 0.0001
}
