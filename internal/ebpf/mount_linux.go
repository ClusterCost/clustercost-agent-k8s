package ebpf

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// mountBPFFS ensures that /sys/fs/bpf is mounted as a BPF filesystem.
func mountBPFFS() error {
	const bpfPath = "/sys/fs/bpf"

	// Check if directory exists, create if not
	if err := os.MkdirAll(bpfPath, 0o750); err != nil {
		return fmt.Errorf("create bpf mount point: %w", err)
	}

	// Check if already mounted
	var stat unix.Statfs_t
	if err := unix.Statfs(bpfPath, &stat); err != nil {
		return fmt.Errorf("statfs %s: %w", bpfPath, err)
	}

	// Magic number for BPF_FS is 0xCAFE4A11
	const bpfFsMagic = 0xCAFE4A11
	if stat.Type == bpfFsMagic {
		return nil // Already mounted
	}

	// Mount it
	// mount(source, target, fstype, flags, data)
	if err := unix.Mount("bpf", bpfPath, "bpf", 0, "mode=0700"); err != nil {
		return fmt.Errorf("mount bpffs: %w", err)
	}

	return nil
}
