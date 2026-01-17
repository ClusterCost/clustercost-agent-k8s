//go:build linux

package collector

import (
	"context"
	"log/slog"
	"math"
	"sync"
	"time"

	"clustercost-agent-k8s/internal/config"
	"clustercost-agent-k8s/internal/kube"

	corev1 "k8s.io/api/core/v1"
)

type cgroupNodeMetricsCollector struct {
	cgroupRoot string
	logger     *slog.Logger

	mu     sync.Mutex
	last   cpuStat
	lastAt time.Time

	layout    cgroupLayout
	layoutErr error

	nodeName     string
	layoutLogged bool
	nodeWarned   bool
}

func newCgroupNodeMetricsCollector(cfg config.MetricsConfig, nodeName string, logger *slog.Logger) NodeMetricsCollector {
	cgroupRoot := cfg.CgroupRoot
	if cgroupRoot == "" {
		cgroupRoot = "/sys/fs/cgroup"
	}
	layout, layoutErr := detectCgroupLayout(cgroupRoot)
	return &cgroupNodeMetricsCollector{
		cgroupRoot: cgroupRoot,
		logger:     logger,
		layout:     layout,
		layoutErr:  layoutErr,
		nodeName:   nodeName,
	}
}

func (c *cgroupNodeMetricsCollector) CollectNodeMetrics(ctx context.Context, nodes []*corev1.Node) (map[string]kube.NodeUsage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	nodeKey, ok := c.resolveNodeName(nodes)
	if !ok {
		return map[string]kube.NodeUsage{}, nil
	}

	if c.layoutErr != nil && c.logger != nil && !c.layoutLogged {
		c.logger.Warn("cgroup layout detection incomplete; node metrics may be partial", slog.String("error", c.layoutErr.Error()))
	}

	cpuAcctPath := c.layout.root
	cpuPath := c.layout.root
	memPath := c.layout.root
	if !c.layout.unified {
		cpuAcctPath = c.layout.cpuAcctRoot
		cpuPath = c.layout.cpuRoot
		memPath = c.layout.memRoot
	}

	if c.logger != nil && !c.layoutLogged {
		if c.layout.unified {
			c.logger.Info("detected cgroup v2 (unified) layout for node metrics", slog.String("root", c.layout.root))
		} else {
			c.logger.Info("detected cgroup v1 layout for node metrics",
				slog.String("cpuacct", c.layout.cpuAcctRoot),
				slog.String("cpu", c.layout.cpuRoot),
				slog.String("memory", c.layout.memRoot),
			)
		}
		c.layoutLogged = true
	}

	if c.layout.unified && c.layout.root == "" {
		return map[string]kube.NodeUsage{}, nil
	}
	if !c.layout.unified && cpuAcctPath == "" && cpuPath == "" && memPath == "" {
		return map[string]kube.NodeUsage{}, nil
	}

	now := time.Now()
	if c.lastAt.IsZero() {
		c.lastAt = now
		var throttled uint64
		if cpuAcctPath != "" || cpuPath != "" {
			if stats, err := readCPUStat(cpuAcctPath, cpuPath); err == nil {
				c.last = stats
				throttled = stats.ThrottledNs
			}
		}
		return map[string]kube.NodeUsage{nodeKey: {CPUUsageMilli: 0, MemoryUsageBytes: readNodeMemoryUsage(memPath), ThrottledNs: throttled}}, nil
	}

	dtNs := now.Sub(c.lastAt).Nanoseconds()
	if dtNs <= 0 {
		var throttled uint64
		if cpuAcctPath != "" || cpuPath != "" {
			if stats, err := readCPUStat(cpuAcctPath, cpuPath); err == nil {
				throttled = stats.ThrottledNs
			}
		}
		return map[string]kube.NodeUsage{nodeKey: {CPUUsageMilli: 0, MemoryUsageBytes: readNodeMemoryUsage(memPath), ThrottledNs: throttled}}, nil
	}

	var current cpuStat
	if cpuAcctPath != "" || cpuPath != "" {
		if c.logger != nil && c.logger.Enabled(context.Background(), slog.LevelDebug) {
			c.logger.Debug("reading node cpu stats", slog.String("cpuAcct", cpuAcctPath), slog.String("cpu", cpuPath))
		}
		stats, err := readCPUStat(cpuAcctPath, cpuPath)
		if err != nil {
			return nil, err
		}
		current = stats

		if c.logger != nil && c.logger.Enabled(context.Background(), slog.LevelDebug) {
			c.logger.Debug("node cpu stats read",
				slog.Uint64("usageNs", current.UsageNs),
				slog.Uint64("userNs", current.UserNs),
				slog.Uint64("systemNs", current.SystemNs),
			)
		}
	}
	deltaNs := diffUint64(current.UsageNs, c.last.UsageNs)

	millicores := int64(0)
	if deltaNs > 0 {
		millicores = int64(math.Round((float64(deltaNs) / float64(dtNs)) * 1000))
		if millicores < 0 {
			millicores = 0
		}
	}

	c.lastAt = now
	c.last = current

	return map[string]kube.NodeUsage{nodeKey: {CPUUsageMilli: millicores, MemoryUsageBytes: readNodeMemoryUsage(memPath), ThrottledNs: current.ThrottledNs}}, nil
}

func (c *cgroupNodeMetricsCollector) resolveNodeName(nodes []*corev1.Node) (string, bool) {
	if c.nodeName != "" {
		for _, node := range nodes {
			if node != nil && node.Name == c.nodeName {
				return c.nodeName, true
			}
		}
		if c.logger != nil && !c.nodeWarned {
			c.logger.Warn("node name not found in cache; node metrics disabled", slog.String("nodeName", c.nodeName))
			c.nodeWarned = true
		}
		return "", false
	}

	if len(nodes) == 1 && nodes[0] != nil {
		return nodes[0].Name, true
	}

	if c.logger != nil && !c.nodeWarned {
		c.logger.Warn("node metrics require node name or single-node view; node metrics disabled")
		c.nodeWarned = true
	}
	return "", false
}

func readNodeMemoryUsage(memPath string) int64 {
	val, err := readCgroupMemory(memPath)
	if err != nil {
		return 0
	}
	return int64(val)
}
