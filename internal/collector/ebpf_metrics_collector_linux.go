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

	// Cache mapping from Inode -> Pod Key
	inodeToPod map[uint64]string
	// Cache mapping from Pod Key -> Cgroup Path (Root) for RSS
	podToPath map[string]string

	cacheAt  time.Time
	cacheTTL time.Duration
}

type metricKey struct {
	CgroupID uint64
}

type metricStats struct {
	CPUUserNs       uint64
	CPUKernelNs     uint64
	CPURunDelayNs   uint64
	PageFaultsMajor uint64
	MemoryRSSBytes  uint64
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
		// Initialize empty maps
		inodeToPod: map[uint64]string{},
		podToPath:  map[string]string{},
		cacheTTL:   30 * time.Second,
	}
}

func (c *ebpfMetricsCollector) CollectPodMetrics(ctx context.Context, pods []*corev1.Pod) (map[string]kube.PodUsage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.ensureMap(); err != nil {
		return nil, err
	}

	if err := c.refreshCgroupCache(pods); err != nil {
		return nil, err
	}

	result := make(map[string]kube.PodUsage, len(pods))

	// Iterate map to get aggregated CPU/Fault stats
	iter := c.usageMap.Iterate()
	var key metricKey
	var stats metricStats

	// We simply accumulate counters for all cgroups belonging to a pod
	for iter.Next(&key, &stats) {
		podKey, ok := c.inodeToPod[key.CgroupID]
		if !ok {
			continue
		}

		c.last[key.CgroupID] = stats

		usage := result[podKey] // Zero value if new

		// Aggregate (sum) counters from all containers in the pod
		usage.CPUUsageUserNs += stats.CPUUserNs
		usage.CPUUsageKernelNs += stats.CPUKernelNs
		usage.CPUThrottlingNs += stats.CPURunDelayNs
		usage.MemoryPageFaults += stats.PageFaultsMajor

		result[podKey] = usage
	}

	if err := iter.Err(); err != nil {
		return result, fmt.Errorf("iterate eBPF metrics map: %w", err)
	}

	// Fill RSS from cgroup files (Userspace) - only from the Root Pod Cgroup
	for podKey, path := range c.podToPath {
		usage := result[podKey]
		// Determine if "path" is sufficient or if we need to recurse for RSS?
		// memory.current in the Pod Cgroup includes all children (Swap/Anon/File).
		// So reading root is correct for total Pod RSS/Memory.
		rss, err := readCgroupMemory(path)
		if err == nil {
			usage.MemoryRSS = rss
		}

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

func (c *ebpfMetricsCollector) refreshCgroupCache(pods []*corev1.Pod) error {
	now := time.Now()
	// Simple TTL cache
	if now.Sub(c.cacheAt) < c.cacheTTL && len(c.inodeToPod) > 0 {
		return nil
	}

	inodeToPod, podToPath, err := mapPodCgroups(c.cgroupRoot, pods)
	if err != nil {
		return err
	}

	c.inodeToPod = inodeToPod
	c.podToPath = podToPath
	c.cacheAt = now
	return nil
}

// mapPodCgroups walks the cgroup hierarchy and identifies:
// 1. All cgroups (inodes) that belong to a Pod (including children) -> inodeToPod
// 2. The specific Root cgroup path for the Pod (for RSS reading) -> podToPath
func mapPodCgroups(cgroupRoot string, pods []*corev1.Pod) (map[uint64]string, map[string]string, error) {
	// Prepare token matching
	uidTokens := make(map[string]string, len(pods))
	for _, pod := range pods {
		if pod == nil || pod.UID == "" {
			continue
		}
		uid := string(pod.UID)
		// Match various k8s cgroup naming patterns containing the UID
		// e.g. "pod<UID>", "pod<UID>_<etc>"
		token := strings.ReplaceAll(uid, "-", "_")
		uidTokens[token] = fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
	}

	inodeToPod := make(map[uint64]string)
	podToPath := make(map[string]string)

	if len(uidTokens) == 0 {
		return inodeToPod, podToPath, nil
	}

	err := filepath.WalkDir(cgroupRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			// Ignore access errors in cgroup walk, typical permissions
			return nil
		}
		if !entry.IsDir() {
			return nil
		}

		// Check if this directory corresponds to a known Pod
		// We check if the path contains the UID token.
		// NOTE: This assumes unique UIDs in paths.
		// Ideally we match the *Directory Name* for the Root, and path for children.
		//
		// Root cgroup usually looks like: .../kubepods/.../pod<UID-ish>/
		// Child cgroup: .../kubepods/.../pod<UID-ish>/<container-id>
		//
		// We want:
		// - If directory name contains "pod<UID>", it's the Root.
		// - If path contains "pod<UID>", it belongs to the Pod (Root or Child).

		dirName := entry.Name()
		// Optimization: Check if we are inside a relevant subtree?
		// For now, simpler check.

		for token, podKey := range uidTokens {
			if strings.Contains(path, token) {
				// This cgroup belongs to the pod (either root or child)
				// Get Inode
				inode, ok := cgroupInode(path)
				if ok {
					inodeToPod[inode] = podKey
				}

				// Check if this is the Root of the pod
				// Heuristic: The directory name itself contains "pod" + part of UID,
				// or it matches the pattern commonly used for Pod roots.
				// K8s usually names the Pod Cgroup directory "pod<UID-with-underscores>" or similar.
				// Containers are children named by ContainerID.
				if strings.Contains(dirName, token) || strings.Contains(dirName, "pod"+token) {
					// This is likely the root
					// Store it for RSS. If multiple match, last one wins (usually only one root).
					podToPath[podKey] = path
				}
			}
		}
		return nil
	})

	if err != nil {
		return nil, nil, err
	}
	return inodeToPod, podToPath, nil
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
	data, err := os.ReadFile(filepath.Join(cgroupPath, "memory.current"))
	if err != nil {
		return 0, err
	}
	val := strings.TrimSpace(string(data))
	return strconv.ParseUint(val, 10, 64)
}
