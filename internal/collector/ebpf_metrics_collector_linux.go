//go:build linux

package collector

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
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
	lastTime time.Time
	cache    map[string]uint64
	cacheAt  time.Time
	cacheTTL time.Duration
}

type metricKey struct {
	CgroupID uint64
}

type metricStats struct {
	CPUTimeNS   uint64
	MemoryBytes uint64
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
		cache:      map[string]uint64{},
		cacheTTL:   30 * time.Second,
	}
}

func (c *ebpfMetricsCollector) CollectPodMetrics(ctx context.Context, pods []*corev1.Pod) (map[string]kube.PodUsage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.ensureMap(); err != nil {
		return nil, err
	}

	podCgroups, err := c.mapPodCgroupIDsCached(pods)
	if err != nil {
		return nil, err
	}

	lookup := make(map[uint64]string, len(podCgroups))
	for key, cgID := range podCgroups {
		lookup[cgID] = key
	}

	now := time.Now()
	elapsed := now.Sub(c.lastTime)
	firstSample := c.lastTime.IsZero()
	c.lastTime = now
	elapsedNS := elapsed.Nanoseconds()
	if elapsedNS <= 0 {
		elapsedNS = 1
	}

	result := map[string]kube.PodUsage{}
	iter := c.usageMap.Iterate()
	var key metricKey
	var stats metricStats
	for iter.Next(&key, &stats) {
		podKey, ok := lookup[key.CgroupID]
		if !ok {
			continue
		}
		last := c.last[key.CgroupID]
		c.last[key.CgroupID] = stats

		var cpuMilli int64
		if !firstSample {
			deltaCPU := diffUint64(stats.CPUTimeNS, last.CPUTimeNS)
			cpuMilli = int64(float64(deltaCPU) / float64(elapsedNS) * 1000)
			if cpuMilli < 0 {
				cpuMilli = 0
			}
		}
		result[podKey] = kube.PodUsage{
			CPUUsageMilli:    cpuMilli,
			MemoryUsageBytes: int64(stats.MemoryBytes),
		}
	}

	if err := iter.Err(); err != nil {
		return result, fmt.Errorf("iterate eBPF metrics map: %w", err)
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

func mapPodCgroupIDs(cgroupRoot string, pods []*corev1.Pod) (map[string]uint64, error) {
	uidTokens := make(map[string]string, len(pods))
	for _, pod := range pods {
		if pod == nil || pod.UID == "" {
			continue
		}
		uid := string(pod.UID)
		token := "pod" + strings.ReplaceAll(uid, "-", "_")
		uidTokens[token] = fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
		uidTokens["pod"+uid] = fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
	}

	result := map[string]uint64{}
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
				result[podKey] = inode
			}
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	return result, nil
}

func (c *ebpfMetricsCollector) mapPodCgroupIDsCached(pods []*corev1.Pod) (map[string]uint64, error) {
	now := time.Now()
	if now.Sub(c.cacheAt) >= c.cacheTTL {
		cache, err := mapPodCgroupIDs(c.cgroupRoot, pods)
		if err != nil {
			return cache, err
		}
		c.cache = cache
		c.cacheAt = now
		return cache, nil
	}
	result := make(map[string]uint64, len(pods))
	for _, pod := range pods {
		if pod == nil || pod.UID == "" {
			continue
		}
		key := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
		if inode, ok := c.cache[key]; ok {
			result[key] = inode
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

func diffUint64(current, previous uint64) uint64 {
	if current >= previous {
		return current - previous
	}
	return current
}
