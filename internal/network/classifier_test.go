package network

import (
	"net/netip"
	"testing"
)

func TestClassifyEgress(t *testing.T) {
	podByIP := map[netip.Addr]PodInfo{
		netip.MustParseAddr("10.0.0.10"): {Namespace: "a", Pod: "pod-a", Node: "node-1", AvailabilityZone: "us-east-1a"},
		netip.MustParseAddr("10.0.0.11"): {Namespace: "b", Pod: "pod-b", Node: "node-1", AvailabilityZone: "us-east-1a"},
		netip.MustParseAddr("10.0.1.10"): {Namespace: "c", Pod: "pod-c", Node: "node-2", AvailabilityZone: "us-east-1a"},
		netip.MustParseAddr("10.0.2.10"): {Namespace: "d", Pod: "pod-d", Node: "node-3", AvailabilityZone: "us-east-1b"},
	}

	nodeByIP := map[netip.Addr]PodInfo{
		netip.MustParseAddr("192.168.1.1"): {Node: "node-1", AvailabilityZone: "us-east-1a"},
		netip.MustParseAddr("192.168.1.2"): {Node: "node-2", AvailabilityZone: "us-east-1a"},
		netip.MustParseAddr("192.168.1.3"): {Node: "node-3", AvailabilityZone: "us-east-1b"},
	}

	src := podByIP[netip.MustParseAddr("10.0.0.10")] // On node-1, us-east-1a

	tests := []struct {
		name string
		dst  string
		want string
	}{
		{"same-node-pod", "10.0.0.11", TrafficClassIntraNode},
		{"same-az-pod", "10.0.1.10", TrafficClassIntraAZ},
		{"inter-az-pod", "10.0.2.10", TrafficClassInterAZ},

		{"same-node-ip", "192.168.1.1", TrafficClassIntraNode},
		{"same-az-node-ip", "192.168.1.2", TrafficClassIntraAZ},
		{"inter-az-node-ip", "192.168.1.3", TrafficClassInterAZ},

		{"vpc-private", "10.10.10.10", TrafficClassVPCPrivate},
		{"public-internet", "8.8.8.8", TrafficClassPublicInternet},
		{"unknown", "169.254.1.1", TrafficClassUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := netip.MustParseAddr(tt.dst)
			got := ClassifyEgress(src, dst, podByIP, nodeByIP)
			if got != tt.want {
				t.Fatalf("classify %s: got %s want %s", tt.dst, got, tt.want)
			}
		})
	}
}
