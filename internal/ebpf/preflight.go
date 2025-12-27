package ebpf

import (
	"fmt"
	"os"
	"path/filepath"

	"clustercost-agent-k8s/internal/config"

	"github.com/cilium/ebpf/rlimit"
)

// PreflightIssue describes a single eBPF readiness problem.
type PreflightIssue struct {
	Component string
	Message   string
}

// PreflightReport holds the detected issues.
type PreflightReport struct {
	Issues []PreflightIssue
}

// HasErrors reports whether any preflight issues were found.
func (r PreflightReport) HasErrors() bool {
	return len(r.Issues) > 0
}

// Preflight validates the runtime prerequisites for eBPF collection.
func Preflight(cfg config.Config) PreflightReport {
	report := PreflightReport{}

	if err := rlimit.RemoveMemlock(); err != nil {
		report.Issues = append(report.Issues, PreflightIssue{
			Component: "memlock",
			Message:   fmt.Sprintf("raise memlock rlimit failed: %v", err),
		})
	}

	if cfg.Metrics.Enabled {
		checkBTF(&report)
		checkPathReadable(&report, "metrics cgroup", cfg.Metrics.CgroupPath, "/sys/fs/cgroup")
		checkPinDirWritable(&report, "metrics map", cfg.Metrics.BPFMapPath)
	}
	if cfg.Network.Enabled {
		checkBTF(&report)
		checkPathReadable(&report, "network cgroup", cfg.Network.CgroupPath, "/sys/fs/cgroup")
		checkPinDirWritable(&report, "network map", cfg.Network.BPFMapPath)
	}

	return report
}

func checkBTF(report *PreflightReport) {
	if _, err := os.Stat("/sys/kernel/btf/vmlinux"); err != nil {
		report.Issues = append(report.Issues, PreflightIssue{
			Component: "btf",
			Message:   fmt.Sprintf("missing /sys/kernel/btf/vmlinux: %v", err),
		})
	}
}

func checkPathReadable(report *PreflightReport, label, path, fallback string) {
	if path == "" {
		path = fallback
	}
	if _, err := os.Stat(path); err != nil {
		report.Issues = append(report.Issues, PreflightIssue{
			Component: label,
			Message:   fmt.Sprintf("path not found: %s: %v", path, err),
		})
	}
}

func checkPinDirWritable(report *PreflightReport, label, pinPath string) {
	if pinPath == "" {
		return
	}
	dir := filepath.Dir(pinPath)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		report.Issues = append(report.Issues, PreflightIssue{
			Component: label,
			Message:   fmt.Sprintf("create pin dir failed: %s: %v", dir, err),
		})
		return
	}

	tmp, err := os.CreateTemp(dir, ".clustercost-ebpf-*")
	if err != nil {
		report.Issues = append(report.Issues, PreflightIssue{
			Component: label,
			Message:   fmt.Sprintf("pin dir not writable: %s: %v", dir, err),
		})
		return
	}
	if err := tmp.Close(); err != nil {
		report.Issues = append(report.Issues, PreflightIssue{
			Component: label,
			Message:   fmt.Sprintf("pin dir temp close failed: %s: %v", dir, err),
		})
	}
	_ = os.Remove(tmp.Name())
}
