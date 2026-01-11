//go:build linux

package collector

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"clustercost-agent-k8s/internal/config"
	"clustercost-agent-k8s/internal/kube"

	"github.com/cilium/ebpf"
	corev1 "k8s.io/api/core/v1"
)

type ebpfMetricsCollector struct {
	mapPath    string
	cgroupRoot string
	logger     *slog.Logger

	mu       sync.Mutex
	usageMap *ebpf.Map
	last     map[uint64]metricStats

	// Cache mapping from Pod Key (ns/name) -> Cgroup Info
	cache    map[string]cgroupInfo
	cacheAt  time.Time
	cacheTTL time.Duration
}

type cgroupInfo struct {
	Inode uint64
	Path  string
}

type metricKey struct {
	CgroupID uint64
}

type metricStats struct {
	CPUUserNs       uint64
	CPUKernelNs     uint64
	CPURunDelayNs   uint64
	PageFaultsMajor uint64
	MemoryRSSBytes  uint64 // Assuming BPF could have it, or we fill it manually
}

// newEBPFMetricsCollector reads a pinned eBPF map of cgroup stats.
func newEBPFMetricsCollector(cfg config.MetricsConfig, logger *slog.Logger) PodMetricsCollector {
	mapPath := cfg.BPFMapPath
	if mapPath == "" {
		mapPath = "/sys/fs/bpf/clustercost/metrics"
	}
	cgroupRoot := cfg.CgroupRoot
	if cgroupRoot == "" {
		cgroupRoot = "/sys/fs/cgroup"
	}
	return &ebpfMetricsCollector{
		mapPath:    mapPath,
		cgroupRoot: cgroupRoot,
		logger:     logger,
		last:       map[uint64]metricStats{},
		cache:      map[string]cgroupInfo{},
		cacheTTL:   30 * time.Second,
	}
}

func (c *ebpfMetricsCollector) CollectPodMetrics(ctx context.Context, pods []*corev1.Pod) (map[string]kube.PodUsage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.ensureMap(); err != nil {
		return nil, err
	}

	podCgroups, err := c.mapPodCgroupsCached(pods)
	if err != nil {
		return nil, err
	}

	// Reverse lookup: Inode -> Pod Key
	inodeToPod := make(map[uint64]string, len(podCgroups))
	for key, info := range podCgroups {
		inodeToPod[info.Inode] = key
	}

	result := make(map[string]kube.PodUsage, len(pods))

	// Pre-fill result map with zero usage so we include all running pods?
	// Or just those we find stats for.

	// Iterate map to get aggregated CPU/Fault stats
	iter := c.usageMap.Iterate()
	var key metricKey
	var stats metricStats
	for iter.Next(&key, &stats) {
		podKey, ok := inodeToPod[key.CgroupID]
		if !ok {
			continue
		}

		// We track last but don't strictly usage it for diff if sending counters
		c.last[key.CgroupID] = stats

		usage := result[podKey]

		// CPU & Faults are counters (deltas accumulated).
		// We want total since start? Or delta since last report?
		// The Agent buffers for 10s. The aggregator likely expects "Usage during window" or "Total usage counter"?
		// Spec: "CPU Usage: Total nanoseconds/cycles...". Usually implies counter.
		// "Throughput: Total bytes_sent".
		// But in `builder.go` we just assign.
		// If we assign counters, the aggregator can compute rate.
		// `Sched_stat_runtime` is cumulative. `utime` is cumulative.
		// So we can send Cumulative values.
		// BPF map stores Cumulative (it adds deltas to a counter).
		// So `stats.CPUUserNs` is cumulative from map creation/agent start.
		// Actually BPF map persists?
		// "Stateless... buffers events for 10s".
		// Agent just reports what's in the map.
		// Aggregator calc rates.

		usage.CPUUsageUserNs = stats.CPUUserNs
		usage.CPUUsageKernelNs = stats.CPUKernelNs
		usage.CPUThrottlingNs = stats.CPURunDelayNs
		usage.MemoryPageFaults = stats.PageFaultsMajor

		result[podKey] = usage
	}

	if err := iter.Err(); err != nil {
		return result, fmt.Errorf("iterate eBPF metrics map: %w", err)
	}

	// Fill RSS from cgroup files (Userspace)
	for podKey, info := range podCgroups {
		usage := result[podKey]
		rss, err := readCgroupMemory(info.Path)
		if err == nil {
			usage.MemoryRSS = rss
		}

		// If we had no BPF entry (idle pod?), usage is 0 counters.
		// But we have RSS.
		result[podKey] = usage
	}

	return result, nil
}

func (c *ebpfMetricsCollector) ensureMap() error {
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

func mapPodCgroups(cgroupRoot string, pods []*corev1.Pod) (map[string]cgroupInfo, error) {
	uidTokens := make(map[string]string, len(pods))
	for _, pod := range pods {
		if pod == nil || pod.UID == "" {
			continue
		}
		uid := string(pod.UID)
		// Match typical k8s cgroup naming patterns
		token := "pod" + strings.ReplaceAll(uid, "-", "_")
		uidTokens[token] = fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
		uidTokens["pod"+uid] = fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
	}

	result := map[string]cgroupInfo{}
	if len(uidTokens) == 0 {
		return result, nil
	}

	err := filepath.WalkDir(cgroupRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			return nil
		}
		base := entry.Name()
		for token, podKey := range uidTokens {
			if !strings.Contains(base, token) {
				continue
			}
			if _, ok := result[podKey]; ok {
				continue
			}
			if inode, ok := cgroupInode(path); ok {
				result[podKey] = cgroupInfo{Inode: inode, Path: path}
			}
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	return result, nil
}

func (c *ebpfMetricsCollector) mapPodCgroupsCached(pods []*corev1.Pod) (map[string]cgroupInfo, error) {
	now := time.Now()
	if now.Sub(c.cacheAt) >= c.cacheTTL {
		cache, err := mapPodCgroups(c.cgroupRoot, pods)
		if err != nil {
			return cache, err
		}
		c.cache = cache
		c.cacheAt = now
		return cache, nil
	}
	result := make(map[string]cgroupInfo, len(pods))
	for _, pod := range pods {
		if pod == nil || pod.UID == "" {
			continue
		}
		key := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
		if info, ok := c.cache[key]; ok {
			result[key] = info
		}
	}
	return result, nil
}

func cgroupInode(path string) (uint64, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return stat.Ino, true
}

func readCgroupMemory(cgroupPath string) (uint64, error) {
	// Try cgroup v2 memory.current
	// Or memory.stat for anon + file
	data, err := os.ReadFile(filepath.Join(cgroupPath, "memory.current"))
	if err != nil {
		return 0, err
	}
	val := strings.TrimSpace(string(data))
	return strconv.ParseUint(val, 10, 64)
}
