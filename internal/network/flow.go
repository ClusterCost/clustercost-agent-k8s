package network

import "net/netip"

// Flow represents a network flow delta between two IPs.
type Flow struct {
	SrcIP    netip.Addr
	DstIP    netip.Addr
	Protocol uint8
	TxBytes  uint64
	RxBytes  uint64
}
