package collector

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// readProcMemInfo parses /proc/meminfo to calculate used memory (Total - Available)
// This function is kept platform-independent for easier testing, although /proc/meminfo is Linux-specific.
func readProcMemInfo(path string) (int64, error) {
	data, err := os.ReadFile(path) // #nosec G304
	if err != nil {
		return 0, err
	}

	var memTotal, memAvailable, memFree, buffers, cached uint64
	var foundTotal, foundAvailable bool

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		key := strings.TrimSuffix(fields[0], ":")
		val, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		// /proc/meminfo values are in kB
		valBytes := val * 1024

		switch key {
		case "MemTotal":
			memTotal = valBytes
			foundTotal = true
		case "MemAvailable":
			memAvailable = valBytes
			foundAvailable = true
		case "MemFree":
			memFree = valBytes
		case "Buffers":
			buffers = valBytes
		case "Cached":
			cached = valBytes
		}
	}

	if !foundTotal {
		return 0, fmt.Errorf("MemTotal not found in %s", path)
	}

	// Ideally use MemAvailable (kernels 3.14+)
	if foundAvailable {
		// Used = Total - Available
		return int64(memTotal - memAvailable), nil
	}

	// Fallback for older kernels: Used = Total - Free - Buffers - Cached
	used := memTotal - memFree - buffers - cached
	return int64(used), nil
}
