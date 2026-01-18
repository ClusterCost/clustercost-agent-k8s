package collector

import (
	"net/netip"
	"testing"

	"clustercost-agent-k8s/internal/network"
)

func TestAggregateNetworkUsage(t *testing.T) {
	// Setup Nodes and Pods IPs
	node1IP := netip.MustParseAddr("192.168.1.101") // Zone 1
	node2IP := netip.MustParseAddr("192.168.1.102") // Zone 2

	podAIP := netip.MustParseAddr("10.0.0.1") // On Node 1 (Zone 1)
	podBIP := netip.MustParseAddr("10.0.0.2") // On Node 1 (Zone 1) - IntraNode
	podCIP := netip.MustParseAddr("10.0.1.1") // On Node 3 (Zone 1) - IntraAZ
	podDIP := netip.MustParseAddr("10.0.2.1") // On Node 2 (Zone 2) - CrossAZ

	publicIP := netip.MustParseAddr("8.8.8.8")
	privateIP := netip.MustParseAddr("10.255.0.1")

	podByIP := map[netip.Addr]network.PodInfo{
		podAIP: {Namespace: "ns", Pod: "pod-a", Node: "node-1", AvailabilityZone: "zone-1"},
		podBIP: {Namespace: "ns", Pod: "pod-b", Node: "node-1", AvailabilityZone: "zone-1"},
		podCIP: {Namespace: "ns", Pod: "pod-c", Node: "node-3", AvailabilityZone: "zone-1"},
		podDIP: {Namespace: "ns", Pod: "pod-d", Node: "node-2", AvailabilityZone: "zone-2"},
	}

	nodeByIP := map[netip.Addr]network.PodInfo{
		node1IP: {Node: "node-1", AvailabilityZone: "zone-1"},
		node2IP: {Node: "node-2", AvailabilityZone: "zone-2"},
	}

	flows := []network.Flow{
		// 1. Pod A -> Public (1000 bytes)
		{SrcIP: podAIP, DstIP: publicIP, TxBytes: 1000},

		// 2. Pod A -> Pod B (Same Node) (200 bytes) -> Internal
		{SrcIP: podAIP, DstIP: podBIP, TxBytes: 200},

		// 3. Pod A -> Pod C (Same AZ, Diff Node) (300 bytes) -> Internal
		{SrcIP: podAIP, DstIP: podCIP, TxBytes: 300},

		// 4. Pod A -> Pod D (Diff AZ) (400 bytes) -> CrossAZ
		{SrcIP: podAIP, DstIP: podDIP, TxBytes: 400},

		// 5. Pod A -> Node 2 IP (Diff AZ) (500 bytes) -> CrossAZ
		{SrcIP: podAIP, DstIP: node2IP, TxBytes: 500},

		// 6. Pod A -> Node 1 IP (Same Node/AZ) (100 bytes) -> Internal
		{SrcIP: podAIP, DstIP: node1IP, TxBytes: 100},

		// 7. Pod A -> Unknown Private (50 bytes) -> Internal
		{SrcIP: podAIP, DstIP: privateIP, TxBytes: 50},

		// 8. Public -> Pod A ingress (1200 bytes)
		{SrcIP: publicIP, DstIP: podAIP, RxBytes: 1200},
	}
	// Aggregate flows
	podUsage, _ := AggregateNetworkUsage(flows, podByIP, nodeByIP)

	// Check result
	// Expected: Pod A (Tx), Pod B (Rx), Pod C (Rx), Pod D (Rx) = 4 entries
	if len(podUsage) != 4 {
		t.Fatalf("expected 4 pod usage entries, got %d", len(podUsage))
	}

	keyA := "ns/pod-a"
	u, ok := podUsage[keyA]
	if !ok {
		t.Fatalf("expected usage for %s", keyA)
	}

	// Verification
	// Total Tx: 1000 + 200 + 300 + 400 + 500 + 100 + 50 = 2550
	if u.TxBytes != 2550 {
		t.Errorf("Total TxBytes: got %d, want 2550", u.TxBytes)
	}

	// Egress Public: 1000
	if u.EgressPublicBytes != 1000 {
		t.Errorf("EgressPublicBytes: got %d, want 1000", u.EgressPublicBytes)
	}

	// Egress CrossAZ: Pod D (400) + Node 2 (500) = 900
	if u.EgressCrossAZBytes != 900 {
		t.Errorf("EgressCrossAZBytes: got %d, want 900", u.EgressCrossAZBytes)
	}

	// Egress Internal: Pod B (200) + Pod C (300) + Node 1 (100) + Private (50) = 650
	if u.EgressInternalBytes != 650 {
		t.Errorf("EgressInternalBytes: got %d, want 650", u.EgressInternalBytes)
	}

	// Ingress: 1200 bytes from public IP
	if u.RxBytes != 1200 {
		t.Errorf("RxBytes: got %d, want 1200", u.RxBytes)
	}
}
