//go:build linux

package collector

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"clustercost-agent-k8s/internal/config"
	"clustercost-agent-k8s/internal/kube"

	corev1 "k8s.io/api/core/v1"
)

type cgroupMetricsCollector struct {
	cgroupRoot string
	logger     *slog.Logger

	mu   sync.Mutex
	last map[string]cpuStat
	at   time.Time

	// Cache mapping from Pod Key -> Cgroup Paths
	podToPaths map[string]podCgroupPaths
	cacheAt    time.Time
	cacheTTL   time.Duration

	layoutLogged bool
}

type cpuStat struct {
	UsageNs     uint64
	UserNs      uint64
	SystemNs    uint64
	ThrottledNs uint64
}

type podCgroupPaths struct {
	cpuAcct string
	cpu     string
	mem     string
}

// newCgroupMetricsCollector reads cgroup cpu.stat and memory.current.
func newCgroupMetricsCollector(cfg config.MetricsConfig, logger *slog.Logger) PodMetricsCollector {
	cgroupRoot := cfg.CgroupRoot
	if cgroupRoot == "" {
		cgroupRoot = "/sys/fs/cgroup"
	}
	return &cgroupMetricsCollector{
		cgroupRoot: cgroupRoot,
		logger:     logger,
		last:       map[string]cpuStat{},
		podToPaths: map[string]podCgroupPaths{},
		cacheTTL:   30 * time.Second,
	}
}

func (c *cgroupMetricsCollector) CollectPodMetrics(ctx context.Context, pods []*corev1.Pod) (map[string]kube.PodUsage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.refreshCgroupCache(pods); err != nil {
		return nil, err
	}

	result := make(map[string]kube.PodUsage, len(pods))
	now := time.Now()

	for podKey, paths := range c.podToPaths {
		usage := result[podKey]
		stats, err := readCPUStat(c.logger, paths.cpuAcct, paths.cpu)
		if err == nil {
			if !c.at.IsZero() {
				if prev, ok := c.last[podKey]; ok {
					deltaNs := diffUint64(stats.UsageNs, prev.UsageNs)
					dtNs := now.Sub(c.at).Nanoseconds()

					if dtNs > 0 {
						usage.CPUUsageMilli = int64(math.Round((float64(deltaNs) / float64(dtNs)) * 1000))
						if usage.CPUUsageMilli < 0 {
							usage.CPUUsageMilli = 0
						}
					}
				}
			}
			c.last[podKey] = stats
		} else if c.logger != nil {
			c.logger.Debug("read cpu.stat failed", slog.String("pod", podKey), slog.String("error", err.Error()))
		}

		rss, err := readCgroupMemory(c.logger, paths.mem)
		if err == nil {
			usage.MemoryRSS = rss
		}

		result[podKey] = usage
	}

	c.at = now
	return result, nil
}

func (c *cgroupMetricsCollector) refreshCgroupCache(pods []*corev1.Pod) error {
	now := time.Now()
	if now.Sub(c.cacheAt) < c.cacheTTL && len(c.podToPaths) > 0 {
		return nil
	}

	layout, err := detectCgroupLayout(c.cgroupRoot)
	if err != nil && c.logger != nil {
		c.logger.Warn("cgroup layout detection incomplete; metrics may be partial", slog.String("error", err.Error()))
	}
	if c.logger != nil && !c.layoutLogged {
		if layout.unified {
			c.logger.Info("detected cgroup v2 (unified) layout for metrics", slog.String("root", layout.root))
		} else {
			c.logger.Info("detected cgroup v1 layout for metrics",
				slog.String("cpuacct", layout.cpuAcctRoot),
				slog.String("cpu", layout.cpuRoot),
				slog.String("memory", layout.memRoot),
			)
		}
		c.layoutLogged = true
	}

	podToPaths := map[string]podCgroupPaths{}
	if layout.unified {
		if layout.root == "" {
			return fmt.Errorf("cgroup v2 root not found under %s", c.cgroupRoot)
		}
		podToPath, err := c.mapPodCgroups(layout.root, pods)
		if err != nil {
			return err
		}
		for podKey, path := range podToPath {
			podToPaths[podKey] = podCgroupPaths{
				cpuAcct: path,
				cpu:     path,
				mem:     path,
			}
		}
	} else {
		var cpuAcctPods map[string]string
		var cpuPods map[string]string
		var memPods map[string]string

		if layout.cpuAcctRoot != "" {
			cpuAcctPods, err = c.mapPodCgroups(layout.cpuAcctRoot, pods)
			if err != nil {
				return err
			}
		}
		if layout.cpuRoot != "" {
			cpuPods, err = c.mapPodCgroups(layout.cpuRoot, pods)
			if err != nil {
				return err
			}
		}
		if layout.memRoot != "" {
			memPods, err = c.mapPodCgroups(layout.memRoot, pods)
			if err != nil {
				return err
			}
		}

		for podKey, cpuPath := range cpuAcctPods {
			paths := podToPaths[podKey]
			paths.cpuAcct = cpuPath
			podToPaths[podKey] = paths
		}
		for podKey, cpuPath := range cpuPods {
			paths := podToPaths[podKey]
			paths.cpu = cpuPath
			podToPaths[podKey] = paths
		}
		for podKey, memPath := range memPods {
			paths := podToPaths[podKey]
			paths.mem = memPath
			podToPaths[podKey] = paths
		}
	}

	c.podToPaths = podToPaths
	c.cacheAt = now
	return nil
}

