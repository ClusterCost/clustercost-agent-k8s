package collector

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadProcMemInfo(t *testing.T) {
	// Create a temporary file mimicking /proc/meminfo
	tmpDir := t.TempDir()
	memInfoFile := filepath.Join(tmpDir, "meminfo")

	content := `MemTotal:       16306544 kB
MemFree:         4354264 kB
MemAvailable:    8992348 kB
Buffers:          345124 kB
Cached:          4567232 kB
SwapCached:            0 kB
`
	if err := os.WriteFile(memInfoFile, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write meminfo mock: %v", err)
	}

	// Test Case 1: Standard MemAvailable present
	usage, err := readProcMemInfo(memInfoFile)
	if err != nil {
		t.Fatalf("readProcMemInfo failed: %v", err)
	}

	// Expected: Total (16306544) - Available (8992348) = 7314196 kB
	// In bytes: 7314196 * 1024 = 7,489,736,704
	expectedKB := int64(16306544 - 8992348)
	expectedBytes := expectedKB * 1024

	if usage != expectedBytes {
		t.Errorf("expected usage %d bytes, got %d", expectedBytes, usage)
	}

	// Test Case 2: No MemAvailable (older kernels)
	oldContent := `MemTotal:       16306544 kB
MemFree:         4354264 kB
Buffers:          345124 kB
Cached:          4567232 kB
`
	memInfoFileOld := filepath.Join(tmpDir, "meminfo_old")
	if err := os.WriteFile(memInfoFileOld, []byte(oldContent), 0o644); err != nil {
		t.Fatalf("failed to write meminfo old mock: %v", err)
	}

	usageOld, err := readProcMemInfo(memInfoFileOld)
	if err != nil {
		t.Fatalf("readProcMemInfo (old) failed: %v", err)
	}

	// Expected: Total - Free - Buffers - Cached
	// 16306544 - 4354264 - 345124 - 4567232 = 7039924 kB
	// 7039924 * 1024 = 7,208,882,176
	expectedKBOld := int64(16306544 - 4354264 - 345124 - 4567232)
	expectedBytesOld := expectedKBOld * 1024

	if usageOld != expectedBytesOld {
		t.Errorf("expected usage (old) %d bytes, got %d", expectedBytesOld, usageOld)
	}
}
