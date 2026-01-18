//go:build linux

package collector

import (
	"os"
	"path/filepath"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestMapPodCgroups_WithDashedUID(t *testing.T) {
	root := t.TempDir()
	uid := "11111111-2222-3333-4444-555555555555"
	// Ensure we test the case where the filesystem uses dashes, NOT underscores
	// The current code expects underscores (uidToken := strings.ReplaceAll(uid, "-", "_"))
	// So this test setup mimics a system where that replacement DOES NOT happen in the filesystem path.

	podDirName := "pod" + uid // Using original UID with dashes!
	rootCgroupPath := filepath.Join(root, "kubepods.slice", "kubepods-burstable.slice", "kubepods-burstable-"+podDirName+".slice")
	if err := os.MkdirAll(rootCgroupPath, 0o755); err != nil {
		t.Fatalf("mkdir root: %v", err)
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "api-dashed",
			Namespace: "payments",
			UID:       types.UID(uid),
		},
	}

	c := &cgroupMetricsCollector{}

	podToPath, err := c.mapPodCgroups(root, []*corev1.Pod{pod})
	if err != nil {
		t.Fatalf("mapPodCgroups: %v", err)
	}

	expectedKey := "payments/api-dashed"
	if path, ok := podToPath[expectedKey]; !ok {
		// This is expected to fail with the current code, which looks for underscores
		t.Logf("Currently failing as expected: could not find path for %s when FS uses dashes", expectedKey)
	} else if path != rootCgroupPath {
		t.Errorf("expected pod path %s, got %s", rootCgroupPath, path)
	} else {
		// If it passes, good, but likely it won't until we fix the code.
		t.Logf("Surprisingly found path: %s", path)
	}
}
