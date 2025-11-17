package aggregator

import (
	"testing"
	"time"

	"clustercost-agent-k8s/internal/enricher"
	"clustercost-agent-k8s/internal/kube"
	"clustercost-agent-k8s/internal/pricing"

	"log/slog"
	"os"
)

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestCostAggregatorAggregate(t *testing.T) {
	calc := pricing.NewCalculator(1, 0.5) // simple prices for assertions
	nodePricer, err := pricing.NewAWSPricing("us-east-1", map[string]map[string]float64{
		"us-east-1": {"m5.large": 0.096},
	})
	if err != nil {
		t.Fatalf("pricing init: %v", err)
	}
	agg := NewCostAggregator(calc, enricher.NewLabelEnricher(), nodePricer, "aws", "us-east-1", newTestLogger())

	snapshot := kube.ClusterSnapshot{
		ClusterName: "dev",
		Timestamp:   time.Unix(123, 0),
		Namespaces: []kube.Namespace{
			{Name: "payments", Labels: map[string]string{"team": "finops", "env": "prod"}},
		},
		Nodes: []kube.Node{
			{
				Name:             "node-a",
				InstanceType:     "m5.large",
				AvailabilityZone: "us-east-1a",
				AllocatableCPU:   4000,
				AllocatableMem:   16 * 1024 * 1024 * 1024,
				RequestedCPU:     2000,
				RequestedMem:     8 * 1024 * 1024 * 1024,
				Labels:           map[string]string{"topology.kubernetes.io/zone": "us-east-1a"},
			},
		},
		Pods: []kube.Pod{
			{
				Namespace: "payments",
				Name:      "cart-abc",
				NodeName:  "node-a",
				Labels:    map[string]string{"service": "cart"},
				OwnerKind: "Deployment",
				OwnerName: "cart-ctrl",
				Containers: []kube.PodContainer{
					{CPURequestMilli: 200, MemoryRequestBytes: 512 * 1024 * 1024},
				},
			},
		},
	}

	usage := map[string]kube.PodUsage{
		"payments/cart-abc": {CPUUsageMilli: 300, MemoryUsageBytes: 512 * 1024 * 1024},
	}

	data := agg.Aggregate(snapshot, usage)

	if len(data.Pods) != 1 {
		t.Fatalf("expected 1 pod result, got %d", len(data.Pods))
	}
	if len(data.Namespaces) != 1 {
		t.Fatalf("expected 1 namespace result, got %d", len(data.Namespaces))
	}

	pod := data.Pods[0]
	expectedCost := calc.CostFor(300, 512*1024*1024)
	if pod.HourlyCost != expectedCost {
		t.Fatalf("expected pod cost %.2f, got %.2f", expectedCost, pod.HourlyCost)
	}

	ns := data.Namespaces[0]
	if ns.PodCount != 1 {
		t.Fatalf("expected namespace pod count=1 got %d", ns.PodCount)
	}
	if ns.CPURequestMilli != 200 {
		t.Fatalf("expected namespace CPU request=200 got %d", ns.CPURequestMilli)
	}
	if ns.MemoryRequestBytes != 512*1024*1024 {
		t.Fatalf("expected namespace memory request=512MiB got %d", ns.MemoryRequestBytes)
	}
	if ns.CPUUsageMilli != 300 {
		t.Fatalf("expected namespace CPU usage=300 got %d", ns.CPUUsageMilli)
	}
	if ns.MemoryUsageBytes != 512*1024*1024 {
		t.Fatalf("expected namespace memory usage=512MiB got %d", ns.MemoryUsageBytes)
	}

	if data.Cluster.HourlyCost != expectedCost {
		t.Fatalf("cluster hourly cost mismatch: %.2f vs %.2f", data.Cluster.HourlyCost, expectedCost)
	}

	if len(data.Nodes) != 1 {
		t.Fatalf("expected 1 node entry, got %d", len(data.Nodes))
	}
	if data.Nodes[0].RawNodePriceHourly != 0.096 {
		t.Fatalf("expected raw node price 0.096 got %.3f", data.Nodes[0].RawNodePriceHourly)
	}
	if data.Cluster.CostByInstanceType == nil || len(data.Cluster.CostByInstanceType) != 1 {
		t.Fatalf("expected cost by instance type entry")
	}

	if len(data.Labels) == 0 {
		t.Fatalf("expected label aggregations")
	}

	foundTeam := false
	for _, label := range data.Labels {
		if label.Key == "team" && label.Value == "finops" {
			foundTeam = true
			if label.HourlyCost != expectedCost {
				t.Fatalf("expected team cost %.2f got %.2f", expectedCost, label.HourlyCost)
			}
		}
	}
	if !foundTeam {
		t.Fatalf("team label aggregation missing")
	}

	workloads := WorkloadsFromPods(data.Pods)
	if len(workloads) != 1 {
		t.Fatalf("expected 1 workload, got %d", len(workloads))
	}
	w := workloads[0]
	if w.WorkloadKind != "Deployment" || w.WorkloadName != "cart-ctrl" {
		t.Fatalf("unexpected workload grouping: %+v", w)
	}
	if w.Replicas != 1 {
		t.Fatalf("expected replicas=1 got %d", w.Replicas)
	}
}

func init() {
	// ensure test pods have controller metadata
}
