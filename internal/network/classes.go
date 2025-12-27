package network

// Traffic class identifiers used for network cost attribution.
const (
	TrafficClassIntraNode      = "intra_node"
	TrafficClassIntraAZ        = "intra_az"
	TrafficClassInterAZ        = "inter_az"
	TrafficClassVPCPrivate     = "vpc_private"
	TrafficClassPublicInternet = "public_internet"
	TrafficClassUnknown        = "unknown"
)

// KnownClasses returns the canonical ordering for traffic classes.
func KnownClasses() []string {
	return []string{
		TrafficClassIntraNode,
		TrafficClassIntraAZ,
		TrafficClassInterAZ,
		TrafficClassVPCPrivate,
		TrafficClassPublicInternet,
		TrafficClassUnknown,
	}
}
