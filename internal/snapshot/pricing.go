package snapshot

import "strings"

// NodePriceLookup resolves node hourly prices by instance type.
type NodePriceLookup struct {
	prices      map[string]float64
	defaultCost float64
}

// NewNodePriceLookup builds a lookup map with normalized keys.
func NewNodePriceLookup(prices map[string]float64, defaultCost float64) *NodePriceLookup {
	n := &NodePriceLookup{
		prices:      map[string]float64{},
		defaultCost: defaultCost,
	}
	for k, v := range prices {
		if k == "" || v < 0 {
			continue
		}
		n.prices[strings.ToLower(k)] = v
	}
	return n
}

// Price returns the hourly node cost for the instance type or the fallback.
func (n *NodePriceLookup) Price(instanceType string) float64 {
	if n == nil {
		return 0
	}
	if price, ok := n.prices[strings.ToLower(instanceType)]; ok {
		return price
	}
	return n.defaultCost
}

const bytesInGiB = 1024 * 1024 * 1024

// NetworkPriceLookup resolves egress cost by traffic class.
type NetworkPriceLookup struct {
	defaultEgressPrice float64
	egressPrices       map[string]float64
}

// NewNetworkPriceLookup builds a network price lookup.
func NewNetworkPriceLookup(defaultPrice float64, prices map[string]float64) *NetworkPriceLookup {
	n := &NetworkPriceLookup{
		defaultEgressPrice: defaultPrice,
		egressPrices:       map[string]float64{},
	}
	for k, v := range prices {
		if k == "" || v < 0 {
			continue
		}
		n.egressPrices[strings.ToLower(k)] = v
	}
	return n
}

// EgressCost returns the hourly cost for the egress bytes in the given class.
func (n *NetworkPriceLookup) EgressCost(class string, txBytes uint64) float64 {
	if n == nil {
		return 0
	}
	price := n.defaultEgressPrice
	if v, ok := n.egressPrices[strings.ToLower(class)]; ok {
		price = v
	}
	gib := float64(txBytes) / bytesInGiB
	return gib * price
}
