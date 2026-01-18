package forwarder

import (
	"context"
	"fmt"
	"log/slog"
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
	logger    *slog.Logger
}

// NewGRPCSender returns a configured GRPCSender.
func NewGRPCSender(ctx context.Context, endpoint, authToken string, timeout time.Duration, logger *slog.Logger) (*GRPCSender, error) {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	endpoint = strings.TrimPrefix(endpoint, "http://")
	endpoint = strings.TrimPrefix(endpoint, "https://")

	if !strings.HasPrefix(endpoint, "dns:///") {
		endpoint = "dns:///" + endpoint
	}

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
		logger:    logger,
	}, nil
}

func (s *GRPCSender) Close() error {
	return s.conn.Close()
}

func (s *GRPCSender) Send(ctx context.Context, report AgentReport) error {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	if s.authToken != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+s.authToken)
	}

	if report.Type == ReportTypeNetwork {
		if s.logger != nil {
			s.logger.Debug("sending network report via grpc", slog.String("endpoint", s.endpoint))
		}
		req := s.toNetworkProto(report)
		_, err := s.client.ReportNetwork(ctx, req)
		return err
	}

	// Default to metrics
	if s.logger != nil {
		s.logger.Debug("sending metrics report via grpc", slog.String("endpoint", s.endpoint))
	}
	req := s.toMetricsProto(report)
	_, err := s.client.ReportMetrics(ctx, req)
	return err
}

func (s *GRPCSender) toMetricsProto(r AgentReport) *agentv1.MetricsReportRequest {
	req := &agentv1.MetricsReportRequest{
		ClusterId:        r.ClusterID,
		NodeName:         r.NodeName,
		AvailabilityZone: r.AvailabilityZone,
		Region:           r.Region,
		InstanceType:     r.InstanceType,
		AgentId:          r.AgentID,
		TimestampSeconds: r.Timestamp.Unix(),
	}

	for _, p := range r.Snapshot.Pods {
		req.Pods = append(req.Pods, s.podMetricToProto(p))
	}
	for _, n := range r.Snapshot.Nodes {
		req.Nodes = append(req.Nodes, s.nodeMetricToProto(n))
	}

	return req
}

func (s *GRPCSender) toNetworkProto(r AgentReport) *agentv1.NetworkReportRequest {
	req := &agentv1.NetworkReportRequest{
		ClusterId:        r.ClusterID,
		NodeName:         r.NodeName,
		AvailabilityZone: r.AvailabilityZone,
		Region:           r.Region,
		InstanceType:     r.InstanceType,
		AgentId:          r.AgentID,
		TimestampSeconds: r.Timestamp.Unix(),
	}

	// Deduplication Map (IPKey -> Index)
	// Using IP as the unique key because an IP at a given time resolves to one entity.
	// We might need a composite key if IP is not enough, but NetworkEndpoint has all metadata.
	// To be safe, we can serialize the endpoint to string as key? Or just use IP?
	// The Snapshot Builder resolves IP -> Metadata. This mapping is static for the snapshot.
	// So IP is a sufficient key for *within* this snapshot unless we have overlapping IPs (not within same snapshot usually).
	// Actually, let's use the full Endpoint content as key to be absolutely safe (e.g. if DNS name differs).
	endpointIndex := make(map[string]uint32)

	getOrAddEndpoint := func(e snapshot.NetworkEndpoint) uint32 {
		// Simple unique string key: IP + Pod + NS
		// Or marshaled proto?
		// Sufficient key: IP
		// If builder resolved same IP to different Metadata in same snapshot, that would be weird.
		// Let's use IP as key.
		key := e.IP
		if idx, ok := endpointIndex[key]; ok {
			return idx
		}

		if len(req.Endpoints) > 4294967295 {
			s.logger.Warn("too many endpoints for network report", "count", len(req.Endpoints))
			return 0 // Fallback to 0 or skip? safely returning 0 might be wrong but better than crash
		}
		idx := uint32(len(req.Endpoints)) // #nosec G115 -- checked above
		endpointIndex[key] = idx
		req.Endpoints = append(req.Endpoints, s.endpointToProto(e))
		return idx
	}

	for _, c := range r.Snapshot.Connections {
		srcIdx := getOrAddEndpoint(c.Src)
		dstIdx := getOrAddEndpoint(c.Dst)

		req.CompactConnections = append(req.CompactConnections, &agentv1.CompactNetworkConnection{
			SrcIndex:      srcIdx,
			DstIndex:      dstIdx,
			Protocol:      c.Protocol,
			BytesSent:     c.BytesSent,
			BytesReceived: c.BytesReceived,
			EgressClass:   c.EgressClass,
			DstKind:       c.DstKind,
			ServiceMatch:  c.ServiceMatch,
			IsEgress:      c.IsEgress,
		})
	}
	for _, p := range r.Snapshot.Pods {
		req.Pods = append(req.Pods, s.podMetricToProto(p))
	}

	return req
}