func readCPUStat(logger *slog.Logger, cpuAcctPath, cpuPath string) (cpuStat, error) {
	if cpuAcctPath == "" {
		cpuAcctPath = cpuPath
	}
	if cpuPath == "" {
		cpuPath = cpuAcctPath
	}
	if stat, err := readCPUStatV2(logger, cpuPath); err == nil {
		return stat, nil
	}
	return readCPUStatV1(logger, cpuAcctPath, cpuPath)
}

func readCgroupMemory(logger *slog.Logger, memPath string) (uint64, error) {
	if memPath == "" {
		return 0, fmt.Errorf("memory cgroup path is empty")
	}
	path := filepath.Join(memPath, "memory.current")
	val, err := readUintFromFile(path)
	if err == nil {
		if logger != nil && logger.Enabled(context.Background(), slog.LevelDebug) {
			logger.Debug("read memory.current", slog.String("path", path), slog.Uint64("val", val))
		}
		return val, nil
	}

	path = filepath.Join(memPath, "memory.usage_in_bytes")
	val, err = readUintFromFile(path)
	if logger != nil && logger.Enabled(context.Background(), slog.LevelDebug) {
		logger.Debug("read memory.usage_in_bytes", slog.String("path", path), slog.Uint64("val", val), slog.Any("error", err))
	}
	return val, err
}

func diffUint64(current, prev uint64) uint64 {
	if current <= prev {
		return 0
	}
	return current - prev
}

func readCPUStatV2(logger *slog.Logger, cgroupPath string) (cpuStat, error) {
	if cgroupPath == "" {
		return cpuStat{}, fmt.Errorf("cpu cgroup path is empty")
	}
	path := filepath.Join(cgroupPath, "cpu.stat")
	data, err := os.ReadFile(path)
	if err != nil {
		return cpuStat{}, err
	}

	if logger != nil && logger.Enabled(context.Background(), slog.LevelDebug) {
		logger.Debug("raw cpu.stat", slog.String("path", path), slog.String("content", string(data)))
	}

	var out cpuStat
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		val, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch fields[0] {
		case "usage_usec":
			out.UsageNs = val * 1000
		case "user_usec":
			out.UserNs = val * 1000
		case "system_usec":
			out.SystemNs = val * 1000
		case "throttled_usec":
			out.ThrottledNs = val * 1000
		}
	}
	if out.UsageNs == 0 && out.UserNs == 0 && out.SystemNs == 0 {
		return cpuStat{}, fmt.Errorf("cpu.stat missing usage fields at %s", cgroupPath)
	}
	if out.UsageNs == 0 {
		out.UsageNs = out.UserNs + out.SystemNs
	}
	return out, nil
}

func readCPUStatV1(logger *slog.Logger, cpuAcctPath, cpuPath string) (cpuStat, error) {
	var out cpuStat

	if cpuAcctPath == "" {
		return cpuStat{}, fmt.Errorf("cpuacct cgroup path is empty")
	}
	usageNs, err := readUintFromFile(filepath.Join(cpuAcctPath, "cpuacct.usage"))
	if err != nil {
		return cpuStat{}, err
	}
	out.UsageNs = usageNs

	if ticks, err := readCPUAcctStat(filepath.Join(cpuAcctPath, "cpuacct.stat")); err == nil {
		hz := uint64(100) // USER_HZ is typically 100 on modern Linux; avoids CGO/sysconf dependency.
		out.UserNs = ticks.user * 1_000_000_000 / hz
		out.SystemNs = ticks.system * 1_000_000_000 / hz
	}

	if cpuPath != "" {
		if throttled, err := readThrottledTime(filepath.Join(cpuPath, "cpu.stat")); err == nil {
			out.ThrottledNs = throttled
		}
	} else if throttled, err := readThrottledTime(filepath.Join(cpuAcctPath, "cpu.stat")); err == nil {
		out.ThrottledNs = throttled
	}

	return out, nil
}

type cpuTicks struct {
	user   uint64
	system uint64
}

func readCPUAcctStat(path string) (cpuTicks, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return cpuTicks{}, err
	}
	var out cpuTicks
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		val, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch fields[0] {
		case "user":
			out.user = val
		case "system":
			out.system = val
		}
	}
	if out.user == 0 && out.system == 0 {
		return cpuTicks{}, fmt.Errorf("cpuacct.stat missing user/system")
	}
	return out, nil
}

