package forwarder

import (
	"context"
	"fmt"
	"strings"
	"time"

	agentv1 "clustercost-agent-k8s/internal/proto/agent/v1"
	"clustercost-agent-k8s/internal/snapshot"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// GRPCSender sends reports via gRPC.
type GRPCSender struct {
	client    agentv1.CollectorClient
	conn      *grpc.ClientConn
	endpoint  string
	authToken string
	timeout   time.Duration
}

// NewGRPCSender returns a configured GRPCSender.
// It establishes the connection immediately (blocking or non-blocking? grpc.NewClient is non-blocking).
func NewGRPCSender(ctx context.Context, endpoint, authToken string, timeout time.Duration) (*GRPCSender, error) {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	// Strip http:// or https:// scheme if present (gRPC expects target without scheme)
	endpoint = strings.TrimPrefix(endpoint, "http://")
	endpoint = strings.TrimPrefix(endpoint, "https://")

	// Add dns:/// scheme for proper Kubernetes DNS resolution
	// This tells gRPC to use the DNS resolver instead of passthrough
	if !strings.HasPrefix(endpoint, "dns:///") {
		endpoint = "dns:///" + endpoint
	}

	// TODO: Add TLS support based on config/scheme. For now using insecure.
	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("grpc dial: %w", err)
	}

	return &GRPCSender{
		client:    agentv1.NewCollectorClient(conn),
		conn:      conn,
		endpoint:  endpoint,
		authToken: authToken,
		timeout:   timeout,
	}, nil
}

func (s *GRPCSender) Close() error {
	return s.conn.Close()
}

func (s *GRPCSender) Send(ctx context.Context, report AgentReport) error {
	req := s.toProto(report)
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	if s.authToken != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+s.authToken)
	}

	_, err := s.client.Report(ctx, req)
	return err
}

func (s *GRPCSender) SendBatch(ctx context.Context, reports []AgentReport) error {
	if len(reports) == 0 {
		return nil
	}
	req := &agentv1.ReportBatchRequest{
		Reports: make([]*agentv1.ReportRequest, len(reports)),
	}
	for i, r := range reports {
		req.Reports[i] = s.toProto(r)
	}

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	if s.authToken != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+s.authToken)
	}

	_, err := s.client.ReportBatch(ctx, req)
	return err
}

func (s *GRPCSender) toProto(r AgentReport) *agentv1.ReportRequest {
	return &agentv1.ReportRequest{
		ClusterId:        r.ClusterID,
		ClusterName:      r.ClusterName,
		NodeName:         r.NodeName,
		AgentId:          r.AgentID,
		Version:          r.Version,
		TimestampSeconds: r.Timestamp.Unix(),
		Snapshot:         s.snapshotToProto(r.Snapshot),
	}
}

func (s *GRPCSender) snapshotToProto(snap snapshot.Snapshot) *agentv1.Snapshot {
	out := &agentv1.Snapshot{
		TimestampSeconds: snap.Timestamp.Unix(),
		Resources:        s.resourceSnapshotToProto(snap.Resources),
		Network:          s.networkSnapshotToProto(snap.Network),
	}

	for _, n := range snap.Namespaces {
		out.Namespaces = append(out.Namespaces, s.namespaceCostToProto(n))
	}
	for _, n := range snap.Nodes {
		out.Nodes = append(out.Nodes, s.nodeCostToProto(n))
	}
	return out
}

func (s *GRPCSender) namespaceCostToProto(n snapshot.NamespaceCostRecord) *agentv1.NamespaceCostRecord {
	return &agentv1.NamespaceCostRecord{
		ClusterId:          n.ClusterID,
		Namespace:          n.Namespace,
		Labels:             n.Labels,
		Environment:        n.Environment,
		CpuRequestMilli:    n.CPURequestMilli,
		CpuUsageMilli:      n.CPUUsageMilli,
		MemoryRequestBytes: n.MemoryRequestBytes,
		MemoryUsageBytes:   n.MemoryUsageBytes,
		PodCount:           int64(n.PodCount),
		HourlyCost:         n.HourlyCost,
		NetworkTxBytes:     n.NetworkTxBytes,
		NetworkRxBytes:     n.NetworkRxBytes,
		NetworkEgressCost:  n.NetworkEgressCost,
	}
}

func (s *GRPCSender) nodeCostToProto(n snapshot.NodeCostRecord) *agentv1.NodeCostRecord {
	return &agentv1.NodeCostRecord{
		ClusterId:              n.ClusterID,
		NodeName:               n.NodeName,
		CpuAllocatableMilli:    n.CPUAllocatableMilli,
		MemoryAllocatableBytes: n.MemoryAllocatableBytes,
		Labels:                 n.Labels,
		Taints:                 n.Taints,
		InstanceType:           n.InstanceType,
		Status:                 n.Status,
		IsUnderPressure:        n.IsUnderPressure,
		HourlyCost:             n.HourlyCost,
		CpuUsagePercent:        n.CPUUsagePercent,
		MemoryUsagePercent:     n.MemoryUsagePercent,
		PodCount:               int64(n.PodCount),
	}
}

