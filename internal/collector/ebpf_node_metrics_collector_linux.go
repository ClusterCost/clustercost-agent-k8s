//go:build linux

package collector

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"clustercost-agent-k8s/internal/config"
	"clustercost-agent-k8s/internal/kube"

	"github.com/cilium/ebpf"
	corev1 "k8s.io/api/core/v1"
)

type ebpfNodeMetricsCollector struct {
	mapPath string
	logger  *slog.Logger

	mu       sync.Mutex
	usageMap *ebpf.Map
	last     map[uint64]nodeMetricStats
	lastAt   time.Time

	nodeName string
	warned   bool
}

type nodeMetricKey struct {
	CgroupID uint64
}

type nodeMetricStats struct {
	CPUUserNs       uint64
	CPUKernelNs     uint64
	CPURunDelayNs   uint64
	PageFaultsMajor uint64
	MemoryRSSBytes  uint64
}

func newEBPFNodeMetricsCollector(cfg config.MetricsConfig, nodeName string, logger *slog.Logger) NodeMetricsCollector {
	mapPath := cfg.BPFMapPath
	if mapPath == "" {
		mapPath = "/sys/fs/bpf/clustercost/metrics"
	}
	return &ebpfNodeMetricsCollector{
		mapPath:  mapPath,
		logger:   logger,
		last:     map[uint64]nodeMetricStats{},
		nodeName: nodeName,
	}
}

func (c *ebpfNodeMetricsCollector) CollectNodeMetrics(ctx context.Context, nodes []*corev1.Node) (map[string]kube.NodeUsage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.ensureMap(); err != nil {
		return nil, err
	}

	nodeKey, ok := c.resolveNodeName(nodes)
	if !ok {
		return map[string]kube.NodeUsage{}, nil
	}

	now := time.Now()
	if c.lastAt.IsZero() {
		c.lastAt = now
		c.snapshotCurrent()
		return map[string]kube.NodeUsage{nodeKey: {CPUUsageMilli: 0, MemoryUsageBytes: readNodeMemoryUsage()}}, nil
	}

	dtNs := now.Sub(c.lastAt).Nanoseconds()
	if dtNs <= 0 {
		return map[string]kube.NodeUsage{nodeKey: {CPUUsageMilli: 0, MemoryUsageBytes: readNodeMemoryUsage()}}, nil
	}

	deltaNs, err := c.deltaCPU()
	if err != nil {
		return nil, err
	}

	millicores := int64(math.Round((float64(deltaNs) / float64(dtNs)) * 1000))
	if millicores < 0 {
		millicores = 0
	}

	c.lastAt = now

	return map[string]kube.NodeUsage{nodeKey: {CPUUsageMilli: millicores, MemoryUsageBytes: readNodeMemoryUsage()}}, nil
}

func (c *ebpfNodeMetricsCollector) ensureMap() error {
	if c.usageMap != nil {
		return nil
	}
	m, err := ebpf.LoadPinnedMap(c.mapPath, nil)
	if err != nil {
		return fmt.Errorf("load pinned eBPF metrics map at %s: %w", c.mapPath, err)
	}
	c.usageMap = m
	return nil
}

func (c *ebpfNodeMetricsCollector) resolveNodeName(nodes []*corev1.Node) (string, bool) {
	if c.nodeName != "" {
		for _, node := range nodes {
			if node != nil && node.Name == c.nodeName {
				return c.nodeName, true
			}
		}
		if c.logger != nil && !c.warned {
			c.logger.Warn("node name not found in cache; node metrics disabled", slog.String("nodeName", c.nodeName))
			c.warned = true
		}
		return "", false
	}

	if len(nodes) == 1 && nodes[0] != nil {
		return nodes[0].Name, true
	}

	if c.logger != nil && !c.warned {
		c.logger.Warn("node metrics require node name or single-node view; node metrics disabled")
		c.warned = true
	}
	return "", false
}

func (c *ebpfNodeMetricsCollector) snapshotCurrent() {
	current := map[uint64]nodeMetricStats{}
	iter := c.usageMap.Iterate()
	var key nodeMetricKey
	var stats nodeMetricStats
	for iter.Next(&key, &stats) {
		current[key.CgroupID] = stats
	}
	if iter.Err() == nil {
		c.last = current
	}
}

func (c *ebpfNodeMetricsCollector) deltaCPU() (uint64, error) {
	current := map[uint64]nodeMetricStats{}
	iter := c.usageMap.Iterate()
	var key nodeMetricKey
	var stats nodeMetricStats

	var total uint64
	for iter.Next(&key, &stats) {
		current[key.CgroupID] = stats
		prev, ok := c.last[key.CgroupID]
		if !ok {
			continue
		}
		deltaUser := diffUint64(stats.CPUUserNs, prev.CPUUserNs)
		deltaKernel := diffUint64(stats.CPUKernelNs, prev.CPUKernelNs)
		total += deltaUser + deltaKernel
	}
	if err := iter.Err(); err != nil {
		return 0, fmt.Errorf("iterate eBPF metrics map: %w", err)
	}

	c.last = current
	return total, nil
}

func diffUint64(current, prev uint64) uint64 {
	if current <= prev {
		return 0
	}
	return current - prev
}

func readNodeMemoryUsage() int64 {
	total, available, err := readMemInfo()
	if err != nil || total == 0 || available > total {
		return 0
	}
	return int64(total - available)
}

func readMemInfo() (uint64, uint64, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()

	var totalKB uint64
	var availableKB uint64

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "MemTotal:") {
			totalKB = parseMeminfoValue(line)
		}
		if strings.HasPrefix(line, "MemAvailable:") {
			availableKB = parseMeminfoValue(line)
		}
		if totalKB > 0 && availableKB > 0 {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, 0, err
	}
	if totalKB == 0 || availableKB == 0 {
		return 0, 0, fmt.Errorf("meminfo missing MemTotal or MemAvailable")
	}
	return totalKB * 1024, availableKB * 1024, nil
}

func parseMeminfoValue(line string) uint64 {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return 0
	}
	val, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0
	}
	return val
}
