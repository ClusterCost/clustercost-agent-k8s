//go:build linux

package ebpf

import (
	"fmt"
	"log/slog"
	"os"

	"golang.org/x/sys/unix"
)

// mountBPFFS ensures that /sys/fs/bpf is mounted as a BPF filesystem and is writable.
func mountBPFFS(logger *slog.Logger) error {
	const bpfPath = "/sys/fs/bpf"

	// 1. Ensure directory exists
	// Using 0750 to keep gosec happy and be secure
	if err := os.MkdirAll(bpfPath, 0o750); err != nil {
		return fmt.Errorf("create bpf mount point: %w", err)
	}

	// Helper to attempt mounting
	mount := func() error {
		// Use mode=0700 (standard for BPF FS)
		// 0700 = rwx------ (only root can access)
		return unix.Mount("bpf", bpfPath, "bpf", 0, "mode=0700")
	}

	// 2. Initial healthy check using Directory creation (not file!)
	if err := checkWriteAccess(bpfPath); err == nil {
		logger.Info("BPF filesystem is already writable")
		return nil
	}

	// 3. Determine Mount State
	var stat unix.Statfs_t
	if err := unix.Statfs(bpfPath, &stat); err != nil {
		logger.Warn("statfs failed", slog.String("error", err.Error()))
	}

	const bpfFsMagic = 0xCAFE4A11
	isMounted := stat.Type == bpfFsMagic

	if isMounted {
		logger.Info("BPF filesystem detected but not writable; attempting REMOUNT RW")
		// Try remounting
		if err := unix.Mount("bpf", bpfPath, "bpf", unix.MS_REMOUNT, "mode=0700"); err != nil {
			logger.Warn("remount failed; trying force unmount", slog.String("error", err.Error()))
			// Force unmount if remount fails
			_ = unix.Unmount(bpfPath, unix.MNT_FORCE|unix.MNT_DETACH)
			isMounted = false
		}
	}

	if !isMounted {
		logger.Info("mounting BPF filesystem...")
		if err := mount(); err != nil {
			return fmt.Errorf("mount bpffs: %w", err)
		}
	}

	// 4. Final Verification
	if err := checkWriteAccess(bpfPath); err != nil {
		logger.Warn("write check failed after mount; trying hard reset (unmount+mount)", slog.String("error", err.Error()))

		// Hard Reset
		_ = unix.Unmount(bpfPath, unix.MNT_FORCE|unix.MNT_DETACH)
		if err := mount(); err != nil {
			return fmt.Errorf("hard reset mount failed: %w", err)
		}

		// One last check
		if err := checkWriteAccess(bpfPath); err != nil {
			return fmt.Errorf("BPF filesystem still not writable after hard reset: %w", err)
		}
	}

	logger.Info("BPF filesystem mounted and writable")
	return nil
}

// checkWriteAccess tries to create and remove a temp dir to verify RW access.
// BPF FS only supports directories and BPF objects, regular files will fail!
func checkWriteAccess(path string) error {
	d, err := os.MkdirTemp(path, ".clustercost-check-")
	if err != nil {
		return err
	}
	return os.Remove(d)
}
