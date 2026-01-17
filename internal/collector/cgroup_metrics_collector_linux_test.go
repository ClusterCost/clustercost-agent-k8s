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

func TestMapPodCgroups_WithChildren(t *testing.T) {
	root := t.TempDir()
	uid := "11111111-2222-3333-4444-555555555555"
	uidToken := "11111111_2222_3333_4444_555555555555"

	// Mock Hierarchy:
	// .../pod<UID>/ (Root)
	// .../pod<UID>/container1 (Child)
	// .../pod<UID>/container2 (Child)

	podDirName := "pod" + uidToken
	rootCgroupPath := filepath.Join(root, "kubepods.slice", "kubepods-burstable.slice", "kubepods-burstable-"+podDirName+".slice")
	if err := os.MkdirAll(rootCgroupPath, 0o755); err != nil {
		t.Fatalf("mkdir root: %v", err)
	}

	container1Path := filepath.Join(rootCgroupPath, "docker-container1.scope")
	if err := os.MkdirAll(container1Path, 0o755); err != nil {
		t.Fatalf("mkdir container1: %v", err)
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "api-0",
			Namespace: "payments",
			UID:       types.UID(uid),
		},
	}

	podToPath, err := mapPodCgroups(root, []*corev1.Pod{pod})
	if err != nil {
		t.Fatalf("mapPodCgroups: %v", err)
	}

	expectedKey := "payments/api-0"
	// Verify Path mapping (should point to Root)
	if path, ok := podToPath[expectedKey]; !ok {
		t.Errorf("expected pod path mapping for %s", expectedKey)
	} else if path != rootCgroupPath {
		t.Errorf("expected pod path %s, got %s", rootCgroupPath, path)
	}
}