func (s *GRPCSender) podMetricToProto(p snapshot.PodMetric) *agentv1.PodMetric {
	return &agentv1.PodMetric{
		PodUid:      p.PodUID,
		ContainerId: p.ContainerID,
		PidTgid:     p.PID,
		Namespace:   p.Namespace,
		PodName:     p.PodName,
		Cpu: &agentv1.CpuMetrics{
			RequestMillicores: p.Cpu.RequestMillicores,
			LimitMillicores:   p.Cpu.LimitMillicores,
			UsageMillicores:   p.Cpu.UsageMillicores,
		},
		Memory: &agentv1.MemoryMetrics{
			RssBytes:        p.Memory.RSS,
			PageFaultsMajor: p.Memory.PageFaults,
			RequestBytes:    p.Memory.RequestBytes,
			LimitBytes:      p.Memory.LimitBytes,
		},
		Network: &agentv1.NetworkMetrics{
			BytesSent:           p.Network.BytesSent,
			BytesReceived:       p.Network.BytesReceived,
			EgressPublicBytes:   p.Network.EgressPublic,
			EgressCrossAzBytes:  p.Network.EgressCrossAZ,
			EgressInternalBytes: p.Network.EgressInternal,
		},
		Storage: &agentv1.StorageMetrics{
			ReadBytes:      p.Storage.ReadBytes,
			WriteBytes:     p.Storage.WriteBytes,
			ReadOps:        p.Storage.ReadOps,
			WriteOps:       p.Storage.WriteOps,
			TotalLatencyNs: p.Storage.TotalLatency,
		},
	}
}

func (s *GRPCSender) endpointToProto(e snapshot.NetworkEndpoint) *agentv1.NetworkEndpoint {
	endpoint := &agentv1.NetworkEndpoint{
		Ip:               e.IP,
		DnsName:          e.DnsName,
		Namespace:        e.Namespace,
		PodName:          e.PodName,
		NodeName:         e.NodeName,
		AvailabilityZone: e.AvailabilityZone,
	}
	for _, svc := range e.Services {
		endpoint.Services = append(endpoint.Services, &agentv1.ServiceRef{
			Namespace: svc.Namespace,
			Name:      svc.Name,
		})
	}
	return endpoint
}

func (s *GRPCSender) nodeMetricToProto(n snapshot.NodeMetric) *agentv1.NodeMetric {
	return &agentv1.NodeMetric{
		NodeName:                 n.NodeName,
		CpuUsageMillicores:       n.CPUUsageMillicores,
		MemoryUsageBytes:         n.MemoryUsageBytes,
		CapacityCpuMillicores:    n.CapacityCPUMilli,
		CapacityMemoryBytes:      n.CapacityMemoryBytes,
		AllocatableCpuMillicores: n.AllocatableCPUMilli,
		AllocatableMemoryBytes:   n.AllocatableMemBytes,
		RequestedCpuMillicores:   n.RequestedCPUMilli,
		RequestedMemoryBytes:     n.RequestedMemBytes,
		ThrottlingNs:             n.ThrottlingNs,
		Network: &agentv1.NetworkMetrics{
			BytesSent:           n.Network.BytesSent,
			BytesReceived:       n.Network.BytesReceived,
			EgressPublicBytes:   n.Network.EgressPublic,
			EgressCrossAzBytes:  n.Network.EgressCrossAZ,
			EgressInternalBytes: n.Network.EgressInternal,
		},
	}
}
