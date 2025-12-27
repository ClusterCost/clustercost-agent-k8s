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

func TestMapPodCgroupIDs(t *testing.T) {
	root := t.TempDir()
	uid := "11111111-2222-3333-4444-555555555555"
	uidToken := "pod" + "11111111_2222_3333_4444_555555555555"
	path := filepath.Join(root, "kubepods.slice", "kubepods-burstable.slice", "kubepods-burstable-"+uidToken+".slice")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "api-0",
			Namespace: "payments",
			UID:       types.UID(uid),
		},
	}

	cgroups, err := mapPodCgroupIDs(root, []*corev1.Pod{pod})
	if err != nil {
		t.Fatalf("mapPodCgroupIDs: %v", err)
	}
	if len(cgroups) != 1 {
		t.Fatalf("expected 1 cgroup mapping, got %d", len(cgroups))
	}
	if cgroups["payments/api-0"] == 0 {
		t.Fatalf("expected non-zero inode")
	}
}
