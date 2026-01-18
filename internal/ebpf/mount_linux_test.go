//go:build linux

package ebpf

import (
	"log/slog"
	"os"
	"testing"

	"github.com/cilium/ebpf"
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
	// Verify we can write to it (the ultimate robust check)
	// BPF FS only allows directories and BPF objects, NO regular files.
	// We try to pin a map to the root of the mount.

	// Create a minimal map spec
	spec := &ebpf.MapSpec{
		Type:       ebpf.Array,
		KeySize:    4,
		ValueSize:  4,
		MaxEntries: 1,
	}
	m, err := ebpf.NewMap(spec)
	if err != nil {
		t.Fatalf("Failed to create test map: %v", err)
	}
	defer m.Close()

	pinPath := "/sys/fs/bpf/test_verify_map"
	if err := m.Pin(pinPath); err != nil {
		t.Errorf("Failed to pin map to %s: %v", pinPath, err)
	} else {
		t.Logf("Successfully pinned map to %s", pinPath)
		_ = m.Unpin()
		_ = os.Remove(pinPath)
	}
}
