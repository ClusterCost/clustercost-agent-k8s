package snapshot

import (
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

func TestBuilder_Build_RequestsLimits(t *testing.T) {
	clusterID := "test-cluster"
	builder := NewBuilder(BuilderConfig{ClusterID: clusterID})

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

func TestBuilder_Build_NetworkConnectionServiceIntent(t *testing.T) {
	clusterID := "test-cluster"
	builder := NewBuilder(BuilderConfig{
		ClusterID:       clusterID,
		NetworkDetailed: true,
	})

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "source-pod",
			Namespace: "default",
			UID:       "uid-1",
		},
		Spec: corev1.PodSpec{
			NodeName: "test-node",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			PodIP: "10.0.0.10",
		},
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "api-service",
			Namespace: "default",
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: "10.96.0.1",
		},
	}

	nodes := []*corev1.Node{{
		ObjectMeta: metav1.ObjectMeta{Name: "test-node"},
	}}
	pods := []*corev1.Pod{pod}
	services := []*corev1.Service{svc}

	flows := []network.Flow{
		{
			SrcIP:   netip.MustParseAddr("10.0.0.10"),
			DstIP:   netip.MustParseAddr("10.96.0.1"),
			TxBytes: 1024,
		},
	}

	netColl := collector.NetworkCollection{Flows: flows}
	snap := builder.Build(nodes, nil, pods, services, nil, map[string]kube.PodUsage{}, netColl, time.Now())

	if len(snap.Connections) != 1 {
		t.Fatalf("expected 1 connection, got %d", len(snap.Connections))
	}

	conn := snap.Connections[0]
	if conn.DstKind != "service" {
		t.Fatalf("expected dst kind service, got %s", conn.DstKind)
	}
	if conn.ServiceMatch != "cluster_ip" {
		t.Fatalf("expected service match cluster_ip, got %s", conn.ServiceMatch)
	}
	if conn.IsEgress {
		t.Fatalf("expected is_egress false for service dst")
	}
	if len(conn.Dst.Services) != 1 || conn.Dst.Services[0].Name != "api-service" {
		t.Fatalf("expected destination service api-service")
	}
}

func TestBuilder_Build_NetworkConnectionIntentScenarios(t *testing.T) {
	builder := NewBuilder(BuilderConfig{
		ClusterID:       "test-cluster",
		NetworkDetailed: true,
	})

	srcPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "source-pod",
			Namespace: "default",
			UID:       "uid-1",
		},
		Spec: corev1.PodSpec{
			NodeName: "node-1",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			PodIP: "10.0.0.10",
		},
	}

	dstPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backend-pod",
			Namespace: "default",
			UID:       "uid-2",
		},
		Spec: corev1.PodSpec{
			NodeName: "node-2",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			PodIP: "10.0.1.20",
		},
	}

	serviceA := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "service-a",
			Namespace: "default",
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: "10.96.0.10",
			ExternalIPs: []string{
				"172.20.0.10",
			},
		},
		Status: corev1.ServiceStatus{
			LoadBalancer: corev1.LoadBalancerStatus{
				Ingress: []corev1.LoadBalancerIngress{{IP: "35.1.2.3"}},
			},
		},
	}

	serviceB := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "service-b",
			Namespace: "default",
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: "10.96.0.20",
		},
	}

	endpoints := []*discoveryv1.EndpointSlice{{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "service-b-slice",
			Namespace: "default",
			Labels: map[string]string{
				discoveryv1.LabelServiceName: "service-b",
			},
		},
		Endpoints: []discoveryv1.Endpoint{{
			Addresses: []string{"10.0.1.20"},
		}},
	}}

	nodes := []*corev1.Node{{
		ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
		Status: corev1.NodeStatus{
			Addresses: []corev1.NodeAddress{
				{Type: corev1.NodeInternalIP, Address: "192.168.1.10"},
			},
		},
	}, {
		ObjectMeta: metav1.ObjectMeta{Name: "node-2"},
		Status: corev1.NodeStatus{
			Addresses: []corev1.NodeAddress{
				{Type: corev1.NodeInternalIP, Address: "192.168.1.11"},
			},
		},
	}}

	flows := []network.Flow{
		{SrcIP: netip.MustParseAddr("10.0.0.10"), DstIP: netip.MustParseAddr("10.96.0.10"), TxBytes: 100},   // cluster_ip
		{SrcIP: netip.MustParseAddr("10.0.0.10"), DstIP: netip.MustParseAddr("172.20.0.10"), TxBytes: 200},  // external_ip
		{SrcIP: netip.MustParseAddr("10.0.0.10"), DstIP: netip.MustParseAddr("35.1.2.3"), TxBytes: 300},     // lb_ip
		{SrcIP: netip.MustParseAddr("10.0.0.10"), DstIP: netip.MustParseAddr("10.0.1.20"), TxBytes: 400},    // endpoint pod
		{SrcIP: netip.MustParseAddr("10.0.0.10"), DstIP: netip.MustParseAddr("192.168.1.11"), TxBytes: 500}, // node ip
		{SrcIP: netip.MustParseAddr("10.0.0.10"), DstIP: netip.MustParseAddr("10.255.0.1"), TxBytes: 600},   // private
		{SrcIP: netip.MustParseAddr("10.0.0.10"), DstIP: netip.MustParseAddr("8.8.8.8"), TxBytes: 700},      // external
	}

	snap := builder.Build(nodes, nil, []*corev1.Pod{srcPod, dstPod}, []*corev1.Service{serviceA, serviceB}, endpoints, map[string]kube.PodUsage{}, collector.NetworkCollection{Flows: flows}, time.Now())

	if len(snap.Connections) != len(flows) {
		t.Fatalf("expected %d connections, got %d", len(flows), len(snap.Connections))
	}

	byDst := map[string]NetworkConnection{}
	for _, conn := range snap.Connections {
		byDst[conn.Dst.IP] = conn
	}

	assert := func(dstIP, wantKind, wantMatch string, wantEgress bool) {
		conn, ok := byDst[dstIP]
		if !ok {
			t.Fatalf("missing connection for %s", dstIP)
		}
		if conn.DstKind != wantKind {
			t.Fatalf("dst %s kind: got %s want %s", dstIP, conn.DstKind, wantKind)
		}
		if conn.ServiceMatch != wantMatch {
			t.Fatalf("dst %s service match: got %s want %s", dstIP, conn.ServiceMatch, wantMatch)
		}
		if conn.IsEgress != wantEgress {
			t.Fatalf("dst %s is_egress: got %v want %v", dstIP, conn.IsEgress, wantEgress)
		}
	}

	assert("10.96.0.10", "service", "cluster_ip", false)
	assert("172.20.0.10", "service", "external_ip", false)
	assert("35.1.2.3", "service", "load_balancer_ip", false)
	assert("10.0.1.20", "pod", "endpoint", false)
	assert("192.168.1.11", "node", "none", false)
	assert("10.255.0.1", "private", "none", true)
	assert("8.8.8.8", "external", "none", true)
}
