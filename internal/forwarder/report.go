package forwarder

import (
	"time"

	"clustercost-agent-k8s/internal/snapshot"
)

// AgentReport is the payload forwarded to the central agent.
type AgentReport struct {
	ClusterID   string            `json:"clusterId"`
	ClusterName string            `json:"clusterName"`
	NodeName    string            `json:"nodeName"`
	Version     string            `json:"version"`
	Timestamp   time.Time         `json:"timestamp"`
	Snapshot    snapshot.Snapshot `json:"snapshot"`
}
