//go:build !linux

package ebpf

import (
	"log/slog"
)

// mountBPFFS is a no-op on non-Linux systems check.
func mountBPFFS(logger *slog.Logger) error {
	logger.Info("skipping BPF mount on non-Linux system")
	return nil
}
