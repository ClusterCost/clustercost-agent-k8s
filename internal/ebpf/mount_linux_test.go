//go:build linux

package ebpf

import (
	"log/slog"
	"os"
	"testing"
)

// TestMountBPFFS verifies that we can mount the BPF filesystem.
// This test is skipped if not running on Linux or without root privileges.
func TestMountBPFFS(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("Skipping test: requires root privileges")
	}

	// Create a dummy logger for the test
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// The function we want to test is unexported, but we are in the same package (ebpf).
	if err := mountBPFFS(logger); err != nil {
		t.Fatalf("mountBPFFS failed: %v", err)
	}

	// Verify it exists
	if _, err := os.Stat("/sys/fs/bpf"); err != nil {
		t.Errorf("/sys/fs/bpf does not exist after mount: %v", err)
	}

	// Verify we can write to it (the ultimate robust check)
	// BPF FS only allows directories and BPF objects, NO regular files.
	// So we test by creating a directory.
	testDir, err := os.MkdirTemp("/sys/fs/bpf", "test_write_")
	if err != nil {
		t.Errorf("Failed to create dir in /sys/fs/bpf: %v", err)
	} else {
		t.Logf("Successfully created dir %s", testDir)
		_ = os.Remove(testDir)
	}
}
