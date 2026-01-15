package snapshot

import (
	"net/netip"
	"testing"
	"time"

	"clustercost-agent-k8s/internal/collector"
	"clustercost-agent-k8s/internal/kube"
	"clustercost-agent-k8s/internal/network"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestBuilderAddsDNSNames(t *testing.T) {
	builder := NewBuilder(BuilderConfig{ClusterID: "test", NetworkDetailed: true})

	srcIP := netip.MustParseAddr("10.0.0.2")
	dstIP := netip.MustParseAddr("8.8.8.8")

	pods := []*corev1.Pod{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "src",
				Namespace: "default",
				UID:       "uid-src",
			},
			Spec: corev1.PodSpec{
				NodeName: "node-a",
			},
			Status: corev1.PodStatus{
				PodIP: srcIP.String(),
			},
		},
	}

	netColl := collector.NetworkCollection{
		Flows: []network.Flow{
			{SrcIP: srcIP, DstIP: dstIP, Protocol: 6, TxBytes: 128},
		},
	}
	dnsNames := map[netip.Addr]string{
		dstIP: "dns.google",
	}

	snap := builder.Build(nil, nil, pods, nil, nil, map[string]kube.PodUsage{}, netColl, map[string]kube.NodeUsage{}, dnsNames, time.Now())
	if len(snap.Connections) != 1 {
		t.Fatalf("expected 1 connection, got %d", len(snap.Connections))
	}
	if snap.Connections[0].Dst.DnsName != "dns.google" {
		t.Fatalf("expected dns.google, got %q", snap.Connections[0].Dst.DnsName)
	}
}