func (s *GRPCSender) resourceSnapshotToProto(r snapshot.ResourceSnapshot) *agentv1.ResourceSnapshot {
	return &agentv1.ResourceSnapshot{
		ClusterId:               r.ClusterID,
		CpuUsageMilliTotal:      r.CPUUsageMilliTotal,
		CpuRequestMilliTotal:    r.CPURequestMilliTotal,
		MemoryUsageBytesTotal:   r.MemoryUsageBytesTotal,
		MemoryRequestBytesTotal: r.MemoryRequestBytesTotal,
		TotalNodeHourlyCost:     r.TotalNodeHourlyCost,
		NetworkTxBytesTotal:     r.NetworkTxBytesTotal,
		NetworkRxBytesTotal:     r.NetworkRxBytesTotal,
		NetworkEgressCostTotal:  r.NetworkEgressCostTotal,
	}
}

func (s *GRPCSender) networkSnapshotToProto(n snapshot.NetworkSnapshot) *agentv1.NetworkSnapshot {
	out := &agentv1.NetworkSnapshot{
		ClusterId:  n.ClusterID,
		TxBytes:    n.TxBytes,
		RxBytes:    n.RxBytes,
		EgressCost: n.EgressCost,
	}

	for _, c := range n.ByClass {
		out.ByClass = append(out.ByClass, &agentv1.NetworkClassTotals{
			Class:            c.Class,
			TxBytes:          c.TxBytes,
			RxBytes:          c.RxBytes,
			EgressCostHourly: c.EgressCostHourly,
		})
	}
	for _, p := range n.Pods {
		out.Pods = append(out.Pods, s.podNetworkToProto(p))
	}
	for _, ns := range n.Namespaces {
		out.Namespaces = append(out.Namespaces, s.namespaceNetworkToProto(ns))
	}
	for _, c := range n.PodConnections {
		out.PodConnections = append(out.PodConnections, s.connectionToProto(c))
	}
	for _, c := range n.WorkloadConnections {
		out.WorkloadConnections = append(out.WorkloadConnections, s.connectionToProto(c))
	}
	for _, c := range n.NamespaceConnections {
		out.NamespaceConnections = append(out.NamespaceConnections, s.connectionToProto(c))
	}
	for _, c := range n.ServiceConnections {
		out.ServiceConnections = append(out.ServiceConnections, s.connectionToProto(c))
	}
	return out
}

func (s *GRPCSender) podNetworkToProto(p snapshot.PodNetworkRecord) *agentv1.PodNetworkRecord {
	rec := &agentv1.PodNetworkRecord{
		Namespace:        p.Namespace,
		Pod:              p.Pod,
		Node:             p.Node,
		TxBytes:          p.TxBytes,
		RxBytes:          p.RxBytes,
		EgressCostHourly: p.EgressCostHourly,
	}
	for _, c := range p.ByClass {
		rec.ByClass = append(rec.ByClass, &agentv1.NetworkClassTotals{
			Class:            c.Class,
			TxBytes:          c.TxBytes,
			RxBytes:          c.RxBytes,
			EgressCostHourly: c.EgressCostHourly,
		})
	}
	return rec
}

func (s *GRPCSender) namespaceNetworkToProto(n snapshot.NamespaceNetworkRecord) *agentv1.NamespaceNetworkRecord {
	rec := &agentv1.NamespaceNetworkRecord{
		Namespace:        n.Namespace,
		TxBytes:          n.TxBytes,
		RxBytes:          n.RxBytes,
		EgressCostHourly: n.EgressCostHourly,
	}
	for _, c := range n.ByClass {
		rec.ByClass = append(rec.ByClass, &agentv1.NetworkClassTotals{
			Class:            c.Class,
			TxBytes:          c.TxBytes,
			RxBytes:          c.RxBytes,
			EgressCostHourly: c.EgressCostHourly,
		})
	}
	return rec
}

func (s *GRPCSender) connectionToProto(c snapshot.NetworkConnection) *agentv1.NetworkConnection {
	return &agentv1.NetworkConnection{
		Source: &agentv1.NetworkEndpoint{
			Kind:      c.Source.Kind,
			Namespace: c.Source.Namespace,
			Name:      c.Source.Name,
		},
		Destination: &agentv1.NetworkEndpoint{
			Kind:      c.Destination.Kind,
			Namespace: c.Destination.Namespace,
			Name:      c.Destination.Name,
		},
		Class:            c.Class,
		TxBytes:          c.TxBytes,
		RxBytes:          c.RxBytes,
		EgressCostHourly: c.EgressCostHourly,
	}
}
