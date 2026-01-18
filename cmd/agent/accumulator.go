package main

import (
	"clustercost-agent-k8s/internal/network"
	"fmt"
)

type FlowAccumulator struct {
	flows map[string]network.Flow
}

func NewFlowAccumulator() *FlowAccumulator {
	return &FlowAccumulator{
		flows: make(map[string]network.Flow),
	}
}

func (a *FlowAccumulator) Add(flows []network.Flow) {
	if a.flows == nil {
		a.flows = make(map[string]network.Flow)
	}
	for _, f := range flows {
		// Key: src|dst|proto
		// Using string key is simple enough
		key := fmt.Sprintf("%s|%s|%d", f.SrcIP, f.DstIP, f.Protocol)

		existing, ok := a.flows[key]
		if !ok {
			a.flows[key] = f
		} else {
			existing.TxBytes += f.TxBytes
			existing.RxBytes += f.RxBytes
			a.flows[key] = existing
		}
	}
}

func (a *FlowAccumulator) Flush() []network.Flow {
	out := make([]network.Flow, 0, len(a.flows))
	for _, f := range a.flows {
		out = append(out, f)
	}
	a.flows = make(map[string]network.Flow)
	return out
}
