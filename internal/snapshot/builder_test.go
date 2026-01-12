package snapshot

import (
	"testing"
	"time"

	"clustercost-agent-k8s/internal/collector"
	"clustercost-agent-k8s/internal/kube"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestBuilder_Build_RequestsLimits(t *testing.T) {
	clusterID := "test-cluster"
	builder := NewBuilder(clusterID)

	podCPUReq := resource.MustParse("500m")
	podCPULim := resource.MustParse("1000m")
	podMemReq := resource.MustParse("128Mi")
	podMemLim := resource.MustParse("256Mi")

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
			UID:       "uid-1",
		},
		Spec: corev1.PodSpec{
			NodeName: "test-node",
			Containers: []corev1.Container{
				{
					Name: "main",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    podCPUReq,
							corev1.ResourceMemory: podMemReq,
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    podCPULim,
							corev1.ResourceMemory: podMemLim,
						},
					},
				},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			PodIP: "10.0.0.1",
		},
	}

	pods := []*corev1.Pod{pod}
	nodes := []*corev1.Node{{
		ObjectMeta: metav1.ObjectMeta{Name: "test-node"},
	}}
	usage := map[string]kube.PodUsage{}
	netColl := collector.NetworkCollection{}

	snap := builder.Build(nodes, nil, pods, nil, nil, usage, netColl, time.Now())

	if len(snap.Pods) != 1 {
		t.Fatalf("expected 1 pod, got %d", len(snap.Pods))
	}

	p := snap.Pods[0]

	// Verify Requests
	if p.Cpu.RequestMillicores != 500 {
		t.Errorf("expected 500m CPU request, got %d", p.Cpu.RequestMillicores)
	}
	if p.Memory.RequestBytes != uint64(podMemReq.Value()) {
		t.Errorf("expected %d bytes memory request, got %d", podMemReq.Value(), p.Memory.RequestBytes)
	}

	// Verify Limits
	if p.Cpu.LimitMillicores != 1000 {
		t.Errorf("expected 1000m CPU limit, got %d", p.Cpu.LimitMillicores)
	}
	if p.Memory.LimitBytes != uint64(podMemLim.Value()) {
		t.Errorf("expected %d bytes memory limit, got %d", podMemLim.Value(), p.Memory.LimitBytes)
	}
}
