package main

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestFilterNodes(t *testing.T) {
	nodes := []*corev1.Node{
		{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "node-b"}},
	}
	filtered := filterNodes(nodes, "node-b")
	if len(filtered) != 1 || filtered[0].Name != "node-b" {
		t.Fatalf("unexpected node filter result: %+v", filtered)
	}
}

func TestFilterPods(t *testing.T) {
	pods := []*corev1.Pod{
		{ObjectMeta: metav1.ObjectMeta{Name: "p1"}, Spec: corev1.PodSpec{NodeName: "node-a"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "p2"}, Spec: corev1.PodSpec{NodeName: "node-b"}},
	}
	filtered := filterPods(pods, "node-a")
	if len(filtered) != 1 || filtered[0].Name != "p1" {
		t.Fatalf("unexpected pod filter result: %+v", filtered)
	}
}