func readThrottledTime(path string) (uint64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		if fields[0] != "throttled_time" {
			continue
		}
		val, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0, err
		}
		return val, nil
	}
	return 0, fmt.Errorf("cpu.stat missing throttled_time")
}

func readUintFromFile(path string) (uint64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	val := strings.TrimSpace(string(data))
	return strconv.ParseUint(val, 10, 64)
}

type cgroupLayout struct {
	unified     bool
	root        string
	cpuAcctRoot string
	cpuRoot     string
	memRoot     string
}

func detectCgroupLayout(cgroupRoot string) (cgroupLayout, error) {
	if cgroupRoot == "" {
		cgroupRoot = "/sys/fs/cgroup"
	}
	if _, err := os.Stat(filepath.Join(cgroupRoot, "cgroup.controllers")); err == nil {
		return cgroupLayout{unified: true, root: cgroupRoot}, nil
	}

	layout := cgroupLayout{unified: false}
	layout.cpuAcctRoot = firstExisting(cgroupRoot, []string{"cpu,cpuacct", "cpuacct"})
	layout.cpuRoot = firstExisting(cgroupRoot, []string{"cpu,cpuacct", "cpu"})
	layout.memRoot = firstExisting(cgroupRoot, []string{"memory"})

	if layout.cpuAcctRoot == "" || layout.cpuRoot == "" || layout.memRoot == "" {
		return layout, fmt.Errorf("cgroup v1 controllers missing under %s", cgroupRoot)
	}
	return layout, nil
}

func firstExisting(root string, candidates []string) string {
	for _, name := range candidates {
		path := filepath.Join(root, name)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

// mapPodCgroups walks the cgroup hierarchy and identifies the root cgroup path for each pod.
func (c *cgroupMetricsCollector) mapPodCgroups(cgroupRoot string, pods []*corev1.Pod) (map[string]string, error) {
	// maintain a map of "token" -> podKey
	// we now track TWO tokens per pod:
	// 1. underscore-replaced (standard k8s/systemd behavior)
	// 2. original dash-based (some container runtimes/configs)

	type podTarget struct {
		podKey string
		uid    string
	}

	tokenToTarget := make(map[string]podTarget)

	for _, pod := range pods {
		if pod == nil || pod.UID == "" {
			continue
		}
		uid := string(pod.UID)
		podKey := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)

		// 1. Underscore format (most common)
		underscoreToken := strings.ReplaceAll(uid, "-", "_")
		tokenToTarget[underscoreToken] = podTarget{podKey: podKey, uid: uid}

		// 2. Dash format (fallback)
		if uid != underscoreToken {
			tokenToTarget[uid] = podTarget{podKey: podKey, uid: uid}
		}
	}

	podToPath := make(map[string]string)

	if len(tokenToTarget) == 0 {
		return podToPath, nil
	}

	start := time.Now()
	foundCount := 0

	err := filepath.WalkDir(cgroupRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !entry.IsDir() {
			return nil
		}

		dirName := entry.Name()

		// Optimization: Check if directory name contains ANY known token fragment?
		// For now, we iterate tokens. Since we have ~100 pods max usually, 200 tokens is fine.
		// If this is too slow, we can optimize later.

		for token, target := range tokenToTarget {
			// Check if we already found this pod? (Optional optimization, but maybe multiple paths exist?)
			// Let's stick to "first valid match" or overwrite?
			// The original code overwrote: podToPath[podKey] = path

			if strings.Contains(dirName, token) {
				// We definitely found a candidate.
				// Verify if it is "pod<token>" or just "<token>" or "kubepods...<token>"
				// The original check was stricter on path containing token too.

				podToPath[target.podKey] = path
			}
		}
		return nil
	})

	if err != nil {
		return nil, err
	}

	// Logging results for debugging
	foundCount = len(podToPath)
	duration := time.Since(start)

	if c.logger != nil {
		level := slog.LevelDebug
		if foundCount == 0 && len(pods) > 0 {
			// If we expected pods but found NONE, this is critical info
			level = slog.LevelInfo
		}

		c.logger.Log(context.Background(), level, "mapped pod cgroups",
			slog.Int("pods_input", len(pods)),
			slog.Int("pods_found", foundCount),
			slog.Int("tokens_tracked", len(tokenToTarget)),
			slog.Duration("duration", duration),
		)

		// If verbose, log missing pods
		if foundCount < len(pods) && c.logger.Enabled(context.Background(), slog.LevelDebug) {
			for _, pod := range pods {
				key := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
				if _, ok := podToPath[key]; !ok {
					c.logger.Debug("cgroup path not found for pod", slog.String("pod", key), slog.String("uid", string(pod.UID)))
				}
			}
		}
	}

	return podToPath, nil
}
