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
