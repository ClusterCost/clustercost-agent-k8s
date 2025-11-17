package kube

import (
	"context"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes/fake"
)

func TestClusterCacheStartCancelledContext(t *testing.T) {
	client := fake.NewSimpleClientset()
	cache := NewClusterCache(client, 0)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := cache.Start(ctx); err == nil {
		t.Fatalf("expected error when context cancelled before start")
	}
}

func TestClusterCacheStartSuccess(t *testing.T) {
	client := fake.NewSimpleClientset()
	cache := NewClusterCache(client, 0)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		if err := cache.Start(ctx); err != nil {
			t.Errorf("start error: %v", err)
		}
		close(done)
	}()

	select {
	case <-done:
		// success
	case <-time.After(2 * time.Second):
		t.Fatal("cache start timeout")
	}
}
