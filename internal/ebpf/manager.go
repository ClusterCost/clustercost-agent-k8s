package ebpf

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"clustercost-agent-k8s/internal/config"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/rlimit"
)

// Manager keeps eBPF programs and links alive for the agent.
type Manager struct {
	logger *slog.Logger
	links  []link.Link
	objs   []*ebpf.Collection
}

// Start loads and attaches eBPF programs based on configuration.
func Start(cfg config.Config, logger *slog.Logger) (*Manager, error) {
	mgr := &Manager{logger: logger}
	if err := rlimit.RemoveMemlock(); err != nil {
		return nil, fmt.Errorf("raise memlock rlimit: %w", err)
	}

	if cfg.Metrics.Enabled {
		if err := mgr.loadMetrics(cfg.Metrics); err != nil {
			mgr.Close()
			return nil, err
		}
	}
	if cfg.Network.Enabled {
		if err := mgr.loadNetwork(cfg.Network); err != nil {
			mgr.Close()
			return nil, err
		}
	}

	return mgr, nil
}

// Close releases eBPF resources.
func (m *Manager) Close() {
	for _, l := range m.links {
		_ = l.Close()
	}
	for _, obj := range m.objs {
		obj.Close()
	}
}

func (m *Manager) loadMetrics(cfg config.MetricsConfig) error {
	spec, err := ebpf.LoadCollectionSpec(cfg.ObjectPath)
	if err != nil {
		return fmt.Errorf("load metrics eBPF object: %w", err)
	}
	collection, err := ebpf.NewCollection(spec)
	if err != nil {
		return fmt.Errorf("create metrics collection: %w", err)
	}
	m.objs = append(m.objs, collection)
	if cfg.BPFMapPath != "" {
		if mp := collection.Maps["clustercost_metrics"]; mp != nil {
			if err := os.MkdirAll(filepath.Dir(cfg.BPFMapPath), 0o750); err != nil {
				return fmt.Errorf("create metrics map dir: %w", err)
			}
			if _, err := os.Stat(cfg.BPFMapPath); err == nil {
				_ = os.Remove(cfg.BPFMapPath)
			}
			if err := mp.Pin(cfg.BPFMapPath); err != nil {
				return fmt.Errorf("pin metrics map: %w", err)
			}
		}
	}

	cgroupPath := cfg.CgroupPath
	if cgroupPath == "" {
		cgroupPath = "/sys/fs/cgroup"
	}
	if _, err := os.Stat(cgroupPath); err != nil {
		return fmt.Errorf("metrics cgroup path not found: %w", err)
	}

	schedProg := collection.Programs["handle_sched_switch"]
	if schedProg == nil {
		m.logger.Warn("missing metrics program handle_sched_switch; cpu metrics will be unavailable")
	} else {
		linkSched, err := link.Tracepoint("sched", "sched_switch", schedProg, nil)
		if err != nil {
			m.logger.Warn("optional tracepoint not found; cpu metrics will be unavailable",
				slog.String("tracepoint", "sched_switch"),
				slog.String("error", err.Error()))
		} else {
			m.links = append(m.links, linkSched)
		}
	}

	allocProg := collection.Programs["handle_mm_page_alloc"]
	if allocProg != nil {
		linkAlloc, err := link.Tracepoint("mm", "mm_page_alloc", allocProg, nil)
		if err != nil {
			m.logger.Warn("optional tracepoint not found; memory metrics may be incomplete",
				slog.String("tracepoint", "mm_page_alloc"),
				slog.String("error", err.Error()))
		} else {
			m.links = append(m.links, linkAlloc)
		}
	}

	freeProg := collection.Programs["handle_mm_page_free"]
	if freeProg != nil {
		linkFree, err := link.Tracepoint("mm", "mm_page_free", freeProg, nil)
		if err != nil {
			m.logger.Warn("optional tracepoint not found; memory metrics may be incomplete",
				slog.String("tracepoint", "mm_page_free"),
				slog.String("error", err.Error()))
		} else {
			m.links = append(m.links, linkFree)
		}
	}

	m.logger.Info("loaded eBPF metrics programs", slog.String("object", cfg.ObjectPath))
	return nil
}

func (m *Manager) loadNetwork(cfg config.NetworkConfig) error {
	spec, err := ebpf.LoadCollectionSpec(cfg.ObjectPath)
	if err != nil {
		return fmt.Errorf("load network eBPF object: %w", err)
	}
	collection, err := ebpf.NewCollection(spec)
	if err != nil {
		return fmt.Errorf("create network collection: %w", err)
	}
	m.objs = append(m.objs, collection)
	if cfg.BPFMapPath != "" {
		if mp := collection.Maps["clustercost_flows"]; mp != nil {
			if err := os.MkdirAll(filepath.Dir(cfg.BPFMapPath), 0o750); err != nil {
				return fmt.Errorf("create network map dir: %w", err)
			}
			if _, err := os.Stat(cfg.BPFMapPath); err == nil {
				_ = os.Remove(cfg.BPFMapPath)
			}
			if err := mp.Pin(cfg.BPFMapPath); err != nil {
				return fmt.Errorf("pin network map: %w", err)
			}
		}
	}

	cgroupPath := cfg.CgroupPath
	if cgroupPath == "" {
		cgroupPath = "/sys/fs/cgroup"
	}
	cgroup, err := os.Open(cgroupPath) // #nosec G304 -- path is provided by operator configuration
	if err != nil {
		m.logger.Warn("open cgroup path failed; network metrics unavailable", slog.String("error", err.Error()))
		return nil // Don't crash, just skip network
	}
	defer func() {
		if err := cgroup.Close(); err != nil {
			m.logger.Warn("close cgroup handle failed", slog.String("error", err.Error()))
		}
	}()

	ingressProg := collection.Programs["handle_cgroup_ingress"]
	if ingressProg == nil {
		m.logger.Warn("missing network program handle_cgroup_ingress")
	} else {
		linkIngress, err := link.AttachCgroup(link.CgroupOptions{
			Path:    cgroupPath,
			Attach:  ebpf.AttachCGroupInetIngress,
			Program: ingressProg,
		})
		if err != nil {
			m.logger.Warn("attach cgroup ingress failed; network metrics may be incomplete",
				slog.String("error", err.Error()))
		} else {
			m.links = append(m.links, linkIngress)
		}
	}

	egressProg := collection.Programs["handle_cgroup_egress"]
	if egressProg == nil {
		m.logger.Warn("missing network program handle_cgroup_egress")
	} else {
		linkEgress, err := link.AttachCgroup(link.CgroupOptions{
			Path:    cgroupPath,
			Attach:  ebpf.AttachCGroupInetEgress,
			Program: egressProg,
		})
		if err != nil {
			m.logger.Warn("attach cgroup egress failed; network metrics may be incomplete",
				slog.String("error", err.Error()))
		} else {
			m.links = append(m.links, linkEgress)
		}
	}

	m.logger.Info("loaded eBPF network programs", slog.String("object", cfg.ObjectPath))
	return nil
}
