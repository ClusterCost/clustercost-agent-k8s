package network

import "net/netip"

// PodInfo provides the minimum pod context needed for traffic classification.
type PodInfo struct {
	Namespace        string
	Pod              string
	Node             string
	AvailabilityZone string
}

// ClassifyEgress determines the traffic class for a pod's egress to a destination IP.
func ClassifyEgress(src PodInfo, dst netip.Addr, podByIP map[netip.Addr]PodInfo) string {
	if !dst.IsValid() {
		return TrafficClassUnknown
	}
	if dstPod, ok := podByIP[dst]; ok {
		if dstPod.Node == src.Node && src.Node != "" {
			return TrafficClassIntraNode
		}
		if dstPod.AvailabilityZone != "" && dstPod.AvailabilityZone == src.AvailabilityZone {
			return TrafficClassIntraAZ
		}
		if dstPod.AvailabilityZone != "" && src.AvailabilityZone != "" {
			return TrafficClassInterAZ
		}
		return TrafficClassUnknown
	}
	if isPrivateIP(dst) {
		return TrafficClassVPCPrivate
	}
	if dst.IsGlobalUnicast() {
		return TrafficClassPublicInternet
	}
	return TrafficClassUnknown
}

func isPrivateIP(addr netip.Addr) bool {
	if addr.Is4() {
		v4 := addr.As4()
		switch {
		case v4[0] == 10:
			return true
		case v4[0] == 172 && v4[1]&0xf0 == 16:
			return true
		case v4[0] == 192 && v4[1] == 168:
			return true
		default:
			return false
		}
	}
	// IPv6 unique local addresses (fc00::/7)
	if addr.Is6() {
		v6 := addr.As16()
		return v6[0]&0xfe == 0xfc
	}
	return false
}
