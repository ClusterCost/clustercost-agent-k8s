//go:build linux

package ebpf

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/cilium/ebpf"
	"golang.org/x/sys/unix"
)

// mountBPFFS ensures that /sys/fs/bpf is mounted as a BPF filesystem and is writable.
func mountBPFFS(logger *slog.Logger) error {
	const bpfPath = "/sys/fs/bpf"

	// 1. Ensure directory exists
	if err := os.MkdirAll(bpfPath, 0o755); err != nil {
		return fmt.Errorf("create bpf mount point: %w", err)
	}

	// Helper to attempt mounting
	mount := func() error {
		// Use mode=0755 (rwxr-xr-x) for maximum compatibility
		return unix.Mount("bpf", bpfPath, "bpf", 0, "mode=0755")
	}

	// Log access issues for debugging
	checkAccess := func(stage string) {
		err := unix.Access(bpfPath, unix.W_OK)
		if err != nil {
			logger.Debug("BPF directory not writable", slog.String("stage", stage), slog.String("error", err.Error()))
		} else {
			logger.Debug("BPF directory is writable", slog.String("stage", stage))
		}
	}

	checkAccess("pre-mount")

	// 2. Initial Checks
	// First, check if map pinning works (functional test).
	if err := verifyBPFPinning(bpfPath); err == nil {
		logger.Info("BPF filesystem is available and writable (verified via map pinning)")
		return nil
	} else {
		logger.Debug("initial pinning verification failed", slog.String("error", err.Error()))
	}

	// 3. Determine Mount State
	var stat unix.Statfs_t
	if err := unix.Statfs(bpfPath, &stat); err != nil {
		logger.Warn("statfs failed", slog.String("error", err.Error()))
	}

	const bpfFsMagic = 0xCAFE4A11
	isMounted := stat.Type == bpfFsMagic

	if isMounted {
		logger.Info("BPF filesystem detected but verification failed; attempting REMOUNT RW")
		// Try remounting
		if err := unix.Mount("bpf", bpfPath, "bpf", unix.MS_REMOUNT, "mode=0755"); err != nil {
			logger.Warn("remount failed; trying force unmount", slog.String("error", err.Error()))
			// Force unmount if remount fails
			if err := unix.Unmount(bpfPath, unix.MNT_FORCE|unix.MNT_DETACH); err != nil {
				logger.Warn("force unmount failed", slog.String("error", err.Error()))
			}
			isMounted = false
		} else {
			checkAccess("post-remount")
		}
	}

	if !isMounted {
		logger.Info("mounting BPF filesystem...")
		if err := mount(); err != nil {
			return fmt.Errorf("mount bpffs: %w", err)
		}
		checkAccess("post-mount")
	}

	// 4. Final Verification
	if err := verifyBPFPinning(bpfPath); err != nil {
		logger.Warn("verification failed after mount; trying hard reset (unmount+mount)", slog.String("error", err.Error()))

		// Hard Reset
		if err := unix.Unmount(bpfPath, unix.MNT_FORCE|unix.MNT_DETACH); err != nil {
			logger.Warn("hard reset: unmount failed", slog.String("error", err.Error()))
		}

		if err := mount(); err != nil {
			return fmt.Errorf("hard reset mount failed: %w", err)
		}
		checkAccess("post-hard-reset")

		// One last check
		if err := verifyBPFPinning(bpfPath); err != nil {
			return fmt.Errorf("BPF filesystem still not writable after hard reset: %w", err)
		}
	}

	logger.Info("BPF filesystem mounted and writable")
	return nil
}

// verifyBPFPinning verifies write access by creating and pinning a temporary BPF map.
// This is the most reliable test as it uses actual BPF syscalls.
func verifyBPFPinning(basePath string) error {
	// Create a minimal map
	spec := &ebpf.MapSpec{
		Type:       ebpf.Array,
		KeySize:    4,
		ValueSize:  4,
		MaxEntries: 1,
	}
	m, err := ebpf.NewMap(spec)
	if err != nil {
		// Differentiate between "can't create map" (likely capability issue)
		// and "can't pin map" (likely FS permission/mount issue)
		return fmt.Errorf("verifyBPFPinning: map creation failed (missing CAP_BPF/CAP_SYS_ADMIN?): %w", err)
	}
	defer m.Close()

	// Pin directly to root to avoid mkdir permission issues in subdirs
	pinName := fmt.Sprintf("clustercost-check-map-%d", os.Getpid())
	pinPath := filepath.Join(basePath, pinName)

	// Ensure clean slate
	_ = os.Remove(pinPath)

	if err := m.Pin(pinPath); err != nil {
		return fmt.Errorf("verifyBPFPinning: pin failed (readonly fs?): %w", err)
	}

	// Success! Clean up.
	_ = m.Unpin()
	_ = os.Remove(pinPath)

	return nil
}
