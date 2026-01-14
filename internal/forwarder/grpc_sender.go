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
func NewGRPCSender(ctx context.Context, endpoint, authToken string, timeout time.Duration) (*GRPCSender, error) {
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

func (s *GRPCSender) toProto(r AgentReport) *agentv1.ReportRequest {
	req := &agentv1.ReportRequest{
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
	for _, c := range r.Snapshot.Connections {
		req.Connections = append(req.Connections, s.connectionToProto(c))
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
			UsageUserNs:       p.Cpu.UsageUser,
			UsageKernelNs:     p.Cpu.UsageKernel,
			ThrottlingNs:      p.Cpu.Throttling,
			RequestMillicores: p.Cpu.RequestMillicores,
			LimitMillicores:   p.Cpu.LimitMillicores,
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

func (s *GRPCSender) connectionToProto(c snapshot.NetworkConnection) *agentv1.NetworkConnection {
	return &agentv1.NetworkConnection{
		Src:           s.endpointToProto(c.Src),
		Dst:           s.endpointToProto(c.Dst),
		Protocol:      c.Protocol,
		BytesSent:     c.BytesSent,
		BytesReceived: c.BytesReceived,
		EgressClass:   c.EgressClass,
		EgressCostUsd: c.EgressCostUSD,
		DstKind:       c.DstKind,
		ServiceMatch:  c.ServiceMatch,
		IsEgress:      c.IsEgress,
	}
}

func (s *GRPCSender) endpointToProto(e snapshot.NetworkEndpoint) *agentv1.NetworkEndpoint {
	endpoint := &agentv1.NetworkEndpoint{
		Ip:               e.IP,
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
